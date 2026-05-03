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
	srv, err := server.New("127.0.0.1", 0, store)
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
