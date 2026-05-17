// Package forum implements the shared multi-tenant forum API:
//   GET  /api/forum/topics                  list topics (newest first)
//   POST /api/forum/topics                  create topic
//   GET  /api/forum/topics/{id}/posts       list posts in a topic
//   POST /api/forum/topics/{id}/posts       reply
//
// All endpoints require an authenticated user (cookie). The handler
// joins author info from users.username so the wire shape carries
// display data the caller can render without an extra fetch.
package forum

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound — topic id passed in URL doesn't exist.
var ErrNotFound = errors.New("forum: not found")

// Topic is the wire shape (also used as the row type).
type Topic struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"` // joined from users.username
	CreatedAt  int64  `json:"created_at"`
	PostCount  int    `json:"post_count"`
}

// Post is the wire shape for a single reply within a topic.
type Post struct {
	ID         int64  `json:"id"`
	TopicID    int64  `json:"topic_id"`
	AuthorID   string `json:"author_id"`
	AuthorName string `json:"author_name"`
	Content    string `json:"content"`
	CreatedAt  int64  `json:"created_at"`
}

// Store wraps *sql.DB with the forum-scoped queries. The same DB
// instance backs users / web_sessions; we just don't import the store
// package directly (would import-cycle via auth → forum → store).
type Store struct {
	db *sql.DB
}

// NewStore constructs a forum Store. Caller passes the same *sql.DB
// that store.Store uses; tables already exist via schema.sql.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

// CreateTopic inserts a new topic and returns the full hydrated row
// (including AuthorName from the joined users.username).
func (s *Store) CreateTopic(ctx context.Context, title, authorID string, createdAt int64) (*Topic, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO forum_topics (title, author_id, created_at) VALUES (?, ?, ?)`,
		title, authorID, createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert topic: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return s.GetTopic(ctx, id)
}

// topicSelect is the join + computed-count expression used by
// ListTopics / GetTopic. Centralised so both queries surface the same
// shape and a future column add lands one place.
const topicSelect = `SELECT
	t.id, t.title, t.author_id,
	COALESCE(u.username, ''),
	t.created_at,
	(SELECT COUNT(1) FROM forum_posts p WHERE p.topic_id = t.id)
FROM forum_topics t LEFT JOIN users u ON u.id = t.author_id`

// GetTopic returns a single topic by id. ErrNotFound on miss.
func (s *Store) GetTopic(ctx context.Context, id int64) (*Topic, error) {
	row := s.db.QueryRowContext(ctx, topicSelect+` WHERE t.id = ?`, id)
	var tp Topic
	if err := row.Scan(&tp.ID, &tp.Title, &tp.AuthorID, &tp.AuthorName,
		&tp.CreatedAt, &tp.PostCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &tp, nil
}

// ListTopics returns every topic, newest first.
func (s *Store) ListTopics(ctx context.Context) ([]Topic, error) {
	rows, err := s.db.QueryContext(ctx, topicSelect+` ORDER BY t.created_at DESC, t.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Topic
	for rows.Next() {
		var tp Topic
		if err := rows.Scan(&tp.ID, &tp.Title, &tp.AuthorID, &tp.AuthorName,
			&tp.CreatedAt, &tp.PostCount); err != nil {
			return nil, err
		}
		out = append(out, tp)
	}
	return out, rows.Err()
}

// CreatePost inserts a reply into topicID and returns the hydrated row.
// Errors with ErrNotFound if topicID doesn't exist (REFERENCES doesn't
// fire for foreign keys without `PRAGMA foreign_keys = ON`, so we
// pre-check explicitly).
func (s *Store) CreatePost(ctx context.Context, topicID int64, authorID, content string, createdAt int64) (*Post, error) {
	if _, err := s.GetTopic(ctx, topicID); err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO forum_posts (topic_id, author_id, content, created_at) VALUES (?, ?, ?, ?)`,
		topicID, authorID, content, createdAt)
	if err != nil {
		return nil, fmt.Errorf("insert post: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getPost(ctx, id)
}

const postSelect = `SELECT
	p.id, p.topic_id, p.author_id,
	COALESCE(u.username, ''),
	p.content, p.created_at
FROM forum_posts p LEFT JOIN users u ON u.id = p.author_id`

func (s *Store) getPost(ctx context.Context, id int64) (*Post, error) {
	row := s.db.QueryRowContext(ctx, postSelect+` WHERE p.id = ?`, id)
	var p Post
	if err := row.Scan(&p.ID, &p.TopicID, &p.AuthorID, &p.AuthorName,
		&p.Content, &p.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ListPostsByTopic returns every post in a topic, newest first.
// Empty slice (not nil + not error) when the topic exists with no posts.
// ErrNotFound when the topic id doesn't exist.
func (s *Store) ListPostsByTopic(ctx context.Context, topicID int64) ([]Post, error) {
	if _, err := s.GetTopic(ctx, topicID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		postSelect+` WHERE p.topic_id = ? ORDER BY p.created_at DESC, p.id DESC`, topicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Post{}
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.TopicID, &p.AuthorID, &p.AuthorName,
			&p.Content, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
