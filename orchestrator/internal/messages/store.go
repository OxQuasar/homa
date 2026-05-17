// Package messages implements the direct-message API:
//   GET  /api/messages/conversations         list peers + last msg + unread
//   GET  /api/messages/with/<userId>         thread (oldest first) + mark-read
//   POST /api/messages/with/<userId>         send (body: {content})
//   GET  /api/messages/unread-count          {count: N} for the tab badge
//
// All endpoints require an authenticated cookie. CORS-wrapped (same
// posture as forum/usersapi) even though the editor is same-origin —
// keeps the policy uniform across /api/* endpoints.
package messages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound — recipient userid doesn't resolve to a user, or no
// thread exists between the calling user and the requested peer when
// the API requires one. Maps to 404 at the handler.
var ErrNotFound = errors.New("messages: not found")

// Message is the wire shape for a single DM in a thread.
type Message struct {
	ID             int64  `json:"id"`
	SenderID       string `json:"sender_id"`
	SenderUsername string `json:"sender_username"`
	Content        string `json:"content"`
	CreatedAt      int64  `json:"created_at"`
}

// Conversation is the wire shape for one row in the conversations list.
type Conversation struct {
	PeerID        string `json:"peer_id"`
	PeerUsername  string `json:"peer_username"`
	LastAt        int64  `json:"last_at"`         // unix s of most recent message
	LastPreview   string `json:"last_preview"`    // truncated to ~120 chars for list display
	UnreadCount   int    `json:"unread_count"`    // unread messages FROM the peer TO calling user
}

// Store wraps the shared *sql.DB with messages queries.
type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CreateMessage inserts a new DM and returns the hydrated row (with
// sender_username joined). Caller has already authenticated as sender.
func (s *Store) CreateMessage(ctx context.Context, senderID, recipientID, content string, createdAt int64) (*Message, error) {
	// Sanity: recipient must exist. Cheap pre-check so we return a
	// clean 404 rather than an opaque FK error (SQLite FK enforcement
	// is off by default in this DB).
	var ok int
	if err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM users WHERE id = ?`, recipientID).Scan(&ok); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO private_messages
		(sender_id, recipient_id, content, created_at) VALUES (?, ?, ?, ?)`,
		senderID, recipientID, content, createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getMessage(ctx, id)
}

// getMessage returns one message by id with sender_username joined.
// Internal helper; the API doesn't expose single-message GET.
func (s *Store) getMessage(ctx context.Context, id int64) (*Message, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		m.id, m.sender_id, COALESCE(u.username, ''), m.content, m.created_at
		FROM private_messages m LEFT JOIN users u ON u.id = m.sender_id
		WHERE m.id = ?`, id)
	var m Message
	if err := row.Scan(&m.ID, &m.SenderID, &m.SenderUsername, &m.Content, &m.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// ListThread returns every message between user and peer, ordered
// oldest-first (chat-convention: scroll to bottom). Empty slice on
// no-thread; ErrNotFound if peer doesn't exist.
func (s *Store) ListThread(ctx context.Context, userID, peerID string) ([]Message, error) {
	var ok int
	if err := s.db.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, peerID).Scan(&ok); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		m.id, m.sender_id, COALESCE(u.username, ''), m.content, m.created_at
		FROM private_messages m LEFT JOIN users u ON u.id = m.sender_id
		WHERE (m.sender_id = ? AND m.recipient_id = ?)
		   OR (m.sender_id = ? AND m.recipient_id = ?)
		ORDER BY m.created_at ASC, m.id ASC`,
		userID, peerID, peerID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SenderID, &m.SenderUsername, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MarkRead sets read_at=now on every message in the (userID ← peerID)
// direction where read_at is still null. Returns the number of rows
// updated; the handler logs but the caller doesn't need it.
func (s *Store) MarkRead(ctx context.Context, userID, peerID string, now int64) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE private_messages
		SET read_at = ?
		WHERE recipient_id = ? AND sender_id = ? AND read_at IS NULL`,
		now, userID, peerID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnreadCount returns the total number of unread DMs across all
// senders to userID. Backs the badge polling endpoint.
func (s *Store) UnreadCount(ctx context.Context, userID string) (int, error) {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM private_messages
		WHERE recipient_id = ? AND read_at IS NULL`, userID)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListConversations returns one row per peer the user has exchanged
// DMs with: peer_id, peer_username, latest-message timestamp + preview,
// and unread count for that peer.
//
// Implementation: distinct-peers query → per-peer follow-up. N+1 in
// the small (single-operator, low-traffic) case; fine. If conversation
// counts ever explode we can switch to a single CTE.
func (s *Store) ListConversations(ctx context.Context, userID string) ([]Conversation, error) {
	peerRows, err := s.db.QueryContext(ctx, `SELECT DISTINCT
		CASE WHEN sender_id = ? THEN recipient_id ELSE sender_id END AS peer
		FROM private_messages
		WHERE sender_id = ? OR recipient_id = ?`,
		userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer peerRows.Close()
	var peers []string
	for peerRows.Next() {
		var p string
		if err := peerRows.Scan(&p); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	if err := peerRows.Err(); err != nil {
		return nil, err
	}

	out := make([]Conversation, 0, len(peers))
	for _, p := range peers {
		c, err := s.conversationFor(ctx, userID, p)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	// Sort newest-first (LastAt DESC). Doing it in Go since the per-peer
	// query did the per-peer fetching; sorting in SQL would force a
	// different shape.
	sortConversationsByLastAt(out)
	return out, nil
}

// conversationFor builds one Conversation row for the user↔peer pair.
// Three small queries: most-recent message (for last_at + content),
// unread count, peer username. Could be combined; clarity beats
// micro-opt at this scale.
func (s *Store) conversationFor(ctx context.Context, userID, peerID string) (Conversation, error) {
	c := Conversation{PeerID: peerID}
	// Latest msg in either direction.
	row := s.db.QueryRowContext(ctx, `SELECT content, created_at
		FROM private_messages
		WHERE (sender_id = ? AND recipient_id = ?)
		   OR (sender_id = ? AND recipient_id = ?)
		ORDER BY created_at DESC, id DESC LIMIT 1`,
		userID, peerID, peerID, userID)
	var content string
	if err := row.Scan(&content, &c.LastAt); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	c.LastPreview = preview(content)
	// Unread count for this thread (msgs from peer to user, read_at null).
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM private_messages
		 WHERE recipient_id = ? AND sender_id = ? AND read_at IS NULL`,
		userID, peerID).Scan(&c.UnreadCount); err != nil {
		return c, err
	}
	// Peer username (empty if user was deleted — shouldn't happen).
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(username,'') FROM users WHERE id = ?`,
		peerID).Scan(&c.PeerUsername); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return c, err
	}
	return c, nil
}

// previewMaxChars caps the conversations-list preview text. UI shows
// this in a single line; longer messages add ellipsis.
const previewMaxChars = 120

func preview(s string) string {
	if len(s) <= previewMaxChars {
		return s
	}
	return s[:previewMaxChars] + "…"
}

// sortConversationsByLastAt — newest first; in-place. Tiny custom
// sort to avoid pulling in sort.Slice for one call site.
func sortConversationsByLastAt(c []Conversation) {
	// Insertion sort — n is small (one entry per peer the user
	// has messaged). Avoids any allocation.
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].LastAt > c[j-1].LastAt; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}
