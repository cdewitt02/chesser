package config

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/chesser/internal/db"
	"github.com/chesser/internal/llm"
)

// Preflight runs an adapter's reachability, credential, and model checks.
//
// An inconclusive result — a models endpoint a gateway does not implement, say
// — is written to warn and swallowed: the real call will report the truth, and
// blocking startup over an auxiliary call would break valid setups. Anything
// else is fatal.
func Preflight(ctx context.Context, warn io.Writer, models ...any) error {
	for _, m := range models {
		p, ok := m.(llm.Preflighter)
		if !ok {
			continue
		}
		err := p.Preflight(ctx)
		switch {
		case err == nil:
		case errors.Is(err, llm.ErrPreflightInconclusive):
			fmt.Fprintf(warn, "Warning: startup check skipped: %v\n", err)
		default:
			return err
		}
	}
	return nil
}

// CheckIndex verifies that the configured embedder matches the index it will
// query or extend.
//
// Two distinct failures hide here. The vector(N) column width is the obvious
// one. The subtler one is provenance: two 768-dimension models from different
// providers pass a width check while producing vectors in unrelated spaces, so
// cosine distance across them is meaningless and retrieval degrades without
// erroring.
//
// adopt records the current embedder when the index carries no stamp yet —
// which is every index built before provenance existed. Callers that write to
// the index (ingestion) adopt; read-only callers (chat) do not.
func CheckIndex(ctx context.Context, database *db.DB, embedder llm.Embedder, adopt bool, warn io.Writer) error {
	dims := embedder.Dimensions()
	if dims > 0 {
		columnDims, err := database.EmbeddingDimensions(ctx)
		if err != nil {
			fmt.Fprintf(warn, "Warning: could not read the embedding column width: %v\n", err)
		} else if columnDims > 0 && columnDims != dims {
			return fmt.Errorf(
				"embedding width mismatch: %s/%s produces %d dimensions but game_summaries.embedding is vector(%d)",
				embedder.Name(), embedder.Model(), dims, columnDims)
		}
	}

	meta, err := database.GetIndexMeta(ctx)
	if err != nil {
		fmt.Fprintf(warn, "Warning: could not read index provenance: %v\n", err)
		return nil
	}

	if meta == nil {
		if !adopt {
			return nil
		}
		return database.SetIndexMeta(ctx, &db.IndexMeta{
			EmbedProvider: embedder.Name(),
			EmbedModel:    embedder.Model(),
			Dimensions:    dims,
		})
	}

	if meta.EmbedProvider != embedder.Name() || meta.EmbedModel != embedder.Model() {
		return fmt.Errorf(
			"embedding provider mismatch: the index was built with %s/%s but the configured embedder is %s/%s. "+
				"Vectors from different models are not comparable even at the same width. "+
				"Either restore EMBED_PROVIDER=%s and EMBED_MODEL=%s, or re-embed with: go run ./cmd/data reembed",
			meta.EmbedProvider, meta.EmbedModel, embedder.Name(), embedder.Model(),
			meta.EmbedProvider, meta.EmbedModel)
	}
	return nil
}
