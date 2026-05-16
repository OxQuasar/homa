CREATE TABLE IF NOT EXISTS users (
  id                  TEXT PRIMARY KEY,        -- random short id, e.g. 8 hex chars
  email               TEXT UNIQUE NOT NULL,
  password_hash       TEXT NOT NULL,           -- bcrypt
  name                TEXT,
  branch_name         TEXT NOT NULL,           -- "user/<id>"
  worktree_path       TEXT NOT NULL,           -- absolute path on host
  container_name      TEXT NOT NULL,           -- "homa-user-<id>"
  nous_port           INTEGER NOT NULL,        -- host port → sandbox :9000
  preview_port        INTEGER NOT NULL,        -- host port → sandbox :5173
  preview_serve_port  INTEGER NOT NULL,        -- tailscale-serve HTTPS port
  nous_session_id     TEXT NOT NULL DEFAULT '',-- pinned nous session id (passed in Hello)
  created_at          INTEGER NOT NULL,        -- unix seconds UTC
  last_active_at      INTEGER NOT NULL,        -- bumped by WS keepalive (proxy ticker)
  last_message_at     INTEGER NOT NULL DEFAULT 0 -- bumped on user `run` requests / login; drives idle-compact lifecycle
);

CREATE TABLE IF NOT EXISTS web_sessions (
  token       TEXT PRIMARY KEY,                -- 32 random bytes hex
  user_id     TEXT NOT NULL REFERENCES users(id),
  expires_at  INTEGER NOT NULL                 -- unix seconds UTC
);

CREATE INDEX IF NOT EXISTS idx_web_sessions_user ON web_sessions(user_id);
