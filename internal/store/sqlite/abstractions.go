package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	store "github.com/appsprout/mnemonic/internal/store"
)

// abstractionColumns is the standard column list for abstraction queries.
const abstractionColumns = `id, level, title, description, parent_id, source_pattern_ids, source_memory_ids, confidence, concepts, embedding, access_count, state, created_at, updated_at`

// WriteAbstraction inserts a new abstraction.
func (s *SQLiteStore) WriteAbstraction(ctx context.Context, a store.Abstraction) error {
	sourcePatternIDs, _ := encodeStringSlice(a.SourcePatternIDs)
	sourceMemoryIDs, _ := encodeStringSlice(a.SourceMemoryIDs)
	concepts, _ := encodeStringSlice(a.Concepts)
	var embeddingBlob []byte
	if len(a.Embedding) > 0 {
		embeddingBlob = encodeEmbedding(a.Embedding)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO abstractions (`+abstractionColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID,
		a.Level,
		a.Title,
		a.Description,
		nullableString(a.ParentID),
		sourcePatternIDs,
		sourceMemoryIDs,
		a.Confidence,
		concepts,
		embeddingBlob,
		a.AccessCount,
		a.State,
		a.CreatedAt.Format(time.RFC3339),
		a.UpdatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("failed to write abstraction: %w", err)
	}
	return nil
}

// GetAbstraction retrieves an abstraction by ID.
func (s *SQLiteStore) GetAbstraction(ctx context.Context, id string) (store.Abstraction, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+abstractionColumns+` FROM abstractions WHERE id = ?`, id)
	return scanAbstraction(row)
}

// UpdateAbstraction updates an existing abstraction.
func (s *SQLiteStore) UpdateAbstraction(ctx context.Context, a store.Abstraction) error {
	sourcePatternIDs, _ := encodeStringSlice(a.SourcePatternIDs)
	sourceMemoryIDs, _ := encodeStringSlice(a.SourceMemoryIDs)
	concepts, _ := encodeStringSlice(a.Concepts)
	var embeddingBlob []byte
	if len(a.Embedding) > 0 {
		embeddingBlob = encodeEmbedding(a.Embedding)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE abstractions
		SET level = ?, title = ?, description = ?, parent_id = ?,
		    source_pattern_ids = ?, source_memory_ids = ?, confidence = ?,
		    concepts = ?, embedding = ?, access_count = ?, state = ?, updated_at = ?
		WHERE id = ?`,
		a.Level,
		a.Title,
		a.Description,
		nullableString(a.ParentID),
		sourcePatternIDs,
		sourceMemoryIDs,
		a.Confidence,
		concepts,
		embeddingBlob,
		a.AccessCount,
		a.State,
		a.UpdatedAt.Format(time.RFC3339),
		a.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update abstraction: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("abstraction with id %s not found", a.ID)
	}
	return nil
}

// ListAbstractions lists abstractions, optionally filtered by level.
// Pass level=0 to list all levels.
func (s *SQLiteStore) ListAbstractions(ctx context.Context, level int, limit int) ([]store.Abstraction, error) {
	var query string
	var args []interface{}

	if level == 0 {
		query = `SELECT ` + abstractionColumns + ` FROM abstractions WHERE state = 'active' ORDER BY confidence DESC LIMIT ?`
		args = []interface{}{limit}
	} else {
		query = `SELECT ` + abstractionColumns + ` FROM abstractions WHERE state = 'active' AND level = ? ORDER BY confidence DESC LIMIT ?`
		args = []interface{}{level, limit}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list abstractions: %w", err)
	}
	return scanAbstractionRows(rows)
}

// scanAbstraction scans a single abstraction row.
func scanAbstraction(row *sql.Row) (store.Abstraction, error) {
	var a store.Abstraction
	var parentID, sourcePatternIDsStr, sourceMemoryIDsStr, conceptsStr sql.NullString
	var embeddingBlob []byte

	err := row.Scan(
		&a.ID,
		&a.Level,
		&a.Title,
		&a.Description,
		&parentID,
		&sourcePatternIDsStr,
		&sourceMemoryIDsStr,
		&a.Confidence,
		&conceptsStr,
		&embeddingBlob,
		&a.AccessCount,
		&a.State,
		&a.CreatedAt,
		&a.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return a, fmt.Errorf("abstraction not found")
		}
		return a, fmt.Errorf("failed to scan abstraction: %w", err)
	}

	a.ParentID = parentID.String
	a.SourcePatternIDs, _ = decodeStringSlice(sourcePatternIDsStr.String)
	a.SourceMemoryIDs, _ = decodeStringSlice(sourceMemoryIDsStr.String)
	a.Concepts, _ = decodeStringSlice(conceptsStr.String)
	if len(embeddingBlob) > 0 {
		a.Embedding = decodeEmbedding(embeddingBlob)
	}

	return a, nil
}

// scanAbstractionRows scans multiple abstraction rows.
func scanAbstractionRows(rows *sql.Rows) ([]store.Abstraction, error) {
	defer rows.Close()
	var abstractions []store.Abstraction

	for rows.Next() {
		var a store.Abstraction
		var parentID, sourcePatternIDsStr, sourceMemoryIDsStr, conceptsStr sql.NullString
		var embeddingBlob []byte

		err := rows.Scan(
			&a.ID,
			&a.Level,
			&a.Title,
			&a.Description,
			&parentID,
			&sourcePatternIDsStr,
			&sourceMemoryIDsStr,
			&a.Confidence,
			&conceptsStr,
			&embeddingBlob,
			&a.AccessCount,
			&a.State,
			&a.CreatedAt,
			&a.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan abstraction row: %w", err)
		}

		a.ParentID = parentID.String
		a.SourcePatternIDs, _ = decodeStringSlice(sourcePatternIDsStr.String)
		a.SourceMemoryIDs, _ = decodeStringSlice(sourceMemoryIDsStr.String)
		a.Concepts, _ = decodeStringSlice(conceptsStr.String)
		if len(embeddingBlob) > 0 {
			a.Embedding = decodeEmbedding(embeddingBlob)
		}

		abstractions = append(abstractions, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading abstraction rows: %w", err)
	}

	return abstractions, nil
}
