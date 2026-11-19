package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func newAnnotationTestServer(t *testing.T) (*Server, *session.Store, string) {
	t.Helper()

	store := newTestStore(t)
	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	srv, err := New("127.0.0.1", 0, store, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	})

	return srv, store, sess.ID
}

func doAnnotationRequest(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequest(method, path, reader)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestAnnotationsCRUD(t *testing.T) {
	t.Parallel()

	srv, _, sessionID := newAnnotationTestServer(t)
	base := srv.Origin() + "/api/sessions/" + sessionID + "/annotations"

	// Create an underline (no comment required).
	resp := doAnnotationRequest(t, http.MethodPost, base, map[string]string{
		"kind":   "underline",
		"quote":  "预览链接只是临时快照",
		"prefix": "一是",
		"suffix": "；二是",
		"tag":    "需求",
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 201, got %d body=%s", resp.StatusCode, body)
	}
	var created session.Annotation
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Quote != "预览链接只是临时快照" {
		t.Fatalf("unexpected created record: %+v", created)
	}

	// Create a note.
	resp = doAnnotationRequest(t, http.MethodPost, base, map[string]string{
		"kind":    "note",
		"quote":   "标注只能截图",
		"comment": "痛点是想法无处可写",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 for note, got %d", resp.StatusCode)
	}

	// List shows both, oldest first.
	resp = doAnnotationRequest(t, http.MethodGet, base, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var list struct {
		Annotations []session.Annotation `json:"annotations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Annotations) != 2 {
		t.Fatalf("expected 2 annotations, got %d", len(list.Annotations))
	}
	if list.Annotations[0].Quote != "预览链接只是临时快照" {
		t.Fatalf("unexpected order: %+v", list.Annotations)
	}

	// Delete the second one.
	resp = doAnnotationRequest(t, http.MethodDelete, base+"/"+list.Annotations[1].ID, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on delete, got %d", resp.StatusCode)
	}

	// Deleting again is a 404.
	resp = doAnnotationRequest(t, http.MethodDelete, base+"/"+list.Annotations[1].ID, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on repeat delete, got %d", resp.StatusCode)
	}

	resp = doAnnotationRequest(t, http.MethodGet, base, nil)
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Annotations) != 1 {
		t.Fatalf("expected 1 annotation after delete, got %d", len(list.Annotations))
	}
}

func TestCreateAnnotationValidation(t *testing.T) {
	t.Parallel()

	srv, _, sessionID := newAnnotationTestServer(t)
	base := srv.Origin() + "/api/sessions/" + sessionID + "/annotations"

	cases := []struct {
		name string
		body map[string]string
	}{
		{"invalid kind", map[string]string{"kind": "highlight", "quote": "q"}},
		{"empty quote", map[string]string{"kind": "underline", "quote": "   "}},
		{"note without comment", map[string]string{"kind": "note", "quote": "q"}},
	}
	for _, tc := range cases {
		resp := doAnnotationRequest(t, http.MethodPost, base, tc.body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: expected 400, got %d", tc.name, resp.StatusCode)
		}
	}
}

func TestAnnotationsSessionNotFound(t *testing.T) {
	t.Parallel()

	srv, _, _ := newAnnotationTestServer(t)
	base := srv.Origin() + "/api/sessions/doesnotexist/annotations"

	if resp := doAnnotationRequest(t, http.MethodGet, base, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on list, got %d", resp.StatusCode)
	}
	if resp := doAnnotationRequest(t, http.MethodPost, base, map[string]string{"kind": "underline", "quote": "q"}); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on create, got %d", resp.StatusCode)
	}
	// A well-formed but unknown annotation id on an unknown session: 404.
	if resp := doAnnotationRequest(t, http.MethodDelete, base+"/someid", nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on delete, got %d", resp.StatusCode)
	}
}
