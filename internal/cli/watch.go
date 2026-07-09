package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sun-praise/static-html/internal/server"
)

func runWatch(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 1 {
		return errors.New("usage: sth watch <path> --session <id> [--server http://127.0.0.1:3939]")
	}

	sessionID := flags["session"]
	if sessionID == "" {
		return errors.New("--session is required")
	}

	serverURL := server.DefaultServerURL
	if v, ok := flags["server"]; ok {
		serverURL = v
	}

	watchPath, err := filepath.Abs(positionals[0])
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	info, err := os.Stat(watchPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("path does not exist: %q", watchPath)
		}
		return err
	}
	if !info.IsDir() {
		return errors.New("watch path must be a directory")
	}

	apiKey := resolveAPIKey(flags)

	if err := validateSession(serverURL, sessionID, apiKey); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Watching %s → session %s\n", watchPath, sessionID)

	return watchAndSync(context.Background(), watchPath, sessionID, serverURL, apiKey, stdout)
}

func validateSession(serverURL, sessionID, apiKey string) error {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	req, err := http.NewRequest(http.MethodGet, parsedURL.ResolveReference(&url.URL{Path: "/api/sessions/" + sessionID + "/metadata"}).String(), nil)
	if err != nil {
		return err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("could not reach server at %s: %w", parsedURL.Host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("server requires authentication (401). Provide a valid API key via --api-key or STH_API_KEY")
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("session %q not found", sessionID)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}
	return nil
}

func watchAndSync(ctx context.Context, dir, sessionID, serverURL, apiKey string, stdout io.Writer) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}
	defer w.Close()

	if err := addWatchDirs(w, dir); err != nil {
		return fmt.Errorf("failed to set up file watching: %w", err)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		mu         sync.Mutex
		pending    = make(map[string]struct{})
		debounce   *time.Timer
	)
	const debounceMs = 300 * time.Millisecond

	flush := func() {
		mu.Lock()
		paths := make([]string, 0, len(pending))
		for p := range pending {
			paths = append(paths, p)
		}
		pending = make(map[string]struct{})
		debounce = nil
		mu.Unlock()

		if len(paths) == 0 {
			return
		}

		if err := uploadFiles(serverURL, sessionID, apiKey, dir, paths); err != nil {
			fmt.Fprintf(stdout, "Error syncing %d file(s): %v\n", len(paths), err)
		} else {
			for _, p := range paths {
				rel, _ := filepath.Rel(dir, p)
				fmt.Fprintf(stdout, "Synced: %s\n", rel)
			}
		}
	}

	for {
		select {
		case <-ctx.Done():
			if debounce != nil {
				debounce.Stop()
			}
			fmt.Fprintln(stdout, "\nStopped watching.")
			return nil
		case event, ok := <-w.Events:
			if !ok {
				return nil
			}
			if shouldIgnorePath(event.Name) {
				continue
			}
			if event.Has(fsnotify.Create) {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					_ = addWatchDirs(w, event.Name)
					continue
				}
			}
			mu.Lock()
			pending[event.Name] = struct{}{}
			mu.Unlock()
			if debounce != nil {
				debounce.Reset(debounceMs)
			} else {
				debounce = time.AfterFunc(debounceMs, flush)
			}
		case _, ok := <-w.Errors:
			if !ok {
				return nil
			}
		}
	}
}

func addWatchDirs(w *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && !shouldIgnorePath(path) {
			return w.Add(path)
		}
		return nil
	})
}

func uploadFiles(serverURL, sessionID, apiKey, watchRoot string, paths []string) error {
	pr, pw := io.Pipe()
	mpw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()
		for _, p := range paths {
			f, err := os.Open(p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "watch: cannot open %s: %v\n", p, err)
				continue
			}
			info, err := f.Stat()
			if err != nil || !info.Mode().IsRegular() {
				f.Close()
				continue
			}
			rel, err := filepath.Rel(watchRoot, p)
			if err != nil {
				f.Close()
				continue
			}
			part, err := mpw.CreateFormFile("files", filepath.ToSlash(rel))
			if err != nil {
				f.Close()
				_ = pw.CloseWithError(err)
				return
			}
			if _, err := io.Copy(part, f); err != nil {
				f.Close()
				_ = pw.CloseWithError(err)
				return
			}
			f.Close()
		}
		_ = mpw.Close()
	}()

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPut,
		parsedURL.ResolveReference(&url.URL{Path: "/api/sessions/" + sessionID + "/files"}).String(),
		pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mpw.FormDataContentType())
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("server requires authentication (401). Provide a valid API key via --api-key or STH_API_KEY")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}

func shouldIgnorePath(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") && base != "." && base != ".." {
		return true
	}
	ext := strings.ToLower(filepath.Ext(base))
	switch ext {
	case ".swp", ".tmp":
		return true
	}
	return false
}
