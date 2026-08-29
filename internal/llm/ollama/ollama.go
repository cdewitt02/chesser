// Package ollama adapts a local Ollama server to the llm interfaces.
//
// The client stays hand-rolled net/http: the surface is two endpoints with flat
// JSON, and the official Go package would pull in the whole Ollama application
// module to reach them.
//
// There is deliberately no retry loop. Ollama is a local process — 429 does not
// happen, a dial failure means it is not running, and a 5xx usually means the
// model failed to load. Retrying only delays telling the user that.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cdewitt02/chesser/internal/llm"
)

const (
	// ProviderName is the value of CHAT_PROVIDER / EMBED_PROVIDER for Ollama.
	ProviderName = "ollama"

	DefaultBaseURL    = "http://localhost:11434"
	DefaultChatModel  = "llama3.2"
	DefaultEmbedModel = "nomic-embed-text"

	// A cold model can take well over the 10s the old client allowed.
	defaultEmbedTimeout = 60 * time.Second
	defaultChatTimeout  = 120 * time.Second
)

// knownDimensions lets startup verify the vector(N) column without a probe
// request. A model that is absent reports 0, meaning "unknown", and the check
// is skipped rather than guessed.
var knownDimensions = map[string]int{
	"nomic-embed-text":        768,
	"all-minilm":              384,
	"mxbai-embed-large":       1024,
	"bge-m3":                  1024,
	"snowflake-arctic-embed":  1024,
	"snowflake-arctic-embed2": 1024,
}

type Config struct {
	BaseURL string
	// Model is the chat model for NewChat, or the embedding model for
	// NewEmbedder.
	Model string
	// HTTPClient is optional; tests inject one pointed at an httptest.Server.
	HTTPClient *http.Client
	// Timeout applies per operation when HTTPClient is not supplied.
	Timeout time.Duration
}

type client struct {
	baseURL string
	model   string
	http    *http.Client
}

func newClient(cfg Config, defaultModel string, defaultTimeout time.Duration) (*client, error) {
	baseURL := strings.TrimSuffix(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	model := cfg.Model
	if model == "" {
		model = defaultModel
	}
	hc := cfg.HTTPClient
	if hc == nil {
		timeout := cfg.Timeout
		if timeout == 0 {
			timeout = defaultTimeout
		}
		// One pooled client per adapter, rather than a fresh client per call.
		hc = &http.Client{Timeout: timeout}
	}
	return &client{baseURL: baseURL, model: model, http: hc}, nil
}

func (c *client) Name() string { return ProviderName }

// postJSON sends body to path and decodes a 2xx response into out. Non-2xx and
// transport failures are mapped onto the llm sentinels.
func (c *client) postJSON(ctx context.Context, op, path string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrInvalidRequest, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrInvalidRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return llm.WrapErr(ProviderName, op, llm.ErrUnavailable, ctx.Err())
		}
		return llm.Errf(ProviderName, op, llm.ErrUnavailable,
			"%v (is Ollama running at %s?)", err, c.baseURL)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrUnavailable, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		kind := llm.ClassifyStatus(resp.StatusCode)
		if resp.StatusCode == 404 {
			return llm.Errf(ProviderName, op, llm.ErrModelNotFound,
				"status %d: %s (try: ollama pull %s)", resp.StatusCode, snippet(respBody), c.model)
		}
		return llm.Errf(ProviderName, op, kind, "status %d: %s", resp.StatusCode, snippet(respBody))
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return llm.Errf(ProviderName, op, llm.ErrBadResponse, "%v: %s", err, snippet(respBody))
	}
	return nil
}

// postStream sends body to path and hands each newline-delimited JSON object in
// the response to onObject as it arrives.
//
// Ollama streams NDJSON rather than SSE, so this is a scanner over the body
// instead of an event parser. Errors are mapped onto the same sentinels
// postJSON uses: a stream that fails must be indistinguishable, to the caller,
// from a non-streaming call that failed the same way.
func (c *client) postStream(ctx context.Context, op, path string, body any, onObject func([]byte) error) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrInvalidRequest, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrInvalidRequest, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return llm.WrapErr(ProviderName, op, llm.ErrUnavailable, ctx.Err())
		}
		return llm.Errf(ProviderName, op, llm.ErrUnavailable,
			"%v (is Ollama running at %s?)", err, c.baseURL)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The error body is small and not streamed, so reading it whole is
		// safe and gives the same message the non-streaming path produces.
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 404 {
			return llm.Errf(ProviderName, op, llm.ErrModelNotFound,
				"status %d: %s (try: ollama pull %s)", resp.StatusCode, snippet(respBody), c.model)
		}
		return llm.Errf(ProviderName, op, llm.ClassifyStatus(resp.StatusCode),
			"status %d: %s", resp.StatusCode, snippet(respBody))
	}

	scanner := bufio.NewScanner(resp.Body)
	// A single NDJSON object holds one token's worth of text, but the final
	// object carries the full stats block. The default 64KB limit is ample;
	// raising it costs nothing and removes a failure mode that would only
	// appear on unusual responses.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := onObject(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return llm.WrapErr(ProviderName, op, llm.ErrUnavailable, ctx.Err())
		}
		// A stream that stops mid-response is a transport failure, not a bad
		// body: nothing about the JSON we did receive was malformed.
		return llm.WrapErr(ProviderName, op, llm.ErrUnavailable, err)
	}
	return nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}

// ---------- Chat ----------

type ChatModel struct{ *client }

func NewChat(cfg Config) (*ChatModel, error) {
	c, err := newClient(cfg, DefaultChatModel, defaultChatTimeout)
	if err != nil {
		return nil, err
	}
	return &ChatModel{client: c}, nil
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type chatReq struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Options  *chatOptions  `json:"options,omitempty"`
}

type chatResp struct {
	Model      string      `json:"model"`
	Message    chatMessage `json:"message"`
	DoneReason string      `json:"done_reason"`
	// Error carries a mid-stream failure, which Ollama reports inside a 200
	// response body rather than as a status code.
	Error string `json:"error"`

	PromptEvalCount int `json:"prompt_eval_count"`
	EvalCount       int `json:"eval_count"`
}

func (m *ChatModel) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	body, model, err := m.buildChatReq(req, false)
	if err != nil {
		return nil, err
	}

	var out chatResp
	if err := m.postJSON(ctx, "chat", "/api/chat", body, &out); err != nil {
		return nil, err
	}
	return buildResponse(&out, model, out.Message.Content)
}

// ChatStream is the same request as Chat with Ollama's stream flag set. onDelta
// receives each token's text as it arrives.
//
// Ollama streams partial messages and then a final object carrying the stats
// and done_reason, so the deltas are concatenated here rather than trusting any
// single object to hold the whole answer.
func (m *ChatModel) ChatStream(ctx context.Context, req llm.ChatRequest, onDelta func(string) error) (*llm.ChatResponse, error) {
	body, model, err := m.buildChatReq(req, true)
	if err != nil {
		return nil, err
	}

	var full strings.Builder
	var last chatResp
	err = m.postStream(ctx, "chat", "/api/chat", body, func(line []byte) error {
		var out chatResp
		if err := json.Unmarshal(line, &out); err != nil {
			return llm.Errf(ProviderName, "chat", llm.ErrBadResponse, "%v: %s", err, snippet(line))
		}
		// Ollama reports mid-stream errors in the body of a 200 response, so
		// this is the only place they can be caught.
		if out.Error != "" {
			return llm.Errf(ProviderName, "chat", llm.ErrBadResponse, "%s", out.Error)
		}
		last = out
		if out.Message.Content == "" {
			return nil
		}
		full.WriteString(out.Message.Content)
		if err := onDelta(out.Message.Content); err != nil {
			// A consumer failure is the consumer's error, not the provider's;
			// it must not be classified as a provider fault.
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return buildResponse(&last, model, full.String())
}

// buildChatReq translates a ChatRequest into an Ollama request body and reports
// the model the request will be sent to.
func (m *ChatModel) buildChatReq(req llm.ChatRequest, stream bool) (*chatReq, string, error) {
	if err := llm.ValidateMessages(req.Messages); err != nil {
		return nil, "", err
	}

	model := req.Model
	if model == "" {
		model = m.model
	}

	// Ollama takes the system prompt as messages[0]; lifting it out of the
	// caller's slice is this adapter's job, not the caller's.
	msgs := make([]chatMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, chatMessage{Role: "system", Content: req.System})
	}
	for _, msg := range req.Messages {
		msgs = append(msgs, chatMessage{Role: string(msg.Role), Content: msg.Content})
	}

	body := &chatReq{Model: model, Messages: msgs, Stream: stream}
	if req.Temperature != nil || req.MaxTokens > 0 || len(req.StopAfter) > 0 {
		body.Options = &chatOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
			Stop:        req.StopAfter,
		}
	}
	return body, model, nil
}

// buildResponse normalizes a completed exchange. text is passed separately
// because a streamed answer is assembled from many objects while out holds only
// the last one.
func buildResponse(out *chatResp, model, text string) (*llm.ChatResponse, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, llm.Errf(ProviderName, "chat", llm.ErrBadResponse,
			"model %s returned no content", model)
	}

	respModel := out.Model
	if respModel == "" {
		respModel = model
	}

	return &llm.ChatResponse{
		Text:         text,
		Model:        respModel,
		FinishReason: normalizeDoneReason(out.DoneReason),
		Usage: llm.Usage{
			InputTokens:  out.PromptEvalCount,
			OutputTokens: out.EvalCount,
		},
	}, nil
}

func normalizeDoneReason(r string) string {
	switch r {
	case "stop", "":
		return llm.FinishStop
	case "length":
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}

// ---------- Embeddings ----------

type Embedder struct{ *client }

func NewEmbedder(cfg Config) (*Embedder, error) {
	c, err := newClient(cfg, DefaultEmbedModel, defaultEmbedTimeout)
	if err != nil {
		return nil, err
	}
	return &Embedder{client: c}, nil
}

type embedReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type embedResp struct {
	Embedding []float32 `json:"embedding"`
}

func (e *Embedder) Model() string { return e.model }

func (e *Embedder) Dimensions() int { return knownDimensions[baseModelName(e.model)] }

// baseModelName strips an Ollama tag ("nomic-embed-text:latest").
func baseModelName(model string) string {
	if i := strings.IndexByte(model, ':'); i >= 0 {
		return model[:i]
	}
	return model
}

// Embed loops per text: /api/embeddings takes one prompt per request.
func (e *Embedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for i, text := range texts {
		var resp embedResp
		if err := e.postJSON(ctx, "embed", "/api/embeddings", embedReq{Model: e.model, Prompt: text}, &resp); err != nil {
			return nil, err
		}
		// The old client returned (nil, nil) here: an Ollama error body
		// unmarshals cleanly into an empty embedding, and the nil vector
		// reached the vector(768) column. An empty vector is an error.
		if len(resp.Embedding) == 0 {
			return nil, llm.Errf(ProviderName, "embed", llm.ErrBadResponse,
				"model %s returned an empty embedding for input %d", e.model, i)
		}
		out = append(out, resp.Embedding)
	}
	return out, nil
}

// ---------- Preflight ----------

type tagsResp struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

func (m *ChatModel) Preflight(ctx context.Context) error { return preflight(ctx, m.client, "chat") }

func (e *Embedder) Preflight(ctx context.Context) error { return preflight(ctx, e.client, "embed") }

// preflight checks that Ollama is reachable and the configured model is pulled.
// "Model not pulled" is a top setup failure, so it is worth catching before the
// first question rather than after.
func preflight(ctx context.Context, c *client, op string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrInvalidRequest, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// A dial failure against a local process is definitive.
		return llm.Errf(ProviderName, op, llm.ErrUnavailable,
			"cannot reach Ollama at %s: %v (is it running?)", c.baseURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrPreflightInconclusive, err)
	}
	if resp.StatusCode != http.StatusOK {
		return llm.Errf(ProviderName, op, llm.ErrPreflightInconclusive,
			"/api/tags returned status %d", resp.StatusCode)
	}

	var tags tagsResp
	if err := json.Unmarshal(body, &tags); err != nil {
		return llm.WrapErr(ProviderName, op, llm.ErrPreflightInconclusive, err)
	}

	for _, mdl := range tags.Models {
		for _, name := range []string{mdl.Name, mdl.Model} {
			if name == c.model || baseModelName(name) == baseModelName(c.model) {
				return nil
			}
		}
	}
	return llm.Errf(ProviderName, op, llm.ErrModelNotFound,
		"model %q is not pulled; run: ollama pull %s", c.model, c.model)
}

var (
	_ llm.ChatModel          = (*ChatModel)(nil)
	_ llm.StreamingChatModel = (*ChatModel)(nil)
	_ llm.Embedder           = (*Embedder)(nil)
	_ llm.Preflighter        = (*ChatModel)(nil)
	_ llm.Preflighter        = (*Embedder)(nil)
)
