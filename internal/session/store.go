package session

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Session struct {
	ID        string    `json:"sessionId"`
	EntryFile string    `json:"entryFile"`
	RootDir   string    `json:"rootDir"`
	CreatedAt time.Time `json:"-"`
}

func (s Session) CreatedAtISO() string {
	return s.CreatedAt.UTC().Format(time.RFC3339)
}

type Store struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewStore() *Store {
	return &Store{
		sessions: make(map[string]Session),
	}
}

func (s *Store) Create(entryFile string) (Session, error) {
	id, err := generateID()
	if err != nil {
		return Session{}, err
	}

	session := Session{
		ID:        id,
		EntryFile: entryFile,
		RootDir:   filepath.Dir(entryFile),
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	return session, nil
}

func (s *Store) Get(id string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *Store) ListRecent(limit int) []Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessions := make([]Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		sessions = append(sessions, session)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})

	if limit > 0 && len(sessions) > limit {
		sessions = sessions[:limit]
	}

	return sessions
}

func generateID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}
