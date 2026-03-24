//go:build sqlite_fts5

package sqlite

import (
	"context"
	"testing"
	"time"

	store "github.com/appsprout-dev/mnemonic/internal/store"
)

func TestForumPostCRUD(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	// Create a thread (root post)
	root := store.ForumPost{
		ID:         "post-001",
		ThreadID:   "post-001", // root post: thread_id = id
		AuthorType: "human",
		AuthorName: "Caleb",
		AuthorKey:  "",
		Content:    "Hello, this is the first forum post!",
		Mentions:   []string{},
		MemoryIDs:  []string{},
		State:      "active",
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	if err := s.WriteForumPost(ctx, root); err != nil {
		t.Fatalf("WriteForumPost (root): %v", err)
	}

	// Read it back
	got, err := s.GetForumPost(ctx, "post-001")
	if err != nil {
		t.Fatalf("GetForumPost: %v", err)
	}
	if got.ID != root.ID {
		t.Errorf("ID: got %q, want %q", got.ID, root.ID)
	}
	if got.Content != root.Content {
		t.Errorf("Content: got %q, want %q", got.Content, root.Content)
	}
	if got.AuthorType != "human" {
		t.Errorf("AuthorType: got %q, want %q", got.AuthorType, "human")
	}
	if got.ThreadID != "post-001" {
		t.Errorf("ThreadID: got %q, want %q", got.ThreadID, "post-001")
	}

	// Create a reply
	reply := store.ForumPost{
		ID:         "post-002",
		ParentID:   "post-001",
		ThreadID:   "post-001",
		AuthorType: "agent",
		AuthorName: "Encoding Agent",
		AuthorKey:  "encoding",
		Content:    "Encoded your post. Extracted 3 concepts.",
		Mentions:   []string{},
		MemoryIDs:  []string{"mem-abc"},
		EventRef:   "memory_encoded",
		State:      "active",
		CreatedAt:  now.Add(time.Second),
		UpdatedAt:  now.Add(time.Second),
	}

	if err := s.WriteForumPost(ctx, reply); err != nil {
		t.Fatalf("WriteForumPost (reply): %v", err)
	}

	// List thread posts
	posts, err := s.ListForumPostsByThread(ctx, "post-001", 100)
	if err != nil {
		t.Fatalf("ListForumPostsByThread: %v", err)
	}
	if len(posts) != 2 {
		t.Fatalf("expected 2 posts, got %d", len(posts))
	}
	if posts[0].ID != "post-001" {
		t.Errorf("first post should be root, got %q", posts[0].ID)
	}
	if posts[1].ID != "post-002" {
		t.Errorf("second post should be reply, got %q", posts[1].ID)
	}
	if posts[1].ParentID != "post-001" {
		t.Errorf("reply ParentID: got %q, want %q", posts[1].ParentID, "post-001")
	}
	if posts[1].AuthorKey != "encoding" {
		t.Errorf("reply AuthorKey: got %q, want %q", posts[1].AuthorKey, "encoding")
	}
	if len(posts[1].MemoryIDs) != 1 || posts[1].MemoryIDs[0] != "mem-abc" {
		t.Errorf("reply MemoryIDs: got %v, want [mem-abc]", posts[1].MemoryIDs)
	}
}

func TestForumThreadListing(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	// Create two threads
	seedThreads := []store.ForumPost{
		{
			ID: "thread-a", ThreadID: "thread-a",
			AuthorType: "human", AuthorName: "Caleb",
			Content: "First thread", State: "active",
			CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "thread-b", ThreadID: "thread-b",
			AuthorType: "agent", AuthorName: "Dreaming Agent", AuthorKey: "dreaming",
			Content: "Dream cycle insights", State: "active",
			CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
		},
	}
	for i, thread := range seedThreads {
		if err := s.WriteForumPost(ctx, thread); err != nil {
			t.Fatalf("WriteForumPost thread %d: %v", i, err)
		}
	}

	// Add a reply to thread-a (makes it more recent)
	reply := store.ForumPost{
		ID: "reply-a1", ParentID: "thread-a", ThreadID: "thread-a",
		AuthorType: "agent", AuthorName: "Metacognition Agent", AuthorKey: "metacognition",
		Content: "Quality looks good.", State: "active",
		CreatedAt: now.Add(10 * time.Second), UpdatedAt: now.Add(10 * time.Second),
	}
	if err := s.WriteForumPost(ctx, reply); err != nil {
		t.Fatalf("WriteForumPost reply: %v", err)
	}

	// List threads — should be ordered by last activity (thread-a first due to reply)
	threads, err := s.ListForumThreads(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListForumThreads: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	if threads[0].RootPost.ID != "thread-a" {
		t.Errorf("first thread should be thread-a (has most recent reply), got %q", threads[0].RootPost.ID)
	}
	if threads[0].ReplyCount != 1 {
		t.Errorf("thread-a reply count: got %d, want 1", threads[0].ReplyCount)
	}
	if threads[1].RootPost.ID != "thread-b" {
		t.Errorf("second thread should be thread-b, got %q", threads[1].RootPost.ID)
	}
	if threads[1].ReplyCount != 0 {
		t.Errorf("thread-b reply count: got %d, want 0", threads[1].ReplyCount)
	}
}

func TestForumPostStateUpdate(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	post := store.ForumPost{
		ID: "post-state", ThreadID: "post-state",
		AuthorType: "human", AuthorName: "Caleb",
		Content: "To be internalized", State: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.WriteForumPost(ctx, post); err != nil {
		t.Fatalf("WriteForumPost: %v", err)
	}

	// Update to internalized
	if err := s.UpdateForumPostState(ctx, "post-state", "internalized"); err != nil {
		t.Fatalf("UpdateForumPostState: %v", err)
	}

	got, err := s.GetForumPost(ctx, "post-state")
	if err != nil {
		t.Fatalf("GetForumPost after update: %v", err)
	}
	if got.State != "internalized" {
		t.Errorf("State: got %q, want %q", got.State, "internalized")
	}

	// Not found case
	err = s.UpdateForumPostState(ctx, "nonexistent", "archived")
	if err == nil {
		t.Error("expected error for nonexistent post")
	}
}

func TestForumPostCount(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	count, err := s.CountForumPosts(ctx)
	if err != nil {
		t.Fatalf("CountForumPosts (empty): %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0, got %d", count)
	}

	post := store.ForumPost{
		ID: "count-1", ThreadID: "count-1",
		AuthorType: "human", AuthorName: "Caleb",
		Content: "Counting post", State: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.WriteForumPost(ctx, post); err != nil {
		t.Fatalf("WriteForumPost: %v", err)
	}

	count, err = s.CountForumPosts(ctx)
	if err != nil {
		t.Fatalf("CountForumPosts (1 post): %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1, got %d", count)
	}
}

func TestForumPostMentions(t *testing.T) {
	s := createTestStore(t)
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	post := store.ForumPost{
		ID: "mention-post", ThreadID: "mention-post",
		AuthorType: "human", AuthorName: "Caleb",
		Content:   "@retrieval what do you know about encoding?",
		Mentions:  []string{"retrieval"},
		State:     "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.WriteForumPost(ctx, post); err != nil {
		t.Fatalf("WriteForumPost: %v", err)
	}

	got, err := s.GetForumPost(ctx, "mention-post")
	if err != nil {
		t.Fatalf("GetForumPost: %v", err)
	}
	if len(got.Mentions) != 1 || got.Mentions[0] != "retrieval" {
		t.Errorf("Mentions: got %v, want [retrieval]", got.Mentions)
	}
}
