package storage

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"sync"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"

	"filetransfer/internal/models"
)

type Store struct {
	db       *sql.DB
	sessions map[string]string // token → email
	mu       sync.RWMutex
}

func NewStore(connStr string) (*Store, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}

	s := &Store{db: db, sessions: make(map[string]string)}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id            SERIAL PRIMARY KEY,
			email         TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS transfer_history (
			id         TEXT NOT NULL,
			user_email TEXT NOT NULL,
			file_name  TEXT NOT NULL,
			file_size  BIGINT NOT NULL,
			direction  TEXT NOT NULL,
			peer_name  TEXT NOT NULL,
			status     TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (id, user_email)
		);
	`)
	return err
}

// RegisterUser creates a new unverified user.
func (s *Store) RegisterUser(email, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO users (email, password_hash) VALUES ($1, $2)`,
		email, string(hash),
	)
	return err
}

// AuthenticateUser validates email+password and returns the user.
func (s *Store) AuthenticateUser(email, password string) (*models.User, error) {
	u := &models.User{}
	err := s.db.QueryRow(
		`SELECT id, email, password_hash, created_at FROM users WHERE email=$1`, email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}
	return u, nil
}

// GetUserByEmail returns a user record (without sensitive fields).
func (s *Store) GetUserByEmail(email string) (*models.User, error) {
	u := &models.User{}
	err := s.db.QueryRow(
		`SELECT id, email, created_at FROM users WHERE email=$1`, email,
	).Scan(&u.ID, &u.Email, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// CreateSession stores a session token → email mapping and returns the token.
func (s *Store) CreateSession(email string) string {
	token := generateToken()
	s.mu.Lock()
	s.sessions[token] = email
	s.mu.Unlock()
	return token
}

// GetSession returns the email for the given session token.
func (s *Store) GetSession(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	email, ok := s.sessions[token]
	return email, ok
}

// DeleteSession removes a session token.
func (s *Store) DeleteSession(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// AddHistory persists a completed transfer record for a specific user.
func (s *Store) AddHistory(userEmail string, item *models.TransferHistory) error {
	_, err := s.db.Exec(
		`INSERT INTO transfer_history (id, user_email, file_name, file_size, direction, peer_name, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id, user_email) DO NOTHING`,
		item.ID, userEmail, item.FileName, item.FileSize, item.Direction, item.PeerName, item.Status,
	)
	return err
}

// GetHistory returns all transfer history for the user, newest first.
func (s *Store) GetHistory(userEmail string) ([]*models.TransferHistory, error) {
	rows, err := s.db.Query(
		`SELECT id, file_name, file_size, direction, peer_name, status, created_at
		 FROM transfer_history WHERE user_email=$1 ORDER BY created_at DESC`,
		userEmail,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []*models.TransferHistory
	for rows.Next() {
		item := &models.TransferHistory{}
		if err := rows.Scan(&item.ID, &item.FileName, &item.FileSize, &item.Direction,
			&item.PeerName, &item.Status, &item.Timestamp); err != nil {
			continue
		}
		history = append(history, item)
	}
	return history, nil
}

// generateToken returns a 32-byte hex session token.
func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
