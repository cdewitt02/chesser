package db

import (
	"context"
	"fmt"
)

const Schema = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS games (
    uuid UUID PRIMARY KEY,
    url TEXT,
    pgn TEXT NOT NULL,
    eco_code VARCHAR(10),
    eco_name TEXT,
    white_username TEXT NOT NULL,
    white_rating INT,
    black_username TEXT NOT NULL,
    black_rating INT,
    result VARCHAR(10),
    termination_type VARCHAR(50),
    time_control VARCHAR(50),
    time_class VARCHAR(20),
    rated BOOLEAN DEFAULT true,
    avg_cpl_white REAL DEFAULT 0,
    avg_cpl_black REAL DEFAULT 0,
    blunders_white INT DEFAULT 0,
    blunders_black INT DEFAULT 0,
    mistakes_white INT DEFAULT 0,
    mistakes_black INT DEFAULT 0,
    inaccuracies_white INT DEFAULT 0,
    inaccuracies_black INT DEFAULT 0,
    best_moves_white INT DEFAULT 0,
    best_moves_black INT DEFAULT 0,
    played_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_games_eco_code ON games(eco_code);
CREATE INDEX IF NOT EXISTS idx_games_white_username ON games(white_username);
CREATE INDEX IF NOT EXISTS idx_games_black_username ON games(black_username);
CREATE INDEX IF NOT EXISTS idx_games_played_at ON games(played_at DESC);
CREATE INDEX IF NOT EXISTS idx_games_created_at ON games(created_at DESC);

CREATE TABLE IF NOT EXISTS moves (
    id SERIAL PRIMARY KEY,
    game_uuid UUID REFERENCES games(uuid) ON DELETE CASCADE,
    move_number INT NOT NULL,
    side VARCHAR(5) NOT NULL,
    played_move TEXT NOT NULL,
    best_move TEXT,
    fen_before TEXT,
    evaluation INT,  -- centipawns
    is_mate BOOLEAN DEFAULT false,
    mate_in INT DEFAULT 0,
    cpl INT DEFAULT 0,
    classification VARCHAR(20)
);

CREATE INDEX IF NOT EXISTS idx_moves_game_uuid ON moves(game_uuid);
CREATE INDEX IF NOT EXISTS idx_moves_classification ON moves(classification);
CREATE INDEX IF NOT EXISTS idx_moves_cpl ON moves(cpl DESC);

CREATE TABLE IF NOT EXISTS game_summaries (
    game_uuid UUID PRIMARY KEY REFERENCES games(uuid) ON DELETE CASCADE,
    summary_text TEXT NOT NULL,
    embedding vector(768)
);

CREATE INDEX IF NOT EXISTS idx_game_summaries_embedding 
ON game_summaries USING ivfflat (embedding vector_cosine_ops)
WITH (lists = 100);

CREATE TABLE IF NOT EXISTS player_stats (
    username TEXT PRIMARY KEY,
    total_games INTEGER DEFAULT 0,
    wins INTEGER DEFAULT 0,
    losses INTEGER DEFAULT 0,
    draws INTEGER DEFAULT 0,
    avg_cpl REAL DEFAULT 0,
    
    -- Dimensional stats stored as JSON
    stats_by_color TEXT,        -- JSON: {"white": {...}, "black": {...}}
    stats_by_time_class TEXT,   -- JSON: {"bullet": {...}, "blitz": {...}, ...}
    stats_by_opening TEXT,      -- JSON: {"B20": {...}, "C50": {...}, ...}
    stats_by_rating_band TEXT,  -- JSON: {"<1000": {...}, "1000-1200": {...}, ...}
    stats_by_termination TEXT,  -- JSON: {"checkmate": 10, "resignation": 20, ...}
    
    -- Period-based stats for trend analysis
    last_30_days TEXT,          -- JSON: PeriodStats
    last_90_days TEXT,          -- JSON: PeriodStats
    
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
`

func (db *DB) Migrate(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, Schema)
	if err != nil {
		return fmt.Errorf("failed to run migration: %w", err)
	}
	return nil
}

// func (db *DB) DropAllTables(ctx context.Context) error {
// 	query := `
// 		DROP TABLE IF EXISTS game_summaries CASCADE;
// 		DROP TABLE IF EXISTS moves CASCADE;
// 		DROP TABLE IF EXISTS games CASCADE;
// 	`
// 	_, err := db.pool.Exec(ctx, query)
// 	if err != nil {
// 		return fmt.Errorf("failed to drop tables: %w", err)
// 	}
// 	return nil
// }
