package chat

import (
	"context"
	"fmt"
	"os"

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

// noDataAnswer is returned verbatim when the corpus is empty. It is markdown,
// like every other answer, so the caller can render one thing and not two.
const noDataAnswer = "I don't have any game data to analyze. Make sure you've imported and analyzed some games first."

// Ask processes a question using query classification and routing.
// Different query types get different context:
// - Aggregate/Comparative: Focus on pre-computed stats
// - SpecificGames: Focus on RAG search results
// - Recommendation: Combine stats + relevant examples
//
// The returned answer is markdown source, deliberately unrendered: presentation
// belongs to the caller, so an eval harness and the terminal REPL see the same
// text.
func (s *Service) Ask(ctx context.Context, question string) (string, error) {
	return s.AskStream(ctx, question, nil)
}

// AskStream is Ask with incremental delivery. onDelta, when non-nil, receives
// fragments of the answer as the provider produces them; the complete answer is
// still returned, so a caller that streams for display does not have to
// reassemble it.
//
// A chat model that does not implement llm.StreamingChatModel still works: the
// whole answer arrives as a single delta. Callers therefore never need to ask
// which provider is configured.
//
// An error from onDelta aborts the request and is returned unwrapped, so a
// caller can recognize its own failure.
func (s *Service) AskStream(ctx context.Context, question string, onDelta func(string) error) (string, error) {
	qctx, err := s.queryRouter.Route(ctx, question)
	if err != nil {
		return "", fmt.Errorf("failed to route query: %w", err)
	}

	hasStats := qctx.PlayerStats != nil && qctx.PlayerStats.TotalGames > 0
	hasGames := len(qctx.Games) > 0

	if !hasStats && !hasGames {
		// Not streamed: there is no provider call to stream, and emitting it
		// as a delta would make the caller erase and repaint identical text.
		return noDataAnswer, nil
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

	req := llm.ChatRequest{
		System:   systemPrompt,
		Messages: messages,
		Model:    s.chatModel,
	}

	var resp *llm.ChatResponse
	streamer, canStream := s.chat.(llm.StreamingChatModel)
	switch {
	case onDelta != nil && canStream:
		resp, err = streamer.ChatStream(ctx, req, onDelta)
	case onDelta != nil:
		resp, err = s.chat.Chat(ctx, req)
		if err == nil {
			if derr := onDelta(resp.Text); derr != nil {
				return "", derr
			}
		}
	default:
		resp, err = s.chat.Chat(ctx, req)
	}
	if err != nil {
		return "", fmt.Errorf("failed to generate response: %w", err)
	}
	response := resp.Text

	s.history = append(s.history,
		llm.Message{Role: llm.RoleUser, Content: question},
		llm.Message{Role: llm.RoleAssistant, Content: response},
	)
	s.truncateHistory()

	s.debugPrompt(qctx.QueryType, systemPrompt)

	return response, nil
}

// debugPrompt dumps the assembled prompt when CHESSER_DEBUG_PROMPT is set.
//
// This used to print unconditionally, which buried every answer under a few
// hundred lines of game summaries — the single largest readability problem in
// the REPL. It goes to stderr so that redirecting stdout still captures a clean
// transcript.
func (s *Service) debugPrompt(queryType any, systemPrompt string) {
	if os.Getenv("CHESSER_DEBUG_PROMPT") == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "=== QUERY TYPE: %v ===\n", queryType)
	fmt.Fprintln(os.Stderr, "=== SYSTEM PROMPT ===")
	fmt.Fprintln(os.Stderr, systemPrompt)
	fmt.Fprintln(os.Stderr, "=== END PROMPT ===")
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
