# Chesser

A chess game analysis system that lets you chat with your Chess.com games using RAG (Retrieval-Augmented Generation).

## Prerequisites

- **Python 3.11+**
- **PostgreSQL** with [pgvector](https://github.com/pgvector/pgvector) extension
- **Ollama** running locally — needed unless you select a hosted provider for
  *both* chat and embeddings (see [Providers](#providers))
- **Stockfish** installed and available in PATH

## Quick Start

### 1. Install

```bash
uv tool install .          # or: pipx install .
```

For development, an editable install in a virtualenv:

```bash
uv venv && uv pip install -e ".[dev]"
```

### 2. Set up the database

```bash
# Create database and enable pgvector
psql -c "CREATE DATABASE chesser;"
psql -d chesser -c "CREATE EXTENSION vector;"
```

`chesser data` creates the tables itself on first run, so this is only the
database and the extension.

### 3. Pull Ollama models

```bash
ollama pull nomic-embed-text   # embeddings
ollama pull llama3.2           # chat (or your preferred model)
```

### 4. Set environment variables

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/chesser"
```

### 5. Fetch and analyze games

```bash
chesser data analyze <username> <year> <month>

# Example: analyze January 2026 games
chesser data analyze magnus 2026 01
```

The month keeps its leading zero. Expect roughly a second per game: every move
is searched twice by Stockfish, which dominates the run.

### 6. Chat with your games

```bash
chesser chat <username> [chat-model]

# Example
chesser chat magnus llama3.2
```

The optional positional model is passed to whichever chat provider is selected,
and it outranks `CHAT_MODEL` — so pass a model that provider actually offers.

## Providers

Chat and embeddings are selected **independently**, because Anthropic offers no
embeddings API. Both default to Ollama, so an existing setup keeps working
unchanged and the tool still runs with no account, no key, and no network.

| Role | Variable | Values | Default model |
|------|----------|--------|---------------|
| Chat | `CHAT_PROVIDER` | `ollama`, `anthropic`, `openai` | `llama3.2` / `claude-opus-5` / `gpt-5-2025-08-07` |
| Embeddings | `EMBED_PROVIDER` | `ollama`, `openai` | `nomic-embed-text` / `text-embedding-3-small` |

Default model IDs are **pinned, never aliased**: a server-side upgrade behind an
alias would change answers with no code change and no way to notice.

### Choosing a chat model

**Start with the default.** Several chat models were compared informally against
one real corpus and came out close enough that no provider stood out — the local
`llama3.2` default included. There is no measured quality reason to reach for a
hosted API first.

That comparison is more meaningful than it sounds, because the prompt is
deterministic: every provider receives byte-identical retrieved games,
statistics, and instructions, so the model is the only thing that differs. It is
still one person's games and one person's judgment, not a benchmark — which is
exactly why the choice is left to you rather than baked into a recommendation.

Reasons to switch that do **not** depend on answer quality:

- **Hardware.** Local inference wants RAM and CPU that a small laptop or VM may
  not have. A hosted chat provider moves that cost off your machine.
- **Speed.** Hosted models generally return faster than local inference on
  modest hardware.
- **Dropping Ollama entirely.** Only `EMBED_PROVIDER=openai` does that, since
  Anthropic has no embeddings API — and it means re-embedding your index.

Reasons to stay local: no account, no API key, no per-query cost, and **nothing
about your games leaves the machine**.

Because chat and embeddings are selected independently, trying a different chat
model is cheap and reversible — the index is untouched, so switching back costs
nothing. Comparing on your own corpus is the only comparison that is really
about your games; `docs/multi-provider/03-eval-plan.md` describes how to hold
everything else constant if you want to do it properly.

### Using Anthropic for chat

```bash
export ANTHROPIC_API_KEY="..."     # never committed; .env is gitignored
export CHAT_PROVIDER=anthropic
chesser chat magnus
```

Embeddings stay on Ollama, so **no re-embedding is needed** and the existing
index keeps working — which is also what makes "same index, different chat
model" an honest comparison. Ollama is still a prerequisite for the embedding
half.

**This sends data off-machine.** Selecting a hosted provider sends your game
summaries and Chess.com username to a third party. The startup banner says so
whenever a hosted provider is active.

> **Known gap.** It also sends *other players'* usernames. Chess.com's
> termination strings embed the opponent's handle ("Bolzman0 won by
> resignation"), and those reach the prompt verbatim in the "Game endings"
> section. See [readiness P0-8](docs/opensource-readiness/01-roadmap.md).

Failures are reported, never silently retried on another provider: an Anthropic
error answered from `llama3.2` would leave you comparing outputs without knowing
which model produced which.

### Using OpenAI

OpenAI is the only provider that serves both halves, so it is the one that can
take Ollama off the prerequisite list entirely:

```bash
export OPENAI_API_KEY="..."
export CHAT_PROVIDER=openai
export EMBED_PROVIDER=openai
chesser data analyze magnus 2026 01
chesser chat magnus
```

`text-embedding-3-small` is asked for **768 dimensions**, which is the width the
`game_summaries` column already declares — so no schema migration is involved.
It is still a *different* embedding model: vectors from two models are not
comparable even at the same width, so switching embed providers on an existing
index is refused at startup, naming the re-embed path (`chesser data reembed`).

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | *required* |
| `CHAT_PROVIDER` | Chat provider: `ollama`, `anthropic`, or `openai` | `ollama` |
| `CHAT_MODEL` | Chat model (positional CLI arg outranks it) | per provider |
| `EMBED_PROVIDER` | Embedding provider: `ollama` or `openai` | `ollama` |
| `EMBED_MODEL` | Embedding model — must emit **768 dimensions** | per provider |
| `ANTHROPIC_API_KEY` | Required when `CHAT_PROVIDER=anthropic` | — |
| `OPENAI_API_KEY` | Required when either provider is `openai` | — |
| `OLLAMA_URL` | Ollama server URL (honored by both subcommands) | `http://localhost:11434` |
| `OLLAMA_EMBED_MODEL` | Alias for `EMBED_MODEL` when the embed provider is Ollama | `nomic-embed-text` |
| `NUM_WORKERS` | Parallel analysis workers | `4` |
| `NO_COLOR` | Set to any value to print raw markdown instead of styled output | — |
| `CHESSER_DEBUG_PROMPT` | Set to any value to dump the assembled prompt to stderr | — |

Credentials come from the environment only, under the provider-standard names.
See `.env.example`.

### Terminal output

The coach answers in markdown, and `chesser chat` renders it in place: headings,
bullets, tables, and fenced code blocks are styled to the terminal's width. The
reply streams in as plain text while the model works, then is repainted once as
the finished document — markdown cannot be laid out incrementally, because
wrapping and table widths are properties of the whole answer.

Styling is skipped, and the raw markdown printed instead, whenever stdout is not
a terminal, `NO_COLOR` is set, or `TERM` reports a terminal that cannot render
it. So `chesser chat magnus > notes.md` captures clean markdown rather than
escape codes.

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
chesser data reembed
```

## Project Structure

```
chesser/
  api.py       # Chess.com API client
  cli.py       # The `chesser` command: data and chat subcommands
  config.py    # Provider selection and startup validation
  engine.py    # Stockfish analysis via python-chess
  ingest.py    # The analysis worker pool
  repl.py      # Terminal chat loop
  summary.py   # Game Summary generation
  chat/        # Query classification, prompt assembly, chat service
  db/          # PostgreSQL/pgvector operations
  llm/         # Provider-neutral chat and embedding protocols
    anthropic.py # Anthropic adapter (chat only)
    ollama.py    # Ollama adapter (chat + embeddings)
    openai.py    # OpenAI adapter (chat + embeddings)
  models/      # Domain types
  search/      # Query parsing, filters, hybrid retrieval
tests/
testdata/golden/  # Regression suite frozen at the cutover — see its MANIFEST.md
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). The short version: `ruff check`,
`mypy --strict`, and `pytest` must all pass, and the corpus-backed tests need a
database.
