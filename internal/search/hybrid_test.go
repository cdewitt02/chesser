package search_test

import (
	"context"
	"errors"
	"testing"

	"github.com/chesser/internal/llm/llmtest"
	"github.com/chesser/internal/search"
)

// stubSearcher stands in for the database.
type stubSearcher struct {
	count       int
	results     []*search.SimilarGameResult
	gotEmbed    []float32
	gotLimit    int
	gotFilters  *search.GameFilters
	searchError error
}

func (s *stubSearcher) FindSimilarGamesWithFilters(
	ctx context.Context, queryEmbedding []float32, filters *search.GameFilters, limit int,
) ([]*search.SimilarGameResult, error) {
	s.gotEmbed, s.gotFilters, s.gotLimit = queryEmbedding, filters, limit
	return s.results, s.searchError
}

func (s *stubSearcher) CountGamesMatchingFilters(ctx context.Context, filters *search.GameFilters) (int, error) {
	return s.count, nil
}

// Search was previously untestable: it depended on a concrete client that
// required a live Ollama.
func TestSearchEmbedsTheSemanticRemainder(t *testing.T) {
	embedder := llmtest.NewFakeEmbedder(768)
	searcher := &stubSearcher{
		count: 12,
		results: []*search.SimilarGameResult{
			{GameUUID: "a", SummaryText: "won with the Sicilian", Distance: 0.1},
			{GameUUID: "b", SummaryText: "lost on time", Distance: 0.4},
		},
	}

	h := search.NewHybridSearcher(embedder, searcher)
	got, err := h.Search(context.Background(),
		search.SearchQuery{Query: "my losses as black in blitz", TopK: 7}, "magnus")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if len(embedder.Calls) != 1 || len(embedder.Calls[0]) != 1 {
		t.Fatalf("embedder calls = %v, want exactly one batch of one text", embedder.Calls)
	}
	if len(searcher.gotEmbed) != 768 {
		t.Errorf("query vector width = %d, want 768", len(searcher.gotEmbed))
	}
	if searcher.gotLimit != 7 {
		t.Errorf("limit = %d, want the requested TopK of 7", searcher.gotLimit)
	}
	if len(got.Games) != 2 {
		t.Fatalf("got %d games, want 2", len(got.Games))
	}
	if got.MatchingGamesCount != 12 {
		t.Errorf("MatchingGamesCount = %d, want 12", got.MatchingGamesCount)
	}
}

func TestSearchAppliesMaxDistance(t *testing.T) {
	searcher := &stubSearcher{
		count: 2,
		results: []*search.SimilarGameResult{
			{GameUUID: "near", Distance: 0.1},
			{GameUUID: "far", Distance: 0.9},
		},
	}
	h := search.NewHybridSearcher(llmtest.NewFakeEmbedder(768), searcher)

	got, err := h.Search(context.Background(),
		search.SearchQuery{Query: "endgames", MaxDistance: 0.5}, "magnus")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Games) != 1 || got.Games[0].GameUUID != "near" {
		t.Fatalf("got %+v, want only the game inside MaxDistance", got.Games)
	}
}

// An embedding failure must surface, not produce a search against a zero
// vector.
func TestSearchPropagatesEmbeddingFailure(t *testing.T) {
	wantErr := errors.New("embedder is down")
	embedder := llmtest.NewFakeEmbedder(768)
	embedder.Err = wantErr

	h := search.NewHybridSearcher(embedder, &stubSearcher{count: 3})
	_, err := h.Search(context.Background(), search.SearchQuery{Query: "anything"}, "magnus")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want it to wrap %v", err, wantErr)
	}
}
