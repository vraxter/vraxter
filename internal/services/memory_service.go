package services

import (
	"context"
	"fmt"
	"sort"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
)

// SemanticMemory represents a chunk of retrieved context with its score
type SemanticMemory struct {
	Content string
	Score   float32
}

type MemoryService struct {
	repo   *db.MemoryRepository
	router *llm.Router
}

func NewMemoryService(repo *db.MemoryRepository, router *llm.Router) *MemoryService {
	return &MemoryService{
		repo:   repo,
		router: router,
	}
}

// StoreMemory generates an embedding for a piece of text and saves it using the repository
func (s *MemoryService) StoreMemory(ctx context.Context, sessionID, text string) error {
	config, provider, err := s.router.GetEmbeddingProvider()
	if err != nil {
		// Silent skip if no embedding model is configured
		return nil
	}

	embeddings, err := provider.Embed(ctx, config.Model, []string{text})
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	if len(embeddings) == 0 {
		return fmt.Errorf("provider returned no embeddings")
	}

	return s.repo.SaveEmbedding(sessionID, text, embeddings[0])
}

// SearchMemories performs a semantic search using the repository's search logic
func (s *MemoryService) SearchMemories(ctx context.Context, queryText string, limit int) ([]SemanticMemory, error) {
	config, provider, err := s.router.GetEmbeddingProvider()
	if err != nil {
		return nil, nil // Memory disabled
	}

	targetVecs, err := provider.Embed(ctx, config.Model, []string{queryText})
	if err != nil || len(targetVecs) == 0 {
		return nil, err
	}

	// Fetch both current session and global memories
	results, err := s.repo.SearchGlobalSimilar(targetVecs[0], limit)
	if err != nil {
		return nil, err
	}

	var memories []SemanticMemory
	for _, res := range results {
		memories = append(memories, SemanticMemory{
			Content: res.Node.TextContent,
			Score:   res.Score,
		})
	}

	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Score > memories[j].Score
	})

	return memories, nil
}
