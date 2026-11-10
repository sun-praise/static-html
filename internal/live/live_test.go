package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/sun-praise/static-html/internal/analytics"
)

func TestInjectMiddlewareSkipsNonSessionPaths(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "WebSocket") {
		t.Fatal("non-session path should not have script injected")
	}
	if strings.Contains(body, "analysis.sun-praise.com") {
		t.Fatal("non-session path should not have analytics injected")
	}
	if body != `{"ok":true}` {
		t.Fatalf("body = %q, want unchanged JSON", body)
	}
}

func TestInjectMiddlewareAddsScriptToHTML(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><head><title>Test</title></head><body>Hello</body></html>`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/s/abc123/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "WebSocket") {
		t.Fatal("HTML response should contain WebSocket script")
	}
	if !strings.Contains(body, "<title>Test</title>") {
		t.Fatal("original content should be preserved")
	}
}

func TestInjectMiddlewareSkipsNonHTML(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`body { color: red; }`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/s/abc123/style.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "WebSocket") {
		t.Fatal("CSS response should not have script injected")
	}
	if strings.Contains(body, "analysis.sun-praise.com") {
		t.Fatal("CSS response should not have analytics injected")
	}
	if body != `body { color: red; }` {
		t.Fatalf("body = %q, want unchanged CSS", body)
	}
}

// TestInjectMiddlewareAddsAnalyticsOnPublicHost pins the analytics contract:
// /s/ preview HTML served for the public deployment carries the Umami tracker,
// including its data-domains reporting guard.
func TestInjectMiddlewareAddsAnalyticsOnPublicHost(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><head><title>Test</title></head><body>Hello</body></html>`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/s/abc123/", nil)
	req.Host = analytics.PublicHost
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		"analysis.sun-praise.com/script.js",
		`data-domains="` + analytics.PublicHost + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("public-host HTML response missing %q in body:\n%s", want, body)
		}
	}
}

// TestInjectMiddlewareOmitsAnalyticsOnOtherHosts pins the server-side host
// gate: any host other than the public deployment must not receive the
// tracker at all (intranet/local pages load no external script).
func TestInjectMiddlewareOmitsAnalyticsOnOtherHosts(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><head><title>Test</title></head><body>Hello</body></html>`))
	})

	for _, host := range []string{"192.168.2.15:3939", "localhost:3939", "example.com"} {
		handler := InjectMiddleware(inner)
		req := httptest.NewRequest(http.MethodGet, "/s/abc123/", nil)
		req.Host = host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if strings.Contains(rec.Body.String(), "analysis.sun-praise.com") {
			t.Fatalf("host %q must not receive the analytics script:\n%s", host, rec.Body.String())
		}
	}
}

func TestInjectMiddlewareSkipsWSPath(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
		w.Write([]byte("upgraded"))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/s/abc123/ws", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status = %d, want 101", rec.Code)
	}
}

func TestInjectMiddlewareAddsDrawerToHTML(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><head><title>Test</title></head><body>Hello</body></html>`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/s/abc123/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`id="sth-drawer-btn"`,
		`id="sth-drawer-panel"`,
		`id="sth-drawer-content"`,
		`id="sth-drawer-close"`,
		"/api/sessions/",
		"Back to Home",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("HTML response missing %q in body:\n%s", want, body)
		}
	}
}

func TestInjectMiddlewareSkipsDrawerForNonHTML(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`body { color: red; }`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/s/abc123/style.css", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "sth-drawer-btn") {
		t.Fatalf("CSS response should not contain the drawer button:\n%s", body)
	}
}

func TestInjectMiddlewareSkipsDrawerForNonSessionPath(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`<html><body>Home</body></html>`))
	})

	handler := InjectMiddleware(inner)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "sth-drawer-btn") {
		t.Fatalf("home page should not contain the drawer button:\n%s", body)
	}
}

func TestHubBroadcast(t *testing.T) {
	hub := NewHub(nil, nil)

	received := make(chan []byte, 1)
	c := &mockClient{send: func(data []byte) { received <- data }}
	hub.Register(c)

	hub.Broadcast(ReloadJSON())

	select {
	case data := <-received:
		var msg Message
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "reload" {
			t.Fatalf("type = %q, want reload", msg.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for broadcast")
	}
}

func TestHubUnregisterCallsOnEmpty(t *testing.T) {
	called := make(chan struct{}, 1)
	hub := NewHub(nil, func() { called <- struct{}{} })

	c := &mockClient{}
	hub.Register(c)
	hub.Unregister(c)

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("onEmpty not called")
	}
}

func TestWebSocketHandler(t *testing.T) {
	mgr := NewManager(nil)
	handler := HandleWebSocket(mgr, func(sessionID string) string {
		return "/tmp/test-session"
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/s/test-session/ws"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}

	// Wait for server goroutine to register the client
	for i := 0; i < 50; i++ {
		mgr.mu.Lock()
		entry, ok := mgr.hubs["test-session"]
		mgr.mu.Unlock()
		if ok && entry.hub.ClientCount() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mgr.mu.Lock()
	entry, ok := mgr.hubs["test-session"]
	mgr.mu.Unlock()

	if !ok {
		t.Fatal("expected hub to be created for test-session")
	}
	if entry.hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", entry.hub.ClientCount())
	}

	conn.Close(websocket.StatusNormalClosure, "")
}

type mockClient struct {
	send func(data []byte)
}

func (m *mockClient) Send(data []byte) {
	if m.send != nil {
		m.send(data)
	}
}

func (m *mockClient) Close() {}
