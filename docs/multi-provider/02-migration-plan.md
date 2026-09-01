# Multi-Provider Support — Migration Plan

Phased plan for landing [`01-design.md`](./01-design.md) without a single disruptive rewrite. **This is
a plan, not implementation.**

Each phase is independently mergeable and leaves `main` working. Phases 0–2 change no user-visible
behavior; the first behavior change a user can see arrives in Phase 3, and only if they opt in.

**Scope note.** [`04-onboarding.md`](./04-onboarding.md) §2.2 establishes that hosted *embeddings* are
main-line rather than Phase 6 work: without them, switching chat providers still leaves Ollama a
prerequisite and the onboarding gain is about six minutes. Phases 2 and 4 below carry that. Storage is
settled separately in [`ADR 0001`](../adr/0001-postgres-in-docker-over-sqlite.md) — Postgres stays, so
`vector(768)` stays with it.

**Verification reality check.** The repo has one test file — `internal/search/parser_test.go`, six pure
unit tests, and **zero LLM-related coverage** of any kind. There is no regression suite to lean on,
so most verification below is either *new* tests written as part of the phase or explicit manual steps.
Pretending otherwise would be the fastest way to break ingestion silently.

**A baseline worth capturing before Phase 0:** run `cmd/chat` against a real database and save the
answers to the ten eval questions from [`03-eval-plan.md`](./03-eval-plan.md) with the current Ollama
setup. That file is the only "before" artifact that exists for the chat path, and it doubles as the
first eval data point.

---

## Phase 0 — Fix the config split-brain

**Prerequisite, not part of the abstraction.** Small, and everything after it depends on it.

- `cmd/data/main.go:251` hardcodes `embeddings.New("http://localhost:11434", "nomic-embed-text")`,
  ignoring `OLLAMA_URL` and `OLLAMA_EMBED_MODEL` — which `cmd/chat/main.go:84` honors. Ingestion is
  therefore silently on a different endpoint and model than chat, so a provider chosen for chat leaves
  the index built by something else. Make it read the same env vars as `cmd/chat/main.go:52-60`.
- Lift that resolution into one shared place so the two entrypoints cannot drift again.

**Why first.** Provider selection wired into `cmd/chat` alone would give a user chat on Anthropic and
embeddings silently still on local Ollama — with query vectors from a different model than the index.
That is worse than no feature.

**Verification.** `go build ./...`. Run ingestion on a handful of games with `OLLAMA_URL` set to a
deliberately wrong port and confirm it now *fails* instead of quietly succeeding. Reset and confirm a
normal run still works.

**Backward compatibility.** This is a real behavior change for anyone who set `OLLAMA_URL` and worked
around it being ignored. Vanishingly unlikely, and the alternative — documented config that is silently
discarded — is worse. Note it in the changelog.

---

## Phase 1 — Define the interfaces, wrap Ollama, change nothing else

Introduce `internal/llm` with `ChatModel`, `Embedder`, `Message`, `ChatRequest`, `ChatResponse`,
`Usage`, and the sentinel errors ([`01-design.md` §3](./01-design.md)). Add `internal/llm/ollama` as the
one implementation, ported from `internal/embeddings/ollama.go`.

Change dependents to accept interfaces:

- `chat.Service.ollama` → two fields (`chat llm.ChatModel`, and the embedder passed through to
  `search.NewHybridSearcher`), replacing the concrete `*embeddings.Client` at `service.go:14, 34, 51`.
  This is where the double duty of one concrete object finally comes apart: `*embeddings.Client`
  currently plays both the embedder role (passed to `NewHybridSearcher`, `service.go:51`) and the chat
  role (`service.go:100, 178`) — two roles that different providers implement differently, and one of
  which Anthropic does not implement at all.
- `Worker.embeddingClient` / `WorkerPool.embeddingClient` → `llm.Embedder`
  (`worker.go:22, 100, 104`).
- `internal/search` needs **no change** — `llm.Embedder` satisfies `search.EmbeddingClient` structurally,
  once `Embed` is adapted at the call boundary.

Delete `internal/embeddings` when nothing imports it.

**Deliberate behavior changes in this phase** — "no behavior change" cannot be taken literally, because
some current behavior is a bug:

1. **Status codes are now checked on embeddings.** `GetEmbedding` currently returns `(nil, nil)` on an
   error response and lets a nil vector reach the `vector(768)` column: `ollama.go:69-86` never
   inspects `resp.StatusCode`, so an error body unmarshals cleanly into `{Embedding: nil}` and the nil
   flows through `worker.go:35-38` into `SaveGameSummary`. A hosted provider returning 401 or 429 would
   take exactly this path. (`Chat` in the same file checks status correctly — this is an oversight, not
   a convention.) The port must check status and reject empty
   vectors. Ingestion that used to "succeed" against a broken Ollama will now fail — correctly.
2. **`context.Context` is threaded through.** Cancelling ingestion now actually cancels in-flight calls.
3. **Timeouts unify.** Replace the 10s embed / 120s chat split and the per-call client at
   `ollama.go:104-106` with one pooled client and per-operation timeouts. Raise the embedding timeout
   above 10s — cold-model load can exceed it.
4. **Errors get wrapped** with provider and operation context.

The Ollama adapter stays hand-rolled `net/http` ([`01-design.md` §11](./01-design.md)) and deliberately
does **not** gain a retry loop: it talks to a local process, so a dial failure means "not running" and
retrying only delays telling the user that (§7.2).

Everything else stays identical: same endpoints, same JSON, same prompts, same `stream: false`, same
defaults.

**Delete, do not port:** `AskWithDetails` (`service.go:136`) and `BuildFollowUpPrompt`
(`prompts.go:158`) — both unreferenced. Deleting `AskWithDetails` also removes the only caller of
`WrapUserQuestion` (`prompts.go:168-178`), which is a small-model workaround that would need redesigning
to survive a provider swap ([`01-design.md` §10](./01-design.md)). This removes one of the two chat call
sites, leaving `Service.Ask` as the only one the interface has to serve.

**Verification.**
- `go build ./...`, `go vet ./...`, `go test ./...` (the six parser tests must still pass — they are
  untouched, so this proves the refactor did not break compilation of `internal/search`).
- **New:** adapter tests against `httptest.Server` for the Ollama adapter — success fixture, 500, 401,
  malformed JSON, and a 200 with an empty embedding. These are the first tests that would catch the
  `(nil, nil)` bug, and they need no Ollama running.
- **New:** fakes in `internal/llm/llmtest` implementing both interfaces, plus a first test of
  `HybridSearcher.Search` using the fake embedder — currently untestable and untested.
- **Manual:** ingest the same small set of games before and after; the stored `summary_text` must be
  byte-identical and embeddings must match within float tolerance (same model, same input).
- **Manual:** run `cmd/chat` against the Phase-0 baseline questions. Prompts are unchanged, so with a
  fixed seed unavailable, verify the *system prompt* printed at `service.go:111-114` is byte-identical.
  That is the deterministic part, and it is the part the refactor could break.

**Backward compatibility.** Total. Same env vars, same CLI, same wire traffic.

---

## Phase 2 — Add the OpenAI adapter

Implement `internal/llm/openai` with both `NewChat` and `NewEmbedder`. Not yet reachable from any
entrypoint — no selection mechanism exists until Phase 4, so this phase ships a package plus tests and
zero user-visible change.

**This phase adds the first LLM dependency to `go.mod`** — the official OpenAI Go SDK
([`01-design.md` §11](./01-design.md)). Adapter responsibilities: prepend `System` as `messages[0]`; read
the completion text; map `finish_reason` onto the normalized set; populate `Usage`; drop reasoning blocks
for o-series models; map 401/429/5xx onto the sentinels ([`01-design.md` §3.3](./01-design.md)).
`Dimensions()` reports the configured model's width.

**Set the SDK's max-retries to the project policy and add no retry loop of your own**
([`01-design.md` §7.2](./01-design.md)). Wrapping an SDK that already retries yields 9 requests against a
rate-limited endpoint. This is the single most likely implementation mistake in the whole migration.

**The embedder must send `dimensions=768`** so its vectors fit the existing column with no migration
([`04-onboarding.md` §5](./04-onboarding.md)). Add the `index_meta` provenance record here too — writing
the embedder's provider and model when an index is built — because Phase 4's startup check has nothing
to compare against otherwise, and an index built before the stamp exists is indistinguishable from one
built by any provider.

**Why OpenAI before Anthropic.** It implements *both* interfaces, so it exercises the split from
[`01-design.md` §2](./01-design.md) end to end, and its message format is closest to Ollama's — the
smaller step. Anthropic's system-parameter and content-block differences are better attempted once the
conformance suite exists.

**Verification.**
- **New:** the same `httptest` fixture suite as the Ollama adapter, with OpenAI-shaped payloads.
- **New:** a **shared conformance suite** in `llmtest` — one table run against every adapter, asserting
  identical normalized semantics: non-2xx is an error, empty content is `ErrBadResponse`, truncation
  surfaces as `FinishReason == "length"`, 429 maps to `ErrRateLimited`. This is the artifact that keeps
  adapters honest as more are added. **Assert on error classification, never on attempt counts** —
  retry behavior is deliberately non-uniform, since the SDKs retry for hosted providers while Ollama
  fails fast as a local process ([`01-design.md` §7.2](./01-design.md)).
- **Manual, gated:** one live call each to chat and embeddings behind an env-gated build tag, run by
  hand, never in CI.

**Backward compatibility.** Nothing is reachable; nothing can regress.

---

## Phase 3 — Add the Anthropic adapter

Implement `internal/llm/anthropic`, **chat only**. There is deliberately no `NewEmbedder`
([`01-design.md` §2](./01-design.md)).

The three things this adapter must get right, all invisible to callers:

0. Use the official Anthropic Go SDK, with max-retries set to policy and no adapter-level retry loop
   ([`01-design.md` §11](./01-design.md), §7.2).
1. Send `System` as the top-level `system` parameter, never as a message.
2. Validate that `Messages` begins with `user` and alternates; return a clear error rather than passing
   a malformed request through. Today's history assembly (`service.go:105-108`) satisfies this by
   accident — the adapter is where it becomes guaranteed.
3. Flatten the content-block array to text, skipping thinking blocks. A naive `.content` read yields an
   empty string: `ollama.go:124-130` decodes into a single `Message.Content` string, where Anthropic
   returns `content` as an array of typed blocks.

**Verification.** Same fixture suite plus the shared conformance table — notably a fixture with a
multi-block response and one with a thinking block, asserting `Text` contains only the answer. Plus a
test asserting the system prompt does **not** appear in the messages array, and one asserting a
non-alternating input is rejected. One gated live call by hand.

**Backward compatibility.** Still unreachable. No regression surface.

---

## Phase 4 — Provider selection and startup validation

The phase that makes everything above reachable. Add resolution of `CHAT_PROVIDER`, `EMBED_PROVIDER`,
`CHAT_MODEL`, `EMBED_MODEL`, and the API keys ([`01-design.md` §4](./01-design.md)) in the shared config
helper introduced in Phase 0, used identically by `cmd/chat` and `cmd/data`.

Startup, **before** the welcome banner at `cmd/chat/main.go:94-101`:

1. Resolve config; unknown provider names fail listing valid values.
2. `EMBED_PROVIDER=anthropic` fails with a message explaining Anthropic has no embeddings API.
3. Required API key present? Fail naming the variable, never printing the value.
4. Compare `Embedder.Dimensions()` against the `vector(N)` column and refuse to start on mismatch — the
   check that turns the §4.2 landmine into one clear sentence.
5. Compare the configured embedder against the `index_meta` provenance record and refuse to start on
   mismatch, naming both and the re-embed path ([`01-design.md` §2.1](./01-design.md)). Width alone
   passes for two 768-dim models from different providers, which is exactly the silent-degradation case.
6. Reachability preflight **and model validation in one call** — Ollama `GET /api/tags`, models list
   for hosted providers ([`01-design.md` §4.4](./01-design.md)). Model absent from a successful list is a
   hard fail with the enriched message; a failed list call warns and continues, so OpenAI-compatible
   gateways that omit the endpoint still work. For Ollama this also catches "model not pulled", which is
   a top setup failure (readiness P3-4).
6b. Fix the usage text at `cmd/chat/main.go:27-36` — it documents the positional argument as *"Ollama
   model for chat"* and lists only `OLLAMA_*` variables. Left as-is it steers quick-start users straight
   into the footgun §4.4 exists to catch.
7. Print the resolved configuration — chat provider/model, embed provider/model — and, when a hosted
   provider is selected, one line stating that game summaries and the username will be sent to a third
   party.

**Backward compatibility — the binding constraint on this phase.** Both providers default to `ollama`,
and `OLLAMA_EMBED_MODEL` is honored as an alias for `EMBED_MODEL` when the embed provider is Ollama
([`01-design.md` §5](./01-design.md)). A user with only `DATABASE_URL` and `OLLAMA_URL` set must see
**identical** behavior, and the positional
`go run cmd/chat/main.go <username> [model]` must keep overriding the chat model. That constraint is why
`OLLAMA_URL` is not renamed to a uniform `<PROVIDER>_URL` and why there is no clean `LLM_*` namespace —
an accepted, deliberate asymmetry.

**Verification.**
- **New:** table-driven tests of config resolution over an env map — every precedence rule, both
  aliases, every invalid combination. Pure, fast, no network. **This is the highest-value test in the
  whole migration**, because config resolution is where backward compatibility actually lives.
- **Manual regression:** with only the pre-existing variables set, run ingestion and chat and confirm
  behavior matches the Phase-0 baseline.
- **Manual:** `CHAT_PROVIDER=anthropic` with `ANTHROPIC_API_KEY` unset must fail before the banner.
- **Manual:** `CHAT_PROVIDER=anthropic` + default Ollama embeddings against an existing index — the
  headline configuration, and it must require no re-embedding.
- **Manual:** `EMBED_PROVIDER=openai` against a 768-dim index must be refused at startup, not at insert
  time.

---

## Phase 5 — Documentation

- **README leads with the hosted quick-start** — Compose plus an API key, the ~31-minute path
  ([`04-onboarding.md` §1](./04-onboarding.md)) — with local-first as a clearly labeled alternative and
  its ~18-minute cost stated. Note that the *code* default remains `ollama` for both providers
  ([`01-design.md` §4.2](./01-design.md)); only the reading order changes.
- README: provider table **including each provider's pinned default model**
  ([`01-design.md` §4.3](./01-design.md)), the new variables, `.env.example` with empty values, and an
  explicit statement that hosted providers send game summaries and the username off-machine.
- Fix `README.md:50`, which passes `llama3.2` positionally in the quick-start example — the source of
  the footgun in [`01-design.md` §4.4](./01-design.md).
- Correct `OLLAMA_EMBED_MODEL`'s row to note the 768-dimension constraint.
- Update the project-structure block (`README.md:64-77`) — `embeddings/ # Ollama embeddings client`
  will be stale.
- CONTRIBUTING (if it exists by then): what can be tested with no provider running. After Phases 1–4
  that answer is much better than it is today — the entire adapter layer is testable with `httptest`,
  and config resolution with an env map.
- ~~Cross-link [`03-eval-plan.md`](./03-eval-plan.md) as the next step.~~ **Dropped 2026-08-31** —
  the evaluation is not being run and model choice is left to the user. The README's *Choosing a
  chat model* section carries what a reader actually needs instead.

---

## Phase 6 — Deferred, explicitly not in this migration

Listed so they are not smuggled into earlier phases:

- ~~**Streaming** via the optional `StreamingChatModel` ([`01-design.md` §9](./01-design.md)) — purely
  additive when wanted.~~ **Done, after this migration**, exactly as designed: both adapters implement
  the optional interface, `chat.Service.AskStream` type-asserts, and nothing about `ChatModel` changed.
  It shipped alongside terminal markdown rendering (`internal/render`), which is what made the latency
  visible enough to be worth fixing.
- **Token budgeting.** Making `NumSimilar`/`DetailLimit` (`cmd/chat/main.go:21-22`) configurable is the
  cheap 80% and could ride along with Phase 5; automatic prompt trimming to a token budget is a real
  project.
- **Parameterized `vector(N)`** — needed only for an embedding model that cannot emit 768. Narrowed, not
  eliminated, by `dimensions=768` plus provenance ([`01-design.md` §10](./01-design.md)).
- **SQLite storage backend** — evaluated and shelved; see
  [`ADR 0001`](../adr/0001-postgres-in-docker-over-sqlite.md). Reachable ~20-minute on-ramp versus ~31
  with Docker, at roughly 1300 lines of rewritten `internal/db`.
- **Voyage adapter** for Anthropic users wanting hosted embeddings.
- **Tool calling.** Additive struct fields; no feature needs it.
- **Removing the debug prompt dump** at `service.go:111-114` — should happen, unrelated to providers.

---

## Phase summary

| Phase | Change | User-visible? | Rollback |
|---|---|---|---|
| 0 | `cmd/data` honors env | Only if `OLLAMA_URL` was set and ignored | Revert |
| 1 | Interfaces + Ollama adapter | No (except correct failures) | Revert |
| 2 | OpenAI adapter | No | Delete package |
| 3 | Anthropic adapter | No | Delete package |
| 4 | Selection + startup validation | **Yes, opt-in** | Revert; defaults preserve old behavior |
| 5 | Docs — hosted quick-start leads | Docs only | Revert |

The containerization work that makes the quick-start possible (full-stack Compose absorbing Postgres,
pgvector, Go, and Stockfish) is tracked as an expansion of readiness P3-1, not as a phase here — it is
independent of the provider abstraction and can land in parallel. Neither track delivers an on-ramp
alone ([`04-onboarding.md` §2.3](./04-onboarding.md)).

Phases 2 and 3 are independent and can be reordered or parallelized. Phase 4 depends on at least one of
them.
