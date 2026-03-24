package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	store "github.com/appsprout-dev/mnemonic/internal/store"
)

// forumPostColumns is the standard column list for forum post queries.
const forumPostColumns = `id, parent_id, thread_id, author_type, author_name, author_key, content, mentions, memory_ids, event_ref, pinned, state, created_at, updated_at`

// WriteForumPost inserts a new forum post.
func (s *SQLiteStore) WriteForumPost(ctx context.Context, post store.ForumPost) error {
	mentions, _ := encodeStringSlice(post.Mentions)
	memoryIDs, _ := encodeStringSlice(post.MemoryIDs)
	pinned := 0
	if post.Pinned {
		pinned = 1
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO forum_posts (`+forumPostColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		post.ID,
		nullableString(post.ParentID),
		post.ThreadID,
		post.AuthorType,
		post.AuthorName,
		post.AuthorKey,
		post.Content,
		mentions,
		memoryIDs,
		nullableString(post.EventRef),
		pinned,
		post.State,
		post.CreatedAt.Format(time.RFC3339),
		post.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("writing forum post: %w", err)
	}
	return nil
}

// GetForumPost retrieves a forum post by ID.
func (s *SQLiteStore) GetForumPost(ctx context.Context, id string) (store.ForumPost, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+forumPostColumns+` FROM forum_posts WHERE id = ?`, id)
	return scanForumPost(row)
}

// ListForumThreads returns root-level posts (threads) with reply counts.
func (s *SQLiteStore) ListForumThreads(ctx context.Context, limit, offset int) ([]store.ForumThread, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fp.`+forumPostColumns+`,
		       COALESCE(rc.reply_count, 0) AS reply_count,
		       COALESCE(rc.last_reply, fp.created_at) AS last_reply
		FROM forum_posts fp
		LEFT JOIN (
		    SELECT fp2.thread_id AS rc_thread_id,
		           COUNT(*) AS reply_count,
		           MAX(fp2.created_at) AS last_reply
		    FROM forum_posts fp2
		    WHERE fp2.id != fp2.thread_id AND fp2.state = 'active'
		    GROUP BY fp2.thread_id
		) rc ON rc.rc_thread_id = fp.id
		WHERE fp.id = fp.thread_id AND fp.state = 'active'
		ORDER BY COALESCE(rc.last_reply, fp.created_at) DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing forum threads: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var threads []store.ForumThread
	for rows.Next() {
		var post store.ForumPost
		var parentID, authorKey, eventRef, mentionsStr, memoryIDsStr sql.NullString
		var pinned int
		var createdAtStr, updatedAtStr string
		var replyCount int
		var lastReply string

		err := rows.Scan(
			&post.ID,
			&parentID,
			&post.ThreadID,
			&post.AuthorType,
			&post.AuthorName,
			&authorKey,
			&post.Content,
			&mentionsStr,
			&memoryIDsStr,
			&eventRef,
			&pinned,
			&post.State,
			&createdAtStr,
			&updatedAtStr,
			&replyCount,
			&lastReply,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning forum thread row: %w", err)
		}

		post.ParentID = parentID.String
		post.AuthorKey = authorKey.String
		post.EventRef = eventRef.String
		post.Mentions, _ = decodeStringSlice(mentionsStr.String)
		post.MemoryIDs, _ = decodeStringSlice(memoryIDsStr.String)
		post.Pinned = pinned != 0
		post.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		post.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

		lr, _ := time.Parse(time.RFC3339, lastReply)
		if lr.IsZero() {
			lr = post.CreatedAt
		}

		threads = append(threads, store.ForumThread{
			RootPost:   post,
			ReplyCount: replyCount,
			LastReply:  lr,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading forum thread rows: %w", err)
	}
	return threads, nil
}

// ListForumPostsByThread returns all posts in a thread ordered by creation time.
func (s *SQLiteStore) ListForumPostsByThread(ctx context.Context, threadID string, limit int) ([]store.ForumPost, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+forumPostColumns+` FROM forum_posts
		WHERE thread_id = ? AND state = 'active'
		ORDER BY created_at ASC
		LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing forum posts by thread: %w", err)
	}
	return scanForumPostRows(rows)
}

// UpdateForumPostState updates the state of a forum post.
func (s *SQLiteStore) UpdateForumPostState(ctx context.Context, id string, state string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE forum_posts SET state = ?, updated_at = datetime('now') WHERE id = ?`, state, id)
	if err != nil {
		return fmt.Errorf("updating forum post state %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("forum post %s: %w", id, store.ErrNotFound)
	}
	return nil
}

// CountForumPosts returns the total number of active forum posts.
func (s *SQLiteStore) CountForumPosts(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM forum_posts WHERE state = 'active'`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("counting forum posts: %w", err)
	}
	return count, nil
}

// scanForumPostFrom scans a single ForumPost from any scanner.
func scanForumPostFrom(s scanner) (store.ForumPost, error) {
	var post store.ForumPost
	var parentID, authorKey, eventRef, mentionsStr, memoryIDsStr sql.NullString
	var pinned int
	var createdAtStr, updatedAtStr string

	err := s.Scan(
		&post.ID,
		&parentID,
		&post.ThreadID,
		&post.AuthorType,
		&post.AuthorName,
		&authorKey,
		&post.Content,
		&mentionsStr,
		&memoryIDsStr,
		&eventRef,
		&pinned,
		&post.State,
		&createdAtStr,
		&updatedAtStr,
	)
	if err != nil {
		return post, err
	}

	post.ParentID = parentID.String
	post.AuthorKey = authorKey.String
	post.EventRef = eventRef.String
	post.Mentions, _ = decodeStringSlice(mentionsStr.String)
	post.MemoryIDs, _ = decodeStringSlice(memoryIDsStr.String)
	post.Pinned = pinned != 0
	post.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	post.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAtStr)

	return post, nil
}

// scanForumPost scans a single forum post row.
func scanForumPost(row *sql.Row) (store.ForumPost, error) {
	p, err := scanForumPostFrom(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return p, fmt.Errorf("forum post: %w", store.ErrNotFound)
		}
		return p, fmt.Errorf("scanning forum post: %w", err)
	}
	return p, nil
}

// scanForumPostRows scans multiple forum post rows.
func scanForumPostRows(rows *sql.Rows) ([]store.ForumPost, error) {
	defer func() { _ = rows.Close() }()
	var posts []store.ForumPost

	for rows.Next() {
		p, err := scanForumPostFrom(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning forum post row: %w", err)
		}
		posts = append(posts, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading forum post rows: %w", err)
	}
	return posts, nil
}
