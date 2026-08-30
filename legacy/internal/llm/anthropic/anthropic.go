// Package anthropic adapts the Anthropic Messages API to llm.ChatModel.
//
// There is deliberately no NewEmbedder: Anthropic offers no embeddings API, so
// this package implements exactly one of the two llm interfaces.
//
// Retry ownership sits with the SDK. It retries 408/409/429/5xx and connection
// errors internally; wrapping that in an adapter-level loop would turn three
// attempts into nine against an endpoint that just said 429. This adapter maps
// the SDK's final error onto the llm sentinels and does nothing else.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/cdewitt02/chesser/internal/llm"
)

const (
	// ProviderName is the value of CHAT_PROVIDER for Anthropic.
	ProviderName = "anthropic"

	// DefaultModel is pinned, never an alias. A server-side upgrade behind an
	// alias would change eval results with no code change and no way to
	// notice; a pinned ID that goes visibly stale is strictly better.
	DefaultModel = "claude-opus-5"

	// APIKeyEnv is the provider-standard variable name. Do not invent a
	// project-specific one.
	APIKeyEnv = "ANTHROPIC_API_KEY"

	// defaultMaxTokens is a ceiling, not a target. Answers run ~500 tokens;
	// this only exists so a long one is never truncated mid-sentence.
	defaultMaxTokens = 16000

	// maxRetries is the project's retry policy, handed to the SDK.
	maxRetries = 3
)

type Config struct {
	APIKey string
	Model  string
	// MaxTokens caps the response. Zero uses defaultMaxTokens.
	MaxTokens int
	// BaseURL overrides the API endpoint; tests point it at an httptest.Server.
	BaseURL string
	// MaxRetries overrides the project retry policy. Tests set 0 so a fixture
	// answers once. Never layer an adapter-level loop on top of it.
	MaxRetries *int
	// HTTPClient is optional; tests inject one.
	HTTPClient *http.Client
}

type ChatModel struct {
	client    sdk.Client
	model     string
	maxTokens int
}

func NewChat(cfg Config) (*ChatModel, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, llm.Errf(ProviderName, "configure", llm.ErrNotConfigured,
			"CHAT_PROVIDER=%s requires %s", ProviderName, APIKeyEnv)
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

	model := cfg.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	return &ChatModel{client: sdk.NewClient(opts...), model: model, maxTokens: maxTokens}, nil
}

func (m *ChatModel) Name() string { return ProviderName }

func (m *ChatModel) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	params, model, err := m.buildParams(req)
	if err != nil {
		return nil, err
	}

	resp, err := m.client.Messages.New(ctx, *params)
	if err != nil {
		return nil, classify(err)
	}
	return buildResponse(resp, model)
}

// ChatStream is the same request as Chat, delivered incrementally. onDelta
// receives text fragments only: thinking and tool-use deltas are skipped, for
// the same reason Chat skips those blocks.
//
// The accumulated message is the identical shape Chat receives, so both paths
// share buildResponse and cannot drift on refusal handling, truncation, or
// usage accounting.
func (m *ChatModel) ChatStream(ctx context.Context, req llm.ChatRequest, onDelta func(string) error) (*llm.ChatResponse, error) {
	params, model, err := m.buildParams(req)
	if err != nil {
		return nil, err
	}

	stream := m.client.Messages.NewStreaming(ctx, *params)
	var acc sdk.Message
	for stream.Next() {
		event := stream.Current()
		if err := acc.Accumulate(event); err != nil {
			return nil, llm.WrapErr(ProviderName, "chat", llm.ErrBadResponse, err)
		}
		delta, ok := event.AsAny().(sdk.ContentBlockDeltaEvent)
		if !ok {
			continue
		}
		text, ok := delta.Delta.AsAny().(sdk.TextDelta)
		if !ok || text.Text == "" {
			continue
		}
		if err := onDelta(text.Text); err != nil {
			// A consumer failure is the consumer's error, not the provider's;
			// it must not be classified as a provider fault.
			return nil, err
		}
	}
	if err := stream.Err(); err != nil {
		return nil, classify(err)
	}
	return buildResponse(&acc, model)
}

// buildParams translates a ChatRequest into SDK params and reports the model
// the request will be sent to.
func (m *ChatModel) buildParams(req llm.ChatRequest) (*sdk.MessageNewParams, string, error) {
	// Anthropic requires messages to begin with a user turn and to alternate.
	// Today's history assembly satisfies that by accident; here it becomes
	// guaranteed, with a clear error instead of a passed-through 400.
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

	msgs := make([]sdk.MessageParam, 0, len(req.Messages))
	for _, msg := range req.Messages {
		block := sdk.NewTextBlock(msg.Content)
		if msg.Role == llm.RoleAssistant {
			msgs = append(msgs, sdk.NewAssistantMessage(block))
		} else {
			msgs = append(msgs, sdk.NewUserMessage(block))
		}
	}

	params := sdk.MessageNewParams{
		Model:     sdk.Model(model),
		MaxTokens: int64(maxTokens),
		Messages:  msgs,
	}
	// The system prompt is a top-level parameter, never a message. This is the
	// difference the interface exists to hide from callers.
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}
	if len(req.StopAfter) > 0 {
		params.StopSequences = req.StopAfter
	}

	return &params, model, nil
}

// buildResponse normalizes a completed message, whether it arrived in one
// response or was accumulated from a stream.
func buildResponse(resp *sdk.Message, model string) (*llm.ChatResponse, error) {
	// content is an array of typed blocks. Concatenate the text ones and skip
	// everything else — notably thinking blocks, which a naive read would
	// either drop the answer for or splice reasoning into it.
	var sb strings.Builder
	for _, block := range resp.Content {
		if text, ok := block.AsAny().(sdk.TextBlock); ok {
			sb.WriteString(text.Text)
		}
	}
	text := strings.TrimSpace(sb.String())

	if resp.StopReason == sdk.StopReasonRefusal {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrBadResponse,
			"model %s declined the request (%s)", model, resp.StopDetails.Category)
	}
	if resp.StopReason == sdk.StopReasonModelContextWindowExceeded {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrContextLength,
			"prompt exceeds the context window of %s; lower DetailLimit or /clear the conversation", model)
	}
	if text == "" {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrBadResponse,
			"model %s returned no text content (stop_reason=%s)", model, resp.StopReason)
	}

	respModel := string(resp.Model)
	if respModel == "" {
		respModel = model
	}

	return &llm.ChatResponse{
		Text:         text,
		Model:        respModel,
		FinishReason: normalizeStopReason(resp.StopReason),
		Usage: llm.Usage{
			InputTokens:  int(resp.Usage.InputTokens),
			OutputTokens: int(resp.Usage.OutputTokens),
		},
	}, nil
}

func normalizeStopReason(r sdk.StopReason) string {
	switch r {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
		return llm.FinishStop
	case sdk.StopReasonMaxTokens:
		return llm.FinishLength
	case sdk.StopReasonRefusal:
		return llm.FinishContentFilter
	default:
		return llm.FinishOther
	}
}

// classify maps an SDK error, after its retries are exhausted, onto a sentinel.
func classify(err error) error {
	if err == nil {
		return nil
	}
	// A body the SDK could not decode is deterministic — no retry, and it is a
	// bad response rather than an unavailable provider.
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &syntaxErr) || errors.As(err, &typeErr) {
		return llm.WrapErr(ProviderName, "chat", llm.ErrBadResponse, err)
	}

	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		kind := llm.ClassifyStatus(apiErr.StatusCode)
		if apiErr.StatusCode == 400 && strings.Contains(strings.ToLower(err.Error()), "context") {
			kind = llm.ErrContextLength
		}
		if apiErr.StatusCode == 401 || apiErr.StatusCode == 403 {
			return llm.Errf(ProviderName, "chat", kind, "%v (check %s)", err, APIKeyEnv)
		}
		return llm.WrapErr(ProviderName, "chat", kind, err)
	}
	return llm.WrapErr(ProviderName, "chat", llm.ErrUnavailable, err)
}

// Preflight verifies credentials and that the configured model exists, before
// the welcome banner rather than at the first question.
//
// A models-list call that fails for a non-auth reason warns and continues: the
// endpoint may be absent behind an Anthropic-compatible gateway, and blocking
// startup over an auxiliary call would break valid setups. 401/403 is the
// credential check, and it is fatal.
func (m *ChatModel) Preflight(ctx context.Context) error {
	pager := m.client.Models.ListAutoPaging(ctx, sdk.ModelListParams{})

	var ids []string
	for pager.Next() {
		info := pager.Current()
		if info.ID == m.model {
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
		"%q is not available on Anthropic.%s Available models include: %s",
		m.model, hint(m.model), strings.Join(ids[:min(len(ids), 5)], ", "))
}

// hint enriches — never gates — the failure the live check already
// established. The common footgun is copying the README's positional model
// argument (an Ollama model name) while CHAT_PROVIDER=anthropic.
func hint(model string) string {
	lower := strings.ToLower(model)
	for _, marker := range []string{"llama", "mistral", "qwen", "gemma", "phi", "deepseek", "nomic"} {
		if strings.Contains(lower, marker) {
			return " That looks like an Ollama model — did you mean CHAT_PROVIDER=ollama?" +
				" Note the chat model can be passed positionally, which outranks CHAT_MODEL."
		}
	}
	return ""
}

var (
	_ llm.ChatModel          = (*ChatModel)(nil)
	_ llm.StreamingChatModel = (*ChatModel)(nil)
	_ llm.Preflighter        = (*ChatModel)(nil)
)
