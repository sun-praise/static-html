package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

func TestTagCommandAddsTags(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s2, err := testStore.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"tag", s2.ID, "go", "web", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "Added tags") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}

	meta, err := testStore.GetMetadata(s2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(meta.Tags))
	}
}

func TestTagCommandRemovesTags(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.AddTags(s.ID, "go", "web"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"tag", "--rm", s.ID, "web", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "Removed tags") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}

	meta, err := testStore.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Tags) != 1 || meta.Tags[0] != "go" {
		t.Fatalf("expected 1 tag 'go', got %v", meta.Tags)
	}
}

func TestCategorizeCommand(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"categorize", s.ID, "tutorial", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "tutorial") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}

	meta, err := testStore.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Category != "tutorial" {
		t.Fatalf("expected category 'tutorial', got %q", meta.Category)
	}
}

func TestCategorizeCommandRejectsEmpty(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetCategory(s.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"categorize", s.ID, "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when category is omitted")
	}

	if !strings.Contains(err.Error(), "usage") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProjectCommand(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"project", s.ID, "my-site", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "my-site") {
		t.Fatalf("unexpected output: %q", stdout.String())
	}

	meta, err := testStore.GetMetadata(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Project != "my-site" {
		t.Fatalf("expected project 'my-site', got %q", meta.Project)
	}
}

func TestProjectCommandRejectsEmpty(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/index.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.SetProject(s.ID, "my-site"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"project", s.ID, "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when project is omitted")
	}

	if !strings.Contains(err.Error(), "usage") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListCommand(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/my-page.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.AddTags(s.ID, "go"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"list", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if !strings.Contains(output, "my-page.html") {
		t.Fatalf("expected output to contain 'my-page.html', got: %q", output)
	}
	if !strings.Contains(output, "go") {
		t.Fatalf("expected output to contain 'go' tag, got: %q", output)
	}
}

func TestListCommandWithFilter(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s1, err := testStore.Create("/tmp/a.html")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := testStore.Create("/tmp/b.html")
	if err != nil {
		t.Fatal(err)
	}

	if err := testStore.AddTags(s1.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := testStore.AddTags(s2.ID, "web"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"list", "--tag", "go", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if !strings.Contains(output, "a.html") {
		t.Fatalf("expected 'a.html' in output, got: %q", output)
	}
	if strings.Contains(output, "b.html") {
		t.Fatalf("expected 'b.html' not in filtered output, got: %q", output)
	}
}

func TestSearchCommand(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "cli-test.db")
	testStore, err := session.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer testStore.Close()

	s, err := testStore.Create("/tmp/my-report.html")
	if err != nil {
		t.Fatal(err)
	}
	if err := testStore.AddTags(s.ID, "important"); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err = Run([]string{"search", "report", "--db", dbPath}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if !strings.Contains(output, "my-report.html") {
		t.Fatalf("expected 'my-report.html' in search results, got: %q", output)
	}
}

func TestHomePageShowsMetadata(t *testing.T) {
	t.Parallel()

	fixtureHTML := filepath.Join("..", "..", "fixtures", "basic", "index.html")
	absoluteFixture, err := filepath.Abs(fixtureHTML)
	if err != nil {
		t.Fatal(err)
	}

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

	// Create a session and add metadata
	s, err := store.Create(absoluteFixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddTags(s.ID, "go", "web"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCategory(s.ID, "tutorial"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetProject(s.ID, "my-site"); err != nil {
		t.Fatal(err)
	}

	resp, err := get(srv.Origin() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body := readBody(resp)
	if !strings.Contains(string(body), "go") {
		t.Fatalf("expected home page to show 'go' tag, got: %s", string(body))
	}
	if !strings.Contains(string(body), "tutorial") {
		t.Fatalf("expected home page to show 'tutorial' category, got: %s", string(body))
	}
	if !strings.Contains(string(body), "my-site") {
		t.Fatalf("expected home page to show 'my-site' project, got: %s", string(body))
	}
}

func TestHomePageFilterByTag(t *testing.T) {
	t.Parallel()

	fixtureHTML := filepath.Join("..", "..", "fixtures", "basic", "index.html")
	absoluteFixture, err := filepath.Abs(fixtureHTML)
	if err != nil {
		t.Fatal(err)
	}

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

	s1, err := store.Create(absoluteFixture)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := store.Create(absoluteFixture)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.AddTags(s1.ID, "go"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddTags(s2.ID, "web"); err != nil {
		t.Fatal(err)
	}

	resp, err := get(srv.Origin() + "/?tag=go")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body := string(readBody(resp))
	if !strings.Contains(body, "/s/"+s1.ID+"/") {
		t.Fatalf("expected filtered page to include session s1, got: %s", body)
	}
	if strings.Contains(body, "/s/"+s2.ID+"/") {
		t.Fatalf("expected filtered page to exclude session s2, got: %s", body)
	}
}

func TestHomePageSearch(t *testing.T) {
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

	s1, err := store.Create("/tmp/my-report.html")
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Create("/tmp/other.html")
	if err != nil {
		t.Fatal(err)
	}

	resp, err := get(srv.Origin() + "/?q=report")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body := string(readBody(resp))
	if !strings.Contains(body, "/s/"+s1.ID+"/") {
		t.Fatalf("expected search results to include s1, got: %s", body)
	}
}

func get(url string) (*http.Response, error) {
	return http.Get(url)
}

func readBody(resp *http.Response) []byte {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	return body
}
