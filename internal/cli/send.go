package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sun-praise/static-html/internal/server"
	"github.com/sun-praise/static-html/internal/session"
)

// sendResult is the parsed response of a successful upload: the preview URL
// plus version-chain context when the server links the upload to a chain.
type sendResult struct {
	URL       string `json:"url"`
	ChainID   string `json:"chainId,omitempty"`
	VersionNo int    `json:"versionNo,omitempty"`
}

// uploadTarget carries everything uploadSite needs to build and POST the
// multipart archive. It is produced by resolveUploadTarget so both the CLI
// and the MCP server share one validation path.
type uploadTarget struct {
	entryFile   string
	rootDir     string
	entryPath   string
	title       string
	tags        []string
	category    string
	project     string
	forceSingle bool
}

// resolveUploadTarget validates send arguments and resolves the entry file,
// archive root and entry path exactly as `sth send` does: title is normalized
// up front so a whitespace-only or over-long title fails before the archive is
// built; --single and --root are mutually exclusive; the entry file must be a
// local .html/.htm file; a --root directory must contain the entry file.
func resolveUploadTarget(entryFileArg, title, tag, category, project, rootArg string, hasRoot, forceSingle bool) (uploadTarget, error) {
	normTitle, err := normalizeUploadTitle(title)
	if err != nil {
		return uploadTarget{}, err
	}
	if err := requireUploadField("tag", tag); err != nil {
		return uploadTarget{}, err
	}
	if err := requireUploadField("category", category); err != nil {
		return uploadTarget{}, err
	}
	if err := requireUploadField("project", project); err != nil {
		return uploadTarget{}, err
	}

	if forceSingle && hasRoot {
		return uploadTarget{}, errors.New("--single and --root are mutually exclusive")
	}

	entryFile, err := filepath.Abs(entryFileArg)
	if err != nil {
		return uploadTarget{}, fmt.Errorf("failed to resolve file path: %w", err)
	}

	if !server.IsHTMLFile(entryFile) {
		return uploadTarget{}, errors.New("only .html and .htm files are supported")
	}
	if err := ensureLocalFile(entryFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return uploadTarget{}, fmt.Errorf("HTML file does not exist on this machine: %q", entryFile)
		}
		return uploadTarget{}, err
	}

	rootDir := filepath.Dir(entryFile)
	entryPath := filepath.Base(entryFile)

	if hasRoot {
		target, err := resolveRootDir(entryFile, rootArg)
		if err != nil {
			return uploadTarget{}, err
		}
		rootDir = target.rootDir
		entryPath = target.entryPath
	}

	return uploadTarget{
		entryFile:   entryFile,
		rootDir:     rootDir,
		entryPath:   entryPath,
		title:       normTitle,
		tags:        strings.Split(tag, ","),
		category:    category,
		project:     project,
		forceSingle: forceSingle,
	}, nil
}

func normalizeUploadTitle(title string) (string, error) {
	norm, err := session.NormalizeTitle(title)
	if err != nil {
		return "", err
	}
	if norm == "" {
		return "", errors.New("--title is required")
	}
	return norm, nil
}

func requireUploadField(name, value string) error {
	if value == "" {
		return fmt.Errorf("--%s is required", name)
	}
	return nil
}

// resolvedRoot is the archive root plus the slash-separated entry path within
// it, once --root has been validated.
type resolvedRoot struct {
	rootDir   string
	entryPath string
}

func resolveRootDir(entryFile, rootArg string) (resolvedRoot, error) {
	if rootArg == "" {
		return resolvedRoot{}, errors.New("--root must not be empty")
	}
	absRoot, err := filepath.Abs(rootArg)
	if err != nil {
		return resolvedRoot{}, fmt.Errorf("failed to resolve --root path: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return resolvedRoot{}, fmt.Errorf("--root directory does not exist: %q", absRoot)
	}
	if !info.IsDir() {
		return resolvedRoot{}, fmt.Errorf("--root must be a directory: %q", absRoot)
	}
	rel, err := filepath.Rel(absRoot, entryFile)
	if err != nil {
		return resolvedRoot{}, fmt.Errorf("failed to locate entry file under --root: %w", err)
	}
	relSlash := filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(relSlash, "../") {
		return resolvedRoot{}, fmt.Errorf("entry file %q is outside --root %q", entryFile, absRoot)
	}
	return resolvedRoot{rootDir: absRoot, entryPath: relSlash}, nil
}

// uploadSite builds the multipart archive and POSTs it to the server's
// /api/sessions endpoint, mapping error responses to actionable messages.
func uploadSite(target uploadTarget, serverURL, apiKey string) (sendResult, error) {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return sendResult{}, fmt.Errorf("invalid server URL: %w", err)
	}

	requestBody, contentType, err := newUploadRequestBody(target.entryFile, target.rootDir, target.entryPath, target.title, target.forceSingle, target.tags, target.category, target.project)
	if err != nil {
		return sendResult{}, err
	}
	request, err := http.NewRequest(http.MethodPost, parsedURL.ResolveReference(&url.URL{Path: "/api/sessions"}).String(), requestBody)
	if err != nil {
		return sendResult{}, err
	}
	request.Header.Set("Content-Type", contentType)

	// Attach API key when provided. The server decides whether to enforce; the
	// client simply forwards whatever credential it has.
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		displayURL := parsedURL.Scheme + "://" + parsedURL.Host
		return sendResult{}, fmt.Errorf(`could not reach %s: %w. Start the server with "sth start" first.`, displayURL, err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return sendResult{}, err
	}

	// Check 401 before JSON parsing: the auth middleware returns plain text,
	// not JSON, so an unmarshal failure would mask the actionable hint.
	if response.StatusCode == http.StatusUnauthorized {
		return sendResult{}, errors.New("server requires authentication (401). Provide a valid API key via --api-key or the STH_API_KEY environment variable")
	}

	var resp struct {
		URL       string `json:"url"`
		Error     string `json:"error"`
		ChainID   string `json:"chainId"`
		VersionNo int    `json:"versionNo"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return sendResult{}, errors.New("server returned an invalid response")
	}

	if response.StatusCode == http.StatusForbidden {
		if resp.Error != "" {
			return sendResult{}, errors.New(resp.Error)
		}
		return sendResult{}, errors.New("forbidden: the API key does not own this session")
	}
	if response.StatusCode >= http.StatusBadRequest {
		if resp.Error != "" {
			return sendResult{}, errors.New(resp.Error)
		}
		return sendResult{}, errors.New(strings.TrimSpace(string(body)))
	}

	return sendResult{URL: resp.URL, ChainID: resp.ChainID, VersionNo: resp.VersionNo}, nil
}
