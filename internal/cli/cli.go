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
	"strconv"
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
		return runSend(args[1:], stdout, stderr)
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
	case "watch":
		return runWatch(args[1:], stdout)
	case "user":
		return runUser(args[1:], stdout)
	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  sth start [--host 0.0.0.0] [--bind 0.0.0.0] [--port 3939] [--server-name <addr>] [--server-port <n>] [--db /path/to/sessions.db] [--upload-root /path/to/uploads] [--auth] [--protect-previews]
  sth send <file.html> --tag <tag1,tag2,...> --category <cat> --project <proj> [--server http://127.0.0.1:3939] [--single] [--root <dir>] [--api-key <key>]
  sth tag [--rm] <session-id> <tag...> [--db /path/to/sessions.db] [--server http://...]
  sth categorize <session-id> <category> [--db /path/to/sessions.db] [--server http://...]
  sth project <session-id> <project> [--db /path/to/sessions.db] [--server http://...]
  sth list [--tag <tag>] [--category <cat>] [--project <proj>] [--limit <n>] [--offset <n>] [--db /path/to/sessions.db]
  sth search <query> [--tag <tag>] [--category <cat>] [--project <proj>] [--limit <n>] [--offset <n>] [--db /path/to/sessions.db]
  sth delete <session-id> [--db /path/to/sessions.db]
  sth watch <path> --session <id> [--server http://127.0.0.1:3939] [--api-key <key>]
  sth user <add <name> | issue-key <name> | revoke-key <id|prefix> | list> [--db /path/to/sessions.db]

Authentication:
  --auth                 Enable API-key auth on the server (or STH_AUTH=true). Default off.
  --protect-previews     Require a key for /s/<id>/ previews too (implies --auth).
  --api-key <key>        API key for send/watch (or STH_API_KEY env var).`)
}

func runStart(args []string, stdout io.Writer) error {
	// popBoolFlagWithPresence handles value-less, =value, and presence
	// detection so --auth=false can override STH_AUTH=true.
	args, authSet, authFlag, err := popBoolFlagWithPresence(args, "auth")
	if err != nil {
		return err
	}
	args, protectSet, protectPreviewsFlag, err := popBoolFlagWithPresence(args, "protect-previews")
	if err != nil {
		return err
	}
	args, regSet, allowRegFlag, err := popBoolFlagWithPresence(args, "allow-registration")
	if err != nil {
		return err
	}

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

	serverPort := 0
	if value, ok := flags["server-port"]; ok {
		n, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("server-port must be an integer")
		}
		if n < 1 || n > 65535 {
			return fmt.Errorf("server-port must be in range 1-65535, got %d", n)
		}
		serverPort = n
	}

	uploadRoot := ""
	if value, ok := flags["upload-root"]; ok {
		uploadRoot = value
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}

	srv, err := server.New(bindAddr, port, store, serverName, serverPort, uploadRoot)
	if err != nil {
		return errors.Join(err, store.Close())
	}

	// Resolve auth posture: explicit flag wins, else env, else false.
	authEnabled := resolveBool(authSet, authFlag, "STH_AUTH")
	protectPreviews := resolveBool(protectSet, protectPreviewsFlag, "STH_PROTECT_PREVIEWS")

	// protectPreviews implies authEnabled (relies on auth's key infra). Set
	// protect first so its setter force-enables auth regardless.
	if protectPreviews {
		srv.SetProtectPreviews(true)
		// If the user did not explicitly request auth, surface the implicit
		// enablement so the running posture is unambiguous.
		if !authEnabled && !authSet {
			fmt.Fprintln(os.Stderr, "note: --protect-previews implies --auth; authentication is enabled.")
		}
	} else if authEnabled {
		srv.SetAuthEnabled(true)
	}

	// Registration defaults to open (resolveBoolDefaultTrue) but is meaningful
	// only under --auth; when auth is off the whole auth layer is a no-op and
	// the /register page is never gated, so we skip applying it to keep the
	// startup log clean.
	if srv.AuthEnabled() {
		allowReg := resolveBoolDefaultTrue(regSet, allowRegFlag, "STH_ALLOW_REGISTRATION")
		srv.SetAllowRegistration(allowReg)
	}

	if srv.AuthEnabled() {
		users, _ := store.ListUsers()
		fmt.Fprintf(os.Stderr, "auth: enabled (protect-previews=%t, allow-registration=%t, users=%d)\n", srv.ProtectPreviews(), srv.AllowRegistration(), len(users))
		if len(users) == 0 && srv.AllowRegistration() {
			fmt.Fprintln(os.Stderr, "note: no users exist yet. Visit /register in your browser, or create one with `sth user add <name>`.")
		} else if len(users) == 0 {
			fmt.Fprintln(os.Stderr, "note: no users exist yet. Create one with `sth user add <name>` (registration is disabled).")
		}
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

// resolveBool resolves a boolean setting with explicit-flag-overrides-env
// semantics. When the flag was explicitly set, its value wins; otherwise the
// named environment variable is consulted (accepting true/1/false/0); when
// neither is set, the default false is returned.
func resolveBool(flagSet, flagValue bool, envVar string) bool {
	if flagSet {
		return flagValue
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envVar))) {
	case "true", "1":
		return true
	case "false", "0":
		return false
	default:
		return false
	}
}

// resolveBoolDefaultTrue is the open-by-default twin of resolveBool: when
// neither flag nor env is set, it returns true. Used for --allow-registration
// so self-service sign-up is the out-of-the-box posture.
func resolveBoolDefaultTrue(flagSet, flagValue bool, envVar string) bool {
	if flagSet {
		return flagValue
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envVar))) {
	case "false", "0":
		return false
	case "true", "1":
		return true
	default:
		return true
	}
}

func runSend(args []string, stdout, stderr io.Writer) error {
	args, forceSingle, err := popBoolFlag(args, "single")
	if err != nil {
		return err
	}

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
	rootArg, hasRoot := flags["root"]

	if tag == "" {
		return errors.New("--tag is required")
	}
	if category == "" {
		return errors.New("--category is required")
	}
	if project == "" {
		return errors.New("--project is required")
	}

	if forceSingle && hasRoot {
		return errors.New("--single and --root are mutually exclusive")
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

	rootDir := filepath.Dir(entryFile)
	entryPath := filepath.Base(entryFile)

	if hasRoot {
		if rootArg == "" {
			return errors.New("--root must not be empty")
		}
		absRoot, err := filepath.Abs(rootArg)
		if err != nil {
			return fmt.Errorf("failed to resolve --root path: %w", err)
		}
		info, err := os.Stat(absRoot)
		if err != nil {
			return fmt.Errorf("--root directory does not exist: %q", absRoot)
		}
		if !info.IsDir() {
			return fmt.Errorf("--root must be a directory: %q", absRoot)
		}
		rel, err := filepath.Rel(absRoot, entryFile)
		if err != nil {
			return fmt.Errorf("failed to locate entry file under --root: %w", err)
		}
		relSlash := filepath.ToSlash(rel)
		if rel == ".." || strings.HasPrefix(relSlash, "../") {
			return fmt.Errorf("entry file %q is outside --root %q", entryFile, absRoot)
		}
		rootDir = absRoot
		entryPath = relSlash
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

	requestBody, contentType, err := newUploadRequestBody(entryFile, rootDir, entryPath, forceSingle, tags, category, project)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPost, parsedURL.ResolveReference(&url.URL{Path: "/api/sessions"}).String(), requestBody)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", contentType)

	// Attach API key when provided (flag or env). The server decides whether
	// to enforce; the client simply forwards whatever credential it has.
	if apiKey := resolveAPIKey(flags); apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

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

	// Check 401 before JSON parsing: the auth middleware returns plain text,
	// not JSON, so an unmarshal failure would mask the actionable hint.
	if response.StatusCode == http.StatusUnauthorized {
		return errors.New("server requires authentication (401). Provide a valid API key via --api-key or the STH_API_KEY environment variable")
	}

	var resp struct {
		URL       string `json:"url"`
		Error     string `json:"error"`
		ChainID   string `json:"chainId"`
		VersionNo int    `json:"versionNo"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return errors.New("server returned an invalid response")
	}

	if response.StatusCode == http.StatusForbidden {
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return errors.New("forbidden: the API key does not own this session")
	}
	if response.StatusCode >= http.StatusBadRequest {
		if resp.Error != "" {
			return errors.New(resp.Error)
		}
		return errors.New(strings.TrimSpace(string(body)))
	}

	fmt.Fprintln(stdout, resp.URL)
	// Version-chain context is informational; surface it on stderr so it
	// never pollutes piped URL capture on stdout.
	if resp.ChainID != "" {
		fmt.Fprintf(stderr, "version: v%d of chain %s\n", resp.VersionNo, resp.ChainID)
	}
	return nil
}

// resolveAPIKey returns the API key from --api-key (flag) when set, otherwise
// from the STH_API_KEY environment variable, otherwise empty. Flag precedence.
func resolveAPIKey(flags map[string]string) string {
	if v, ok := flags["api-key"]; ok {
		return v
	}
	return os.Getenv("STH_API_KEY")
}

func newUploadRequestBody(entryFile, rootDir, entryPath string, forceSingle bool, tags []string, category, project string) (io.Reader, string, error) {
	uploadName := filepath.Base(entryFile)
	entryPathSlash := filepath.ToSlash(entryPath)

	reader, writer := io.Pipe()
	formWriter := multipart.NewWriter(writer)

	go func() {
		defer writer.Close()

		if err := formWriter.WriteField("entryFile", uploadName); err != nil {
			_ = writer.CloseWithError(err)
			return
		}
		if err := formWriter.WriteField("entryPath", entryPathSlash); err != nil {
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

		if forceSingle {
			if err := writeForcedSingleEntry(archiveWriter, entryFile, entryPathSlash); err != nil {
				_ = writer.CloseWithError(err)
				return
			}
		} else if err := writeZIPArchive(rootDir, archiveWriter); err != nil {
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

// writeForcedSingleEntry archives only the entry file itself, storing it under
// archiveName. It skips the parent-directory walk entirely so the upload never
// pulls in unrelated sibling files (e.g. when the file lives in a large dir).
func writeForcedSingleEntry(target io.Writer, entryFile, archiveName string) error {
	archive := zip.NewWriter(target)

	sourceFile, err := os.Open(entryFile)
	if err != nil {
		_ = archive.Close()
		return err
	}

	w, err := archive.Create(archiveName)
	if err != nil {
		_ = sourceFile.Close()
		_ = archive.Close()
		return err
	}

	_, copyErr := io.Copy(w, sourceFile)
	closeErr := sourceFile.Close()
	if copyErr != nil {
		_ = archive.Close()
		return copyErr
	}
	if closeErr != nil {
		_ = archive.Close()
		return closeErr
	}
	return archive.Close()
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

// popBoolFlag removes a boolean flag (e.g. --single or --single=true/false) from
// args, returning the remaining args and the resolved value. Unlike parseArgs,
// it supports value-less boolean flags.
func popBoolFlag(args []string, name string) ([]string, bool, error) {
	flagName := "--" + name
	out := make([]string, 0, len(args))
	seen := false
	value := false

	for _, a := range args {
		if a == flagName {
			if seen {
				return nil, false, fmt.Errorf("duplicate flag --%s", name)
			}
			seen = true
			value = true
			continue
		}
		if strings.HasPrefix(a, flagName+"=") {
			if seen {
				return nil, false, fmt.Errorf("duplicate flag --%s", name)
			}
			raw := strings.TrimPrefix(a, flagName+"=")
			switch strings.ToLower(raw) {
			case "true", "1":
				value = true
			case "false", "0":
				value = false
			default:
				return nil, false, fmt.Errorf("invalid value for --%s: %q (expected true or false)", name, raw)
			}
			seen = true
			continue
		}
		out = append(out, a)
	}

	return out, value, nil
}

// popBoolFlagWithPresence is like popBoolFlag but also reports whether the flag
// was present at all, so callers can distinguish "--auth=false" (explicitly
// disabled, overrides env) from the flag being absent (fall back to env).
func popBoolFlagWithPresence(args []string, name string) (remaining []string, present, value bool, err error) {
	flagName := "--" + name
	out := make([]string, 0, len(args))
	seen := false
	v := false

	for _, a := range args {
		if a == flagName {
			if seen {
				return nil, false, false, fmt.Errorf("duplicate flag --%s", name)
			}
			seen = true
			v = true
			continue
		}
		if strings.HasPrefix(a, flagName+"=") {
			if seen {
				return nil, false, false, fmt.Errorf("duplicate flag --%s", name)
			}
			raw := strings.TrimPrefix(a, flagName+"=")
			switch strings.ToLower(raw) {
			case "true", "1":
				v = true
			case "false", "0":
				v = false
			default:
				return nil, false, false, fmt.Errorf("invalid value for --%s: %q (expected true or false)", name, raw)
			}
			seen = true
			continue
		}
		out = append(out, a)
	}

	return out, seen, v, nil
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
