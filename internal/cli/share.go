package cli

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/sun-praise/static-html/internal/session"
)

// runShare implements `sth share <session-id> <username>`: grants the user
// read access to the session's preview. Works against a remote server
// (--server, Bearer-authenticated via --api-key/STH_API_KEY) or a local
// database (--db), mirroring the tag/categorize commands.
func runShare(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 2 {
		return errors.New("usage: sth share <session-id> <username>")
	}

	sessionID, username := positionals[0], positionals[1]

	if serverURL, ok := flags["server"]; ok {
		result, err := doJSONRequest(http.MethodPut, serverURL,
			"/api/sessions/"+sessionID+"/shares",
			resolveAPIKey(flags),
			map[string]any{"user": username})
		if err != nil {
			return err
		}
		printSharesResult(stdout, result)
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	target, err := store.FindUserByUsername(username)
	if err != nil {
		return err
	}
	if owner, status, err := store.SessionOwner(sessionID); err == nil && status == session.OwnerOwned && owner == target.ID {
		return errors.New("the owner already has access to this session")
	}
	if err := store.ShareSession(sessionID, target.ID); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Shared %s with %s.\n", sessionID, username)
	return nil
}

// runUnshare implements `sth unshare <session-id> <username>`: revokes a
// user's share grant.
func runUnshare(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 2 {
		return errors.New("usage: sth unshare <session-id> <username>")
	}

	sessionID, username := positionals[0], positionals[1]

	if serverURL, ok := flags["server"]; ok {
		result, err := doJSONRequest(http.MethodDelete, serverURL,
			"/api/sessions/"+sessionID+"/shares/"+username,
			resolveAPIKey(flags), nil)
		if err != nil {
			return err
		}
		printSharesResult(stdout, result)
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	target, err := store.FindUserByUsername(username)
	if err != nil {
		return err
	}
	if err := store.UnshareSession(sessionID, target.ID); err != nil {
		if errors.Is(err, session.ErrNotShared) {
			return fmt.Errorf("user %s is not shared on session %s", username, sessionID)
		}
		return err
	}
	fmt.Fprintf(stdout, "Unshared %s from %s.\n", username, sessionID)
	return nil
}

// runShares implements `sth shares <session-id>`: lists the share grants.
func runShares(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		return errors.New("usage: sth shares <session-id>")
	}

	sessionID := positionals[0]

	if serverURL, ok := flags["server"]; ok {
		result, err := doJSONRequest(http.MethodGet, serverURL,
			"/api/sessions/"+sessionID+"/shares",
			resolveAPIKey(flags), nil)
		if err != nil {
			return err
		}
		printSharesResult(stdout, result)
		return nil
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	shares, err := store.ListSessionShares(sessionID)
	if err != nil {
		return err
	}
	printShares(stdout, shares)
	return nil
}

func printSharesResult(stdout io.Writer, result map[string]any) {
	raw, ok := result["shares"].([]any)
	if !ok {
		fmt.Fprintln(stdout, "No shares.")
		return
	}
	shares := make([]session.ShareEntry, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entry := session.ShareEntry{
			UserID:    stringField(m, "userId"),
			Username:  stringField(m, "username"),
			CreatedAt: stringField(m, "createdAt"),
		}
		shares = append(shares, entry)
	}
	printShares(stdout, shares)
}

func printShares(stdout io.Writer, shares []session.ShareEntry) {
	if len(shares) == 0 {
		fmt.Fprintln(stdout, "No shares.")
		return
	}
	for _, s := range shares {
		fmt.Fprintf(stdout, "%s\t%s\n", s.Username, s.CreatedAt)
	}
}

func stringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
