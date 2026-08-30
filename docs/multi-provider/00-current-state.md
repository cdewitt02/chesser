# Multi-Provider Support — Current State Audit

**Scope.** Every place chesser talks to an LLM today, what it sends, what it expects back, and which
assumptions are Ollama-specific. Design-only; nothing here has been changed.

**Related prior work.** [`../opensource-readiness/01-roadmap.md`](../opensource-readiness/01-roadmap.md)
already flagged provider lock-in as a P2 item (§ "Ollama is hardcoded throughout", lines 403–409) and
several of the bugs below as P0 items. This document supersedes that summary with the detail needed to
design an abstraction.

---

## 1. The single Ollama client

All Ollama traffic in the repo originates from **one file**: `internal/embeddings/ollama.go` (131 lines).
There is no other HTTP client targeting Ollama, no SDK dependency, and nothing in `go.mod` related to
any LLM provider — the client is hand-rolled `net/http` + `encoding/json`.

```
internal/embeddings/ollama.go:13-17   Client{baseURL, httpClient, model}
internal/embeddings/ollama.go:45-53   New(baseURL, model) — 10s http.Client timeout
internal/embeddings/ollama.go:55-87   GetEmbedding(text) ([]float32, error)
internal/embeddings/ollama.go:90-131  Chat(model, messages) (string, error)
```

Note the package name is `embeddings` but it also owns chat. That naming is already misleading and gets
worse under a multi-provider design.

### 1.1 `GetEmbedding` — the embedding call

| | |
|---|---|
| **Endpoint** | `POST {baseURL}/api/embeddings` (`ollama.go:67`) |
| **Request** | `{"model": <c.model>, "prompt": <text>}` (`ollama.go:20-23, 57-60`) |
| **Response** | `{"embedding": [...]}` decoded into `[]float32` (`ollama.go:26-28`) |
| **Model source** | Bound at construction, `Client.model` — *not* a per-call argument |
| **Timeout** | 10s, from the shared client (`ollama.go:49`) |
| **Retries** | None |
| **`context.Context`** | **Not accepted.** Uses `httpClient.Post`, so the call is uncancellable |
| **Batching** | None — one HTTP round trip per text |

Three defects matter for the abstraction design:

- **No status check.** `ollama.go:69-86` never inspects `resp.StatusCode`. An Ollama error body
  unmarshals cleanly into `embeddingResponse{Embedding: nil}` and the function returns `(nil, nil)`.
  The nil vector flows to `cmd/data/worker.go:35-38` (no error seen) and then into the `vector(768)`
  column via `SaveGameSummary` (`worker.go:90`). A hosted provider returning 401/429 would hit exactly
  this path. **Any abstraction must not preserve this behavior**, which means the wrap-with-no-behavior-
  change phase has one deliberate exception (see the migration plan).
- **Bare errors.** `ollama.go:64, 71, 77, 83` return unwrapped errors, so failures arrive with no
  indication of which provider or which call produced them.
- **10s is too short for a cold model.** Ollama loads the model into memory on first request; that can
  exceed 10s and surfaces as an opaque timeout.

`Chat` in the same file does all three correctly — this is an oversight, not a convention.

### 1.2 `Chat` — the chat call

| | |
|---|---|
| **Endpoint** | `POST {baseURL}/api/chat` (`ollama.go:102`) |
| **Request** | `{"model": <arg>, "messages": [{"role","content"}...], "stream": false}` (`ollama.go:35-39`) |
| **Response** | `{"message": {"role","content"}}` — only `.Message.Content` is read (`ollama.go:130`) |
| **Model source** | **Per-call argument**, unlike embeddings |
| **Timeout** | 120s, from a **new `http.Client` constructed per call** (`ollama.go:104-106`) |
| **Retries** | None |
| **`context.Context`** | **Not accepted** |
| **Streaming** | Hardcoded off (`ollama.go:94`) |
| **Sampling params** | None — no temperature, top_p, max_tokens, seed, stop sequences |
| **Tool calling** | Not supported, not used |
| **Usage/token accounting** | Discarded — Ollama returns `prompt_eval_count`/`eval_count`, the struct at `ollama.go:41-43` ignores them |

The per-call client at `ollama.go:104-106` also discards the connection pooling that the comment on
`ollama.go:15` says the shared client exists to provide.

**The model-source asymmetry is a real design constraint.** Embedding model is per-*client*; chat model
is per-*call*. Any unified interface has to pick one, and the two call sites have different natural
answers (see §5.1).

---

## 2. Call sites

### 2.1 Chat-style calls (2 sites, 1 live)

| Site | Caller | Live? |
|---|---|---|
| `internal/chat/service.go:100` | `Service.Ask` — the REPL path | **Yes** |
| `internal/chat/service.go:178` | `Service.AskWithDetails` | **No** — unreferenced anywhere in the repo |

`Ask` builds its message slice at `service.go:94-98`:

```
[0]  {role: "system",    content: <router-built prompt>}
[1:] <conversation history, alternating user/assistant>
[n]  {role: "user",      content: <raw question>}
```

History is appended as strict user/assistant pairs (`service.go:105-108`) and truncated to
`maxHistoryPairs * 2` messages, default 4 pairs (`service.go:45-48, 119-124`). `cmd/chat/main.go:87-92`
never sets `MaxHistoryPairs`, so the default always applies.

`AskWithDetails` builds a two-message slice (`service.go:173-176`) and additionally wraps the user's
question via `PromptBuilder.WrapUserQuestion` (`prompts.go:168-178`). It is dead code today, but it is
the *only* caller of `WrapUserQuestion`, and that wrapper is the most provider-sensitive prompt in the
repo (§4.3). Decide explicitly whether to port or delete it — do not port it by inertia.

`PromptBuilder.BuildFollowUpPrompt` (`prompts.go:158-166`) is also unreferenced.

### 2.2 Embedding calls (2 sites, both live)

| Site | Caller | Frequency |
|---|---|---|
| `cmd/data/worker.go:35` | `Worker.ProcessGame` — embeds each generated game summary during ingestion | Once per game, across `NUM_WORKERS` goroutines |
| `internal/search/search.go:80` | `HybridSearcher.Search` — embeds the semantic remainder of the user's query | Once per user question |

The search-time call is reached from the chat path through `chat/router.go:372-379` →
`search.HybridSearcher`, wired at `chat/service.go:51`.

Note the ingestion call sits inside a goroutine pool with a shared cancellable context
(`worker.go:114-202`), but `GetEmbedding` cannot observe that context — a cancelled run still blocks up
to 10s per in-flight embedding.

### 2.3 Construction sites — and the hardcoding bug

```
cmd/chat/main.go:84     embeddings.New(ollamaURL, embedModel)              // honors env
cmd/data/main.go:251    embeddings.New("http://localhost:11434", "nomic-embed-text")  // HARDCODED
```

`cmd/data` ignores both `OLLAMA_URL` and `OLLAMA_EMBED_MODEL` despite the README documenting them as
global. This is already tracked as P0 in the readiness roadmap
([`01-roadmap.md:73`](../opensource-readiness/01-roadmap.md)). For this project it matters because a
provider-selection mechanism added only at `cmd/chat` would reproduce the same split-brain: chat on
Anthropic, ingestion silently still on local Ollama, with embeddings from a different model than the
ones in the index. **Fixing the hardcode is a prerequisite for provider selection, not a nice-to-have.**

---

## 3. Existing abstraction seams

One interface already exists and is exactly the right shape:

```go
// internal/search/search.go:8-10
type EmbeddingClient interface {
    GetEmbedding(text string) ([]float32, error)
}
```

`*embeddings.Client` satisfies it structurally. But it is only used *inside* `internal/search`; every
composition root still passes the concrete type:

- `chat.Service.ollama` is `*embeddings.Client` (`service.go:14`), and `NewService` takes the concrete
  type (`service.go:34`) — because the same object serves both the embedder role (passed to
  `NewHybridSearcher`, `service.go:51`) and the chat role (`service.go:100, 178`).
- `Worker.embeddingClient` and `WorkerPool.embeddingClient` are concrete (`worker.go:22, 100, 104`).

**That double duty is the crux of the whole design.** One concrete type currently plays two roles that
different providers implement differently — or, in Anthropic's case, one of which it does not implement
at all (§4.1).

There is no `ChatCompleter`-equivalent interface anywhere.

---

## 4. Provider-specific behavior baked into the code

### 4.1 Anthropic has no embeddings endpoint

The decisive fact for this design. Anthropic offers no embeddings API and points users to third parties
(e.g. Voyage). So "pick a provider" cannot mean one provider for both roles — a user selecting Anthropic
must still get embeddings from somewhere else. Any single unified `Provider` interface would need
Anthropic's `Embed` to return "unsupported" at runtime, which is a type that lies about its capabilities.

### 4.2 The `vector(768)` schema constraint

```
internal/db/schema.go:68   embedding vector(768)
internal/db/schema.go:72   ON game_summaries USING ivfflat (embedding vector_cosine_ops)
```

`nomic-embed-text` emits 768 dimensions, so the default works. Consequences:

- **The declared width is less of an obstacle than it looks.** OpenAI's `text-embedding-3-small`
  accepts a `dimensions` parameter and can emit 768 directly, which this column accepts with no
  migration at all. See [`04-onboarding.md`](./04-onboarding.md) §5.
- **The vector space is the real constraint.** nomic-768 and OpenAI-768 are the same width and different
  spaces. Mixed vectors in one column are silently wrong — cosine distance across embedding spaces is
  meaningless, so retrieval degrades without erroring. Nothing today records which model built the index.
- **Mitigating factor:** `game_summaries` stores `summary_text` alongside the embedding, and summaries
  are generated deterministically by `internal/summary/generator.go:144+` with **no LLM involved**. So
  re-embedding a corpus means re-reading stored text and updating vectors — it does **not** require
  re-running Stockfish. That makes embedding-provider swaps a bounded re-embed pass rather than a schema
  migration.

### 4.3 Prompt conventions that assume a small local model

- **The bracketed in-user-turn wrapper.** `prompts.go:168-178` prepends
  `[IMPORTANT: You are a chess coach for X. Only discuss chess...]` to the *user* message. This is a
  small-model steering workaround. Larger hosted models honor the system prompt without it, and on
  Anthropic in particular, instructions smuggled into the user turn can read as an injection attempt
  and produce hedging. Only reachable from the dead `AskWithDetails`.
- **Single leading system message.** `service.go:94-96` puts the system prompt as `messages[0]`.
  Ollama and OpenAI both accept this. **Anthropic does not** — `system` is a top-level request
  parameter, and `messages` must contain only user/assistant. An Anthropic adapter has to lift it out.
- **Alternation assumptions.** Anthropic requires messages to begin with `user` and strictly alternate.
  Today's history is appended in clean user/assistant pairs (`service.go:105-108`), so this holds — but
  it holds by accident, not by construction. If a future change appends a partial pair or a
  system-message mid-conversation, only Anthropic breaks.
- **Heavy uppercase/arrow formatting.** `router.go:430-723` emits `PLAYER OVERVIEW:`, `→ STRONGEST
  time control: ...`, and imperative bullet blocks. This is portable, but the pre-computed comparison
  strings (`router.go:276-295`, e.g. `(3.2% ABOVE overall)`) exist specifically to spare a weak model
  from doing arithmetic. Against a strong model they are harmless redundancy; they are worth keeping
  precisely because they make the comparison across providers fairer.

### 4.4 Response parsing assumes a single plain-text block

`ollama.go:124-130` decodes into `chatResponse{Message ChatMessage}` and returns `.Content` — one
string, always. This breaks on:

- **Anthropic**, which returns `content` as an **array of typed blocks**; a naive port yields empty
  string.
- **Reasoning models** (OpenAI o-series, Claude extended thinking), which add blocks the parser must
  skip rather than concatenate.
- **Empty completions / refusals / `finish_reason: length`** — all currently indistinguishable from a
  short answer. Nothing checks for empty content anywhere.

### 4.5 Prompt size — bounded in practice, but larger than Ollama's default context

Nothing counts tokens and nothing truncates on size, but the prompt is **not** unbounded. The bounds are
incidental rather than designed, which is the actual problem.

- `writeGameContext` (`router.go:395-396`) caps summaries at `detailLimit`, default **10**
  (`cmd/chat/main.go:22`). Aggregate and Comparative queries truncate to 3 first (`router.go:360-363`).
- The stats blocks in `writePlayerStats` (`router.go:430-509`) are bounded by real cardinality: colors
  (2), time classes (~5), rating bands (~6), openings (top 5 via `writeOpeningStats`). Only
  `StatsByTermination` is genuinely uncapped, and its cardinality is small.
- Plus the trend block (`router.go:726-768`), any mentioned-opening block (`router.go:771-802`), and 4
  turns of history.

A realistic prompt is therefore **~3k input tokens**, with ~500 out.

**`defaultNumSimilar = 100` (`cmd/chat/main.go:21`) does not affect prompt size at all.** It sets
`TopK` for retrieval (`router.go:115`), so 100 games are fetched from Postgres with full record joins
and 90 of them are discarded before the prompt is built. That is wasted query work per question, not
prompt bloat — and it means `NumSimilar` is not a cost lever. Only `DetailLimit` is.

The two consequences of the ~3k figure:

- **Cost on a hosted provider is negligible** — a fraction of a cent per question. Bill shock is not a
  real risk here, and token budgeting does not merit priority.
- **On Ollama today the prompt likely exceeds the default `num_ctx`** (2048–4096 depending on version),
  so it is being silently truncated *right now*. `writeInstructions` is emitted **last** in
  `BuildPrompt` (`router.go:160`), which makes the instructions the most likely casualty. This is a live
  quality bug on the default local setup, and it will confound any local-vs-hosted comparison — see
  [`03-eval-plan.md`](./03-eval-plan.md) §5.

That truncation asymmetry, not cost, is the sharpest behavioral difference a provider swap exposes.

### 4.6 Debug output

`service.go:111-114` prints the entire system prompt to stdout on every question. Harmless locally;
noisy and prompt-leaking once real API traffic is involved.

---

## 5. Config and environment handling today

| Variable | Read at | Default | Notes |
|---|---|---|---|
| `DATABASE_URL` | `cmd/chat/main.go:46`, `cmd/data/main.go:148,221`, `internal/db/db.go:17` | — (required) | |
| `OLLAMA_URL` | `cmd/chat/main.go:52` **only** | `http://localhost:11434` | Ignored by `cmd/data` (§2.3) |
| `OLLAMA_EMBED_MODEL` | `cmd/chat/main.go:57` **only** | `nomic-embed-text` | Ignored by `cmd/data`; must be 768-dim (§4.2) |
| `NUM_WORKERS` | `cmd/data/main.go:106-107` | 8 (4 documented) | |

**The chat model is not an env var.** It is `os.Args[2]`, a positional CLI argument, defaulting to
`llama3.2` (`cmd/chat/main.go:18, 41-44`). So the project's conventions are:

- **Environment** for infrastructure endpoints and tuning knobs.
- **Positional CLI args** for per-invocation choices (username, chat model).

Provider selection has to slot into that split coherently rather than inventing a third mechanism.
`.env` is gitignored (`.gitignore`), and the project currently needs **no third-party credentials at
all** — Chess.com's public API is unauthenticated (`internal/api/data.go:16`) and Ollama is local. API
keys would be the first secret this project has ever handled.

There is no config file, no flag package usage, no `LoadConfig` function, and no central config struct.
`chat.Config` (`service.go:26-32`) is the closest thing and it is populated inline at the call site.

---

## 6. Test coverage

**Total LLM-related test coverage: zero.**

The only test file in the repo is `internal/search/parser_test.go` — 6 test functions
(`TestQueryParser_Parse`, `TestQueryParser_SemanticRemainder`, `TestGameFilters_BuildWHERE`,
`TestGameFilters_Clone`, `TestGameFilters_IsEmpty`, `TestGameFilters_String`). All are pure unit tests
over query parsing and filter construction. None touches `EmbeddingClient`, and `HybridSearcher.Search`
— the function that calls it — is untested.

Testability blockers:

- `embeddings.Client` has an unexported, non-injectable `*http.Client` (`ollama.go:13-17`), so there is
  no `httptest` seam without refactoring.
- `chat.Service` depends on the concrete client (`service.go:14, 34`), so the chat path cannot be
  exercised without a live Ollama.
- No fake implements `search.EmbeddingClient`, despite the interface existing.

What multi-provider testing needs to add:

1. **Adapter-level tests per provider** against `httptest.Server` fixtures — canned JSON for each
   provider's success shape, plus 401/429/500 and malformed bodies. These are the tests that would have
   caught the `(nil, nil)` bug, and they run with no network and no Ollama.
2. **A shared conformance suite** — one table of cases run against every adapter, asserting that all
   providers normalize to the same `(text, error)` semantics: empty content is an error, non-2xx is an
   error, a truncated response is distinguishable.
3. **A fake embedder + fake chat model** in a testing helper package, so `HybridSearcher.Search`,
   `QueryRouter.Route`, and `Service.Ask` become testable at all — an independent win that the current
   design blocks.
4. **Live provider tests** gated behind an env var and build tag, never run in CI. Real API calls are
   nondeterministic and cost money; they belong in the eval harness
   ([`03-eval-plan.md`](./03-eval-plan.md)), not the test suite.

---

## 7. Audit findings that constrain the design

1. Anthropic has no embeddings API → chat and embeddings **cannot** share one provider concept (§4.1).
2. `vector(768)` is survivable via `dimensions=768`, but same-width/different-space vectors degrade retrieval silently — the index needs a provenance stamp, not a schema migration (§4.2).
3. `cmd/data` hardcodes the Ollama endpoint → must be fixed before selection means anything (§2.3).
4. No `context.Context` on either method → the interface should add it, which is a signature change at
   every call site (§1).
5. Anthropic's system-parameter and alternation rules break the current message assembly (§4.3).
6. Single-string response parsing breaks on block-based and reasoning responses (§4.4).
7. Prompt is ~3k tokens — cheap on hosted providers, but likely truncated by Ollama's default
   `num_ctx`, cutting the instructions that are emitted last (§4.5).
8. Chat model is a CLI positional; embed model is env — provider selection must respect both (§5).
9. Zero test coverage on every call site being changed (§6).
