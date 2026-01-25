package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type MoveRecord struct {
	ID             int
	GameUUID       string
	MoveNumber     int
	Side           string // "white" or "black"
	PlayedMove     string // UCI notation: "e2e4"
	BestMove       string // UCI notation: "e2e4"
	FENBefore      string
	Evaluation     int // centipawns
	IsMate         bool
	MateIn         int
	CPL            int    // centipawn loss
	Classification string // "best", "good", "inaccuracy", "mistake", "blunder"
}

func (db *DB) SaveMoves(ctx context.Context, moves []*MoveRecord) error {
	if len(moves) == 0 {
		return nil
	}

	_, err := db.pool.CopyFrom(
		ctx,
		pgx.Identifier{"moves"},
		[]string{
			"game_uuid", "move_number", "side", "played_move", "best_move",
			"fen_before", "evaluation", "is_mate", "mate_in", "cpl", "classification",
		},
		pgx.CopyFromSlice(len(moves), func(i int) ([]any, error) {
			m := moves[i]
			return []any{
				m.GameUUID, m.MoveNumber, m.Side, m.PlayedMove, m.BestMove,
				m.FENBefore, m.Evaluation, m.IsMate, m.MateIn, m.CPL, m.Classification,
			}, nil
		}),
	)
	if err != nil {
		return fmt.Errorf("failed to bulk insert moves: %w", err)
	}
	return nil
}

func (db *DB) GetMovesForGame(ctx context.Context, gameUUID string) ([]*MoveRecord, error) {
	query := `
		SELECT id, game_uuid, move_number, side, played_move, best_move,
			   fen_before, evaluation, is_mate, mate_in, cpl, classification
		FROM moves
		WHERE game_uuid = $1
		ORDER BY move_number
	`

	rows, err := db.pool.Query(ctx, query, gameUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query moves: %w", err)
	}
	defer rows.Close()

	var moves []*MoveRecord
	for rows.Next() {
		var m MoveRecord
		err := rows.Scan(
			&m.ID, &m.GameUUID, &m.MoveNumber, &m.Side, &m.PlayedMove, &m.BestMove,
			&m.FENBefore, &m.Evaluation, &m.IsMate, &m.MateIn, &m.CPL, &m.Classification,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan move: %w", err)
		}
		moves = append(moves, &m)
	}
	return moves, nil
}

func (db *DB) GetBlundersForPlayer(ctx context.Context, username string, limit int) ([]*MoveRecord, error) {
	query := `
		SELECT m.id, m.game_uuid, m.move_number, m.side, m.played_move, m.best_move,
			   m.fen_before, m.evaluation, m.is_mate, m.mate_in, m.cpl, m.classification
		FROM moves m
		JOIN games g ON m.game_uuid = g.uuid
		WHERE m.classification = 'blunder'
		  AND ((m.side = 'white' AND g.white_username = $1)
		    OR (m.side = 'black' AND g.black_username = $1))
		ORDER BY m.cpl DESC
		LIMIT $2
	`

	rows, err := db.pool.Query(ctx, query, username, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query blunders: %w", err)
	}
	defer rows.Close()

	var moves []*MoveRecord
	for rows.Next() {
		var m MoveRecord
		err := rows.Scan(
			&m.ID, &m.GameUUID, &m.MoveNumber, &m.Side, &m.PlayedMove, &m.BestMove,
			&m.FENBefore, &m.Evaluation, &m.IsMate, &m.MateIn, &m.CPL, &m.Classification,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan blunder: %w", err)
		}
		moves = append(moves, &m)
	}
	return moves, nil
}

func (db *DB) GetMovesByClassification(ctx context.Context, username string, classification string, limit int) ([]*MoveRecord, error) {
	query := `
		SELECT m.id, m.game_uuid, m.move_number, m.side, m.played_move, m.best_move,
			   m.fen_before, m.evaluation, m.is_mate, m.mate_in, m.cpl, m.classification
		FROM moves m
		JOIN games g ON m.game_uuid = g.uuid
		WHERE m.classification = $1
		  AND ((m.side = 'white' AND g.white_username = $2)
		    OR (m.side = 'black' AND g.black_username = $2))
		ORDER BY m.cpl DESC
		LIMIT $3
	`

	rows, err := db.pool.Query(ctx, query, classification, username, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query moves by classification: %w", err)
	}
	defer rows.Close()

	var moves []*MoveRecord
	for rows.Next() {
		var m MoveRecord
		err := rows.Scan(
			&m.ID, &m.GameUUID, &m.MoveNumber, &m.Side, &m.PlayedMove, &m.BestMove,
			&m.FENBefore, &m.Evaluation, &m.IsMate, &m.MateIn, &m.CPL, &m.Classification,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan move: %w", err)
		}
		moves = append(moves, &m)
	}
	return moves, nil
}

func (db *DB) DeleteMovesForGame(ctx context.Context, gameUUID string) error {
	_, err := db.pool.Exec(ctx, "DELETE FROM moves WHERE game_uuid = $1", gameUUID)
	if err != nil {
		return fmt.Errorf("failed to delete moves: %w", err)
	}
	return nil
}
