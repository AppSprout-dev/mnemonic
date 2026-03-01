package git

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/appsprout/mnemonic/internal/watcher"
	"github.com/google/uuid"
)

// Config holds configuration for the git watcher.
type Config struct {
	WatchDirs       []string // top-level dirs to scan for repos
	PollIntervalSec int      // how often to poll each repo (default: 45)
	MaxRepoDepth    int      // how deep to scan for .git/ dirs (default: 3)
}

// GitWatcher monitors git repositories for changes by polling git status/diff.
// It emits events when working tree changes are detected, providing richer
// context than raw filesystem events (actual diffs vs "file X was modified").
// Consumes zero inotify watches.
type GitWatcher struct {
	cfg    Config
	log    *slog.Logger
	events chan watcher.Event
	done   chan struct{}
	mu     sync.RWMutex

	running bool
	repos   []string          // discovered repo root paths
	state   map[string]string // repo path → hash of last-seen status
}

// NewGitWatcher creates a new git repository watcher.
func NewGitWatcher(cfg Config, log *slog.Logger) (*GitWatcher, error) {
	if log == nil {
		return nil, fmt.Errorf("logger must not be nil")
	}

	// Check that git is available
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git not found in PATH: %w", err)
	}

	return &GitWatcher{
		cfg:    cfg,
		log:    log,
		events: make(chan watcher.Event, 100),
		done:   make(chan struct{}),
		state:  make(map[string]string),
	}, nil
}

func (gw *GitWatcher) Name() string {
	return "git"
}

func (gw *GitWatcher) Start(ctx context.Context) error {
	gw.mu.Lock()
	if gw.running {
		gw.mu.Unlock()
		return fmt.Errorf("git watcher is already running")
	}
	gw.running = true
	gw.mu.Unlock()

	maxDepth := gw.cfg.MaxRepoDepth
	if maxDepth == 0 {
		maxDepth = 3
	}

	// Discover git repositories
	gw.repos = discoverRepos(gw.cfg.WatchDirs, maxDepth)
	gw.log.Info("git watcher: discovered repositories",
		"count", len(gw.repos),
		"repos", gw.repos,
	)

	// Initialize state for each repo
	for _, repo := range gw.repos {
		status := gw.getRepoState(repo)
		gw.state[repo] = hashState(status)
	}

	// Start polling loop
	pollInterval := time.Duration(gw.cfg.PollIntervalSec) * time.Second
	if pollInterval == 0 {
		pollInterval = 45 * time.Second
	}
	go gw.pollLoop(ctx, pollInterval)

	gw.log.Info("git watcher started", "poll_interval", pollInterval, "repos", len(gw.repos))
	return nil
}

func (gw *GitWatcher) Stop() error {
	gw.mu.Lock()
	if !gw.running {
		gw.mu.Unlock()
		return fmt.Errorf("git watcher is not running")
	}
	gw.running = false
	gw.mu.Unlock()

	close(gw.done)
	close(gw.events)
	gw.log.Info("git watcher stopped")
	return nil
}

func (gw *GitWatcher) Events() <-chan watcher.Event {
	return gw.events
}

func (gw *GitWatcher) Health(ctx context.Context) error {
	gw.mu.RLock()
	defer gw.mu.RUnlock()
	if !gw.running {
		return fmt.Errorf("git watcher is not running")
	}
	return nil
}

// discoverRepos scans directories for git repositories up to maxDepth.
func discoverRepos(dirs []string, maxDepth int) []string {
	var repos []string
	seen := make(map[string]bool)

	for _, dir := range dirs {
		rootDepth := strings.Count(filepath.Clean(dir), string(filepath.Separator))

		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !d.IsDir() {
				return nil
			}

			// Check depth limit
			pathDepth := strings.Count(filepath.Clean(path), string(filepath.Separator))
			if pathDepth-rootDepth > maxDepth {
				return filepath.SkipDir
			}

			// Skip common non-repo directories
			name := d.Name()
			if name == "node_modules" || name == ".git" || name == "__pycache__" || name == "vendor" {
				return filepath.SkipDir
			}

			// Check if this directory is a git repo
			gitDir := filepath.Join(path, ".git")
			if info, err := statFile(gitDir); err == nil && info.IsDir() {
				cleanPath := filepath.Clean(path)
				if !seen[cleanPath] {
					seen[cleanPath] = true
					repos = append(repos, cleanPath)
				}
				return filepath.SkipDir // don't recurse into repos (submodules are separate)
			}

			return nil
		})
	}

	return repos
}

// statFile wraps os.Stat for testability.
var statFile = func(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

// pollLoop periodically checks each repo for changes.
func (gw *GitWatcher) pollLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-gw.done:
			return
		case <-ticker.C:
			gw.checkAllRepos()
		}
	}
}

// checkAllRepos checks each discovered repo for working tree changes.
func (gw *GitWatcher) checkAllRepos() {
	for _, repo := range gw.repos {
		state := gw.getRepoState(repo)
		newHash := hashState(state)

		oldHash, known := gw.state[repo]
		if known && newHash == oldHash {
			continue // no changes
		}
		gw.state[repo] = newHash

		if !known {
			continue // first time seeing this repo, don't emit
		}

		if state == "" {
			continue // empty state means no changes
		}

		// Build a meaningful event
		diff := gw.getRepoDiff(repo)
		content := state
		if diff != "" {
			content = state + "\n---\n" + diff
		}

		// Truncate to avoid huge diffs
		if len(content) > 5000 {
			content = content[:5000] + "\n... [truncated]"
		}

		wevent := watcher.Event{
			ID:        uuid.New().String(),
			Source:    "git",
			Type:      "repo_changed",
			Path:      repo,
			Content:   content,
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"repo": repo,
			},
		}

		select {
		case gw.events <- wevent:
			gw.log.Debug("git watcher: detected changes", "repo", repo)
		case <-gw.done:
			return
		}
	}
}

// getRepoState returns the combined git status output for a repo.
func (gw *GitWatcher) getRepoState(repo string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repo, "status", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		gw.log.Debug("git status failed", "repo", repo, "err", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

// getRepoDiff returns a brief diff summary for a repo.
func (gw *GitWatcher) getRepoDiff(repo string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "-C", repo, "diff", "--stat")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hashState produces a short hash of the state string for change detection.
func hashState(state string) string {
	h := sha256.Sum256([]byte(state))
	return fmt.Sprintf("%x", h[:8])
}
