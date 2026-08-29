package anthropic_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chesser/internal/llm"
	llmanthropic "github.com/chesser/internal/llm/anthropic"
	"github.com/chesser/internal/llm/llmtest"
)

const testModel = "claude-opus-5"

func newChat(t *testing.T, baseURL string) *llmanthropic.ChatModel {
	t.Helper()
	m, err := llmanthropic.NewChat(llmanthropic.Config{
		APIKey:  "test-key",
		Model:   testModel,
		BaseURL: baseURL,
	})
	if err != nil {
		t.Fatalf("NewChat: %v", err)
	}
	return m
}

// message builds an Anthropic-shaped response: content is an array of typed
// blocks, not a string.
func message(blocks []map[string]any, stopReason string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":          "msg_1",
			"type":        "message",
			"role":        "assistant",
			"model":       testModel,
			"content":     blocks,
			"stop_reason": stopReason,
			"usage":       map[string]any{"input_tokens": 3000, "output_tokens": 500},
		})
	}
}

func textBlocks(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text}}
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body))
	}
}

func TestChatConformance(t *testing.T) {
	llmtest.ChatConformance{
		Provider: "anthropic",
		New: func(baseURL string) (llm.ChatModel, error) {
			noRetry := 0
			return llmanthropic.NewChat(llmanthropic.Config{
				APIKey: "test-key", Model: testModel, BaseURL: baseURL,
				MaxRetries: &noRetry,
			})
		},
		Fixtures: map[llmtest.Scenario]http.HandlerFunc{
			llmtest.ScenarioSuccess:      message(textBlocks("You hang pawns."), "end_turn"),
			llmtest.ScenarioTruncated:    message(textBlocks("You hang"), "max_tokens"),
			llmtest.ScenarioEmptyContent: message([]map[string]any{}, "end_turn"),
			llmtest.ScenarioMalformed:    status(200, "{not json"),
			llmtest.ScenarioUnauthorized: status(401, `{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`),
			llmtest.ScenarioRateLimited:  status(429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`),
			llmtest.ScenarioServerError:  status(500, `{"type":"error","error":{"type":"api_error","message":"boom"}}`),
		},
	}.Run(t)
}

func TestChatMessageValidation(t *testing.T) {
	srv := httptest.NewServer(message(textBlocks("hi"), "end_turn"))
	defer srv.Close()
	llmtest.RunMessageValidation(t, "anthropic", newChat(t, srv.URL))
}

// The system prompt is a top-level parameter. A caller that put it in
// messages[0] would produce a request Anthropic rejects.
func TestSystemPromptIsNotAMessage(t *testing.T) {
	var got struct {
		System []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"system"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		message(textBlocks("ok"), "end_turn")(w, r)
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

	if len(got.System) != 1 || got.System[0].Text != req.System {
		t.Fatalf("system = %+v, want the system prompt as a top-level parameter", got.System)
	}
	if len(got.Messages) != 3 {
		t.Fatalf("got %d messages, want 3 — the system prompt must not be one", len(got.Messages))
	}
	wantRoles := []string{"user", "assistant", "user"}
	for i, want := range wantRoles {
		if got.Messages[i].Role != want {
			t.Errorf("messages[%d].role = %q, want %q", i, got.Messages[i].Role, want)
		}
	}
}

// A naive read of .content yields an empty string on a block array, and
// splicing every block together would put reasoning in the answer.
func TestThinkingBlocksAreSkipped(t *testing.T) {
	blocks := []map[string]any{
		{"type": "thinking", "thinking": "The player loses on time in blitz.", "signature": "sig"},
		{"type": "text", "text": "You lose most endgames on time."},
	}
	srv := httptest.NewServer(message(blocks, "end_turn"))
	defer srv.Close()

	resp, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "You lose most endgames on time." {
		t.Errorf("Text = %q, want only the text block", resp.Text)
	}
	if strings.Contains(resp.Text, "The player loses on time") {
		t.Error("thinking content leaked into Text")
	}
}

func TestMultipleTextBlocksAreConcatenated(t *testing.T) {
	blocks := []map[string]any{
		{"type": "text", "text": "First. "},
		{"type": "text", "text": "Second."},
	}
	srv := httptest.NewServer(message(blocks, "end_turn"))
	defer srv.Close()

	resp, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Text != "First. Second." {
		t.Errorf("Text = %q, want both text blocks", resp.Text)
	}
}

func TestUsageIsPopulated(t *testing.T) {
	srv := httptest.NewServer(message(textBlocks("ok"), "end_turn"))
	defer srv.Close()

	resp, err := newChat(t, srv.URL).Chat(context.Background(), llmtest.SampleRequest())
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Usage.InputTokens != 3000 || resp.Usage.OutputTokens != 500 {
		t.Errorf("Usage = %+v, want {3000 500}", resp.Usage)
	}
}

func TestMissingAPIKeyIsNotConfigured(t *testing.T) {
	_, err := llmanthropic.NewChat(llmanthropic.Config{Model: testModel})
	if !errors.Is(err, llm.ErrNotConfigured) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrNotConfigured)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Error("the error must name the variable, never a key value")
	}
	if !strings.Contains(err.Error(), llmanthropic.APIKeyEnv) {
		t.Errorf("error = %v, want it to name %s", err, llmanthropic.APIKeyEnv)
	}
}

func modelsList(ids ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{
				"id": id, "type": "model", "display_name": id,
				"created_at": "2026-01-01T00:00:00Z",
			})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
	}
}

func TestPreflightAcceptsKnownModel(t *testing.T) {
	srv := httptest.NewServer(modelsList("claude-sonnet-5", testModel))
	defer srv.Close()

	if err := newChat(t, srv.URL).Preflight(context.Background()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

// The quick-start footgun: copying the README's positional Ollama model while
// CHAT_PROVIDER=anthropic. The live check establishes the failure; the
// heuristic only enriches the message.
func TestPreflightRejectsOllamaModelWithHint(t *testing.T) {
	srv := httptest.NewServer(modelsList("claude-opus-5", "claude-sonnet-5"))
	defer srv.Close()

	m, err := llmanthropic.NewChat(llmanthropic.Config{
		APIKey: "test-key", Model: "llama3.2", BaseURL: srv.URL,
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
	srv := httptest.NewServer(status(401, `{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`))
	defer srv.Close()

	err := newChat(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrUnauthorized) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrUnauthorized)
	}
}

// A gateway that does not implement the models endpoint must warn, not block.
func TestPreflightInconclusiveWhenListingFails(t *testing.T) {
	srv := httptest.NewServer(status(404, `{"type":"error","error":{"type":"not_found_error","message":"no such endpoint"}}`))
	defer srv.Close()

	err := newChat(t, srv.URL).Preflight(context.Background())
	if !errors.Is(err, llm.ErrPreflightInconclusive) {
		t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrPreflightInconclusive)
	}
}

// sse writes a series of Anthropic stream events as text/event-stream, which is
// the only shape the SDK's stream decoder accepts.
func sse(events []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, ev := range events {
			payload, err := json.Marshal(ev)
			if err != nil {
				panic(err)
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev["type"], payload)
			// Flushing per event is what makes this a stream rather than one
			// buffered body arriving at the end.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// streamEvents builds a well-formed message stream that emits deltas in order
// and closes with the given stop reason.
func streamEvents(deltas []string, stopReason string) []map[string]any {
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": testModel,
			"content": []any{}, "stop_reason": nil,
			"usage": map[string]any{"input_tokens": 3000, "output_tokens": 0},
		}},
		{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}},
	}
	for _, d := range deltas {
		events = append(events, map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": d},
		})
	}
	return append(events,
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 500}},
		map[string]any{"type": "message_stop"},
	)
}

func TestChatStreamConformance(t *testing.T) {
	llmtest.StreamConformance{
		Provider: "anthropic",
		New: func(baseURL string) (llm.StreamingChatModel, error) {
			noRetry := 0
			return llmanthropic.NewChat(llmanthropic.Config{
				APIKey: "test-key", Model: testModel, BaseURL: baseURL,
				MaxRetries: &noRetry,
			})
		},
		Fixtures: map[llmtest.Scenario]http.HandlerFunc{
			llmtest.ScenarioSuccess:   sse(streamEvents([]string{"You ", "hang ", "pawns."}, "end_turn")),
			llmtest.ScenarioTruncated: sse(streamEvents([]string{"You ", "hang"}, "max_tokens")),
			// A stream that opens a text block and never fills it is the
			// streaming form of an empty completion.
			llmtest.ScenarioEmptyContent: sse(streamEvents(nil, "end_turn")),
			llmtest.ScenarioUnauthorized: status(401, `{"type":"error","error":{"type":"authentication_error","message":"invalid key"}}`),
			llmtest.ScenarioRateLimited:  status(429, `{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`),
			llmtest.ScenarioServerError:  status(500, `{"type":"error","error":{"type":"api_error","message":"boom"}}`),
		},
	}.Run(t)
}

// TestChatStreamMatchesChat pins the equivalence the REPL depends on: the same
// answer, delivered either way, produces the same normalized response.
func TestChatStreamMatchesChat(t *testing.T) {
	const answer = "You hang pawns."

	bufSrv := httptest.NewServer(message(textBlocks(answer), "end_turn"))
	defer bufSrv.Close()
	strSrv := httptest.NewServer(sse(streamEvents([]string{"You ", "hang ", "pawns."}, "end_turn")))
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
	if streamed.Model != buffered.Model {
		t.Errorf("streamed Model = %q, buffered = %q", streamed.Model, buffered.Model)
	}
	if streamed.Usage != buffered.Usage {
		t.Errorf("streamed Usage = %+v, buffered = %+v", streamed.Usage, buffered.Usage)
	}
}

// TestChatStreamSkipsThinkingDeltas guards the one way a stream can show the
// user text the buffered path filters out.
func TestChatStreamSkipsThinkingDeltas(t *testing.T) {
	events := []map[string]any{
		{"type": "message_start", "message": map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": testModel,
			"content": []any{}, "stop_reason": nil,
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 0},
		}},
		{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}},
		{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "thinking_delta", "thinking": "Let me count the pawns."}},
		{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "signature_delta", "signature": "sig123"}},
		{"type": "content_block_stop", "index": 0},
		{"type": "content_block_start", "index": 1,
			"content_block": map[string]any{"type": "text", "text": ""}},
		{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "text_delta", "text": "You hang pawns."}},
		{"type": "content_block_stop", "index": 1},
		{"type": "message_delta",
			"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 20}},
		{"type": "message_stop"},
	}
	srv := httptest.NewServer(sse(events))
	defer srv.Close()

	var got strings.Builder
	resp, err := newChat(t, srv.URL).ChatStream(context.Background(), llmtest.SampleRequest(),
		func(d string) error { got.WriteString(d); return nil })
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if got.String() != "You hang pawns." {
		t.Errorf("deltas = %q, want only the text block", got.String())
	}
	if resp.Text != "You hang pawns." {
		t.Errorf("Text = %q, want only the text block", resp.Text)
	}
}
