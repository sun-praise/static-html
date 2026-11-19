package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

// pathSeg validates that s is safe to interpolate as a single URL path
// segment. "/" would change routing, a bare dot-segment (".", "..", "...") is
// resolved away by some proxies and could escape the endpoint namespace, and
// "?" or "#" would introduce URL syntax into the opaque path. Everything else
// is used as-is (URL.String escapes the reserved characters).
func pathSeg(s string) (string, error) {
	if s == "" || strings.ContainsAny(s, "/?#") || strings.Trim(s, ".") == "" {
		return "", fmt.Errorf("invalid path segment %q", s)
	}
	return s, nil
}

// doJSONRequest performs an authenticated JSON request against serverURL with
// the given plain request path. The path is treated as an opaque escaped path
// (no query string, no URL syntax) — dynamic segments must be validated with
// pathSeg; queries go through doJSONRequestQuery.
func doJSONRequest(method, serverURL, path, apiKey string, body any) (map[string]any, error) {
	return doJSONAt(method, serverURL, &url.URL{Path: path}, apiKey, body)
}

// doJSONRequestQuery is doJSONRequest with an explicit query string, keeping
// the path free of URL syntax.
func doJSONRequestQuery(method, serverURL, path string, query url.Values, apiKey string, body any) (map[string]any, error) {
	return doJSONAt(method, serverURL, &url.URL{Path: path, RawQuery: query.Encode()}, apiKey, body)
}

func doJSONAt(method, serverURL string, ref *url.URL, apiKey string, body any) (map[string]any, error) {
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("invalid server URL: %w", err)
	}

	var reqBody io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, parsedURL.ResolveReference(ref).String(), reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach server at %s: %w", parsedURL.Host, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("server returned non-JSON response: %s", strings.TrimSpace(string(respBody)))
	}

	if resp.StatusCode >= 400 {
		if errMsg, ok := result["error"].(string); ok && errMsg != "" {
			return nil, errors.New(errMsg)
		}
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	return result, nil
}

func runTag(args []string, stdout io.Writer) error {
	remove := false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--rm" {
			remove = true
			continue
		}
		filtered = append(filtered, a)
	}

	flags, positionals, err := parseArgs(filtered)
	if err != nil {
		return err
	}

	if len(positionals) < 1 {
		return errors.New("usage: sth tag [--rm] <session-id> <tag...>")
	}

	sessionID := positionals[0]
	tags := positionals[1:]
	if len(tags) == 0 {
		return errors.New("at least one tag is required")
	}

	if serverURL, ok := flags["server"]; ok {
		method := http.MethodPut
		if remove {
			method = http.MethodDelete
		}
		sid, err := pathSeg(sessionID)
		if err != nil {
			return err
		}
		if _, err := doJSONRequest(method, serverURL,
			"/api/sessions/"+sid+"/tags",
			resolveAPIKey(flags),
			map[string]any{"tags": tags}); err != nil {
			return err
		}
		if remove {
			fmt.Fprintf(stdout, "Removed tags from %s: %s\n", sessionID, strings.Join(tags, ", "))
		} else {
			fmt.Fprintf(stdout, "Added tags to %s: %s\n", sessionID, strings.Join(tags, ", "))
		}
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if remove {
		if err := store.RemoveTags(sessionID, tags...); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Removed tags from %s: %s\n", sessionID, strings.Join(tags, ", "))
	} else {
		if err := store.AddTags(sessionID, tags...); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Added tags to %s: %s\n", sessionID, strings.Join(tags, ", "))
	}

	return nil
}

func runCategorize(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 2 {
		return errors.New("usage: sth categorize <session-id> <category>")
	}

	sessionID := positionals[0]
	category := positionals[1]

	if serverURL, ok := flags["server"]; ok {
		sid, err := pathSeg(sessionID)
		if err != nil {
			return err
		}
		_, err = doJSONRequest(http.MethodPut, serverURL,
			"/api/sessions/"+sid+"/category",
			resolveAPIKey(flags),
			map[string]any{"category": category})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set category of %s to %s\n", sessionID, category)
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.SetCategory(sessionID, category); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Set category of %s to %s\n", sessionID, category)

	return nil
}

func runProject(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 2 {
		return errors.New("usage: sth project <session-id> <project>")
	}

	sessionID := positionals[0]
	project := positionals[1]

	if serverURL, ok := flags["server"]; ok {
		sid, err := pathSeg(sessionID)
		if err != nil {
			return err
		}
		_, err = doJSONRequest(http.MethodPut, serverURL,
			"/api/sessions/"+sid+"/project",
			resolveAPIKey(flags),
			map[string]any{"project": project})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set project of %s to %s\n", sessionID, project)
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.SetProject(sessionID, project); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Set project of %s to %s\n", sessionID, project)

	return nil
}

func runTitle(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 2 {
		return errors.New("usage: sth title <session-id> <title>")
	}

	sessionID := positionals[0]
	title := positionals[1]

	if serverURL, ok := flags["server"]; ok {
		sid, err := pathSeg(sessionID)
		if err != nil {
			return err
		}
		_, err = doJSONRequest(http.MethodPut, serverURL,
			"/api/sessions/"+sid+"/title",
			resolveAPIKey(flags),
			map[string]any{"title": title})
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set title of %s to %s\n", sessionID, title)
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.SetTitle(sessionID, title); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Set title of %s to %s\n", sessionID, title)

	return nil
}

func runList(args []string, stdout io.Writer) error {
	flags, _, err := parseArgs(args)
	if err != nil {
		return err
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	filter, err := parseFilterOptions(flags)
	if err != nil {
		return err
	}

	docs, err := store.ListDocuments(filter)
	if err != nil {
		return err
	}

	if len(docs) == 0 {
		fmt.Fprintln(stdout, "No documents found.")
		return nil
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(docs); err != nil {
		return err
	}

	return nil
}

func runSearch(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 1 {
		return errors.New("usage: sth search <query>")
	}

	query := strings.Join(positionals, " ")

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	filter, err := parseFilterOptions(flags)
	if err != nil {
		return err
	}

	docs, err := store.SearchDocuments(query, filter)
	if err != nil {
		return err
	}

	if len(docs) == 0 {
		fmt.Fprintln(stdout, "No documents found.")
		return nil
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(docs); err != nil {
		return err
	}

	return nil
}

func parseFilterOptions(flags map[string]string) (session.FilterOptions, error) {
	filter := session.FilterOptions{
		Tag:      flags["tag"],
		Category: flags["category"],
		Project:  flags["project"],
	}

	if limitStr, ok := flags["limit"]; ok {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 0 {
			return session.FilterOptions{}, errors.New("limit must be a non-negative integer")
		}
		filter.Limit = n
	}

	if offsetStr, ok := flags["offset"]; ok {
		n, err := strconv.Atoi(offsetStr)
		if err != nil || n < 0 {
			return session.FilterOptions{}, errors.New("offset must be a non-negative integer")
		}
		filter.Offset = n
	}

	if filter.Offset > 0 && filter.Limit <= 0 {
		return session.FilterOptions{}, errors.New("offset requires a positive limit")
	}

	return filter, nil
}
