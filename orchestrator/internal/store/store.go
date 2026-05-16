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
	"time"

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
	NousSessionID    string // pinned nous session id (sent in Hello)
	CreatedAt        int64  // unix seconds UTC
	LastActiveAt     int64  // bumped by WS keepalive (proxy ticker)
	LastMessageAt    int64  // bumped only on user `run` requests / login; drives idle-compact lifecycle
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
// busy timeout, applies the schema, and runs forward-only migrations for
// older DBs. Safe to call repeatedly.
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
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// migrate runs forward-only schema migrations for databases predating
// new columns. Each step uses tableColumns to detect prior application
// so re-running Open is a no-op once a column is present.
func migrate(db *sql.DB) error {
	cols, err := tableColumns(db, "users")
	if err != nil {
		return err
	}
	if !cols["nous_session_id"] {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN nous_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add users.nous_session_id: %w", err)
		}
	}
	if !cols["last_message_at"] {
		// Adds the column with DEFAULT 0, then seeds existing rows so they
		// don't appear "idle for years" to the compact-on-idle lifecycle.
		// COALESCE(NULLIF(last_active_at, 0), ?) prefers an existing
		// last_active_at; falls back to current time. Either way the
		// row is a plausible starting point for its 60-min idle window.
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN last_message_at INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add users.last_message_at: %w", err)
		}
		nowTs := time.Now().UTC().Unix()
		if _, err := db.Exec(
			`UPDATE users SET last_message_at = COALESCE(NULLIF(last_active_at, 0), ?) WHERE last_message_at = 0`,
			nowTs); err != nil {
			return fmt.Errorf("backfill users.last_message_at: %w", err)
		}
	}
	return nil
}

// tableColumns returns the set of column names for the given table.
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%q)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// CreateUser inserts a new user. Caller is responsible for hashing passwords
// and supplying provisioned fields. If LastMessageAt is left zero, it's
// seeded with CreatedAt so the user's 60-min idle window starts at signup
// (not at unix epoch).
func (s *Store) CreateUser(ctx context.Context, u User) error {
	if u.LastMessageAt == 0 {
		u.LastMessageAt = u.CreatedAt
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (
			id, email, password_hash, name,
			branch_name, worktree_path, container_name,
			nous_port, preview_port, preview_serve_port,
			nous_session_id,
			created_at, last_active_at, last_message_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Name,
		u.BranchName, u.WorktreePath, u.ContainerName,
		u.NousPort, u.PreviewPort, u.PreviewServePort,
		u.NousSessionID,
		u.CreatedAt, u.LastActiveAt, u.LastMessageAt,
	)
	return err
}

// UpdateLastMessage bumps last_message_at to ts. Called on:
//   - successful login (auth.Login)
//   - each user `run` request observed by the WS proxy
// NOT called by the WS keepalive ticker; that bumps last_active_at instead.
// The split makes the compact-on-idle lifecycle key off "real messaging
// activity" rather than "tab is open."
func (s *Store) UpdateLastMessage(ctx context.Context, userID string, ts int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET last_message_at = ? WHERE id = ?`, ts, userID)
	return err
}

// SetNousSessionID updates a user's pinned nous session id. Used by the
// signup path to record the id generated by auth, and by manual ops to
// repoint an existing user at a specific session.
func (s *Store) SetNousSessionID(ctx context.Context, userID, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET nous_session_id = ? WHERE id = ?`,
		sessionID, userID)
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

// UserSummary is the minimal projection used by the lifecycle loop —
// keeps the query cheap and avoids dragging password_hash etc. across
// the tick.
type UserSummary struct {
	ID             string
	ContainerName  string
	NousPort       int   // needed when lifecycle dials nous for compaction
	NousSessionID  string
	WorktreePath   string
	LastActiveAt   int64 // unix seconds UTC; bumped by WS keepalive
	LastMessageAt  int64 // unix seconds UTC; bumped on actual user messages
}

// ListUsers returns the projection used by lifecycle.GC: container name +
// nous ports/session for compaction dialing + both timestamps.
func (s *Store) ListUsers(ctx context.Context) ([]UserSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		id, container_name, nous_port, nous_session_id, worktree_path,
		last_active_at, last_message_at FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UserSummary
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(
			&u.ID, &u.ContainerName, &u.NousPort, &u.NousSessionID, &u.WorktreePath,
			&u.LastActiveAt, &u.LastMessageAt,
		); err != nil {
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
	nous_session_id,
	created_at, last_active_at, last_message_at FROM users`

func (s *Store) scanUser(row *sql.Row) (*User, error) {
	var u User
	var name sql.NullString
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &name,
		&u.BranchName, &u.WorktreePath, &u.ContainerName,
		&u.NousPort, &u.PreviewPort, &u.PreviewServePort,
		&u.NousSessionID,
		&u.CreatedAt, &u.LastActiveAt, &u.LastMessageAt,
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
