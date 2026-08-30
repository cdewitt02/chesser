package search

import (
	"context"
	"fmt"
)

// EmbeddingClient is the embedding capability HybridSearcher needs.
// llm.Embedder satisfies it.
type EmbeddingClient interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type GameSearcher interface {
	FindSimilarGamesWithFilters(ctx context.Context, queryEmbedding []float32, filters *GameFilters, limit int) ([]*SimilarGameResult, error)
	CountGamesMatchingFilters(ctx context.Context, filters *GameFilters) (int, error)
}

type SimilarGameResult struct {
	GameUUID    string
	SummaryText string
	Distance    float64
	Game        interface{} // Will hold *db.GameRecord
}

// hybrid search request
type SearchQuery struct {
	Query string
	ExplicitFilters *GameFilters
	TopK int
	MaxDistance float64  //similarity
}

// results of a hybrid search
type SearchResult struct {
	// ranked by similarity
	Games []*SimilarGameResult
	AppliedFilters *GameFilters
	SemanticQuery string
	ExtractedFilters []string
	MatchingGamesCount int
}

type HybridSearcher struct {
	parser    *QueryParser
	embedder  EmbeddingClient
	searcher  GameSearcher
	defaultK  int
}

func NewHybridSearcher(embedder EmbeddingClient, searcher GameSearcher) *HybridSearcher {
	return &HybridSearcher{
		parser:   NewQueryParser(),
		embedder: embedder,
		searcher: searcher,
		defaultK: 5,
	}
}


func (h *HybridSearcher) Search(ctx context.Context, query SearchQuery, username string) (*SearchResult, error) {
	// Set defaults
	topK := query.TopK
	if topK <= 0 {
		topK = h.defaultK
	}

	parseResult := h.parser.Parse(query.Query, username)

	mergedFilters := h.mergeFilters(parseResult.Filters, query.ExplicitFilters)

	matchCount, err := h.searcher.CountGamesMatchingFilters(ctx, mergedFilters)
	if err != nil {
		return nil, fmt.Errorf("failed to count matching games: %w", err)
	}

	semanticQuery := parseResult.SemanticQuery
	if semanticQuery == "" {
		semanticQuery = query.Query
	}

	embeddings, err := h.embedder.Embed(ctx, []string{semanticQuery})
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}
	if len(embeddings) != 1 {
		return nil, fmt.Errorf("expected 1 query embedding, got %d", len(embeddings))
	}
	embedding := embeddings[0]

	games, err := h.searcher.FindSimilarGamesWithFilters(ctx, embedding, mergedFilters, topK)
	if err != nil {
		return nil, fmt.Errorf("failed to search games: %w", err)
	}

	if query.MaxDistance > 0 {
		var filtered []*SimilarGameResult
		for _, g := range games {
			if g.Distance <= query.MaxDistance {
				filtered = append(filtered, g)
			}
		}
		games = filtered
	}

	return &SearchResult{
		Games:              games,
		AppliedFilters:     mergedFilters,
		SemanticQuery:      semanticQuery,
		ExtractedFilters:   parseResult.ExtractedFilters,
		MatchingGamesCount: matchCount,
	}, nil
}

func (h *HybridSearcher) mergeFilters(parsed, explicit *GameFilters) *GameFilters {
	if explicit == nil {
		return parsed
	}
	if parsed == nil {
		return explicit
	}

	merged := parsed.Clone()

	if explicit.Result != nil {
		merged.Result = explicit.Result
	}
	if explicit.UserColor != nil {
		merged.UserColor = explicit.UserColor
	}
	if explicit.TimeClass != nil {
		merged.TimeClass = explicit.TimeClass
	}
	if explicit.WeakPhase != nil {
		merged.WeakPhase = explicit.WeakPhase
	}
	if explicit.ECOPrefix != nil {
		merged.ECOPrefix = explicit.ECOPrefix
	}
	if explicit.OpeningName != nil {
		merged.OpeningName = explicit.OpeningName
	}
	if explicit.MinBlunders != nil {
		merged.MinBlunders = explicit.MinBlunders
	}
	if explicit.MaxBlunders != nil {
		merged.MaxBlunders = explicit.MaxBlunders
	}
	if explicit.MinMistakes != nil {
		merged.MinMistakes = explicit.MinMistakes
	}
	if explicit.MinRating != nil {
		merged.MinRating = explicit.MinRating
	}
	if explicit.MaxRating != nil {
		merged.MaxRating = explicit.MaxRating
	}
	if explicit.DateFrom != nil {
		merged.DateFrom = explicit.DateFrom
	}
	if explicit.DateTo != nil {
		merged.DateTo = explicit.DateTo
	}
	// Username from explicit always wins
	if explicit.Username != "" {
		merged.Username = explicit.Username
	}

	return merged
}

func (h *HybridSearcher) GetParser() *QueryParser {
	return h.parser
}
