package chat

import (
	"context"
	"fmt"

	"github.com/chesser/internal/db"
	"github.com/chesser/internal/embeddings"
	"github.com/chesser/internal/search"
)

type Service struct {
	db             *db.DB
	ollama         *embeddings.Client
	chatModel      string
	username       string
	numSimilar     int
	promptBuilder  *PromptBuilder
	hybridSearcher *search.HybridSearcher
	queryRouter    *QueryRouter
	detailLimit    int
	history        []embeddings.ChatMessage
	maxHistoryPairs int
}

type Config struct {
	ChatModel       string
	Username        string
	NumSimilar      int
	DetailLimit     int
	MaxHistoryPairs int
}

func NewService(database *db.DB, ollamaClient *embeddings.Client, cfg Config) *Service {
	numSimilar := cfg.NumSimilar
	if numSimilar <= 0 {
		numSimilar = 5
	}

	detailLimit := cfg.DetailLimit
	if detailLimit <= 0 {
		detailLimit = 5
	}

	maxHistoryPairs := cfg.MaxHistoryPairs
	if maxHistoryPairs <= 0 {
		maxHistoryPairs = 4
	}

	dbAdapter := &dbSearchAdapter{db: database}
	hybridSearcher := search.NewHybridSearcher(ollamaClient, dbAdapter)
	promptBuilder := NewPromptBuilder(cfg.Username)
	queryRouter := NewQueryRouter(database, hybridSearcher, promptBuilder, cfg.Username, numSimilar)

	return &Service{
		db:              database,
		ollama:          ollamaClient,
		chatModel:       cfg.ChatModel,
		username:        cfg.Username,
		numSimilar:      numSimilar,
		promptBuilder:   promptBuilder,
		hybridSearcher:  hybridSearcher,
		queryRouter:     queryRouter,
		detailLimit:     detailLimit,
		history:         make([]embeddings.ChatMessage, 0),
		maxHistoryPairs: maxHistoryPairs,
	}
}

// processes a question using query classification and routing.
// Different query types get different context:
// - Aggregate/Comparative: Focus on pre-computed stats
// - SpecificGames: Focus on RAG search results
// - Recommendation: Combine stats + relevant examples
func (s *Service) Ask(ctx context.Context, question string) (string, error) {
	qctx, err := s.queryRouter.Route(ctx, question)
	if err != nil {
		return "", fmt.Errorf("failed to route query: %w", err)
	}

	hasStats := qctx.PlayerStats != nil && qctx.PlayerStats.TotalGames > 0
	hasGames := len(qctx.Games) > 0

	if !hasStats && !hasGames {
		return "I don't have any game data to analyze. Make sure you've imported and analyzed some games first.", nil
	}

	systemPrompt := s.queryRouter.BuildPrompt(qctx, s.detailLimit)

	if len(qctx.Filters) > 0 {
		systemPrompt += fmt.Sprintf("\n\nNote: The search was filtered by: %v", qctx.Filters)
	}

	messages := []embeddings.ChatMessage{
		{Role: "system", Content: systemPrompt},
	}
	messages = append(messages, s.history...)
	messages = append(messages, embeddings.ChatMessage{Role: "user", Content: question})

	response, err := s.ollama.Chat(s.chatModel, messages)
	if err != nil {
		return "", fmt.Errorf("failed to generate response: %w", err)
	}

	s.history = append(s.history,
		embeddings.ChatMessage{Role: "user", Content: question},
		embeddings.ChatMessage{Role: "assistant", Content: response},
	)
	s.truncateHistory()

	fmt.Printf("=== QUERY TYPE: %s ===\n", qctx.QueryType)
	fmt.Println("=== SYSTEM PROMPT ===")
	fmt.Println(systemPrompt)
	fmt.Println("=== END PROMPT ===")

	return response, nil
}

func (s *Service) truncateHistory() {
	maxMessages := s.maxHistoryPairs * 2
	if len(s.history) > maxMessages {
		s.history = s.history[len(s.history)-maxMessages:]
	}
}

func (s *Service) ClearHistory() {
	s.history = s.history[:0]
}

type AskResponse struct {
	Response         string
	ExtractedFilters []string
	GamesFound       int
}

func (s *Service) AskWithDetails(ctx context.Context, question string) (*AskResponse, error) {
	// Perform hybrid search
	searchResult, err := s.hybridSearcher.Search(
		ctx,
		search.SearchQuery{
			Query: question,
			TopK:  s.numSimilar,
		},
		s.username,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to search games: %w", err)
	}

	if len(searchResult.Games) == 0 {
		return &AskResponse{
			Response:   "I don't have any game data to analyze. Make sure you've imported and analyzed some games first.",
			GamesFound: 0,
		}, nil
	}

	// Convert search results
	dbResults := make([]*db.SimilarGameResult, len(searchResult.Games))
	for i, g := range searchResult.Games {
		dbResults[i] = &db.SimilarGameResult{
			GameUUID:    g.GameUUID,
			SummaryText: g.SummaryText,
			Distance:    g.Distance,
			Game:        g.Game.(*db.GameRecord),
		}
	}

	systemPrompt := s.promptBuilder.BuildSystemPrompt(dbResults, s.detailLimit)

    //wrap question with more constraints
	wrappedQuestion := s.promptBuilder.WrapUserQuestion(question)

	messages := []embeddings.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: wrappedQuestion},
	}

	response, err := s.ollama.Chat(s.chatModel, messages)
	if err != nil {
		return nil, fmt.Errorf("failed to generate response: %w", err)
	}

	return &AskResponse{
		Response:         response,
		ExtractedFilters: searchResult.ExtractedFilters,
		GamesFound:       len(searchResult.Games),
	}, nil
}

// dbSearchAdapter adapts the db.DB to implement search.GameSearcher.
type dbSearchAdapter struct {
	db *db.DB
}

func (a *dbSearchAdapter) FindSimilarGamesWithFilters(
	ctx context.Context,
	queryEmbedding []float32,
	filters *search.GameFilters,
	limit int,
) ([]*search.SimilarGameResult, error) {
	// Call the db method
	results, err := a.db.FindSimilarGamesWithFilters(ctx, queryEmbedding, filters, limit)
	if err != nil {
		return nil, err
	}

	// Convert db results to search results
	searchResults := make([]*search.SimilarGameResult, len(results))
	for i, r := range results {
		searchResults[i] = &search.SimilarGameResult{
			GameUUID:    r.GameUUID,
			SummaryText: r.SummaryText,
			Distance:    r.Distance,
			Game:        r.Game,
		}
	}
	return searchResults, nil
}

func (a *dbSearchAdapter) CountGamesMatchingFilters(ctx context.Context, filters *search.GameFilters) (int, error) {
	return a.db.CountGamesMatchingFilters(ctx, filters)
}
