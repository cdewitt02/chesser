# Golden reference — Phase 0

Captured by `go run ./cmd/golden cdew4` from the Go implementation, so the Python
port can be **diffed** against it rather than trusted
([`docs/python-rewrite/00-plan.md`](../../docs/python-rewrite/00-plan.md) Phase 0).

## Capture point

| | |
|---|---|
| Capture commit | `efb8f49` (`determinism: sort keyword-map iteration in QueryParser`) |
| `ANALYSIS_DEPTH` | **12** — frozen for the duration of the rewrite |
| `NumSimilar` / `DetailLimit` | 100 / 10, mirroring `cmd/chat/main.go` |
| Username | `cdew4` |
| Corpus fingerprint | `db9ac094e998b68b` |
| Game count | 74 |
| Embedder at capture | `ollama` / `nomic-embed-text` |

Any behavior change after that commit invalidates these files and forces a
deliberate recapture. **A golden regenerated from the current tree always matches
the current tree and proves nothing** — regenerate only when a behavior change is
intended, and update this table when you do.

`ANALYSIS_DEPTH` is a hardcoded constant today. Changing it rewrites every `cpl`
and `classification`, so it must not be touched while the port is in flight.

Verified: three consecutive captures produce byte-identical output.

## The two sets have different validity conditions

| File | Scope | Survives a growing corpus? |
|---|---|---|
| `eval_helpers.json` | Pure arithmetic over a fixed grid | **Yes** — no corpus involved |
| `classification.json`, `parsing.json` | Pure functions over a fixed question set | **Yes** |
| `summaries.json` | Keyed per game UUID | **Yes** — each game's values depend only on that game |
| `prompts/` | Whole-corpus | **No** — every added game shifts win rates, CPL averages, and the comparison strings built from them |

`prompts/manifest.json` carries the corpus fingerprint. The harness compares
fingerprints first and refuses to run when they differ, reporting *corpus
changed, recapture required* rather than emitting a diff that looks like a port
bug.

## What is and is not committed

**Committed:** `eval_helpers.json`, `classification.json`, `parsing.json`. These
are corpus-independent — a grid of integers and a fixed question set — so they
are portable, reviewable, and are the regression suite `internal/engine`,
`internal/chat`, and `internal/search` have never had.

**Gitignored:** `summaries.json` and `prompts/`. Both are derived from one
person's real corpus, and both embed **other players' Chess.com usernames**:
Chess.com's termination strings are of the form `"Bolzman0 won by resignation"`,
so a single assembled prompt carries 31 third-party handles. That is
[readiness P0-8](../../docs/opensource-readiness/01-roadmap.md) — a live
disclosure problem in the prompt path — and committing the capture would move it
from *sent to a provider* to *published in a repository*.

They are regenerated locally from the database in seconds, which is the whole
point: the database is the source of truth, and it is language-neutral.

## Note on `parsing.json`

`date_from` is recorded as a boolean flag, never a timestamp. It is `now()` minus
a duration, so a captured value would fail one second after capture while telling
you nothing about the port.

The corresponding prompts are stable for a different reason: every row's
`created_at` falls inside even the narrowest window any frozen question opens, so
the date filter currently selects the whole corpus regardless of when the harness
runs. That will stop being true once the corpus contains rows older than
60 days — at which point `prompts/09.txt` needs recapturing.

## Regenerating

```sh
. ./.env
go run ./cmd/golden cdew4     # requires Postgres and, for prompts/, Ollama
```

The capture also re-derives every summary from stored `games` and `moves` rows
and compares it against the stored `summary_text`. A mismatch is reported loudly:
it means the Go tree has drifted from what produced the stored embeddings, and
the goldens would freeze that drift. At the capture commit, **74 of 74 match**.
