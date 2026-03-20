package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/appsprout-dev/mnemonic/internal/agent/abstraction"
	"github.com/appsprout-dev/mnemonic/internal/agent/consolidation"
	"github.com/appsprout-dev/mnemonic/internal/agent/dreaming"
	"github.com/appsprout-dev/mnemonic/internal/agent/encoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/episoding"
	"github.com/appsprout-dev/mnemonic/internal/agent/metacognition"
	"github.com/appsprout-dev/mnemonic/internal/agent/retrieval"
	"github.com/appsprout-dev/mnemonic/internal/events"
	"github.com/appsprout-dev/mnemonic/internal/llm"
	"github.com/appsprout-dev/mnemonic/internal/store/sqlite"
	"time"
)

// Harness holds the shared state for all lifecycle phases.
type Harness struct {
	Store        *sqlite.SQLiteStore
	LLM          llm.Provider
	Bus          events.Bus
	Log          *slog.Logger
	Clock        *SimClock
	TmpDir       string
	DBPath       string

	Encoder      *encoding.EncodingAgent
	Episoder     *episoding.EpisodingAgent
	Consolidator *consolidation.ConsolidationAgent
	Dreamer      *dreaming.DreamingAgent
	Abstractor   *abstraction.AbstractionAgent
	Metacog      *metacognition.MetacognitionAgent
	Retriever    *retrieval.RetrievalAgent
}

// NewHarness creates an isolated test environment with a temp DB and all agents.
func NewHarness(provider llm.Provider, log *slog.Logger) (*Harness, error) {
	tmpDir, err := os.MkdirTemp("", "mnemonic-lifecycle-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	dbPath := filepath.Join(tmpDir, "lifecycle.db")
	s, err := sqlite.NewSQLiteStore(dbPath, 5000)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("creating store: %w", err)
	}

	bus := events.NewInMemoryBus(100)

	h := &Harness{
		Store:  s,
		LLM:    provider,
		Bus:    bus,
		Log:    log,
		Clock:  NewSimClock(),
		TmpDir: tmpDir,
		DBPath: dbPath,
	}

	// Create agents with configs matching benchmark-quality defaults.
	h.Encoder = encoding.NewEncodingAgentWithConfig(s, provider, log, encoding.DefaultConfig())
	h.Episoder = episoding.NewEpisodingAgent(s, provider, log, episoding.EpisodingConfig{
		EpisodeWindowSizeMin: 10,
		MinEventsPerEpisode:  2,
		PollingInterval:      10 * time.Second,
	})
	h.Consolidator = consolidation.NewConsolidationAgent(s, provider, consolidation.DefaultConfig(), log)
	h.Dreamer = dreaming.NewDreamingAgent(s, provider, dreaming.DreamingConfig{
		Interval:               time.Hour,
		BatchSize:              60,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}, log)
	h.Abstractor = abstraction.NewAbstractionAgent(s, provider, abstraction.AbstractionConfig{
		Interval:    time.Hour,
		MinStrength: 0.4,
		MaxLLMCalls: 5,
	}, log)
	h.Metacog = metacognition.NewMetacognitionAgent(s, provider, metacognition.MetacognitionConfig{
		Interval: time.Hour,
	}, log)
	h.Retriever = retrieval.NewRetrievalAgent(s, provider, retrieval.DefaultConfig(), log)

	return h, nil
}

// NewHarnessFromCheckpoint loads a DB snapshot and creates agents around it.
// The checkpoint file is copied to a temp dir so the original is preserved.
func NewHarnessFromCheckpoint(checkpointPath string, provider llm.Provider, log *slog.Logger) (*Harness, error) {
	tmpDir, err := os.MkdirTemp("", "mnemonic-lifecycle-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	dbPath := filepath.Join(tmpDir, "lifecycle.db")

	// Copy checkpoint to temp dir.
	src, err := os.ReadFile(checkpointPath)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("reading checkpoint %s: %w", checkpointPath, err)
	}
	if err := os.WriteFile(dbPath, src, 0o644); err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("writing checkpoint copy: %w", err)
	}

	s, err := sqlite.NewSQLiteStore(dbPath, 5000)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("opening checkpoint store: %w", err)
	}

	bus := events.NewInMemoryBus(100)

	h := &Harness{
		Store:  s,
		LLM:    provider,
		Bus:    bus,
		Log:    log,
		Clock:  NewSimClock(),
		TmpDir: tmpDir,
		DBPath: dbPath,
	}

	h.Encoder = encoding.NewEncodingAgentWithConfig(s, provider, log, encoding.DefaultConfig())
	h.Episoder = episoding.NewEpisodingAgent(s, provider, log, episoding.EpisodingConfig{
		EpisodeWindowSizeMin: 10,
		MinEventsPerEpisode:  2,
		PollingInterval:      10 * time.Second,
	})
	h.Consolidator = consolidation.NewConsolidationAgent(s, provider, consolidation.DefaultConfig(), log)
	h.Dreamer = dreaming.NewDreamingAgent(s, provider, dreaming.DreamingConfig{
		Interval:               time.Hour,
		BatchSize:              60,
		SalienceThreshold:      0.3,
		AssociationBoostFactor: 1.15,
		NoisePruneThreshold:    0.15,
	}, log)
	h.Abstractor = abstraction.NewAbstractionAgent(s, provider, abstraction.AbstractionConfig{
		Interval:    time.Hour,
		MinStrength: 0.4,
		MaxLLMCalls: 5,
	}, log)
	h.Metacog = metacognition.NewMetacognitionAgent(s, provider, metacognition.MetacognitionConfig{
		Interval: time.Hour,
	}, log)
	h.Retriever = retrieval.NewRetrievalAgent(s, provider, retrieval.DefaultConfig(), log)

	return h, nil
}

// Cleanup removes the temp directory and closes resources.
func (h *Harness) Cleanup() {
	_ = h.Bus.Close()
	_ = h.Store.Close()
	_ = os.RemoveAll(h.TmpDir)
}
