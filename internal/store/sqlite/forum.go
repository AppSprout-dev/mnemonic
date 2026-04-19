package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	store "github.com/appsprout-dev/mnemonic/internal/store"
)

// BackfillEpisodeMemoryLinks fixes the race condition where memories were encoded
// before their raw observations were assigned to episodes. Iterates episodes (small set)
// and links any encoded memories found via raw_id lookup.
func (s *SQLiteStore) BackfillEpisodeMemoryLinks(ctx context.Context) (int, error) {
	episodes, err := s.ListEpisodes(ctx, "", 500, 0)
	if err != nil {
		return 0, fmt.Errorf("listing episodes: %w", err)
	}

	linked := 0
	for _, ep := range episodes {
		for _, rawID := range ep.RawMemoryIDs {
			if rawID == "" {
				continue
			}
			// Check if the encoded memory exists and needs linking
			mem, err := s.GetMemoryByRawID(ctx, rawID)
			if err != nil {
				continue
			}
			if mem.EpisodeID == ep.ID {
				continue
			}
			// Update the memory's episode_id
			_, err = s.db.ExecContext(ctx,
				`UPDATE memories SET episode_id = ? WHERE id = ? AND (episode_id IS NULL OR episode_id = '')`,
				ep.ID, mem.ID)
			if err == nil {
				linked++
			}
		}
		// Update episode.memory_ids
		var memIDs []string
		for _, rawID := range ep.RawMemoryIDs {
			mem, err := s.GetMemoryByRawID(ctx, rawID)
			if err != nil {
				continue
			}
			memIDs = append(memIDs, mem.ID)
		}
		if len(memIDs) > 0 {
			encoded, _ := encodeStringSlice(memIDs)
			_, _ = s.db.ExecContext(ctx,
				`UPDATE episodes SET memory_ids = ? WHERE id = ?`, encoded, ep.ID)
		}
	}
	return linked, nil
}

// SyncProjectCategories creates forum categories for any projects that don't have one yet.
func (s *SQLiteStore) SyncProjectCategories(ctx context.Context) (int, error) {
	// Get all known projects
	projects, err := s.ListProjects(ctx)
	if err != nil {
		return 0, fmt.Errorf("listing projects: %w", err)
	}

	created := 0
	for _, project := range projects {
		catID := "project-" + project
		// Check if category already exists
		_, err := s.GetForumCategory(ctx, catID)
		if err == nil {
			continue // already exists
		}

		cat := store.ForumCategory{
			ID:          catID,
			Name:        project,
			Slug:        "project-" + project,
			Description: "Threads about the " + project + " project",
			Icon:        "PJ",
			Color:       "var(--accent-green)",
			Type:        "project",
			SortOrder:   200,
			CreatedAt:   time.Now(),
		}
		if err := s.WriteForumCategory(ctx, cat); err != nil {
			continue
		}
		created++
	}
	return created, nil
}

// forumCategoryColumns is the standard column list for forum category queries.
const forumCategoryColumns = `id, name, slug, description, icon, color, type, sort_order, created_at`

// WriteForumCategory inserts a new forum category.
func (s *SQLiteStore) WriteForumCategory(ctx context.Context, cat store.ForumCategory) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO forum_categories (`+forumCategoryColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cat.ID, cat.Name, cat.Slug, cat.Description, cat.Icon, cat.Color, cat.Type, cat.SortOrder,
		cat.CreatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("writing forum category: %w", err)
	}
	return nil
}

// GetForumCategory retrieves a forum category by ID.
func (s *SQLiteStore) GetForumCategory(ctx context.Context, id string) (store.ForumCategory, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+forumCategoryColumns+` FROM forum_categories WHERE id = ?`, id)
	var cat store.ForumCategory
	var createdAtStr string
	err := row.Scan(&cat.ID, &cat.Name, &cat.Slug, &cat.Description, &cat.Icon, &cat.Color, &cat.Type, &cat.SortOrder, &createdAtStr)
	if err != nil {
		if err == sql.ErrNoRows {
			return cat, fmt.Errorf("forum category: %w", store.ErrNotFound)
		}
		return cat, fmt.Errorf("scanning forum category: %w", err)
	}
	cat.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
	return cat, nil
}

// ListForumCategories returns all categories ordered by sort_order.
func (s *SQLiteStore) ListForumCategories(ctx context.Context) ([]store.ForumCategory, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+forumCategoryColumns+` FROM forum_categories ORDER BY sort_order ASC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing forum categories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var cats []store.ForumCategory
	for rows.Next() {
		var cat store.ForumCategory
		var createdAtStr string
		if err := rows.Scan(&cat.ID, &cat.Name, &cat.Slug, &cat.Description, &cat.Icon, &cat.Color, &cat.Type, &cat.SortOrder, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scanning forum category row: %w", err)
		}
		cat.CreatedAt, _ = time.Parse(time.RFC3339, createdAtStr)
		cats = append(cats, cat)
	}
	return cats, rows.Err()
}

// ListForumCategorySummaries returns all categories with thread/post counts and last post.
func (s *SQLiteStore) ListForumCategorySummaries(ctx context.Context) ([]store.ForumCategorySummary, error) {
	cats, err := s.ListForumCategories(ctx)
	if err != nil {
		return nil, err
	}

	var summaries []store.ForumCategorySummary
	for _, cat := range cats {
		var threadCount, postCount int
		_ = s.db.QueryRowContext(ctx,
			`SELECT COUNT(DISTINCT thread_id), COUNT(*) FROM forum_posts WHERE category_id = ? AND state = 'active'`, cat.ID).Scan(&threadCount, &postCount)

		summary := store.ForumCategorySummary{
			Category:    cat,
			ThreadCount: threadCount,
			PostCount:   postCount,
		}

		// Get last post in this category
		row := s.db.QueryRowContext(ctx,
			`SELECT `+forumPostColumns+` FROM forum_posts WHERE category_id = ? AND state = 'active' ORDER BY created_at DESC LIMIT 1`, cat.ID)
		lastPost, err := scanForumPostFrom(row)
		if err == nil {
			summary.LastPost = &lastPost
		}

		summaries = append(summaries, summary)
	}
	return summaries, nil
}

// forumPostColumns is the standard column list for forum post queries.
const forumPostColumns = `id, parent_id, thread_id, author_type, author_name, author_key, content, mentions, memory_ids, event_ref, category_id, pinned, state, created_at, updated_at`

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
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
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
		nullableString(post.CategoryID),
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

	return s.scanForumThreadRows(rows)
}

// ListForumThreadsByCategory returns threads in a specific category.
func (s *SQLiteStore) ListForumThreadsByCategory(ctx context.Context, categoryID string, limit, offset int) ([]store.ForumThread, error) {
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
		WHERE fp.id = fp.thread_id AND fp.state = 'active' AND fp.category_id = ?
		ORDER BY COALESCE(rc.last_reply, fp.created_at) DESC
		LIMIT ? OFFSET ?`, categoryID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing forum threads by category: %w", err)
	}
	defer func() { _ = rows.Close() }()

	return s.scanForumThreadRows(rows)
}

func (s *SQLiteStore) scanForumThreadRows(rows *sql.Rows) ([]store.ForumThread, error) {
	var threads []store.ForumThread
	for rows.Next() {
		var post store.ForumPost
		var parentID, authorKey, eventRef, categoryID, mentionsStr, memoryIDsStr sql.NullString
		var pinned int
		var createdAtStr, updatedAtStr string
		var replyCount int
		var lastReply string

		err := rows.Scan(
			&post.ID, &parentID, &post.ThreadID, &post.AuthorType, &post.AuthorName,
			&authorKey, &post.Content, &mentionsStr, &memoryIDsStr, &eventRef,
			&categoryID, &pinned, &post.State, &createdAtStr, &updatedAtStr,
			&replyCount, &lastReply,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning forum thread row: %w", err)
		}

		post.ParentID = parentID.String
		post.AuthorKey = authorKey.String
		post.EventRef = eventRef.String
		post.CategoryID = categoryID.String
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

// UpdateForumPostContent rewrites a post's content body.
func (s *SQLiteStore) UpdateForumPostContent(ctx context.Context, id string, content string) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE forum_posts SET content = ?, updated_at = datetime('now') WHERE id = ?`, content, id)
	if err != nil {
		return fmt.Errorf("updating forum post content %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("forum post %s: %w", id, store.ErrNotFound)
	}
	return nil
}

// DeleteForumPost hard-deletes a forum post by ID. Returns an error if the post
// has child replies (callers must delete descendants first).
func (s *SQLiteStore) DeleteForumPost(ctx context.Context, id string) error {
	var childCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM forum_posts WHERE parent_id = ?`, id).Scan(&childCount); err != nil {
		return fmt.Errorf("checking forum post children %s: %w", id, err)
	}
	if childCount > 0 {
		return fmt.Errorf("forum post %s has %d replies — delete those first", id, childCount)
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM forum_posts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting forum post %s: %w", id, err)
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

// GetDailyDigestThread returns today's digest root post for a category, or ErrNotFound if none exists.
func (s *SQLiteStore) GetDailyDigestThread(ctx context.Context, categoryID string, date time.Time) (store.ForumPost, error) {
	dateStr := date.Format("2006-01-02")
	row := s.db.QueryRowContext(ctx, `
		SELECT `+forumPostColumns+`
		FROM forum_posts
		WHERE category_id = ?
		  AND id = thread_id
		  AND DATE(created_at) = ?
		  AND state = 'active'
		ORDER BY created_at DESC
		LIMIT 1`, categoryID, dateStr)
	return scanForumPost(row)
}

// scanForumPostFrom scans a single ForumPost from any scanner.
func scanForumPostFrom(s scanner) (store.ForumPost, error) {
	var post store.ForumPost
	var parentID, authorKey, eventRef, categoryID, mentionsStr, memoryIDsStr sql.NullString
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
		&categoryID,
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
	post.CategoryID = categoryID.String
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
