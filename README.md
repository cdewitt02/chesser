# Chesser

A chess game analysis system that lets you chat with your Chess.com games using RAG (Retrieval-Augmented Generation).

## Prerequisites

- **Go 1.24+**
- **PostgreSQL** with [pgvector](https://github.com/pgvector/pgvector) extension
- **Ollama** running locally (for embeddings and chat)
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
go run cmd/data/main.go <username> <year> <month>

# Example: analyze January 2024 games
go run cmd/data/main.go magnus 2024 01
```

### 5. Chat with your games

```bash
go run cmd/chat/main.go <username> [model]

# Example
go run cmd/chat/main.go magnus llama3.2
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | *required* |
| `OLLAMA_URL` | Ollama server URL | `http://localhost:11434` |
| `OLLAMA_EMBED_MODEL` | Embedding model | `nomic-embed-text` |
| `NUM_WORKERS` | Parallel analysis workers | `4` |

## Project Structure

```
cmd/
  chat/     # Interactive chat interface
  data/     # Data ingestion and analysis pipeline
internal/
  api/      # Chess.com API client
  chat/     # Chat service and prompts
  db/       # PostgreSQL/pgvector operations
  embeddings/ # Ollama embeddings client
  engine/   # Stockfish UCI wrapper
  models/   # Domain types
  search/   # Query parsing and filters
  summary/  # Game summary generation
```
