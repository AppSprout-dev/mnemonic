-- Raw observations before encoding
CREATE TABLE IF NOT EXISTS raw_memories (
    id TEXT PRIMARY KEY,
    timestamp DATETIME NOT NULL,
    source TEXT NOT NULL,
    type TEXT,
    content TEXT NOT NULL,
    metadata JSON,
    heuristic_score REAL DEFAULT 0.5,
    initial_salience REAL DEFAULT 0.5,
    processed BOOLEAN DEFAULT FALSE,
    created_at DATETIME DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_raw_timestamp ON raw_memories(timestamp);
CREATE INDEX IF NOT EXISTS idx_raw_processed ON raw_memories(processed);

-- Encoded memories
CREATE TABLE IF NOT EXISTS memories (
    id TEXT PRIMARY KEY,
    raw_id TEXT REFERENCES raw_memories(id),
    timestamp DATETIME NOT NULL,
    content TEXT NOT NULL,
    summary TEXT NOT NULL,
    concepts JSON,
    embedding BLOB,
    salience REAL DEFAULT 0.5,
    access_count INTEGER DEFAULT 0,
    last_accessed DATETIME,
    state TEXT DEFAULT 'active',
    gist_of JSON,
    created_at DATETIME DEFAULT (datetime('now')),
    updated_at DATETIME DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_memory_state ON memories(state);
CREATE INDEX IF NOT EXISTS idx_memory_salience ON memories(salience);
CREATE INDEX IF NOT EXISTS idx_memory_timestamp ON memories(timestamp);

-- FTS5 for full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS memories_fts USING fts5(
    summary,
    content,
    concepts,
    content='memories',
    content_rowid='rowid'
);

-- Triggers to keep FTS in sync
CREATE TRIGGER IF NOT EXISTS memories_ai AFTER INSERT ON memories BEGIN
    INSERT INTO memories_fts(rowid, summary, content, concepts)
    VALUES (new.rowid, new.summary, new.content, new.concepts);
END;

CREATE TRIGGER IF NOT EXISTS memories_ad AFTER DELETE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, summary, content, concepts)
    VALUES('delete', old.rowid, old.summary, old.content, old.concepts);
END;

CREATE TRIGGER IF NOT EXISTS memories_au AFTER UPDATE ON memories BEGIN
    INSERT INTO memories_fts(memories_fts, rowid, summary, content, concepts)
    VALUES('delete', old.rowid, old.summary, old.content, old.concepts);
    INSERT INTO memories_fts(rowid, summary, content, concepts)
    VALUES (new.rowid, new.summary, new.content, new.concepts);
END;

-- Association graph
CREATE TABLE IF NOT EXISTS associations (
    source_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    target_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    strength REAL DEFAULT 0.5,
    relation_type TEXT DEFAULT 'similar',
    created_at DATETIME DEFAULT (datetime('now')),
    last_activated DATETIME,
    activation_count INTEGER DEFAULT 0,
    PRIMARY KEY (source_id, target_id)
);
CREATE INDEX IF NOT EXISTS idx_assoc_source ON associations(source_id);
CREATE INDEX IF NOT EXISTS idx_assoc_target ON associations(target_id);
CREATE INDEX IF NOT EXISTS idx_assoc_strength ON associations(strength);

-- Meta-cognition observations
CREATE TABLE IF NOT EXISTS meta_observations (
    id TEXT PRIMARY KEY,
    observation_type TEXT NOT NULL,
    severity TEXT DEFAULT 'info',
    details JSON,
    created_at DATETIME DEFAULT (datetime('now'))
);

-- Retrieval feedback
CREATE TABLE IF NOT EXISTS retrieval_feedback (
    query_id TEXT PRIMARY KEY,
    query_text TEXT NOT NULL,
    retrieved_memory_ids JSON,
    feedback TEXT,
    notes TEXT,
    created_at DATETIME DEFAULT (datetime('now'))
);

-- Consolidation history
CREATE TABLE IF NOT EXISTS consolidation_history (
    id TEXT PRIMARY KEY,
    start_time DATETIME NOT NULL,
    end_time DATETIME NOT NULL,
    duration_ms INTEGER,
    memories_processed INTEGER,
    memories_decayed INTEGER,
    merged_clusters INTEGER,
    associations_pruned INTEGER,
    created_at DATETIME DEFAULT (datetime('now'))
);

-- Configure SQLite settings
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;
PRAGMA busy_timeout=5000;
