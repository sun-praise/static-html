package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sun-praise/static-html/internal/session"
)

const (
	DefaultHost      = "127.0.0.1"
	DefaultPort      = 3939
	DefaultServerURL = "http://127.0.0.1:3939"
)

type Server struct {
	host       string
	port       int
	store      *session.Store
	httpServer *http.Server
	listener   net.Listener
	mu         sync.RWMutex
}

type createSessionRequest struct {
	FilePath string `json:"filePath"`
}

type createSessionResponse struct {
	SessionID string `json:"sessionId"`
	URL       string `json:"url"`
	EntryFile string `json:"entryFile"`
	RootDir   string `json:"rootDir"`
}

type homePageData struct {
	Sessions []homePageSession
}

type homePageSession struct {
	ID          string
	Name        string
	EntryFile   string
	CreatedAt   string
	PreviewPath string
}

var homePageTemplate = template.Must(template.New("home").Parse(`<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8">
    <title>HTML Preview Server</title>
    <style>
      :root {
        color-scheme: light;
        font-family: "IBM Plex Sans", "Segoe UI", sans-serif;
      }
      body {
        margin: 0;
        padding: 2rem;
        background: linear-gradient(180deg, #f6f4ee 0%, #ebe7dc 100%);
        color: #171717;
      }
      main {
        max-width: 900px;
        margin: 0 auto;
        background: rgba(255, 255, 255, 0.8);
        border: 1px solid #d6d1c6;
        border-radius: 16px;
        padding: 2rem;
        box-shadow: 0 20px 50px rgba(0, 0, 0, 0.08);
      }
      h1 {
        margin-top: 0;
      }
      code {
        display: inline-block;
        margin-left: 0.75rem;
        padding: 0.15rem 0.4rem;
        border-radius: 6px;
        background: #f2efe6;
      }
      ul {
        padding-left: 1.2rem;
      }
      li {
        margin-bottom: 0.8rem;
      }
      time {
        margin-left: 0.75rem;
        color: #5a5a5a;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>HTML Preview Server</h1>
      <p>Register a file with <code>sth send path/to/file.html</code> and open the returned session URL.</p>
      <ul>
      {{- if .Sessions }}
        {{- range .Sessions }}
        <li>
          <a href="{{ .PreviewPath }}">{{ .Name }}</a>
          <code>{{ .EntryFile }}</code>
          <time datetime="{{ .CreatedAt }}">{{ .CreatedAt }}</time>
        </li>
        {{- end }}
      {{- else }}
        <li>No preview sessions yet.</li>
      {{- end }}
      </ul>
    </main>
  </body>
</html>`))

func New(host string, port int, store *session.Store) (*Server, error) {
	if host == "" {
		host = DefaultHost
	}

	if store == nil {
		var err error
		store, err = session.NewInMemoryStore()
		if err != nil {
			return nil, err
		}
	}

	srv := &Server{
		host:  host,
		port:  port,
		store: store,
	}

	srv.httpServer = &http.Server{
		Handler: srv.routes(),
	}

	return srv, nil
}

func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener != nil {
		return nil
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", s.host, s.port))
	if err != nil {
		return err
	}

	s.listener = listener

	go func() {
		err := s.httpServer.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		}
	}()

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.listener == nil {
		return nil
	}

	err := s.httpServer.Shutdown(ctx)
	s.listener = nil
	return err
}

func (s *Server) Origin() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.listener == nil {
		return ""
	}

	address, ok := s.listener.Addr().(*net.TCPAddr)
	if !ok {
		return ""
	}

	return fmt.Sprintf("http://%s:%d", address.IP.String(), address.Port)
}

func (s *Server) routes() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			s.handleHome(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/sessions":
			s.handleCreateSession(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/s/"):
			s.handlePreview(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) handleHome(w http.ResponseWriter, _ *http.Request) {
	sessions, err := s.store.ListRecent(20)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	items := make([]homePageSession, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, homePageSession{
			ID:          session.ID,
			Name:        filepath.Base(session.EntryFile),
			EntryFile:   session.EntryFile,
			CreatedAt:   session.CreatedAtISO(),
			PreviewPath: "/s/" + session.ID + "/",
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := homePageTemplate.Execute(w, homePageData{Sessions: items}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to read request body.")
		return
	}

	var req createSessionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}

	if req.FilePath == "" {
		writeJSONError(w, http.StatusBadRequest, "filePath is required.")
		return
	}

	entryFile, err := filepath.Abs(req.FilePath)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to resolve filePath.")
		return
	}

	if !IsHTMLFile(entryFile) {
		writeJSONError(w, http.StatusBadRequest, "Only .html and .htm files are supported.")
		return
	}

	if err := ensureFile(entryFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, "HTML file does not exist.")
			return
		}

		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	session, err := s.store.Create(entryFile)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	baseURL := baseURL(r)
	response := createSessionResponse{
		SessionID: session.ID,
		URL:       baseURL + "/s/" + session.ID + "/",
		EntryFile: session.EntryFile,
		RootDir:   session.RootDir,
	}

	writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	sessionID, assetPath, redirectPath, err := parsePreviewPath(r)
	if err != nil {
		if errors.Is(err, errRedirectRequired) {
			http.Redirect(w, r, redirectPath, http.StatusFound)
			return
		}

		http.NotFound(w, r)
		return
	}

	session, found, err := s.store.Get(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if !found {
		http.Error(w, "Session not found.", http.StatusNotFound)
		return
	}

	if assetPath == "" {
		http.ServeFile(w, r, session.EntryFile)
		return
	}

	targetPath := filepath.Clean(filepath.Join(session.RootDir, filepath.FromSlash(assetPath)))
	if !IsSubpath(session.RootDir, targetPath) {
		http.Error(w, "Path escapes the session root.", http.StatusForbidden)
		return
	}

	if err := ensureFile(targetPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			http.Error(w, "Resource not found.", http.StatusNotFound)
			return
		}

		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.ServeFile(w, r, targetPath)
}

var errRedirectRequired = errors.New("redirect required")

func parsePreviewPath(r *http.Request) (string, string, string, error) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/s/")
	if trimmed == r.URL.Path || trimmed == "" {
		return "", "", "", errors.New("invalid preview path")
	}

	parts := strings.SplitN(trimmed, "/", 2)
	sessionID := parts[0]
	if sessionID == "" {
		return "", "", "", errors.New("missing session id")
	}

	if len(parts) == 1 {
		return sessionID, "", "/s/" + sessionID + "/", errRedirectRequired
	}

	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/s/"+sessionID+"/")
	if escaped == r.URL.EscapedPath() {
		return "", "", "", errors.New("invalid escaped preview path")
	}

	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", "", "", err
	}

	return sessionID, decoded, "", nil
}

func IsHTMLFile(filePath string) bool {
	extension := strings.ToLower(filepath.Ext(filePath))
	return extension == ".html" || extension == ".htm"
}

func IsSubpath(rootDir string, targetPath string) bool {
	relativePath, err := filepath.Rel(rootDir, targetPath)
	if err != nil {
		return false
	}

	return relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)))
}

func ensureFile(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("target path is not a file")
	}

	return nil
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host
}
