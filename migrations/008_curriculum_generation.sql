-- Phase B: Curriculum generation columns on experience_buffer
ALTER TABLE experience_buffer ADD COLUMN corrected_output TEXT DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN corrected_epr REAL DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN corrected_fr REAL DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN correction_source TEXT DEFAULT NULL;
ALTER TABLE experience_buffer ADD COLUMN corrected_at DATETIME DEFAULT NULL;

-- Curriculum run tracking
CREATE TABLE IF NOT EXISTS curriculum_runs (
    id TEXT PRIMARY KEY,
    started_at DATETIME NOT NULL,
    completed_at DATETIME,
    corrections_attempted INTEGER DEFAULT 0,
    corrections_passed INTEGER DEFAULT 0,
    corrections_failed INTEGER DEFAULT 0,
    entries_reclassified INTEGER DEFAULT 0,
    training_batch_path TEXT,
    status TEXT DEFAULT 'pending',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_curriculum_runs_status ON curriculum_runs(status);
