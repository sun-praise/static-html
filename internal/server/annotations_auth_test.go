package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sun-praise/static-html/internal/session"
)

// doAnnotationJSON issues a JSON request through handler with optional
// credentials — either a Bearer API key, or a login cookie pair (the
// "sth_session=..." string returned by registerAndLogin) — and returns the
// recorder. Extra headers (e.g. Origin) can be passed as a final map.
func doAnnotationJSON(t *testing.T, handler http.Handler, method, path, creds string, body any, extraHeaders ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, reader)
	if creds != "" {
		if strings.HasPrefix(creds, sessionCookieName+"=") {
			req.Header.Set("Cookie", creds)
		} else {
			req.Header.Set("Authorization", "Bearer "+creds)
		}
	}
	for _, headers := range extraHeaders {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestAnnotationsAuthOn: with --auth enabled and previews protected,
// annotation reads and writes require the session owner's Bearer key;
// anonymous requests are rejected.
func TestAnnotationsAuthOn(t *testing.T) {
	t.Parallel()

	srv, store := newAuthServer(t, true, true)
	srv.SetAllowRegistration(true)
	// One user owns the session and holds both credentials, matching a real
	// deployment: the uploader's API key and the same user's browser login.
	aliceCookie := registerAndLogin(t, srv, "alice", "pw-webpass")
	alice, err := store.FindUserByUsername("alice")
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, err := store.IssueAPIKey(alice.ID)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	// In a real deployment the uploader owns the session.
	if err := store.SetSessionOwner(sess.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	base := "/api/sessions/" + sess.ID + "/annotations"
	handler := srv.httpServer.Handler

	// Anonymous write is rejected (mutating path is Bearer-only).
	if rec := doAnnotationJSON(t, handler, http.MethodPost, base, "", map[string]string{
		"kind": "underline", "quote": "q",
	}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous post: expected 401, got %d", rec.Code)
	}

	// Owner write via Bearer succeeds.
	rec := doAnnotationJSON(t, handler, http.MethodPost, base, aliceKey, map[string]string{
		"kind": "underline", "quote": "预览链接只是临时快照", "tag": "需求",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bearer post: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created session.Annotation
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}

	// Owner list works.
	rec = doAnnotationJSON(t, handler, http.MethodGet, base, aliceKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer list: expected 200, got %d", rec.Code)
	}
	var list struct {
		Annotations []session.Annotation `json:"annotations"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Annotations) != 1 {
		t.Fatalf("expected 1 annotation, got %d", len(list.Annotations))
	}

	// Owner delete via Bearer succeeds.
	rec = doAnnotationJSON(t, handler, http.MethodDelete, base+"/"+created.ID, aliceKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer delete: expected 200, got %d", rec.Code)
	}

	// The cookie path (requireBearerOrCookie) works for the browser UI too:
	// the session owner, logged in via web, creates via session cookie —
	// no Bearer involved.
	rec = doAnnotationJSON(t, handler, http.MethodPost, base, aliceCookie, map[string]string{
		"kind": "note", "quote": "浏览器端批注", "comment": "cookie 授权链路",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("cookie post: expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var cookieCreated session.Annotation
	if err := json.NewDecoder(rec.Body).Decode(&cookieCreated); err != nil {
		t.Fatal(err)
	}
	rec = doAnnotationJSON(t, handler, http.MethodDelete, base+"/"+cookieCreated.ID, aliceCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie delete: expected 200, got %d", rec.Code)
	}

	// Another reader's cookie cannot annotate the owner's session.
	malloryCookie := registerAndLogin(t, srv, "mallory", "pw-mallory")
	rec = doAnnotationJSON(t, handler, http.MethodPost, base, malloryCookie, map[string]string{
		"kind": "underline", "quote": "非属主写入",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner cookie post: expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}

	// The owner's cookie with a cross-site Origin is rejected (CSRF guard).
	rec = doAnnotationJSON(t, handler, http.MethodPost, base, aliceCookie, map[string]string{
		"kind": "underline", "quote": "跨站请求",
	}, map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin cookie post: expected 403, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestAnnotationsRequireLoginEvenWhenPreviewsOpen: with auth enabled but
// previews unprotected the page is anonymous, yet annotation endpoints still
// demand a Bearer key — the injected UI degrades to a "log in to annotate"
// hint instead of failing loudly.
func TestAnnotationsRequireLoginEvenWhenPreviewsOpen(t *testing.T) {
	t.Parallel()

	srv, store := newAuthServer(t, true, false)
	alice, err := store.CreateUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, err := store.IssueAPIKey(alice.ID)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSessionOwner(sess.ID, alice.ID); err != nil {
		t.Fatal(err)
	}
	base := "/api/sessions/" + sess.ID + "/annotations"
	handler := srv.httpServer.Handler

	if rec := doAnnotationJSON(t, handler, http.MethodPost, base, "", map[string]string{
		"kind": "underline", "quote": "匿名请求",
	}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous post: expected 401, got %d", rec.Code)
	}
	if rec := doAnnotationJSON(t, handler, http.MethodGet, base, "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: expected 401, got %d", rec.Code)
	}

	// The owner's key works even though the preview page is open.
	rec := doAnnotationJSON(t, handler, http.MethodPost, base, aliceKey, map[string]string{
		"kind": "underline", "quote": "alice 的个人标注",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bearer post: expected 201, got %d", rec.Code)
	}
}
