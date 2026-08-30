package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cdewitt02/chesser/internal/llm"
	"github.com/cdewitt02/chesser/internal/llm/llmtest"
	"github.com/cdewitt02/chesser/internal/llm/ollama"
)

func newChat(t *testing.T, baseURL string) *ollama.ChatModel {
	t.Helper()
	m, err := ollama.NewChat(ollama.Config{BaseURL: baseURL, Model: "llama3.2"})
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	return m
}

func newEmbedder(t *testing.T, baseURL string) *ollama.Embedder {
	t.Helper()
	e, err := ollama.NewEmbedder(ollama.Config{BaseURL: baseURL, Model: "nomic-embed-text"})
	if err != nil {
		t.Fatalf("NewEmbedder: %v", err)
	}
	return e
}

func chatBody(content, doneReason string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"model":             "llama3.2",
			"message":           map[string]string{"role": "assistant", "content": content},
			"done_reason":       doneReason,
			"prompt_eval_count": 42,
			"eval_count":        7,
		})
	}
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}
}

func TestChatConformance(t *testing.T) {
	llmtest.ChatConformance{
		Provider: "ollama",
		New: func(baseURL string) (llm.ChatModel, error) {
			return ollama.NewChat(ollama.Config{BaseURL: baseURL, Model: "llama3.2"})
		},
		Fixtures: map[llmtest.Scenario]http.HandlerFunc{
			llmtest.ScenarioSuccess:      chatBody("You hang pawns.", "stop"),
			llmtest.ScenarioTruncated:    chatBody("You hang", "length"),
			llmtest.ScenarioEmptyContent: chatBody("", "stop"),
			llmtest.ScenarioMalformed:    status(200, "{not json"),
			llmtest.ScenarioUnauthorized: status(401, `{"error":"unauthorized"}`),
			llmtest.ScenarioRateLimited:  status(429, `{"error":"slow down"}`),
			llmtest.ScenarioServerError:  status(500, `{"error":"boom"}`),
		},
	}.Run(t)
}

func TestChatMessageValidation(t *testing.T) {
	srv := httptest.NewServer(chatBody("hi", "stop"))
	defer srv.Close()
	llmtest.RunMessageValidation(t, "ollama", newChat(t, srv.URL))
}

func TestChatSendsSystemAsFirstMessage(t *testing.T) {
	var got struct {
		Model    string `json:"model"`
		Stream   bool   `json:"stream"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		chatBody("ok", "stop")(w, r)
	}))
	defer srv.Close()

	_, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(got.Messages))
	}
	if got.Messages[0].Role != "system" {
		t.Errorf("messages[0].role = %q, want system", got.Messages[0].Role)
	}
	if got.Messages[1].Role != "user" {
		t.Errorf("messages[1].role = %q, want user", got.Messages[1].Role)
	}
	if got.Stream {
		t.Error("stream must stay false")
	}
}

func TestChatUsageIsPopulated(t *testing.T) {
	srv := httptest.NewServer(chatBody("ok", "stop"))
	defer srv.Close()

	resp, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v, want {42 7}", resp.Usage)
	}
}

// The old client returned (nil, nil) here and let a nil vector reach the
// vector(768) column.
func TestEmbedRejectsEmptyEmbedding(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	_, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a summary"})
	if !errors.Is(err, llm.ErrBadResponse) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrBadResponse)
	}
}

func TestEmbedErrorStatuses(t *testing.T) {
	cases := []struct {
		code int
		want error
	}{
		{401, llm.ErrUnauthorized},
		{429, llm.ErrRateLimited},
		{500, llm.ErrUnavailable},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(status(tc.code, `{"error":"nope"}`))
		_, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"x"})
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: error = %v, want it to wrap %v", tc.code, err, tc.want)
		}
	}
}

func TestEmbedReturnsOneVectorPerInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"embedding":[0.1,0.2,0.3]}`))
	}))
	defer srv.Close()

	vecs, err := newEmbedder(t, srv.URL).Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 {
		t.Fatalf("got %d vectors of width %d, want 2 of width 3", len(vecs), len(vecs[0]))
	}
}

func TestEmbedderDimensions(t *testing.T) {
	e, _ := ollama.NewEmbedder(ollama.Config{Model: "nomic-embed-text:latest"})
	if got := e.Dimensions(); got != 768 {
		t.Errorf("Dimensions() = %d, want 768", got)
	}
	// An unrecognized model reports 0 — "unknown", so the startup width check
	// is skipped rather than guessed.
	u, _ := ollama.NewEmbedder(ollama.Config{Model: "some-new-model"})
	if got := u.Dimensions(); got != 0 {
		t.Errorf("Dimensions() = %d, want 0 for an unknown model", got)
	}
}

func TestPreflightDetectsUnpulledModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"mistral:latest","model":"mistral:latest"}]}`))
	}))
	defer srv.Close()

	err := newChat(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrModelNotFound) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrModelNotFound)
	}
}

func TestPreflightAcceptsTaggedModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"name":"llama3.2:latest","model":"llama3.2:latest"}]}`))
	}))
	defer srv.Close()

	if err := newChat(t, srv.URL).Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

// A tags endpoint that answers but not usefully must not block startup.
func TestPreflightInconclusiveOnBadTagsResponse(t *testing.T) {
	srv := httptest.NewServer(status(404, "not found"))
	defer srv.Close()

	err := newChat(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrPreflightInconclusive) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrPreflightInconclusive)
	}
}

// ndjson writes newline-delimited JSON objects, which is how Ollama streams:
// one object per token, then a final object carrying the stats.
func ndjson(objects []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		for _, obj := range objects {
			if err := enc.Encode(obj); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// streamObjects builds a well-formed chat stream: content-bearing objects, then
// a terminal object that carries done_reason and the token counts and no text.
func streamObjects(deltas []string, doneReason string) []map[string]any {
	objects := make([]map[string]any, 0, len(deltas)+1)
	for _, d := range deltas {
		objects = append(objects, map[string]any{
			"model":   "llama3.2",
			"message": map[string]string{"role": "assistant", "content": d},
			"done":    false,
		})
	}
	return append(objects, map[string]any{
		"model":             "llama3.2",
		"message":           map[string]string{"role": "assistant", "content": ""},
		"done":              true,
		"done_reason":       doneReason,
		"prompt_eval_count": 42,
		"eval_count":        7,
	})
}

func TestChatStreamConformance(t *testing.T) {
	llmtest.StreamConformance{
		Provider: "ollama",
		New: func(baseURL string) (llm.StreamingChatModel, error) {
			return ollama.NewChat(ollama.Config{BaseURL: baseURL, Model: "llama3.2"})
		},
		Fixtures: map[llmtest.Scenario]http.HandlerFunc{
			llmtest.ScenarioSuccess:   ndjson(streamObjects([]string{"You ", "hang ", "pawns."}, "stop")),
			llmtest.ScenarioTruncated: ndjson(streamObjects([]string{"You ", "hang"}, "length")),
			// A stream whose objects never carry content is the streaming form
			// of an empty completion.
			llmtest.ScenarioEmptyContent: ndjson(streamObjects(nil, "stop")),
			llmtest.ScenarioMalformed:    status(200, "{not json\n"),
			llmtest.ScenarioUnauthorized: status(401, `{"error":"unauthorized"}`),
			llmtest.ScenarioRateLimited:  status(429, `{"error":"slow down"}`),
			llmtest.ScenarioServerError:  status(500, `{"error":"boom"}`),
		},
	}.Run(t)
}

// TestChatStreamMatchesChat pins the equivalence the REPL depends on.
func TestChatStreamMatchesChat(t *testing.T) {
	bufSrv := httptest.NewServer(chatBody("You hang pawns.", "stop"))
	defer bufSrv.Close()
	strSrv := httptest.NewServer(ndjson(streamObjects([]string{"You ", "hang ", "pawns."}, "stop")))
	defer strSrv.Close()

	buffered, err := newChat(t, bufSrv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	streamed, err := newChat(t, strSrv.URL).ChatStream(context.Background(), llmtest.SampleRequest(), func(string) error { return nil })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}

	if streamed.Text != buffered.Text {
		t.Errorf("streamed text = %q, buffered = %q", streamed.Text, buffered.Text)
	}
	if streamed.FinishReason != buffered.FinishReason {
		t.Errorf("streamed FinishReason = %q, buffered = %q", streamed.FinishReason, buffered.FinishReason)
	}
	if streamed.Usage != buffered.Usage {
		t.Errorf("streamed Usage = %+v, buffered = %+v", streamed.Usage, buffered.Usage)
	}
}

// TestChatStreamSetsStreamFlag guards the flag itself: without it Ollama
// answers with one buffered object, the conformance suite's several-deltas
// assertion is the only thing that would notice, and only by accident.
func TestChatStreamSetsStreamFlag(t *testing.T) {
	var gotStream any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotStream = body["stream"]
		ndjson(streamObjects([]string{"ok"}, "stop"))(w, r)
	}))
	defer srv.Close()

	if _, err := newChat(t, srv.URL).ChatStream(context.Background(), llmtest.SampleRequest(),
		func(string) error { return nil }); err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if gotStream != true {
		t.Errorf("request stream = %v, want true", gotStream)
	}
}

// TestChatStreamMidStreamError covers Ollama's habit of reporting failures in
// the body of a 200 response, where a status check alone would miss them.
func TestChatStreamMidStreamError(t *testing.T) {
	srv := httptest.NewServer(ndjson([]map[string]any{
		{"model": "llama3.2", "message": map[string]string{"role": "assistant", "content": "You "}, "done": false},
		{"error": "model runner has unexpectedly stopped"},
	}))
	defer srv.Close()

	_, err := newChat(t, srv.URL).ChatStream(context.Background(), llmtest.SampleRequest(),
		func(string) error { return nil })
	if !errors.Is(err, llm.ErrBadResponse) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrBadResponse)
	}
}
