package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/cdewitt02/chesser/internal/api"
	"github.com/cdewitt02/chesser/internal/config"
	"github.com/cdewitt02/chesser/internal/db"
	"github.com/cdewitt02/chesser/internal/llm"
	"github.com/cdewitt02/chesser/internal/models"
)

// Struct to parse the test data JSON
type TestData struct {
	Games []models.Game `json:"games"`
}

const ANALYSIS_DEPTH = 12

type Stats struct {
	TotalCPL     int
	Moves        int
	Blunders     int
	Mistakes     int
	Inaccuracies int
	BestMoves    int
}

func toMoveRecords(gameUUID string, analyses []*models.MoveAnalysis) []*db.MoveRecord {
	records := make([]*db.MoveRecord, 0, len(analyses)) // pre-allocate capacity for efficiency

	for i, analysis := range analyses {
		if analysis == nil {
			continue
		}

		// Chess move numbering: moves 0,1 are move 1, moves 2,3 are move 2, etc.
		moveNumber := (i / 2) + 1

		side := "white"
		if i%2 == 1 {
			side = "black"
		}

		playedMove := ""
		if analysis.PlayedMove != nil {
			playedMove = analysis.PlayedMove.String()
		}

		bestMove := ""
		if analysis.BestMove != nil {
			bestMove = analysis.BestMove.String()
		}

		records = append(records, &db.MoveRecord{
			GameUUID:       gameUUID,
			MoveNumber:     moveNumber,
			Side:           side,
			PlayedMove:     playedMove,
			BestMove:       bestMove,
			FENBefore:      analysis.FENBefore,
			Evaluation:     analysis.Evaluation,
			IsMate:         analysis.IsMate,
			MateIn:         analysis.MateIn,
			CPL:            analysis.CentipawnLoss,
			Classification: analysis.Classification,
		})
	}

	return records
}

func aggregateStats(analyses []*models.MoveAnalysis) (white, black Stats) {
	for i, analysis := range analyses {
		if analysis == nil {
			continue
		}

		// Determine which side made this move
		stats := &white
		if i%2 == 1 {
			stats = &black
		}

		stats.Moves++
		stats.TotalCPL += analysis.CentipawnLoss

		switch analysis.Classification {
		case "blunder":
			stats.Blunders++
		case "mistake":
			stats.Mistakes++
		case "inaccuracy":
			stats.Inaccuracies++
		case "best":
			stats.BestMoves++
		}
	}
	return white, black
}

func getNumWorkers() int {
	if val := os.Getenv("NUM_WORKERS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			return n
		}
	}
	return 4 // default
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "analyze":
		runAnalyze()
	case "refresh-stats":
		runRefreshStats()
	case "reembed":
		runReembed()
	default:
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  go run ./cmd/data analyze <username> <year> <month>  - Analyze games from Chess.com")
	fmt.Println("  go run ./cmd/data refresh-stats <username>           - Refresh aggregate stats for a player")
	fmt.Println("  go run ./cmd/data reembed                            - Rebuild embeddings from stored summaries")
	fmt.Println()
	fmt.Println("Environment variables:")
	fmt.Println("  DATABASE_URL       PostgreSQL connection string (required)")
	fmt.Println("  EMBED_PROVIDER     ollama (default: ollama)")
	fmt.Println("  EMBED_MODEL        Embedding model, must be 768-dimension (default: nomic-embed-text)")
	fmt.Println("  OLLAMA_URL         Ollama server URL (default: http://localhost:11434)")
	fmt.Println("  OLLAMA_EMBED_MODEL Alias for EMBED_MODEL when EMBED_PROVIDER=ollama")
	fmt.Println("  NUM_WORKERS        Analysis worker goroutines (default: 4)")
}

// newEmbedder resolves the embedding provider from the same shared config that
// cmd/chat uses. This entrypoint used to hardcode the Ollama endpoint and
// model, which meant a provider chosen for chat left ingestion silently on a
// different model than the one that built the index.
func newEmbedder(ctx context.Context, database *db.DB) (llm.Embedder, error) {
	cfg, err := config.Resolve(config.OSEnv, "")
	if err != nil {
		return nil, err
	}
	embedder, err := cfg.NewEmbedder()
	if err != nil {
		return nil, err
	}
	if err := config.Preflight(ctx, os.Stderr, embedder); err != nil {
		return nil, err
	}
	// Ingestion writes the index, so it adopts an unstamped one.
	if err := config.CheckIndex(ctx, database, embedder, true, os.Stderr); err != nil {
		return nil, err
	}
	fmt.Printf("Embeddings: %s / %s\n", embedder.Name(), embedder.Model())
	return embedder, nil
}

// runReembed rebuilds every vector from the stored summary text. Summaries are
// generated deterministically with no LLM and no Stockfish, so switching
// embedding models is a bounded re-embed pass rather than a re-analysis.
func runReembed() {
	ctx := context.Background()

	database, err := db.New(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(ctx); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	cfg, err := config.Resolve(config.OSEnv, "")
	if err != nil {
		log.Fatalf("%v", err)
	}
	embedder, err := cfg.NewEmbedder()
	if err != nil {
		log.Fatalf("%v", err)
	}
	if err := config.Preflight(ctx, os.Stderr, embedder); err != nil {
		log.Fatalf("%v", err)
	}

	rows, err := database.AllSummaryTexts(ctx)
	if err != nil {
		log.Fatalf("Failed to read summaries: %v", err)
	}
	if len(rows) == 0 {
		fmt.Println("No stored summaries to re-embed.")
		return
	}

	fmt.Printf("Re-embedding %d summaries with %s / %s...\n", len(rows), embedder.Name(), embedder.Model())
	start := time.Now()

	for i, row := range rows {
		vec, err := llm.EmbedOne(ctx, embedder, row.SummaryText)
		if err != nil {
			log.Fatalf("Failed to embed %s: %v", row.GameUUID, err)
		}
		if err := database.UpdateSummaryEmbedding(ctx, row.GameUUID, vec); err != nil {
			log.Fatalf("%v", err)
		}
		if (i+1)%25 == 0 || i+1 == len(rows) {
			fmt.Printf("[%d/%d]\n", i+1, len(rows))
		}
	}

	if err := database.SetIndexMeta(ctx, &db.IndexMeta{
		EmbedProvider: embedder.Name(),
		EmbedModel:    embedder.Model(),
		Dimensions:    embedder.Dimensions(),
	}); err != nil {
		log.Fatalf("%v", err)
	}

	fmt.Printf("Re-embedded %d summaries in %s\n", len(rows), time.Since(start).Round(time.Millisecond))
}

func runRefreshStats() {
	if len(os.Args) != 3 {
		fmt.Println("Usage: go run ./cmd/data refresh-stats <username>")
		os.Exit(1)
	}

	username := os.Args[2]

	database, err := db.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(context.Background()); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	fmt.Printf("Refreshing stats for %s...\n", username)
	start := time.Now()

	stats, err := database.RefreshPlayerStats(context.Background(), username)
	if err != nil {
		log.Fatalf("Failed to refresh stats: %v", err)
	}

	elapsed := time.Since(start)
	fmt.Printf("Stats refreshed in %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("\nPlayer: %s\n", stats.Username)
	fmt.Printf("Total Games: %d (W: %d, L: %d, D: %d)\n",
		stats.TotalGames, stats.Wins, stats.Losses, stats.Draws)
	fmt.Printf("Average CPL: %.1f\n", stats.AvgCPL)

	if len(stats.StatsByColor) > 0 {
		fmt.Println("\nBy Color:")
		for color, s := range stats.StatsByColor {
			fmt.Printf("  %s: %d games, %.1f%% win rate, %.1f avg CPL\n",
				color, s.Games, s.WinRate, s.AvgCPL)
		}
	}

	if len(stats.StatsByTimeClass) > 0 {
		fmt.Println("\nBy Time Class:")
		for tc, s := range stats.StatsByTimeClass {
			fmt.Printf("  %s: %d games, %.1f%% win rate, %.1f avg CPL\n",
				tc, s.Games, s.WinRate, s.AvgCPL)
		}
	}

	if len(stats.StatsByTermination) > 0 {
		fmt.Println("\nBy Termination:")
		for term, count := range stats.StatsByTermination {
			fmt.Printf("  %s: %d\n", term, count)
		}
	}
}

func runAnalyze() {
	if len(os.Args) != 5 {
		fmt.Println("Usage: go run ./cmd/data analyze <username> <year> <month>")
		os.Exit(1)
	}

	username := os.Args[2]
	year, err := strconv.Atoi(os.Args[3])
	if err != nil {
		log.Fatalf("Invalid year: %v", err)
	}
	month := os.Args[4]

	date := models.YearMonth{
		Year:  year,
		Month: month,
	}

	games, err := api.GetData(date, username)
	if err != nil {
		log.Fatalf("Failed to get data: %v", err)
	}
	fmt.Printf("Fetched %d games from Chess.com\n", len(games))

	database, err := db.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(context.Background()); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	// Filter out already-analyzed games
	var gamesToAnalyze []models.Game
	for _, game := range games {
		exists, err := database.GameExists(context.Background(), game.UUID)
		if err != nil {
			log.Fatalf("Failed to check if game exists: %v", err)
		}
		if !exists {
			gamesToAnalyze = append(gamesToAnalyze, game)
		}
	}

	if len(gamesToAnalyze) == 0 {
		fmt.Println("All games already analyzed!")
		return
	}

	fmt.Printf("%d new games to analyze\n", len(gamesToAnalyze))

	// Initialize embedding client (shared across workers)
	embeddingClient, err := newEmbedder(context.Background(), database)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Create and run worker pool
	numWorkers := getNumWorkers()
	fmt.Printf("Starting %d workers...\n", numWorkers)

	start := time.Now()

	pool := NewWorkerPool(numWorkers, database, embeddingClient, username)
	if err := pool.Process(context.Background(), gamesToAnalyze); err != nil {
		log.Fatalf("Processing failed: %v", err)
	}

	elapsed := time.Since(start)
	gamesPerSecond := float64(len(gamesToAnalyze)) / elapsed.Seconds()

	fmt.Printf("Successfully analyzed %d games in %s (%.2f games/sec)\n",
		len(gamesToAnalyze), elapsed.Round(time.Millisecond), gamesPerSecond)

	// Refresh aggregate stats after analysis
	fmt.Println("\nRefreshing aggregate stats...")
	stats, err := database.RefreshPlayerStats(context.Background(), username)
	if err != nil {
		log.Printf("Warning: Failed to refresh stats: %v", err)
	} else {
		fmt.Printf("Stats updated: %d total games, %.1f%% win rate\n",
			stats.TotalGames,
			float64(stats.Wins)/float64(stats.TotalGames)*100)
	}
}
