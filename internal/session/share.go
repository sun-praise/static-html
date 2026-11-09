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

// ShareEntry is a single grant on a session: the user it belongs to (with
// username resolved for display) and when the grant was created.
type ShareEntry struct {
	UserID    string `json:"userId"`
	Username  string `json:"username"`
	CreatedAt string `json:"createdAt"`
}

// SharedDocument is a DocumentInfo plus the username of the sharing owner,
// returned by ListSharedWithUser for the "shared with you" listing.
type SharedDocument struct {
	DocumentInfo
	OwnerUsername string `json:"owner"`
}

// initShares creates the session_shares table idempotently. Each row grants
// one user read access to one session's preview. The owner is implicit via
// sessions.user_id and is never stored here.
func (s *Store) initShares() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS session_shares (
			session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			created_at_unix INTEGER NOT NULL,
			PRIMARY KEY (session_id, user_id)
		);
		CREATE INDEX IF NOT EXISTS idx_session_shares_user_id ON session_shares(user_id);
	`)
	return err
}

// ShareSession grants userID read access to sessionID. Idempotent: re-sharing
// with an already-shared user succeeds without duplicating the row. Returns
// ErrSessionNotFound when the session does not exist.
func (s *Store) ShareSession(sessionID, userID string) error {
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}
	_, err := s.db.Exec(
		`INSERT OR IGNORE INTO session_shares (session_id, user_id, created_at_unix) VALUES (?, ?, ?)`,
		sessionID, userID, time.Now().UTC().UnixNano(),
	)
	return err
}

// UnshareSession revokes userID's grant on sessionID. Returns
// ErrSessionNotFound when the session does not exist and ErrNotShared when the
// user was not shared on it.
func (s *Store) UnshareSession(sessionID, userID string) error {
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}
	result, err := s.db.Exec(
		`DELETE FROM session_shares WHERE session_id = ? AND user_id = ?`,
		sessionID, userID,
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
// ordered by username. Soft-deleted sessions keep their shares but callers
// gate on existence first.
func (s *Store) ListSessionShares(sessionID string) ([]ShareEntry, error) {
	rows, err := s.db.Query(`
		SELECT ss.user_id, u.username, ss.created_at_unix
		FROM session_shares ss
		JOIN users u ON u.id = ss.user_id
		WHERE ss.session_id = ?
		ORDER BY u.username`,
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
		if err := rows.Scan(&entry.UserID, &entry.Username, &createdUnix); err != nil {
			return nil, err
		}
		entry.CreatedAt = time.Unix(0, createdUnix).UTC().Format(time.RFC3339)
		shares = append(shares, entry)
	}
	return shares, rows.Err()
}

// IsSessionSharedWith reports whether userID holds an explicit share grant on
// sessionID. Ownership is not consulted; callers check it separately.
func (s *Store) IsSessionSharedWith(sessionID, userID string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM session_shares WHERE session_id = ? AND user_id = ?)`,
		sessionID, userID,
	).Scan(&exists)
	return exists, err
}

// ListSharedWithUser returns the live (non-deleted) sessions shared with
// userID, newest first, each annotated with the sharing owner's username.
func (s *Store) ListSharedWithUser(userID string) ([]SharedDocument, error) {
	rows, err := s.db.Query(`
		SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix,
			GROUP_CONCAT(dt.tag, char(1)),
			dc.category,
			dp.project,
			COALESCE(s.chain_id, ''),
			COALESCE(s.version_no, 0),
			u.username
		FROM sessions s
		JOIN session_shares ss ON ss.session_id = s.session_id
		JOIN users u ON u.id = s.user_id
		LEFT JOIN document_tags dt ON s.session_id = dt.session_id
		LEFT JOIN document_categories dc ON s.session_id = dc.session_id
		LEFT JOIN document_projects dp ON s.session_id = dp.session_id
		WHERE ss.user_id = ? AND s.deleted_at IS NULL
		GROUP BY s.session_id
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
			&doc.ChainID, &doc.VersionNo, &doc.OwnerUsername,
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
