package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chesser/internal/llm"
	"github.com/chesser/internal/llm/llmtest"
	"github.com/chesser/internal/llm/ollama"
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
