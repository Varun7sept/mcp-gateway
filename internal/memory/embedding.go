package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type EmbeddingConfig struct {
	Endpoint string
	APIKey   string
	Model    string
}

type EmbeddingGenerator interface {
	Generate(text string) ([]float64, error)
}

type GroqEmbeddingGenerator struct {
	client *http.Client
	config EmbeddingConfig
}

func NewGroqEmbeddingGenerator(apiKey, model string) *GroqEmbeddingGenerator {
	return &GroqEmbeddingGenerator{
		client: &http.Client{Timeout: 30 * time.Second},
		config: EmbeddingConfig{
			Endpoint: "https://api.groq.com/openai/v1/embeddings",
			APIKey:   apiKey,
			Model:    model,
		},
	}
}

func (g *GroqEmbeddingGenerator) Generate(text string) ([]float64, error) {
	reqBody := map[string]any{
		"model": g.config.Model,
		"input": text,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal embedding request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, g.config.Endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.config.APIKey)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedding response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embResp struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("failed to parse embedding response: %w", err)
	}

	if len(embResp.Data) == 0 {
		return nil, fmt.Errorf("embedding response contained no data")
	}

	return embResp.Data[0].Embedding, nil
}