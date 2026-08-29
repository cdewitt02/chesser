// Package llm defines the provider-neutral interfaces chesser uses to talk to
// language models. Chat and embeddings are deliberately separate concepts:
// Anthropic offers no embeddings API, so a single Provider interface would have
// to lie about its capabilities.
package llm

import (
	"context"
	"fmt"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one conversational turn. There is deliberately no RoleSystem: the
// system prompt is a field on ChatRequest, not a message. That matches
// Anthropic's wire format, and the Ollama adapter prepends it as a message
// itself, so callers cannot construct a shape Anthropic rejects.
type Message struct {
	Role    Role
	Content string
}

type ChatRequest struct {
	System   string    // may be empty
	Messages []Message // must alternate, must begin with RoleUser
	Model    string    // empty => adapter's configured default

	// Optional knobs. Zero values mean "provider default"; adapters omit
	// rather than guess.
	MaxTokens   int
	Temperature *float64
	StopAfter   []string
}

// Normalized finish reasons. Anything an adapter cannot map becomes
// FinishOther.
const (
	FinishStop          = "stop"
	FinishLength        = "length"
	FinishContentFilter = "content_filter"
	FinishOther         = "other"
)

type ChatResponse struct {
	Text         string
	Model        string // model that actually served the request
	FinishReason string
	Usage        Usage
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type ChatModel interface {
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Name identifies the provider for error messages, startup banners, and
	// eval result labeling.
	Name() string
}

type Embedder interface {
	// Embed returns one vector per input, in input order. Adapters that lack
	// native batching loop internally, so callers need only one code path.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dimensions reports the vector width this embedder produces, or 0 when
	// the width is not known ahead of the first call. Startup uses it to
	// verify the vector(N) column instead of discovering a mismatch
	// mid-ingestion.
	Dimensions() int
	// Model reports the embedding model producing the vectors. It is recorded
	// alongside the index so a later change is caught rather than silently
	// degrading retrieval.
	Model() string
	Name() string
}

// StreamingChatModel is an optional capability. Nothing implements it yet; it
// exists so streaming can be added without a breaking change to ChatModel.
type StreamingChatModel interface {
	ChatModel
	ChatStream(ctx context.Context, req ChatRequest, onDelta func(string) error) (*ChatResponse, error)
}

// Preflighter is implemented by adapters that can verify credentials,
// reachability, and the configured model before the first real request.
//
// A returned error wrapping ErrPreflightInconclusive is a warning: the check
// itself could not be completed (a models endpoint that a gateway does not
// implement, say), and startup should continue. Any other error is fatal.
type Preflighter interface {
	Preflight(ctx context.Context) error
}

// EmbedOne is a convenience for the many call sites that embed exactly one
// string.
func EmbedOne(ctx context.Context, e Embedder, text string) ([]float32, error) {
	vecs, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, WrapErr(e.Name(), "embed", ErrBadResponse,
			fmt.Errorf("expected 1 vector, got %d", len(vecs)))
	}
	return vecs[0], nil
}

// ValidateMessages enforces the shape every adapter must be able to send:
// non-empty, begins with a user turn, strictly alternating. Today's history
// assembly satisfies this by construction; validating here is what keeps it
// true.
func ValidateMessages(msgs []Message) error {
	if len(msgs) == 0 {
		return fmt.Errorf("%w: messages must not be empty", ErrInvalidRequest)
	}
	if msgs[0].Role != RoleUser {
		return fmt.Errorf("%w: messages must begin with a %q turn, got %q",
			ErrInvalidRequest, RoleUser, msgs[0].Role)
	}
	for i, m := range msgs {
		switch m.Role {
		case RoleUser, RoleAssistant:
		default:
			return fmt.Errorf("%w: message %d has unsupported role %q (the system prompt belongs in ChatRequest.System)",
				ErrInvalidRequest, i, m.Role)
		}
		if i > 0 && m.Role == msgs[i-1].Role {
			return fmt.Errorf("%w: messages must alternate, but %d and %d are both %q",
				ErrInvalidRequest, i-1, i, m.Role)
		}
	}
	return nil
}
