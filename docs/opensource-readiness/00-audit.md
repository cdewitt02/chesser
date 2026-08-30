# chesser — Repository Audit

**Date:** 2026-08-25
**Commit audited:** `2bcd4cb` (main, clean working tree)
**Scope:** read-only assessment. No application code, config, or CI was modified.

---

## How this was verified

Every finding below is backed by a command run against the tree at `2bcd4cb`. Nothing here is inferred from the README's claims alone.

| Command | Result |
|---|---|
| `go build ./...` | exit 0, no output |
| `go vet ./...` | exit 0, no output |
| `go test ./...` | `ok internal/search`; **9 packages report `[no test files]`** |
| `gofmt -l .` | **24 of 30 `.go` files listed** |
| `go list -m -u github.com/jackc/pgx/v5 github.com/notnil/chess github.com/pgvector/pgvector-go` | 2 of 3 direct deps behind |
| `grep -rn "chesser_local" .` | **no matches** |
| `git remote -v` | `https://github.com/cdewitt02/chesser.git` |
| `find . -path ./.git -prune -o -type f -print` | 30 files; no `.github/`, no `LICENSE`, no `Makefile`, no Docker files |

Codebase size: **5,329 lines of Go across 30 files**, 10 packages.

---

## 1. Documentation

### 1.1 Missing files

| File | Present? |
|---|---|
| `LICENSE` | ❌ |
| `CONTRIBUTING.md` | ❌ |
| `CODE_OF_CONDUCT.md` | ❌ |
| `SECURITY.md` | ❌ |
| Architecture / design docs | ❌ |
| `CHANGELOG.md` | ❌ |

`README.md` (1,897 bytes) is the only documentation in the repository.

**The absent LICENSE is the most consequential.** Without one, the repository is "all rights reserved" by default under copyright law. No one can legally fork, modify, or redistribute it, regardless of the fact that it is publicly visible on GitHub. This blocks outside contribution entirely — not as a matter of convention, but as a matter of law.

### 1.2 README accuracy — checked against the code, not taken at face value

Four discrepancies, one of them fatal to the documented workflow.

**(a) Step 4 does not work.** `README.md` instructs:

```bash
go run cmd/data/main.go <username> <year> <month>
```

Two independent reasons this fails:

1. **It cannot compile.** `NewWorkerPool`, `Worker`, and `WorkerPool` live in `cmd/data/worker.go` (`cmd/data/worker.go:104`), a second file in `package main`. Naming a single `.go` file in `go run` compiles only that file, so `cmd/data/main.go:259` — `pool := NewWorkerPool(...)` — fails with `undefined: NewWorkerPool`.
2. **The argument shape is wrong.** `main()` at `cmd/data/main.go:115-132` dispatches on a **subcommand**:

   ```go
   command := os.Args[1]
   switch command {
   case "analyze":        runAnalyze()
   case "refresh-stats":  runRefreshStats()
   default:               printUsage(); os.Exit(1)
   }
   ```

   `runAnalyze` then requires `len(os.Args) == 5` (`cmd/data/main.go:198`).

The correct invocation, per the program's own `printUsage()` at `cmd/data/main.go:134-138`:

```bash
go run ./cmd/data analyze <username> <year> <month>
```

A new contributor following the README verbatim hits a compile error at the first step that actually does anything. Note the program's built-in usage text is *correct* — only the README is wrong.

**(b) The `refresh-stats` subcommand is undocumented.** `cmd/data/main.go:140-195` implements a full stats-recomputation command with formatted output by color, time class, and termination. It appears nowhere in the README.

**(c) `NUM_WORKERS` default is documented incorrectly.** README's env table says `8 (4 for less compute)`. The code (`cmd/data/main.go:106-113`) returns `4`:

```go
func getNumWorkers() int {
	if val := os.Getenv("NUM_WORKERS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 { return n }
	}
	return 4 // default
}
```

The parenthetical also reads as a recommendation rather than a value, which is ambiguous even setting aside the wrong number.

**(d) `OLLAMA_URL` and `OLLAMA_EMBED_MODEL` are silently ignored by the ingestion pipeline.** The README documents both as global environment variables. They are honored only in the chat entrypoint (`cmd/chat/main.go:52-60`). The data pipeline **hardcodes both** at `cmd/data/main.go:251`:

```go
embeddingClient := embeddings.New("http://localhost:11434", "nomic-embed-text")
```

A contributor running Ollama on a different host or port sets `OLLAMA_URL`, watches ingestion fail to connect, and has no signal that their configuration was discarded. This is worse than an undocumented variable: the documentation actively misleads.

### 1.3 Undocumented hard constraint: 768-dimension embeddings

`internal/db/schema.go:68` fixes the vector width in DDL:

```sql
embedding vector(768)
```

`nomic-embed-text` happens to emit 768 dimensions, so the default works. But the README presents `OLLAMA_EMBED_MODEL` as freely configurable with no mention of this coupling. Setting it to, say, `mxbai-embed-large` (1024 dims) produces a Postgres dimension-mismatch error at insert time, deep inside a worker goroutine — long after ingestion has already spent minutes on Stockfish analysis. The constraint needs to be stated in the env table.

### 1.4 What the README does well

Prerequisites are listed, the project-structure tree is accurate against the actual layout, and the env-var table is the right format. The structure is sound; the contents have drifted from the code.

---

## 2. Testing

### 2.1 Coverage per package

| Package | LOC | Test file | Status |
|---|---:|---|---|
| `cmd/chat` | 141 | — | no tests |
| `cmd/data` | 483 | — | no tests |
| `internal/api` | 37 | — | no tests |
| `internal/chat` | 1,202 | — | no tests |
| `internal/db` | 1,120 | — | no tests |
| `internal/embeddings` | 130 | — | no tests |
| `internal/engine` | 171 | — | no tests |
| `internal/models` | 250 | — | no tests |
| `internal/search` | 857 | `parser_test.go` (500 lines) | ✅ only tested package |
| `internal/summary` | 228 | — | no tests |

One test file in the entire repository. `internal/search/parser_test.go` is genuinely good — table-driven, 500 lines — which makes the contrast sharper: the author knows how to write Go tests, so the gap is about testability of the other packages, not skill.

### 2.2 Why `internal/search` is the tested package — and the others aren't

This is the central testing finding. `internal/search/search.go:8-15` defines dependency interfaces:

```go
type EmbeddingClient interface {
	GetEmbedding(text string) ([]float32, error)
}

type GameSearcher interface {
	FindSimilarGamesWithFilters(ctx context.Context, queryEmbedding []float32, filters *GameFilters, limit int) ([]*SimilarGameResult, error)
	CountGamesMatchingFilters(ctx context.Context, filters *GameFilters) (int, error)
}
```

`internal/chat/service.go:191-222` supplies the concrete implementation through `dbSearchAdapter`. This is textbook dependency inversion, and it is precisely why `internal/search` could be tested without Postgres or Ollama running.

**No other package does this.** The consequences, package by package:

- **`internal/engine`** — `AnalyzeGame(engine *uci.Engine, pgnString string, depth int)` (`internal/engine/stockfish.go:87`) takes a concrete `*uci.Engine` from a third-party library. Nothing in the package can execute without a real Stockfish binary on PATH. This matters because `internal/engine` contains genuinely subtle logic that *deserves* tests: `normalizeEval` flips sign by move parity (`stockfish.go:65-70`), `getEvaluation` encodes mate scores as ±10000 offset by `MateIn` (`stockfish.go:54-63`), and `AnalyzeGame` computes centipawn loss with opposite subtraction order for White and Black (`stockfish.go:132-141`). Off-by-one or sign errors in any of these would silently corrupt every stored analysis, and nothing would catch it.
- **`internal/embeddings`** — `Client` is a concrete struct with an embedded `*http.Client` (`ollama.go:13-17`). No interface, no injectable transport, so no `httptest` seam without refactoring.
- **`internal/api`** — `GetData` calls the package-level `http.Get` directly (`data.go:18`) with a hardcoded URL. Not stubbable at all.
- **`internal/db`** — concrete `*pgxpool.Pool` (`db.go:11-13`). Needs a live Postgres.
- **`internal/summary`** — **the exception worth flagging.** `ExtractSummaryData` and `GenerateSummary` (`generator.go:39`, `generator.go:144`) are pure functions over plain structs with **zero external dependencies**. `weakestPhase`, `detectPattern`, and `classifyGameLength` are pure and branch-heavy — `detectPattern` alone has 10 distinct outcomes across a won/lost/drew × wasWinning/wasLosing matrix (`generator.go:194-228`). This package is testable **today, with no refactoring whatsoever**, and is entirely untested. It is the single cheapest coverage win in the repo.

### 2.3 No CI-runnable test story

There is no way to run a meaningful test suite in an environment lacking Postgres, Ollama, and Stockfish. Right now that limitation is masked, because only `internal/search` has tests and it happens not to need them. The moment anyone tries to add tests to `db` or `engine`, this becomes a blocking design question.

---

## 3. CI/CD

**Nothing exists.** No `.github/` directory of any kind.

| Item | Status |
|---|---|
| `.github/workflows/` | ❌ absent |
| Build on PR | ❌ |
| `go vet` on PR | ❌ |
| Lint (`golangci-lint`, `staticcheck`) | ❌ not configured, not installed |
| Test execution on PR | ❌ |
| `gofmt` check | ❌ |
| `govulncheck` | ❌ not configured, not installed |
| Dependabot / Renovate | ❌ |
| `CODEOWNERS` | ❌ |
| Release automation | ❌ |

Every quality property of the repo is currently maintained by hand and unverified on any incoming change.

**Important sequencing note that surfaced here:** a `gofmt` gate cannot be added first. It would fail on 24 of 30 files on the very first run (see §4.2). The formatting sweep has to land before the gate, or CI is red from the moment it is introduced.

---

## 4. Dependency & build hygiene

### 4.1 Dependencies

`go.mod` declares three direct dependencies. All are pinned to concrete semantic versions; `go.sum` (75 lines) is committed. No `replace` directives, no pseudo-versions among direct deps, no vendored tree. This part is healthy.

| Dependency | Current | Latest | Gap |
|---|---|---|---|
| `github.com/jackc/pgx/v5` | v5.8.0 | v5.10.0 | 2 minor |
| `github.com/pgvector/pgvector-go` | v0.3.0 | v0.4.1 | 1 minor |
| `github.com/notnil/chess` | v1.10.0 | v1.10.0 | ✅ current |

Both gaps are minor-version and low-risk, but neither has been evaluated for security content — `govulncheck` has never been run against this module.

Go directive: `go 1.24.0`, `toolchain go1.24.12`. Current and consistent.

### 4.2 Formatting — 24 of 30 files fail `gofmt`

This is the largest mechanical debt in the repo. Representative causes:

- **Spaces instead of tabs for indentation.** `internal/embeddings/ollama.go:13-43` — the entire block of type declarations (`Client`, `embeddingRequest`, `embeddingResponse`, `ChatMessage`, `chatRequest`, `chatResponse`) is space-indented, while the function bodies below it use tabs. Same pattern at `internal/chat/service.go:170`.
- **Trailing whitespace.** `cmd/data/main.go:169`, `:172`, `:177`, `:184`, `:277`; `internal/engine/stockfish.go:66`, `:111`; `cmd/data/worker.go:145`, `:171`; `internal/summary/generator.go:140`.
- **Missing newline at end of file.** `internal/models/position.go`, `internal/models/game.go`.
- **Misaligned struct field blocks.** `cmd/chat/main.go:21-22`, `internal/chat/service.go:22-23`.

None of this affects behavior. All of it is fixed by a single `gofmt -w .`. The significance is procedural: it must happen before any CI format gate and before outside PRs start arriving, or every subsequent diff carries formatting noise and every PR conflicts.

### 4.3 No task runner

No `Makefile`, no `Taskfile.yml`, no `justfile`, no scripts directory. Every command a contributor needs — build, test, vet, the two multi-argument CLI invocations — must be typed from memory or copied from a README that is currently wrong about one of them.

### 4.4 Dead code

`cmd/data/main.go:17-20`:

```go
// Struct to parse the test data JSON
type TestData struct {
	Games []models.Game `json:"games"`
}
```

Unreferenced anywhere in the tree. A leftover from the `test_data/` workflow that `.gitignore` still lists. `go vet` does not flag unused types, which is why it survives a clean vet.

---

## 5. Onboarding friction

### 5.1 The actual cold-start path

For a contributor with none of the toolchain installed:

| # | Step | Time | Friction |
|---|---|---|---|
| 1 | Install Go 1.24+ | 5 min | low |
| 2 | Install PostgreSQL | 10 min | moderate; platform-dependent |
| 3 | Install pgvector | 10–20 min | **high** — often requires building from source against local PG headers; the README links the project but gives no install command |
| 4 | `CREATE DATABASE` + `CREATE EXTENSION` | 2 min | low, and documented |
| 5 | Install Ollama | 5 min | low |
| 6 | `ollama pull` two models | 10–20 min | ~1.5 GB+ download, network-bound |
| 7 | Install Stockfish, ensure on PATH | 5 min | moderate; no install guidance given |
| 8 | Export `DATABASE_URL` | 1 min | low |
| 9 | Run ingestion | **fails** | README command does not compile (§1.2a) |

**Realistic total: 45–90 minutes, ending in failure** at the one step where the contributor would first see the project do something.

### 5.2 No container path

No `Dockerfile`, no `docker-compose.yml`, no `.devcontainer/`. Step 3 — pgvector — is the single worst step, and it is the one most trivially eliminated: the official `pgvector/pgvector:pg17` image ships the extension prebuilt. A ~15-line Compose file removes steps 2, 3, and 4 outright.

### 5.3 Hardware expectations undocumented

The ingestion workload is heavy and nothing says so. `ANALYSIS_DEPTH = 12` (`cmd/data/main.go:22`), and each of the 4 default workers spawns its own Stockfish process (`cmd/data/worker.go:141`), so a default run saturates 4 cores for the duration. Concurrently, Ollama holds an embedding model resident, and the chat path runs LLM inference locally. A contributor on a 2-core laptop or a small VM will find this unusably slow with no warning — and no guidance that `NUM_WORKERS` is the knob.

### 5.4 No troubleshooting section

The failure modes are known and enumerable (§7), but none are documented. Every one of them currently presents as either a bare Go error or, worse, silence.

---

## 6. Security & secrets handling

### 6.1 What is done correctly

- **Secrets come from the environment only.** `DATABASE_URL` is read at `cmd/chat/main.go:46`, `cmd/data/main.go:148`, `cmd/data/main.go:221`, and `internal/db/db.go:17`. There is no config file, no flag, no default connection string with embedded credentials.
- **`.env` is gitignored** (`.gitignore:29`).
- **Nothing sensitive is committed.** No credentials, keys, or connection strings anywhere in the tree.
- **Chat entrypoint validates presence and fails fast** with a clear message (`cmd/chat/main.go:47-50`).
- **No third-party credentials needed.** The Chess.com public API requires no key (`internal/api/data.go:16`), and Ollama is local and unauthenticated.

### 6.2 SQL injection — checked, and clean

Worth stating explicitly, because `internal/search/filters.go` builds SQL dynamically and looks alarming at a glance. It is correct. `BuildWHERE` (`filters.go:43-195`) uses `fmt.Sprintf` **only to interpolate positional placeholder numbers**, never values:

```go
addCondition(fmt.Sprintf("g.time_class = $%d", paramNum), *f.TimeClass)
```

The filter value goes into the `args` slice and reaches Postgres through pgx's parameter binding. The same discipline holds at `internal/db/summaries.go:183` (`LIMIT $N`). Every user-controlled value — including free-text opening names at `filters.go:121` — is parameterized. **No injection vector found.**

### 6.3 Connection-string exposure in error output

A narrow but real leak path. `internal/db/db.go:20-28` wraps pgx errors with `%w`:

```go
pool, err := pgxpool.New(ctx, connString)
if err != nil {
	return nil, fmt.Errorf("failed to create connection pool: %w", err)
}
```

pgx's connection-string parse errors can embed the offending string. That error is then printed unredacted to stderr at `cmd/chat/main.go:78` and to `log.Fatalf` at `cmd/data/main.go:150`. If a contributor pastes a failure into an issue while using a real password, it goes public.

Severity is moderate — it requires a malformed URL, not a merely-wrong-password one — but the fix (redact the password component before wrapping) is small, and the cost of not fixing it is borne by users rather than by the maintainer.

### 6.4 Prompt content dumped to stdout on every question

`internal/chat/service.go:111-114`, executing unconditionally in the main `Ask` path:

```go
fmt.Printf("=== QUERY TYPE: %s ===\n", qctx.QueryType)
fmt.Println("=== SYSTEM PROMPT ===")
fmt.Println(systemPrompt)
fmt.Println("=== END PROMPT ===")
```

The system prompt embeds retrieved game summaries and aggregate player statistics (assembled by `QueryRouter.BuildPrompt`, `internal/chat/router.go`). Every question the user asks dumps that entire block to the terminal before the answer appears.

Two distinct problems: it is debug instrumentation shipped in the interactive product, and it makes the tool unpleasant to screen-share or record without exposing personal game history. It also sits *after* the LLM call and *before* the return, so it interleaves confusingly with the `Thinking...` output from `cmd/chat/main.go:127`.

The right fix is a `--debug` flag or env gate, not deletion — the output is genuinely useful during RAG development.

---

## 7. Error handling & observability

### 7.1 Chess.com API — silent failure on every error status

**The most serious defect found in the audit.** `internal/api/data.go:15-36` in full:

```go
func GetData(date models.YearMonth, username string) ([]models.Game, error) {
	url := fmt.Sprintf("https://api.chess.com/pub/player/%s/games/%d/%s", username, date.Year, date.Month)

	resp, err := http.Get(url)
	if err != nil { return nil, err }
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil { return nil, err }

	var response Response
	err = json.Unmarshal(body, &response)
	if err != nil { return nil, err }

	return response.Games, nil
}
```

`resp.StatusCode` is never examined. Every non-200 response body fails to populate `response.Games`, and — because `encoding/json` does not error on absent fields — unmarshals cleanly into a zero-valued struct. The function returns `([], nil)`. Control flows to `cmd/data/main.go:219`, which prints:

```
Fetched 0 games from Chess.com
```

...followed by `All games already analyzed!` at `cmd/data/main.go:244`, and exits 0. **The program reports success on every failure.**

Three failure modes all collapse into this same false-success path:

1. **Typo'd or nonexistent username** → 404. Almost certainly the most common first-run mistake, and it produces a confident "all done."
2. **Rate limiting** → 429. Chess.com throttles the public API; a contributor backfilling several months sequentially will hit it, and will conclude those months simply have no games.
3. **Blocked User-Agent** → 403. **No `User-Agent` header is set anywhere**, so requests go out with Go's default `Go-http-client/1.1`. Chess.com's public API is known to reject or throttle unidentified default clients. This is a live risk, not a hypothetical, and it means the tool may fail this way for a new user on their very first run with a perfectly valid username.

There is also no retry, no backoff, and no timeout on `http.Get` — it uses `http.DefaultClient`, which has none, so a hung connection hangs the pipeline indefinitely.

### 7.2 Ollama embeddings — same bug, and inconsistent within one file

`internal/embeddings/ollama.go:55-87`, `GetEmbedding`, also never checks `resp.StatusCode`. An Ollama error response (model not pulled, server busy) unmarshals into `embeddingResponse{Embedding: nil}` and returns `(nil, nil)`.

That nil embedding is not discarded. `cmd/data/worker.go:35-38` receives it with a nil error, and it flows to `SaveGameSummary` (`worker.go:90`) and into the `vector(768)` column. Depending on how pgvector handles the empty vector, this either errors far from its cause or **stores a garbage embedding that silently degrades every future similarity search** — a corruption that would be very hard to trace back.

What makes this clearly a bug rather than a style choice: **`Chat` in the same file does it correctly** (`ollama.go:114-117`):

```go
if resp.StatusCode != http.StatusOK {
	body, _ := io.ReadAll(resp.Body)
	return "", fmt.Errorf("chat request failed with status %d: %s", resp.StatusCode, string(body))
}
```

`Chat` also wraps every error with context (`ollama.go:99, 110, 121, 127`) while `GetEmbedding` returns bare errors (`ollama.go:64, 71, 77, 83`). One method got hardened; the other did not.

### 7.3 Timeout asymmetry

`New` sets a **10-second** client timeout (`ollama.go:49`) used by `GetEmbedding`. `Chat` ignores it and constructs its own client at **120 seconds** (`ollama.go:104-106`). A first embedding call against a cold Ollama — which must load the model into memory — can plausibly exceed 10s, producing a timeout that looks like a network fault. The per-call client in `Chat` also discards the connection pooling that the comment at `ollama.go:15` says the shared client exists to provide.

### 7.4 Ollama unreachable — no dedicated diagnosis

There is no startup health check. If Ollama is not running, `cmd/chat` connects to Postgres successfully, prints the full welcome banner (`cmd/chat/main.go:94-101`), accepts a question, and only then surfaces a raw `connection refused` wrapped as `failed to generate response`. The user has been told everything is fine right up until it isn't.

### 7.5 Stockfish missing — surfaced, but not actionable

`engine.StartEngine` (`internal/engine/stockfish.go:11-17`) returns `uci.New("stockfish")`'s raw error. `cmd/data/worker.go:141-149` wraps it usefully:

```go
errChan <- fmt.Errorf("worker %d: failed to start engine: %w", workerID, err)
```

This does fail loudly and does cancel the pool — the error propagates to `log.Fatalf` at `cmd/data/main.go:261`. Good. What is missing is the remedy: the message never says "install Stockfish and ensure it is on your PATH." A contributor sees an `exec: "stockfish": executable file not found in $PATH` nested inside worker-pool framing.

Note the failure occurs **per worker, after** games have been fetched and the DB migrated, so the user has already waited through setup work before learning about a missing prerequisite that could have been checked in the first second.

### 7.6 Database unreachable — handled well

The one path that is genuinely correct. `internal/db/db.go:26-28` pings after pool creation, so a bad connection fails immediately rather than at first query. Both entrypoints check the error and exit non-zero (`cmd/chat/main.go:77-80`, `cmd/data/main.go:149-151`). Modulo the redaction issue in §6.3, this is the model the other dependencies should follow.

### 7.7 No structured logging

Zero uses of `log/slog` in the tree. Output is split between:

- `fmt.Printf`/`Println` to stdout for progress and results (`cmd/data/worker.go:178`, `cmd/data/main.go` throughout),
- `fmt.Fprintf(os.Stderr, ...)` in the chat entrypoint (`cmd/chat/main.go:78, 130, 138`),
- `log.Fatalf` in the data entrypoint (`cmd/data/main.go:150, 163, 206, 217, 224, 228, 236, 261`),
- and the debug dump in §6.4.

Consequences: no log levels (the §6.4 dump cannot be turned off without a recompile), no machine-readable output, no correlation of a failure to a specific game UUID or worker across a concurrent run, and inconsistent stdout/stderr discipline that makes the CLI hard to script around.

`log.Fatalf` in `cmd/data` also calls `os.Exit`, skipping the `defer database.Close()` at `cmd/data/main.go:152` and `:225`. Harmless for a short-lived CLI, but it is the kind of thing a reviewer will flag.

### 7.8 Worker pool error handling — well built

Credit where due. `WorkerPool.Process` (`cmd/data/worker.go:114-202`) is careful: a cancellable context so one failure stops all workers (`:120`), a buffered single-slot error channel with non-blocking send so the first error wins without goroutines leaking (`:127, :143-147, :169-173`), `atomic.Int32` for progress (`:130`), `defer wg.Done()` and `defer engine.StopEngine(eng)` per worker (`:139, :150`), and a ctx check inside the consume loop (`:161-165`). This is the strongest code in the repository.

One minor defect: at `cmd/data/worker.go:185-190`, the `break` inside `select` breaks the `select`, not the enclosing `for` loop over games. On cancellation the producer continues iterating the remaining games rather than stopping early. Because `gameChan` is buffered to `len(games)` (`:124`) the sends never block, so this is a correctness wart with no practical consequence — but it is exactly the sort of thing a `for`-loop label would make clear, and worth noting for anyone who later reduces the buffer size.

---

## 8. API / interface stability

### 8.1 Nothing is importable today

Two independent barriers:

1. **Every package is under `internal/`**, which the Go toolchain enforces as unimportable outside the module.
2. **The module path is wrong** (§10.1). Even hoisting a package out of `internal/` would not make it fetchable.

So the "should this be public?" question is entirely forward-looking. Nothing can break today because nothing can depend on it.

### 8.2 `internal/engine` — the real promotion candidate

`internal/engine` is the one package with obvious value outside this project: a compact Stockfish UCI wrapper that analyzes a PGN and returns per-move evaluations with centipawn loss and move classification. Any Go project doing chess analysis wants exactly this, and the surface is small — `StartEngine`, `StopEngine`, `AnalyzePosition`, `AnalyzeGame`.

Its coupling to chesser is genuinely light. The only project-specific dependency is `models.MoveAnalysis` (`internal/engine/stockfish.go:6`), which is a plain data struct that could move alongside it. The classification thresholds (`classifyMove`, `stockfish.go:72-85`) are conventional chess-analysis values, not chesser-specific tuning.

**But it should not be promoted as-is.** Three API problems would be frozen the moment it becomes public:

- **The caller owns the engine.** `AnalyzeGame(engine *uci.Engine, ...)` requires callers to manage process lifecycle and hands them a third-party type in the signature — so `notnil/chess`'s API becomes part of *chesser's* public API, and any breaking change there breaks chesser's contract transitively.
- **No `context.Context`.** `AnalyzeGame` (`stockfish.go:87`) runs a bounded but potentially long loop — two engine calls per move at depth 12 — with no cancellation. Everything else in the codebase threads ctx properly; this is the one place that doesn't, and it is the place that most needs it. A public API without ctx is very hard to add ctx to later.
- **`models.MoveAnalysis` is shared with the DB layer** and shaped partly by storage concerns (`cmd/data/main.go:59-71` maps it field-by-field into `db.MoveRecord`). Publishing it couples an external contract to internal storage decisions.

The recommendation is to treat promotion as a design task gated behind an API redesign, not a directory move. Documented as an open question (§02).

### 8.3 The rest should stay internal

- `internal/db` — schema-coupled, offers nothing generic.
- `internal/chat`, `internal/summary` — chesser's actual domain logic; that *is* the product.
- `internal/embeddings` — thin Ollama client; the ecosystem has better-maintained options. Its interest is as an internal seam (§9), not a public package.
- `internal/api` — 37 lines against one Chess.com endpoint, and currently buggy (§7.1).
- `internal/search` — already well-factored with interfaces, but its `GameFilters` maps directly onto chesser's schema columns.

---

## 9. Community infrastructure

Nothing exists. There is no `.github/` directory.

| Item | Status | Assessment |
|---|---|---|
| Issue templates | ❌ | Worth adding. Bug reports for this project need OS, Go version, Postgres/pgvector version, Ollama model, and Stockfish version to be actionable at all — a template is the only way to get that reliably. |
| PR template | ❌ | Worth adding; short. |
| `CODEOWNERS` | ❌ | Low value at one maintainer. Recommend deferring. |
| `FUNDING.yml` | ❌ | Maintainer's call; no technical bearing. |
| Discussions vs. Issues policy | ❌ | Undecided. Given the setup complexity, "my setup doesn't work" traffic is likely to dominate — a policy prevents that from burying real bugs. |
| Labels | ❌ default set only | A `good-first-issue` label is the cheapest contributor on-ramp available, and this repo has real candidates (§2.2: `internal/summary` tests). |
| Milestones / project board | ❌ | Optional. This roadmap can serve the same purpose initially. |
| Badges in README | ❌ | Blocked on CI and LICENSE existing first. |

Note the dependency ordering: issue templates that ask "did CI pass?" and badges that report build status both presuppose §3. Community infrastructure is downstream of CI, not parallel to it.

---

## 10. Rename status and module path

### 10.1 No stale `chesser_local` references — but the module path is wrong anyway

The rename is textually complete. `grep -rn "chesser_local"` across the entire working tree (excluding `.git/`) returns **zero matches**. README, go.mod, imports, and `.gitignore` are all clean. There are no badge URLs, Docker image names, or CI references to be stale, because none of those artifacts exist yet.

**However**, the audit surfaced a related and more serious problem in the same place. `go.mod:1`:

```
module github.com/chesser
```

The actual repository is `https://github.com/cdewitt02/chesser.git` (`git remote -v`). The declared module path is **not a valid GitHub repository path** — `github.com/chesser` names a user or organization, not a repo.

Consequences:

- `go get github.com/chesser` fails. `go install github.com/chesser/cmd/chat@latest` fails. The module has never been fetchable and will not become so on publication.
- All 30 Go files import through this path (`github.com/chesser/internal/...`), so the fix touches every file with an import block.
- If `github.com/chesser` is ever registered by someone else, the path becomes actively misleading.

The correct value is `module github.com/cdewitt02/chesser`, with a corresponding rewrite of every internal import. This is mechanical — `go mod edit -module` plus a `find`/`sed` sweep — and `go build ./...` verifies it completely.

This belongs in P0 not because of the rename, but because it is the difference between a repository that can be used as a Go module and one that cannot. It also has a strong sequencing constraint: it rewrites nearly every file, so it should land before the `gofmt` sweep and before any outside PRs exist to conflict with.

---

## 11. Benchmark: what "well-maintained" looks like for a Go CLI/RAG project

Written from general knowledge of the Go ecosystem. **Specific figures — star counts, release dates, cadence — are deliberately not asserted**, as they were not verified during this offline audit. What follows are structural patterns that are stable and checkable against any comparable project.

**Reference points:** `charmbracelet/mods` (LLM-backed Go CLI, similar shape: terminal-first, talks to local and remote model backends) and `philippgille/chromem-go` (embeddable Go vector database, similar RAG-adjacent domain and similar "small dependency-light Go library" positioning).

**What projects of that class consistently have that chesser does not:**

1. **A license in the first commit, effectively always MIT or Apache-2.0** for Go tooling. This is the strongest convention in the ecosystem, and its absence is the loudest signal chesser currently sends.
2. **README leading with a demo before prerequisites.** A GIF, screencast, or literal pasted terminal session showing the tool answering a question. chesser's README opens with a four-step install wall. For a project whose entire value is "ask questions about your chess games and get real answers," showing one exchange would communicate more than the whole Quick Start does. This is chesser's single biggest missed opportunity, and it costs nothing technical.
3. **Badges as status, not decoration** — typically CI, Go Report Card, `pkg.go.dev` reference, license. Note all four are currently blocked: CI doesn't exist (§3), pkg.go.dev cannot index an unfetchable module (§10.1), and there is no license (§1.1). The absent badges are a symptom of the P0/P1 items, not a separate task.
4. **Tagged releases with notes**, even pre-1.0. Go's module system makes an untagged repo resolve to a pseudo-version, which reads as unfinished regardless of code quality.
5. **`internal/` used deliberately** — public packages for what is meant to be reused, `internal/` for what isn't. chesser currently has everything internal, which is a defensible *default* but is presently an unexamined one rather than a decision (§8).
6. **Dev dependencies containerized.** Any Go project needing Postgres for local development ships a Compose file. This is near-universal and directly addresses chesser's worst onboarding step (§5.1, step 3).
7. **A `Makefile` with `build`/`test`/`lint`/`fmt`** as the documented entry points, so CI and local development run identical commands.
8. **CONTRIBUTING.md that states the test story explicitly** — what runs without external services, what needs them, how to run the latter. For chesser this is the *hard* part (§2.3) and the part most likely to turn away a contributor who wants to help but cannot tell whether their change is safe.

**Where chesser already compares well:** the package layout is clean and conventional; `internal/search`'s interface-based design is better than typical for a project this young; the worker pool (§7.8) is genuinely well-engineered concurrency; and dependency count is admirably low — three direct dependencies for a RAG system is lean, and the ecosystem values that.

The gap is not code quality. It is entirely the connective tissue — license, CI, tests, container, accurate docs — that lets someone other than the author participate.
