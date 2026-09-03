# Chesser

Chat with your own Chess.com games. Chesser analyzes every move with Stockfish,
writes a summary of each game, and answers questions about your play grounded in
what actually happened on the board — not in generic chess advice.

```console
$ chesser chat cdew4

Chess Coach Chat
================
Analyzing games for: cdew4
Chat:       ollama / llama3.2
Embeddings: ollama / nomic-embed-text

Ask questions about your chess games.
Commands: /clear (reset conversation), exit/quit (leave)

You: Which time control is my best, and why?
  Thinking...

Rapid, clearly — and the gap is bigger than most players expect.

  Rapid    4 games    75.0% win rate    37.2 avg CPL
  Blitz   32 games    34.4% win rate    98.9 avg CPL
  Bullet  38 games    26.3% win rate   242.6 avg CPL

Your rapid centipawn loss is 37, which is genuinely strong. In bullet it is
243 — nearly seven times worse. That is not a different opponent pool, it is
the same player with less time to see.

The tell is where the mistakes land: your bullet games show blunders spread
through the middlegame, while your rapid games mostly stay clean until the
endgame. You are not losing bullet games on preparation. You are losing them
on calculation you do not have time to do.

If the goal is rating, play more rapid. If the goal is improvement, your
bullet games are the cheapest source of tactical mistakes to review.

You: exit
Goodbye!
```

That answer is built from 74 real analyzed games. Every number in it comes from
your own database.

## Prerequisites

- **Docker** (recommended) or **PostgreSQL** with the
  [pgvector](https://github.com/pgvector/pgvector) extension
- **Python 3.11+**
- **Stockfish** on your `PATH`
- **Ollama** running locally — needed unless you point *both* chat and
  embeddings at a hosted provider (see [Providers](#providers))

## Quick Start

### 1. Start the database

```bash
docker compose up -d
```

That is the whole database setup: the image ships pgvector, and the extension is
enabled on first start. Chesser creates its own tables when you first run it.

Already running PostgreSQL on port 5432? Pick another host port:

```bash
CHESSER_DB_PORT=5433 docker compose up -d
```

The port then has to match in `DATABASE_URL` at step 4 as well — the container
and chesser are configured independently, and nothing checks that they agree.
Setting `CHESSER_DB_PORT` in `.env` instead of on the command line keeps them in
step: `docker compose` reads that file, and `.env.example` builds `DATABASE_URL`
from the same variable.

<details>
<summary>Using your own PostgreSQL instead</summary>

```bash
psql -c "CREATE DATABASE chesser;"
psql -d chesser -c "CREATE EXTENSION vector;"
```

You will need pgvector installed, which often means building it from source
against your local PostgreSQL headers. The container exists to avoid exactly
this step.
</details>

### 2. Install chesser

```bash
uv tool install .          # or: pipx install .
```

<details>
<summary>Development install</summary>

```bash
uv venv && uv pip install -e ".[dev]"
```
</details>

### 3. Pull the models

```bash
ollama pull nomic-embed-text   # embeddings
ollama pull llama3.2           # chat
```

Roughly 1.5 GB, and the slowest step on a cold machine. You can skip it entirely
by using hosted providers for both roles — see [Providers](#providers).

### 4. Point chesser at the database

```bash
export DATABASE_URL="postgres://chesser:chesser@localhost:5432/chesser"
```

That URL matches `docker-compose.yml`. If you brought your own PostgreSQL, use
its credentials instead.

### 5. Analyze a month of games

```bash
chesser data analyze <username> <year> <month>

# Example
chesser data analyze cdew4 2026 01
```

**The month needs its leading zero** — `01`, not `1`. Expect roughly a second
per game: every move is searched twice by Stockfish, which dominates the run.

### 6. Ask it something

```bash
chesser chat <username> [chat-model]

# Example
chesser chat cdew4
```

The optional positional model is passed to whichever chat provider is selected,
and it outranks `CHAT_MODEL` — so pass a model that provider actually offers.

Good opening questions: *"What openings do I lose with most often?"*,
*"Show me games where I threw a winning position"*, *"What should I study to
improve fastest?"*

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

Opponents' usernames are **not** sent. Chess.com's termination strings embed the
winner's handle ("Bolzman0 won by resignation"), so those are normalized to the
outcome and method — "lost by resignation" — before they reach the prompt or a
stored summary. Anything in an unrecognized format collapses to "lost by other
means" rather than being passed through.

If you ingested games before this change, run `chesser data refresh-stats
<username>` to rebuild the aggregates. Summaries written earlier still carry the
old text until they are regenerated.

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
testdata/golden/    # Regression suite frozen at the cutover — see its MANIFEST.md
docker-compose.yml  # Postgres + pgvector for local development
```

## Troubleshooting

**`Error: invalid month '1': expected 01-12, with the leading zero`**
Chess.com's URL needs two digits. Use `01`, not `1`. Rejected before any network
request, so nothing is wasted.

**`no games found for user "x" in 2026/01 — Chess.com returned 404`**
Either the username is misspelled or that player has no games archived for that
month. A 404 cannot tell those apart, which is why the message names both.

**`Error: embedding provider mismatch: the index was built with ollama/...`**
Working as designed, not a bug. Vectors from different embedding models are not
comparable even at the same width, so retrieval would silently degrade rather
than fail. Either restore the previous `EMBED_PROVIDER` and `EMBED_MODEL`, or
re-embed:

```bash
chesser data reembed
```

That reads stored summary text and rebuilds vectors — no Stockfish, no
re-analysis, so it is fast.

**`Error: could not connect to the database after 30s`**
The line under it is what PostgreSQL actually said, and it separates the two
cases:

- *Connection refused* — nothing is listening. Check `docker compose ps`; a
  container that exited leaves the port empty.
- *password authentication failed* — something answered, but it is not the
  chesser container. Usually a native PostgreSQL already holds 5432, which also
  stopped `docker compose up` from binding it. Publish another port and point
  `DATABASE_URL` at it:

  ```bash
  CHESSER_DB_PORT=5433 docker compose up -d
  export DATABASE_URL="postgres://chesser:chesser@localhost:5433/chesser"
  ```

Startup waits the full 30 seconds before giving up, because `docker compose up
-d` returns before PostgreSQL accepts connections — a first run that races a
cold container retries rather than failing.

**`Error: DATABASE_URL contains a control character`**, or **`invalid hostPort`**
with nothing visible after the number
The env file has Windows (CRLF) line endings. Sourcing it appends a carriage
return to *every* value — including your API keys — and, because `DATABASE_URL`
is built from `CHESSER_DB_PORT`, the CR lands in the middle of the connection
string rather than at the end. Convert the file once:

```bash
sed -i 's/\r$//' .env      # or: dos2unix .env
```

`docker compose` reads that same file without complaining, because it trims
whitespace from values and a shell does not. Compose can therefore be perfectly
happy while every sourced variable is subtly wrong, so check the shell rather
than the file:

```bash
printf '%q\n' "$CHESSER_DB_PORT"    # a bare number, or $'5433\r'?
```

If the file keeps reverting to CRLF — an editor on another machine, usually —
source it through a filter instead of repeatedly fixing it:

```bash
. <(tr -d '\r' < .env)
```

**`invalid hostPort: 5433`** from `docker compose config` or `up`
`CHESSER_DB_PORT` has a stray character — a trailing space, or a zero-width
space picked up by copy-paste. Compose strips whitespace from values in the env
file but *not* from the shell environment, so an exported variable is the usual
culprit and the error prints nothing visible after the number. Show it exactly:

```bash
printf '%q\n' "$CHESSER_DB_PORT"
```

Anything other than a bare number is the problem. `unset CHESSER_DB_PORT` and
set it again, or set it in the env file instead, where the whitespace is
tolerated.

**`FATAL:  database "chesser" does not exist`**
The container is running and the credentials work — this is not a connection
problem. The PostgreSQL image runs `POSTGRES_DB` *and* `docker/init-pgvector.sql`
only on a first start with an empty data directory. A first `docker compose up`
that was interrupted leaves the volume non-empty but incomplete, and from then
on every start skips initialization: restarting, re-running `up`, and changing
the port all leave it exactly as broken.

Discard the volume so it initializes again. **This deletes any games you have
already analyzed** — re-run `chesser data analyze` afterwards:

```bash
docker compose down -v
docker compose up -d
```

`down -v` is what distinguishes this from `down`, which keeps the volume and so
changes nothing.

**`failed to bind host port 0.0.0.0:5432: address already in use`**
Something else — usually a native PostgreSQL — already holds the port.

```bash
CHESSER_DB_PORT=5433 docker compose up -d
export DATABASE_URL="postgres://chesser:chesser@localhost:5433/chesser"
```

**`Error: DATABASE_URL environment variable is required`**
Nothing loads `.env` automatically. Source it, or export the variable:

```bash
. ./.env
```

Note that sourcing it *overwrites* a `DATABASE_URL` you exported by hand, which
is the usual reason a port change appears not to take. Check what is actually in
effect rather than what you last typed:

```bash
echo "$DATABASE_URL"
docker compose ps    # PORTS names the published one
```

**Ollama connection refused, or a model 404**
Check `ollama list` — the chat model and `nomic-embed-text` both need pulling.
Startup checks run before the banner, so this surfaces immediately rather than
after your first question.

**`exec: "stockfish": executable file not found`**
Stockfish must be on `PATH`. It is only needed for `chesser data analyze`, not
for chatting, so this appears during ingestion.

**Missing API key for a hosted provider**
`ANTHROPIC_API_KEY` for `CHAT_PROVIDER=anthropic`, `OPENAI_API_KEY` for either
role set to `openai`. Resolved before the welcome banner, so an auth failure is
never revealed only after your first question.

**Ingestion is slow**
Expect roughly a second per game — every move is searched twice by Stockfish,
which dominates the run. `NUM_WORKERS` (default 4) is the knob; each worker
spawns its own Stockfish process, so raising it past your core count will not
help.

**Something else**
Error output is redacted for passwords and API keys before printing, so it is
safe to paste into an issue. Please include the environment the bug template
asks for — most reports are unanswerable without it.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) — the short
version is that `ruff check`, `mypy --strict`, and `pytest` must all pass, and
the corpus-backed tests need a database.

If you are looking for somewhere to start, `docs/opensource-readiness/01-roadmap.md`
lists the outstanding work with the reasoning behind each item.

## License

MIT — see [LICENSE](LICENSE).
