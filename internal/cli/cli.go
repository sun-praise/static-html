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
  sth start [--host 127.0.0.1] [--port 3939] [--db /path/to/sessions.db]
  sth send <file.html> [--server http://127.0.0.1:3939]
  sth tag [--rm] <session-id> <tag...> [--db /path/to/sessions.db] [--server http://...]
  sth categorize <session-id> [category] [--db /path/to/sessions.db] [--server http://...]
  sth project <session-id> [project] [--db /path/to/sessions.db] [--server http://...]
  sth list [--tag <tag>] [--category <cat>] [--project <proj>] [--db /path/to/sessions.db]
  sth search <query> [--db /path/to/sessions.db]
  sth delete <session-id> [--db /path/to/sessions.db]`)
}

func runStart(args []string, stdout io.Writer) error {
	flags, _, err := parseArgs(args)
	if err != nil {
		return err
	}

	host := server.DefaultHost
	if value, ok := flags["host"]; ok {
		host = value
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

	store, err := openStore(flags)
	if err != nil {
		return err
	}

	srv, err := server.New(host, port, store)
	if err != nil {
		return errors.Join(err, store.Close())
	}

	if err := srv.Start(); err != nil {
		return errors.Join(err, store.Close())
	}

	fmt.Fprintf(stdout, "HTML server listening on %s\n", srv.Origin())

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

	requestBody, contentType, err := newUploadRequestBody(entryFile)
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
		return fmt.Errorf(`could not reach %s. Start the server with "sth start" first.`, parsedURL.Scheme+"://"+parsedURL.Host)
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

func newUploadRequestBody(entryFile string) (io.Reader, string, error) {
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
	err := filepath.WalkDir(rootDir, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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

		relativePath, err := filepath.Rel(rootDir, filePath)
		if err != nil {
			return err
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
	if err != nil {
		_ = archive.Close()
		return err
	}

	return archive.Close()
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
