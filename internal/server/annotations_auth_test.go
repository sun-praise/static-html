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
// recorder.
func doAnnotationJSON(t *testing.T, handler http.Handler, method, path, creds string, body any) *httptest.ResponseRecorder {
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
	rec = doAnnotationJSON(t, handler, http.MethodDelete, base+"/"+func() string {
		var saved session.Annotation
		if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
			t.Fatal(err)
		}
		return saved.ID
	}(), aliceCookie, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie delete: expected 200, got %d", rec.Code)
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
