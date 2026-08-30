// Package openai adapts the OpenAI Chat Completions and Embeddings APIs to the
// llm interfaces.
//
// This is the only adapter that implements both halves of the split in
// docs/multi-provider/01-design.md §2 — chat and embeddings — which is what
// makes an Ollama-free setup possible at all.
//
// Retry ownership sits with the SDK. It retries 408/409/429/5xx and connection
// errors internally; wrapping that in an adapter-level loop would turn three
// attempts into nine against an endpoint that just said 429. This adapter maps
// the SDK's final error onto the llm sentinels and does nothing else.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"

	"github.com/cdewitt02/chesser/internal/llm"
)

const (
	// ProviderName is the value of CHAT_PROVIDER / EMBED_PROVIDER for OpenAI.
	ProviderName = "openai"

	// DefaultChatModel is pinned to a dated snapshot, never a floating alias.
	// An alias lets a server-side upgrade change eval results with no code
	// change and no way to notice; a pinned ID that goes visibly stale is
	// strictly better. Bumping it is routine maintenance that should re-run
	// the eval question set.
	DefaultChatModel = "gpt-5-2025-08-07"

	// DefaultEmbedModel is the 3-series small model, which supports the
	// dimensions parameter — the property that lets its vectors fit the
	// existing vector(768) column with no migration.
	DefaultEmbedModel = "text-embedding-3-small"

	// DefaultEmbedDimensions matches the vector(768) column the schema already
	// declares. Its native width is 1536; requesting 768 truncates server-side
	// and is what keeps an existing index's shape usable.
	DefaultEmbedDimensions = 768

	// APIKeyEnv is the provider-standard variable name. Do not invent a
	// project-specific one.
	APIKeyEnv = "OPENAI_API_KEY"

	// defaultMaxTokens is a ceiling, not a target. Answers run ~500 tokens;
	// this only exists so a long one is never truncated mid-sentence. It also
	// has to cover reasoning tokens, which count against the same budget.
	defaultMaxTokens = 16000

	// maxRetries is the project's retry policy, handed to the SDK.
	maxRetries = 3

	// maxBatch caps inputs per embeddings request. The API allows 2048 array
	// entries; this stays well under it, and under the 300k-token request cap
	// that a large batch of game summaries would otherwise approach.
	maxBatch = 128
)

// nativeDimensions covers embedding models that cannot truncate, so the width
// is fixed by the model rather than by configuration. A model that is absent
// reports 0 from Dimensions(), meaning "unknown", and the startup width check
// is skipped rather than guessed.
var nativeDimensions = map[string]int{
	"text-embedding-ada-002": 1536,
}

type Config struct {
	APIKey string
	// Model is the chat model for NewChat, or the embedding model for
	// NewEmbedder.
	Model string
	// MaxTokens caps the response. Zero uses defaultMaxTokens. Chat only.
	MaxTokens int
	// Dimensions requests a vector width. Zero uses DefaultEmbedDimensions.
	// Embedder only, and honored only by models that support truncation.
	Dimensions int
	// BaseURL overrides the API endpoint — tests point it at an
	// httptest.Server, and it is also how an OpenAI-compatible gateway is
	// reached.
	BaseURL string
	// MaxRetries overrides the project retry policy. Tests set 0 so a fixture
	// answers once. Never layer an adapter-level loop on top of it.
	MaxRetries *int
	// HTTPClient is optional; tests inject one.
	HTTPClient *http.Client
}

func newClient(cfg Config) (sdk.Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return sdk.Client{}, llm.Errf(ProviderName, "configure", llm.ErrNotConfigured,
			"provider %s requires %s", ProviderName, APIKeyEnv)
	}

	retries := maxRetries
	if cfg.MaxRetries != nil {
		retries = *cfg.MaxRetries
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(retries),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return sdk.NewClient(opts...), nil
}

// ---------- Chat ----------

type ChatModel struct {
	client    sdk.Client
	model     string
	maxTokens int
}

func NewChat(cfg Config) (*ChatModel, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}

	model := cfg.Model
	if model == "" {
		model = DefaultChatModel
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	return &ChatModel{client: client, model: model, maxTokens: maxTokens}, nil
}

func (m *ChatModel) Name() string { return ProviderName }

func (m *ChatModel) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	params, model, err := m.buildParams(req)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Chat.Completions.New(ctx, *params)
	if err != nil {
		return nil, classify("chat", err)
	}
	return buildResponse(resp, model)
}

// ChatStream is the same request as Chat, delivered incrementally. onDelta
// receives content deltas only.
//
// The accumulated completion is the identical shape Chat receives, so both
// paths share buildResponse and cannot drift on refusal handling, truncation,
// or usage accounting.
func (m *ChatModel) ChatStream(ctx context.Context, req llm.ChatRequest, onDelta func(string) error) (*llm.ChatResponse, error) {
	params, model, err := m.buildParams(req)
	if err != nil {
		return nil, err
	}
	// Usage arrives only in a final extra chunk, and only when asked for.
	// Without this the streamed path would report zero tokens while the
	// buffered path reported real ones.
	params.StreamOptions.IncludeUsage = param.NewOpt(true)

	stream := m.client.Chat.Completions.NewStreaming(ctx, *params)
	defer stream.Close()

	var acc sdk.ChatCompletionAccumulator
	for stream.Next() {
		chunk := stream.Current()
		acc.AddChunk(chunk)
		if len(chunk.Choices) == 0 {
			// The usage-only chunk that follows the last content chunk.
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		if err := onDelta(delta); err != nil {
			// A consumer failure is the consumer's error, not the provider's;
			// it must not be classified as a provider fault.
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, classify("chat", err)
	}
	return buildResponse(&acc.ChatCompletion, model)
}

// buildParams translates a ChatRequest into SDK params and reports the model
// the request will be sent to.
func (m *ChatModel) buildParams(req llm.ChatRequest) (*sdk.ChatCompletionNewParams, string, error) {
	// OpenAI is lenient about message ordering where Anthropic is not.
	// Validating identically here is what keeps a malformed conversation from
	// being rejected by one provider and silently accepted by another.
	if err := llm.ValidateMessages(req.Messages); err != nil {
		return nil, "", err
	}

	model := req.Model
	if model == "" {
		model = m.model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = m.maxTokens
	}

	// OpenAI takes the system prompt as messages[0]; lifting it out of the
	// caller's slice is this adapter's job, not the caller's.
	msgs := make([]sdk.ChatCompletionMessageParamUnion, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, sdk.SystemMessage(req.System))
	}
	for _, msg := range req.Messages {
		if msg.Role == llm.RoleAssistant {
			msgs = append(msgs, sdk.AssistantMessage(msg.Content))
		} else {
			msgs = append(msgs, sdk.UserMessage(msg.Content))
		}
	}

	params := &sdk.ChatCompletionNewParams{
		Model:    sdk.ChatModel(model),
		Messages: msgs,
		// max_completion_tokens, not the deprecated max_tokens: the latter is
		// rejected outright by reasoning models.
		MaxCompletionTokens: param.NewOpt(int64(maxTokens)),
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}
	if len(req.StopAfter) > 0 {
		params.Stop = sdk.ChatCompletionNewParamsStopUnion{OfStringArray: req.StopAfter}
	}

	return params, model, nil
}

// buildResponse normalizes a completed exchange, whether it arrived in one
// response or was accumulated from a stream.
func buildResponse(resp *sdk.ChatCompletion, model string) (*llm.ChatResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrBadResponse,
			"model %s returned no choices", model)
	}
	// n defaults to 1 and this adapter never raises it, so there is exactly
	// one choice to read.
	choice := resp.Choices[0]

	// Reasoning tokens never appear in content on this API — the model's
	// thinking is billed in usage.completion_tokens_details and not returned —
	// so unlike the Anthropic adapter there are no thinking blocks to filter.
	text := strings.TrimSpace(choice.Message.Content)

	if choice.Message.Refusal != "" {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrBadResponse,
			"model %s declined the request: %s", model, choice.Message.Refusal)
	}
	if text == "" {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrBadResponse,
			"model %s returned no text content (finish_reason=%s)", model, choice.FinishReason)
	}

	respModel := resp.Model
	if respModel == "" {
		respModel = model
	}

	return &llm.ChatResponse{
		Text:         text,
		Model:        respModel,
		FinishReason: normalizeFinishReason(choice.FinishReason),
		Usage: llm.Usage{
			InputTokens:  int(resp.Usage.PromptTokens),
			OutputTokens: int(resp.Usage.CompletionTokens),
		},
	}, nil
}

func normalizeFinishReason(r string) string {
	switch r {
	// An absent reason on an otherwise complete response is a normal stop;
	// OpenAI-compatible gateways are the ones that omit it.
	case "stop", "":
		return llm.FinishStop
	case "length":
		return llm.FinishLength
	case "content_filter":
		return llm.FinishContentFilter
	default:
		return llm.FinishOther
	}
}

// ---------- Embeddings ----------

type Embedder struct {
	client sdk.Client
	model  string
	dims   int
}

func NewEmbedder(cfg Config) (*Embedder, error) {
	client, err := newClient(cfg)
	if err != nil {
		return nil, err
	}

	model := cfg.Model
	if model == "" {
		model = DefaultEmbedModel
	}
	dims := cfg.Dimensions
	if dims <= 0 {
		dims = DefaultEmbedDimensions
	}

	return &Embedder{client: client, model: model, dims: dims}, nil
}

func (e *Embedder) Name() string  { return ProviderName }
func (e *Embedder) Model() string { return e.model }

// Dimensions reports the width this embedder will actually produce: the
// configured width for models that can truncate, the model's fixed width for
// those that cannot, and 0 — "unknown", check skipped — for a model this
// adapter has no fact about.
func (e *Embedder) Dimensions() int {
	if supportsDimensions(e.model) {
		return e.dims
	}
	return nativeDimensions[e.model]
}

// supportsDimensions reports whether the model accepts the dimensions
// parameter. Sending it to a model that does not — ada-002 — is a 400, so it
// is omitted rather than sent hopefully.
func supportsDimensions(model string) bool {
	return strings.HasPrefix(model, "text-embedding-3")
}

// Embed batches natively: unlike Ollama's one-prompt-per-request endpoint, a
// single call carries many inputs. Inputs are chunked so a large batch cannot
// exceed the API's array limit.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	for i, text := range texts {
		// The API rejects an empty string. Catching it here names the input,
		// which a 400 from the far end would not.
		if strings.TrimSpace(text) == "" {
			return nil, llm.Errf(ProviderName, "embed", llm.ErrInvalidRequest,
				"input %d is empty; embedding an empty string is not possible", i)
		}
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += maxBatch {
		end := min(start+maxBatch, len(texts))
		batch := texts[start:end]

		params := sdk.EmbeddingNewParams{
			Model: sdk.EmbeddingModel(e.model),
			Input: sdk.EmbeddingNewParamsInputUnion{OfArrayOfStrings: batch},
		}
		if supportsDimensions(e.model) {
			// The whole reason a hosted embedder needs no migration: ask for
			// the width the existing vector(768) column declares.
			params.Dimensions = param.NewOpt(int64(e.dims))
		}

		resp, err := e.client.Embeddings.New(ctx, params)
		if err != nil {
			return nil, classify("embed", err)
		}
		if len(resp.Data) != len(batch) {
			return nil, llm.Errf(ProviderName, "embed", llm.ErrBadResponse,
				"asked for %d embeddings, got %d", len(batch), len(resp.Data))
		}

		// Order the vectors by the index the response reports rather than by
		// arrival. Embed's contract is one vector per input *in input order*,
		// and a mismatched pairing would attach every summary to the wrong
		// vector — silently, and only detectable as bad retrieval.
		vecs := make([][]float32, len(batch))
		for _, item := range resp.Data {
			idx := int(item.Index)
			if idx < 0 || idx >= len(batch) {
				return nil, llm.Errf(ProviderName, "embed", llm.ErrBadResponse,
					"response index %d is outside the batch of %d", idx, len(batch))
			}
			if len(item.Embedding) == 0 {
				return nil, llm.Errf(ProviderName, "embed", llm.ErrBadResponse,
					"model %s returned an empty embedding for input %d", e.model, start+idx)
			}
			if vecs[idx] != nil {
				return nil, llm.Errf(ProviderName, "embed", llm.ErrBadResponse,
					"response repeated index %d", idx)
			}
			vec := make([]float32, len(item.Embedding))
			for i, v := range item.Embedding {
				vec[i] = float32(v)
			}
			vecs[idx] = vec
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// ---------- Errors ----------

// classify maps an SDK error, after its retries are exhausted, onto a sentinel.
func classify(op string, err error) error {
	if err == nil {
		return nil
	}
	// A body the SDK could not decode is deterministic — no retry, and it is a
	// bad response rather than an unavailable provider.
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return llm.WrapErr(ProviderName, op, llm.ErrBadResponse, err)
	}

	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		kind := llm.ClassifyStatus(apiErr.StatusCode)
		if apiErr.StatusCode == 400 {
			// OpenAI reports an oversized prompt as a 400 with a specific
			// code, which is a different remedy from a malformed request.
			lower := strings.ToLower(apiErr.Message + " " + apiErr.Code)
			if strings.Contains(lower, "context_length_exceeded") || strings.Contains(lower, "context length") {
				return llm.Errf(ProviderName, op, llm.ErrContextLength,
					"%v: lower DetailLimit or /clear the conversation", err)
			}
		}
		if apiErr.StatusCode == 401 || apiErr.StatusCode == 403 {
			return llm.Errf(ProviderName, op, kind, "%v (check %s)", err, APIKeyEnv)
		}
		return llm.WrapErr(ProviderName, op, kind, err)
	}
	return llm.WrapErr(ProviderName, op, llm.ErrUnavailable, err)
}

// ---------- Preflight ----------

// Preflight verifies credentials and that the configured model exists, before
// the welcome banner rather than at the first question.
//
// A models-list call that fails for a non-auth reason warns and continues: the
// endpoint is frequently absent behind an OpenAI-compatible gateway (LiteLLM,
// OpenRouter, a local vLLM) reached via BaseURL, and blocking startup over an
// auxiliary call would break valid setups. 401/403 is the credential check,
// and it is fatal.
func (m *ChatModel) Preflight(ctx context.Context) error {
	return preflight(ctx, m.client, "chat", m.model)
}

func (e *Embedder) Preflight(ctx context.Context) error {
	return preflight(ctx, e.client, "embed", e.model)
}

func preflight(ctx context.Context, client sdk.Client, op, model string) error {
	pager := client.Models.ListAutoPaging(ctx)

	var ids []string
	for pager.Next() {
		info := pager.Current()
		if info.ID == model {
			return nil
		}
		ids = append(ids, info.ID)
	}
	if err := pager.Err(); err != nil {
		var apiErr *sdk.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403) {
			return llm.Errf(ProviderName, "preflight", llm.ErrUnauthorized,
				"%v (check %s)", err, APIKeyEnv)
		}
		return llm.WrapErr(ProviderName, "preflight", llm.ErrPreflightInconclusive, err)
	}
	if len(ids) == 0 {
		// A successful but empty listing tells us nothing.
		return llm.Errf(ProviderName, "preflight", llm.ErrPreflightInconclusive,
			"models list returned no models")
	}

	return llm.Errf(ProviderName, "preflight", llm.ErrModelNotFound,
		"%q is not available on OpenAI (%s).%s Available models include: %s",
		model, op, hint(model), strings.Join(ids[:min(len(ids), 5)], ", "))
}

// hint enriches — never gates — the failure the live check already
// established. The common footgun is copying the README's positional model
// argument (an Ollama model name) while CHAT_PROVIDER=openai.
func hint(model string) string {
	lower := strings.ToLower(model)
	for _, marker := range []string{"llama", "mistral", "qwen", "gemma", "phi", "deepseek", "nomic"} {
		if strings.Contains(lower, marker) {
			return " That looks like an Ollama model — did you mean CHAT_PROVIDER=ollama?" +
				" Note the chat model can be passed positionally, which outranks CHAT_MODEL."
		}
	}
	if strings.HasPrefix(lower, "claude") {
		return " That looks like an Anthropic model — did you mean CHAT_PROVIDER=anthropic?"
	}
	return ""
}

var (
	_ llm.ChatModel          = (*ChatModel)(nil)
	_ llm.StreamingChatModel = (*ChatModel)(nil)
	_ llm.Embedder           = (*Embedder)(nil)
	_ llm.Preflighter        = (*ChatModel)(nil)
	_ llm.Preflighter        = (*Embedder)(nil)
)
