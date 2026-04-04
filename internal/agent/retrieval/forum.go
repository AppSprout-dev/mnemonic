package retrieval

import (
	"context"

	"github.com/appsprout-dev/mnemonic/internal/store"
)

// ForumQuery runs a simple retrieval query for the forum @mention system.
// Returns ranked results without synthesis.
func (ra *RetrievalAgent) ForumQuery(ctx context.Context, query string, limit int) ([]store.RetrievalResult, error) {
	resp, err := ra.Query(ctx, QueryRequest{
		Query:      query,
		MaxResults: limit,
	})
	if err != nil {
		return nil, err
	}
	return resp.Memories, nil
}
