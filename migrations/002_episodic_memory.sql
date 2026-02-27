-- Migration 002: Episodic Memory Architecture
-- Adds episodes, multi-resolution storage, structured concepts, and emotional valence.

-- Episodic containers: temporal groupings of raw events
CREATE TABLE IF NOT EXISTS episodes (
    id TEXT PRIMARY KEY,
    title TEXT,
    start_time DATETIME NOT NULL,
    end_time DATETIME NOT NULL,
    duration_sec INTEGER,
    raw_memory_ids JSON,
    memory_ids JSON,
    summary TEXT,
    narrative TEXT,
    salience REAL DEFAULT 0.5,
    emotional_tone TEXT,
    outcome TEXT,
    state TEXT DEFAULT 'open',
    created_at DATETIME DEFAULT (datetime('now')),
    updated_at DATETIME DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_episode_start ON episodes(start_time);
CREATE INDEX IF NOT EXISTS idx_episode_end ON episodes(end_time);
CREATE INDEX IF NOT EXISTS idx_episode_state ON episodes(state);
CREATE INDEX IF NOT EXISTS idx_episode_salience ON episodes(salience);

-- Multi-resolution memory: gist + narrative + raw detail references
CREATE TABLE IF NOT EXISTS memory_resolutions (
    memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    gist TEXT,
    narrative TEXT,
    detail_raw_ids JSON,
    created_at DATETIME DEFAULT (datetime('now'))
);

-- Structured concepts: replaces flat keyword extraction
CREATE TABLE IF NOT EXISTS concept_sets (
    memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    topics JSON,
    entities JSON,
    actions JSON,
    causality JSON,
    significance TEXT,
    created_at DATETIME DEFAULT (datetime('now'))
);

-- Emotional/motivational valence
CREATE TABLE IF NOT EXISTS memory_attributes (
    memory_id TEXT PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
    significance TEXT,
    emotional_tone TEXT,
    outcome TEXT,
    causality_notes TEXT,
    created_at DATETIME DEFAULT (datetime('now'))
);

-- Add episode_id to memories (nullable for backward compatibility)
ALTER TABLE memories ADD COLUMN episode_id TEXT REFERENCES episodes(id);
CREATE INDEX IF NOT EXISTS idx_memory_episode ON memories(episode_id);
