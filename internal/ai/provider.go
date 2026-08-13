package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
)

const (
	// groqEndpoint is the OpenAI-compatible chat completions endpoint for Groq.
	groqEndpoint = "https://api.groq.com/openai/v1/chat/completions"
	// cerebrasEndpoint is the OpenAI-compatible chat completions endpoint for Cerebras.
	cerebrasEndpoint = "https://api.cerebras.ai/v1/chat/completions"
	// defaultCerebrasModel is used when CEREBRAS_MODEL is not set.
	defaultCerebrasModel = "gpt-oss-120b"
)

// LLMProvider is one independent LLM backend (e.g. Groq, Cerebras). Each
// provider owns its endpoint, auth, and model list; the Brain owns ordering,
// fallback, and logging.
type LLMProvider interface {
	Name() string
	Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error)
}

// ProviderError describes a failed provider call. Retryable errors (rate
// limits, unavailable models, temporary server errors) may be served by
// another provider; non-retryable errors (bad requests) must be returned to
// the caller immediately.
type ProviderError struct {
	Provider  string
	Model     string
	Message   string
	Retryable bool
}

func (e *ProviderError) Error() string {
	m := e.Provider
	if e.Model != "" {
		m += "/" + e.Model
	}
	return m + " failed: " + e.Message
}

// OpenAIProvider is a generic OpenAI-compatible chat completions provider.
type OpenAIProvider struct {
	name       string
	endpoint   string
	apiKey     string
	models     []string
	httpClient *http.Client
}

// NewGroqProvider builds the primary provider that tries the given Groq
// models in order. Rate limits are model-specific, so a 429 from one model
// can be served by another model on the same provider.
func NewGroqProvider(apiKey string, models []string, client *http.Client) *OpenAIProvider {
	return &OpenAIProvider{
		name:       "groq",
		endpoint:   groqEndpoint,
		apiKey:     apiKey,
		models:     models,
		httpClient: client,
	}
}

// NewCerebrasProvider builds the fallback provider using Cerebras's own API
// key, endpoint, and model configuration.
func NewCerebrasProvider(apiKey, model string, client *http.Client) *OpenAIProvider {
	return &OpenAIProvider{
		name:       "cerebras",
		endpoint:   cerebrasEndpoint,
		apiKey:     apiKey,
		models:     []string{model},
		httpClient: client,
	}
}

// Name returns the provider identifier used in logs.
func (p *OpenAIProvider) Name() string { return p.name }

// Chat sends the request to each of the provider's models in order until one
// succeeds. Retryable failures accumulate and are tried on the next model.
func (p *OpenAIProvider) Chat(ctx context.Context, request ChatRequest) (*ChatResponse, error) {
	var failures []string

	for _, model := range p.models {
		request.Model = model
		chatResp, err := p.call(ctx, request, model)
		if err == nil {
			return chatResp, nil
		}
		if perr, ok := err.(*ProviderError); ok && !perr.Retryable {
			return nil, err
		}
		failures = append(failures, err.Error())
		log.Printf("%v", err)
	}

	return nil, &ProviderError{
		Provider:  p.name,
		Message:   "all models failed: " + strings.Join(failures, "; "),
		Retryable: true,
	}
}

// call performs a single chat completions request against p.endpoint.
func (p *OpenAIProvider) call(ctx context.Context, request ChatRequest, model string) (*ChatResponse, error) {
	bodyBytes, err := json.Marshal(request)
	if err != nil {
		return nil, &ProviderError{Provider: p.name, Model: model, Message: "marshal failed", Retryable: false}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, &ProviderError{Provider: p.name, Model: model, Message: "build request failed", Retryable: false}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, &ProviderError{Provider: p.name, Model: model, Message: err.Error(), Retryable: true}
	}

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024)) // 4 MB max
	resp.Body.Close()
	if readErr != nil {
		return nil, &ProviderError{Provider: p.name, Model: model, Message: readErr.Error(), Retryable: true}
	}

	var chatResp ChatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		if resp.StatusCode >= 500 {
			return nil, &ProviderError{Provider: p.name, Model: model, Message: "invalid response", Retryable: true}
		}
		return nil, &ProviderError{Provider: p.name, Model: model, Message: "invalid response", Retryable: false}
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 && chatResp.Error == nil {
		if len(chatResp.Choices) == 0 {
			return nil, &ProviderError{Provider: p.name, Model: model, Message: "empty response", Retryable: true}
		}
		log.Printf("[LLM] provider=%s model=%s success", p.name, model)
		return &chatResp, nil
	}

	message := http.StatusText(resp.StatusCode)
	if chatResp.Error != nil && chatResp.Error.Message != "" {
		message = chatResp.Error.Message
	}
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		message += " (retry after " + retryAfter + "s)"
	}

	// Rate limit, forbidden model, missing model, and temporary provider
	// errors are retryable — a later model or another provider can serve them.
	if resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode >= 500 {
		return nil, &ProviderError{Provider: p.name, Model: model, Message: message, Retryable: true}
	}
	return nil, &ProviderError{Provider: p.name, Model: model, Message: message, Retryable: false}
}