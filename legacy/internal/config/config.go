// Package config resolves provider selection from the environment.
//
// Both entrypoints — cmd/chat and cmd/data — resolve through here, so they
// cannot drift into the split-brain where chat runs on one provider and
// ingestion silently runs on another.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/cdewitt02/chesser/internal/llm"
	llmanthropic "github.com/cdewitt02/chesser/internal/llm/anthropic"
	llmollama "github.com/cdewitt02/chesser/internal/llm/ollama"
	llmopenai "github.com/cdewitt02/chesser/internal/llm/openai"
)

// Provider names.
const (
	ProviderOllama    = llmollama.ProviderName
	ProviderAnthropic = llmanthropic.ProviderName
	ProviderOpenAI    = llmopenai.ProviderName
)

// The two lists differ, which is the whole reason chat and embeddings are
// selected separately: Anthropic exposes no embeddings API.
var (
	chatProviders  = []string{ProviderOllama, ProviderAnthropic, ProviderOpenAI}
	embedProviders = []string{ProviderOllama, ProviderOpenAI}
)

// Config is the resolved provider selection. Both defaults are ollama, so an
// existing setup with only DATABASE_URL and OLLAMA_URL behaves identically and
// the tool never starts spending money because a default moved.
type Config struct {
	ChatProvider string
	ChatModel    string

	EmbedProvider string
	EmbedModel    string

	OllamaURL string

	// API keys are never printed, only passed to the adapter that needs them.
	anthropicAPIKey string
	openaiAPIKey    string
}

// Env reads one environment variable. Tests pass a map lookup.
type Env func(string) string

// OSEnv reads the process environment.
func OSEnv(key string) string { return os.Getenv(key) }

// Resolve builds a Config from the environment.
//
// chatModelOverride is the positional CLI argument, which is the most specific
// source for the chat model. Precedence is: positional arg -> CHAT_MODEL ->
// provider default.
func Resolve(env Env, chatModelOverride string) (*Config, error) {
	if env == nil {
		env = OSEnv
	}

	cfg := &Config{
		ChatProvider:  providerOr(env("CHAT_PROVIDER"), ProviderOllama),
		EmbedProvider: providerOr(env("EMBED_PROVIDER"), ProviderOllama),
		OllamaURL:     valueOr(env("OLLAMA_URL"), llmollama.DefaultBaseURL),
	}

	if !contains(chatProviders, cfg.ChatProvider) {
		return nil, fmt.Errorf("unknown CHAT_PROVIDER %q; valid values: %s",
			cfg.ChatProvider, strings.Join(chatProviders, ", "))
	}
	if !contains(embedProviders, cfg.EmbedProvider) {
		if cfg.EmbedProvider == ProviderAnthropic {
			return nil, fmt.Errorf(
				"EMBED_PROVIDER=anthropic is not supported: Anthropic offers no embeddings API. " +
					"Keep EMBED_PROVIDER=ollama or use EMBED_PROVIDER=openai " +
					"(chat and embeddings are selected independently)")
		}
		return nil, fmt.Errorf("unknown EMBED_PROVIDER %q; valid values: %s",
			cfg.EmbedProvider, strings.Join(embedProviders, ", "))
	}

	// Chat model.
	cfg.ChatModel = firstNonEmpty(chatModelOverride, env("CHAT_MODEL"))
	if cfg.ChatModel == "" {
		switch cfg.ChatProvider {
		case ProviderOllama:
			cfg.ChatModel = llmollama.DefaultChatModel
		case ProviderAnthropic:
			cfg.ChatModel = llmanthropic.DefaultModel
		case ProviderOpenAI:
			cfg.ChatModel = llmopenai.DefaultChatModel
		}
	}

	// Embed model. OLLAMA_EMBED_MODEL stays a working alias for EMBED_MODEL —
	// it is documented in the README today and costs one line to honor — but
	// only while the embed provider is Ollama. Honoring it otherwise would
	// send a still-exported "nomic-embed-text" to OpenAI.
	cfg.EmbedModel = env("EMBED_MODEL")
	if cfg.EmbedProvider == ProviderOllama {
		cfg.EmbedModel = firstNonEmpty(cfg.EmbedModel, env("OLLAMA_EMBED_MODEL"))
	}
	cfg.EmbedModel = strings.TrimSpace(cfg.EmbedModel)
	if cfg.EmbedModel == "" {
		switch cfg.EmbedProvider {
		case ProviderOllama:
			cfg.EmbedModel = llmollama.DefaultEmbedModel
		case ProviderOpenAI:
			cfg.EmbedModel = llmopenai.DefaultEmbedModel
		}
	}

	// Credentials are checked when the chat model is constructed, which
	// cmd/chat does before its welcome banner. Doing it here instead would
	// make ingestion — which needs no chat provider at all — fail over a
	// missing chat credential.
	cfg.anthropicAPIKey = strings.TrimSpace(env(llmanthropic.APIKeyEnv))
	cfg.openaiAPIKey = strings.TrimSpace(env(llmopenai.APIKeyEnv))

	return cfg, nil
}

// UsesHostedProvider reports whether any selected provider sends data
// off-machine.
func (c *Config) UsesHostedProvider() bool {
	return c.ChatProvider != ProviderOllama || c.EmbedProvider != ProviderOllama
}

func (c *Config) NewChatModel() (llm.ChatModel, error) {
	switch c.ChatProvider {
	case ProviderOllama:
		return llmollama.NewChat(llmollama.Config{BaseURL: c.OllamaURL, Model: c.ChatModel})
	case ProviderAnthropic:
		return llmanthropic.NewChat(llmanthropic.Config{APIKey: c.anthropicAPIKey, Model: c.ChatModel})
	case ProviderOpenAI:
		return llmopenai.NewChat(llmopenai.Config{APIKey: c.openaiAPIKey, Model: c.ChatModel})
	default:
		return nil, fmt.Errorf("unknown chat provider %q", c.ChatProvider)
	}
}

func (c *Config) NewEmbedder() (llm.Embedder, error) {
	switch c.EmbedProvider {
	case ProviderOllama:
		return llmollama.NewEmbedder(llmollama.Config{BaseURL: c.OllamaURL, Model: c.EmbedModel})
	case ProviderOpenAI:
		// Dimensions is left at its default, which is the width the
		// game_summaries column already declares. An index built by another
		// embedder is caught by CheckIndex, not here.
		return llmopenai.NewEmbedder(llmopenai.Config{APIKey: c.openaiAPIKey, Model: c.EmbedModel})
	default:
		return nil, fmt.Errorf("unknown embed provider %q", c.EmbedProvider)
	}
}

// Summary is the resolved configuration, printed at startup so a user who set
// only CHAT_PROVIDER can see that embeddings stayed local.
func (c *Config) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Chat:       %s / %s\n", c.ChatProvider, c.ChatModel)
	fmt.Fprintf(&b, "Embeddings: %s / %s", c.EmbedProvider, c.EmbedModel)
	if c.UsesHostedProvider() {
		b.WriteString("\nNote: a hosted provider is selected — game summaries and the username are sent to a third party.")
	}
	return b.String()
}

// providerOr normalizes a provider name, which is case-insensitive.
func providerOr(v, fallback string) string {
	if v = strings.TrimSpace(v); v == "" {
		return fallback
	}
	return strings.ToLower(v)
}

func valueOr(v, fallback string) string {
	if v = strings.TrimSpace(v); v == "" {
		return fallback
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
