package codeserver

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadOrCreateSecret_NewFile — first call creates the file with a
// fresh secret; the bytes returned match what's on disk.
func TestLoadOrCreateSecret_NewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	got, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	if len(got) != secretBytes {
		t.Errorf("secret len: got %d, want %d", len(got), secretBytes)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(onDisk) {
		t.Error("returned bytes don't match disk")
	}
}

// TestLoadOrCreateSecret_Idempotent — subsequent calls return the
// SAME secret (don't regenerate on every restart).
func TestLoadOrCreateSecret_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	first, _ := LoadOrCreateSecret(path)
	second, err := LoadOrCreateSecret(path)
	if err != nil {
		t.Fatalf("second LoadOrCreateSecret: %v", err)
	}
	if string(first) != string(second) {
		t.Error("secret changed between calls (would invalidate every user's password)")
	}
}

// TestLoadOrCreateSecret_Mode0600 — sanity that the file is operator-
// only readable. The secret is the master key for every user's
// code-server password; world-readable would expose them all.
func TestLoadOrCreateSecret_Mode0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if _, err := LoadOrCreateSecret(path); err != nil {
		t.Fatalf("LoadOrCreateSecret: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %o, want 0600", mode)
	}
}

// TestLoadOrCreateSecret_RejectShort — a truncated/corrupted secret
// file should error rather than silently produce weak passwords.
func TestLoadOrCreateSecret_RejectShort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("too short"), 0o600); err != nil {
		t.Fatalf("seed short secret: %v", err)
	}
	if _, err := LoadOrCreateSecret(path); err == nil {
		t.Error("LoadOrCreateSecret on short file: got nil, want error")
	}
}

// TestLoadOrCreateSecret_EmptyPath — argument validation.
func TestLoadOrCreateSecret_EmptyPath(t *testing.T) {
	if _, err := LoadOrCreateSecret(""); err == nil {
		t.Error("empty path: got nil err")
	}
}

// TestPasswordFor_Deterministic — same (secret, userID) → same password.
// This is the whole point — no DB column needed because we re-derive
// on each container start.
func TestPasswordFor_Deterministic(t *testing.T) {
	secret := []byte("a-stable-test-secret-of-suff-len")
	a := PasswordFor(secret, "abcd1234")
	b := PasswordFor(secret, "abcd1234")
	if a != b {
		t.Errorf("PasswordFor not deterministic: %q != %q", a, b)
	}
}

// TestPasswordFor_DifferentUsersDifferentPasswords — sanity that the
// derivation uses userID. Two users on the same secret get distinct
// passwords; otherwise a single leak compromises everyone.
func TestPasswordFor_DifferentUsersDifferentPasswords(t *testing.T) {
	secret := []byte("a-stable-test-secret-of-suff-len")
	a := PasswordFor(secret, "abcd1234")
	b := PasswordFor(secret, "efgh5678")
	if a == b {
		t.Errorf("two users got the same password: %q", a)
	}
}

// TestPasswordFor_SecretRotationChangesPasswords — rewriting the
// secret file rotates every user's password. Confirms the derivation
// is sensitive to the secret bytes too.
func TestPasswordFor_SecretRotationChangesPasswords(t *testing.T) {
	a := PasswordFor([]byte("secret-version-one-with-len-enow"), "abcd1234")
	b := PasswordFor([]byte("secret-version-two-with-len-enow"), "abcd1234")
	if a == b {
		t.Errorf("password didn't change with secret rotation: %q", a)
	}
}

// TestPasswordFor_Shape — 22 url-safe base64 chars; no padding, no
// non-url-safe characters (so the URL token survives query-string
// embedding without further escaping).
func TestPasswordFor_Shape(t *testing.T) {
	pw := PasswordFor([]byte("secret-of-suff-length-for-test-"), "u")
	if len(pw) != 22 {
		t.Errorf("password length: got %d, want 22", len(pw))
	}
	if strings.ContainsAny(pw, "+/=") {
		t.Errorf("password contains non-url-safe chars: %q", pw)
	}
}

// TestURLFor_Shape — builds the URL the editor opens. Verify host,
// port, tkn, folder all show up correctly + folder is omitted when
// empty.
func TestURLFor_Shape(t *testing.T) {
	got := URLFor("gandiva.example.com", 20001, "pw-token", "/workspace")
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("URL.Parse(%q): %v", got, err)
	}
	if u.Scheme != "https" {
		t.Errorf("scheme: got %q, want https", u.Scheme)
	}
	if u.Host != "gandiva.example.com:20001" {
		t.Errorf("host: got %q", u.Host)
	}
	if tkn := u.Query().Get("tkn"); tkn != "pw-token" {
		t.Errorf("tkn: got %q", tkn)
	}
	if folder := u.Query().Get("folder"); folder != "/workspace" {
		t.Errorf("folder: got %q", folder)
	}
}

func TestURLFor_OmitsEmptyFolder(t *testing.T) {
	got := URLFor("h", 1, "pw", "")
	if strings.Contains(got, "folder=") {
		t.Errorf("URL has folder= when empty was passed: %q", got)
	}
}
