package config_test

import (
	"strings"
	"testing"

	"github.com/chesser/internal/config"
)

func envOf(pairs map[string]string) config.Env {
	return func(key string) string { return pairs[key] }
}

func TestResolvePrecedence(t *testing.T) {
	cases := []struct {
		name          string
		env           map[string]string
		positional    string
		wantChatProv  string
		wantChatModel string
		wantEmbedProv string
		wantEmbedMdl  string
		wantOllamaURL string
	}{
		{
			// The backward-compatibility case: a user with only the
			// pre-existing variables must see identical behavior.
			name:          "defaults are ollama end to end",
			env:           map[string]string{},
			wantChatProv:  "ollama",
			wantChatModel: "llama3.2",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "nomic-embed-text",
			wantOllamaURL: "http://localhost:11434",
		},
		{
			name:          "pre-existing OLLAMA_ variables still apply",
			env:           map[string]string{"OLLAMA_URL": "http://box:11434", "OLLAMA_EMBED_MODEL": "mxbai-embed-large"},
			wantChatProv:  "ollama",
			wantChatModel: "llama3.2",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "mxbai-embed-large",
			wantOllamaURL: "http://box:11434",
		},
		{
			name:          "EMBED_MODEL outranks the OLLAMA_EMBED_MODEL alias",
			env:           map[string]string{"EMBED_MODEL": "bge-m3", "OLLAMA_EMBED_MODEL": "nomic-embed-text"},
			wantChatProv:  "ollama",
			wantChatModel: "llama3.2",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "bge-m3",
			wantOllamaURL: "http://localhost:11434",
		},
		{
			name:          "anthropic chat keeps embeddings local",
			env:           map[string]string{"CHAT_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-test"},
			wantChatProv:  "anthropic",
			wantChatModel: "claude-opus-5",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "nomic-embed-text",
			wantOllamaURL: "http://localhost:11434",
		},
		{
			name:          "CHAT_MODEL overrides the provider default",
			env:           map[string]string{"CHAT_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "sk-test", "CHAT_MODEL": "claude-sonnet-5"},
			wantChatProv:  "anthropic",
			wantChatModel: "claude-sonnet-5",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "nomic-embed-text",
			wantOllamaURL: "http://localhost:11434",
		},
		{
			// The positional argument is the most specific source, which is
			// what keeps `go run cmd/chat/main.go <username> [model]` working.
			name:          "positional argument outranks CHAT_MODEL",
			env:           map[string]string{"CHAT_MODEL": "llama3.1"},
			positional:    "mistral",
			wantChatProv:  "ollama",
			wantChatModel: "mistral",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "nomic-embed-text",
			wantOllamaURL: "http://localhost:11434",
		},
		{
			name:          "provider names are case-insensitive",
			env:           map[string]string{"CHAT_PROVIDER": "Anthropic", "ANTHROPIC_API_KEY": "sk-test"},
			wantChatProv:  "anthropic",
			wantChatModel: "claude-opus-5",
			wantEmbedProv: "ollama",
			wantEmbedMdl:  "nomic-embed-text",
			wantOllamaURL: "http://localhost:11434",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.Resolve(envOf(tc.env), tc.positional)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if cfg.ChatProvider != tc.wantChatProv {
				t.Errorf("ChatProvider = %q, want %q", cfg.ChatProvider, tc.wantChatProv)
			}
			if cfg.ChatModel != tc.wantChatModel {
				t.Errorf("ChatModel = %q, want %q", cfg.ChatModel, tc.wantChatModel)
			}
			if cfg.EmbedProvider != tc.wantEmbedProv {
				t.Errorf("EmbedProvider = %q, want %q", cfg.EmbedProvider, tc.wantEmbedProv)
			}
			if cfg.EmbedModel != tc.wantEmbedMdl {
				t.Errorf("EmbedModel = %q, want %q", cfg.EmbedModel, tc.wantEmbedMdl)
			}
			if cfg.OllamaURL != tc.wantOllamaURL {
				t.Errorf("OllamaURL = %q, want %q", cfg.OllamaURL, tc.wantOllamaURL)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantText []string
	}{
		{
			name:     "unknown chat provider lists valid values",
			env:      map[string]string{"CHAT_PROVIDER": "gemini"},
			wantText: []string{"CHAT_PROVIDER", "ollama", "anthropic"},
		},
		{
			// Anthropic has no embeddings API, so this is explained rather
			// than silently falling back.
			name:     "anthropic embeddings are refused with an explanation",
			env:      map[string]string{"EMBED_PROVIDER": "anthropic"},
			wantText: []string{"no embeddings API", "EMBED_PROVIDER=ollama"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := config.Resolve(envOf(tc.env), "")
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error = %q, want it to mention %q", err, want)
				}
			}
		})
	}
}

// The credential check lives in NewChatModel, which cmd/chat calls before its
// welcome banner. Resolve itself stays credential-free so ingestion — which
// needs no chat provider — is not blocked by a missing chat key.
func TestMissingAPIKeyFailsWhenBuildingTheChatModel(t *testing.T) {
	cfg, err := config.Resolve(envOf(map[string]string{"CHAT_PROVIDER": "anthropic"}), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := cfg.NewChatModel(); err == nil {
		t.Fatal("want an error, got nil")
	} else if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("error = %q, want it to name ANTHROPIC_API_KEY", err)
	}

	// The embedder is unaffected: it never needed that credential.
	if _, err := cfg.NewEmbedder(); err != nil {
		t.Errorf("NewEmbedder: %v", err)
	}
}

// The key must never reach a message or the startup banner.
func TestSummaryNeverLeaksTheKey(t *testing.T) {
	cfg, err := config.Resolve(envOf(map[string]string{
		"CHAT_PROVIDER":     "anthropic",
		"ANTHROPIC_API_KEY": "sk-ant-secret-value",
	}), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	summary := cfg.Summary()
	if strings.Contains(summary, "sk-ant-secret-value") {
		t.Fatal("Summary leaked the API key")
	}
	if !strings.Contains(summary, "sent to a third party") {
		t.Error("Summary must state that a hosted provider sends data off-machine")
	}
	if !cfg.UsesHostedProvider() {
		t.Error("UsesHostedProvider() = false, want true")
	}
}

func TestLocalOnlySetupSaysNothingAboutEgress(t *testing.T) {
	cfg, err := config.Resolve(envOf(map[string]string{}), "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if cfg.UsesHostedProvider() {
		t.Error("the default configuration must stay account-free and local")
	}
	if strings.Contains(cfg.Summary(), "third party") {
		t.Error("a local-only setup must not warn about egress")
	}
}
