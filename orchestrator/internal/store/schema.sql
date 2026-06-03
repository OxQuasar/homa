CREATE TABLE IF NOT EXISTS users (
  id                  TEXT PRIMARY KEY,        -- random short id, e.g. 8 hex chars
  email               TEXT UNIQUE NOT NULL,
  password_hash       TEXT NOT NULL,           -- bcrypt
  name                TEXT,                    -- optional, freeform
  username            TEXT NOT NULL DEFAULT '', -- required at signup; displayed publicly (forum etc); [a-z0-9_]{3,32}
  -- Signup application fields. Captured at /signup; operator reads via
  -- `homa review <userid>` to inform manual approval. NULL on rows
  -- created before this column was added.
  join_reason         TEXT,                    -- "Why are you interested in joining the White Tower?"
  mystery_interest    TEXT,                    -- "What mystery are you interested in investigating?"
  background          TEXT,                    -- "What is your background?"
  branch_name         TEXT NOT NULL,           -- "user/<id>"
  worktree_path       TEXT NOT NULL,           -- absolute path on host
  container_name      TEXT NOT NULL,           -- "homa-user-<id>"
  nous_port           INTEGER NOT NULL,        -- host port → sandbox :9000
  preview_port        INTEGER NOT NULL,        -- host port → sandbox :5173
  preview_serve_port  INTEGER NOT NULL,        -- tailscale-serve HTTPS port
  nous_session_id     TEXT NOT NULL DEFAULT '',-- pinned nous session id (passed in Hello)
  created_at          INTEGER NOT NULL,        -- unix seconds UTC
  last_active_at      INTEGER NOT NULL,        -- bumped by WS keepalive (proxy ticker)
  last_message_at     INTEGER NOT NULL DEFAULT 0, -- bumped on user `run` requests / login; drives idle-compact lifecycle
  code_server_port    INTEGER NOT NULL DEFAULT 0, -- host port → sandbox :8443 (code-server)
  code_server_serve_port INTEGER NOT NULL DEFAULT 0, -- tailscale-serve HTTPS port for browser code-server access
  approved            INTEGER NOT NULL DEFAULT 0, -- 0 = application pending; 1 = approved by operator (homa approve)
  rejected            INTEGER NOT NULL DEFAULT 0, -- 0 = not rejected; 1 = application rejected via /api/admin (login refused)
  is_admin            INTEGER NOT NULL DEFAULT 0  -- 0 = regular user; 1 = admin (can access /api/admin/* + admin UI). Set via `homa promote`.
);

CREATE TABLE IF NOT EXISTS web_sessions (
  token       TEXT PRIMARY KEY,                -- 32 random bytes hex
  user_id     TEXT NOT NULL REFERENCES users(id),
  expires_at  INTEGER NOT NULL                 -- unix seconds UTC
);

CREATE INDEX IF NOT EXISTS idx_web_sessions_user ON web_sessions(user_id);

-- Forum: shared, multi-tenant. Any logged-in user can create topics
-- and post replies. Read access is also auth-required (forum lives in
-- the authed section of the public site).
CREATE TABLE IF NOT EXISTS forum_topics (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT NOT NULL,
  author_id  TEXT NOT NULL REFERENCES users(id),
  created_at INTEGER NOT NULL                       -- unix seconds UTC
);
CREATE INDEX IF NOT EXISTS idx_forum_topics_created
  ON forum_topics(created_at DESC);

CREATE TABLE IF NOT EXISTS forum_posts (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  topic_id   INTEGER NOT NULL REFERENCES forum_topics(id),
  author_id  TEXT NOT NULL REFERENCES users(id),
  content    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_forum_posts_topic_created
  ON forum_posts(topic_id, created_at DESC);

-- Direct messages between users. Flat 1:1 for v1 (no conversations
-- table) — a "thread" is implied by the (sender, recipient) pair.
-- read_at is null while unread; set when the recipient first opens
-- the thread.
CREATE TABLE IF NOT EXISTS private_messages (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  sender_id    TEXT NOT NULL REFERENCES users(id),
  recipient_id TEXT NOT NULL REFERENCES users(id),
  content      TEXT NOT NULL,
  created_at   INTEGER NOT NULL,                -- unix seconds UTC
  read_at      INTEGER                          -- null = unread
);
-- Unread-count queries: (recipient_id, read_at IS NULL).
CREATE INDEX IF NOT EXISTS idx_pm_recipient_unread
  ON private_messages(recipient_id, read_at);
-- Thread lookup in both directions; ORDER BY created_at uses these.
CREATE INDEX IF NOT EXISTS idx_pm_sent
  ON private_messages(sender_id, recipient_id, created_at);
CREATE INDEX IF NOT EXISTS idx_pm_received
  ON private_messages(recipient_id, sender_id, created_at);

-- Password reset requests — created via POST /forgot, resolved by an
-- admin via the /admin UI (homa reset-password CLI is a separate path
-- without going through this table). user_id is nullable: when the
-- email doesn't match any account we still record the row so the
-- admin sees the attempt (and we never leak account existence via
-- the public response).
CREATE TABLE IF NOT EXISTS password_reset_requests (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  email        TEXT NOT NULL,                -- as typed by the user
  user_id      TEXT,                          -- nullable; resolved at insertion
  note         TEXT,                          -- optional free-form context
  client_ip    TEXT,                          -- best-effort, for admin context
  created_at   INTEGER NOT NULL,              -- unix seconds UTC
  resolved_at  INTEGER,                       -- null = pending
  resolved_by  TEXT                           -- admin user_id who handled
);
-- Pending-list query: WHERE resolved_at IS NULL ORDER BY created_at.
CREATE INDEX IF NOT EXISTS idx_prr_pending
  ON password_reset_requests(resolved_at, created_at);
