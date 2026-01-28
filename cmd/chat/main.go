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
	"github.com/chesser/internal/db"
	"github.com/chesser/internal/embeddings"
)

const (
	defaultChatModel  = "llama3.2"
	defaultOllamaURL  = "http://localhost:11434"
	defaultEmbedModel = "nomic-embed-text"
	defaultNumSimilar = 100 //number of games to retrieve for context
	defaultDetailLimit = 10 //number of most relevant games to show in the response
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run cmd/chat/main.go <username> [chat-model]")
		fmt.Println()
		fmt.Println("Arguments:")
		fmt.Println("  username    Chess.com username to filter games")
		fmt.Println("  chat-model  Ollama model for chat (default: llama3.2)")
		fmt.Println()
		fmt.Println("Environment variables:")
		fmt.Println("  DATABASE_URL       PostgreSQL connection string (required)")
		fmt.Println("  OLLAMA_URL         Ollama server URL (default: http://localhost:11434)")
		fmt.Println("  OLLAMA_EMBED_MODEL Embedding model (default: nomic-embed-text)")
		os.Exit(1)
	}

	username := os.Args[1]
	chatModel := defaultChatModel
	if len(os.Args) >= 3 {
		chatModel = os.Args[2]
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		fmt.Fprintln(os.Stderr, "Error: DATABASE_URL environment variable is required")
		os.Exit(1)
	}

	ollamaURL := os.Getenv("OLLAMA_URL")
	if ollamaURL == "" {
		ollamaURL = defaultOllamaURL
	}

	embedModel := os.Getenv("OLLAMA_EMBED_MODEL")
	if embedModel == "" {
		embedModel = defaultEmbedModel
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

	// Create Ollama client (used for both embeddings and chat)
	ollamaClient := embeddings.New(ollamaURL, embedModel)

	// Create chat service
	chatService := chat.NewService(database, ollamaClient, chat.Config{
		ChatModel:   chatModel,
		Username:    username,
		NumSimilar:  defaultNumSimilar,
		DetailLimit: defaultDetailLimit,
	})

	fmt.Println("Chess Coach Chat")
	fmt.Println("================")
	fmt.Printf("Analyzing games for: %s\n", username)
	fmt.Printf("Using model: %s\n", chatModel)
	fmt.Println()
	fmt.Println("Ask questions about your chess games. Type 'exit' or 'quit' to leave.")
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

		fmt.Println("Thinking...")
		response, err := chatService.Ask(ctx, input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			continue
		}

		fmt.Printf("\nCoach: %s\n\n", response)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
}
