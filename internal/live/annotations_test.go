package live

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestInjectMiddlewareAddsAnnotationUI pins the annotation contract: HTML
// preview responses carry the annotation button, drawer and highlight CSS.
func TestInjectMiddlewareAddsAnnotationUI(t *testing.T) {
	handler := InjectMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><head></head><body><p>hello</p></body></html>"))
	}))

	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/s/testsession/", nil))

	body := resp.Body.String()
	for _, marker := range []string{"sth-ann-fab", "sth-ann-drawer", "sth-ann-bubble", "::highlight(sth-ann)", "sth-ann-margin"} {
		if !strings.Contains(body, marker) {
			t.Fatalf("expected injected page to contain %s", marker)
		}
	}
}
