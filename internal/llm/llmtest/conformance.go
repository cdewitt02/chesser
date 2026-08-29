package llmtest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/chesser/internal/llm"
)

// Scenario names a wire-level situation every provider can produce. The JSON
// that produces it differs per provider; the normalized outcome must not.
type Scenario string

const (
	ScenarioSuccess      Scenario = "success"
	ScenarioEmptyContent Scenario = "empty content"
	ScenarioTruncated    Scenario = "truncated response"
	ScenarioUnauthorized Scenario = "401 unauthorized"
	ScenarioRateLimited  Scenario = "429 rate limited"
	ScenarioServerError  Scenario = "500 server error"
	ScenarioMalformed    Scenario = "malformed body"
)

// ChatConformance runs one table of cases against a chat adapter.
//
// It asserts on error classification, never on attempt counts: retry behavior
// is deliberately non-uniform — the SDK retries for hosted providers while the
// Ollama adapter fails fast against a local process.
type ChatConformance struct {
	Provider string
	// New builds the adapter under test, pointed at the fixture server.
	New func(baseURL string) (llm.ChatModel, error)
	// Fixtures supply provider-shaped responses per scenario. A scenario with
	// no fixture is skipped.
	Fixtures map[Scenario]http.HandlerFunc
}

// expectations are the normalized semantics every adapter must produce.
var chatExpectations = []struct {
	scenario Scenario
	wantErr  error // nil means the call must succeed
	// check runs on a successful response.
	check func(t *testing.T, resp *llm.ChatResponse)
}{
	{
		scenario: ScenarioSuccess,
		check: func(t *testing.T, resp *llm.ChatResponse) {
			if resp.Text == "" {
				t.Error("want non-empty text")
			}
			if resp.FinishReason != llm.FinishStop {
				t.Errorf("FinishReason = %q, want %q", resp.FinishReason, llm.FinishStop)
			}
		},
	},
	{
		scenario: ScenarioTruncated,
		check: func(t *testing.T, resp *llm.ChatResponse) {
			if resp.FinishReason != llm.FinishLength {
				t.Errorf("FinishReason = %q, want %q — truncation must be visible",
					resp.FinishReason, llm.FinishLength)
			}
		},
	},
	{scenario: ScenarioEmptyContent, wantErr: llm.ErrBadResponse},
	{scenario: ScenarioMalformed, wantErr: llm.ErrBadResponse},
	{scenario: ScenarioUnauthorized, wantErr: llm.ErrUnauthorized},
	{scenario: ScenarioRateLimited, wantErr: llm.ErrRateLimited},
	{scenario: ScenarioServerError, wantErr: llm.ErrUnavailable},
}

// SampleRequest is the message shape every adapter must accept: a system
// prompt as a field, and messages that begin with a user turn and alternate.
func SampleRequest() llm.ChatRequest {
	return llm.ChatRequest{
		System: "You are a chess coach.",
		Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "Why do I lose in the endgame?"},
		},
	}
}

func (c ChatConformance) Run(t *testing.T) {
	t.Helper()
	for _, exp := range chatExpectations {
		handler, ok := c.Fixtures[exp.scenario]
		if !ok {
			continue
		}
		t.Run(c.Provider+"/"+string(exp.scenario), func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()

			model, err := c.New(srv.URL)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			resp, err := model.Chat(context.Background(), SampleRequest())

			if exp.wantErr != nil {
				if err == nil {
					t.Fatalf("want error %v, got response %+v", exp.wantErr, resp)
				}
				if !errors.Is(err, exp.wantErr) {
					t.Fatalf("error = %v, want it to wrap %v", err, exp.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if exp.check != nil {
				exp.check(t, resp)
			}
		})
	}
}

// RunMessageValidation asserts the message-shape rules every adapter enforces,
// so a malformed conversation is rejected here rather than by one provider's
// API and not another's.
func RunMessageValidation(t *testing.T, provider string, model llm.ChatModel) {
	t.Helper()
	cases := []struct {
		name string
		msgs []llm.Message
	}{
		{"empty", nil},
		{"starts with assistant", []llm.Message{{Role: llm.RoleAssistant, Content: "hi"}}},
		{"non-alternating", []llm.Message{
			{Role: llm.RoleUser, Content: "a"},
			{Role: llm.RoleUser, Content: "b"},
		}},
		{"system smuggled into messages", []llm.Message{{Role: "system", Content: "be nice"}}},
	}
	for _, tc := range cases {
		t.Run(provider+"/reject "+tc.name, func(t *testing.T) {
			_, err := model.Chat(context.Background(), llm.ChatRequest{Messages: tc.msgs})
			if !errors.Is(err, llm.ErrInvalidRequest) {
				t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrInvalidRequest)
			}
		})
	}
}
