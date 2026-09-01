# Contributing

## Setup

```bash
uv venv
uv pip install -e ".[dev]"
. ./.env                       # copy .env.example first
```

You will also need PostgreSQL with pgvector, Stockfish on `PATH`, and — for the
default configuration — Ollama with `nomic-embed-text` and `llama3.2` pulled.

## The checks

```bash
ruff check . && ruff format --check .
mypy
pytest
```

All three must pass. CI runs exactly these.

**`mypy --strict` is not optional and not negotiable down.** It is configured in
`pyproject.toml` and it is what replaced the Go compiler; a `# type: ignore`
needs a reason next to it.

## The testing matrix

Tests are marked by what they need, so a bare `pytest` says what it did not
check rather than skipping silently.

| Marker | Needs | Skips when |
|---|---|---|
| *(unmarked)* | nothing | never — these must always run |
| `corpus` | `DATABASE_URL` pointing at a populated database | it is unset |
| `golden` | locally regenerated corpus goldens | `testdata/golden/prompts/` is absent |
| `ollama` | a running Ollama server | it is unreachable |

```bash
pytest -m "not corpus"          # the portable subset — what a fresh clone runs
pytest                          # everything the environment supports
```

The corpus-backed tests run against the **live** database on purpose. A
float-conversion bug in the vector path is exactly the kind of defect a fixture
would reproduce faithfully and wrongly.

## The goldens

`testdata/golden/` began as the parity reference from the Python rewrite and is
now the regression suite for the packages that never had one. **Read
[`testdata/golden/MANIFEST.md`](testdata/golden/MANIFEST.md) before touching
anything in it** — a golden regenerated from the current tree always matches the
current tree and proves nothing.

**They are frozen at the cutover and there is no capture tool.** The Go
implementation that produced them was deleted, so they are no longer a
cross-language reference: they are a Python-vs-Python regression suite pinned to
the behavior that shipped. Regenerating any of them now requires first writing a
Python capture tool, which is a deliberate change with its own verification, not
a step you take to make a red test green.

Three of the five files are gitignored because they derive from one person's
real corpus and embed other players' usernames (readiness P0-8):
`summaries.json`, `analysis.json`, and `prompts/`. A fresh clone will not have
them, and the tests that need them skip with a message saying so — that is
expected, not a broken checkout.

## Two preserved defects

`chesser/summary.py` carries two bugs on purpose, each marked `PARITY:` and
listed in [`docs/python-rewrite/00-plan.md`](docs/python-rewrite/00-plan.md).
A test asserts they are still present.

**Do not fix them incidentally.** They are Phase 8 items with their own
verification, and fixing either changes the Game Summary text — which changes
the embedded text, which makes every stored vector stale relative to its own
source. Each needs a regeneration pass alongside it.

## Things that are load-bearing

A few properties are easy to break with a change that looks like a cleanup:

- **Every dict that reaches the assembled prompt is iterated sorted.** The
  prompt must be reproducible across runs; `docs/multi-provider/03-eval-plan.md`
  depends on it, and so does every golden.
- **Adapters never send a parameter the caller did not set** — a stray
  temperature default would change answer distributions with prompt parity fully
  intact. The conformance suite asserts this.
- **Retry ownership sits with the provider SDKs.** Never layer an adapter-level
  loop on top; three attempts become nine against an endpoint that just said
  429.
- **An empty or missing result is an error, not a success.** This project's
  characteristic bug is silence: an empty embedding reaching a `vector(768)`
  column, an error body parsing into an empty game list.

## Commits

One phase or one concern per commit, with a message that says *why*. The commit
log is the design record for anything not written down in `docs/`.
