package embeddings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client wraps the Ollama HTTP API
type Client struct {
    baseURL    string       // "http://localhost:11434"
    httpClient *http.Client // Reuse for connection pooling
    model      string       // "nomic-embed-text"
}

// Request structure for Ollama embeddings API
type embeddingRequest struct {
    Model  string `json:"model"`
    Prompt string `json:"prompt"`
}

// Response structure from Ollama embeddings API
type embeddingResponse struct {
    Embedding []float32 `json:"embedding"`
}

type ChatMessage struct {
    Role    string `json:"role"`   
    Content string `json:"content"`
}

type chatRequest struct {
    Model    string        `json:"model"`
    Messages []ChatMessage `json:"messages"`
    Stream   bool          `json:"stream"`
}

type chatResponse struct {
    Message ChatMessage `json:"message"`
}

func New(baseURL, model string) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		model: model,
	}
}

func (c *Client) GetEmbedding(text string) ([]float32, error) {

	request := embeddingRequest{
		Model: c.model,
		Prompt: text,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/api/embeddings", c.baseURL)

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response embeddingResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return nil, err
	}

	return response.Embedding, nil
}

// chat completion request to Ollama
func (c *Client) Chat(model string, messages []ChatMessage) (string, error) {
	request := chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal chat request: %w", err)
	}

	url := fmt.Sprintf("%s/api/chat", c.baseURL)

	chatClient := &http.Client{
		Timeout: 120 * time.Second,
	}

	resp, err := chatClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to send chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("chat request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read chat response: %w", err)
	}

	var response chatResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal chat response: %w", err)
	}

	return response.Message.Content, nil
}