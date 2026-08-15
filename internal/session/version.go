package session

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ChainInfo describes a version chain: a stable grouping of sessions that
// share the same (project, entry_file, owner) tuple. The OwnerID is empty
// when auth is disabled (chains are global in that mode).
type ChainInfo struct {
	ChainID    string `json:"chainId"`
	Project    string `json:"project"`
	EntryFile  string `json:"entryFile"`
	OwnerID    string `json:"ownerId"`
	CreatedAt  string `json:"createdAt"`
	VersionNum int    `json:"versionNum"`
}

// ChainVersion is a single session's view inside its chain.
type ChainVersion struct {
	SessionID string   `json:"sessionId"`
	Name      string   `json:"name"`
	VersionNo int      `json:"versionNo"`
	Tags      []string `json:"tags"`
	Category  string   `json:"category"`
	Project   string   `json:"project"`
	CreatedAt string   `json:"createdAt"`
	Current   bool     `json:"current"`
}

// VersionMetadataDiff captures what changed in tags/category/project between
// one version and its predecessor. AddedTags/RemovedTags are set-relative;
// the category/project fields show from→to transitions.
type VersionMetadataDiff struct {
	FromVersion int      `json:"fromVersion"`
	ToVersion   int      `json:"toVersion"`
	AddedTags   []string `json:"addedTags"`
	RemovedTags []string `json:"removedTags"`
	CategoryOld string   `json:"categoryOld"`
	CategoryNew string   `json:"categoryNew"`
	ProjectOld  string   `json:"projectOld"`
	ProjectNew  string   `json:"projectNew"`
}

// LinkToChain attaches sessionID to the chain identified by
// (project, entryFile, ownerID). If the chain exists, the session becomes its
// next version (MAX(version_no)+1); otherwise a new chain is created with
// version_no = 1. ownerID may be empty (auth disabled → global chain).
//
// The basename of entryFile is used for chain membership so that two sends
// of "/path/a/index.html" and "/other/b/index.html" under the same project
// land on the same chain, matching the user-visible identity ("index.html").
//
// The entire operation runs in a single IMMEDIATE transaction so that
// concurrent uploads (notably the anonymous / auth-disabled path, where
// SQLite's UNIQUE(project, entry_file, user_id) does NOT enforce NULL-owner
// uniqueness — SQL treats each NULL as distinct) are serialized at the write
// lock and cannot split one (project, entry_file) into two chains. The
// chain row itself is created via INSERT ... ON CONFLICT DO NOTHING, then
// re-read, so the authenticated-owner path is additionally backed by the
// UNIQUE constraint. Callers that do not require chain membership (e.g.
// graceful degradation on upload) should ignore the returned error.
func (s *Store) LinkToChain(sessionID, project, entryFile, ownerID string) (chainID string, versionNo int, err error) {
	if !s.sessionExists(sessionID) {
		return "", 0, ErrSessionNotFound
	}

	entryBase := filepath.Base(entryFile)
	if project == "" || entryBase == "" || entryBase == "." {
		return "", 0, errors.New("project and entry file are required for chain linking")
	}

	// BEGIN IMMEDIATE acquires the RESERVED lock up front, serializing this
	// whole read-modify-write against any other writer. This is what makes
	// the anonymous-owner path race-free even though the UNIQUE constraint
	// does not cover NULL user_id.
	if _, err = s.db.Exec(`BEGIN IMMEDIATE`); err != nil {
		return "", 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = s.db.Exec(`ROLLBACK`)
		}
	}()

	// ownerID NULL needs IS for SQL equality semantics. sqlite maps a Go nil
	// arg to NULL; a Go "" would be the empty string and must not collide.
	var ownerArg any
	if ownerID == "" {
		ownerArg = nil
	} else {
		ownerArg = ownerID
	}

	// Try to insert a new chain row. For authenticated owners the UNIQUE
	// constraint makes this a no-op when the chain already exists; for the
	// anonymous (NULL owner) path the IMMEDIATE lock above is the actual
	// guard. We then unconditionally SELECT the chain id back.
	newChainID, idErr := generateID()
	if idErr != nil {
		return "", 0, idErr
	}
	if _, err = s.db.Exec(
		`INSERT INTO document_chains (chain_id, project, entry_file, user_id, created_at_unix)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project, entry_file, user_id) DO NOTHING`,
		newChainID, project, entryBase, ownerArg, time.Now().UnixNano(),
	); err != nil {
		return "", 0, err
	}

	err = s.db.QueryRow(
		`SELECT chain_id FROM document_chains
		 WHERE project = ? AND entry_file = ? AND user_id IS ?`,
		project, entryBase, ownerArg,
	).Scan(&chainID)
	if err != nil {
		return "", 0, err
	}

	err = s.db.QueryRow(
		`SELECT COALESCE(MAX(version_no), 0)
		 FROM sessions
		 WHERE chain_id = ? AND deleted_at IS NULL`,
		chainID,
	).Scan(&versionNo)
	if err != nil {
		return "", 0, err
	}
	versionNo++

	if _, err = s.db.Exec(
		`UPDATE sessions SET chain_id = ?, version_no = ? WHERE session_id = ?`,
		chainID, versionNo, sessionID,
	); err != nil {
		return "", 0, err
	}

	if _, err = s.db.Exec(`COMMIT`); err != nil {
		return "", 0, err
	}
	committed = true
	return chainID, versionNo, nil
}

// GetChainOfSession returns the chain metadata and all non-deleted versions
// for the chain that sessionID belongs to. Returns ErrSessionNotFound when
// the session does not exist. The versions slice is ordered by version_no
// ascending; the Current flag marks the requested session.
//
// When the session is not linked to any chain (older rows or linking failed),
// a ChainInfo with only the session's own identity is returned together with
// that single version, so callers can render a degenerate "v1 of 1" view
// without special-casing.
func (s *Store) GetChainOfSession(sessionID string) (ChainInfo, []ChainVersion, error) {
	current, found, err := s.Get(sessionID)
	if err != nil {
		return ChainInfo{}, nil, err
	}
	if !found {
		return ChainInfo{}, nil, ErrSessionNotFound
	}

	meta, err := s.GetMetadata(sessionID)
	if err != nil {
		return ChainInfo{}, nil, err
	}

	var (
		chainID   sql.NullString
		versionNo sql.NullInt64
		deletedAt sql.NullInt64
	)
	err = s.db.QueryRow(
		`SELECT chain_id, version_no, deleted_at FROM sessions WHERE session_id = ?`,
		sessionID,
	).Scan(&chainID, &versionNo, &deletedAt)
	if err != nil {
		return ChainInfo{}, nil, err
	}
	// A soft-deleted session is invisible: consistent with listChainVersions
	// (which filters deleted_at IS NULL) and with the rest of the app's
	// soft-delete semantics. Surface it as not-found so the chain handler
	// returns 404 instead of fabricating a "current" version from the rest
	// of the chain.
	if deletedAt.Valid {
		return ChainInfo{}, nil, ErrSessionNotFound
	}

	currentVersion := 0
	if versionNo.Valid {
		currentVersion = int(versionNo.Int64)
	}

	// Not linked: synthesize a single-version chain view.
	if !chainID.Valid || chainID.String == "" {
		synthesized := ChainInfo{
			ChainID:    "",
			Project:    meta.Project,
			EntryFile:  filepath.Base(current.EntryFile),
			VersionNum: 1,
		}
		v := chainVersionFrom(sessionID, current, meta, currentVersion)
		v.Current = true
		return synthesized, []ChainVersion{v}, nil
	}

	info, err := s.getChainInfo(chainID.String)
	if err != nil {
		return ChainInfo{}, nil, err
	}

	versions, err := s.listChainVersions(chainID.String)
	if err != nil {
		return ChainInfo{}, nil, err
	}
	info.VersionNum = len(versions)

	for i := range versions {
		if versions[i].SessionID == sessionID {
			versions[i].Current = true
		}
	}

	return info, versions, nil
}

func (s *Store) getChainInfo(chainID string) (ChainInfo, error) {
	var (
		info      ChainInfo
		createdAt int64
		ownerID   sql.NullString
	)
	err := s.db.QueryRow(
		`SELECT chain_id, project, entry_file, user_id, created_at_unix
		 FROM document_chains WHERE chain_id = ?`,
		chainID,
	).Scan(&info.ChainID, &info.Project, &info.EntryFile, &ownerID, &createdAt)
	if err != nil {
		return ChainInfo{}, err
	}
	info.OwnerID = ownerID.String
	info.CreatedAt = time.Unix(0, createdAt).UTC().Format(time.RFC3339)
	return info, nil
}

func (s *Store) listChainVersions(chainID string) ([]ChainVersion, error) {
	rows, err := s.db.Query(
		`SELECT s.session_id, COALESCE(s.stored_entry_file, s.entry_file), s.created_at_unix,
		        s.version_no,
		        GROUP_CONCAT(dt.tag, char(1)),
		        dc.category, dp.project
		 FROM sessions s
		 LEFT JOIN document_tags dt ON s.session_id = dt.session_id
		 LEFT JOIN document_categories dc ON s.session_id = dc.session_id
		 LEFT JOIN document_projects dp ON s.session_id = dp.session_id
		 WHERE s.chain_id = ? AND s.deleted_at IS NULL
		 GROUP BY s.session_id
		 ORDER BY s.version_no ASC`,
		chainID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []ChainVersion
	for rows.Next() {
		var (
			sessionID     string
			entryFile     string
			createdAtUnix int64
			versionNo     sql.NullInt64
			tagsStr       sql.NullString
			category      sql.NullString
			project       sql.NullString
		)
		if err := rows.Scan(&sessionID, &entryFile, &createdAtUnix, &versionNo, &tagsStr, &category, &project); err != nil {
			return nil, err
		}
		v := ChainVersion{
			SessionID: sessionID,
			Name:      filepath.Base(entryFile),
			VersionNo: int(versionNo.Int64),
			Category:  category.String,
			Project:   project.String,
			CreatedAt: time.Unix(0, createdAtUnix).UTC().Format(time.RFC3339),
		}
		if tagsStr.Valid && tagsStr.String != "" {
			v.Tags = strings.Split(tagsStr.String, "\x01")
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if versions == nil {
		versions = []ChainVersion{}
	}
	return versions, nil
}

// DiffChainMetadata computes the per-step metadata transitions across the
// chain. The result is ordered by ascending version_no. The first version
// (v1) is compared against an empty baseline so its initial tags/category/
// project surface as "added" entries. Returns an empty slice for chains
// with no versions.
func (s *Store) DiffChainMetadata(chainID string) ([]VersionMetadataDiff, error) {
	versions, err := s.listChainVersions(chainID)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return []VersionMetadataDiff{}, nil
	}

	sort.SliceStable(versions, func(i, j int) bool {
		return versions[i].VersionNo < versions[j].VersionNo
	})

	diffs := make([]VersionMetadataDiff, 0, len(versions))
	prev := ChainVersion{}
	for _, v := range versions {
		diffs = append(diffs, diffVersions(prev, v))
		prev = v
	}
	return diffs, nil
}

func diffVersions(from, to ChainVersion) VersionMetadataDiff {
	prevSet := tagSet(from.Tags)
	nextSet := tagSet(to.Tags)

	var added, removed []string
	for t := range nextSet {
		if !prevSet[t] {
			added = append(added, t)
		}
	}
	for t := range prevSet {
		if !nextSet[t] {
			removed = append(removed, t)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	return VersionMetadataDiff{
		FromVersion: from.VersionNo,
		ToVersion:   to.VersionNo,
		AddedTags:   added,
		RemovedTags: removed,
		CategoryOld: from.Category,
		CategoryNew: to.Category,
		ProjectOld:  from.Project,
		ProjectNew:  to.Project,
	}
}

func tagSet(tags []string) map[string]bool {
	out := make(map[string]bool, len(tags))
	for _, t := range tags {
		if t != "" {
			out[t] = true
		}
	}
	return out
}

func chainVersionFrom(sessionID string, sess Session, meta DocumentMetadata, versionNo int) ChainVersion {
	entryFile := sess.StoredEntryFile
	if entryFile == "" {
		entryFile = sess.EntryFile
	}
	tags := meta.Tags
	if tags == nil {
		tags = []string{}
	}
	return ChainVersion{
		SessionID: sessionID,
		Name:      filepath.Base(entryFile),
		VersionNo: versionNo,
		Tags:      tags,
		Category:  meta.Category,
		Project:   meta.Project,
		CreatedAt: sess.CreatedAtISO(),
	}
}

// DiffSessionHTML computes a line-level diff between the entry HTML of two
// sessions by reading each session's StoredEntryFile off disk. It is the
// first code in the codebase to read uploaded session content into memory;
// it reuses the established store.Get → StoredEntryFile access pattern.
//
// Missing or unreadable files are treated as empty content (not an error):
// legacy sessions created without on-disk files, or whose upload dir was
// cleared, still produce a valid (one-sided) diff rather than failing. This
// graceful degradation keeps the diff endpoint usable across mixed data.
//
// Either session being absent from the store returns ErrSessionNotFound.
// The returned DiffResult carries both the ordered ops and an explicit
// TooLarge flag (set when either input exceeded MaxDiffLines) so callers do
// not have to infer "too large" from op text.
func (s *Store) DiffSessionHTML(fromSessionID, toSessionID string) (DiffResult, error) {
	fromSess, found, err := s.Get(fromSessionID)
	if err != nil {
		return DiffResult{}, err
	}
	if !found {
		return DiffResult{}, ErrSessionNotFound
	}
	toSess, found, err := s.Get(toSessionID)
	if err != nil {
		return DiffResult{}, err
	}
	if !found {
		return DiffResult{}, ErrSessionNotFound
	}

	oldText := readSessionHTML(fromSess)
	newText := readSessionHTML(toSess)

	result := DiffLines(SplitLines(oldText), SplitLines(newText))
	if result.Ops == nil {
		result.Ops = []LineOp{}
	}
	return result, nil
}

// readSessionHTML reads the session's entry HTML, returning "" for any I/O
// failure so the caller can diff against an empty document. StoredEntryFile
// is preferred (the on-disk upload path) and falls back to EntryFile.
func readSessionHTML(sess Session) string {
	path := sess.StoredEntryFile
	if path == "" {
		path = sess.EntryFile
	}
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
