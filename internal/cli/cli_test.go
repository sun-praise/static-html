package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

func TestSendPrintsSessionURL(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := server.New("127.0.0.1", 0, store, "", 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Stop(ctx)
	}()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"send", fixtureHTML, "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.Origin()}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if !strings.Contains(output, srv.Origin()+"/s/") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestSendFailsClearlyWhenServerUnavailable(t *testing.T) {
	t.Parallel()

	fixtureHTML, err := filepath.Abs(filepath.Join("..", "..", "fixtures", "basic", "index.html"))
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run([]string{"send", fixtureHTML, "--tag", "test", "--category", "cat", "--project", "proj", "--server", "http://127.0.0.1:4399"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected send to fail")
	}

	if !strings.Contains(err.Error(), `Start the server with "sth start" first.`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendSurfacesServerJSONError(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	fixtureHTML := filepath.Join(tempDir, "index.html")
	if err := os.WriteFile(fixtureHTML, []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"HTML file does not exist."}`)
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Run([]string{"send", fixtureHTML, "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected send to fail")
	}

	if !strings.Contains(err.Error(), "HTML file does not exist.") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendUploadsMultipartArchive(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	assetFile := filepath.Join(rootDir, "assets", "style.css")
	if err := os.MkdirAll(filepath.Dir(assetFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryFile, []byte("<link rel=\"stylesheet\" href=\"assets/style.css\">"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetFile, []byte("body{background:#fff}"), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			t.Fatalf("unexpected content type: %s", r.Header.Get("Content-Type"))
		}

		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}

		fields := map[string]string{}
		archiveEntries := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}

			if part.FormName() == "archive" {
				archiveBytes, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				zipReader, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
				if err != nil {
					t.Fatal(err)
				}
				for _, archivedFile := range zipReader.File {
					fileReader, err := archivedFile.Open()
					if err != nil {
						t.Fatal(err)
					}
					content, err := io.ReadAll(fileReader)
					_ = fileReader.Close()
					if err != nil {
						t.Fatal(err)
					}
					archiveEntries[archivedFile.Name] = string(content)
				}
				continue
			}

			value, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			fields[part.FormName()] = string(value)
		}

		if fields["entryFile"] != filepath.Base(entryFile) {
			t.Fatalf("unexpected entryFile: %q", fields["entryFile"])
		}
		if fields["entryPath"] != "index.html" {
			t.Fatalf("unexpected entryPath: %q", fields["entryPath"])
		}
		if archiveEntries["index.html"] == "" || archiveEntries["assets/style.css"] == "" {
			t.Fatalf("archive missing expected files: %#v", archiveEntries)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "http://example.test/s/session/"})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"send", entryFile, "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "http://example.test/s/session/") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}
}

func newTestStore(t *testing.T) *session.Store {
	t.Helper()

	store, err := session.NewSQLiteStore(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return store
}

func TestDeleteCommandSuccess(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"delete", sess.ID, "--db", dbPath}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "deleted") {
		t.Fatalf("expected deletion confirmation, got %q", stdout.String())
	}
}

func TestDeleteCommandNonExistent(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "sessions.db")
	store, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err = Run([]string{"delete", "nonexistent", "--db", dbPath}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteCommandMissingArg(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := Run([]string{"delete"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing session ID")
	}
	if !strings.Contains(err.Error(), "missing session ID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendOnlyIncludesWebAssetsInArchive(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	cssFile := filepath.Join(rootDir, "style.css")
	binFile := filepath.Join(rootDir, "program")
	logFile := filepath.Join(rootDir, "debug.log")
	hiddenFile := filepath.Join(rootDir, ".hidden")
	hiddenDir := filepath.Join(rootDir, ".secret")
	hiddenNested := filepath.Join(hiddenDir, "data.html")

	for _, f := range []struct {
		path    string
		content []byte
		isDir   bool
	}{
		{entryFile, []byte("<!doctype html><title>ok</title>"), false},
		{cssFile, []byte("body{background:#fff}"), false},
		{binFile, []byte("\x00\x01\x02binary"), false},
		{logFile, []byte("2024-01-01 log entry"), false},
		{hiddenFile, []byte("hidden"), false},
		{hiddenDir, nil, true},
		{hiddenNested, []byte("secret"), false},
	} {
		if f.isDir {
			if err := os.MkdirAll(f.path, 0o755); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.path, f.content, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}

		archiveEntries := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() == "archive" {
				archiveBytes, _ := io.ReadAll(part)
				zr, _ := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
				for _, f := range zr.File {
					fr, _ := f.Open()
					c, _ := io.ReadAll(fr)
					fr.Close()
					archiveEntries[f.Name] = string(c)
				}
			}
		}

		if _, ok := archiveEntries["index.html"]; !ok {
			t.Fatal("archive missing index.html")
		}
		if _, ok := archiveEntries["style.css"]; !ok {
			t.Fatal("archive missing style.css")
		}
		if _, ok := archiveEntries["program"]; ok {
			t.Fatal("archive should not include binary file 'program'")
		}
		if _, ok := archiveEntries["debug.log"]; ok {
			t.Fatal("archive should not include non-web file 'debug.log'")
		}
		if _, ok := archiveEntries[".hidden"]; ok {
			t.Fatal("archive should not include hidden file '.hidden'")
		}
		if _, ok := archiveEntries[".secret/data.html"]; ok {
			t.Fatal("archive should not include files in hidden directories")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "http://example.test/s/session/"})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"send", entryFile, "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestWriteZIPArchiveSkipsPermissionDenied(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	readableFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(readableFile, []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	noPermDir := filepath.Join(rootDir, "no-permission")
	if err := os.MkdirAll(noPermDir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(noPermDir, 0o755) // Restore permissions for cleanup
	}()

	var buf bytes.Buffer
	if err := writeZIPArchive(rootDir, &buf); err != nil {
		t.Fatalf("writeZIPArchive failed: %v", err)
	}

	zipReader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}

	if len(zipReader.File) != 1 {
		t.Fatalf("expected 1 file in archive, got %d", len(zipReader.File))
	}

	if zipReader.File[0].Name != "index.html" {
		t.Fatalf("expected index.html, got %q", zipReader.File[0].Name)
	}
}

func TestSingleFileSkipsWalkOnLargeDirectory(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "report.html")
	if err := os.WriteFile(entryFile, []byte("<!doctype html><title>report</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create many non-web sibling files to simulate a large directory (e.g. home dir)
	for i := 0; i < 200; i++ {
		f := filepath.Join(rootDir, fmt.Sprintf("data-%d.log", i))
		if err := os.WriteFile(f, []byte("log data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		archiveEntries := map[string]string{}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if part.FormName() == "archive" {
				archiveBytes, _ := io.ReadAll(part)
				zr, _ := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
				for _, f := range zr.File {
					fr, _ := f.Open()
					c, _ := io.ReadAll(fr)
					fr.Close()
					archiveEntries[f.Name] = string(c)
				}
			}
		}

		if len(archiveEntries) != 1 {
			t.Fatalf("expected 1 file in archive, got %d: %v", len(archiveEntries), archiveEntries)
		}
		if archiveEntries["report.html"] == "" {
			t.Fatal("archive missing report.html")
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "http://example.test/s/session/"})
	}))
	defer srv.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run([]string{"send", entryFile, "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
}

func TestSubdirectoryWithAssetsStillWalks(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(entryFile, []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-standard subdirectory name that isWebAssetDir won't recognize
	cssDir := filepath.Join(rootDir, "src", "components")
	if err := os.MkdirAll(cssDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cssFile := filepath.Join(cssDir, "style.css")
	if err := os.WriteFile(cssFile, []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := writeZIPArchive(rootDir, &buf); err != nil {
		t.Fatalf("writeZIPArchive failed: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("failed to read zip: %v", err)
	}

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}

	if !names["index.html"] {
		t.Fatal("archive missing index.html")
	}
	if !names["src/components/style.css"] {
		t.Fatal("archive missing src/components/style.css — subdirectory assets should be included")
	}
}

func TestStartRejectsInvalidServerPort(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "sessions.db")

	tests := []struct {
		name    string
		portArg string
	}{
		{"non-numeric", "foo"},
		{"zero", "0"},
		{"negative", "-1"},
		{"too large", "70000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			err := Run([]string{"start", "--server-port", tt.portArg, "--db", dbPath}, &stdout, &stderr)
			if err == nil {
				t.Fatalf("expected error for --server-port %q, got nil", tt.portArg)
			}
		})
	}
}

// uploadCapture records the multipart upload produced by sth send. All fields
// are guarded by mu because the httptest handler runs in its own goroutine;
// callers read state via snapshot/err from the test goroutine after the request
// completes.
type uploadCapture struct {
	mu         sync.Mutex
	entryPath  string
	entries    map[string]string
	handlerErr error
}

func (c *uploadCapture) set(entryPath string, entries map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entryPath = entryPath
	c.entries = entries
}

func (c *uploadCapture) setErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.handlerErr == nil {
		c.handlerErr = err
	}
}

// snapshot returns a copy of the recorded entryPath and archive entries.
func (c *uploadCapture) snapshot() (string, map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := make(map[string]string, len(c.entries))
	for k, v := range c.entries {
		entries[k] = v
	}
	return c.entryPath, entries
}

func (c *uploadCapture) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handlerErr
}

// requireHandlerOK fails the test if the capture handler recorded an error.
func (c *uploadCapture) requireHandlerOK(t *testing.T) {
	t.Helper()
	if err := c.err(); err != nil {
		t.Fatalf("capture handler error: %v", err)
	}
}

// newCaptureServer returns an httptest server that records the uploaded
// multipart archive (entryPath field + archive entries) into capture. Handler
// failures are recorded on capture rather than calling t.Fatal from the handler
// goroutine; callers should check capture.requireHandlerOK after the request.
func newCaptureServer(t *testing.T, capture *uploadCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			capture.setErr(err)
			http.Error(w, "bad multipart", http.StatusBadRequest)
			return
		}
		entries := map[string]string{}
		var entryPath string
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				capture.setErr(err)
				http.Error(w, "bad multipart", http.StatusBadRequest)
				return
			}
			switch part.FormName() {
			case "entryPath":
				b, _ := io.ReadAll(part)
				entryPath = string(b)
			case "archive":
				archiveBytes, _ := io.ReadAll(part)
				zr, zerr := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
				if zerr != nil {
					capture.setErr(zerr)
					http.Error(w, "bad archive", http.StatusBadRequest)
					return
				}
				for _, f := range zr.File {
					fr, _ := f.Open()
					c, _ := io.ReadAll(fr)
					fr.Close()
					entries[f.Name] = string(c)
				}
			}
		}
		capture.set(entryPath, entries)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": "http://example.test/s/session/"})
	}))
}

// When the entry shares a directory with other web assets, the default heuristic
// would walk and bundle them. --single must force archiving only the entry file.
func TestSendSingleFlagForcesSingleFile(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	siblingCSS := filepath.Join(rootDir, "style.css")
	if err := os.WriteFile(entryFile, []byte("<!doctype html><title>ok</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(siblingCSS, []byte("body{background:#fff}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var capture uploadCapture
	srv := newCaptureServer(t, &capture)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"send", entryFile, "--single", "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	capture.requireHandlerOK(t)

	entryPath, entries := capture.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 file in archive, got %d: %v", len(entries), entries)
	}
	if _, ok := entries["index.html"]; !ok {
		t.Fatalf("archive missing index.html, got %v", entries)
	}
	if _, ok := entries["style.css"]; ok {
		t.Fatal("--single should not include sibling web assets")
	}
	if entryPath != "index.html" {
		t.Fatalf("expected entryPath %q, got %q", "index.html", entryPath)
	}
}

// --single=true should behave identically to bare --single.
func TestSendSingleFlagAcceptsExplicitTrue(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(entryFile, []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "style.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var capture uploadCapture
	srv := newCaptureServer(t, &capture)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	if err := Run([]string{"send", entryFile, "--single=true", "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	capture.requireHandlerOK(t)
	_, entries := capture.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}
}

// --root scopes the walk to an explicit directory and reports a nested
// entryPath so the server can locate the entry inside the archive.
func TestSendRootFlagArchivesSpecifiedRoot(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	entryFile := filepath.Join(projectRoot, "site", "index.html")
	siblingAsset := filepath.Join(projectRoot, "site", "style.css")
	noiseFile := filepath.Join(projectRoot, "data.log") // non-web, must be filtered
	for _, f := range []string{entryFile, siblingAsset, noiseFile} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("payload"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var capture uploadCapture
	srv := newCaptureServer(t, &capture)
	defer srv.Close()

	var stdout, stderr bytes.Buffer
	args := []string{"send", entryFile, "--root", projectRoot, "--tag", "test", "--category", "cat", "--project", "proj", "--server", srv.URL}
	if err := Run(args, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	capture.requireHandlerOK(t)

	entryPath, entries := capture.snapshot()
	wantEntries := map[string]bool{"site/index.html": true, "site/style.css": true}
	for name := range wantEntries {
		if _, ok := entries[name]; !ok {
			t.Fatalf("archive missing %q, got %v", name, entries)
		}
	}
	if _, ok := entries["data.log"]; ok {
		t.Fatal("non-web file data.log should be filtered out of the archive")
	}
	wantEntryPath := filepath.ToSlash(filepath.Join("site", "index.html"))
	if entryPath != wantEntryPath {
		t.Fatalf("expected entryPath %q, got %q", wantEntryPath, entryPath)
	}
}

func TestSendSingleAndRootAreMutuallyExclusive(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(entryFile, []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"send", entryFile, "--single", "--root", rootDir, "--tag", "t", "--category", "c", "--project", "p"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for --single together with --root")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually-exclusive error, got %v", err)
	}
}

func TestSendRootRejectsEntryOutsideRoot(t *testing.T) {
	t.Parallel()

	a := t.TempDir()
	b := t.TempDir()
	entryFile := filepath.Join(b, "index.html")
	if err := os.WriteFile(entryFile, []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"send", entryFile, "--root", a, "--tag", "t", "--category", "c", "--project", "p"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for entry outside --root")
	}
	if !strings.Contains(err.Error(), "outside --root") {
		t.Fatalf("expected outside-root error, got %v", err)
	}
}

// An explicitly empty --root must be rejected rather than silently falling back
// to packaging the parent directory.
func TestSendRootRejectsEmpty(t *testing.T) {
	t.Parallel()

	rootDir := t.TempDir()
	entryFile := filepath.Join(rootDir, "index.html")
	if err := os.WriteFile(entryFile, []byte("<!doctype html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := Run([]string{"send", entryFile, "--root", "", "--tag", "t", "--category", "c", "--project", "p"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for empty --root")
	}
	if !strings.Contains(err.Error(), "--root must not be empty") {
		t.Fatalf("expected empty-root error, got %v", err)
	}
}

func TestPopBoolFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		args          []string
		want          bool
		wantRemaining []string
		wantErr       bool
	}{
		{"absent", []string{"--tag", "x"}, false, []string{"--tag", "x"}, false},
		{"bare", []string{"--single", "--tag", "x"}, true, []string{"--tag", "x"}, false},
		{"true", []string{"--single=true", "--tag", "x"}, true, []string{"--tag", "x"}, false},
		{"false", []string{"--single=false", "--tag", "x"}, false, []string{"--tag", "x"}, false},
		{"one", []string{"--single=1"}, true, []string{}, false},
		{"zero", []string{"--single=0"}, false, []string{}, false},
		{"invalid", []string{"--single=maybe"}, false, []string{}, true},
		{"duplicate", []string{"--single", "--single"}, false, []string{}, true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			remaining, got, err := popBoolFlag(tc.args, "single")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got value=%v remaining=%v", got, remaining)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if !reflect.DeepEqual(remaining, tc.wantRemaining) {
				t.Fatalf("remaining args got %v, want %v", remaining, tc.wantRemaining)
			}
		})
	}
}
