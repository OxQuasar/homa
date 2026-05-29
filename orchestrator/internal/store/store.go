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
	ID                  string
	Email               string
	PasswordHash        string
	Name                string // optional, freeform
	Username            string // required at signup; displayed publicly (forum, etc); [a-z0-9_]{3,32}
	// Application fields captured at signup; operator reads via `homa
	// review <userid>` to inform manual approval. Empty on rows that
	// existed before this field was added.
	JoinReason          string // "Why are you interested in joining the White Tower?"
	MysteryInterest     string // "What mystery are you interested in investigating?"
	Background          string // "What is your background?"
	BranchName          string
	WorktreePath        string
	ContainerName       string
	NousPort            int
	PreviewPort         int
	PreviewServePort    int
	NousSessionID       string // pinned nous session id (sent in Hello)
	CodeServerPort      int    // host port → sandbox :8443 (code-server)
	CodeServerServePort int    // tailscale-serve HTTPS port for code-server browser access
	CreatedAt           int64  // unix seconds UTC
	LastActiveAt        int64  // bumped by WS keepalive (proxy ticker)
	LastMessageAt       int64  // bumped only on user `run` requests / login; drives idle-compact lifecycle
	// Approved gate: signup creates Approved=false (application pending);
	// operator runs `homa approve <userid>` to flip it true. Login refuses
	// with 403 while Approved=false. Existing users predating this column
	// get backfilled to true (theyre already in).
	Approved bool
	// Rejected: operator (admin) marks an application rejected via the
	// admin UI. Login refuses with a different 403 message than pending.
	// Pre-existing rows are not rejected (default false).
	Rejected bool
	// IsAdmin: regular user vs admin. Admins can access /api/admin/*
	// endpoints + see the admin UI. Set via `homa promote <userid>`;
	// can NOT be set via signup (operator-controlled).
	IsAdmin bool
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
	if !cols["code_server_port"] {
		// Two new columns for the code-server integration (Phase 1 of
		// memories/homa/codeserver.md). Default 0 = "not allocated yet";
		// startup-time backfill (cmd/homa/main.go) picks new ports for
		// existing users without colliding with the user port pool.
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN code_server_port INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add users.code_server_port: %w", err)
		}
	}
	if !cols["code_server_serve_port"] {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN code_server_serve_port INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add users.code_server_serve_port: %w", err)
		}
	}
	if !cols["username"] {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN username TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add users.username: %w", err)
		}
		if err := backfillUsernames(db); err != nil {
			return fmt.Errorf("backfill users.username: %w", err)
		}
	}
	// Signup application fields (added when expanding the signup form
	// with intent / mystery interest / background). NULL on pre-existing
	// rows — they predate the application flow.
	for _, col := range []string{"join_reason", "mystery_interest", "background"} {
		if !cols[col] {
			if _, err := db.Exec(`ALTER TABLE users ADD COLUMN ` + col + ` TEXT`); err != nil {
				return fmt.Errorf("add users.%s: %w", col, err)
			}
		}
	}
	// Manual-approval gate: signup creates Approved=false; operator runs
	// `homa approve <userid>` to grant access. Existing users predating
	// this column are auto-approved so we don't lock them out.
	//
	// Transaction-wrapped so a partial failure (column added but
	// backfill UPDATE crashes) doesn't leave existing users locked
	// out on next start (which would see the column present and skip
	// re-attempting the backfill).
	if !cols["approved"] {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin approved migration: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE users ADD COLUMN approved INTEGER NOT NULL DEFAULT 0`); err != nil {
			tx.Rollback()
			return fmt.Errorf("add users.approved: %w", err)
		}
		if _, err := tx.Exec(`UPDATE users SET approved = 1`); err != nil {
			tx.Rollback()
			return fmt.Errorf("backfill users.approved: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit approved migration: %w", err)
		}
	}
	// Rejected + is_admin: both default to 0 for existing rows, which is
	// the desired backfill (nobody pre-existing is admin or rejected).
	// No transaction needed — pure ALTER, no backfill UPDATE.
	if !cols["rejected"] {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN rejected INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add users.rejected: %w", err)
		}
	}
	if !cols["is_admin"] {
		if _, err := db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add users.is_admin: %w", err)
		}
	}
	// Partial UNIQUE index on non-empty usernames. Lives in migrate()
	// (not schema.sql) because schema.sql runs BEFORE migrate(), so on a
	// migrating DB the index would try to apply before the column
	// exists. IF NOT EXISTS makes this idempotent on subsequent boots.
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username
		ON users(username) WHERE username != ''`); err != nil {
		return fmt.Errorf("create users.username index: %w", err)
	}
	return nil
}

// backfillUsernames assigns deterministic usernames to existing users
// (rows where username == '') based on their email-prefix. Resolves
// collisions by appending '_<short-uid>' from the user id. Ordered by
// created_at so older users get cleaner names.
func backfillUsernames(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, email FROM users WHERE username = '' ORDER BY created_at`)
	if err != nil {
		return err
	}
	type pair struct{ id, email string }
	var todo []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.id, &p.email); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	taken := map[string]struct{}{}
	for _, p := range todo {
		base := DeriveUsername(p.email)
		candidate := base
		for {
			if _, dup := taken[candidate]; !dup {
				break
			}
			candidate = base + "_" + p.id[:4]
			// extremely unlikely to recur; if it does the user id
			// itself is unique so we just keep appending. break after
			// one suffix; remaining unresolvable collisions get logged
			// but allowed through (the partial unique index will fire
			// if the operator ever has two of these).
			break
		}
		if _, err := db.Exec(`UPDATE users SET username = ? WHERE id = ?`, candidate, p.id); err != nil {
			return err
		}
		taken[candidate] = struct{}{}
	}
	return nil
}

// DeriveUsername builds a default username from an email address.
// Sanitizes the local-part to [a-z0-9_], clamps to 32 chars, pads
// to 3 chars with trailing underscores. Used at signup as a default
// suggestion (if the form leaves username empty) and by the migration
// backfill. Pure function — no DB access.
func DeriveUsername(email string) string {
	// Take everything before the first '@'.
	at := strings.IndexByte(email, '@')
	local := email
	if at > 0 {
		local = email[:at]
	}
	local = strings.ToLower(local)
	// Substitute non-conformant chars with '_'. Drop leading underscores
	// at the end so "_foo" stays "foo" (cleaner default).
	var b strings.Builder
	b.Grow(len(local))
	for _, r := range local {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimLeft(b.String(), "_")
	if len(out) > 32 {
		out = out[:32]
	}
	for len(out) < 3 {
		out += "_"
	}
	return out
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

// DB exposes the underlying *sql.DB so peer packages (forum, future
// admin/audit/etc.) can layer their own queries onto the shared SQLite
// instance without re-importing all of store. Caller owns nothing —
// must not Close.
func (s *Store) DB() *sql.DB { return s.db }

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
			id, email, password_hash, name, username,
			join_reason, mystery_interest, background,
			branch_name, worktree_path, container_name,
			nous_port, preview_port, preview_serve_port,
			nous_session_id,
			code_server_port, code_server_serve_port,
			created_at, last_active_at, last_message_at,
			approved, rejected, is_admin
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.Email, u.PasswordHash, u.Name, u.Username,
		u.JoinReason, u.MysteryInterest, u.Background,
		u.BranchName, u.WorktreePath, u.ContainerName,
		u.NousPort, u.PreviewPort, u.PreviewServePort,
		u.NousSessionID,
		u.CodeServerPort, u.CodeServerServePort,
		u.CreatedAt, u.LastActiveAt, u.LastMessageAt,
		u.Approved, u.Rejected, u.IsAdmin,
	)
	return err
}

// SetApproved flips the approved gate. Called by `homa approve <userid>`
// and admin API. Approving auto-clears rejected (operator changed mind).
// Idempotent on value. ErrNotFound when userID matches no row.
func (s *Store) SetApproved(ctx context.Context, userID string, approved bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET approved = ?, rejected = CASE WHEN ? = 1 THEN 0 ELSE rejected END WHERE id = ?`,
		approved, approved, userID)
	return rowsAffectedErr(res, err)
}

// SetRejected marks a pending application as rejected. Auto-clears
// approved if it was somehow set (defensive — shouldn't happen via UI
// since approved users aren't reject-able, but enforces the invariant
// at the data layer). Idempotent on value. ErrNotFound on missing user.
func (s *Store) SetRejected(ctx context.Context, userID string, rejected bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET rejected = ?, approved = CASE WHEN ? = 1 THEN 0 ELSE approved END WHERE id = ?`,
		rejected, rejected, userID)
	return rowsAffectedErr(res, err)
}

// SetAdmin flips the is_admin flag. Operator-only path: `homa promote`.
// No UI endpoint exposes this (intentional — bootstrap is CLI-only).
// Idempotent on value. ErrNotFound on missing user.
func (s *Store) SetAdmin(ctx context.Context, userID string, admin bool) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE users SET is_admin = ? WHERE id = ?`, admin, userID)
	return rowsAffectedErr(res, err)
}

// rowsAffectedErr collapses the three-step "exec → check rows → translate
// zero to ErrNotFound" pattern used by every SetX above.
func rowsAffectedErr(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
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

// SetCodeServerPorts updates the user's allocated code-server ports.
// Used by the startup-time backfill for users that pre-date the
// code-server feature (their columns are still 0).
func (s *Store) SetCodeServerPorts(ctx context.Context, userID string, hostPort, servePort int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET code_server_port = ?, code_server_serve_port = ? WHERE id = ?`,
		hostPort, servePort, userID)
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

// AllUserPorts returns every host port (nous + preview + code-server)
// and every tailscale-serve port (preview + code-server) currently
// allocated across all users. Used at startup to seed PortAllocator
// so we don't re-issue ports after restart. Zero values are filtered
// out (code-server columns may be 0 for pre-feature rows pending
// backfill).
func (s *Store) AllUserPorts(ctx context.Context) (hostPorts, servePorts []int, err error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		nous_port, preview_port, preview_serve_port,
		code_server_port, code_server_serve_port FROM users`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var nous, preview, serve, csPort, csServe int
		if err := rows.Scan(&nous, &preview, &serve, &csPort, &csServe); err != nil {
			return nil, nil, err
		}
		hostPorts = append(hostPorts, nous, preview)
		servePorts = append(servePorts, serve)
		if csPort > 0 {
			hostPorts = append(hostPorts, csPort)
		}
		if csServe > 0 {
			servePorts = append(servePorts, csServe)
		}
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
	ID            string
	ContainerName string
	NousPort      int    // needed when lifecycle dials nous for compaction
	NousSessionID string
	WorktreePath  string
	LastActiveAt  int64 // unix seconds UTC; bumped by WS keepalive
	LastMessageAt int64 // unix seconds UTC; bumped on actual user messages
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

// IsUsernameUniqueViolation reports whether err is the UNIQUE-constraint
// failure on the partial idx_users_username. Auth maps it to 409.
func IsUsernameUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") &&
		strings.Contains(msg, "idx_users_username")
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
const userSelect = `SELECT id, email, password_hash, name, username,
	join_reason, mystery_interest, background,
	branch_name, worktree_path, container_name,
	nous_port, preview_port, preview_serve_port,
	nous_session_id,
	code_server_port, code_server_serve_port,
	created_at, last_active_at, last_message_at,
	approved, rejected, is_admin FROM users`

func (s *Store) scanUser(row *sql.Row) (*User, error) {
	var u User
	var name, joinReason, mysteryInterest, background sql.NullString
	err := row.Scan(
		&u.ID, &u.Email, &u.PasswordHash, &name, &u.Username,
		&joinReason, &mysteryInterest, &background,
		&u.BranchName, &u.WorktreePath, &u.ContainerName,
		&u.NousPort, &u.PreviewPort, &u.PreviewServePort,
		&u.NousSessionID,
		&u.CodeServerPort, &u.CodeServerServePort,
		&u.CreatedAt, &u.LastActiveAt, &u.LastMessageAt,
		&u.Approved, &u.Rejected, &u.IsAdmin,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Name = name.String
	u.JoinReason = joinReason.String
	u.MysteryInterest = mysteryInterest.String
	u.Background = background.String
	return &u, nil
}
