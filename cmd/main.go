package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/chesser/internal/api"
	"github.com/chesser/internal/db"
	"github.com/chesser/internal/engine"
	"github.com/chesser/internal/models"
	"github.com/chesser/internal/summary"
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

func main() {
	if len(os.Args) != 4 {
		fmt.Println("Usage: go run cmd/main.go <username> <year> <month>")
		os.Exit(1)
	}

	username := os.Args[1]
	year, err := strconv.Atoi(os.Args[2])
	month := os.Args[3]

	date := models.YearMonth{
		Year:  year,
		Month: month,
	}

	games, err := api.GetData(date, username)
	if err != nil {
		log.Fatalf("Failed to get data: %v", err)
	}

	database, err := db.New(context.Background(), os.Getenv("DATABASE_URL"))
	if err != nil {
		log.Fatalf("Failed to create database: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(context.Background()); err != nil {
		log.Fatalf("Failed to migrate database: %v", err)
	}

	dbEngine, err := engine.StartEngine()
	if err != nil {
		log.Fatalf("Failed to start engine: %v", err)
	}

	defer engine.StopEngine(dbEngine)

	for _, game := range games {
		exists, err := database.GameExists(context.Background(), game.UUID)
		if err != nil {
			log.Fatalf("Failed to check if game exists: %v", err)
		}
		if exists {
			fmt.Println("Game already analyzed")
			continue
		}

		gameAnalysis, err := engine.AnalyzeGame(dbEngine, game.PGN, ANALYSIS_DEPTH)
		if err != nil {
			log.Fatalf("Failed to analyze game: %v", err)
		}

		summaryData := summary.ExtractSummaryData(&game, gameAnalysis, username)
		gameSummary := summary.GenerateSummary(summaryData)
		fmt.Println("\n--- Game Summary ---")
		fmt.Println(gameSummary)
		fmt.Println("--------------------")

		whiteStats, blackStats := aggregateStats(gameAnalysis)

		avgCPLWhite := 0.0
		avgCPLBlack := 0.0
		if whiteStats.Moves > 0 {
			avgCPLWhite = float64(whiteStats.TotalCPL) / float64(whiteStats.Moves)
		}
		if blackStats.Moves > 0 {
			avgCPLBlack = float64(blackStats.TotalCPL) / float64(blackStats.Moves)
		}

		// Map models.Game + stats to db.GameRecord
		err = database.SaveGame(context.Background(), &db.GameRecord{
			// Direct mappings from API response
			UUID:          game.UUID,
			URL:           game.URL,
			PGN:           game.PGN,
			ECOCode:       game.ECOCode(),
			ECOName:       game.OpeningName(),
			WhiteUsername: game.White.Username,
			WhiteRating:   int(game.White.Rating),
			BlackUsername: game.Black.Username,
			BlackRating:   int(game.Black.Rating),
			Result:        game.GameResult(),
			TimeControl:   game.TimeControl,
			TimeClass:     game.TimeClass,
			Rated:         game.Rated,

			// Computed from analysis
			AvgCPLWhite:       avgCPLWhite,
			AvgCPLBlack:       avgCPLBlack,
			BlundersWhite:     whiteStats.Blunders,
			BlundersBlack:     blackStats.Blunders,
			MistakesWhite:     whiteStats.Mistakes,
			MistakesBlack:     blackStats.Mistakes,
			InaccuraciesWhite: whiteStats.Inaccuracies,
			InaccuraciesBlack: blackStats.Inaccuracies,
			BestMovesWhite:    whiteStats.BestMoves,
			BestMovesBlack:    blackStats.BestMoves,
		})
		if err != nil {
			log.Fatalf("Failed to save game: %v", err)
		}

		moveRecords := toMoveRecords(game.UUID, gameAnalysis)
		if err := database.SaveMoves(context.Background(), moveRecords); err != nil {
			log.Fatalf("Failed to save moves: %v", err)
		}

		fmt.Printf("✅ Analyzed and saved: %s vs %s (%d moves)\n", game.White.Username, game.Black.Username, len(moveRecords))

	}

}
