package db

import (
	"context"
	"fmt"

	"github.com/pgvector/pgvector-go"
)

type GameSummary struct {
	GameUUID    string
	SummaryText string
	Embedding   pgvector.Vector
}

type SimilarGameResult struct {
	GameUUID    string
	SummaryText string
	Distance    float64
	Game        *GameRecord
}

func (db *DB) SaveGameSummary(ctx context.Context, gameUUID string, summaryText string, embedding []float32) error {
	query := `
		INSERT INTO game_summaries (game_uuid, summary_text, embedding)
		VALUES ($1, $2, $3)
		ON CONFLICT (game_uuid) DO UPDATE SET
			summary_text = EXCLUDED.summary_text,
			embedding = EXCLUDED.embedding
	`

	_, err := db.pool.Exec(ctx, query, gameUUID, summaryText, pgvector.NewVector(embedding))
	if err != nil {
		return fmt.Errorf("failed to save game summary: %w", err)
	}
	return nil
}

func (db *DB) GetGameSummary(ctx context.Context, gameUUID string) (*GameSummary, error) {
	query := `
		SELECT game_uuid, summary_text, embedding
		FROM game_summaries
		WHERE game_uuid = $1
	`

	var summary GameSummary
	err := db.pool.QueryRow(ctx, query, gameUUID).Scan(
		&summary.GameUUID, &summary.SummaryText, &summary.Embedding,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get game summary: %w", err)
	}
	return &summary, nil
}

func (db *DB) FindSimilarGames(ctx context.Context, queryEmbedding []float32, limit int) ([]*SimilarGameResult, error) {
	query := `
		SELECT gs.game_uuid, gs.summary_text, gs.embedding <-> $1 AS distance
		FROM game_summaries gs
		ORDER BY distance
		LIMIT $2
	`

	rows, err := db.pool.Query(ctx, query, pgvector.NewVector(queryEmbedding), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar games: %w", err)
	}
	defer rows.Close()

	var results []*SimilarGameResult
	for rows.Next() {
		var r SimilarGameResult
		err := rows.Scan(&r.GameUUID, &r.SummaryText, &r.Distance)
		if err != nil {
			return nil, fmt.Errorf("failed to scan similar game: %w", err)
		}
		results = append(results, &r)
	}
	return results, nil
}

func (db *DB) FindSimilarGamesWithFilter(
	ctx context.Context,
	queryEmbedding []float32,
	ecoPrefix string,
	result string, // "white", "black", "draw", or "" for any
	username string,
	limit int,
) ([]*SimilarGameResult, error) {
	query := `
		SELECT gs.game_uuid, gs.summary_text, gs.embedding <-> $1 AS distance,
			   g.uuid, g.url, g.pgn, g.eco_code, g.eco_name,
			   g.white_username, g.white_rating, g.black_username, g.black_rating,
			   g.result, g.time_control, g.time_class, g.rated,
			   g.avg_cpl_white, g.avg_cpl_black,
			   g.blunders_white, g.blunders_black,
			   g.mistakes_white, g.mistakes_black,
			   g.inaccuracies_white, g.inaccuracies_black,
			   g.best_moves_white, g.best_moves_black
		FROM game_summaries gs
		JOIN games g ON gs.game_uuid = g.uuid
		WHERE ($2 = '' OR g.eco_code LIKE $2)
		  AND ($3 = '' OR g.result = $3)
		  AND ($4 = '' OR g.white_username = $4 OR g.black_username = $4)
		ORDER BY distance
		LIMIT $5
	`

	ecoFilter := ""
	if ecoPrefix != "" {
		ecoFilter = ecoPrefix + "%"
	}

	rows, err := db.pool.Query(ctx, query, pgvector.NewVector(queryEmbedding), ecoFilter, result, username, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar games with filter: %w", err)
	}
	defer rows.Close()

	var results []*SimilarGameResult
	for rows.Next() {
		var r SimilarGameResult
		var game GameRecord
		err := rows.Scan(
			&r.GameUUID, &r.SummaryText, &r.Distance,
			&game.UUID, &game.URL, &game.PGN, &game.ECOCode, &game.ECOName,
			&game.WhiteUsername, &game.WhiteRating, &game.BlackUsername, &game.BlackRating,
			&game.Result, &game.TimeControl, &game.TimeClass, &game.Rated,
			&game.AvgCPLWhite, &game.AvgCPLBlack,
			&game.BlundersWhite, &game.BlundersBlack,
			&game.MistakesWhite, &game.MistakesBlack,
			&game.InaccuraciesWhite, &game.InaccuraciesBlack,
			&game.BestMovesWhite, &game.BestMovesBlack,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan similar game with filter: %w", err)
		}
		r.Game = &game
		results = append(results, &r)
	}
	return results, nil
}

func (db *DB) CountGamesWithSummaries(ctx context.Context) (int, error) {
	var count int
	err := db.pool.QueryRow(ctx, "SELECT COUNT(*) FROM game_summaries").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count summaries: %w", err)
	}
	return count, nil
}

func (db *DB) GamesWithoutSummaries(ctx context.Context, limit int) ([]string, error) {
	query := `
		SELECT g.uuid
		FROM games g
		LEFT JOIN game_summaries gs ON g.uuid = gs.game_uuid
		WHERE gs.game_uuid IS NULL
		LIMIT $1
	`

	rows, err := db.pool.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query games without summaries: %w", err)
	}
	defer rows.Close()

	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, fmt.Errorf("failed to scan uuid: %w", err)
		}
		uuids = append(uuids, uuid)
	}
	return uuids, nil
}
