package session

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Session struct {
	ID              string    `json:"sessionId"`
	EntryFile       string    `json:"entryFile"`
	RootDir         string    `json:"rootDir"`
	StoredEntryFile string    `json:"-"`
	StoredRootDir   string    `json:"-"`
	CreatedAt       time.Time `json:"-"`
}

func (s Session) CreatedAtISO() string {
	return s.CreatedAt.UTC().Format(time.RFC3339)
}

type Store struct {
	db *sql.DB
}

func DefaultDBPath() (string, error) {
	stateDir, err := DefaultStateDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(stateDir, "sth", "sessions.db"), nil
}

func DefaultStateDir() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return stateHome, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".local", "state"), nil
}

func NewStore() (*Store, error) {
	dbPath, err := DefaultDBPath()
	if err != nil {
		return nil, err
	}

	return NewSQLiteStore(dbPath)
}

func NewInMemoryStore() (*Store, error) {
	return NewSQLiteStore(":memory:")
}

func NewSQLiteStore(dbPath string) (*Store, error) {
	if dbPath == "" {
		return nil, errors.New("database path is required")
	}

	if dbPath != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		_ = db.Close()
		return nil, err
	}

	if dbPath != ":memory:" {
		if _, err := db.Exec(`PRAGMA journal_mode = WAL`); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}

	return s.db.Close()
}

func (s *Store) Create(entryFile string) (Session, error) {
	return s.CreateUploaded(entryFile, entryFile)
}

func (s *Store) CreateUploaded(entryFile string, storedEntryFile string) (Session, error) {
	id, err := generateID()
	if err != nil {
		return Session{}, err
	}

	session := Session{
		ID:              id,
		EntryFile:       entryFile,
		RootDir:         filepath.Dir(entryFile),
		StoredEntryFile: storedEntryFile,
		StoredRootDir:   filepath.Dir(storedEntryFile),
		CreatedAt:       time.Now().UTC(),
	}

	_, err = s.db.Exec(
		`INSERT INTO sessions (session_id, entry_file, root_dir, stored_entry_file, stored_root_dir, created_at_unix) VALUES (?, ?, ?, ?, ?, ?)`,
		session.ID,
		session.EntryFile,
		session.RootDir,
		session.StoredEntryFile,
		session.StoredRootDir,
		session.CreatedAt.UnixNano(),
	)
	if err != nil {
		return Session{}, err
	}

	return session, nil
}

func (s *Store) Get(id string) (Session, bool, error) {
	row := s.db.QueryRow(
		`SELECT session_id, entry_file, root_dir, COALESCE(stored_entry_file, entry_file), COALESCE(stored_root_dir, root_dir), created_at_unix FROM sessions WHERE session_id = ?`,
		id,
	)

	var (
		session       Session
		createdAtUnix int64
	)
	if err := row.Scan(&session.ID, &session.EntryFile, &session.RootDir, &session.StoredEntryFile, &session.StoredRootDir, &createdAtUnix); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, false, nil
		}
		return Session{}, false, err
	}

	session.CreatedAt = time.Unix(0, createdAtUnix).UTC()
	return session, true, nil
}

func (s *Store) ListRecent(limit int) ([]Session, error) {
	query := `SELECT session_id, entry_file, root_dir, COALESCE(stored_entry_file, entry_file), COALESCE(stored_root_dir, root_dir), created_at_unix FROM sessions ORDER BY created_at_unix DESC`
	args := make([]any, 0, 1)
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	sessions := make([]Session, 0)
	for rows.Next() {
		var (
			session       Session
			createdAtUnix int64
		)
		if err := rows.Scan(&session.ID, &session.EntryFile, &session.RootDir, &session.StoredEntryFile, &session.StoredRootDir, &createdAtUnix); err != nil {
			return nil, err
		}

		session.CreatedAt = time.Unix(0, createdAtUnix).UTC()
		sessions = append(sessions, session)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sessions, nil
}

func (s *Store) init() error {
	if _, err := s.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return err
	}

	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			entry_file TEXT NOT NULL,
			root_dir TEXT NOT NULL,
			created_at_unix INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_sessions_created_at_unix
		ON sessions(created_at_unix DESC);
	`)
	if err != nil {
		return err
	}

	return s.ensureColumns()
}

func (s *Store) ensureColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(sessions)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var (
			cid       int
			name      string
			dataType  string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if !columns["stored_entry_file"] {
		if _, err := s.db.Exec(`ALTER TABLE sessions ADD COLUMN stored_entry_file TEXT`); err != nil {
			return err
		}
	}
	if !columns["stored_root_dir"] {
		if _, err := s.db.Exec(`ALTER TABLE sessions ADD COLUMN stored_root_dir TEXT`); err != nil {
			return err
		}
	}

	return nil
}

func generateID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}

	return hex.EncodeToString(buf), nil
}
