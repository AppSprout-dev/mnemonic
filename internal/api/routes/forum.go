package routes

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
	"github.com/google/uuid"
)

// mentionPattern matches @agent mentions in forum post content.
var mentionPattern = regexp.MustCompile(`@(retrieval|metacognition|encoding|episoding|consolidation|dreaming|abstraction|perception)`)

// extractMentions parses @agent mentions from post content.
func extractMentions(content string) []string {
	matches := mentionPattern.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var mentions []string
	for _, m := range matches {
		if len(m) > 1 && !seen[m[1]] {
			seen[m[1]] = true
			mentions = append(mentions, m[1])
		}
	}
	return mentions
}

// CreateForumPostRequest is the JSON body for creating a forum post.
type CreateForumPostRequest struct {
	Content    string `json:"content"`
	ThreadID   string `json:"thread_id,omitempty"`   // empty = new thread
	ParentID   string `json:"parent_id,omitempty"`   // empty = reply to thread root
	CategoryID string `json:"category_id,omitempty"` // sub-forum for new threads (default: "discussions")
	EpisodeID  string `json:"episode_id,omitempty"`  // if posting from an episode thread view
}

// HandleListForumCategories returns the forum index with category summaries.
// GET /api/v1/forum/categories
func HandleListForumCategories(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		summaries, err := s.ListForumCategorySummaries(ctx)
		if err != nil {
			log.Error("failed to list forum categories", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list categories", "STORE_ERROR")
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"categories": summaries,
		})
	}
}

// HandleListForumThreads returns all forum threads with reply counts.
// GET /api/v1/forum/threads?limit=20&offset=0
func HandleListForumThreads(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := 20
		offset := 0
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if v := r.URL.Query().Get("offset"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				offset = n
			}
		}

		categoryID := r.URL.Query().Get("category")

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var threads []store.ForumThread
		var err error
		if categoryID != "" {
			threads, err = s.ListForumThreadsByCategory(ctx, categoryID, limit, offset)
		} else {
			threads, err = s.ListForumThreads(ctx, limit, offset)
		}
		if err != nil {
			log.Error("failed to list forum threads", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list threads", "STORE_ERROR")
			return
		}

		count, _ := s.CountForumPosts(ctx)

		writeJSON(w, http.StatusOK, map[string]any{
			"threads":     threads,
			"total_posts": count,
		})
	}
}

// HandleGetForumThread returns all posts in a thread.
// GET /api/v1/forum/threads/{id}
func HandleGetForumThread(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "thread id is required", "MISSING_ID")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		posts, err := s.ListForumPostsByThread(ctx, id, 200)
		if err != nil {
			log.Error("failed to get forum thread", "error", err, "thread_id", id)
			writeError(w, http.StatusInternalServerError, "failed to get thread", "STORE_ERROR")
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"thread_id": id,
			"posts":     posts,
			"count":     len(posts),
		})
	}
}

// HandleCreateForumPost creates a new forum post or reply.
// POST /api/v1/forum/posts
func HandleCreateForumPost(s store.Store, bus events.Bus, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
		defer func() { _ = r.Body.Close() }()

		var req CreateForumPostRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
			return
		}

		content := strings.TrimSpace(req.Content)
		if content == "" {
			writeError(w, http.StatusBadRequest, "content is required", "MISSING_FIELD")
			return
		}

		now := time.Now()
		postID := uuid.New().String()

		// Determine thread context
		threadID := req.ThreadID
		parentID := req.ParentID
		categoryID := req.CategoryID
		if threadID == "" {
			// New thread: thread_id = post id
			threadID = postID
			if categoryID == "" {
				categoryID = "discussions" // default sub-forum for human posts
			}
		}
		if parentID == "" && threadID != postID {
			// Reply without explicit parent — parent is thread root
			parentID = threadID
		}

		// Extract @mentions
		mentions := extractMentions(content)

		post := store.ForumPost{
			ID:         postID,
			ParentID:   parentID,
			ThreadID:   threadID,
			AuthorType: "human",
			AuthorName: "Human",
			AuthorKey:  "",
			Content:    content,
			Mentions:   mentions,
			MemoryIDs:  []string{},
			CategoryID: categoryID,
			State:      "active",
			CreatedAt:  now,
			UpdatedAt:  now,
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := s.WriteForumPost(ctx, post); err != nil {
			log.Error("failed to create forum post", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to create post", "STORE_ERROR")
			return
		}

		// Publish forum post event
		_ = bus.Publish(ctx, events.ForumPostCreated{
			PostID:     postID,
			ThreadID:   threadID,
			ParentID:   parentID,
			AuthorType: "human",
			AuthorName: "Human",
			Content:    content,
			Mentions:   mentions,
			Ts:         now,
		})

		// Publish mention events for each @agent
		for _, agentKey := range mentions {
			_ = bus.Publish(ctx, events.ForumMentionDetected{
				PostID:    postID,
				ThreadID:  threadID,
				AgentKey:  agentKey,
				Content:   content,
				EpisodeID: req.EpisodeID,
				Ts:        now,
			})
		}

		log.Info("forum post created",
			"post_id", postID,
			"thread_id", threadID,
			"mentions", mentions,
		)

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":        postID,
			"thread_id": threadID,
			"mentions":  mentions,
		})
	}
}

// HandleGetForumPost returns a single forum post.
// GET /api/v1/forum/posts/{id}
func HandleGetForumPost(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "post id is required", "MISSING_ID")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		post, err := s.GetForumPost(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "post not found", "NOT_FOUND")
				return
			}
			log.Error("failed to get forum post", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to get post", "STORE_ERROR")
			return
		}

		writeJSON(w, http.StatusOK, post)
	}
}

// UpdateForumPostRequest is the JSON body for updating a forum post state.
type UpdateForumPostRequest struct {
	State string `json:"state"` // "active", "archived", "internalized"
}

// HandleUpdateForumPost updates a forum post's state.
// PATCH /api/v1/forum/posts/{id}
func HandleUpdateForumPost(s store.Store, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "post id is required", "MISSING_ID")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		defer func() { _ = r.Body.Close() }()

		var req UpdateForumPostRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body", "INVALID_REQUEST")
			return
		}

		validStates := map[string]bool{"active": true, "archived": true, "internalized": true}
		if !validStates[req.State] {
			writeError(w, http.StatusBadRequest, "state must be active, archived, or internalized", "INVALID_STATE")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := s.UpdateForumPostState(ctx, id, req.State); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "post not found", "NOT_FOUND")
				return
			}
			log.Error("failed to update forum post", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to update post", "STORE_ERROR")
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

// InternalizeRequest is the JSON body for internalizing a forum post.
type InternalizeRequest struct {
	Type string `json:"type,omitempty"` // memory type: "insight", "decision", etc. Default: "insight"
}

// HandleInternalizeForumPost absorbs a forum post into the memory system.
// POST /api/v1/forum/posts/{id}/internalize
func HandleInternalizeForumPost(s store.Store, bus events.Bus, log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "post id is required", "MISSING_ID")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		defer func() { _ = r.Body.Close() }()

		var req InternalizeRequest
		// Body is optional — allow empty
		_ = json.NewDecoder(r.Body).Decode(&req)
		memType := req.Type
		if memType == "" {
			memType = "insight"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Load the post
		post, err := s.GetForumPost(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "post not found", "NOT_FOUND")
				return
			}
			log.Error("failed to get forum post for internalization", "error", err, "id", id)
			writeError(w, http.StatusInternalServerError, "failed to get post", "STORE_ERROR")
			return
		}

		if post.State == "internalized" {
			writeError(w, http.StatusConflict, "post already internalized", "ALREADY_INTERNALIZED")
			return
		}

		// Create a raw memory from the post content
		rawID := uuid.New().String()
		raw := store.RawMemory{
			ID:              rawID,
			Timestamp:       post.CreatedAt,
			Source:          "forum",
			Type:            memType,
			Content:         post.Content,
			Metadata:        map[string]any{"forum_post_id": post.ID, "author": post.AuthorName},
			HeuristicScore:  1.0,
			InitialSalience: 0.85,
			Processed:       false,
			CreatedAt:       post.CreatedAt,
		}

		if err := s.WriteRaw(ctx, raw); err != nil {
			log.Error("failed to write raw memory from forum post", "error", err, "post_id", id)
			writeError(w, http.StatusInternalServerError, "failed to internalize", "STORE_ERROR")
			return
		}

		// Publish event to enter encoding pipeline
		_ = bus.Publish(ctx, events.RawMemoryCreated{
			ID:             rawID,
			Source:         "forum",
			HeuristicScore: 1.0,
			Salience:       0.85,
			Ts:             post.CreatedAt,
		})

		// Mark post as internalized
		if err := s.UpdateForumPostState(ctx, id, "internalized"); err != nil {
			log.Warn("failed to update post state after internalization", "error", err, "post_id", id)
		}

		log.Info("forum post internalized",
			"post_id", id,
			"raw_memory_id", rawID,
			"type", memType,
		)

		writeJSON(w, http.StatusOK, map[string]any{
			"raw_memory_id": rawID,
			"type":          memType,
			"status":        "internalized",
		})
	}
}
