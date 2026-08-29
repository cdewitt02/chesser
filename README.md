# Chesser

A chess game analysis system that lets you chat with your Chess.com games using RAG (Retrieval-Augmented Generation).

## Prerequisites

- **Go 1.24+**
- **PostgreSQL** with [pgvector](https://github.com/pgvector/pgvector) extension
- **Ollama** running locally (embeddings always; chat unless you select a hosted provider)
- **Stockfish** installed and available in PATH

## Quick Start

### 1. Set up the database

```bash
# Create database and enable pgvector
psql -c "CREATE DATABASE chesser;"
psql -d chesser -c "CREATE EXTENSION vector;"
```

### 2. Pull Ollama models

```bash
ollama pull nomic-embed-text   # embeddings
ollama pull llama3.2           # chat (or your preferred model)
```

### 3. Set environment variables

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/chesser"
```

### 4. Fetch and analyze games

```bash
go run ./cmd/data analyze <username> <year> <month>

# Example: analyze January 2026 games
go run ./cmd/data analyze magnus 2026 01
```

### 5. Chat with your games

```bash
go run cmd/chat/main.go <username> [chat-model]

# Example
go run cmd/chat/main.go magnus llama3.2
```

The optional positional model is passed to whichever chat provider is selected,
and it outranks `CHAT_MODEL` — so pass a model that provider actually offers.

## Providers

Chat and embeddings are selected **independently**, because Anthropic offers no
embeddings API. Both default to Ollama, so an existing setup keeps working
unchanged and the tool still runs with no account, no key, and no network.

| Role | Variable | Values | Default model |
|------|----------|--------|---------------|
| Chat | `CHAT_PROVIDER` | `ollama`, `anthropic` | `llama3.2` / `claude-opus-5` |
| Embeddings | `EMBED_PROVIDER` | `ollama` | `nomic-embed-text` |

Default model IDs are **pinned, never aliased**: a server-side upgrade behind an
alias would change answers with no code change and no way to notice.

### Using Anthropic for chat

```bash
export ANTHROPIC_API_KEY="..."     # never committed; .env is gitignored
export CHAT_PROVIDER=anthropic
go run cmd/chat/main.go magnus
```

Embeddings stay on Ollama, so **no re-embedding is needed** and the existing
index keeps working — which is also what makes "same index, different chat
model" an honest comparison. Ollama is still a prerequisite for the embedding
half.

**This sends data off-machine.** Selecting a hosted provider sends your game
summaries and Chess.com username to a third party. The startup banner says so
whenever a hosted provider is active.

Failures are reported, never silently retried on another provider: an Anthropic
error answered from `llama3.2` would leave you comparing outputs without knowing
which model produced which.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | *required* |
| `CHAT_PROVIDER` | Chat provider: `ollama` or `anthropic` | `ollama` |
| `CHAT_MODEL` | Chat model (positional CLI arg outranks it) | per provider |
| `EMBED_PROVIDER` | Embedding provider: `ollama` | `ollama` |
| `EMBED_MODEL` | Embedding model — must emit **768 dimensions** | `nomic-embed-text` |
| `ANTHROPIC_API_KEY` | Required when `CHAT_PROVIDER=anthropic` | — |
| `OLLAMA_URL` | Ollama server URL (honored by both entrypoints) | `http://localhost:11434` |
| `OLLAMA_EMBED_MODEL` | Alias for `EMBED_MODEL` when the embed provider is Ollama | `nomic-embed-text` |
| `NUM_WORKERS` | Parallel analysis workers | `4` |

Credentials come from the environment only, under the provider-standard names.
See `.env.example`.

### Changing the embedding model

The `game_summaries.embedding` column is `vector(768)`, and the provider and
model that built the index are recorded alongside it. Two models of the same
width occupy different vector spaces, so a width check alone would pass while
retrieval silently degraded — startup refuses to run against an index built by a
different embedder instead.

Re-embedding is bounded work: summaries are generated deterministically with no
LLM and no Stockfish, so this reads stored text and updates vectors rather than
re-running analysis.

```bash
go run ./cmd/data reembed
```

## Project Structure

```
cmd/
  chat/     # Interactive chat interface
  data/     # Data ingestion and analysis pipeline
internal/
  api/      # Chess.com API client
  chat/     # Chat service and prompts
  config/   # Provider selection and startup validation
  db/       # PostgreSQL/pgvector operations
  engine/   # Stockfish UCI wrapper
  llm/      # Provider-neutral chat and embedding interfaces
    anthropic/ # Anthropic adapter (chat only)
    llmtest/   # Fakes and the shared conformance suite
    ollama/    # Ollama adapter (chat + embeddings)
  models/   # Domain types
  search/   # Query parsing and filters
  summary/  # Game summary generation
```
