package cli

import (
	"archive/zip"
	"context"
	"encoding/json"
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
	"syscall"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	switch args[0] {
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	case "start":
		return runStart(args[1:], stdout)
	case "send":
		return runSend(args[1:], stdout)
	case "tag":
		return runTag(args[1:], stdout)
	case "categorize":
		return runCategorize(args[1:], stdout)
	case "project":
		return runProject(args[1:], stdout)
	case "list":
		return runList(args[1:], stdout)
	case "search":
		return runSearch(args[1:], stdout)
	case "delete":
		return runDelete(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  sth start [--host 0.0.0.0] [--bind 0.0.0.0] [--port 3939] [--server-name <addr>] [--db /path/to/sessions.db]
  sth send <file.html> --tag <tag1,tag2,...> --category <cat> --project <proj> [--server http://127.0.0.1:3939]
  sth tag [--rm] <session-id> <tag...> [--db /path/to/sessions.db] [--server http://...]
  sth categorize <session-id> <category> [--db /path/to/sessions.db] [--server http://...]
  sth project <session-id> <project> [--db /path/to/sessions.db] [--server http://...]
  sth list [--tag <tag>] [--category <cat>] [--project <proj>] [--limit <n>] [--offset <n>] [--db /path/to/sessions.db]
  sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>] [--limit <n>] [--offset <n>] [--db /path/to/sessions.db]
  sth delete <session-id> [--db /path/to/sessions.db]`)
}

func runStart(args []string, stdout io.Writer) error {
	flags, _, err := parseArgs(args)
	if err != nil {
		return err
	}

	bindAddr := server.DefaultHost
	_, hasHost := flags["host"]
	_, hasBind := flags["bind"]
	if hasHost && hasBind {
		fmt.Fprintln(os.Stderr, "warning: --host is deprecated, use --bind instead; --host takes precedence")
	}
	if value, ok := flags["host"]; ok {
		bindAddr = value
	} else if value, ok := flags["bind"]; ok {
		bindAddr = value
	}

	port := server.DefaultPort
	if value, ok := flags["port"]; ok {
		_, err := fmt.Sscanf(value, "%d", &port)
		if err != nil {
			return errors.New("port must be a positive integer")
		}
	}

	if port <= 0 {
		return errors.New("port must be a positive integer")
	}

	var serverName string
	if value, ok := flags["server-name"]; ok {
		serverName = value
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}

	srv, err := server.New(bindAddr, port, store, serverName)
	if err != nil {
		return errors.Join(err, store.Close())
	}

	if err := srv.Start(); err != nil {
		return errors.Join(err, store.Close())
	}

	origins := srv.Origins()
	if len(origins) == 0 {
		fmt.Fprintf(stdout, "HTML server listening on %s:%d\n", bindAddr, port)
	} else if len(origins) == 1 {
		fmt.Fprintf(stdout, "HTML server listening on %s\n", origins[0])
	} else {
		fmt.Fprintln(stdout, "HTML server listening on:")
		for _, o := range origins {
			fmt.Fprintf(stdout, "  - %s\n", o)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stopErr := srv.Stop(shutdownCtx)
	closeErr := store.Close()
	return errors.Join(stopErr, closeErr)
}

func openStore(flags map[string]string) (*session.Store, error) {
	if value, ok := flags["db"]; ok {
		return session.NewSQLiteStore(value)
	}

	return session.NewStore()
}

func runSend(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 1 {
		return errors.New("missing HTML file path")
	}

	tag := flags["tag"]
	category := flags["category"]
	project := flags["project"]

	if tag == "" {
		return errors.New("--tag is required")
	}
	if category == "" {
		return errors.New("--category is required")
	}
	if project == "" {
		return errors.New("--project is required")
	}

	entryFile, err := filepath.Abs(positionals[0])
	if err != nil {
		return fmt.Errorf("failed to resolve file path: %w", err)
	}

	if !server.IsHTMLFile(entryFile) {
		return errors.New("only .html and .htm files are supported")
	}
	if err := ensureLocalFile(entryFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("HTML file does not exist on this machine: %q", entryFile)
		}
		return err
	}

	serverURL := server.DefaultServerURL
	if value, ok := flags["server"]; ok {
		serverURL = value
	}

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	tags := strings.Split(tag, ",")

	requestBody, contentType, err := newUploadRequestBody(entryFile, tags, category, project)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, parsedURL.ResolveReference(&url.URL{Path: "/api/sessions"}).String(), requestBody)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)

	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		displayURL := parsedURL.Scheme + "://" + parsedURL.Host
		return fmt.Errorf(`could not reach %s: %w. Start the server with "sth start" first.`, displayURL, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	var resp struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errors.New("server returned an invalid response")
	}

	if response.StatusCode >= http.StatusBadRequest {
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return errors.New(strings.TrimSpace(string(body)))
	}

	fmt.Fprintln(stdout, resp.URL)
	return nil
}

func newUploadRequestBody(entryFile string, tags []string, category, project string) (io.Reader, string, error) {
	rootDir := filepath.Dir(entryFile)
	entryPath := filepath.Base(entryFile)
	uploadName := filepath.Base(entryPath)

	reader, writer := io.Pipe()
	formWriter := multipart.NewWriter(writer)

	go func() {
		defer writer.Close()

		if err := formWriter.WriteField("entryFile", uploadName); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := formWriter.WriteField("entryPath", filepath.ToSlash(entryPath)); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := formWriter.WriteField("category", category); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := formWriter.WriteField("project", project); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		for _, t := range tags {
			if err := formWriter.WriteField("tags", t); err != nil {
				_ = writer.CloseWithError(err)
				return
			}
		}

		archiveWriter, err := formWriter.CreateFormFile("archive", "site.zip")
		if err != nil {
			_ = writer.CloseWithError(err)
			return
		}

		if err := writeZIPArchive(rootDir, archiveWriter); err != nil {
			_ = writer.CloseWithError(err)
			return
		}

		if err := formWriter.Close(); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
	}()

	return reader, formWriter.FormDataContentType(), nil
}

func writeZIPArchive(rootDir string, target io.Writer) error {
	archive := zip.NewWriter(target)

	entries, readErr := os.ReadDir(rootDir)
	singleFile := readErr == nil && isSingleFileDir(entries)

	var err error
	if singleFile {
		err = writeSingleFileEntry(archive, rootDir, entries)
	} else {
		err = writeDirEntries(archive, rootDir)
	}

	if err != nil {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}

// isSingleFileDir returns true only when the directory contains exactly one
// non-hidden web asset file and no non-hidden subdirectories. In all other
// cases we fall back to the full WalkDir to avoid silently dropping resources.
func isSingleFileDir(entries []os.DirEntry) bool {
	webCount := 0
	for _, e := range entries {
		if isHiddenName(e.Name()) {
			continue
		}
		if e.IsDir() {
			return false
		}
		if e.Type().IsRegular() && isWebAsset(e.Name()) {
			webCount++
		}
	}
	return webCount == 1
}

func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

func writeSingleFileEntry(archive *zip.Writer, dir string, entries []os.DirEntry) error {
	for _, e := range entries {
		if isHiddenName(e.Name()) || e.IsDir() || !e.Type().IsRegular() || !isWebAsset(e.Name()) {
			continue
		}
		sourceFile, err := os.Open(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		w, err := archive.Create(e.Name())
		if err != nil {
			_ = sourceFile.Close()
			return err
		}
		_, copyErr := io.Copy(w, sourceFile)
		closeErr := sourceFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return fmt.Errorf("no web asset found in directory")
}

func writeDirEntries(archive *zip.Writer, rootDir string) error {
	err := filepath.WalkDir(rootDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrPermission) || strings.Contains(walkErr.Error(), "permission denied") {
				return nil
			}
			return walkErr
		}

		relativePath, err := filepath.Rel(rootDir, filePath)
		if err != nil {
			return err
		}

		if isHiddenPath(relativePath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}

		if !isWebAsset(filePath) {
			return nil
		}

		sourceFile, err := os.Open(filePath)
		if err != nil {
			return err
		}

		archivedFile, err := archive.Create(filepath.ToSlash(relativePath))
		if err != nil {
			_ = sourceFile.Close()
			return err
		}

		_, copyErr := io.Copy(archivedFile, sourceFile)
		closeErr := sourceFile.Close()
		if copyErr != nil {
			return copyErr
		}

		return closeErr
	})
	return err
}

func isHiddenPath(path string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") && part != "." && part != ".." {
			return true
		}
	}
	return false
}

func isWebAsset(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".html", ".htm",
		".css", ".js", ".mjs",
		".json", ".xml", ".txt",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico", ".bmp", ".avif",
		".woff", ".woff2", ".ttf", ".eot", ".otf",
		".mp3", ".mp4", ".webm", ".ogg", ".wav",
		".map", ".wasm", ".pdf":
		return true
	default:
		return false
	}
}

func ensureLocalFile(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("target path is not a file")
	}

	return nil
}

func parseArgs(args []string) (map[string]string, []string, error) {
	flags := make(map[string]string)
	positionals := make([]string, 0, len(args))

	for index := 0; index < len(args); index++ {
		token := args[index]
		if !strings.HasPrefix(token, "--") {
			positionals = append(positionals, token)
			continue
		}

		name := strings.TrimPrefix(token, "--")
		if name == "" {
			return nil, nil, errors.New("invalid empty flag")
		}

		if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
			return nil, nil, fmt.Errorf("missing value for --%s", name)
		}

		if _, exists := flags[name]; exists {
			return nil, nil, fmt.Errorf("duplicate flag --%s", name)
		}
		flags[name] = args[index+1]
		index++
	}

	return flags, positionals, nil
}

func runDelete(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 1 {
		return errors.New("missing session ID")
	}

	sessionID := positionals[0]

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.SoftDelete(sessionID); err != nil {
		if errors.Is(err, session.ErrSessionNotFound) {
			return fmt.Errorf("session %q not found", sessionID)
		}
		return err
	}

	fmt.Fprintf(stdout, "Session %q deleted.\n", sessionID)
	return nil
}
