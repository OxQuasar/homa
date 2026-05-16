// Package codeserver supports the per-user browser-VSCode integration
// (homa's "Open in VS Code" path). Two concerns:
//
//   - Secret + per-user password derivation (deterministic so containers
//     get the same password across respawns without storing per-user
//     passwords on disk).
//   - URL construction for the one-shot URL-token login the editor
//     sends the user to.
//
// Phase 1 of memories/homa/codeserver.md. See that doc for the broader
// architecture.
package codeserver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// secretBytes is the length of the random seed stored on disk and used
// to derive every per-user code-server password. 32 bytes = 256 bits;
// the derived passwords themselves are truncated to 22 base64 chars
// (16 raw bytes = 128 bits — still well above brute-force ranges).
const secretBytes = 32

// passwordPrefix is how many bytes of the sha256 output we encode.
// 16 bytes → 22 url-safe base64 chars (sans padding). Plenty for a
// password whose threat model is "browser-history exposure on the
// user's own machine."
const passwordPrefix = 16

// LoadOrCreateSecret reads the secret at path. If the file doesn't
// exist, generates a fresh secret + writes it with mode 0600 (operator
// only). Returns the secret bytes either way.
//
// The secret persists across orchestrator restarts and is shared by
// all per-user passwords — rotating it rotates every user's code-server
// password.
func LoadOrCreateSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("codeserver: empty secret path")
	}
	if b, err := os.ReadFile(path); err == nil {
		if len(b) < secretBytes {
			return nil, fmt.Errorf("codeserver: secret at %s is %d bytes, want >=%d",
				path, len(b), secretBytes)
		}
		return b, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("codeserver: read secret %s: %w", path, err)
	}

	// First run: generate + persist.
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("codeserver: generate secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("codeserver: mkdir secret dir: %w", err)
	}
	if err := os.WriteFile(path, secret, 0o600); err != nil {
		return nil, fmt.Errorf("codeserver: write secret %s: %w", path, err)
	}
	return secret, nil
}

// PasswordFor returns the deterministic per-user code-server password.
// Same (secret, userID) → same password, every call. Lets us pass
// PASSWORD into the container at start time without storing per-user
// passwords in the DB; rotation = rewrite the secret file.
//
// Output: 22 url-safe base64 characters (raw, no '=' padding).
func PasswordFor(secret []byte, userID string) string {
	h := sha256.New()
	h.Write(secret)
	h.Write([]byte(userID))
	sum := h.Sum(nil)
	return base64.RawURLEncoding.EncodeToString(sum[:passwordPrefix])
}

// URLFor builds the URL the editor opens. Code-server v4.x doesn't
// support URL-token auto-login (the `tkn` param was a v3-era thing
// and was removed for security — URL params leak in browser history
// and referrer headers). Phase 1 instead runs code-server with
// `--auth none` and relies on tailscale-serve membership as the
// gate; the password argument is accepted but currently unused.
//
// host: externally-reachable hostname (e.g. "gandiva.kingfisher-celsius.ts.net")
// port: tailscale-serve port the orchestrator allocated for this user
// password: unused in Phase 1 — kept on the API so Phase 2's
//   reverse-proxy variant can pass it through to code-server's --auth
//   password mode without an interface change.
// folder: workspace path to open by default (e.g. "/workspace").
func URLFor(host string, port int, password, folder string) string {
	_ = password
	q := url.Values{}
	if folder != "" {
		q.Set("folder", folder)
	}
	if len(q) > 0 {
		return fmt.Sprintf("https://%s:%d/?%s", host, port, q.Encode())
	}
	return fmt.Sprintf("https://%s:%d/", host, port)
}
