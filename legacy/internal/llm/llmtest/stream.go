package llmtest

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cdewitt02/chesser/internal/llm"
)

// StreamConformance runs the streaming half of the shared suite.
//
// Its central assertion is equivalence: the deltas a caller displays must
// concatenate to exactly the text the same request would have returned
// non-streamed. A provider whose stream drops a fragment, or splices in
// thinking text the buffered path filters out, would show the user one answer
// and record another in the conversation history.
type StreamConformance struct {
	Provider string
	// New builds the adapter under test, pointed at the fixture server.
	New func(baseURL string) (llm.StreamingChatModel, error)
	// Fixtures supply provider-shaped streaming responses per scenario. A
	// scenario with no fixture is skipped.
	Fixtures map[Scenario]http.HandlerFunc
}

// errConsumer is returned by the onDelta callback in the consumer-failure case.
// It is a value the adapter has never seen, which is what makes "returned
// unwrapped" checkable.
var errConsumer = errors.New("llmtest: consumer refused a delta")

func (c StreamConformance) Run(t *testing.T) {
	t.Helper()

	for _, exp := range chatExpectations {
		handler, ok := c.Fixtures[exp.scenario]
		if !ok {
			continue
		}
		t.Run(c.Provider+"/stream/"+string(exp.scenario), func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()

			model, err := c.New(srv.URL)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			var deltas []string
			resp, err := model.ChatStream(context.Background(), SampleRequest(), func(d string) error {
				deltas = append(deltas, d)
				return nil
			})

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

			// The deltas are the user-visible answer; resp.Text is what goes
			// into history. They must be the same text.
			joined := strings.TrimSpace(strings.Join(deltas, ""))
			if joined != resp.Text {
				t.Errorf("concatenated deltas = %q, want %q", joined, resp.Text)
			}
			if len(deltas) < 2 {
				t.Errorf("got %d delta(s); a streaming fixture must deliver several, "+
					"or this suite would pass against a buffered implementation", len(deltas))
			}
		})
	}

	// A failure inside the caller's callback must surface as the caller's own
	// error, not be reclassified as a provider fault. The REPL relies on this
	// to tell "the terminal write failed" from "Anthropic is down".
	if handler, ok := c.Fixtures[ScenarioSuccess]; ok {
		t.Run(c.Provider+"/stream/consumer error", func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()

			model, err := c.New(srv.URL)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = model.ChatStream(context.Background(), SampleRequest(), func(string) error {
				return errConsumer
			})
			if !errors.Is(err, errConsumer) {
				t.Fatalf("error = %v, want it to wrap the consumer's own error", err)
			}
			for _, sentinel := range []error{llm.ErrUnavailable, llm.ErrBadResponse} {
				if errors.Is(err, sentinel) {
					t.Errorf("consumer error was reclassified as %v", sentinel)
				}
			}
		})
	}

	// Message validation must happen before any request is sent, exactly as it
	// does for Chat.
	t.Run(c.Provider+"/stream/rejects bad messages", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("adapter sent a request for a conversation it should have rejected")
		}))
		defer srv.Close()

		model, err := c.New(srv.URL)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = model.ChatStream(context.Background(), llm.ChatRequest{
			Messages: []llm.Message{{Role: llm.RoleAssistant, Content: "hi"}},
		}, func(string) error { return nil })
		if !errors.Is(err, llm.ErrInvalidRequest) {
			t.Fatalf("error = %v, want it to wrap %v", err, llm.ErrInvalidRequest)
		}
	})
}
