# chesser — Open Questions

Decisions that need a maintainer call before the corresponding roadmap work starts. Each states what is being asked, why it is genuinely ambiguous rather than a default, what it blocks, and a recommendation.

Ordered by what blocks the earliest roadmap work.

---

## Q1 · Which license?

**Blocks:** P0-2 — and transitively every other contribution, since without a license nobody may legally fork or modify the code.

**Why it is a real question.** Both realistic options are permissive and neither is wrong. The difference is narrow but not zero:

- **MIT** — shortest, most common in Go CLI tooling, near-universally understood. No explicit patent grant.
- **Apache-2.0** — adds an express patent grant and a trademark clause. Common where corporate contribution or adoption is expected; slightly heavier to read.

There is no meaningful copyleft option under consideration given the intent to make this usable, and all three dependencies (pgx, notnil/chess, pgvector-go) are permissively licensed and constrain nothing.

**What makes it consequential.** Practically irreversible. Relicensing after contributions arrive requires consent from every contributor, which in practice means it does not happen. This deserves twenty minutes of thought, not a coin flip — but also not a week.

**Recommendation: MIT.** It matches the ecosystem norm for a project of this size and shape, and the patent-grant advantage of Apache-2.0 has little purchase on a chess analysis tool. Choose Apache-2.0 only if corporate adoption is a specific goal.

---

## Q2 · Is `NUM_WORKERS` supposed to default to 4 or 8?

**Blocks:** P0-4 — the fix differs depending on the answer.

**Why it is a real question.** The README says `8 (4 for less compute)`; `cmd/data/main.go:112` returns `4`. This is not obviously a documentation bug — the phrasing reads like the default *was* 8 and was deliberately lowered, in which case the README is stale rather than wrong-by-typo. Only the maintainer knows which of the two reflects current intent.

It matters because each worker spawns its own Stockfish process at depth 12 (`cmd/data/worker.go:141`, `cmd/data/main.go:22`). On a 4-core machine, 8 workers oversubscribes badly; on a 16-core machine, 4 leaves most of the box idle. The parenthetical also reads as advice rather than a value, which is itself worth cleaning up.

**Recommendation:** keep the code's `4` as a conservative default that works on modest hardware, fix the README to match, and add the `NUM_WORKERS` tuning guidance from P3-3 so users with more cores know to raise it. A hardware-aware default (`runtime.NumCPU()`) is tempting but would be a behavior change; if desired, do it deliberately rather than as part of a doc fix.

---

## Q3 · Does Docker Compose become the primary onboarding path, or sit alongside native install?

**Blocks:** P3-1, and shapes P1-3 (CONTRIBUTING) and P3-2 (Makefile).

**Why it is a real question.** Compose eliminates the worst onboarding step — building pgvector from source (audit §5.1) — but only for Postgres. Ollama and Stockfish stay native regardless: containerizing Ollama complicates GPU passthrough, and Stockfish wants host CPU. So the choice is between two imperfect stories:

- **Compose primary:** `docker compose up -d` replaces three steps, but the docs must explain that only *part* of the stack is containerized, and Docker becomes a prerequisite for the recommended path.
- **Native primary, Compose as an alternative:** no new prerequisite, but the documented default keeps the step most likely to make people give up.

Maintaining both paths in the docs is real ongoing cost — two sets of setup instructions that drift.

**Recommendation:** make Compose the **documented default** for the database only, with native install kept as a clearly-labeled alternative section. Be explicit up front that Ollama and Stockfish are host installs either way, so nobody expects a single-command full stack. The partial containerization is worth being loud about rather than discovering.

---

## Q4 · Should `internal/engine` be promoted to a public package?

**Blocks:** P4-3, and influences how P2-2's refactor is designed.

**Why it is a real question.** `internal/engine` is genuinely the most reusable code here — a compact Stockfish UCI wrapper with CPL computation and move classification, coupled to chesser only via `models.MoveAnalysis`. Other Go chess projects plausibly want exactly this.

Against that: a public package is a **maintenance commitment**. Breaking changes need a major version. And the current API is not one to freeze — `AnalyzeGame` takes a caller-owned `*uci.Engine` (`internal/engine/stockfish.go:87`), which would make `notnil/chess`'s API part of chesser's public contract transitively, and it has no `context.Context` on a loop running two engine calls per move. Both are near-impossible to fix after publication.

The honest uncertainty is **demand**: publishing a package nobody imports is pure cost, and there is currently no evidence anyone wants it.

**Recommendation:** do the **P2-2 refactor regardless** — extract the analyzer interface and add `context.Context`. That is worth doing purely for testability, and it happens to produce exactly the API a public package would need. Then **defer the promotion decision** until someone actually asks. This keeps the option open at no extra cost and avoids committing to a contract prematurely.

---

## Q5 · Should the `gofmt` sweep use `.git-blame-ignore-revs`, and is anything in flight?

**Blocks:** P1-1.

**Why it is a real question.** The sweep touches 24 of 30 files and will pollute `git blame` for one commit. `.git-blame-ignore-revs` fixes that and GitHub honors it — but it is an extra file and an extra step, and some maintainers find it more ceremony than the problem warrants on a young project.

The sharper half of the question: **is there any unpushed or in-flight work?** A repo-wide reformat invalidates every branch that predates it. The audit saw a clean tree at `2bcd4cb`, but that only covers this machine.

**Recommendation:** add `.git-blame-ignore-revs` — it costs one file and one line, and `git blame` on the reformatted files is otherwise degraded permanently. Confirm no unpushed branches exist before running the sweep; if any do, land or discard them first.

---

## Q6 · Discussions or issues-only?

**Blocks:** P1-4 (the issue template's `config.yml` routes traffic based on this).

**Why it is a real question.** Setup for this project is genuinely hard — Postgres, pgvector, Ollama, two models, Stockfish. A predictable share of early inbound will be "my setup doesn't work," which is support, not bug reports. Enabling Discussions routes that traffic away from Issues and keeps the bug tracker meaningful. But Discussions is a second inbox to monitor, and an unanswered Discussions tab signals neglect as loudly as an unanswered issue tracker.

**Recommendation:** **issues-only initially**, with a well-built bug template (P1-4) that front-loads environment questions, plus the troubleshooting section (P3-4) to deflect the common cases. Enable Discussions only if support volume actually materializes. One neglected inbox is better than two.

---

## Q7 · Is `FUNDING.yml` wanted?

**Blocks:** nothing. Listed because the audit brief asked.

**Why it is a real question.** Purely a maintainer preference with no technical bearing. A sponsor button on a young project with no users can read as premature; on the other hand it costs nothing and is trivially removed.

**Recommendation:** skip for now. Revisit if the project gains real users.

---

## Q8 · Should the phase-boundary logic in `internal/summary` be verified before or during test-writing?

**Blocks:** P2-1 — specifically, whether it is a test-writing task or a test-and-fix task.

**Why it is a real question.** `ExtractSummaryData` categorizes moves by phase using `i < OpeningEnd` where `OpeningEnd = 10` (`internal/summary/generator.go:10, 104`). But `i` indexes **plies** (individual half-moves), while the constant's comment reads `// moves 1-10`. If "moves" means full moves, the boundary should be at ply 20, not 10 — meaning the "opening" phase currently covers only the first 5 full moves.

This may well be intentional. It also may be an off-by-factor-of-two that has been silently skewing every phase statistic, every `weakestPhase` result, and therefore every generated summary and embedding.

**Why it needs a maintainer, not a fix.** Only the author knows which was meant. And if it is a bug, fixing it changes the meaning of already-stored summaries and embeddings — existing data would need regeneration to stay consistent with new data, which is a migration decision, not a code decision.

**Recommendation:** answer this **before** writing the P2-1 tests, so they encode intended behavior rather than freezing current behavior. If it turns out to be a bug, note in the changelog (P4-2) that re-ingestion is recommended — and fold that in with the same guidance from the P0-6 embedding fix, since both point at the same remedy.
