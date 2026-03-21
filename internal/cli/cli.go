package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  sth start [--host 127.0.0.1] [--port 3939] [--db /path/to/sessions.db]
  sth send <file.html> [--server http://127.0.0.1:3939]`)
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

	serverURL := server.DefaultServerURL
	if value, ok := flags["server"]; ok {
		serverURL = value
	}

	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}

	payload, err := json.Marshal(map[string]string{"filePath": entryFile})
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, parsedURL.ResolveReference(&url.URL{Path: "/api/sessions"}).String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 5 * time.Second}
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
