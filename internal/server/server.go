package server

import (
	"archive/zip"
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
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/sun-praise/static-html/internal/session"
)

const (
	DefaultHost      = "127.0.0.1"
	DefaultPort      = 3939
	DefaultServerURL = "http://127.0.0.1:3939"
	maxUploadBytes   = 64 << 20
	maxArchiveFiles  = 2048
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
	Sessions    []homePageSession
	Search      string
	FilterTag   string
	FilterCat   string
	FilterProj  string
	ClearSearch string
	ClearTag    string
	ClearCat    string
	ClearProj   string
}

type homePageSession struct {
	ID          string
	Name        string
	EntryFile   string
	CreatedAt   string
	PreviewPath string
	Tags        []string
	Category    string
	Project     string
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
      .search-bar {
        display: flex;
        gap: 0.5rem;
        margin-bottom: 1rem;
      }
      .search-bar input {
        flex: 1;
        padding: 0.5rem 0.75rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        font-size: 0.95rem;
        background: #fafaf7;
      }
      .search-bar button {
        padding: 0.5rem 1rem;
        border: 1px solid #d6d1c6;
        border-radius: 8px;
        background: #f2efe6;
        cursor: pointer;
        font-size: 0.95rem;
      }
      .search-bar button:hover {
        background: #e6e2d6;
      }
      .filters {
        display: flex;
        flex-wrap: wrap;
        gap: 0.4rem;
        margin-bottom: 1rem;
      }
      .filter-tag {
        display: inline-flex;
        align-items: center;
        gap: 0.3rem;
        padding: 0.2rem 0.6rem;
        border-radius: 12px;
        font-size: 0.85rem;
        cursor: pointer;
        text-decoration: none;
      }
      .filter-tag.tag {
        background: #dbeafe;
        color: #1e40af;
      }
      .filter-tag.tag:hover {
        background: #bfdbfe;
      }
      .filter-tag.category {
        background: #dcfce7;
        color: #166534;
      }
      .filter-tag.category:hover {
        background: #bbf7d0;
      }
      .filter-tag.project {
        background: #fef3c7;
        color: #92400e;
      }
      .filter-tag.project:hover {
        background: #fde68a;
      }
      .filter-tag .remove {
        font-weight: bold;
        margin-left: 0.2rem;
      }
      .meta {
        margin-top: 0.25rem;
        display: flex;
        flex-wrap: wrap;
        gap: 0.3rem;
        align-items: center;
      }
      .meta .tag {
        display: inline-block;
        padding: 0.1rem 0.5rem;
        border-radius: 10px;
        font-size: 0.8rem;
        background: #dbeafe;
        color: #1e40af;
      }
      .meta .category {
        display: inline-block;
        padding: 0.1rem 0.5rem;
        border-radius: 10px;
        font-size: 0.8rem;
        background: #dcfce7;
        color: #166534;
      }
      .meta .project {
        display: inline-block;
        padding: 0.1rem 0.5rem;
        border-radius: 10px;
        font-size: 0.8rem;
        background: #fef3c7;
        color: #92400e;
      }
    </style>
  </head>
  <body>
    <main>
      <h1>HTML Preview Server</h1>
      <p>Register a file with <code>sth send path/to/file.html</code> and open the returned session URL.</p>
      <form class="search-bar" method="get" action="/">
        <input type="text" name="q" placeholder="Search documents..." value="{{ .Search }}" />
        <button type="submit">Search</button>
      </form>
      {{- if or .FilterTag .FilterCat .FilterProj .Search }}
      <div class="filters">
        {{- if .Search }}
        <a href="{{ .ClearSearch }}" class="filter-tag tag">Search: {{ .Search }} <span class="remove">&times;</span></a>
        {{- end }}
        {{- if .FilterTag }}
        <a href="{{ .ClearTag }}" class="filter-tag tag">Tag: {{ .FilterTag }} <span class="remove">&times;</span></a>
        {{- end }}
        {{- if .FilterCat }}
        <a href="{{ .ClearCat }}" class="filter-tag category">Category: {{ .FilterCat }} <span class="remove">&times;</span></a>
        {{- end }}
        {{- if .FilterProj }}
        <a href="{{ .ClearProj }}" class="filter-tag project">Project: {{ .FilterProj }} <span class="remove">&times;</span></a>
        {{- end }}
      </div>
      {{- end }}
      <ul>
      {{- if .Sessions }}
        {{- range .Sessions }}
        <li>
          <a href="{{ .PreviewPath }}">{{ .Name }}</a>
          <time datetime="{{ .CreatedAt }}">{{ .CreatedAt }}</time>
          {{- if or .Tags .Category .Project }}
          <div class="meta">
            {{- range .Tags }}
            <a href="?tag={{ . }}" class="tag">{{ . }}</a>
            {{- end }}
            {{- if .Category }}
            <a href="?category={{ .Category }}" class="category">{{ .Category }}</a>
            {{- end }}
            {{- if .Project }}
            <a href="?project={{ .Project }}" class="project">{{ .Project }}</a>
            {{- end }}
          </div>
          {{- end }}
        </li>
        {{- end }}
      {{- else }}
        <li>No preview sessions found.</li>
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
		return nil, errors.New("server: store must not be nil")
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
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/tags"):
			s.handleAddTags(w, r)
		case r.Method == http.MethodDelete && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/tags"):
			s.handleRemoveTags(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/category"):
			s.handleSetCategory(w, r)
		case r.Method == http.MethodDelete && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/category"):
			s.handleClearCategory(w, r)
		case r.Method == http.MethodPut && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/project"):
			s.handleSetProject(w, r)
		case r.Method == http.MethodDelete && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/project"):
			s.handleClearProject(w, r)
		case r.Method == http.MethodGet && hasPrefixSuffix(r.URL.Path, "/api/sessions/", "/metadata"):
			s.handleGetMetadata(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))
	filterTag := strings.TrimSpace(q.Get("tag"))
	filterCat := strings.TrimSpace(q.Get("category"))
	filterProj := strings.TrimSpace(q.Get("project"))

	var items []homePageSession

	if search != "" {
		docs, err := s.store.SearchDocuments(search, session.FilterOptions{
			Tag:      filterTag,
			Category: filterCat,
			Project:  filterProj,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = toHomePageSessions(docs)
	} else if filterTag != "" || filterCat != "" || filterProj != "" {
		docs, err := s.store.ListDocuments(session.FilterOptions{
			Tag:      filterTag,
			Category: filterCat,
			Project:  filterProj,
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = toHomePageSessions(docs)
	} else {
		docs, err := s.store.ListDocuments(session.FilterOptions{Limit: 20})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		items = toHomePageSessions(docs)
	}

	clearSearch := buildClearURL(r.URL, "q")
	clearTag := buildClearURL(r.URL, "tag")
	clearCat := buildClearURL(r.URL, "category")
	clearProj := buildClearURL(r.URL, "project")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := homePageTemplate.Execute(w, homePageData{
		Sessions:    items,
		Search:      search,
		FilterTag:   filterTag,
		FilterCat:   filterCat,
		FilterProj:  filterProj,
		ClearSearch: clearSearch,
		ClearTag:    clearTag,
		ClearCat:    clearCat,
		ClearProj:   clearProj,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func toHomePageSessions(docs []session.DocumentInfo) []homePageSession {
	items := make([]homePageSession, 0, len(docs))
	for _, doc := range docs {
		items = append(items, homePageSession{
			ID:          doc.SessionID,
			Name:        filepath.Base(doc.Name),
			EntryFile:   doc.Name,
			CreatedAt:   doc.CreatedAt,
			PreviewPath: "/s/" + doc.SessionID + "/",
			Tags:        doc.Tags,
			Category:    doc.Category,
			Project:     doc.Project,
		})
	}
	return items
}

func buildClearURL(u *url.URL, removeKey string) string {
	q := u.Query()
	q.Del(removeKey)
	if len(q) == 0 {
		return "/"
	}
	return "/?" + q.Encode()
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		s.handleCreateUploadedSession(w, r)
		return
	}

	s.handleCreatePathSession(w, r)
}

func (s *Server) handleCreatePathSession(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) handleCreateUploadedSession(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Failed to parse multipart form upload.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	entryFile := strings.TrimSpace(r.FormValue("entryFile"))
	if entryFile != "" {
		entryFile = filepath.Base(filepath.Clean(filepath.FromSlash(entryFile)))
		if entryFile == "." || entryFile == "" || entryFile == ".." {
			entryFile = ""
		}
	}
	entryPath, err := normalizeArchivePath(r.FormValue("entryPath"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "entryPath must be a relative path inside the archive.")
		return
	}
	if !IsHTMLFile(entryPath) {
		writeJSONError(w, http.StatusBadRequest, "Only .html and .htm files are supported.")
		return
	}
	if entryFile == "" {
		entryFile = entryPath
	}

	archiveFile, _, err := r.FormFile("archive")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "archive file is required.")
		return
	}
	defer archiveFile.Close()

	uploadRoot, err := defaultUploadRoot()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.MkdirAll(uploadRoot, 0o700); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	sessionDir, err := os.MkdirTemp(uploadRoot, "session-*")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cleanupSessionDir := true
	defer func() {
		if cleanupSessionDir {
			_ = os.RemoveAll(sessionDir)
		}
	}()

	if err := extractZIPArchive(archiveFile, sessionDir); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	storedEntryFile := filepath.Join(sessionDir, filepath.FromSlash(entryPath))
	if !IsSubpath(sessionDir, storedEntryFile) {
		writeJSONError(w, http.StatusBadRequest, "entryPath escapes the uploaded archive root.")
		return
	}
	if err := ensureFile(storedEntryFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSONError(w, http.StatusBadRequest, "entryPath does not exist in the uploaded archive.")
			return
		}
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	session, err := s.store.CreateUploaded(entryFile, storedEntryFile)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cleanupSessionDir = false

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
		http.ServeFile(w, r, session.StoredEntryFile)
		return
	}

	targetPath := filepath.Clean(filepath.Join(session.StoredRootDir, filepath.FromSlash(assetPath)))
	if !IsSubpath(session.StoredRootDir, targetPath) {
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

func defaultUploadRoot() (string, error) {
	stateDir, err := session.DefaultStateDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(stateDir, "sth", "uploads"), nil
}

func normalizeArchivePath(value string) (string, error) {
	cleaned := path.Clean(strings.TrimSpace(value))
	if cleaned == "." || cleaned == "" {
		return "", errors.New("empty archive path")
	}
	if strings.HasPrefix(cleaned, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("archive path escapes root")
	}

	return cleaned, nil
}

func extractZIPArchive(file multipartFile, destinationDir string) error {
	size, err := fileSize(file)
	if err != nil {
		return fmt.Errorf("failed to inspect archive: %w", err)
	}

	reader, err := zip.NewReader(file, size)
	if err != nil {
		return errors.New("archive must be a valid zip file")
	}

	for _, archivedFile := range reader.File {
		if archivedFile.FileInfo().IsDir() {
			continue
		}
	}

	if len(reader.File) > maxArchiveFiles {
		return errors.New("uploaded archive contains too many files")
	}

	var totalUncompressed int64
	for _, archivedFile := range reader.File {
		archivePath, err := normalizeArchivePath(archivedFile.Name)
		if err != nil {
			return errors.New("archive contains an invalid file path")
		}

		targetPath := filepath.Join(destinationDir, filepath.FromSlash(archivePath))
		if !IsSubpath(destinationDir, targetPath) {
			return errors.New("archive contains a path that escapes the session root")
		}

		if archivedFile.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}

		mode := archivedFile.Mode()
		if mode&os.ModeSymlink != 0 || !mode.IsRegular() {
			return errors.New("archive may only contain regular files")
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		sourceFile, err := archivedFile.Open()
		if err != nil {
			return err
		}

		targetFile, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			sourceFile.Close()
			return err
		}

		written, copyErr := copyToLimit(sourceFile, targetFile, maxUploadBytes-totalUncompressed)
		totalUncompressed += written
		closeErr := errors.Join(sourceFile.Close(), targetFile.Close())
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}

	return nil
}

func copyToLimit(source io.Reader, target io.Writer, limit int64) (int64, error) {
	if limit < 0 {
		return 0, errors.New("uploaded archive is too large after extraction")
	}

	buffer := make([]byte, 32*1024)
	var copied int64

	for {
		readLimit := len(buffer)
		nRead, err := source.Read(buffer[:readLimit])

		if nRead > 0 {
			if copied+int64(nRead) > limit {
				excess := copied + int64(nRead) - limit
				nReadWritable := nRead - int(excess)
				if nReadWritable > 0 {
					if _, writeErr := target.Write(buffer[:nReadWritable]); writeErr != nil {
						return copied, writeErr
					}
					copied += int64(nReadWritable)
				}
				return copied, errors.New("uploaded archive is too large after extraction")
			}

			copied += int64(nRead)
			if _, writeErr := target.Write(buffer[:nRead]); writeErr != nil {
				return copied, writeErr
			}
		}

		if err != nil {
			if errors.Is(err, io.EOF) {
				return copied, nil
			}
			return copied, err
		}
	}
}

type multipartFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
}

func fileSize(file io.Seeker) (int64, error) {
	current, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, err
	}
	if _, err := file.Seek(current, io.SeekStart); err != nil {
		return 0, err
	}

	return end, nil
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

func hasPrefixSuffix(path, prefix, suffix string) bool {
	return strings.HasPrefix(path, prefix) && strings.HasSuffix(path, suffix)
}

func extractSessionIDFromMetaPath(urlPath, prefix, suffix string) (string, bool) {
	trimmed := strings.TrimPrefix(urlPath, prefix)
	trimmed = strings.TrimSuffix(trimmed, suffix)
	if trimmed == "" || strings.Contains(trimmed, "/") {
		return "", false
	}
	return trimmed, true
}

type tagsRequest struct {
	Tags []string `json:"tags"`
}

type categoryRequest struct {
	Category string `json:"category"`
}

type projectRequest struct {
	Project string `json:"project"`
}

func (s *Server) requireSession(sessionID string) (session.Session, bool, error) {
	return s.store.Get(sessionID)
}

func (s *Server) writeMetaOrError(w http.ResponseWriter, sessionID string, storeErr error) {
	if storeErr != nil {
		if errors.Is(storeErr, session.ErrSessionNotFound) {
			writeJSONError(w, http.StatusNotFound, "Session not found.")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, storeErr.Error())
		return
	}
	meta, err := s.store.GetMetadata(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleAddTags(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/tags")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	var req tagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if len(req.Tags) == 0 {
		writeJSONError(w, http.StatusBadRequest, "At least one tag is required.")
		return
	}

	err := s.store.AddTags(sessionID, req.Tags...)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleRemoveTags(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/tags")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	var req tagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}
	if len(req.Tags) == 0 {
		writeJSONError(w, http.StatusBadRequest, "At least one tag is required.")
		return
	}

	err := s.store.RemoveTags(sessionID, req.Tags...)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleSetCategory(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/category")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	var req categoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}

	err := s.store.SetCategory(sessionID, req.Category)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleClearCategory(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/category")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if err := s.store.ClearCategory(sessionID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	meta, _ := s.store.GetMetadata(sessionID)
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleSetProject(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/project")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	var req projectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Request body must be valid JSON.")
		return
	}

	err := s.store.SetProject(sessionID, req.Project)
	s.writeMetaOrError(w, sessionID, err)
}

func (s *Server) handleClearProject(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/project")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	if err := s.store.ClearProject(sessionID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	meta, _ := s.store.GetMetadata(sessionID)
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleGetMetadata(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID, ok := extractSessionIDFromMetaPath(r.URL.Path, "/api/sessions/", "/metadata")
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "Invalid session ID.")
		return
	}

	_, found, err := s.requireSession(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "Session not found.")
		return
	}

	meta, err := s.store.GetMetadata(sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, meta)
}
