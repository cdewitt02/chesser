# chesser — Prioritized Roadmap

Derived from [`00-audit.md`](./00-audit.md), audited at commit `2bcd4cb`.

Every item states **Problem → Fix → Effort → Risk/tradeoff**. Effort is calendar-ish for one person familiar with the code: **S** ≤ 1 hour, **M** a few hours to a day, **L** multiple days.

**P0 is ordered by severity.** **P1–P4 are ordered by what unblocks an outside contributor fastest**, which is not always the same as what is most broken. Where an item's placement is driven by sequencing rather than importance, that is called out.

---

## Sequencing constraints

Three hard orderings that cut across priorities. Getting these wrong creates avoidable rework:

1. **Module path rename (P0-1) before the `gofmt` sweep (P1-1).** Both touch nearly every file. Doing them in the other order, or in parallel, produces conflicts for no benefit.
2. **`gofmt` sweep (P1-1) before the CI format gate (P1-2).** 24 of 30 files currently fail `gofmt`. A gate added first is red on its first run.
3. **Both of the above before soliciting outside PRs.** Every repo-wide reformat invalidates in-flight branches. Do the churn while there is nothing to churn.

---

# P0 — Blockers to any public release

Nothing here is optional. Each item either makes the project legally unusable, factually undocumented, or silently wrong.

### P0-1 · Fix the module path

**Problem.** `go.mod:1` declares `module github.com/chesser`, but the repository is `github.com/cdewitt02/chesser`. `github.com/chesser` is a user/org path, not a repo path, so it resolves to nothing. `go get` and `go install` fail; `pkg.go.dev` cannot index the module. All 30 Go files import through this path.

Note this is *not* a leftover from the `chesser_local` rename — `grep -rn "chesser_local"` returns zero matches, and the rename is textually clean. The module path was wrong independently, and it is the real blocker the rename check was looking for.

**Fix.**
```bash
go mod edit -module github.com/cdewitt02/chesser
find . -name '*.go' -exec sed -i 's|github.com/chesser/|github.com/cdewitt02/chesser/|g' {} +
go build ./... && go vet ./... && go test ./...
```

**Effort.** S — mechanical, and `go build ./...` verifies it exhaustively.

**Risk/tradeoff.** Touches ~30 files, so it must land first (see sequencing). No runtime behavior changes. The only real risk is doing it *after* outside contributions exist, at which point every open PR conflicts.

---

### P0-2 · Add a LICENSE

**Problem.** No LICENSE file. Under default copyright, "public on GitHub" grants no rights — nobody may legally fork, modify, or redistribute. This blocks contribution as a matter of law, not convention.

**Fix.** Choose MIT or Apache-2.0 (see [`02-open-questions.md`](./02-open-questions.md) Q1) and add the file verbatim with correct copyright holder and year. Add a License section to the README.

**Effort.** S — minutes, once the choice is made.

**Risk/tradeoff.** The choice is effectively irreversible once contributors start submitting under it: relicensing later requires consent from every contributor. Worth 20 minutes of thought now, not a snap decision. The dependency licenses (pgx: MIT, notnil/chess: MIT-family, pgvector-go: MIT) are permissive and constrain nothing.

---

### P0-3 · Fix the broken ingestion command in the README

**Problem.** `README.md` step 4 says `go run cmd/data/main.go <username> <year> <month>`. This fails two ways: it cannot compile (`NewWorkerPool` lives in `cmd/data/worker.go`, excluded when a single file is named), and the CLI requires an `analyze` subcommand (`cmd/data/main.go:115-132`, `:198`). This is the first step where a new contributor does anything real, and it fails.

**Fix.** Correct to `go run ./cmd/data analyze <username> <year> <month>`, matching the program's own `printUsage()` at `cmd/data/main.go:134-138`. Document the undocumented `refresh-stats` subcommand (`cmd/data/main.go:140`) while in there. Then actually run both commands from a clean shell to confirm.

**Effort.** S — the edit is one line; verification is the real work.

**Risk/tradeoff.** None. Pure correction. Documentation-only, so it can ship independently of everything else — arguably the single highest value-per-minute item in this document.

---

### P0-4 · Reconcile documented config with actual behavior

**Problem.** Three divergences between README and code:

- `NUM_WORKERS` documented default `8 (4 for less compute)`; code returns `4` (`cmd/data/main.go:112`).
- `OLLAMA_URL` and `OLLAMA_EMBED_MODEL` are documented globally but **ignored by the entire ingestion path** — `cmd/data/main.go:251` hardcodes `embeddings.New("http://localhost:11434", "nomic-embed-text")`. Only `cmd/chat/main.go:52-60` honors them.
- The `vector(768)` column (`internal/db/schema.go:68`) hard-constrains embedding dimensionality; changing `OLLAMA_EMBED_MODEL` to any non-768-dim model breaks inserts with an opaque Postgres error, mid-ingestion, after minutes of Stockfish work.

**Fix.** Make `cmd/data` read the same env vars as `cmd/chat` — ideally by extracting one shared config helper so they cannot drift again. Correct the `NUM_WORKERS` default in the table. Add a note to the env table that the embedding model must produce 768 dimensions or the schema must change to match.

**Effort.** S–M. The README half is S; the config extraction is small but touches both entrypoints.

**Risk/tradeoff.** This is the one P0 that modifies application code beyond a rename. Making `cmd/data` respect `OLLAMA_URL` changes behavior for anyone who set it and worked around it being ignored — vanishingly unlikely, but it is a real behavior change rather than a doc fix. Documenting the divergence instead is *not* an acceptable alternative: config that is documented and silently discarded is worse than config that is undocumented.

---

### P0-5 · Check HTTP status in the Chess.com client

**Problem.** The worst defect in the codebase. `internal/api/data.go:15-36` never inspects `resp.StatusCode`. Any error response unmarshals cleanly into an empty struct and returns `([], nil)`, so `cmd/data/main.go:219` prints `Fetched 0 games from Chess.com`, then `All games already analyzed!`, then exits 0. **The program reports success on every failure.**

This swallows three distinct real-world cases: a typo'd username (404 — the most likely first-run mistake), rate limiting (429 — reachable by anyone backfilling several months), and a blocked User-Agent (403 — chesser sets no `User-Agent`, so requests go out as `Go-http-client/1.1`, which Chess.com's public API is known to reject or throttle). The third means this can fail on a new user's very first run with a completely valid username.

**Fix.** Check `resp.StatusCode` and return a distinct, actionable error per class: 404 → "no games found for user X in YYYY/MM — check the username"; 429 → "rate limited by Chess.com, retry after N"; other non-2xx → status plus body excerpt. Set a descriptive `User-Agent` (Chess.com's API docs request contact info). Replace the bare `http.Get` with a client carrying an explicit timeout.

**Effort.** S — roughly 20 lines in a 37-line file.

**Risk/tradeoff.** Runs that previously "succeeded" with zero games will now fail loudly. That is the entire point, but it means anyone with a broken setup finds out — correctly — that it was always broken. Adding retry/backoff for 429 is tempting; keep it out of P0 and do the status check alone, since the failing-loudly part is what matters and backoff adds testing surface.

---

### P0-6 · Check HTTP status in `GetEmbedding`

**Problem.** `internal/embeddings/ollama.go:55-87` has the same missing status check. An Ollama error unmarshals to `embeddingResponse{Embedding: nil}` and returns `(nil, nil)`. That nil is not discarded — `cmd/data/worker.go:35-38` sees no error and passes it to `SaveGameSummary` (`worker.go:90`), where it reaches the `vector(768)` column. Either it errors far from its cause, or it stores a garbage embedding that **silently degrades every future similarity search**, which would be extremely hard to trace back.

This is unambiguously an oversight rather than a choice: `Chat` in the same file checks status correctly (`ollama.go:114-117`) and wraps all its errors with context, while `GetEmbedding` does neither.

**Fix.** Mirror `Chat`'s pattern — status check plus body excerpt, and wrap the four bare error returns (`ollama.go:64, 71, 77, 83`) with context. Also reject a zero-length embedding explicitly, as belt-and-braces against a 200 with an empty body.

**Effort.** S.

**Risk/tradeoff.** None meaningful. Note that any embeddings already corrupted by this bug remain corrupted — re-ingestion would be needed to clean them, which is worth a line in the release notes.

---

### P0-7 · Gate the system-prompt dump behind a debug flag

**Problem.** `internal/chat/service.go:111-114` unconditionally prints the entire system prompt — which embeds retrieved game summaries and aggregate player statistics — to stdout on **every question**. It is debug instrumentation shipped in the interactive product, it makes the tool unpleasant to screen-share or record without exposing personal game history, and because it sits after the LLM call it interleaves confusingly with the `Thinking...` line from `cmd/chat/main.go:127`.

**Fix.** Gate behind `CHESSER_DEBUG` or a `--debug` flag. **Do not delete it** — it is genuinely valuable when tuning retrieval, which is exactly the kind of work this project invites.

**Effort.** S.

**Risk/tradeoff.** None. The only decision is env var vs. flag; an env var is less code and composes better with the existing config, a flag is more discoverable. Either is fine.

---

### P0-8 · Stop sending opponents' usernames to hosted providers

*(Filed 2026-08-29, after the finding below surfaced while auditing prompt determinism for the Python rewrite.)*

**Problem.** `internal/chat/router.go:241` emits the "Game endings" section one line per distinct `termination_type`, and Chess.com's termination strings **embed the opponent's username**: `"Bolzman0 won by resignation"`, `"AlexanderZapata37811 won by resignation"`. On a 74-game corpus that is 51 distinct values — 51 prompt lines, nearly all of them a different third party's handle, sent verbatim to Anthropic or OpenAI on every aggregate query.

Two distinct problems in one section:

1. **Disclosure.** The README and the startup banner promise that a hosted provider receives "your game summaries and your Chess.com username." They do not mention *other players'* usernames, and those people never chose this tool. The promise as written is inaccurate.
2. **Prompt bloat with near-zero signal.** Because the username is part of the key, every opponent forms their own bucket, so the aggregation never aggregates: dozens of rows of `1 (1.4%)` where the useful signal is the handful of *categories* — resignation, checkmate, timeout, abandonment, stalemate, repetition — and whether the player won or lost by each.

**Fix.** Normalize the termination string into (outcome, method) before it reaches the prompt — "won by resignation: 9", "lost on time: 5" — keyed on whether the winner is the player, not on who the winner was. The opponent's identity carries no coaching value in an aggregate. Consider whether `games.termination_type` should store the normalized form as well; retrieval and stats both want the category, not the raw string.

**Effort.** S–M. The normalization is small; deciding whether to also change what is stored is the part worth thinking about.

**Risk/tradeoff.** Changes assembled prompts, so it interacts with [`docs/multi-provider/03-eval-plan.md`](../multi-provider/03-eval-plan.md) — re-run the question set after. It is a strict improvement to signal density, so answers should get better, not merely different.

**Note.** The per-game summaries are unaffected: `internal/summary/generator.go` prints `TerminationType` for a single game, where the opponent's name is already implied by the game itself. This is specifically about the aggregate section.

---

# P1 — Contribution readiness

*Ordered by what unblocks an outside contributor fastest.* The ordering here is deliberately not severity-ordered: the formatting sweep is trivially unimportant on its own, but it gates CI, which gates everything else.

### P1-1 · Repo-wide `gofmt` sweep

**Problem.** `gofmt -l .` flags **24 of 30 files** — space-indented type blocks (`internal/embeddings/ollama.go:13-43`, `internal/chat/service.go:170`), trailing whitespace (`cmd/data/main.go:169,172,177,184,277`; `internal/engine/stockfish.go:66,111`), missing EOF newlines (`internal/models/position.go`, `internal/models/game.go`).

**Why it is first.** Not because unformatted code is harmful — it changes no behavior. Because it **blocks P1-2**, and because a repo-wide reformat invalidates every in-flight branch. Doing it now, while there are no outside branches to invalidate, costs nothing. Doing it in six months costs every open PR.

**Fix.** `gofmt -w .`, single commit, no other changes mixed in so the diff stays trivially reviewable. Record the commit SHA for `.git-blame-ignore-revs` later.

**Effort.** S.

**Risk/tradeoff.** Pollutes `git blame` for one commit across most files. Mitigated by `.git-blame-ignore-revs`, which GitHub honors. Keeping it isolated from any semantic change is essential — a mixed commit here would be genuinely annoying to review forever.

---

### P1-2 · CI workflow: build, vet, test

**Problem.** No `.github/` at all. Nothing verifies that an incoming PR compiles, passes vet, or passes the one existing test. Right now the maintainer is the CI.

**Fix.** `.github/workflows/ci.yml` on push and PR: `actions/setup-go` pinned to the `go.mod` version with module caching, then `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l . | tee /dev/stderr | (! read)` as a hard gate. All four pass today (post-P1-1), so CI is green from the first commit — which matters: a workflow introduced red trains everyone to ignore it.

Add the CI badge to the README once passing.

**Effort.** S–M.

**Risk/tradeoff.** The test step is nearly vacuous — only `internal/search` has tests, so this proves compilation and vet-cleanliness, not correctness. That is still most of the value at this stage. Deliberately **excluded**: `golangci-lint` (opinionated, likely noisy on an existing codebase, better introduced deliberately in P2) and any job requiring Postgres/Ollama/Stockfish (see §2.3 of the audit — that's an unsolved design question, not a CI config question).

---

### P1-3 · CONTRIBUTING.md

**Problem.** No contribution guidance. A willing contributor cannot determine how to build, what to run before submitting, or — critically — **what can be tested without Postgres, Ollama, and Stockfish all running**. That last question is the one most likely to make someone give up: they can write a fix but cannot tell whether it is safe.

**Fix.** Cover: prerequisites with concrete install commands per platform (the README merely names them); the corrected build/run commands from P0-3; the pre-submit checklist (`gofmt -l .`, `go vet ./...`, `go test ./...`); commit/PR conventions; and an explicit, honest **testing matrix** — what runs with no external services (`internal/search` today, `internal/summary` after P2-1), what needs Postgres, what needs Ollama, what needs Stockfish. Say plainly that most packages currently have no tests and that adding them is welcome.

**Effort.** M — mostly writing, and it depends on P0-3/P0-4 being correct first, since it will restate those commands.

**Risk/tradeoff.** Goes stale if the setup changes — particularly if Docker Compose (P3-1) later becomes the primary path. Mitigate by keeping setup steps in the README and having CONTRIBUTING link to them rather than duplicating.

---

### P1-4 · Issue and PR templates

**Problem.** No templates. Bug reports for this project are useless without OS, Go version, Postgres and pgvector versions, Ollama model, and Stockfish version — and given the setup complexity, most early issues will be environment problems that are unanswerable without exactly those fields.

**Fix.** `.github/ISSUE_TEMPLATE/bug_report.yml` as a structured form with those fields required; `feature_request.yml` lighter; `config.yml` pointing setup questions at Discussions if enabled (see [`02-open-questions.md`](./02-open-questions.md) Q6). Short PR template: what changed, how it was verified, checklist matching P1-3.

**Effort.** S.

**Risk/tradeoff.** Heavy templates deter drive-by reports. Keep required fields to what is genuinely necessary for triage. Value is proportional to inbound volume, which is why this sits below CI and CONTRIBUTING rather than above them.

---

### P1-5 · `good-first-issue` labels *(cheap, high leverage)*

**Problem.** No labels beyond GitHub defaults. A contributor who wants to help has no entry point and must reverse-engineer one from the codebase.

**Fix.** File a handful of genuinely small, genuinely useful issues from this audit and label them: tests for `internal/summary` (P2-1 — pure functions, no dependencies, ideal first contribution), remove the dead `TestData` struct (`cmd/data/main.go:17-20`), the actionable-Stockfish-error message (P2-4), the `for`-loop `break` wart (`cmd/data/worker.go:185-190`).

**Effort.** S.

**Risk/tradeoff.** Only works once the repo is discoverable and contributable — i.e. after P0-2 (license) and P1-2 (CI). Stale unclaimed issues are mildly off-putting, so file few and keep them real.

---

### P1-6 · CODEOWNERS — recommend deferring

**Problem.** No `CODEOWNERS`.

**Fix.** A single-line file assigning everything to `@cdewitt02`.

**Effort.** S.

**Risk/tradeoff.** **Genuinely low value at one maintainer** — it automates review assignment to the only person who could review anyway. Listed for completeness because the audit brief asked; recommendation is to skip until there is a second regular contributor. Doing it now is harmless but is busywork dressed as governance.

---

# P2 — Quality & trust signals

*Ordered by value-per-hour, with the cheapest real coverage first.*

### P2-1 · Test `internal/summary`

**Problem.** 228 lines of pure, branch-heavy business logic with zero tests. `detectPattern` (`generator.go:194-228`) alone has 10 outcomes across a won/lost/drew × wasWinning/wasLosing matrix. `weakestPhase` (`:171-192`) has three-way comparison logic with division-by-zero guards. `ExtractSummaryData` (`:39-142`) contains move-parity logic (`:89-90`) and phase boundary conditions (`:104-110`) that are exactly where off-by-ones live. Every one of these feeds the text that gets embedded — so a bug here quietly poisons retrieval quality rather than crashing.

**Why first.** These are **pure functions over plain structs with no external dependencies**. They are testable *today*, with no refactoring at all, and they run in CI with nothing installed. Nothing else in the repo offers coverage this cheaply.

**Fix.** Table-driven tests following the existing style in `internal/search/parser_test.go`. Full matrix for `detectPattern`; boundary cases for `weakestPhase` including all-zero move counts; phase-boundary cases for `ExtractSummaryData` at moves 10/11 and 25/26; both color paths.

**Effort.** M.

**Risk/tradeoff.** May well surface real bugs — the phase boundaries use move *indices* (`i < OpeningEnd` where `i` counts plies, not moves), which does not obviously match the `// moves 1-10` comment at `generator.go:10`. That is a feature of writing the tests, but budget for fixing what they find. Whether such a discrepancy is a bug or intended needs a maintainer call.

---

### P2-2 · Make `internal/engine` testable, then test it

**Problem.** The most subtle logic in the codebase is the least testable. `normalizeEval` flips sign by move parity (`stockfish.go:65-70`); `getEvaluation` encodes mate as ±10000 offset by `MateIn` (`:54-63`); `AnalyzeGame` computes CPL with **opposite subtraction order per color** (`:132-141`); `classifyMove` sets thresholds (`:72-85`). A sign or off-by-one error in any of these silently corrupts every stored analysis, every summary, and every embedding — with no crash and no error. Nothing can currently test them, because `AnalyzeGame` takes a concrete `*uci.Engine` (`:87`) and needs a real Stockfish process.

**Fix.** Two stages, and the first is most of the value:

1. **Test the pure helpers directly** — `getEvaluation`, `normalizeEval`, `classifyMove` need no engine at all and can be tested immediately (in-package, since they're unexported). Cheap, and covers the sign logic.
2. **Extract a `PositionAnalyzer` interface** so `AnalyzeGame` accepts an abstraction rather than `*uci.Engine`. Then a fake analyzer returning scripted evaluations lets the whole CPL pipeline be tested against known PGNs with no Stockfish.

Follow the pattern already proven in `internal/search/search.go:8-15`.

**Effort.** Stage 1: S. Stage 2: M.

**Risk/tradeoff.** Stage 2 changes `internal/engine`'s signature — fine now (it's internal, one caller at `cmd/data/worker.go:27`), much harder after any promotion to public (§8.2 of the audit). **This refactor should precede any promotion decision**, and doing it well largely resolves the promotion question, since the redesigned interface is exactly what a public API would need. Adding `context.Context` at the same time is the natural moment.

---

### P2-3 · Startup preflight checks

**Problem.** Failures arrive late and in the wrong order. `cmd/chat` connects to Postgres, prints the full welcome banner (`cmd/chat/main.go:94-101`), accepts a question, and only *then* reveals that Ollama was never reachable. `cmd/data` fetches games and migrates the DB before discovering Stockfish is missing (`cmd/data/worker.go:141`), per worker. In both cases the user is told everything is fine, then invests time, then learns a prerequisite was absent from the start.

**Fix.** Preflight before the banner / before fetching: `cmd/chat` pings Ollama (`GET /api/tags`) and verifies the embedding model is present; `cmd/data` additionally does one `exec.LookPath("stockfish")`. Fail with the remedy, not just the symptom.

**Effort.** S–M.

**Risk/tradeoff.** Slightly slower startup and a little duplicated knowledge of what each command needs. Both are trivial against turning a confusing mid-run failure into a one-line message at second zero. Note P2-4 overlaps and could be folded in.

---

### P2-4 · Actionable error messages

**Problem.** Errors are surfaced but not actionable. Missing Stockfish yields `worker 0: failed to start engine: exec: "stockfish": executable file not found in $PATH` — technically complete, but it never says *install Stockfish and put it on PATH*. Ollama-down produces a raw `connection refused` wrapped as `failed to generate response`.

**Fix.** A short pass over the failure paths identified in §7 of the audit, attaching remedies. Pair with P0-5/P0-6 (which create the messages) and P2-3 (which moves them earlier).

**Effort.** S.

**Risk/tradeoff.** None. Easy to over-engineer into an error taxonomy — resist; plain sentences with the fix in them are sufficient.

---

### P2-5 · Redact connection strings in error output

**Problem.** `internal/db/db.go:20-28` wraps pgx errors with `%w`; pgx parse errors can embed the connection string including the password. That reaches stderr unredacted at `cmd/chat/main.go:78` and `log.Fatalf` at `cmd/data/main.go:150`. A contributor pasting a failure into an issue publishes their password.

**Fix.** Parse the URL and blank the password component before including it in any wrapped error.

**Effort.** S.

**Risk/tradeoff.** Narrow exposure — needs a malformed URL, not merely a wrong password — which is why it is P2 and not P0. But the cost of the fix is ~10 lines and the cost of the leak lands on users, not the maintainer. That asymmetry argues for doing it early within P2.

---

### P2-6 · Adopt `log/slog`

**Problem.** No structured logging. Output is split four ways: `fmt.Printf` to stdout for progress (`cmd/data/worker.go:178`), `fmt.Fprintf(os.Stderr, ...)` in chat (`cmd/chat/main.go:78,130,138`), `log.Fatalf` in data (eight sites), and the debug dump from P0-7. There are no levels — which is why P0-7 needs a bespoke flag instead of just being `slog.Debug`. In a concurrent 4-worker run there is no way to correlate a failure to a game UUID or worker. `log.Fatalf` also skips the `defer database.Close()` at `cmd/data/main.go:152` and `:225`.

**Fix.** `log/slog` with a level from `CHESSER_LOG_LEVEL`. Convert `log.Fatalf` sites to log-then-`os.Exit(1)` from `main` so defers run. Add worker ID and game UUID as structured attributes. **Keep user-facing CLI output as plain `fmt` on stdout** — the REPL banner, prompts, and progress are UI, not logs, and should not become JSON.

**Effort.** M.

**Risk/tradeoff.** Touches many files, so it should land after the P1-1 sweep and ideally not concurrently with P2-1/P2-2. The real trap is over-converting: turning the chat REPL's output into structured logs would make the tool worse. The line between "log" and "UI output" needs to be drawn deliberately.

---

### P2-7 · Add `golangci-lint` and `govulncheck`

**Problem.** No linting beyond `go vet`; `govulncheck` has never run against this module, so the two behind-by-a-minor dependencies (pgx v5.8.0→v5.10.0, pgvector-go v0.3.0→v0.4.1) have not been assessed for security content.

**Fix.** `.golangci.yml` starting conservative (`errcheck`, `staticcheck`, `ineffassign`, `unused` — `unused` catches the dead `TestData` struct that `go vet` misses). Fix the initial findings in a dedicated commit, *then* add to CI as a gate. Add `govulncheck` as a separate CI job. Update the two deps.

**Effort.** M — unknown until the first run; `errcheck` in particular tends to be loud on code that ignores `Close()` errors.

**Risk/tradeoff.** Deliberately below tests in priority: a linter finds style and low-grade correctness issues, while the untested `internal/engine` sign logic (P2-2) could be silently wrong in ways no linter detects. Enabling too many linters at once produces a wall of findings that gets suppressed wholesale — start small and expand.

---

# P3 — Ease of adoption

*Ordered by how much onboarding time each removes.*

### P3-1 · Docker Compose for Postgres + pgvector

**Problem.** The single worst onboarding step. Installing pgvector (audit §5.1, step 3) commonly means building from source against local PG headers — 10–20 minutes and the most likely point of abandonment. The README links the project but gives no install command.

**Fix.** A ~15-line `docker-compose.yml` using `pgvector/pgvector:pg17`, which ships the extension prebuilt, with a named volume and the default `DATABASE_URL` documented alongside. This eliminates steps 2, 3, and 4 entirely — install Postgres, install pgvector, create DB and extension all collapse into `docker compose up -d`.

**Effort.** S. Highest onboarding-time-saved per line of anything in this document.

**Risk/tradeoff.** Adds Docker as a prerequisite for the recommended path. Whether Compose *replaces* native install as the documented default or sits alongside it is a genuine decision — see [`02-open-questions.md`](./02-open-questions.md) Q3. Note the container only covers Postgres: Ollama and Stockfish stay native, since containerizing Ollama complicates GPU passthrough and Stockfish needs host CPU. Being explicit that this is a *partial* containerization avoids disappointment.

---

### P3-2 · Makefile

**Problem.** No task runner. Every command — build, test, vet, fmt, and the two multi-argument CLI invocations — is typed from memory or copied from a README that is currently wrong about one of them (P0-3).

**Fix.** `make build`, `test`, `vet`, `fmt`, `lint`, `check` (all gates, matching CI exactly), `db-up`/`db-down` wrapping Compose, and `dev` for one-command setup. Have CI invoke the same targets so local and CI cannot diverge.

**Effort.** S.

**Risk/tradeoff.** Make is not universal on Windows. Given Stockfish/Ollama/Postgres are the harder Windows problems anyway, this is not the binding constraint. Keep targets thin wrappers over real commands so anyone can read the Makefile and run them directly.

---

### P3-3 · Document hardware requirements

**Problem.** Nothing warns that ingestion is heavy. `ANALYSIS_DEPTH = 12` (`cmd/data/main.go:22`) with 4 default workers each spawning its own Stockfish process (`cmd/data/worker.go:141`) saturates 4 cores for the run, while Ollama concurrently holds an embedding model resident and the chat path runs local LLM inference. On a 2-core laptop or small VM this is unusably slow, with no warning and no hint that `NUM_WORKERS` is the knob.

**Fix.** A short section: recommended cores and RAM, the ~1.5 GB model download, rough throughput expectations, and explicit `NUM_WORKERS` guidance for constrained machines. Real measured numbers from one or two machines beat hand-waving — `cmd/data/main.go:265-268` already computes and prints games/sec, so the data is free.

**Effort.** S.

**Risk/tradeoff.** Numbers vary wildly by hardware and are easy to misread as guarantees. Frame as observed ranges with the machine described.

---

### P3-4 · Troubleshooting section

**Problem.** The failure modes are known and enumerable (audit §7) but nowhere documented. Every one currently presents as a bare Go error or — before P0-5/P0-6 — as silence.

**Fix.** A README section keyed to the actual failures: `Fetched 0 games` (bad username / rate limit / 403 — and note the P0-5 fix), `connection refused` on Ollama, Stockfish not on PATH, pgvector extension missing, vector-dimension mismatch from a non-768-dim model, slow ingestion → tune `NUM_WORKERS`.

**Effort.** S.

**Risk/tradeoff.** Must be written *after* P0-5/P0-6 land, or it documents error messages that are about to change. Cheap to keep current if entries are added as issues arrive.

---

### P3-5 · README demo

**Problem.** The README opens with a four-step install wall and never shows the tool working. For a project whose entire value proposition is "ask questions about your chess games and get real answers," this is the biggest missed opportunity in the documentation — and it is the most consistent difference from well-regarded Go CLI projects (audit §11).

**Fix.** A pasted terminal session or GIF immediately after the one-line description, showing a real question and a real answer. A plain fenced code block is 90% of the value at 10% of the effort of a recording.

**Effort.** S.

**Risk/tradeoff.** Needs a working setup and a real dataset. Redact the username if the maintainer's own games are used. Placed in P3 as "adoption" but note it is genuinely cheap and could ride along with any earlier README pass.

---

# P4 — Longer-term maturity

### P4-1 · Versioning and releases

**Problem.** No tags. Go's module system resolves an untagged repo to a pseudo-version (`v0.0.0-2026...`), which reads as unfinished regardless of code quality, and gives users no way to pin.

**Fix.** Tag `v0.1.0` once P0 is complete, then semver thereafter. Optionally GoReleaser for cross-platform binaries — worth it since installing Go is otherwise a hard prerequisite for a chess *player* who is not a Go developer.

**Effort.** S for tagging; M with GoReleaser.

**Risk/tradeoff.** Tagging `v0.x` deliberately signals an unstable API, which is honest here. **Do not tag before P0-1** — a tag against the wrong module path is worse than no tag, since it enters the module proxy permanently and cannot be recalled.

---

### P4-2 · CHANGELOG

**Problem.** No changelog. Users cannot tell what changed between versions, and P0-6 in particular ships a fix that means "any embeddings generated before this version may be corrupt and should be regenerated" — precisely the kind of thing that must be written down somewhere durable.

**Fix.** Keep-a-Changelog format, starting at the `v0.1.0` tag.

**Effort.** S, plus ongoing discipline.

**Risk/tradeoff.** Only useful if maintained; a changelog that stops at v0.2.0 is worse than none. Generating from conventional commits is an alternative if hand-maintenance proves unrealistic.

---

### P4-3 · Decide on promoting `internal/engine`

**Problem.** Everything is under `internal/`, so nothing is importable — a defensible default, but currently an unexamined one rather than a decision. `internal/engine` is the clear candidate: a compact Stockfish UCI wrapper with CPL computation and move classification that any Go chess project would want, coupled to chesser only through `models.MoveAnalysis`.

**Fix.** Decide (see [`02-open-questions.md`](./02-open-questions.md) Q4). If promoting, the API redesign from **P2-2 is a hard prerequisite** — the current signature takes a caller-owned `*uci.Engine`, which would make `notnil/chess`'s API part of chesser's public contract transitively, and has no `context.Context` on a loop that runs two engine calls per move. Both are near-impossible to fix after publication.

**Effort.** S if the P2-2 refactor already landed (it becomes a directory move plus doc comments); L if attempted from scratch.

**Risk/tradeoff.** A public package is a maintenance commitment: breaking changes then require a major version. The honest question is whether anyone actually wants this — publishing a package nobody imports is pure cost. A reasonable middle path is to do the P2-2 refactor (valuable regardless, for testability) and defer promotion until someone asks.

---

### P4-4 · Swappable LLM backend

**Problem.** Ollama is hardcoded throughout. `embeddings.Client` is constructed directly at `cmd/chat/main.go:84` and `cmd/data/main.go:251`, and `chat.Service` holds a concrete `*embeddings.Client` (`service.go:14`). Anyone wanting a different local runtime, or a hosted API, must fork.

**Fix.** Define `Embedder` and `ChatCompleter` interfaces in a neutral package; make `chat.Service` and the ingestion path depend on those. Ollama becomes one implementation. The pattern is already proven at `internal/search/search.go:8-15` — `search.EmbeddingClient` is exactly this interface, just not used at the composition root.

**Effort.** M for the interfaces (the codebase is already shaped for it); L per additional backend.

**Risk/tradeoff.** The interfaces are cheap and improve testability regardless (they'd let the chat path be tested without Ollama, which P2 does not otherwise address). The expensive, uncertain part is *maintaining* multiple backends. **Recommendation: extract the interfaces, ship only the Ollama implementation, and let demand decide the rest.** Note the 768-dim schema constraint (P0-4) is a second, independent lock-in: genuinely swappable embedding backends need configurable vector dimensions, which is a migration problem, not an interface problem.

---

## Suggested execution order

| Phase | Items | Rationale |
|---|---|---|
| 1 | P0-1, P0-3 | Module path first (rewrites everything); README fix is doc-only and independently shippable |
| 2 | P0-2, P0-4 … P0-7 | Remaining release blockers; P0-2 needs the license decision |
| 3 | P1-1, P1-2 | Sweep then gate — order is mandatory |
| 4 | P1-3 … P1-5 | Contributor-facing docs, once commands are correct and CI is green |
| 5 | P2-1, P2-5, P2-3, P2-4 | Cheapest real coverage plus the security and UX fixes |
| 6 | P3-1, P3-2 | Biggest onboarding wins, both small |
| 7 | P2-2, P2-6, P2-7 | Larger refactors, once the surface is stable |
| 8 | P3-3 … P3-5, P4-* | Polish and long-term direction |

Phases 1–4 are what stand between this repository and one an outside contributor can meaningfully engage with. That is roughly **two to three focused days**, and most of it is documentation rather than code.
