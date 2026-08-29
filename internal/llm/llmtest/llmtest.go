// Package llmtest provides fakes and a shared conformance suite for llm
// adapters.
//
// The fakes exist so HybridSearcher, QueryRouter, and chat.Service become
// testable at all — the concrete-client dependency previously made every one of
// them require a live Ollama.
package llmtest

import (
	"context"
	"fmt"

	"github.com/chesser/internal/llm"
)

// FakeEmbedder returns deterministic vectors without any network.
type FakeEmbedder struct {
	Dims  int
	Err   error
	Calls [][]string
}

func NewFakeEmbedder(dims int) *FakeEmbedder { return &FakeEmbedder{Dims: dims} }

func (f *FakeEmbedder) Name() string  { return "fake" }
func (f *FakeEmbedder) Model() string { return "fake-embed" }
func (f *FakeEmbedder) Dimensions() int {
	if f.Dims == 0 {
		return 768
	}
	return f.Dims
}

func (f *FakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.Calls = append(f.Calls, texts)
	if f.Err != nil {
		return nil, f.Err
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, f.Dimensions())
		for j := range vec {
			// Deterministic and input-dependent; the values carry no meaning
			// beyond "the same text embeds the same way".
			vec[j] = float32((len(text)+j)%17) / 17
		}
		out[i] = vec
	}
	return out, nil
}

// FakeChatModel returns canned responses and records the requests it received,
// which is what makes the system-prompt and alternation rules assertable.
type FakeChatModel struct {
	Response *llm.ChatResponse
	Err      error
	Requests []llm.ChatRequest
}

func (f *FakeChatModel) Name() string { return "fake" }

func (f *FakeChatModel) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.Requests = append(f.Requests, req)
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Response != nil {
		return f.Response, nil
	}
	return &llm.ChatResponse{
		Text:         fmt.Sprintf("fake answer to %q", req.Messages[len(req.Messages)-1].Content),
		Model:        "fake-chat",
		FinishReason: llm.FinishStop,
	}, nil
}

var (
	_ llm.Embedder  = (*FakeEmbedder)(nil)
	_ llm.ChatModel = (*FakeChatModel)(nil)
)
