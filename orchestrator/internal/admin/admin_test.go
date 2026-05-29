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
