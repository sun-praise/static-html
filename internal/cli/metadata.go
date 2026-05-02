package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/sun-praise/static-html/internal/session"
)

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
	var project string
	if len(positionals) >= 2 {
		project = positionals[1]
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

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
