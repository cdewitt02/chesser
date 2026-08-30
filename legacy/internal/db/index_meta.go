package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/pgvector/pgvector-go"
)

// IndexMeta records which embedder produced the vectors in game_summaries.
// Width alone cannot catch a provider change: two 768-dimension models occupy
// different vector spaces, so mixing them degrades retrieval silently.
type IndexMeta struct {
	EmbedProvider string
	EmbedModel    string
	Dimensions    int
}

// GetIndexMeta returns the recorded provenance, or (nil, nil) when the index
// carries no stamp — either because it predates provenance or because the
// table does not exist yet (cmd/chat never runs migrations).
func (db *DB) GetIndexMeta(ctx context.Context) (*IndexMeta, error) {
	var meta IndexMeta
	err := db.pool.QueryRow(ctx,
		`SELECT embed_provider, embed_model, dimensions FROM index_meta WHERE id = 1`,
	).Scan(&meta.EmbedProvider, &meta.EmbedModel, &meta.Dimensions)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case isUndefinedTable(err):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("failed to read index metadata: %w", err)
	}
	return &meta, nil
}

func (db *DB) SetIndexMeta(ctx context.Context, meta *IndexMeta) error {
	_, err := db.pool.Exec(ctx, `
		INSERT INTO index_meta (id, embed_provider, embed_model, dimensions, updated_at)
		VALUES (1, $1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			embed_provider = EXCLUDED.embed_provider,
			embed_model    = EXCLUDED.embed_model,
			dimensions     = EXCLUDED.dimensions,
			updated_at     = NOW()
	`, meta.EmbedProvider, meta.EmbedModel, meta.Dimensions)
	if err != nil {
		return fmt.Errorf("failed to record index metadata: %w", err)
	}
	return nil
}

// EmbeddingDimensions reports the declared width of game_summaries.embedding,
// so a mismatch is a startup message rather than a mid-ingestion insert error.
func (db *DB) EmbeddingDimensions(ctx context.Context) (int, error) {
	var dims int
	err := db.pool.QueryRow(ctx, `
		SELECT a.atttypmod
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		WHERE c.relname = 'game_summaries' AND a.attname = 'embedding'
	`).Scan(&dims)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("failed to read embedding column width: %w", err)
	}
	if dims < 0 {
		return 0, nil // unconstrained vector column
	}
	return dims, nil
}

// SummaryTextRow is one stored summary awaiting a fresh vector.
type SummaryTextRow struct {
	GameUUID    string
	SummaryText string
}

// AllSummaryTexts returns every stored summary. Re-embedding is bounded work:
// summaries are generated deterministically with no LLM and no Stockfish, so a
// provider swap reads stored text and updates vectors rather than re-running
// analysis.
func (db *DB) AllSummaryTexts(ctx context.Context) ([]SummaryTextRow, error) {
	rows, err := db.pool.Query(ctx, `SELECT game_uuid, summary_text FROM game_summaries`)
	if err != nil {
		return nil, fmt.Errorf("failed to query summaries: %w", err)
	}
	defer rows.Close()

	var out []SummaryTextRow
	for rows.Next() {
		var r SummaryTextRow
		if err := rows.Scan(&r.GameUUID, &r.SummaryText); err != nil {
			return nil, fmt.Errorf("failed to scan summary: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateSummaryEmbedding replaces the vector for one stored summary.
func (db *DB) UpdateSummaryEmbedding(ctx context.Context, gameUUID string, embedding []float32) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE game_summaries SET embedding = $2 WHERE game_uuid = $1`,
		gameUUID, pgvector.NewVector(embedding))
	if err != nil {
		return fmt.Errorf("failed to update embedding for %s: %w", gameUUID, err)
	}
	return nil
}

func isUndefinedTable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P01"
}
