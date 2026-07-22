package session

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

var ErrSessionNotFound = errors.New("session not found")

type DocumentMetadata struct {
	SessionID string
	Tags      []string
	Category  string
	Project   string
}

type FilterOptions struct {
	Tag      string
	Category string
	Project  string
	Limit    int
	Offset   int
	// OwnerID, when non-empty, scopes results to sessions owned by that user.
	// Empty means "no owner filtering" (auth disabled or unscoped).
	OwnerID string
}

type DocumentInfo struct {
	SessionID string    `json:"sessionId"`
	Name      string    `json:"name"`
	Tags      []string  `json:"tags"`
	Category  string    `json:"category"`
	Project   string    `json:"project"`
	ChainID   string    `json:"chainId"`
	VersionNo int       `json:"versionNo"`
	CreatedAt string    `json:"createdAt"`
}

func (s *Store) initMetadata() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS document_tags (
			session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
			tag TEXT NOT NULL,
			PRIMARY KEY (session_id, tag)
		);
		CREATE INDEX IF NOT EXISTS idx_document_tags_tag ON document_tags(tag);

		CREATE TABLE IF NOT EXISTS document_categories (
			session_id TEXT PRIMARY KEY REFERENCES sessions(session_id) ON DELETE CASCADE,
			category TEXT NOT NULL
		);

		CREATE TABLE IF NOT EXISTS document_projects (
			session_id TEXT PRIMARY KEY REFERENCES sessions(session_id) ON DELETE CASCADE,
			project TEXT NOT NULL
		);

		-- document_chains groups sessions into version chains. Note that
		-- SQLite's UNIQUE(project, entry_file, user_id) treats NULL user_id
		-- values as distinct, so it only enforces one-chain-per-owner for
		-- authenticated owners (user_id NOT NULL). The anonymous path
		-- (user_id NULL, i.e. --auth disabled) is instead serialized by
		-- LinkToChain's BEGIN IMMEDIATE write lock. No separate lookup index
		-- is needed: the UNIQUE constraint already provides a
		-- (project, entry_file, user_id) index.
		CREATE TABLE IF NOT EXISTS document_chains (
			chain_id TEXT PRIMARY KEY,
			project TEXT NOT NULL,
			entry_file TEXT NOT NULL,
			user_id TEXT DEFAULT NULL,
			created_at_unix INTEGER NOT NULL,
			UNIQUE(project, entry_file, user_id)
		);
	`)
	return err
}

func (s *Store) AddTags(sessionID string, tags ...string) error {
	if len(tags) == 0 {
		return errors.New("at least one tag is required")
	}
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO document_tags (session_id, tag) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	added := 0
	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, err := stmt.Exec(sessionID, tag); err != nil {
			return err
		}
		added++
	}
	if added == 0 {
		return errors.New("at least one non-empty tag is required")
	}

	return tx.Commit()
}

func (s *Store) RemoveTags(sessionID string, tags ...string) error {
	if len(tags) == 0 {
		return errors.New("at least one tag is required")
	}
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`DELETE FROM document_tags WHERE session_id = ? AND tag = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, tag := range tags {
		if tag == "" {
			continue
		}
		if _, err := stmt.Exec(sessionID, tag); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) SetCategory(sessionID, category string) error {
	if category == "" {
		return s.ClearCategory(sessionID)
	}
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}

	_, err := s.db.Exec(
		`INSERT INTO document_categories (session_id, category) VALUES (?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET category = excluded.category`,
		sessionID, category,
	)
	return err
}

func (s *Store) ClearCategory(sessionID string) error {
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}
	_, err := s.db.Exec(`DELETE FROM document_categories WHERE session_id = ?`, sessionID)
	return err
}

func (s *Store) SetProject(sessionID, project string) error {
	if project == "" {
		return s.ClearProject(sessionID)
	}
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}

	_, err := s.db.Exec(
		`INSERT INTO document_projects (session_id, project) VALUES (?, ?)
		 ON CONFLICT(session_id) DO UPDATE SET project = excluded.project`,
		sessionID, project,
	)
	return err
}

func (s *Store) ClearProject(sessionID string) error {
	if !s.sessionExists(sessionID) {
		return ErrSessionNotFound
	}
	_, err := s.db.Exec(`DELETE FROM document_projects WHERE session_id = ?`, sessionID)
	return err
}

func (s *Store) GetMetadata(sessionID string) (DocumentMetadata, error) {
	var meta DocumentMetadata
	meta.SessionID = sessionID

	rows, err := s.db.Query(`SELECT tag FROM document_tags WHERE session_id = ?`, sessionID)
	if err != nil {
		return DocumentMetadata{}, err
	}
	defer rows.Close()

	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return DocumentMetadata{}, err
		}
		meta.Tags = append(meta.Tags, tag)
	}
	if err := rows.Err(); err != nil {
		return DocumentMetadata{}, err
	}

	err = s.db.QueryRow(`SELECT category FROM document_categories WHERE session_id = ?`, sessionID).Scan(&meta.Category)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DocumentMetadata{}, err
	}

	err = s.db.QueryRow(`SELECT project FROM document_projects WHERE session_id = ?`, sessionID).Scan(&meta.Project)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return DocumentMetadata{}, err
	}

	return meta, nil
}

func (s *Store) CountDocuments(filter FilterOptions) (int, error) {
	query := `SELECT COUNT(DISTINCT s.session_id) FROM sessions s`
	var conditions []string
	var args []any

	conditions = append(conditions, `s.deleted_at IS NULL`)

	if filter.OwnerID != "" {
		conditions = append(conditions, `s.user_id = ?`)
		args = append(args, filter.OwnerID)
	}
	if filter.Tag != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_tags WHERE tag = ?)`)
		args = append(args, filter.Tag)
	}
	if filter.Category != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_categories WHERE category = ?)`)
		args = append(args, filter.Category)
	}
	if filter.Project != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_projects WHERE project = ?)`)
		args = append(args, filter.Project)
	}

	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}

	var count int
	err := s.db.QueryRow(query, args...).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListDocuments(filter FilterOptions) ([]DocumentInfo, error) {
	query := `
		SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix,
			GROUP_CONCAT(dt.tag, char(1)),
			dc.category,
			dp.project,
			COALESCE(s.chain_id, ''),
			COALESCE(s.version_no, 0)
		FROM sessions s
		LEFT JOIN document_tags dt ON s.session_id = dt.session_id
		LEFT JOIN document_categories dc ON s.session_id = dc.session_id
		LEFT JOIN document_projects dp ON s.session_id = dp.session_id
	`
	var conditions []string
	var args []any

	conditions = append(conditions, `s.deleted_at IS NULL`)

	if filter.OwnerID != "" {
		conditions = append(conditions, `s.user_id = ?`)
		args = append(args, filter.OwnerID)
	}
	if filter.Tag != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_tags WHERE tag = ?)`)
		args = append(args, filter.Tag)
	}
	if filter.Category != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_categories WHERE category = ?)`)
		args = append(args, filter.Category)
	}
	if filter.Project != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_projects WHERE project = ?)`)
		args = append(args, filter.Project)
	}

	if len(conditions) > 0 {
		query += ` WHERE ` + strings.Join(conditions, ` AND `)
	}

	query += ` GROUP BY s.session_id ORDER BY s.created_at_unix DESC`

	if filter.Limit > 0 {
		query += ` LIMIT ?`
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			query += ` OFFSET ?`
			args = append(args, filter.Offset)
		}
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []DocumentInfo
	for rows.Next() {
		var (
			sessionID      string
			entryFile      string
			createdAtUnix  int64
			tagsStr        sql.NullString
			category       sql.NullString
			project        sql.NullString
			chainID        string
			versionNo      int
		)
		if err := rows.Scan(&sessionID, &entryFile, &createdAtUnix, &tagsStr, &category, &project, &chainID, &versionNo); err != nil {
			return nil, err
		}

		doc := DocumentInfo{
			SessionID: sessionID,
			Name:      entryFile,
			Category:  category.String,
			Project:   project.String,
			ChainID:   chainID,
			VersionNo: versionNo,
			CreatedAt: time.Unix(0, createdAtUnix).UTC().Format(time.RFC3339),
		}
		if tagsStr.Valid && tagsStr.String != "" {
			doc.Tags = strings.Split(tagsStr.String, "\x01")
		} else {
			doc.Tags = nil
		}
		docs = append(docs, doc)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return docs, nil
}

func (s *Store) SearchDocuments(query string, filter FilterOptions) ([]DocumentInfo, error) {
	if query == "" {
		return nil, errors.New("search query is required")
	}

	// Escape LIKE wildcards so user literals like % and _ are matched exactly.
	// Backslash must be escaped first because it is the ESCAPE character.
	escaped := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(query, `\`, `\\`), `%`, `\%`), `_`, `\_`)
	pattern := "%" + escaped + "%"

	var conditions []string
	var args []any

	conditions = append(conditions, `s.deleted_at IS NULL`)

	if filter.OwnerID != "" {
		conditions = append(conditions, `s.user_id = ?`)
		args = append(args, filter.OwnerID)
	}

	conditions = append(conditions, `(COALESCE(s.stored_entry_file, s.entry_file) LIKE ? ESCAPE '\'
	   OR s.session_id IN (
		   SELECT session_id FROM document_tags WHERE tag LIKE ? ESCAPE '\'
	   )
	   OR s.session_id IN (
		   SELECT session_id FROM document_categories WHERE category LIKE ? ESCAPE '\'
	   )
	   OR s.session_id IN (
		   SELECT session_id FROM document_projects WHERE project LIKE ? ESCAPE '\'
	   ))`)
	args = append(args, pattern, pattern, pattern, pattern)

	if filter.Tag != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_tags WHERE tag = ?)`)
		args = append(args, filter.Tag)
	}
	if filter.Category != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_categories WHERE category = ?)`)
		args = append(args, filter.Category)
	}
	if filter.Project != "" {
		conditions = append(conditions, `s.session_id IN (SELECT session_id FROM document_projects WHERE project = ?)`)
		args = append(args, filter.Project)
	}

	queryStr := `
		SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix,
			GROUP_CONCAT(dt.tag, char(1)),
			dc.category,
			dp.project,
			COALESCE(s.chain_id, ''),
			COALESCE(s.version_no, 0)
		FROM sessions s
		LEFT JOIN document_tags dt ON s.session_id = dt.session_id
		LEFT JOIN document_categories dc ON s.session_id = dc.session_id
		LEFT JOIN document_projects dp ON s.session_id = dp.session_id
		WHERE ` + strings.Join(conditions, ` AND `) + `
		GROUP BY s.session_id
		ORDER BY s.created_at_unix DESC
	`

	if filter.Limit > 0 {
		queryStr += ` LIMIT ?`
		args = append(args, filter.Limit)
		if filter.Offset > 0 {
			queryStr += ` OFFSET ?`
			args = append(args, filter.Offset)
		}
	}

	rows, err := s.db.Query(queryStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []DocumentInfo
	for rows.Next() {
		var (
			sessionID     string
			entryFile     string
			createdAtUnix int64
			tagsStr       sql.NullString
			category      sql.NullString
			project       sql.NullString
			chainID       string
			versionNo     int
		)
		if err := rows.Scan(&sessionID, &entryFile, &createdAtUnix, &tagsStr, &category, &project, &chainID, &versionNo); err != nil {
			return nil, err
		}

		doc := DocumentInfo{
			SessionID: sessionID,
			Name:      entryFile,
			Category:  category.String,
			Project:   project.String,
			ChainID:   chainID,
			VersionNo: versionNo,
			CreatedAt: time.Unix(0, createdAtUnix).UTC().Format(time.RFC3339),
		}
		if tagsStr.Valid && tagsStr.String != "" {
			doc.Tags = strings.Split(tagsStr.String, "\x01")
		} else {
			doc.Tags = nil
		}
		docs = append(docs, doc)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return docs, nil
}

func (s *Store) sessionExists(sessionID string) bool {
	var exists bool
	_ = s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE session_id = ?)`, sessionID).Scan(&exists)
	return exists
}

type PeerEntry struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
}

type PeerCurrent struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Project   string `json:"project"`
}

type PeersResult struct {
	Current    PeerCurrent `json:"current"`
	ByCategory []PeerEntry `json:"byCategory"`
	ByProject  []PeerEntry `json:"byProject"`
}

const DefaultPeerLimit = 20

// GetPeers returns documents sharing the same category or project as sessionID,
// ordered by creation time descending. Each group is capped at limit entries
// (defaulting to 20 when limit <= 0). The session itself and soft-deleted
// sessions are excluded. Returns ErrSessionNotFound when sessionID does not exist.
// GetPeers returns documents sharing the same category or project as sessionID,
// ordered by creation time descending. Each group is capped at limit entries
// (defaulting to 20 when limit <= 0). The session itself and soft-deleted
// sessions are excluded. Returns ErrSessionNotFound when sessionID does not exist.
func (s *Store) GetPeers(sessionID string, limit int) (*PeersResult, error) {
	return s.GetPeersForOwner(sessionID, limit, "")
}

// GetPeersForOwner is the owner-scoped variant of GetPeers. When ownerID is
// non-empty, peer candidates are additionally restricted to sessions owned by
// that user. The current session's own metadata is returned regardless.
func (s *Store) GetPeersForOwner(sessionID string, limit int, ownerID string) (*PeersResult, error) {
	if limit <= 0 {
		limit = DefaultPeerLimit
	}

	current, found, err := s.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrSessionNotFound
	}

	meta, err := s.GetMetadata(sessionID)
	if err != nil {
		return nil, err
	}

	entryFile := current.StoredEntryFile
	if entryFile == "" {
		entryFile = current.EntryFile
	}

	result := &PeersResult{
		Current: PeerCurrent{
			SessionID: sessionID,
			Name:      filepath.Base(entryFile),
			Category:  meta.Category,
			Project:   meta.Project,
		},
		ByCategory: []PeerEntry{},
		ByProject:  []PeerEntry{},
	}

	ownerClause := ""
	ownerArgs := []any{}
	if ownerID != "" {
		ownerClause = ` AND s.user_id = ?`
		ownerArgs = []any{ownerID}
	}

	if meta.Category != "" {
		byCategory, err := s.queryPeers(
			`SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix
			 FROM sessions s
			 JOIN document_categories dc ON s.session_id = dc.session_id
			 WHERE dc.category = ? AND s.session_id != ? AND s.deleted_at IS NULL`+ownerClause+`
			 ORDER BY s.created_at_unix DESC
			 LIMIT ?`,
			append([]any{meta.Category, sessionID}, append(ownerArgs, limit)...)...,
		)
		if err != nil {
			return nil, err
		}
		result.ByCategory = byCategory
	}

	if meta.Project != "" {
		byProject, err := s.queryPeers(
			`SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix
			 FROM sessions s
			 JOIN document_projects dp ON s.session_id = dp.session_id
			 WHERE dp.project = ? AND s.session_id != ? AND s.deleted_at IS NULL`+ownerClause+`
			 ORDER BY s.created_at_unix DESC
			 LIMIT ?`,
			append([]any{meta.Project, sessionID}, append(ownerArgs, limit)...)...,
		)
		if err != nil {
			return nil, err
		}
		result.ByProject = byProject
	}

	return result, nil
}

// queryPeers runs a peer lookup query and maps each row to a PeerEntry with the
// entry-file basename and an RFC3339 creation timestamp.
func (s *Store) queryPeers(query string, args ...any) ([]PeerEntry, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	peers := []PeerEntry{}
	for rows.Next() {
		var (
			sessionID     string
			entryFile     string
			createdAtUnix int64
		)
		if err := rows.Scan(&sessionID, &entryFile, &createdAtUnix); err != nil {
			return nil, err
		}
		peers = append(peers, PeerEntry{
			SessionID: sessionID,
			Name:      filepath.Base(entryFile),
			CreatedAt: time.Unix(0, createdAtUnix).UTC().Format(time.RFC3339),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return peers, nil
}
