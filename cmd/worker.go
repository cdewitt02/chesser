package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/chesser/internal/db"
	"github.com/chesser/internal/embeddings"
	"github.com/chesser/internal/engine"
	"github.com/chesser/internal/models"
	"github.com/chesser/internal/summary"
	"github.com/notnil/chess/uci"
)

type Worker struct {
	id              int
	engine          *uci.Engine
	db              *db.DB
	embeddingClient *embeddings.Client
	username        string
}

func (w *Worker) ProcessGame(ctx context.Context, game models.Game) error {
	gameAnalysis, err := engine.AnalyzeGame(w.engine, game.PGN, ANALYSIS_DEPTH)
	if err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	summaryData := summary.ExtractSummaryData(&game, gameAnalysis, w.username)
	gameSummary := summary.GenerateSummary(summaryData)

	embedding, err := w.embeddingClient.GetEmbedding(gameSummary)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	whiteStats, blackStats := aggregateStats(gameAnalysis)

	avgCPLWhite := 0.0
	avgCPLBlack := 0.0
	if whiteStats.Moves > 0 {
		avgCPLWhite = float64(whiteStats.TotalCPL) / float64(whiteStats.Moves)
	}
	if blackStats.Moves > 0 {
		avgCPLBlack = float64(blackStats.TotalCPL) / float64(blackStats.Moves)
	}

	err = w.db.SaveGame(ctx, &db.GameRecord{
		UUID:              game.UUID,
		URL:               game.URL,
		PGN:               game.PGN,
		ECOCode:           game.ECOCode(),
		ECOName:           game.OpeningName(),
		WhiteUsername:     game.White.Username,
		WhiteRating:       int(game.White.Rating),
		BlackUsername:     game.Black.Username,
		BlackRating:       int(game.Black.Rating),
		Result:            game.GameResult(),
		TimeControl:       game.TimeControl,
		TimeClass:         game.TimeClass,
		Rated:             game.Rated,
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
		return fmt.Errorf("save game failed: %w", err)
	}

	moveRecords := toMoveRecords(game.UUID, gameAnalysis)
	if err := w.db.SaveMoves(ctx, moveRecords); err != nil {
		return fmt.Errorf("save moves failed: %w", err)
	}

	if err := w.db.SaveGameSummary(ctx, game.UUID, gameSummary, embedding); err != nil {
		return fmt.Errorf("save summary failed: %w", err)
	}

	return nil
}

type WorkerPool struct {
	numWorkers      int
	db              *db.DB
	embeddingClient *embeddings.Client
	username        string
}

func NewWorkerPool(numWorkers int, database *db.DB, embClient *embeddings.Client, username string) *WorkerPool {
	return &WorkerPool{
		numWorkers:      numWorkers,
		db:              database,
		embeddingClient: embClient,
		username:        username,
	}
}


func (wp *WorkerPool) Process(ctx context.Context, games []models.Game) error {
	if len(games) == 0 {
		return nil
	}

	// Create cancellable context - when one worker fails, cancel all others
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Channel to send games to workers (buffered to avoid blocking sender)
	gameChan := make(chan models.Game, len(games))

	// Channel to collect the first error
	errChan := make(chan error, 1)

	// Progress tracking
	var completed atomic.Int32
	totalGames := len(games)

	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < wp.numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			eng, err := engine.StartEngine()
			if err != nil {
				select {
				case errChan <- fmt.Errorf("worker %d: failed to start engine: %w", workerID, err):
					cancel() 
				default:
				}
				return
			}
			defer engine.StopEngine(eng)

			worker := &Worker{
				id:              workerID,
				engine:          eng,
				db:              wp.db,
				embeddingClient: wp.embeddingClient,
				username:        wp.username,
			}

			for game := range gameChan {
				select {
				case <-ctx.Done():
					return
				default:
				}

				err := worker.ProcessGame(ctx, game)
				if err != nil {
					select {
					case errChan <- fmt.Errorf("worker %d: game %s: %w", workerID, game.UUID, err):
						cancel() 
					default:
					}
					return
				}

				done := completed.Add(1)
				fmt.Printf("✅ [%d/%d] Worker %d: %s vs %s\n",
					done, totalGames, workerID,
					game.White.Username, game.Black.Username)
			}
		}(i)
	}

	for _, game := range games {
		select {
		case gameChan <- game:
		case <-ctx.Done():
			break
		}
	}
	close(gameChan)

	wg.Wait()

	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}

