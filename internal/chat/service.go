package chat

import (
	"context"
	"fmt"

	"github.com/chesser/internal/db"
	"github.com/chesser/internal/llm"
	"github.com/chesser/internal/search"
)

type Service struct {
	db              *db.DB
	chat            llm.ChatModel
	chatModel       string
	username        string
	numSimilar      int
	promptBuilder   *PromptBuilder
	hybridSearcher  *search.HybridSearcher
	queryRouter     *QueryRouter
	detailLimit     int
	history         []llm.Message
	maxHistoryPairs int
}

type Config struct {
	ChatModel       string
	Username        string
	NumSimilar      int
	DetailLimit     int
	MaxHistoryPairs int
}

// NewService wires the two model roles separately. They used to be one
// concrete Ollama client doing double duty; a chat provider and an embedding
// provider are now independently selectable, which is what makes "same index,
// different chat model" a configurable experiment.
func NewService(database *db.DB, chatModel llm.ChatModel, embedder llm.Embedder, cfg Config) *Service {
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
	hybridSearcher := search.NewHybridSearcher(embedder, dbAdapter)
	promptBuilder := NewPromptBuilder(cfg.Username)
	queryRouter := NewQueryRouter(database, hybridSearcher, promptBuilder, cfg.Username, numSimilar)

	return &Service{
		db:              database,
		chat:            chatModel,
		chatModel:       cfg.ChatModel,
		username:        cfg.Username,
		numSimilar:      numSimilar,
		promptBuilder:   promptBuilder,
		hybridSearcher:  hybridSearcher,
		queryRouter:     queryRouter,
		detailLimit:     detailLimit,
		history:         make([]llm.Message, 0),
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

	// The system prompt is a field, not messages[0]: Anthropic takes it as a
	// top-level parameter, and each adapter puts it where its provider wants.
	messages := make([]llm.Message, 0, len(s.history)+1)
	messages = append(messages, s.history...)
	messages = append(messages, llm.Message{Role: llm.RoleUser, Content: question})

	resp, err := s.chat.Chat(ctx, llm.ChatRequest{
		System:   systemPrompt,
		Messages: messages,
		Model:    s.chatModel,
	})
	if err != nil {
		return "", fmt.Errorf("failed to generate response: %w", err)
	}
	response := resp.Text

	s.history = append(s.history,
		llm.Message{Role: llm.RoleUser, Content: question},
		llm.Message{Role: llm.RoleAssistant, Content: response},
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
