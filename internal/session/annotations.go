package session

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Annotations are reader notes attached to a session: a highlighted quote
// (anchor) plus optional comment and tag. They live across versions of a
// chain — each session row owns its own set, so switching versions switches
// annotation sets with it. owner_id separates readers: every viewer sees and
// manages only their own annotations; empty owner (auth disabled) is the
// shared pool.
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
// with their session.
func (s *Store) initAnnotations() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS session_annotations (
			annotation_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
			owner_id TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL CHECK(kind IN ('underline','note')),
			quote TEXT NOT NULL,
			prefix TEXT NOT NULL DEFAULT '',
			suffix TEXT NOT NULL DEFAULT '',
			comment TEXT NOT NULL DEFAULT '',
			tag TEXT NOT NULL DEFAULT '',
			created_at_nano INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_session_annotations_session
		ON session_annotations(session_id, created_at_nano);
	`)
	return err
}

func clampString(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) > max {
		// Trim to a rune boundary so multi-byte text is not cut mid-character.
		runes := []rune(value)
		if len(runes) > max {
			value = string(runes[:max])
		}
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

// AddAnnotation stores a new annotation and returns the persisted record with
// its generated ID and timestamp.
func (s *Store) AddAnnotation(sessionID, ownerID string, in Annotation) (Annotation, error) {
	if err := in.validate(); err != nil {
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
			(annotation_id, session_id, owner_id, kind, quote, prefix, suffix, comment, tag, created_at_nano)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, ownerID, in.Kind, in.Quote, in.Prefix, in.Suffix, in.Comment, in.Tag, now,
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

// ListAnnotations returns the owner's annotations for a session, oldest first
// (document order matches reading order).
func (s *Store) ListAnnotations(sessionID, ownerID string) ([]Annotation, error) {
	rows, err := s.db.Query(`
		SELECT annotation_id, kind, quote, prefix, suffix, comment, tag, created_at_nano
		FROM session_annotations
		WHERE session_id = ? AND owner_id = ?
		ORDER BY created_at_nano ASC, annotation_id ASC`,
		sessionID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Annotation
	for rows.Next() {
		var a Annotation
		var unix int64
		if err := rows.Scan(&a.ID, &a.Kind, &a.Quote, &a.Prefix, &a.Suffix, &a.Comment, &a.Tag, &unix); err != nil {
			return nil, err
		}
		a.SessionID = sessionID
		a.CreatedAt = time.Unix(0, unix).UTC().Format(time.RFC3339)
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteAnnotation removes one annotation owned by ownerID on the session.
func (s *Store) DeleteAnnotation(sessionID, ownerID, annotationID string) error {
	res, err := s.db.Exec(`
		DELETE FROM session_annotations
		WHERE annotation_id = ? AND session_id = ? AND owner_id = ?`,
		annotationID, sessionID, ownerID)
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

// CountAnnotations returns how many annotations the owner has on a session.
// Used by the version timeline to badge each version.
func (s *Store) CountAnnotations(sessionID, ownerID string) (int, error) {
	var n int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM session_annotations
		WHERE session_id = ? AND owner_id = ?`,
		sessionID, ownerID).Scan(&n)
	return n, err
}
