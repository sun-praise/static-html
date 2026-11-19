package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/sun-praise/static-html/internal/session"
)

// runUser dispatches the `sth user` subcommand for local user/API-key
// management. It operates directly on the local SQLite store (override path
// with --db) and does not require the server to be running.
//
// Usage:
//
//	sth user add <name> [--admin]
//	sth user issue-key <name>
//	sth user revoke-key <id|prefix>
//	sth user promote <name>   (grant the admin role)
//	sth user demote <name>    (withdraw the admin role)
//	sth user list
func runUser(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: sth user <add|issue-key|revoke-key|promote|demote|list> [--db /path/to/sessions.db]")
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "add":
		return userAdd(rest, stdout)
	case "issue-key":
		return userIssueKey(rest, stdout)
	case "revoke-key":
		return userRevokeKey(rest, stdout)
	case "promote":
		return userSetRole(rest, session.RoleAdmin, stdout)
	case "demote":
		return userSetRole(rest, "", stdout)
	case "list":
		return userList(rest, stdout)
	default:
		return fmt.Errorf("unknown user subcommand: %s (expected add|issue-key|revoke-key|promote|demote|list)", sub)
	}
}

func userAdd(args []string, stdout io.Writer) error {
	args, adminFlag, err := popBoolFlag(args, "admin")
	if err != nil {
		return err
	}
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		return errors.New("usage: sth user add <name> [--admin] [--db /path/to/sessions.db]")
	}
	name := positionals[0]

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	user, err := store.CreateUser(name)
	if err != nil {
		if errors.Is(err, session.ErrUsernameTaken) {
			return fmt.Errorf("username %q already taken", name)
		}
		return err
	}
	if adminFlag {
		if err := store.SetUserRole(name, session.RoleAdmin); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "Created user %q (id: %s, role: %s)\n", user.Username, user.ID, roleLabel(adminFlag))
	return nil
}

// userSetRole implements `sth user promote|demote <name>`: grant or withdraw
// the admin role.
func userSetRole(args []string, role string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		return errors.New("usage: sth user promote|demote <name> [--db /path/to/sessions.db]")
	}
	name := positionals[0]

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.SetUserRole(name, role); err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			return fmt.Errorf("user %q not found", name)
		}
		return err
	}
	verb := "Promoted"
	if role == "" {
		verb = "Demoted"
	}
	fmt.Fprintf(stdout, "%s %s (role: %s)\n", verb, name, roleLabel(role != ""))
	return nil
}

// roleLabel renders a boolean admin flag as the stored role name.
func roleLabel(admin bool) string {
	if admin {
		return session.RoleAdmin
	}
	return "(member)"
}

func userIssueKey(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		return errors.New("usage: sth user issue-key <name> [--db /path/to/sessions.db]")
	}
	name := positionals[0]

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	user, err := store.FindUserByUsername(name)
	if err != nil {
		if errors.Is(err, session.ErrUserNotFound) {
			return fmt.Errorf("user %q not found", name)
		}
		return err
	}

	plaintext, record, err := store.IssueAPIKey(user.ID)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Issued new API key for %q (id: %s)\n", user.Username, record.ID)
	fmt.Fprintln(stdout, "Store this key now; it will not be shown again:")
	fmt.Fprintf(stdout, "  %s\n", plaintext)
	return nil
}

func userRevokeKey(args []string, stdout io.Writer) error {
	flags, positionals, err := parseArgs(args)
	if err != nil {
		return err
	}
	if len(positionals) < 1 {
		return errors.New("usage: sth user revoke-key <id|prefix> [--db /path/to/sessions.db]")
	}
	idOrPrefix := positionals[0]

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	if err := store.RevokeAPIKey(idOrPrefix); err != nil {
		switch {
		case errors.Is(err, session.ErrAPIKeyAmbiguous):
			return fmt.Errorf("multiple keys match %q; provide a longer prefix or the full key id", idOrPrefix)
		case errors.Is(err, session.ErrAPIKeyNotFound):
			return fmt.Errorf("no active key matches %q", idOrPrefix)
		default:
			return err
		}
	}
	fmt.Fprintf(stdout, "Revoked key matching %q\n", idOrPrefix)
	return nil
}

func userList(args []string, stdout io.Writer) error {
	flags, _, err := parseArgs(args)
	if err != nil {
		return err
	}

	store, err := openStore(flags)
	if err != nil {
		return err
	}
	defer store.Close()

	users, err := store.ListUsers()
	if err != nil {
		return err
	}
	if len(users) == 0 {
		fmt.Fprintln(stdout, "No users. Create one with `sth user add <name>`.")
		return nil
	}

	fmt.Fprintln(stdout, "Users:")
	for _, u := range users {
		keyCount, err := store.CountAPIKeysByUser(u.ID)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "  - %s (id: %s, role: %s, active keys: %d, created: %s)\n",
			u.Username, u.ID, roleLabel(u.Role == session.RoleAdmin), keyCount, u.CreatedAt.Format("2006-01-02"))
	}
	return nil
}
