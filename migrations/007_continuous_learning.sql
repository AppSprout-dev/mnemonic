-- Verification metrics on memories table
ALTER TABLE memories ADD COLUMN encoding_epr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_fr REAL DEFAULT NULL;
ALTER TABLE memories ADD COLUMN encoding_flags TEXT DEFAULT NULL;

-- Recall-encoding linkage: tracks which memories were recalled and rated
CREATE TABLE IF NOT EXISTS recall_feedback (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    feedback TEXT NOT NULL,
    recall_session_id TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
CREATE INDEX IF NOT EXISTS idx_recall_feedback_memory ON recall_feedback(memory_id);
CREATE INDEX IF NOT EXISTS idx_recall_feedback_session ON recall_feedback(recall_session_id);

-- Experience buffer: curated training candidates with quality metrics
CREATE TABLE IF NOT EXISTS experience_buffer (
    id TEXT PRIMARY KEY,
    raw_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    encoding_epr REAL,
    encoding_fr REAL,
    encoding_flags TEXT,
    recall_score REAL,
    recall_count INTEGER DEFAULT 0,
    category TEXT DEFAULT 'ambiguous',
    used_in_training INTEGER DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (memory_id) REFERENCES memories(id)
);
CREATE INDEX IF NOT EXISTS idx_experience_buffer_category ON experience_buffer(category);
CREATE INDEX IF NOT EXISTS idx_experience_buffer_memory ON experience_buffer(memory_id);
