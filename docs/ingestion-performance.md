# Ingestion Performance — Findings

Parked findings from the multi-provider design session. Not provider work — no dependency on
[`multi-provider/`](./multi-provider/) — but ingestion is the largest single component of Time to First
Chat, so it belongs on the same roadmap.

**Why this matters.** [`multi-provider/04-onboarding.md`](./multi-provider/04-onboarding.md) §1 puts the
best achievable on-ramp at ~31 minutes, of which **~12 is ingestion**. It is the one cost no provider or
storage decision touches.

---

## 1. Every interior position is analyzed twice

**Free ~2× speedup. No quality change, no configuration, no trade-off.**

`AnalyzeGame` (`internal/engine/stockfish.go:87`) loops over half-moves and makes two engine calls per
iteration:

```
stockfish.go:99    beforeAnalysis := AnalyzePosition(engine, gamePositions[i], depth)
stockfish.go:105   afterPos := gamePositions[i+1]
stockfish.go:123   afterAnalysis := AnalyzePosition(engine, afterPos, depth)
```

Iteration `i` analyzes `gamePositions[i]` and `gamePositions[i+1]`. Iteration `i+1` then analyzes
`gamePositions[i+1]` again at line 99 — same position, same depth, same function. **Every interior
position is evaluated exactly twice.**

**Fix.** Carry the previous iteration's `afterAnalysis` forward as the next `beforeAnalysis`. Analyses
drop from `2N` to `N+1`. Stockfish at a fixed depth on a fixed position is deterministic, so the results
are identical — this is caching, not approximation.

**Effect.** Ingestion roughly halves: ~12 min → ~6 min for a month of games. That is a larger cut to
Time to First Chat than every decision in [`multi-provider/`](./multi-provider/) combined.

**Caveats to handle:** the terminal-position branch (`stockfish.go:105-121`) skips the after-analysis and
must not poison the cache; iteration 0 still needs a fresh before-analysis; and the cache is per-game, so
it resets between games and stays compatible with the worker pool (`cmd/data/worker.go:114-202`), which
gives each worker its own engine process.

**Verification.** Analyze the same games before and after and diff the stored `moves` rows — `cpl`,
`classification`, `evaluation`, and `best_move` must be byte-identical. `internal/engine` has no tests
today; `normalizeEval`, `getEvaluation`, and `classifyMove` are pure and testable without a binary
(readiness P2-1), and are worth covering before touching this loop.

---

## 2. `ANALYSIS_DEPTH` is a hardcoded constant

`ANALYSIS_DEPTH = 12` (`cmd/data/main.go:22`) is not configurable, and nothing documents its cost.
Engine time scales steeply with depth, so this is the second-largest lever after §1 — but unlike §1 it is
a real quality trade-off, not free.

Blunder and mistake classification (`classifyMove`, thresholds at `stockfish.go:~75-84`) is comparatively
robust at lower depth, since it keys off large centipawn swings. Best-move agreement, which drives the
`"best"` classification (`stockfish.go:143-146`), degrades faster.

**Suggested:** expose as `ANALYSIS_DEPTH`, keep 12 as the default, and document the trade honestly. Do
**not** lower the default without measuring — the Game Summary is the retrieval corpus, so degraded
analysis degrades every future answer, invisibly and permanently.

---

## 3. `NumSimilar` discards 90% of what it retrieves

Not an ingestion cost — a per-question one — but the same shape of waste.

`defaultNumSimilar = 100` (`cmd/chat/main.go:21`) sets retrieval `TopK` (`router.go:115`), then
`writeGameContext` shows `min(len(games), detailLimit)` = **10** (`router.go:395-396`). Aggregate and
Comparative queries truncate to 3 first (`router.go:360-363`). Nothing else reads the remainder beyond a
`len() > 0` check (`service.go:82`).

So 100 games are fetched from Postgres with full record joins and 90 are discarded, every question.
Either lower `NumSimilar` toward `DetailLimit`, or establish what the wider retrieval is for — a
re-ranking step would justify it, but none exists today.

---

## 4. Chess.com already returns accuracy data, unused

`models.Game.Accuracies` (`internal/models/game.go:19`) is parsed from the API response and referenced
**nowhere else in the repo**.

It cannot replace Stockfish — it is one number per player per game, with no per-move CPL, no blunder
counts, and no phase breakdown, so `GenerateSummary` would lose weakest-phase and pattern detection
(`internal/summary/generator.go:144+`). But it is free, already fetched, and could serve as a
cross-check on computed CPL or as a fast-path preview. Noted rather than recommended.
