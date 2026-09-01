# On-Ramp — Time to First Chat

How multi-provider support fits into reducing setup burden, and what it can and cannot do on its own.

**Decision context.** The storage question this analysis raised is settled in
[`ADR 0001`](../adr/0001-postgres-in-docker-over-sqlite.md): containerize Postgres, shelve SQLite.
This document covers the provider-side consequences.

---

## 1. The measure

**Time to First Chat** — wall-clock minutes from "nothing installed" to a first answer about one's own
games. Setup time alone is misleading, because Ingestion sits between setup and any answer.

Estimates below assume one month of games (~150), ingestion at `ANALYSIS_DEPTH = 12`
(`cmd/data/main.go:22`) with 4 workers, and +15% for Docker's VM on macOS and Windows. Install times
come from a cold-start audit of the Go tree: Go 5 min, PostgreSQL 10, pgvector 10–20, database and
extension 2, Ollama 5, two model pulls 10–20, Stockfish 5, `DATABASE_URL` 1 — 45–90 minutes in total.

| # | Path | Setup | Ingest | **Total** |
|---|---|---|---|---|
| 0 | **Today** — all native, Ollama | 57 | 12 | **~69 min** |
| 1 | Multi-provider, hosted *chat* only | 51 | 12 | **~63 min** |
| 2 | Compose for Postgres only (P3-1) | 45 | 12 | **~57 min** |
| 3 | Multi-provider, hosted chat **+ embeddings** | 39 | 12 | **~51 min** |
| 4 | Full-stack Compose, Ollama on host | 35 | 14 | **~49 min** |
| 5 | Binary + SQLite + Ollama | 26 | 12 | ~38 min |
| 6 | **Full-stack Compose + hosted** ← target | 17 | 14 | **~31 min** |
| 7 | Binary + SQLite + hosted | 8 | 12 | ~20 min |
| 8 | + bundled Stockfish | 3 | 12 | ~15 min |

Rows 5, 7, and 8 require the shelved SQLite route and are retained only to show what was traded away.

**Every hosted row assumes an API key already in hand.** A first-time user needs an account and a
billing method first — see §4.1, which restates the comparison honestly.

---

## 2. Four findings

### 2.1 Ingestion is the floor, and no design decision here moves it

Every row carries ~12 minutes of Stockfish analysis. Nothing in the provider or storage design touches
it; only `ANALYSIS_DEPTH`, worker count, or a reduced-analysis mode would. **Time to First Chat cannot
fall below roughly fifteen minutes.** Any onboarding copy promising faster is lying.

The mitigating fact is that this is unattended waiting rather than fiddly manual work — a different and
milder kind of pain than a pgvector source build. Ingestion is also already scoped to one month per
invocation (`cmd/data` takes year and month), so the default unit of work is already small.

### 2.2 Multi-provider alone barely helps — this is the important one

Row 1 moves the needle from 69 to 63 minutes. Six minutes.

The reason is structural: `EMBED_PROVIDER` defaults to Ollama, so a user who switches only their chat
provider **still installs Ollama and still pulls an embedding model**. All they save is the chat model
pull. Ollama remains a prerequisite, and the on-ramp is unchanged in character.

The gain only materializes when embeddings also move to a hosted provider (row 3: 51 minutes), which is
what removes Ollama from the prerequisite list entirely.

**Consequence for the plan:** hosted embeddings cannot stay deferred. [`01-design.md`](./01-design.md)
§10 listed multi-dimension schema support as a blocker and pushed embedding-provider work toward Phase 6.
That deferral would have made the entire multi-provider effort worth six minutes of onboarding time.
Hosted embeddings move into the main line — see §4.

### 2.3 Compose for Postgres alone barely helps either

Row 2 moves 69 to 57. The existing P3-1 scope trades ~27 minutes of pgvector pain for ~15 minutes of
Docker install, and leaves Go, Stockfish, and Ollama as host steps. **Neither track delivers an on-ramp
by itself.** They have to land together.

### 2.4 Local-first costs about eighteen minutes

Compare rows 4 and 6 (49 vs 31), or 5 and 7 (38 vs 20). Installing Ollama and pulling models is
consistently ~18 minutes across every configuration.

That is a fair price for running with no account, no key, and no egress — and it stays fully supported.
It is an expensive *default* to impose on someone who has not yet seen the tool do anything.

---

## 3. Target architecture

**Row 6.** Full-stack Compose plus hosted providers, with local-first as a documented alternative.

```
┌─ docker compose ──────────────────────────┐
│  app        multi-stage Go build          │
│             + stockfish (apt)             │
│  db         pgvector/pgvector:pg17        │
│             + named volume                │
└───────────────────────────────────────────┘
        │                        │
        │ hosted (default)       │ host.docker.internal:11434
        ▼                        ▼
  Anthropic / OpenAI       Ollama on host (optional)
```

**Ollama is deliberately not containerized.** Docker Desktop on Apple Silicon has no GPU passthrough, so
a containerized Ollama runs CPU-only and far slower than a native install on Metal; Linux requires
nvidia-container-toolkit. Users who want local inference run Ollama on the host, and the app container
reaches it by pointing `OLLAMA_URL` at `host.docker.internal`. This costs one line of configuration and
preserves GPU acceleration.

**Consequences to document rather than discover:** Docker Desktop is a ~1 GB install requiring
virtualization (WSL2 on Windows); Stockfish runs 10–30% slower in the VM on macOS and Windows, which
lengthens the step that already dominates; and `cmd/chat` is an interactive REPL
(`cmd/chat/main.go:104-135`) so it needs `docker compose run --rm -it`, which is worth wrapping in a
script rather than putting in front of a new user.

---

## 4. Provider defaults

The on-ramp goal and the backward-compatibility constraint in [`01-design.md`](./01-design.md) §5 pull in
opposite directions. Existing users with `OLLAMA_URL` set must keep working untouched, which argues for
`ollama` as the default. A new user should not have to install Ollama, which argues for hosted.

**Resolution: separate the code default from the documented default.**

- **Code default stays `ollama`** for both `CHAT_PROVIDER` and `EMBED_PROVIDER`. Anyone with an existing
  configuration sees no change whatsoever, and the tool never silently starts sending data to a third
  party or spending money because a default moved.
- **The README opens with a decision block, hosted first.** Two or three lines with honest costs for
  both paths, followed by **one** primary quick-start command sequence — the hosted one — with "Using
  Ollama instead" as a linked section.

The single command sequence is the important half. Two full parallel sequences is exactly the
documentation drift that [`../opensource-readiness/02-open-questions.md`](../opensource-readiness/02-open-questions.md)
Q3 identifies, and it doubles what must be kept correct. The decision block gets a reader moving in ten
seconds without paying that cost.

Nothing about the software's posture changes: the binary still defaults to local-first and account-free,
so the egress disclosure in [`01-design.md`](./01-design.md) §6 stays meaningful — hosted use remains an
explicit opt-in action. Only the recommended reading order changes.

### 4.1 State the costs honestly, including account creation

The ~31-minute figure for row 6 **assumes an API key already in hand**. A first-time hobbyist needs an
account, a billing method, and a key — realistically 8–10 minutes, plus a genuine psychological barrier
in entering a card to try a chess tool.

| Path | Nature of the cost | Realistic total |
|---|---|---|
| Compose + hosted | 8–10 min **active**: account, billing, key | ~37–39 min |
| Compose + host Ollama | ~18 min **unattended** download | ~49 min |

So the real gap is 10–12 minutes, not 18, and the two costs differ in kind: one is a walk-away download,
the other is handing a company a credit card. Hosted still wins on time, and it is the only path where a
user sees the tool work before committing 1.5 GB — but the decision block should present both costs in
these terms rather than quoting the raw minute counts.

---

## 5. Hosted embeddings without a schema migration

Per §2.2 this moves into the main line. It is cheaper than
[`01-design.md`](./01-design.md) §10 originally assumed.

**The width is not actually a problem.** OpenAI's `text-embedding-3-small` accepts a `dimensions`
parameter and can emit **768** directly. The existing `vector(768)` column
(`internal/db/schema.go:68`) accepts those vectors with no migration and no schema change.

**The vector space is the problem.** nomic-768 and OpenAI-768 are the same width but different spaces.
Cosine distance across them is meaningless, so mixing them in one index degrades retrieval *silently* —
no error, just worse answers. Nothing today records which model produced the stored vectors.

**The fix is a provenance stamp, not a migration.** Record the Embedding Provider and model that built
the index, and refuse at startup to run against an index built by a different one, with a message naming
both and pointing at the re-embed path. Re-embedding is comparatively cheap: `game_summaries` already
stores `summary_text`, and summaries are generated deterministically by `internal/summary` with no LLM
and no Stockfish involved. Re-embedding reads stored text and updates vectors — it does **not** re-run
game analysis.

So the honest cost of switching Embedding Providers is: one re-embed pass over stored summaries, plus a
startup check that makes a mismatch impossible to hit accidentally.

---

## 6. What this does not solve

- **Ingestion time** (§2.1). The floor. Separate work if it matters.
- **A downloadable single binary.** Foreclosed while Postgres is required — see
  [`ADR 0001`](../adr/0001-postgres-in-docker-over-sqlite.md).
- **Users who cannot or will not install Docker.** Older machines, locked-down corporate laptops,
  virtualization unavailable. The native path stays documented for them and remains what it is today.
- **Stockfish.** Containerized for Docker users, still a host install for native users. Bundling it
  outright raises a GPL-3.0 question that interacts with the still-open license decision
  ([`../opensource-readiness/02-open-questions.md`](../opensource-readiness/02-open-questions.md) Q1).
