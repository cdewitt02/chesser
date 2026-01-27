package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/chesser/internal/api"
	"github.com/chesser/internal/db"
	"github.com/chesser/internal/embeddings"
	"github.com/chesser/internal/models"
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
	if len(os.Args) != 4 {
		fmt.Println("Usage: go run cmd/main.go <username> <year> <month>")
		os.Exit(1)
	}

	username := os.Args[1]
	year, err := strconv.Atoi(os.Args[2])
	if err != nil {
		log.Fatalf("Invalid year: %v", err)
	}
	month := os.Args[3]

	date := models.YearMonth{
		Year:  year,
		Month: month,
	}

	games, err := api.GetData(date, username)
	if err != nil {
		log.Fatalf("Failed to get data: %v", err)
	}
	fmt.Printf("📥 Fetched %d games from Chess.com\n", len(games))

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
		fmt.Println("✨ All games already analyzed!")
		return
	}

	fmt.Printf("🔍 %d new games to analyze\n", len(gamesToAnalyze))

	// Initialize embedding client (shared across workers)
	embeddingClient := embeddings.New("http://localhost:11434", "nomic-embed-text")

	// Create and run worker pool
	numWorkers := getNumWorkers()
	fmt.Printf("🚀 Starting %d workers...\n", numWorkers)

	start := time.Now()

	pool := NewWorkerPool(numWorkers, database, embeddingClient, username)
	if err := pool.Process(context.Background(), gamesToAnalyze); err != nil {
		log.Fatalf("Processing failed: %v", err)
	}

	elapsed := time.Since(start)
	gamesPerSecond := float64(len(gamesToAnalyze)) / elapsed.Seconds()

	fmt.Printf("🎉 Successfully analyzed %d games in %s (%.2f games/sec)\n",
		len(gamesToAnalyze), elapsed.Round(time.Millisecond), gamesPerSecond)
}
