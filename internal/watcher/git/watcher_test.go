package git

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

func TestDiscoverRepos(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake git repo
	repoDir := filepath.Join(tmpDir, "myproject")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a non-repo directory
	if err := os.MkdirAll(filepath.Join(tmpDir, "documents", "notes"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a nested repo (depth 2)
	nestedRepo := filepath.Join(tmpDir, "work", "subproject")
	if err := os.MkdirAll(filepath.Join(nestedRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	repos := discoverRepos([]string{tmpDir}, 3)

	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d: %v", len(repos), repos)
	}

	found := make(map[string]bool)
	for _, r := range repos {
		found[r] = true
	}
	if !found[filepath.Clean(repoDir)] {
		t.Errorf("expected to find repo %s", repoDir)
	}
	if !found[filepath.Clean(nestedRepo)] {
		t.Errorf("expected to find repo %s", nestedRepo)
	}
}

func TestDiscoverRepos_DepthLimit(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a repo at depth 5 — should not be found with maxDepth=2
	deepRepo := filepath.Join(tmpDir, "a", "b", "c", "d", "e")
	if err := os.MkdirAll(filepath.Join(deepRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	repos := discoverRepos([]string{tmpDir}, 2)
	if len(repos) != 0 {
		t.Errorf("expected 0 repos with depth limit 2, got %d: %v", len(repos), repos)
	}
}

func TestDiscoverRepos_SkipsNodeModules(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a repo inside node_modules — should be skipped
	nmRepo := filepath.Join(tmpDir, "node_modules", "some-pkg")
	if err := os.MkdirAll(filepath.Join(nmRepo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	repos := discoverRepos([]string{tmpDir}, 5)
	if len(repos) != 0 {
		t.Errorf("expected 0 repos (node_modules skipped), got %d: %v", len(repos), repos)
	}
}

func TestHashState(t *testing.T) {
	h1 := hashState("some state")
	h2 := hashState("some state")
	h3 := hashState("different state")

	if h1 != h2 {
		t.Error("same input should produce same hash")
	}
	if h1 == h3 {
		t.Error("different input should produce different hash")
	}
}

func TestGitWatcher_RealRepo(t *testing.T) {
	// Skip if git is not available
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found, skipping integration test")
	}

	tmpDir := t.TempDir()

	// Create a real git repo
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = tmpDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("command %v failed: %v\n%s", args, err, out)
		}
	}

	run("git", "init")
	run("git", "commit", "--allow-empty", "-m", "initial")

	gw, err := NewGitWatcher(Config{
		WatchDirs:       []string{tmpDir},
		PollIntervalSec: 1,
		MaxRepoDepth:    1,
	}, testLogger())
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := gw.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gw.Stop() }()

	if len(gw.repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(gw.repos))
	}

	// Create a new file in the repo (this changes git status)
	if err := os.WriteFile(filepath.Join(tmpDir, "newfile.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the polling to detect the change
	var received bool
	timeout := time.After(5 * time.Second)
	for !received {
		select {
		case evt, ok := <-gw.Events():
			if !ok {
				t.Fatal("events channel closed unexpectedly")
			}
			if evt.Source == "git" && evt.Type == "repo_changed" {
				received = true
				if evt.Path != filepath.Clean(tmpDir) {
					t.Errorf("expected path %s, got %s", tmpDir, evt.Path)
				}
				if evt.Content == "" {
					t.Error("expected non-empty content")
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for git change event")
		}
	}
}
