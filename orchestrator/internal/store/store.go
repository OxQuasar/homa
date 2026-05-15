// Package store wraps the SQLite-backed persistence layer for homa users and
// web sessions. Schema is loaded from schema.sql via go:embed and applied
// idempotently on Open.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// busyTimeoutMS bounds how long SQLite waits when another connection holds the
// write lock. 5s matches the spec hint and is well above any normal contention.
const busyTimeoutMS = 5000

// ErrNotFound is returned when a lookup fails to find a matching row.
var ErrNotFound = errors.New("not found")

// User mirrors the users table.
type User struct {
	ID               string
	Email            string
	PasswordHash     string
	Name             string
	BranchName       string
	WorktreePath     string
	ContainerName    string
	NousPort         int
	PreviewPort      int
	PreviewServePort int
	CreatedAt        int64 // unix seconds UTC
	LastActiveAt     int64 // unix seconds UTC
}

// WebSession represents a single browser session token.
type WebSession struct {
	Token     string
	UserID    string
	ExpiresAt int64 // unix seconds UTC
}

// Store wraps the SQLite database handle.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at path, enables WAL mode plus a
// busy timeout, and applies the schema. Safe to call repeatedly.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)",
		path, busyTimeoutMS)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sql.Open: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// CreateUser inserts a new user. Caller is responsible for hashing passwords
// and supplying provisioned fields.
func (s *Store) CreateUser(ctx context.Context, u User) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (
			id, email, password_hash, name,
			branch_name, worktree_path, container_name,
			nous_port, preview_port, preview_serve_port,
			created_at, last_active_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Name,
		u.BranchName, u.WorktreePath, u.ContainerName,
		u.NousPort, u.PreviewPort, u.PreviewServePort,
		u.CreatedAt, u.LastActiveAt,
	)
	return err
}

// GetUserByEmail returns the user with the given email, or ErrNotFound.
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, userSelect+` WHERE email = ?`, email))
}

// GetUserByID returns the user with the given id, or ErrNotFound.
func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, userSelect+` WHERE id = ?`, id))
}

// UpdateLastActive updates the last_active_at timestamp for a user.
func (s *Store) UpdateLastActive(ctx context.Context, userID string, ts int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_active_at = ? WHERE id = ?`, ts, userID)
	return err
}

// CreateWebSession inserts a new browser session token.
func (s *Store) CreateWebSession(ctx context.Context, token, userID string, expiresAt int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO web_sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, userID, expiresAt)
	return err
}

// GetWebSession returns the session for a token, or ErrNotFound.
func (s *Store) GetWebSession(ctx context.Context, token string) (*WebSession, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT token, user_id, expires_at FROM web_sessions WHERE token = ?`, token)
	var ws WebSession
	err := row.Scan(&ws.Token, &ws.UserID, &ws.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ws, nil
}

// DeleteWebSession removes a session token; no error if the token didn't exist.
func (s *Store) DeleteWebSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM web_sessions WHERE token = ?`, token)
	return err
}

// AllUserPorts returns every host port (nous + preview) and every
// tailscale-serve port currently allocated across all users. Used at
// startup to seed PortAllocator so we don't re-issue ports after restart.
func (s *Store) AllUserPorts(ctx context.Context) (hostPorts, servePorts []int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT nous_port, preview_port, preview_serve_port FROM users`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var nous, preview, serve int
		if err := rows.Scan(&nous, &preview, &serve); err != nil {
			return nil, nil, err
		}
		hostPorts = append(hostPorts, nous, preview)
		servePorts = append(servePorts, serve)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return hostPorts, servePorts, nil
}

// UserSummary is the minimal projection used by the GC loop — keeps the
// query cheap and avoids dragging password_hash etc. across the GC tick.
type UserSummary struct {
	ID            string
	ContainerName string
	LastActiveAt  int64 // unix seconds UTC
}

// ListUsers returns the (ID, container_name, last_active_at) projection
// for every user. Used by lifecycle.GC.
func (s *Store) ListUsers(ctx context.Context) ([]UserSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, container_name, last_active_at FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserSummary
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.ContainerName, &u.LastActiveAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// IsEmailUniqueViolation reports whether err is the SQLite UNIQUE constraint
// violation specifically on users.email — the only case auth maps to 409.
// Other UNIQUE collisions (e.g. users.id primary-key) are programming bugs
// and should bubble as 500.
func IsEmailUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	// modernc.org/sqlite surfaces this as
	//   "constraint failed: UNIQUE constraint failed: users.email"
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") &&
		strings.Contains(msg, "users.email")
}

// IsAnyUniqueViolation reports whether err is any UNIQUE-constraint failure.
// Reserved for diagnostics; auth uses the narrow IsEmailUniqueViolation.
func IsAnyUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// userSelect is the column list used by both GetUserBy* helpers.
const userSelect = `SELECT id, email, password_hash, name,
	branch_name, worktree_path, container_name,
	nous_port, preview_port, preview_serve_port,
	created_at, last_active_at FROM users`

func (s *Store) scanUser(row *sql.Row) (*User, error) {
	var u User
	var name sql.NullString
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &name,
		&u.BranchName, &u.WorktreePath, &u.ContainerName,
		&u.NousPort, &u.PreviewPort, &u.PreviewServePort,
		&u.CreatedAt, &u.LastActiveAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Name = name.String
	return &u, nil
}
