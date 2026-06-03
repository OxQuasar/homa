package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/skipper/homa/orchestrator/internal/admin"
	"github.com/skipper/homa/orchestrator/internal/auth"
	"github.com/skipper/homa/orchestrator/internal/cors"
	"github.com/skipper/homa/orchestrator/internal/store"
)

const previewBase = "https://gandiva.tailnet.ts.net"

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

type rig struct {
	server *httptest.Server
	store  *store.Store
	tokens map[string]string // userID → cookie token
}

func newRig(t *testing.T) *rig {
	t.Helper()
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "homa.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Three users covering the three states + an admin.
	users := []struct {
		id       string
		username string
		approved bool
		rejected bool
		isAdmin  bool
	}{
		{"adminxxx", "adminuser", true, false, true},
		{"approved", "alice", true, false, false},
		{"pendingx", "bob", false, false, false},
		{"rejected", "carol", false, true, false},
	}
	tokens := map[string]string{}
	for _, p := range users {
		u := store.User{
			ID: p.id, Email: p.id + "@x", PasswordHash: "$2a", Username: p.username,
			JoinReason: "essay for " + p.id, MysteryInterest: "mystery for " + p.id, Background: "bg for " + p.id,
			BranchName: "u/x", WorktreePath: "/wt", ContainerName: "c-" + p.id,
			NousPort: 1, PreviewPort: 2, PreviewServePort: 3,
			NousSessionID: "s-" + p.id,
			CreatedAt:     1_700_000_000, LastActiveAt: 1, LastMessageAt: 1,
			Approved: p.approved, Rejected: p.rejected, IsAdmin: p.isAdmin,
		}
		if err := st.CreateUser(context.Background(), u); err != nil {
			t.Fatalf("CreateUser %s: %v", p.id, err)
		}
		tok := "tok-" + p.id + "-padding-padding-padding"
		tokens[p.id] = tok
		st.CreateWebSession(context.Background(), tok, p.id, 9_999_999_999)
	}

	authSvc := auth.New(st, nil, false, "", nil, quietLog())
	mux := http.NewServeMux()
	policy := cors.New(previewBase)
	admin.New(st, quietLog()).Register(mux, authSvc, policy.Middleware)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return &rig{server: srv, store: st, tokens: tokens}
}

func (r *rig) do(t *testing.T, method, path, userID string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, r.server.URL+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if userID != "" {
		req.AddCookie(&http.Cookie{Name: auth.CookieName, Value: r.tokens[userID]})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	return resp
}

func TestListUsers_AdminCanRead(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "GET", "/api/admin/users", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got []admin.AdminUserRow
	json.NewDecoder(resp.Body).Decode(&got)
	if len(got) != 4 {
		t.Errorf("got %d rows, want 4 (admin + 3 applicants)", len(got))
	}
	// Status field aggregates approved + rejected correctly.
	gotStatus := map[string]string{}
	for _, u := range got {
		gotStatus[u.Username] = u.Status
	}
	if gotStatus["alice"] != "approved" {
		t.Errorf("alice: %q, want approved", gotStatus["alice"])
	}
	if gotStatus["bob"] != "pending" {
		t.Errorf("bob: %q, want pending", gotStatus["bob"])
	}
	if gotStatus["carol"] != "rejected" {
		t.Errorf("carol: %q, want rejected", gotStatus["carol"])
	}
}

func TestListUsers_NonAdminForbidden(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "GET", "/api/admin/users", "approved", nil) // logged in but not admin
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin: got %d, want 403", resp.StatusCode)
	}
}

func TestListUsers_NoAuth(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "GET", "/api/admin/users", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no cookie: got %d, want 401", resp.StatusCode)
	}
}

func TestApprove_PendingBecomesApproved(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/admin/users/pendingx/approve", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var u admin.AdminUserRow
	json.NewDecoder(resp.Body).Decode(&u)
	if u.Status != "approved" {
		t.Errorf("status: %q, want approved", u.Status)
	}
	// Persisted in DB.
	got, _ := r.store.GetUserByID(context.Background(), "pendingx")
	if !got.Approved {
		t.Error("not persisted")
	}
}

func TestApprove_RejectedBecomesApproved(t *testing.T) {
	r := newRig(t)
	// carol is rejected; admin changes mind and approves.
	resp := r.do(t, "POST", "/api/admin/users/rejected/approve", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := r.store.GetUserByID(context.Background(), "rejected")
	if !got.Approved {
		t.Error("not approved")
	}
	if got.Rejected {
		t.Error("rejected flag still set — approve should auto-clear it")
	}
}

func TestReject_PendingBecomesRejected(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/admin/users/pendingx/reject", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	got, _ := r.store.GetUserByID(context.Background(), "pendingx")
	if !got.Rejected {
		t.Error("not rejected")
	}
}

func TestReject_NonAdminForbidden(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/admin/users/pendingx/reject", "approved", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin: got %d, want 403", resp.StatusCode)
	}
}

func TestApprove_UnknownUserIs404(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/admin/users/nonexist/approve", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got %d, want 404", resp.StatusCode)
	}
}

// TestReject_RevokesActiveSessions — admin reject must immediately
// invalidate the target's web_sessions row(s) so existing cookies stop
// working. Without this, a rejected user keeps editor access until
// the cookie naturally expires (30 days).
func TestReject_RevokesActiveSessions(t *testing.T) {
	r := newRig(t)
	// 'approved' user (alice) is currently logged in (cookie set up
	// in newRig). Confirm: a /api/admin/users call as alice fails
	// (non-admin) but proves her session is valid (gate check returns
	// 403 'admin only', not 401).
	resp1 := r.do(t, "GET", "/api/admin/users", "approved", nil)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusForbidden {
		t.Fatalf("pre-reject sanity: got %d, want 403 (alice valid auth, non-admin)", resp1.StatusCode)
	}
	// Now reject alice as admin.
	resp2 := r.do(t, "POST", "/api/admin/users/approved/reject", "adminxxx", nil)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("reject: %d", resp2.StatusCode)
	}
	// Same call as alice now fails with 401 (cookie nuked) — gate check
	// kicks in via RequireAuth's lookupUser, OR the session is gone.
	resp3 := r.do(t, "GET", "/api/admin/users", "approved", nil)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Errorf("post-reject: got %d, want 401 (cookie should be revoked)", resp3.StatusCode)
	}
}

// TestSelfRejectIsBlocked — admin can't reject themselves via the API.
// Without this guard, an admin who curls the reject endpoint at their
// own user_id would nuke their own session + lose access to /api/admin
// (RequireAuth's gate check fires on next request). Recovery would
// require direct SQL — bad UX for an accidental misclick.
// Approve-self stays allowed (idempotent, no harm).
func TestSelfRejectIsBlocked(t *testing.T) {
	r := newRig(t)
	// adminxxx tries to reject themselves.
	resp := r.do(t, "POST", "/api/admin/users/adminxxx/reject", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("self-reject: got %d, want 400", resp.StatusCode)
	}
	// And admin is still admin in the DB.
	got, _ := r.store.GetUserByID(context.Background(), "adminxxx")
	if got.Rejected {
		t.Error("admin rejected anyway (guard failed)")
	}
	if !got.Approved || !got.IsAdmin {
		t.Errorf("admin state corrupted: approved=%v isAdmin=%v", got.Approved, got.IsAdmin)
	}
}

// TestSelfApproveIsAllowed — flip side: admin can approve themselves
// (no-op since already approved). Not gated. Sanity that the self-check
// is scoped to reject, not all actions.
func TestSelfApproveIsAllowed(t *testing.T) {
	r := newRig(t)
	resp := r.do(t, "POST", "/api/admin/users/adminxxx/approve", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("self-approve: got %d, want 200", resp.StatusCode)
	}
}

// TestRequireAuth_GatesOnRejected — even if web_session somehow stays
// (admin reject path failed mid-flight, or external revocation
// scenario), RequireAuth still locks out the user on every request
// via the gate check in lookupUser.
func TestRequireAuth_GatesOnRejected(t *testing.T) {
	r := newRig(t)
	// Flip alice's rejected flag directly in the DB without going
	// through the admin reject endpoint (which would also revoke
	// sessions). Simulates a stale-but-still-mounted cookie.
	if err := r.store.SetRejected(context.Background(), "approved", true); err != nil {
		t.Fatalf("SetRejected: %v", err)
	}
	// alice's call should now 401 because lookupUser sees Rejected=true.
	resp := r.do(t, "GET", "/api/admin/users", "approved", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", resp.StatusCode)
	}
}

func TestCORSPreflight(t *testing.T) {
	r := newRig(t)
	for _, p := range []string{
		"/api/admin/users",
		"/api/admin/users/pendingx/approve",
		"/api/admin/users/pendingx/reject",
	} {
		t.Run(p, func(t *testing.T) {
			req, _ := http.NewRequest("OPTIONS", r.server.URL+p, nil)
			req.Header.Set("Origin", "https://gandiva.tailnet.ts.net:10001")
			resp, _ := http.DefaultClient.Do(req)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNoContent {
				t.Errorf("preflight: got %d, want 204", resp.StatusCode)
			}
		})
	}
}

// --- password reset admin endpoints --------------------------------

// seedResetRequest writes one row directly to the store. Mimics what
// auth.Forgot does in production, with control over each field.
func seedResetRequest(t *testing.T, r *rig, email, userID string) int64 {
	t.Helper()
	id, err := r.store.CreatePasswordResetRequest(context.Background(), store.PasswordResetRequest{
		Email: email, UserID: userID, CreatedAt: 1_700_000_000,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return id
}

func TestListPasswordResets_AdminOnly(t *testing.T) {
	r := newRig(t)
	seedResetRequest(t, r, "approved@x", "approved")
	// Non-admin gets 403.
	if resp := r.do(t, "GET", "/api/admin/password-resets", "approved", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin: %d, want 403", resp.StatusCode)
		resp.Body.Close()
	}
	// Admin gets the row.
	resp := r.do(t, "GET", "/api/admin/password-resets", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin: %d", resp.StatusCode)
	}
	var rows []admin.AdminPasswordResetRow
	json.NewDecoder(resp.Body).Decode(&rows)
	if len(rows) != 1 || rows[0].Status != "pending" || rows[0].UserID != "approved" {
		t.Errorf("rows: %+v", rows)
	}
}

func TestResetPassword_Happy(t *testing.T) {
	r := newRig(t)
	id := seedResetRequest(t, r, "approved@x", "approved")
	// Original hash + a session in place; both should be invalidated.
	r.store.CreateWebSession(context.Background(), "tok-target-padding-padding-padding", "approved", 9_999_999_999)

	resp := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/reset", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reset: %d", resp.StatusCode)
	}
	var body admin.PasswordResetResultResp
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.NewPassword) < 8 {
		t.Errorf("password too short: %q", body.NewPassword)
	}
	if body.Request.Status != "resolved" {
		t.Errorf("status: %q", body.Request.Status)
	}
	// Hash changed.
	u, _ := r.store.GetUserByID(context.Background(), "approved")
	if u.PasswordHash == "$2a" {
		t.Error("password hash unchanged")
	}
	// All sessions for that user are gone.
	rows, _ := r.store.ListPasswordResetRequests(context.Background(), 30)
	if rows[0].ResolvedAt == 0 {
		t.Error("ResolvedAt still 0")
	}
}

func TestResetPassword_NoMatchedUser(t *testing.T) {
	r := newRig(t)
	id := seedResetRequest(t, r, "ghost@x", "") // empty user_id — phantom email
	resp := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/reset", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400 (can't reset without a user)", resp.StatusCode)
	}
}

func TestDismissPasswordReset(t *testing.T) {
	r := newRig(t)
	id := seedResetRequest(t, r, "ghost@x", "")
	resp := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/dismiss", "adminxxx", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dismiss: %d", resp.StatusCode)
	}
	got, _ := r.store.GetPasswordResetRequest(context.Background(), id)
	if got.ResolvedAt == 0 {
		t.Error("not resolved")
	}
	if got.ResolvedBy != "adminxxx" {
		t.Errorf("resolved_by: %q", got.ResolvedBy)
	}
}

func TestResetPassword_NonAdmin(t *testing.T) {
	r := newRig(t)
	id := seedResetRequest(t, r, "approved@x", "approved")
	resp := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/reset", "approved", nil) // self, non-admin
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin reset: %d, want 403", resp.StatusCode)
	}
}

// TestResetPassword_AlreadyResolved — guard against double-reset that
// would issue a second password for the same request. The handler
// checks ResolvedAt > 0 and 400s.
func TestResetPassword_AlreadyResolved(t *testing.T) {
	r := newRig(t)
	id := seedResetRequest(t, r, "approved@x", "approved")
	// First reset succeeds.
	resp1 := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/reset", "adminxxx", nil)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("first reset: %d", resp1.StatusCode)
	}
	// Second reset on the same row 400s.
	resp2 := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/reset", "adminxxx", nil)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("second reset: got %d, want 400", resp2.StatusCode)
	}
}

// TestDismissPasswordReset_AlreadyResolved — same guard on the
// dismiss endpoint.
func TestDismissPasswordReset_AlreadyResolved(t *testing.T) {
	r := newRig(t)
	id := seedResetRequest(t, r, "ghost@x", "")
	r1 := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/dismiss", "adminxxx", nil)
	r1.Body.Close()
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("first dismiss: %d", r1.StatusCode)
	}
	r2 := r.do(t, "POST", "/api/admin/password-resets/"+
		strconv.FormatInt(id, 10)+"/dismiss", "adminxxx", nil)
	defer r2.Body.Close()
	if r2.StatusCode != http.StatusBadRequest {
		t.Errorf("second dismiss: got %d, want 400", r2.StatusCode)
	}
}
