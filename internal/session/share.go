package session

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrNotShared is returned when a share is removed but the user was never on
// the session's share list, so callers can answer 404 instead of a silent ok.
var ErrNotShared = errors.New("user is not shared on this session")

// SharePathAll ("") marks a whole-session grant: every page and asset under
// /s/<id>/. Any other value is a page path inside the session (slash-separated,
// relative), granting exactly that HTML page plus session assets.
const SharePathAll = ""

// ShareScopeSession / ShareScopePage label a grant's reach for display.
const (
	ShareScopeSession = "session"
	ShareScopePage    = "page"
)

// ShareEntry is a single grant on a session: the user it belongs to (with
// username resolved for display), the page path it covers ("" = whole
// session), and when the grant was created.
type ShareEntry struct {
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	Path      string `json:"path"`
	Scope     string `json:"scope"`
	CreatedAt string `json:"createdAt"`
}

func (e ShareEntry) ScopeLabel() string {
	if e.Path == SharePathAll {
		return "whole session"
	}
	return e.Path
}

// SharedDocument is a DocumentInfo plus the username of the sharing owner and
// the granted page path, returned by ListSharedWithUser for the "shared with
// you" listing. Path "" means the whole session.
type SharedDocument struct {
	DocumentInfo
	OwnerUsername string `json:"owner"`
	Path          string `json:"path"`
}

// initShares creates the session_shares table idempotently. Each row grants
// one user read access to one session (path "") or one page inside it. The
// owner is implicit via sessions.user_id and is never stored here.
func (s *Store) initShares() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS session_shares (
			session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			path TEXT NOT NULL DEFAULT '',
			created_at_unix INTEGER NOT NULL,
			PRIMARY KEY (session_id, user_id, path)
		);
		CREATE INDEX IF NOT EXISTS idx_session_shares_user_id ON session_shares(user_id);
	`)
	return err
}

// ShareSession grants userID read access to sessionID — the whole session.
// Idempotent: re-sharing with an already-shared user succeeds without
// duplicating the row. Returns ErrSessionNotFound when the session does not
// exist.
func (s *Store) ShareSession(sessionID, userID string) error {
	return s.share(sessionID, userID, SharePathAll)
}

// ShareSessionPage grants userID read access to a single page (by path inside
// the session) rather than the whole session.
func (s *Store) ShareSessionPage(sessionID, userID, pagePath string) error {
	return s.share(sessionID, userID, pagePath)
}

func (s *Store) share(sessionID, userID, path string) error {
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO session_shares (session_id, user_id, path, created_at_unix) VALUES (?, ?, ?, ?)`,
		sessionID, userID, path, time.Now().UTC().UnixNano(),
	)
	return err
}

// UnshareSession revokes userID's whole-session grant on sessionID. Returns
// ErrSessionNotFound when the session does not exist and ErrNotShared when the
// user was not shared on it.
func (s *Store) UnshareSession(sessionID, userID string) error {
	return s.unshare(sessionID, userID, SharePathAll)
}

// UnshareSessionPage revokes userID's grant on a single page path.
func (s *Store) UnshareSessionPage(sessionID, userID, pagePath string) error {
	return s.unshare(sessionID, userID, pagePath)
}

func (s *Store) unshare(sessionID, userID, path string) error {
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}
	result, err := s.db.Exec(
		`DELETE FROM session_shares WHERE session_id = ? AND user_id = ? AND path = ?`,
		sessionID, userID, path,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotShared
	}
	return nil
}

// ListSessionShares returns the share list of a session, usernames resolved,
// ordered by username then path. Soft-deleted sessions keep their shares but
// callers gate on existence first.
func (s *Store) ListSessionShares(sessionID string) ([]ShareEntry, error) {
	rows, err := s.db.Query(`
		SELECT ss.user_id, u.username, ss.path, ss.created_at_unix
		FROM session_shares ss
		JOIN users u ON u.id = ss.user_id
		WHERE ss.session_id = ?
		ORDER BY u.username, ss.path`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	shares := []ShareEntry{}
	for rows.Next() {
		var (
			entry       ShareEntry
			createdUnix int64
		)
		if err := rows.Scan(&entry.UserID, &entry.Username, &entry.Path, &createdUnix); err != nil {
			return nil, err
		}
		entry.Scope = ShareScopeSession
		if entry.Path != SharePathAll {
			entry.Scope = ShareScopePage
		}
		entry.CreatedAt = time.Unix(0, createdUnix).UTC().Format(time.RFC3339)
		shares = append(shares, entry)
	}
	return shares, rows.Err()
}

// HasShareOn reports whether userID holds a grant covering pagePath inside
// sessionID: either a whole-session grant or an exact grant on that path.
// The preview handler uses this for HTML pages (including the entry page
// served at the /s/<id>/ root).
func (s *Store) HasShareOn(sessionID, userID, pagePath string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM session_shares WHERE session_id = ? AND user_id = ? AND (path = '' OR path = ?))`,
		sessionID, userID, pagePath,
	).Scan(&exists)
	return exists, err
}

// HasAnyShare reports whether userID holds any grant (whole-session or page)
// on sessionID. Used for session assets shared by all pages and for the
// preview drawer endpoints.
func (s *Store) HasAnyShare(sessionID, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM session_shares WHERE session_id = ? AND user_id = ?)`,
		sessionID, userID,
	).Scan(&exists)
	return exists, err
}

// IsSessionSharedWith reports whether userID holds a whole-session grant on
// sessionID. Ownership is not consulted; callers check it separately.
func (s *Store) IsSessionSharedWith(sessionID, userID string) (bool, error) {
	return s.HasShareOn(sessionID, userID, SharePathAll)
}

// ListSharedWithUser returns the live (non-deleted) sessions shared with
// userID, newest first, each annotated with the sharing owner's username and
// the granted page path ("" = whole session).
func (s *Store) ListSharedWithUser(userID string) ([]SharedDocument, error) {
	rows, err := s.db.Query(`
		SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix,
			GROUP_CONCAT(dt.tag, char(1)),
			dc.category,
			dp.project,
			COALESCE(s.chain_id, ''),
			COALESCE(s.version_no, 0),
			u.username,
			ss.path
		FROM sessions s
		JOIN session_shares ss ON ss.session_id = s.session_id
		JOIN users u ON u.id = s.user_id
		LEFT JOIN document_tags dt ON s.session_id = dt.session_id
		LEFT JOIN document_categories dc ON s.session_id = dc.session_id
		LEFT JOIN document_projects dp ON s.session_id = dp.session_id
		WHERE ss.user_id = ? AND s.deleted_at IS NULL
		GROUP BY s.session_id, ss.path
		ORDER BY s.created_at_unix DESC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	docs := []SharedDocument{}
	for rows.Next() {
		var (
			doc           SharedDocument
			entryFile     string
			createdAtUnix int64
			tagsStr       sql.NullString
			category      sql.NullString
			project       sql.NullString
		)
		if err := rows.Scan(
			&doc.SessionID, &entryFile, &createdAtUnix,
			&tagsStr, &category, &project,
			&doc.ChainID, &doc.VersionNo, &doc.OwnerUsername, &doc.Path,
		); err != nil {
			return nil, err
		}
		doc.Name = entryFile
		doc.Category = category.String
		doc.Project = project.String
		doc.CreatedAt = time.Unix(0, createdAtUnix).UTC().Format(time.RFC3339)
		if tagsStr.Valid && tagsStr.String != "" {
			doc.Tags = strings.Split(tagsStr.String, "\x01")
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}
