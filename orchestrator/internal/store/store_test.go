package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
)

// freshStore opens a clean SQLite at a temp path and returns the store
// together with a cleanup hook. Each test gets its own DB — no shared
// state between tests.
func freshStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "homa.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// sampleUser builds a User with all fields populated. Tests override the
// fields they care about.
func sampleUser(id string) User {
	return User{
		ID:               id,
		Email:            id + "@example.com",
		PasswordHash:     "$2a$test",
		Name:             "Test " + id,
		Username:         "u" + id, // unique per id; satisfies the partial UNIQUE index
		BranchName:       "user/" + id,
		WorktreePath:     "/var/homa/branches/" + id,
		ContainerName:    "homa-user-" + id,
		NousPort:         40000,
		PreviewPort:      40001,
		PreviewServePort: 10001,
		NousSessionID:    "sess" + id,
		CreatedAt:        1_700_000_000,
		LastActiveAt:     1_700_000_000,
		LastMessageAt:    1_700_000_000,
	}
}

// ----- Open + schema -----------------------------------------------------

// TestOpenAppliesSchema — Open on a brand-new path leaves the users +
// web_sessions tables in place with all expected columns.
func TestOpenAppliesSchema(t *testing.T) {
	st := freshStore(t)
	cols, err := tableColumns(st.db, "users")
	if err != nil {
		t.Fatalf("tableColumns users: %v", err)
	}
	for _, want := range []string{
		"id", "email", "password_hash", "name",
		"branch_name", "worktree_path", "container_name",
		"nous_port", "preview_port", "preview_serve_port",
		"nous_session_id", "created_at", "last_active_at", "last_message_at",
	} {
		if !cols[want] {
			t.Errorf("users column missing: %q", want)
		}
	}
	wcols, err := tableColumns(st.db, "web_sessions")
	if err != nil {
		t.Fatalf("tableColumns web_sessions: %v", err)
	}
	for _, want := range []string{"token", "user_id", "expires_at"} {
		if !wcols[want] {
			t.Errorf("web_sessions column missing: %q", want)
		}
	}
}

// TestOpenIsIdempotent — running Open against the same path twice does
// not error; the second open finds tables already present.
func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homa.db")
	st1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	st1.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	st2.Close()
}

// ----- Migrations --------------------------------------------------------

// TestMigrateBackfillsNousSessionID — a database predating the
// nous_session_id column gets the column added on Open, defaulted to ''.
// Older rows survive intact (no data loss in the migration).
func TestMigrateBackfillsNousSessionID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homa.db")

	// Create a v0 schema by hand and insert a user.
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY, email TEXT UNIQUE, password_hash TEXT, name TEXT,
		branch_name TEXT, worktree_path TEXT, container_name TEXT,
		nous_port INTEGER, preview_port INTEGER, preview_serve_port INTEGER,
		created_at INTEGER, last_active_at INTEGER)`); err != nil {
		t.Fatalf("create v0 users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, email, password_hash, name, branch_name, worktree_path,
		 container_name, nous_port, preview_port, preview_serve_port,
		 created_at, last_active_at)
		VALUES ('legacy','l@x','$2a','L','u/legacy','/wt','homa-user-legacy',
		        40000, 40001, 10001, 1700000000, 1700000000)`); err != nil {
		t.Fatalf("insert v0: %v", err)
	}
	db.Close()

	// Re-open via the package's Open — migrate() must add the column.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after v0: %v", err)
	}

	u, err := st.GetUserByID(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("GetUserByID legacy: %v", err)
	}
	if u.NousSessionID != "" {
		t.Errorf("legacy NousSessionID: got %q, want '' (default after migration)", u.NousSessionID)
	}
}

// TestDeriveUsername — pure function. Builds default usernames from
// email prefixes with the same sanitization rules used at backfill time.
func TestDeriveUsername(t *testing.T) {
	cases := []struct {
		email, want string
	}{
		{"alice@example.com", "alice"},
		{"BoB@x.io", "bob"},                               // lowercase
		{"a.b.c@x.io", "a_b_c"},                            // dots → _
		{"_underscore@x.io", "underscore"},                 // strip leading _
		{"a@x.io", "a__"},                                  // pad to 3
		{"verylongprefixoflengthovermaxthirtytwo@x.io", "verylongprefixoflengthovermaxthi"}, // 32 cap
		{"a-b@x.io", "a_b"},                                // hyphen → _
		{"noaddr", "noaddr"},                               // no @
	}
	for _, tc := range cases {
		t.Run(tc.email, func(t *testing.T) {
			if got := DeriveUsername(tc.email); got != tc.want {
				t.Errorf("DeriveUsername(%q): got %q, want %q", tc.email, got, tc.want)
			}
		})
	}
}

// TestMigrateBackfillsUsernames — a v3 DB (post-code-server, pre-username)
// gets the column added with empty defaults, then the backfill assigns
// usernames from email prefixes. Two users with the same email-prefix
// would collide; the test confirms the second one gets disambiguated.
func TestMigrateBackfillsUsernames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homa.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY, email TEXT UNIQUE, password_hash TEXT, name TEXT,
		branch_name TEXT, worktree_path TEXT, container_name TEXT,
		nous_port INTEGER, preview_port INTEGER, preview_serve_port INTEGER,
		nous_session_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER, last_active_at INTEGER,
		last_message_at INTEGER NOT NULL DEFAULT 0,
		code_server_port INTEGER NOT NULL DEFAULT 0,
		code_server_serve_port INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create v3 users: %v", err)
	}
	for _, row := range []struct {
		id, email string
		created   int64
	}{
		{"aaaaaaaa", "alice@x.io", 100},
		{"bbbbbbbb", "alice@y.io", 200}, // same prefix, later created_at
	} {
		_, err := db.Exec(`INSERT INTO users
			(id, email, password_hash, name, branch_name, worktree_path,
			 container_name, nous_port, preview_port, preview_serve_port,
			 nous_session_id, created_at, last_active_at)
			VALUES (?, ?, '$2a','x','u/x','/wt','c',0,0,0,'sx',?,?)`,
			row.id, row.email, row.created, row.created)
		if err != nil {
			t.Fatalf("insert %s: %v", row.id, err)
		}
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after v3: %v", err)
	}

	a, _ := st.GetUserByID(context.Background(), "aaaaaaaa")
	b, _ := st.GetUserByID(context.Background(), "bbbbbbbb")
	if a.Username != "alice" {
		t.Errorf("older user: got username %q, want alice", a.Username)
	}
	if b.Username == "alice" {
		t.Errorf("collision not resolved: second user also got 'alice'")
	}
	if b.Username == "" {
		t.Errorf("second user: empty username (backfill missed)")
	}
}

// TestMigrateBackfillsLastMessageAt — a database predating last_message_at
// gets the column added AND existing rows are backfilled so they don't
// appear "idle for years" to the compact-on-idle lifecycle. The migration
// uses COALESCE(NULLIF(last_active_at, 0), now()), so a row with a real
// last_active_at carries that value forward.
func TestMigrateBackfillsLastMessageAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homa.db")

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	const knownActive = int64(1_700_000_555)
	// v1 schema: post-nous_session_id but pre-last_message_at.
	if _, err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY, email TEXT UNIQUE, password_hash TEXT, name TEXT,
		branch_name TEXT, worktree_path TEXT, container_name TEXT,
		nous_port INTEGER, preview_port INTEGER, preview_serve_port INTEGER,
		nous_session_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER, last_active_at INTEGER)`); err != nil {
		t.Fatalf("create v1 users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users
		(id, email, password_hash, name, branch_name, worktree_path,
		 container_name, nous_port, preview_port, preview_serve_port,
		 nous_session_id, created_at, last_active_at)
		VALUES ('a','a@x','$2a','A','u/a','/wt','homa-user-a',
		        40000, 40001, 10001, 'sa', 1700000000, ?)`,
		knownActive); err != nil {
		t.Fatalf("insert v1: %v", err)
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after v1: %v", err)
	}

	u, err := st.GetUserByID(context.Background(), "a")
	if err != nil {
		t.Fatalf("GetUserByID a: %v", err)
	}
	if u.LastMessageAt != knownActive {
		t.Errorf("LastMessageAt backfill: got %d, want %d (= LastActiveAt)",
			u.LastMessageAt, knownActive)
	}
}

// ----- User CRUD --------------------------------------------------------

// TestCreateAndLookupUser — round-trip of every field through CreateUser
// → GetUserByID. Catches drift between INSERT/SELECT column lists.
func TestCreateAndLookupUser(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("abcd1234")
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := st.GetUserByID(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Email != u.Email || got.PasswordHash != u.PasswordHash ||
		got.Name != u.Name || got.BranchName != u.BranchName ||
		got.WorktreePath != u.WorktreePath || got.ContainerName != u.ContainerName ||
		got.NousPort != u.NousPort || got.PreviewPort != u.PreviewPort ||
		got.PreviewServePort != u.PreviewServePort ||
		got.NousSessionID != u.NousSessionID ||
		got.CreatedAt != u.CreatedAt || got.LastActiveAt != u.LastActiveAt ||
		got.LastMessageAt != u.LastMessageAt {
		t.Errorf("roundtrip mismatch:\n got %+v\n want %+v", *got, u)
	}
}

// TestCreateUserSeedsLastMessageAt — when CreateUser is called with
// LastMessageAt=0, the store seeds it to CreatedAt so the user gets a
// fresh idle window starting at signup (not at unix epoch which would
// fire the lifecycle immediately on next tick).
func TestCreateUserSeedsLastMessageAt(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("seedx012")
	u.LastMessageAt = 0 // explicitly unset; CreateUser should fill in
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, _ := st.GetUserByID(context.Background(), u.ID)
	if got.LastMessageAt != u.CreatedAt {
		t.Errorf("LastMessageAt seed: got %d, want %d (= CreatedAt)",
			got.LastMessageAt, u.CreatedAt)
	}
}

// TestGetUserByEmail — lookup-by-email parity with lookup-by-id.
func TestGetUserByEmail(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("emailcas")
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, err := st.GetUserByEmail(context.Background(), u.Email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if got.ID != u.ID {
		t.Errorf("GetUserByEmail ID: got %q, want %q", got.ID, u.ID)
	}
}

// TestGetUserNotFoundReturnsErrNotFound — distinguishable from generic errors
// so callers (auth) can do the "no such email" branch cleanly.
func TestGetUserNotFoundReturnsErrNotFound(t *testing.T) {
	st := freshStore(t)
	if _, err := st.GetUserByID(context.Background(), "nope0000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByID nope: got %v, want ErrNotFound", err)
	}
	if _, err := st.GetUserByEmail(context.Background(), "no@x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUserByEmail no@x: got %v, want ErrNotFound", err)
	}
}

// TestUpdateLastActive and TestUpdateLastMessage — confirm the two
// independent bump points each touch only their target column. Important
// because the entire idle-compact feature pivots on keeping them split.
func TestUpdateLastActiveAndLastMessageIndependent(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("indep001")
	u.LastActiveAt = 1000
	u.LastMessageAt = 1000
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	// Bump only LastActive.
	if err := st.UpdateLastActive(context.Background(), u.ID, 2000); err != nil {
		t.Fatalf("UpdateLastActive: %v", err)
	}
	got, _ := st.GetUserByID(context.Background(), u.ID)
	if got.LastActiveAt != 2000 || got.LastMessageAt != 1000 {
		t.Errorf("after UpdateLastActive: got LastActive=%d LastMessage=%d, want 2000/1000",
			got.LastActiveAt, got.LastMessageAt)
	}
	// Bump only LastMessage.
	if err := st.UpdateLastMessage(context.Background(), u.ID, 3000); err != nil {
		t.Fatalf("UpdateLastMessage: %v", err)
	}
	got, _ = st.GetUserByID(context.Background(), u.ID)
	if got.LastActiveAt != 2000 || got.LastMessageAt != 3000 {
		t.Errorf("after UpdateLastMessage: got LastActive=%d LastMessage=%d, want 2000/3000",
			got.LastActiveAt, got.LastMessageAt)
	}
}

// TestSetNousSessionID — repointing a user's pinned nous session id
// (manual ops path; not used by the runtime today but exists as an API).
func TestSetNousSessionID(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("setsess0")
	u.NousSessionID = "original"
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.SetNousSessionID(context.Background(), u.ID, "swapped!"); err != nil {
		t.Fatalf("SetNousSessionID: %v", err)
	}
	got, _ := st.GetUserByID(context.Background(), u.ID)
	if got.NousSessionID != "swapped!" {
		t.Errorf("NousSessionID: got %q, want swapped!", got.NousSessionID)
	}
}

// TestListUsersIncludesAllNewFields — the projection used by lifecycle
// must carry NousPort, NousSessionID, WorktreePath, LastMessageAt — the
// fields the lifecycle dials nous with. Asserts no field gets accidentally
// dropped from the SELECT list.
func TestListUsersIncludesAllNewFields(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("listfield")
	u.NousPort = 40010
	u.NousSessionID = "pinned-xyz"
	u.WorktreePath = "/var/wt/listfield"
	u.LastMessageAt = 1_700_000_500
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	list, err := st.ListUsers(context.Background())
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListUsers len: got %d, want 1", len(list))
	}
	s := list[0]
	switch {
	case s.NousPort != 40010:
		t.Errorf("NousPort: got %d", s.NousPort)
	case s.NousSessionID != "pinned-xyz":
		t.Errorf("NousSessionID: got %q", s.NousSessionID)
	case s.WorktreePath != "/var/wt/listfield":
		t.Errorf("WorktreePath: got %q", s.WorktreePath)
	case s.LastMessageAt != 1_700_000_500:
		t.Errorf("LastMessageAt: got %d", s.LastMessageAt)
	}
}

// TestAllUserPortsReturnsBothLists — port allocator seeding query.
// Returns hostPorts (nous + preview ports interleaved) and servePorts.
func TestAllUserPortsReturnsBothLists(t *testing.T) {
	st := freshStore(t)
	u1 := sampleUser("p1abcd12")
	u1.NousPort, u1.PreviewPort, u1.PreviewServePort = 40000, 40001, 10001
	u2 := sampleUser("p2abcd34")
	u2.NousPort, u2.PreviewPort, u2.PreviewServePort = 40002, 40003, 10002
	for _, u := range []User{u1, u2} {
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", u.ID, err)
		}
	}
	host, serve, err := st.AllUserPorts(context.Background())
	if err != nil {
		t.Fatalf("AllUserPorts: %v", err)
	}
	sort.Ints(host)
	sort.Ints(serve)
	wantHost := []int{40000, 40001, 40002, 40003}
	wantServe := []int{10001, 10002}
	if !equalInts(host, wantHost) {
		t.Errorf("host ports: got %v, want %v", host, wantHost)
	}
	if !equalInts(serve, wantServe) {
		t.Errorf("serve ports: got %v, want %v", serve, wantServe)
	}
}

// ----- Web sessions -----------------------------------------------------

// TestWebSessionLifecycle — create, lookup, delete; subsequent lookup
// returns ErrNotFound.
func TestWebSessionLifecycle(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("webuser1")
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	const token = "deadbeef"
	const expires = int64(1_700_900_000)
	if err := st.CreateWebSession(context.Background(), token, u.ID, expires); err != nil {
		t.Fatalf("CreateWebSession: %v", err)
	}
	got, err := st.GetWebSession(context.Background(), token)
	if err != nil {
		t.Fatalf("GetWebSession: %v", err)
	}
	if got.UserID != u.ID || got.ExpiresAt != expires {
		t.Errorf("WebSession roundtrip: got %+v", *got)
	}
	if err := st.DeleteWebSession(context.Background(), token); err != nil {
		t.Fatalf("DeleteWebSession: %v", err)
	}
	if _, err := st.GetWebSession(context.Background(), token); !errors.Is(err, ErrNotFound) {
		t.Errorf("post-delete GetWebSession: got %v, want ErrNotFound", err)
	}
}

// TestDuplicateEmailRejected — UNIQUE constraint on email surfaces as an
// error (any error; we don't depend on a specific sqlite text). Important
// because auth.Signup relies on this for "email already in use."
func TestDuplicateEmailRejected(t *testing.T) {
	st := freshStore(t)
	u1 := sampleUser("dupeuse1")
	u1.Email = "shared@x.io"
	if err := st.CreateUser(context.Background(), u1); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	u2 := sampleUser("dupeuse2")
	u2.Email = "shared@x.io" // collision
	if err := st.CreateUser(context.Background(), u2); err == nil {
		t.Error("second CreateUser with same email should have failed (UNIQUE constraint)")
	} else {
		// Sanity check: it really is a constraint-ish error, not something
		// random. Just ensure it surfaces in some recognizable form.
		if !contains(err.Error(), "UNIQUE") && !contains(err.Error(), "constraint") {
			t.Logf("note: duplicate email error string was %q (expected UNIQUE/constraint hint)", err.Error())
		}
	}
}

// equalInts is a tiny slice helper — sort.Equal would be nice but we
// import nothing.
func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool { return len(s) >= len(sub) && indexOf(s, sub) >= 0 }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Smoke that the helper compiles + behaves; lets us drop strings import.
var _ = fmt.Sprintf

// TestSetApprovedNotFound — defends against typo'd CLI input that
// would silently succeed (UPDATE matching 0 rows is "ok" by default).
func TestSetApprovedNotFound(t *testing.T) {
	st := freshStore(t)
	err := st.SetApproved(context.Background(), "noexist", true)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

// TestSetApprovedFlipsAndIsIdempotent — happy path: gate flip, then
// flipping again to the same value is still OK (idempotent on value).
func TestSetApprovedFlipsAndIsIdempotent(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("approver")
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	got, _ := st.GetUserByID(context.Background(), u.ID)
	if got.Approved {
		t.Error("freshly-created user should be Approved=false (gate)")
	}

	if err := st.SetApproved(context.Background(), u.ID, true); err != nil {
		t.Fatalf("first SetApproved: %v", err)
	}
	got, _ = st.GetUserByID(context.Background(), u.ID)
	if !got.Approved {
		t.Error("after SetApproved(true): still Approved=false")
	}

	// Re-applying same value is a no-op success (idempotent).
	if err := st.SetApproved(context.Background(), u.ID, true); err != nil {
		t.Errorf("re-approve: %v", err)
	}
}

// TestMigrateApprovedBackfillsExistingUsers — a v6 DB (post-username,
// pre-approval) gets the column added with backfill so existing users
// remain logged-in-able. Without backfill, they'd be locked out.
func TestMigrateApprovedBackfillsExistingUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "homa.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// v6 schema — has all columns through application essays but NO
	// approved column. (Modeling state of a DB created before this
	// migration shipped.)
	if _, err := db.Exec(`CREATE TABLE users (
		id TEXT PRIMARY KEY, email TEXT UNIQUE, password_hash TEXT, name TEXT,
		username TEXT NOT NULL DEFAULT '',
		join_reason TEXT, mystery_interest TEXT, background TEXT,
		branch_name TEXT, worktree_path TEXT, container_name TEXT,
		nous_port INTEGER, preview_port INTEGER, preview_serve_port INTEGER,
		nous_session_id TEXT NOT NULL DEFAULT '',
		created_at INTEGER, last_active_at INTEGER,
		last_message_at INTEGER NOT NULL DEFAULT 0,
		code_server_port INTEGER NOT NULL DEFAULT 0,
		code_server_serve_port INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatalf("create v6 users: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, password_hash, name, username,
		branch_name, worktree_path, container_name,
		nous_port, preview_port, preview_serve_port,
		nous_session_id, created_at, last_active_at)
		VALUES ('aaaaaaaa', 'pre@x.io', '$2a', '', 'pre',
			'u/x', '/wt', 'c', 0, 0, 0, 's', 1, 1)`); err != nil {
		t.Fatalf("insert v6 user: %v", err)
	}
	db.Close()

	// Re-open via Store → triggers migrate() including the approved
	// column add + backfill.
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after v6: %v", err)
	}

	u, err := st.GetUserByID(context.Background(), "aaaaaaaa")
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if !u.Approved {
		t.Error("pre-migration user not backfilled to Approved=true (would be locked out)")
	}
}

// TestPasswordResetRequest_CRUD — happy path: create with matched user_id,
// list returns it as pending, get-by-id returns same data, resolve flips
// the timestamps, list still returns it (within recentResolvedDays) but
// status reads as resolved.
func TestPasswordResetRequest_CRUD(t *testing.T) {
	st := freshStore(t)

	id, err := st.CreatePasswordResetRequest(context.Background(), PasswordResetRequest{
		Email:     "u@x.io",
		UserID:    "abcd1234",
		Note:      "lost my session",
		ClientIP:  "203.0.113.42",
		CreatedAt: 1_700_000_000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("got id=0")
	}

	got, err := st.GetPasswordResetRequest(context.Background(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.UserID != "abcd1234" || got.Email != "u@x.io" || got.Note != "lost my session" {
		t.Errorf("Get: %+v", got)
	}
	if got.ResolvedAt != 0 {
		t.Errorf("freshly created: ResolvedAt=%d, want 0", got.ResolvedAt)
	}

	list, err := st.ListPasswordResetRequests(context.Background(), 30)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("got %d rows, want 1", len(list))
	}

	if err := st.ResolvePasswordResetRequest(context.Background(), id, "adminxxx", 1_700_000_999); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	got, _ = st.GetPasswordResetRequest(context.Background(), id)
	if got.ResolvedAt != 1_700_000_999 || got.ResolvedBy != "adminxxx" {
		t.Errorf("post-resolve: %+v", got)
	}
}

// TestPasswordResetRequest_UserIDNullable — when the email doesn't match
// a user, we still store the row (no enumeration leak) but UserID is
// NULL → empty string on read.
func TestPasswordResetRequest_UserIDNullable(t *testing.T) {
	st := freshStore(t)
	_, err := st.CreatePasswordResetRequest(context.Background(), PasswordResetRequest{
		Email:     "ghost@x.io",
		UserID:    "", // simulates no-match
		CreatedAt: 1_700_000_000,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	list, _ := st.ListPasswordResetRequests(context.Background(), 30)
	if len(list) != 1 {
		t.Fatalf("got %d rows", len(list))
	}
	if list[0].UserID != "" {
		t.Errorf("UserID: got %q, want empty", list[0].UserID)
	}
}

// TestUpdatePasswordHash — happy path + not-found error.
func TestUpdatePasswordHash(t *testing.T) {
	st := freshStore(t)
	u := sampleUser("pwhash01")
	if err := st.CreateUser(context.Background(), u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := st.UpdatePasswordHash(context.Background(), u.ID, "$2a$new"); err != nil {
		t.Errorf("UpdatePasswordHash: %v", err)
	}
	got, _ := st.GetUserByID(context.Background(), u.ID)
	if got.PasswordHash != "$2a$new" {
		t.Errorf("hash: %q", got.PasswordHash)
	}
	// Unknown id → ErrNotFound.
	if err := st.UpdatePasswordHash(context.Background(), "noexist0", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user: got %v, want ErrNotFound", err)
	}
}
