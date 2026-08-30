package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type GameRecord struct {
	UUID              string
	URL               string
	PGN               string
	ECOCode           string
	ECOName           string
	WhiteUsername     string
	WhiteRating       int
	BlackUsername     string
	BlackRating       int
	Result            string // "white", "black", "draw"
	TerminationType   string // "checkmate", "resignation", "timeout", etc.
	TimeControl       string
	TimeClass         string
	Rated             bool
	AvgCPLWhite       float64
	AvgCPLBlack       float64
	BlundersWhite     int
	BlundersBlack     int
	MistakesWhite     int
	MistakesBlack     int
	InaccuraciesWhite int
	InaccuraciesBlack int
	BestMovesWhite    int
	BestMovesBlack    int
	PlayedAt          time.Time
}

func (db *DB) SaveGame(ctx context.Context, game *GameRecord) error {
	query := `
		INSERT INTO games (
			uuid, url, pgn, eco_code, eco_name,
			white_username, white_rating, black_username, black_rating,
			result, termination_type, time_control, time_class, rated,
			avg_cpl_white, avg_cpl_black,
			blunders_white, blunders_black,
			mistakes_white, mistakes_black,
			inaccuracies_white, inaccuracies_black,
			best_moves_white, best_moves_black, played_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13, $14,
			$15, $16,
			$17, $18,
			$19, $20,
			$21, $22,
			$23, $24, $25
		)
		ON CONFLICT (uuid) DO UPDATE SET
			pgn = EXCLUDED.pgn,
			avg_cpl_white = EXCLUDED.avg_cpl_white,
			avg_cpl_black = EXCLUDED.avg_cpl_black,
			blunders_white = EXCLUDED.blunders_white,
			blunders_black = EXCLUDED.blunders_black,
			mistakes_white = EXCLUDED.mistakes_white,
			mistakes_black = EXCLUDED.mistakes_black,
			inaccuracies_white = EXCLUDED.inaccuracies_white,
			inaccuracies_black = EXCLUDED.inaccuracies_black,
			best_moves_white = EXCLUDED.best_moves_white,
			best_moves_black = EXCLUDED.best_moves_black,
			termination_type = EXCLUDED.termination_type,
			played_at = EXCLUDED.played_at
	`

	_, err := db.pool.Exec(ctx, query,
		game.UUID, game.URL, game.PGN, game.ECOCode, game.ECOName,
		game.WhiteUsername, game.WhiteRating, game.BlackUsername, game.BlackRating,
		game.Result, game.TerminationType, game.TimeControl, game.TimeClass, game.Rated,
		game.AvgCPLWhite, game.AvgCPLBlack,
		game.BlundersWhite, game.BlundersBlack,
		game.MistakesWhite, game.MistakesBlack,
		game.InaccuraciesWhite, game.InaccuraciesBlack,
		game.BestMovesWhite, game.BestMovesBlack, game.PlayedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save game: %w", err)
	}
	return nil
}

func (db *DB) GetGame(ctx context.Context, uuid string) (*GameRecord, error) {
	query := `
		SELECT uuid, url, pgn, eco_code, eco_name,
			   white_username, white_rating, black_username, black_rating,
			   result, termination_type, time_control, time_class, rated,
			   avg_cpl_white, avg_cpl_black,
			   blunders_white, blunders_black,
			   mistakes_white, mistakes_black,
			   inaccuracies_white, inaccuracies_black,
			   best_moves_white, best_moves_black
		FROM games
		WHERE uuid = $1
	`

	var game GameRecord
	err := db.pool.QueryRow(ctx, query, uuid).Scan(
		&game.UUID, &game.URL, &game.PGN, &game.ECOCode, &game.ECOName,
		&game.WhiteUsername, &game.WhiteRating, &game.BlackUsername, &game.BlackRating,
		&game.Result, &game.TerminationType, &game.TimeControl, &game.TimeClass, &game.Rated,
		&game.AvgCPLWhite, &game.AvgCPLBlack,
		&game.BlundersWhite, &game.BlundersBlack,
		&game.MistakesWhite, &game.MistakesBlack,
		&game.InaccuraciesWhite, &game.InaccuraciesBlack,
		&game.BestMovesWhite, &game.BestMovesBlack,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get game: %w", err)
	}
	return &game, nil
}

func (db *DB) GameExists(ctx context.Context, uuid string) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM games WHERE uuid = $1)", uuid).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check game existence: %w", err)
	}
	return exists, nil
}

func (db *DB) GetGamesByECO(ctx context.Context, ecoPrefix string, limit int) ([]*GameRecord, error) {
	query := `
		SELECT uuid, url, pgn, eco_code, eco_name,
			   white_username, white_rating, black_username, black_rating,
			   result, termination_type, time_control, time_class, rated,
			   avg_cpl_white, avg_cpl_black,
			   blunders_white, blunders_black,
			   mistakes_white, mistakes_black,
			   inaccuracies_white, inaccuracies_black,
			   best_moves_white, best_moves_black
		FROM games
		WHERE eco_code LIKE $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := db.pool.Query(ctx, query, ecoPrefix+"%", limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query games by ECO: %w", err)
	}
	defer rows.Close()

	var games []*GameRecord
	for rows.Next() {
		var game GameRecord
		err := rows.Scan(
			&game.UUID, &game.URL, &game.PGN, &game.ECOCode, &game.ECOName,
			&game.WhiteUsername, &game.WhiteRating, &game.BlackUsername, &game.BlackRating,
			&game.Result, &game.TerminationType, &game.TimeControl, &game.TimeClass, &game.Rated,
			&game.AvgCPLWhite, &game.AvgCPLBlack,
			&game.BlundersWhite, &game.BlundersBlack,
			&game.MistakesWhite, &game.MistakesBlack,
			&game.InaccuraciesWhite, &game.InaccuraciesBlack,
			&game.BestMovesWhite, &game.BestMovesBlack,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan game: %w", err)
		}
		games = append(games, &game)
	}
	return games, nil
}

func (db *DB) GetGamesByPlayer(ctx context.Context, username string, limit int) ([]*GameRecord, error) {
	query := `
		SELECT uuid, url, pgn, eco_code, eco_name,
			   white_username, white_rating, black_username, black_rating,
			   result, termination_type, time_control, time_class, rated,
			   avg_cpl_white, avg_cpl_black,
			   blunders_white, blunders_black,
			   mistakes_white, mistakes_black,
			   inaccuracies_white, inaccuracies_black,
			   best_moves_white, best_moves_black
		FROM games
		WHERE white_username = $1 OR black_username = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := db.pool.Query(ctx, query, username, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query games by player: %w", err)
	}
	defer rows.Close()

	var games []*GameRecord
	for rows.Next() {
		var game GameRecord
		err := rows.Scan(
			&game.UUID, &game.URL, &game.PGN, &game.ECOCode, &game.ECOName,
			&game.WhiteUsername, &game.WhiteRating, &game.BlackUsername, &game.BlackRating,
			&game.Result, &game.TerminationType, &game.TimeControl, &game.TimeClass, &game.Rated,
			&game.AvgCPLWhite, &game.AvgCPLBlack,
			&game.BlundersWhite, &game.BlundersBlack,
			&game.MistakesWhite, &game.MistakesBlack,
			&game.InaccuraciesWhite, &game.InaccuraciesBlack,
			&game.BestMovesWhite, &game.BestMovesBlack,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan game: %w", err)
		}
		games = append(games, &game)
	}
	return games, nil
}

type OpeningStats struct {
	ECOCode       string
	ECOName       string
	GamesAsWhite  int
	GamesAsBlack  int
	WinsAsWhite   int
	WinsAsBlack   int
	AvgCPLAsWhite float64
	AvgCPLAsBlack float64
}

func (db *DB) GetOpeningStats(ctx context.Context, username string) ([]*OpeningStats, error) {
	query := `
		SELECT 
			eco_code,
			MAX(eco_name) as eco_name,
			COUNT(*) FILTER (WHERE white_username = $1) as games_as_white,
			COUNT(*) FILTER (WHERE black_username = $1) as games_as_black,
			COUNT(*) FILTER (WHERE white_username = $1 AND result = 'white') as wins_as_white,
			COUNT(*) FILTER (WHERE black_username = $1 AND result = 'black') as wins_as_black,
			COALESCE(AVG(avg_cpl_white) FILTER (WHERE white_username = $1), 0) as avg_cpl_white,
			COALESCE(AVG(avg_cpl_black) FILTER (WHERE black_username = $1), 0) as avg_cpl_black
		FROM games
		WHERE white_username = $1 OR black_username = $1
		GROUP BY eco_code
		ORDER BY (COUNT(*) FILTER (WHERE white_username = $1) + COUNT(*) FILTER (WHERE black_username = $1)) DESC
	`

	rows, err := db.pool.Query(ctx, query, username)
	if err != nil {
		return nil, fmt.Errorf("failed to query opening stats: %w", err)
	}
	defer rows.Close()

	var stats []*OpeningStats
	for rows.Next() {
		var s OpeningStats
		err := rows.Scan(
			&s.ECOCode, &s.ECOName,
			&s.GamesAsWhite, &s.GamesAsBlack,
			&s.WinsAsWhite, &s.WinsAsBlack,
			&s.AvgCPLAsWhite, &s.AvgCPLAsBlack,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan opening stats: %w", err)
		}
		stats = append(stats, &s)
	}
	return stats, nil
}
