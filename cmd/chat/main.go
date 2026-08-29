package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/chesser/internal/chat"
	"github.com/chesser/internal/config"
	"github.com/chesser/internal/db"
	"github.com/chesser/internal/render"
)

const (
	defaultNumSimilar  = 100 //number of games to retrieve for context
	defaultDetailLimit = 10  //number of most relevant games to show in the response
)

func printUsage() {
	fmt.Println("Usage: go run cmd/chat/main.go <username> [chat-model]")
	fmt.Println()
	fmt.Println("Arguments:")
	fmt.Println("  username    Chess.com username to filter games")
	fmt.Println("  chat-model  Chat model for the selected CHAT_PROVIDER.")
	fmt.Println("              Overrides CHAT_MODEL, so pass a model the provider actually offers.")
	fmt.Println()
	fmt.Println("Environment variables:")
	fmt.Println("  DATABASE_URL       PostgreSQL connection string (required)")
	fmt.Println("  CHAT_PROVIDER      ollama | anthropic (default: ollama)")
	fmt.Println("  CHAT_MODEL         Chat model (default: per provider)")
	fmt.Println("  EMBED_PROVIDER     ollama (default: ollama; Anthropic has no embeddings API)")
	fmt.Println("  EMBED_MODEL        Embedding model, must be 768-dimension (default: nomic-embed-text)")
	fmt.Println("  ANTHROPIC_API_KEY  Required when CHAT_PROVIDER=anthropic")
	fmt.Println("  OLLAMA_URL         Ollama server URL (default: http://localhost:11434)")
	fmt.Println("  OLLAMA_EMBED_MODEL Alias for EMBED_MODEL when EMBED_PROVIDER=ollama")
	fmt.Println("  NO_COLOR           Set to any value to print raw markdown instead of styled output")
	fmt.Println("  CHESSER_DEBUG_PROMPT  Set to any value to dump the assembled prompt to stderr")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	username := os.Args[1]
	chatModelArg := ""
	if len(os.Args) >= 3 {
		chatModelArg = os.Args[2]
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "Error: DATABASE_URL environment variable is required")
		os.Exit(1)
	}

	// Resolve providers and credentials before the welcome banner, so an auth
	// or model failure is never revealed only after the first question.
	cfg, err := config.Resolve(config.OSEnv, chatModelArg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nGoodbye!")
		cancel()
		os.Exit(0)
	}()

	// Connect to database
	database, err := db.New(ctx, databaseURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error connecting to database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	chatModel, err := cfg.NewChatModel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	embedder, err := cfg.NewEmbedder()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := config.Preflight(ctx, os.Stderr, chatModel, embedder); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	// Chat only reads the index, so it never adopts an unstamped one.
	if err := config.CheckIndex(ctx, database, embedder, false, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Create chat service
	chatService := chat.NewService(database, chatModel, embedder, chat.Config{
		ChatModel:   cfg.ChatModel,
		Username:    username,
		NumSimilar:  defaultNumSimilar,
		DetailLimit: defaultDetailLimit,
	})

	// The renderer is built once: it reads the terminal width at startup, and
	// rebuilding it per answer would only matter if the window were resized
	// mid-session, which reflows the scrollback anyway.
	renderer := render.New(os.Stdout)

	fmt.Println("Chess Coach Chat")
	fmt.Println("================")
	fmt.Printf("Analyzing games for: %s\n", username)
	fmt.Println(cfg.Summary())
	fmt.Println()
	fmt.Println("Ask questions about your chess games.")
	fmt.Println("Commands: /clear (reset conversation), exit/quit (leave)")
	fmt.Println()

	// Start REPL loop
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())

		if input == "" {
			continue
		}
		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			break
		}
		if input == "/clear" {
			chatService.ClearHistory()
			fmt.Println("Conversation cleared.")
			continue
		}

		if err := answer(ctx, chatService, renderer, input); err != nil {
			// Fail loudly. There is deliberately no fallback to another
			// provider: silently answering from a different model is the exact
			// confusion this feature exists to prevent.
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
}

// answer asks one question and prints the result.
//
// On a styled terminal the reply is streamed as plain text so there is
// something to read while the model works, then erased and repainted as
// rendered markdown. Rendering cannot happen incrementally: wrapping, list
// alignment, and table widths are all properties of the finished document, so a
// partial one would be laid out wrong and then re-laid-out on every token.
//
// Everywhere else — a pipe, a file, NO_COLOR — nothing is streamed and the raw
// markdown is printed once. Cursor-up escapes would corrupt a captured
// transcript, and there is no live view to justify them.
func answer(ctx context.Context, svc *chat.Service, renderer *render.Renderer, question string) error {
	if !renderer.Styled() {
		fmt.Println("Thinking...")
		response, err := svc.Ask(ctx, question)
		if err != nil {
			return err
		}
		fmt.Printf("\nCoach: %s\n\n", response)
		return nil
	}

	fmt.Println()

	// Retrieval — embedding the question, then the vector search — runs before
	// the provider is called at all, so without this the terminal sits silent
	// for several seconds. It is cleared by the first token rather than left
	// above the answer.
	fmt.Print(render.Indent + "Thinking...")
	clearThinking := func() { fmt.Print("\r\x1b[2K") }

	stream := render.NewStreamWriter(os.Stdout, renderer.Width(), render.Indent)
	first := true
	response, err := svc.AskStream(ctx, question, func(delta string) error {
		if first {
			clearThinking()
			first = false
		}
		return stream.WriteString(delta)
	})
	if first {
		// No delta ever arrived: the request failed, or the answer came from a
		// path that does not call the provider.
		clearThinking()
	}
	if err != nil {
		// Erase the partial answer before the error: a half-written reply left
		// above an error message reads as though it were part of the answer.
		stream.Flush()
		stream.Erase()
		return err
	}
	if err := stream.Flush(); err != nil {
		return err
	}
	if err := stream.Erase(); err != nil {
		return err
	}

	fmt.Println(renderer.Render(response))
	fmt.Println()
	return nil
}
