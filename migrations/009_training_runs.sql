-- Migration 009: Training runs table (Phase C — automated spoke training)
-- Tracks each spoke fine-tuning cycle for auditing and rollback.
-- Linked to experience_buffer entries via batch_id.

CREATE TABLE IF NOT EXISTS training_runs (
    id TEXT PRIMARY KEY,
    batch_id TEXT NOT NULL,           -- links to training batch manifest
    batch_path TEXT NOT NULL,         -- JSONL file path
    gold_count INTEGER DEFAULT 0,
    corrected_count INTEGER DEFAULT 0,
    total_examples INTEGER DEFAULT 0,
    status TEXT DEFAULT 'pending',    -- pending, training, evaluating, deploying, completed, failed
    checkpoint_path TEXT,             -- PyTorch checkpoint after training
    model_path TEXT,                  -- deployed GGUF path
    eval_epr REAL DEFAULT 0,         -- post-training evaluation: entity preservation rate
    eval_fr REAL DEFAULT 0,          -- post-training evaluation: fabrication rate
    eval_sc REAL DEFAULT 0,          -- post-training evaluation: schema compliance
    quality_passed BOOLEAN DEFAULT FALSE,
    error_message TEXT,
    started_at DATETIME NOT NULL,
    completed_at DATETIME
);

CREATE INDEX IF NOT EXISTS idx_training_runs_status ON training_runs(status);
CREATE INDEX IF NOT EXISTS idx_training_runs_started_at ON training_runs(started_at);
