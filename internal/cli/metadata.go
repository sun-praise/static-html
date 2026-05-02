package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sun-praise/static-html/internal/session"
)

func doJSONRequest(method, serverURL, path string, body any) (map[string]any, error) {
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

	req, err := http.NewRequest(method, parsedURL.ResolveReference(&url.URL{Path: path}).String(), reqBody)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
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
		return nil, errors.New("server returned an invalid response")
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
		if _, err := doJSONRequest(method, serverURL,
			"/api/sessions/"+sessionID+"/tags",
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

	if len(positionals) < 1 {
		return errors.New("usage: sth categorize <session-id> <category>")
	}

	sessionID := positionals[0]

	if serverURL, ok := flags["server"]; ok {
		if len(positionals) >= 2 {
			_, err := doJSONRequest(http.MethodPut, serverURL,
				"/api/sessions/"+sessionID+"/category",
				map[string]any{"category": positionals[1]})
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Set category of %s to %s\n", sessionID, positionals[1])
		} else {
			_, err := doJSONRequest(http.MethodDelete, serverURL,
				"/api/sessions/"+sessionID+"/category", nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Cleared category of %s\n", sessionID)
		}
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if len(positionals) >= 2 {
		category := positionals[1]
		if err := store.SetCategory(sessionID, category); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Set category of %s to %s\n", sessionID, category)
	} else {
		if err := store.ClearCategory(sessionID); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Cleared category of %s\n", sessionID)
	}

	return nil
}

func runProject(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}

	if len(positionals) < 1 {
		return errors.New("usage: sth project <session-id> [project]")
	}

	sessionID := positionals[0]

	if serverURL, ok := flags["server"]; ok {
		if len(positionals) >= 2 {
			_, err := doJSONRequest(http.MethodPut, serverURL,
				"/api/sessions/"+sessionID+"/project",
				map[string]any{"project": positionals[1]})
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Set project of %s to %s\n", sessionID, positionals[1])
		} else {
			_, err := doJSONRequest(http.MethodDelete, serverURL,
				"/api/sessions/"+sessionID+"/project", nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "Cleared project of %s\n", sessionID)
		}
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	var project string
	if len(positionals) >= 2 {
		project = positionals[1]
	}

	if err := store.SetProject(sessionID, project); err != nil {
		return err
	}

	if project != "" {
		fmt.Fprintf(stdout, "Set project of %s to %s\n", sessionID, project)
	} else {
		fmt.Fprintf(stdout, "Cleared project of %s\n", sessionID)
	}

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

	filter := session.FilterOptions{
		Tag:      flags["tag"],
		Category: flags["category"],
		Project:  flags["project"],
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

	docs, err := store.SearchDocuments(query, session.FilterOptions{
		Tag:      flags["tag"],
		Category: flags["category"],
		Project:  flags["project"],
	})
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
