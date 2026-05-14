package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

func TestSendPrintsSessionURL(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	srv, err := server.New("127.0.0.1", 0, store, "")
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
