package mcp

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/config"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/store"
)

// SessionManager manages MCPServer instances for HTTP transport sessions.
// Each unique session ID gets its own MCPServer with isolated per-session state
// (session memories, recall cache, context suggestions). All sessions share
// the daemon's store, LLM, retrieval agent, and event bus.
type SessionManager struct {
	mu       sync.Mutex
	sessions map[string]*httpSession

	// Shared dependencies (from daemon)
	store           store.Store
	retriever       *retrieval.RetrievalAgent
	bus             events.Bus
	log             *slog.Logger
	version         string
	coachingFile    string
	excludePatterns []string
	maxContentBytes int
	resolver        ProjectResolver
	daemonURL          string
	memDefaults        MemoryDefaults
	trainingTriggerFn  func(ctx context.Context) (map[string]any, error)

	idleTimeout time.Duration // how long before an idle session is expired
	stopCh      chan struct{} // signals the reaper goroutine to stop
}

type httpSession struct {
	server     *MCPServer
	lastActive time.Time
}

// SessionManagerConfig holds configuration for the session manager.
type SessionManagerConfig struct {
	Store           store.Store
	Retriever       *retrieval.RetrievalAgent
	Bus             events.Bus
	Log             *slog.Logger
	Version         string
	CoachingFile    string
	ExcludePatterns []string
	MaxContentBytes int
	Resolver        *config.ProjectResolver
	DaemonURL          string
	MemDefaults        MemoryDefaults
	TrainingTriggerFn  func(ctx context.Context) (map[string]any, error)
	IdleTimeout        time.Duration // default: 30 minutes
}

// NewSessionManager creates a session manager for HTTP MCP transport.
func NewSessionManager(cfg SessionManagerConfig) *SessionManager {
	timeout := cfg.IdleTimeout
	if timeout == 0 {
		timeout = 30 * time.Minute
	}

	sm := &SessionManager{
		sessions:        make(map[string]*httpSession),
		store:           cfg.Store,
		retriever:       cfg.Retriever,
		bus:             cfg.Bus,
		log:             cfg.Log,
		version:         cfg.Version,
		coachingFile:    cfg.CoachingFile,
		excludePatterns: cfg.ExcludePatterns,
		maxContentBytes: cfg.MaxContentBytes,
		resolver:        cfg.Resolver,
		daemonURL:          cfg.DaemonURL,
		memDefaults:        cfg.MemDefaults,
		trainingTriggerFn:  cfg.TrainingTriggerFn,
		idleTimeout:        timeout,
		stopCh:          make(chan struct{}),
	}

	// Start background reaper for idle sessions
	go sm.reapLoop()

	return sm
}

// GetOrCreate returns the MCPServer for a session and its session key.
// If clientSessionID is empty (first request), a new session is created.
// If clientSessionID matches an existing session, that session is returned.
// The returned sessionKey should be sent back to the client in the Mcp-Session-Id header.
func (sm *SessionManager) GetOrCreate(clientSessionID string) (*MCPServer, string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if clientSessionID != "" {
		if s, ok := sm.sessions[clientSessionID]; ok {
			s.lastActive = time.Now()
			return s.server, clientSessionID
		}
	}

	// Create new MCPServer for this session
	srv := NewMCPServer(
		sm.store, sm.retriever, sm.bus, sm.log,
		sm.version, sm.coachingFile, sm.excludePatterns,
		sm.maxContentBytes, sm.resolver, sm.daemonURL,
		sm.memDefaults,
	)

	if sm.trainingTriggerFn != nil {
		srv.SetTrainingTrigger(sm.trainingTriggerFn)
	}

	// Use the MCPServer's generated session ID as the key
	key := srv.SessionID()
	sm.sessions[key] = &httpSession{
		server:     srv,
		lastActive: time.Now(),
	}

	sm.log.Info("HTTP MCP session created", "session_id", key)
	return srv, key
}

// EndSession explicitly ends a session and cleans up.
func (sm *SessionManager) EndSession(ctx context.Context, sessionID string) {
	sm.mu.Lock()
	s, ok := sm.sessions[sessionID]
	if ok {
		delete(sm.sessions, sessionID)
	}
	sm.mu.Unlock()

	if ok {
		s.server.OnSessionEnd(ctx)
		sm.log.Info("HTTP MCP session ended", "client_session", sessionID)
	}
}

// ActiveSessions returns the number of active sessions.
func (sm *SessionManager) ActiveSessions() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	return len(sm.sessions)
}

// Stop shuts down the session manager, ending all active sessions.
func (sm *SessionManager) Stop(ctx context.Context) {
	close(sm.stopCh)

	sm.mu.Lock()
	sessions := make(map[string]*httpSession, len(sm.sessions))
	for k, v := range sm.sessions {
		sessions[k] = v
	}
	sm.sessions = make(map[string]*httpSession)
	sm.mu.Unlock()

	for _, s := range sessions {
		s.server.OnSessionEnd(ctx)
	}
	sm.log.Info("session manager stopped", "sessions_ended", len(sessions))
}

// reapLoop periodically checks for and expires idle sessions.
func (sm *SessionManager) reapLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sm.stopCh:
			return
		case <-ticker.C:
			sm.reapIdle()
		}
	}
}

func (sm *SessionManager) reapIdle() {
	sm.mu.Lock()
	var expired []string
	now := time.Now()
	for id, s := range sm.sessions {
		if now.Sub(s.lastActive) > sm.idleTimeout {
			expired = append(expired, id)
		}
	}
	// Remove from map while holding lock
	expiredSessions := make([]*httpSession, 0, len(expired))
	for _, id := range expired {
		expiredSessions = append(expiredSessions, sm.sessions[id])
		delete(sm.sessions, id)
	}
	sm.mu.Unlock()

	// Clean up outside the lock
	for _, s := range expiredSessions {
		s.server.OnSessionEnd(context.Background())
	}
	if len(expired) > 0 {
		sm.log.Info("reaped idle HTTP MCP sessions", "count", len(expired))
	}
}
