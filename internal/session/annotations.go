package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Annotations are reader notes attached to a preview document. They anchor to
// a **chain**, not to a single session: every version of a chain shows the
// same annotation set, and the front end re-anchors each quote against the
// version it is rendering (quotes that no longer exist surface as "missing").
// owner_id separates readers: every viewer sees and manages only their own
// annotations; empty owner (auth disabled / anonymous preview deployments) is
// the shared pool. Sessions without a chain (rare, legacy rows) fall back to
// per-session scoping.
const (
	AnnotationKindUnderline = "underline"
	AnnotationKindNote      = "note"
)

var ErrAnnotationNotFound = errors.New("annotation not found")

const (
	maxAnnotationQuote  = 2000
	maxAnnotationAnchor = 120
	maxAnnotationText   = 2000
	maxAnnotationTag    = 60
)

type Annotation struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Kind      string `json:"kind"`    // underline | note
	Quote     string `json:"quote"`   // highlighted excerpt (anchor text)
	Prefix    string `json:"prefix"`  // preceding context used for re-anchoring
	Suffix    string `json:"suffix"`  // following context used for re-anchoring
	Comment   string `json:"comment"` // reader's thought (kind=note)
	Tag       string `json:"tag"`
	CreatedAt string `json:"createdAt"` // RFC3339
}

// initAnnotations creates the annotation table. Called from the store's init
// chain; FK pragma is enabled there so CASCADE removes annotations together
// with their chain.
func (s *Store) initAnnotations() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS session_annotations (
			annotation_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
			chain_id TEXT NOT NULL DEFAULT '',
			owner_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL CHECK(kind IN ('underline','note')),
			quote TEXT NOT NULL,
			prefix TEXT NOT NULL DEFAULT '',
			suffix TEXT NOT NULL DEFAULT '',
			comment TEXT NOT NULL DEFAULT '',
			tag TEXT NOT NULL DEFAULT '',
			created_at_nano INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_session_annotations_chain
		ON session_annotations(chain_id, owner_id, created_at_nano);
	`)
	return err
}

func clampString(value string, max int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > max {
		value = string(runes[:max])
	}
	return value
}

// validate normalises the incoming annotation and returns a user-facing error
// message when a required field is missing or a limit exceeded.
func (a *Annotation) validate() error {
	a.Kind = strings.TrimSpace(a.Kind)
	if a.Kind != AnnotationKindUnderline && a.Kind != AnnotationKindNote {
		return fmt.Errorf("kind must be underline or note")
	}
	a.Quote = clampString(a.Quote, maxAnnotationQuote)
	if a.Quote == "" {
		return fmt.Errorf("quote is required")
	}
	a.Prefix = clampString(a.Prefix, maxAnnotationAnchor)
	a.Suffix = clampString(a.Suffix, maxAnnotationAnchor)
	a.Comment = clampString(a.Comment, maxAnnotationText)
	a.Tag = clampString(a.Tag, maxAnnotationTag)
	if a.Kind == AnnotationKindNote && a.Comment == "" {
		return fmt.Errorf("comment is required for note annotations")
	}
	return nil
}

// chainIDOfSession resolves the chain a session belongs to. Sessions are
// soft-deletable rows; soft-deleted ones report no chain like missing ones.
// Sessions without chain membership return "" (per-session fallback).
func (s *Store) chainIDOfSession(sessionID string) (string, error) {
	var chainID sql.NullString
	err := s.db.QueryRow(
		`SELECT chain_id FROM sessions WHERE session_id = ? AND deleted_at IS NULL`,
		sessionID,
	).Scan(&chainID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionNotFound
	}
	if err != nil {
		return "", err
	}
	return chainID.String, nil
}

// AddAnnotation stores a new annotation and returns the persisted record with
// its generated ID and timestamp. The annotation is attached to the session's
// chain so every version of the document sees the same set.
func (s *Store) AddAnnotation(sessionID, ownerID string, in Annotation) (Annotation, error) {
	if err := in.validate(); err != nil {
		return Annotation{}, err
	}
	chainID, err := s.chainIDOfSession(sessionID)
	if err != nil {
		return Annotation{}, err
	}

	id, err := generateID()
	if err != nil {
		return Annotation{}, err
	}
	// Nanosecond resolution keeps insertion order stable even for annotations
	// created within the same second (document order = reading order).
	now := time.Now().UnixNano()

	if _, err := s.db.Exec(`
		INSERT INTO session_annotations
			(annotation_id, session_id, chain_id, owner_id, kind, quote, prefix, suffix, comment, tag, created_at_nano)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, chainID, ownerID, in.Kind, in.Quote, in.Prefix, in.Suffix, in.Comment, in.Tag, now,
	); err != nil {
		return Annotation{}, err
	}

	return Annotation{
		ID:        id,
		SessionID: sessionID,
		Kind:      in.Kind,
		Quote:     in.Quote,
		Prefix:    in.Prefix,
		Suffix:    in.Suffix,
		Comment:   in.Comment,
		Tag:       in.Tag,
		CreatedAt: time.Unix(0, now).UTC().Format(time.RFC3339),
	}, nil
}

// ListAnnotations returns the owner's annotations for the chain the session
// belongs to, oldest first (document order matches reading order). Sessions
// without chain membership fall back to their own rows.
func (s *Store) ListAnnotations(sessionID, ownerID string) ([]Annotation, error) {
	chainID, err := s.chainIDOfSession(sessionID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT annotation_id, session_id, kind, quote, prefix, suffix, comment, tag, created_at_nano
		FROM session_annotations
		WHERE owner_id = ? AND `
	args := []any{ownerID}
	if chainID != "" {
		query += `chain_id = ? `
		args = append(args, chainID)
	} else {
		query += `session_id = ? AND chain_id = '' `
		args = append(args, sessionID)
	}
	query += `ORDER BY created_at_nano ASC, annotation_id ASC`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Annotation
	for rows.Next() {
		var a Annotation
		var nano int64
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Kind, &a.Quote, &a.Prefix, &a.Suffix, &a.Comment, &a.Tag, &nano); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(0, nano).UTC().Format(time.RFC3339)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAnnotation removes one annotation owned by ownerID. The annotation id
// is globally unique; sessionID is only used upstream for authorization.
func (s *Store) DeleteAnnotation(ownerID, annotationID string) error {
	res, err := s.db.Exec(`
		DELETE FROM session_annotations
		WHERE annotation_id = ? AND owner_id = ?`,
		annotationID, ownerID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrAnnotationNotFound
	}
	return nil
}
