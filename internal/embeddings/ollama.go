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