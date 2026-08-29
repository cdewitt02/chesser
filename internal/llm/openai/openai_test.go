package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cdewitt02/chesser/internal/llm"
	"github.com/cdewitt02/chesser/internal/llm/llmtest"
	llmopenai "github.com/cdewitt02/chesser/internal/llm/openai"
)

const (
	testChatModel  = "gpt-5-2025-08-07"
	testEmbedModel = "text-embedding-3-small"
)

func newChat(t *testing.T, baseURL string) *llmopenai.ChatModel {
	t.Helper()
	noRetry := 0
	m, err := llmopenai.NewChat(llmopenai.Config{
		APIKey: "test-key", Model: testChatModel, BaseURL: baseURL, MaxRetries: &noRetry,
	})
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	return m
}

func newEmbedder(t *testing.T, baseURL string) *llmopenai.Embedder {
	t.Helper()
	noRetry := 0
	e, err := llmopenai.NewEmbedder(llmopenai.Config{
		APIKey: "test-key", Model: testEmbedModel, BaseURL: baseURL, MaxRetries: &noRetry,
	})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	return e
}

// completion builds an OpenAI-shaped chat completion: the answer lives at
// choices[0].message.content.
func completion(choice map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": 1,
			"model":   testChatModel,
			"choices": []map[string]any{choice},
			"usage": map[string]any{
				"prompt_tokens": 3000, "completion_tokens": 500, "total_tokens": 3500,
			},
		})
	}
}

func textChoice(content, finishReason string) map[string]any {
	return map[string]any{
		"index":         0,
		"message":       map[string]any{"role": "assistant", "content": content},
		"finish_reason": finishReason,
	}
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body))
	}
}

func apiError(typ, code, message string) string {
	b, _ := json.Marshal(map[string]any{"error": map[string]any{
		"type": typ, "code": code, "message": message,
	}})
	return string(b)
}

func TestChatConformance(t *testing.T) {
	llmtest.ChatConformance{
		Provider: "openai",
		New: func(baseURL string) (llm.ChatModel, error) {
			noRetry := 0
			return llmopenai.NewChat(llmopenai.Config{
				APIKey: "test-key", Model: testChatModel, BaseURL: baseURL, MaxRetries: &noRetry,
			})
		},
		Fixtures: map[llmtest.Scenario]http.HandlerFunc{
			llmtest.ScenarioSuccess:      completion(textChoice("You hang pawns.", "stop")),
			llmtest.ScenarioTruncated:    completion(textChoice("You hang", "length")),
			llmtest.ScenarioEmptyContent: completion(textChoice("", "stop")),
			llmtest.ScenarioMalformed:    status(200, "{not json"),
			llmtest.ScenarioUnauthorized: status(401, apiError("invalid_request_error", "invalid_api_key", "invalid key")),
			llmtest.ScenarioRateLimited:  status(429, apiError("rate_limit_error", "rate_limit_exceeded", "slow down")),
			llmtest.ScenarioServerError:  status(500, apiError("server_error", "", "boom")),
		},
	}.Run(t)
}

func TestChatMessageValidation(t *testing.T) {
	srv := httptest.NewServer(completion(textChoice("hi", "stop")))
	defer srv.Close()
	llmtest.RunMessageValidation(t, "openai", newChat(t, srv.URL))
}

// OpenAI takes the system prompt as messages[0]. The caller never builds it,
// so this asserts the adapter prepends exactly one system turn and leaves the
// caller's alternation intact.
func TestSystemPromptIsPrependedAsFirstMessage(t *testing.T) {
	var got struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		completion(textChoice("ok", "stop"))(w, r)
	}))
	defer srv.Close()

	req := llmtest.SampleRequest()
	req.Messages = append(req.Messages,
		llm.Message{Role: llm.RoleAssistant, Content: "Because of king activity."},
		llm.Message{Role: llm.RoleUser, Content: "How do I fix it?"},
	)

	if _, err := newChat(t, srv.URL).Chat(context.Background(), req); err != nil {
		t.Fatalf("Chat: %v", err)
	}

	wantRoles := []string{"system", "user", "assistant", "user"}
	if len(got.Messages) != len(wantRoles) {
		t.Fatalf("got %d messages, want %d", len(got.Messages), len(wantRoles))
	}
	for i, want := range wantRoles {
		if got.Messages[i].Role != want {
			t.Errorf("messages[%d].role = %q, want %q", i, got.Messages[i].Role, want)
		}
	}
	if got.Messages[0].Content != req.System {
		t.Errorf("messages[0].content = %q, want the system prompt", got.Messages[0].Content)
	}
}

// Reasoning models reject max_tokens outright, so the adapter must send
// max_completion_tokens instead.
func TestSendsMaxCompletionTokens(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		completion(textChoice("ok", "stop"))(w, r)
	}))
	defer srv.Close()

	if _, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest()); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if _, ok := got["max_completion_tokens"]; !ok {
		t.Errorf("request = %v, want max_completion_tokens", got)
	}
	if _, ok := got["max_tokens"]; ok {
		t.Error("request carries the deprecated max_tokens, which reasoning models reject")
	}
}

func TestRefusalIsAnError(t *testing.T) {
	choice := map[string]any{
		"index": 0,
		"message": map[string]any{
			"role": "assistant", "content": "", "refusal": "I can't help with that.",
		},
		"finish_reason": "stop",
	}
	srv := httptest.NewServer(completion(choice))
	defer srv.Close()

	_, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if !errors.Is(err, llm.ErrBadResponse) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrBadResponse)
	}
	if !strings.Contains(err.Error(), "I can't help with that.") {
		t.Errorf("error = %v, want it to carry the refusal text", err)
	}
}

func TestUsageIsPopulated(t *testing.T) {
	srv := httptest.NewServer(completion(textChoice("ok", "stop")))
	defer srv.Close()

	resp, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.InputTokens != 3000 || resp.Usage.OutputTokens != 500 {
		t.Errorf("Usage = %+v, want {3000 500}", resp.Usage)
	}
}

// An oversized prompt is a 400 like a malformed request, but the remedy is
// different, so it must not be classified as ErrInvalidRequest.
func TestContextLengthExceeded(t *testing.T) {
	srv := httptest.NewServer(status(400, apiError(
		"invalid_request_error", "context_length_exceeded",
		"This model's maximum context length is 128000 tokens.")))
	defer srv.Close()

	_, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if !errors.Is(err, llm.ErrContextLength) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrContextLength)
	}
}

func TestMissingAPIKeyIsNotConfigured(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"chat", func() error {
			_, err := llmopenai.NewChat(llmopenai.Config{Model: testChatModel})
			return err
		}},
		{"embedder", func() error {
			_, err := llmopenai.NewEmbedder(llmopenai.Config{Model: testEmbedModel})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if !errors.Is(err, llm.ErrNotConfigured) {
				t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrNotConfigured)
			}
			if !strings.Contains(err.Error(), llmopenai.APIKeyEnv) {
				t.Errorf("error = %v, want it to name %s", err, llmopenai.APIKeyEnv)
			}
		})
	}
}

// ---------- Embeddings ----------

// embeddings builds an OpenAI-shaped embeddings response of the given width.
func embeddings(width int, indices ...int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data := make([]map[string]any, 0, len(indices))
		for _, idx := range indices {
			vec := make([]float64, width)
			for i := range vec {
				vec[i] = float64(idx) + float64(i)/1000
			}
			data = append(data, map[string]any{
				"object": "embedding", "index": idx, "embedding": vec,
			})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "model": testEmbedModel, "data": data,
			"usage": map[string]any{"prompt_tokens": 10, "total_tokens": 10},
		})
	}
}

// The whole reason a hosted embedder needs no schema migration.
func TestEmbedRequestsColumnWidth(t *testing.T) {
	var got struct {
		Model      string   `json:"model"`
		Dimensions int      `json:"dimensions"`
		Input      []string `json:"input"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		embeddings(768, 0)(w, r)
	}))
	defer srv.Close()

	e := newEmbedder(t, srv.URL)
	vecs, err := e.Embed(context.Background(), []string{"a summary"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if got.Dimensions != 768 {
		t.Errorf("dimensions = %d, want 768 to fit the vector(768) column", got.Dimensions)
	}
	if len(vecs) != 1 || len(vecs[0]) != 768 {
		t.Fatalf("got %d vector(s) of width %d, want 1 of 768", len(vecs), len(vecs[0]))
	}
	if e.Dimensions() != 768 {
		t.Errorf("Dimensions() = %d, want 768", e.Dimensions())
	}
}

// A model that cannot truncate must not be sent the parameter, and must report
// its own fixed width instead of the configured one.
func TestEmbedOmitsDimensionsForFixedWidthModel(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		embeddings(1536, 0)(w, r)
	}))
	defer srv.Close()

	e, err := llmopenai.NewEmbedder(llmopenai.Config{
		APIKey: "test-key", Model: "text-embedding-ada-002", BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if _, err := e.Embed(context.Background(), []string{"a summary"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if _, ok := got["dimensions"]; ok {
		t.Error("request carries dimensions for a model that rejects it")
	}
	if e.Dimensions() != 1536 {
		t.Errorf("Dimensions() = %d, want the model's native 1536", e.Dimensions())
	}
}

// An unknown model reports 0 — "unknown" — so the startup width check is
// skipped rather than asserted against a guess.
func TestDimensionsUnknownModel(t *testing.T) {
	e, err := llmopenai.NewEmbedder(llmopenai.Config{APIKey: "test-key", Model: "some-gateway-model"})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	if e.Dimensions() != 0 {
		t.Errorf("Dimensions() = %d, want 0 for a model with no known width", e.Dimensions())
	}
}

// Embed's contract is one vector per input in input order. A response that
// arrives out of order must still pair correctly, or every summary is stored
// against the wrong vector — silently.
func TestEmbedOrdersByResponseIndex(t *testing.T) {
	srv := httptest.NewServer(embeddings(4, 2, 0, 1))
	defer srv.Close()

	vecs, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vecs))
	}
	// The fixture seeds each vector's first element with its index.
	for i, vec := range vecs {
		if vec[0] != float32(i) {
			t.Errorf("vecs[%d][0] = %v, want %v — vectors are paired to the wrong inputs", i, vec[0], float32(i))
		}
	}
}

func TestEmbedBatchesInOneRequest(t *testing.T) {
	var requests int
	var got struct {
		Input []string `json:"input"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewDecoder(r.Body).Decode(&got)
		embeddings(768, 0, 1, 2)(w, r)
	}))
	defer srv.Close()

	if _, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a", "b", "c"}); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if requests != 1 {
		t.Errorf("made %d requests, want 1 — OpenAI batches natively", requests)
	}
	if len(got.Input) != 3 {
		t.Errorf("input = %v, want all three texts in one request", got.Input)
	}
}

// The bug the old Ollama client had: an error body that unmarshals into an
// empty embedding, which then reaches the vector column as a nil vector.
func TestEmbedRejectsEmptyVector(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"object": "list", "model": testEmbedModel,
			"data": []map[string]any{{"object": "embedding", "index": 0, "embedding": []float64{}}},
		})
	}))
	defer srv.Close()

	_, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a"})
	if !errors.Is(err, llm.ErrBadResponse) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrBadResponse)
	}
}

func TestEmbedRejectsEmptyInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("adapter sent a request for input the API rejects")
	}))
	defer srv.Close()

	_, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"fine", "  "})
	if !errors.Is(err, llm.ErrInvalidRequest) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrInvalidRequest)
	}
}

func TestEmbedCountMismatch(t *testing.T) {
	srv := httptest.NewServer(embeddings(768, 0))
	defer srv.Close()

	_, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a", "b"})
	if !errors.Is(err, llm.ErrBadResponse) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrBadResponse)
	}
}

func TestEmbedClassifiesStatus(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{401, apiError("invalid_request_error", "invalid_api_key", "invalid key"), llm.ErrUnauthorized},
		{429, apiError("rate_limit_error", "rate_limit_exceeded", "slow down"), llm.ErrRateLimited},
		{500, apiError("server_error", "", "boom"), llm.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(status(tc.status, tc.body))
			defer srv.Close()

			_, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want it to wrap %v", err, tc.want)
			}
		})
	}
}

// ---------- Preflight ----------

func modelsList(ids ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{
				"id": id, "object": "model", "created": 1, "owned_by": "openai",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	}
}

func TestPreflightAcceptsKnownModel(t *testing.T) {
	srv := httptest.NewServer(modelsList("gpt-4o", testChatModel))
	defer srv.Close()

	if err := newChat(t, srv.URL).Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

// The embedder preflights too: an EMBED_PROVIDER=openai typo should fail at
// startup, not partway through a thousand-game ingestion.
func TestPreflightChecksEmbedModel(t *testing.T) {
	srv := httptest.NewServer(modelsList("gpt-4o", "text-embedding-3-large"))
	defer srv.Close()

	err := newEmbedder(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrModelNotFound) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrModelNotFound)
	}
}

// The quick-start footgun: copying the README's positional Ollama model while
// CHAT_PROVIDER=openai. The live check establishes the failure; the heuristic
// only enriches the message.
func TestPreflightRejectsOllamaModelWithHint(t *testing.T) {
	srv := httptest.NewServer(modelsList("gpt-4o", testChatModel))
	defer srv.Close()

	noRetry := 0
	m, err := llmopenai.NewChat(llmopenai.Config{
		APIKey: "test-key", Model: "llama3.2", BaseURL: srv.URL, MaxRetries: &noRetry,
	})
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	err = m.Preflight(context.Background())
	if !errors.Is(err, llm.ErrModelNotFound) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrModelNotFound)
	}
	if !strings.Contains(err.Error(), "CHAT_PROVIDER=ollama") {
		t.Errorf("error = %v, want a hint pointing at the Ollama provider", err)
	}
}

func TestPreflightHardFailsOnBadCredentials(t *testing.T) {
	srv := httptest.NewServer(status(401, apiError("invalid_request_error", "invalid_api_key", "invalid key")))
	defer srv.Close()

	err := newChat(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrUnauthorized) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrUnauthorized)
	}
}

// A gateway that does not implement the models endpoint must warn, not block —
// this is the failure mode most likely to break a valid setup.
func TestPreflightInconclusiveWhenListingFails(t *testing.T) {
	srv := httptest.NewServer(status(404, apiError("invalid_request_error", "", "no such endpoint")))
	defer srv.Close()

	err := newChat(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrPreflightInconclusive) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrPreflightInconclusive)
	}
}

// ---------- Streaming ----------

// sse writes chat completion chunks as text/event-stream, which is the only
// shape the SDK's stream decoder accepts.
func sse(chunks []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, chunk := range chunks {
			payload, err := json.Marshal(chunk)
			if err != nil {
				panic(err)
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			// Flushing per chunk is what makes this a stream rather than one
			// buffered body arriving at the end.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}
}

// streamChunks builds a well-formed completion stream: content deltas, a final
// chunk carrying the finish reason, and the usage-only chunk that arrives when
// stream_options.include_usage is set.
func streamChunks(deltas []string, finishReason string) []map[string]any {
	chunk := func(choices []map[string]any, usage any) map[string]any {
		c := map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion.chunk",
			"created": 1, "model": testChatModel, "choices": choices,
		}
		if usage != nil {
			c["usage"] = usage
		}
		return c
	}

	var chunks []map[string]any
	for _, d := range deltas {
		chunks = append(chunks, chunk([]map[string]any{{
			"index": 0, "delta": map[string]any{"role": "assistant", "content": d},
			"finish_reason": nil,
		}}, nil))
	}
	chunks = append(chunks, chunk([]map[string]any{{
		"index": 0, "delta": map[string]any{}, "finish_reason": finishReason,
	}}, nil))
	// choices is always empty on the usage chunk.
	return append(chunks, chunk([]map[string]any{}, map[string]any{
		"prompt_tokens": 3000, "completion_tokens": 500, "total_tokens": 3500,
	}))
}

func TestChatStreamConformance(t *testing.T) {
	llmtest.StreamConformance{
		Provider: "openai",
		New: func(baseURL string) (llm.StreamingChatModel, error) {
			noRetry := 0
			return llmopenai.NewChat(llmopenai.Config{
				APIKey: "test-key", Model: testChatModel, BaseURL: baseURL, MaxRetries: &noRetry,
			})
		},
		Fixtures: map[llmtest.Scenario]http.HandlerFunc{
			llmtest.ScenarioSuccess:   sse(streamChunks([]string{"You ", "hang ", "pawns."}, "stop")),
			llmtest.ScenarioTruncated: sse(streamChunks([]string{"You ", "hang"}, "length")),
			// A stream that finishes without ever sending content is the
			// streaming form of an empty completion.
			llmtest.ScenarioEmptyContent: sse(streamChunks(nil, "stop")),
			llmtest.ScenarioUnauthorized: status(401, apiError("invalid_request_error", "invalid_api_key", "invalid key")),
			llmtest.ScenarioRateLimited:  status(429, apiError("rate_limit_error", "rate_limit_exceeded", "slow down")),
			llmtest.ScenarioServerError:  status(500, apiError("server_error", "", "boom")),
		},
	}.Run(t)
}

// TestChatStreamMatchesChat pins the equivalence the REPL depends on: the same
// answer, delivered either way, produces the same normalized response.
func TestChatStreamMatchesChat(t *testing.T) {
	bufSrv := httptest.NewServer(completion(textChoice("You hang pawns.", "stop")))
	defer bufSrv.Close()
	strSrv := httptest.NewServer(sse(streamChunks([]string{"You ", "hang ", "pawns."}, "stop")))
	defer strSrv.Close()

	buffered, err := newChat(t, bufSrv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	streamed, err := newChat(t, strSrv.URL).ChatStream(context.Background(), llmtest.SampleRequest(),
		func(string) error { return nil })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if streamed.Text != buffered.Text {
		t.Errorf("streamed text = %q, buffered = %q", streamed.Text, buffered.Text)
	}
	if streamed.FinishReason != buffered.FinishReason {
		t.Errorf("streamed FinishReason = %q, buffered = %q", streamed.FinishReason, buffered.FinishReason)
	}
	if streamed.Model != buffered.Model {
		t.Errorf("streamed Model = %q, buffered = %q", streamed.Model, buffered.Model)
	}
	if streamed.Usage != buffered.Usage {
		t.Errorf("streamed Usage = %+v, buffered = %+v", streamed.Usage, buffered.Usage)
	}
}

// The usage-only chunk carries no choices. Reading choices[0] unguarded would
// panic on the last chunk of every successful stream.
func TestChatStreamHandlesUsageOnlyChunk(t *testing.T) {
	srv := httptest.NewServer(sse(streamChunks([]string{"a", "b"}, "stop")))
	defer srv.Close()

	resp, err := newChat(t, srv.URL).ChatStream(context.Background(), llmtest.SampleRequest(),
		func(string) error { return nil })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if resp.Usage.InputTokens != 3000 || resp.Usage.OutputTokens != 500 {
		t.Errorf("Usage = %+v, want {3000 500} from the final usage chunk", resp.Usage)
	}
}

// Usage arrives only when the request asks for it.
func TestChatStreamRequestsUsage(t *testing.T) {
	var got struct {
		Stream        bool `json:"stream"`
		StreamOptions struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		sse(streamChunks([]string{"a"}, "stop"))(w, r)
	}))
	defer srv.Close()

	if _, err := newChat(t, srv.URL).ChatStream(context.Background(), llmtest.SampleRequest(),
		func(string) error { return nil }); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if !got.Stream {
		t.Error("request did not set stream")
	}
	if !got.StreamOptions.IncludeUsage {
		t.Error("request did not set stream_options.include_usage, so usage would be zero")
	}
}
