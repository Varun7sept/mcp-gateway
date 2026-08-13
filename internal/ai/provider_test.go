package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeProvider is a scripted LLMProvider recording every call.
type fakeProvider struct {
	name    string
	resp    *ChatResponse
	err     error
	calls   int
	lastReq ChatRequest
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Chat(_ context.Context, req ChatRequest) (*ChatResponse, error) {
	f.calls++
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

func retryableErr(provider string) error {
	return &ProviderError{Provider: provider, Model: "m", Message: "rate limited", Retryable: true}
}

func okResp(content string) *ChatResponse {
	return &ChatResponse{Choices: []chatChoice{{Message: Message{Content: content}}}}
}

// 1. Groq success → Cerebras must not be called.
func TestChatCall_GroqSuccess_SkipsCerebras(t *testing.T) {
	groq := &fakeProvider{name: "groq", resp: okResp("groq-answer")}
	cerebras := &fakeProvider{name: "cerebras", resp: okResp("cerebras-answer")}
	b := &Brain{providers: []LLMProvider{groq, cerebras}}

	resp, err := b.chatCall(ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("chatCall returned error: %v", err)
	}
	if resp.Choices[0].Message.Content != "groq-answer" {
		t.Errorf("expected groq answer, got %q", resp.Choices[0].Message.Content)
	}
	if cerebras.calls != 0 {
		t.Errorf("cerebras was called %d times but should never be on groq success", cerebras.calls)
	}
}

// 2. Groq retryable failure → Cerebras answers.
func TestChatCall_GroqUnavailable_FallsBackToCerebras(t *testing.T) {
	groq := &fakeProvider{name: "groq", err: retryableErr("groq")}
	cerebras := &fakeProvider{name: "cerebras", resp: okResp("cerebras-answer")}
	b := &Brain{providers: []LLMProvider{groq, cerebras}}

	resp, err := b.chatCall(ChatRequest{})
	if err != nil {
		t.Fatalf("chatCall returned error: %v", err)
	}
	if resp.Choices[0].Message.Content != "cerebras-answer" {
		t.Errorf("expected cerebras fallback answer, got %q", resp.Choices[0].Message.Content)
	}
	if groq.calls != 1 || cerebras.calls != 1 {
		t.Errorf("expected one call per provider, got groq=%d cerebras=%d", groq.calls, cerebras.calls)
	}
}

// 3. All providers fail → combined error mentions every provider.
func TestChatCall_AllProvidersFail_CombinedError(t *testing.T) {
	groq := &fakeProvider{name: "groq", err: retryableErr("groq")}
	cerebras := &fakeProvider{name: "cerebras", err: retryableErr("cerebras")}
	b := &Brain{providers: []LLMProvider{groq, cerebras}}

	_, err := b.chatCall(ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	for _, want := range []string{"all LLM providers failed", "groq", "cerebras"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("combined error missing %q: %v", want, err)
		}
	}
}

// 4. Non-retryable error → no fallback, error returned immediately.
func TestChatCall_NonRetryableError_NoFallback(t *testing.T) {
	groq := &fakeProvider{
		name: "groq",
		err:  &ProviderError{Provider: "groq", Model: "m", Message: "bad request", Retryable: false},
	}
	cerebras := &fakeProvider{name: "cerebras", resp: okResp("cerebras-answer")}
	b := &Brain{providers: []LLMProvider{groq, cerebras}}

	_, err := b.chatCall(ChatRequest{})
	if err == nil {
		t.Fatal("expected non-retryable error")
	}
	var perr *ProviderError
	if !errors.As(err, &perr) || perr.Retryable {
		t.Errorf("expected non-retryable ProviderError, got %v", err)
	}
	if cerebras.calls != 0 {
		t.Errorf("cerebras called %d times despite non-retryable groq error", cerebras.calls)
	}
}

// 5. Missing CEREBRAS_API_KEY → Cerebras provider is skipped entirely.
func TestNew_NoCerebrasKey_OnlyGroq(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "")
	t.Setenv("CEREBRAS_MODEL", "")
	b := New("test-api-key")

	if len(b.providers) != 1 {
		t.Fatalf("expected exactly 1 provider, got %d", len(b.providers))
	}
	if b.providers[0].Name() != "groq" {
		t.Errorf("expected groq as the only provider, got %q", b.providers[0].Name())
	}
}

// 6. CEREBRAS_API_KEY set → fallback provider appended with its own key/model.
func TestNew_CerebrasKey_AddsFallbackProvider(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "cerebras-secret-key")
	t.Setenv("CEREBRAS_MODEL", "")
	b := New("test-api-key")

	if len(b.providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(b.providers))
	}
	cerebras, ok := b.providers[1].(*OpenAIProvider)
	if !ok {
		t.Fatalf("fallback provider is %T, want *OpenAIProvider", b.providers[1])
	}
	if cerebras.name != "cerebras" {
		t.Errorf("expected cerebras provider, got %q", cerebras.name)
	}
	if cerebras.apiKey != "cerebras-secret-key" {
		t.Errorf("cerebras must use its own API key, got %q", cerebras.apiKey)
	}
	if len(cerebras.models) != 1 || cerebras.models[0] != defaultCerebrasModel {
		t.Errorf("expected default model %q, got %v", defaultCerebrasModel, cerebras.models)
	}
}

// 6b. CEREBRAS_MODEL override is honored.
func TestNew_CerebrasModel_Override(t *testing.T) {
	t.Setenv("CEREBRAS_API_KEY", "k")
	t.Setenv("CEREBRAS_MODEL", "gpt-oss-100b")
	b := New("test-api-key")

	cerebras := b.providers[1].(*OpenAIProvider)
	if cerebras.models[0] != "gpt-oss-100b" {
		t.Errorf("expected CEREBRAS_MODEL override, got %v", cerebras.models)
	}
	if cerebras.endpoint != cerebrasEndpoint {
		t.Errorf("expected cerebras endpoint, got %q", cerebras.endpoint)
	}
}

// 7. Cerebras sends its OWN key, its OWN model, and OpenAI-style tools.
func TestCerebrasProvider_SendsOwnKeyAndModel(t *testing.T) {
	var gotAuth, gotContentType string
	var gotBody ChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"cerebras wired"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	p := NewCerebrasProvider("cerebras-secret-key", "gpt-oss-120b", srv.Client())
	p.endpoint = srv.URL

	tools := GetAvailableTools()[:1]
	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "bitcoin price"}}, Tools: tools})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Choices[0].Message.Content != "cerebras wired" {
		t.Errorf("unexpected content %q", resp.Choices[0].Message.Content)
	}
	if gotAuth != "Bearer cerebras-secret-key" {
		t.Errorf("expected cerebras Bearer key, got %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Errorf("expected application/json, got %q", gotContentType)
	}
	if gotBody.Model != "gpt-oss-120b" {
		t.Errorf("expected gpt-oss-120b on the wire, got %q", gotBody.Model)
	}
	if len(gotBody.Tools) != 1 {
		t.Errorf("expected 1 tool definition on the wire, got %d", len(gotBody.Tools))
	}
}

// 8. Groq model fallback: 404 on model 1 → same provider retries model 2.
func TestGroqProvider_ModelFallback(t *testing.T) {
	var gotModels []string
	var gotAuth string
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotAuth = r.Header.Get("Authorization")
		var body ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		gotModels = append(gotModels, body.Model)
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":{"message":"model does not exist"}}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"ok from model 2"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	p := NewGroqProvider("groq-secret-key", []string{"llama-3.3-70b-versatile", "qwen/qwen3-32b"}, srv.Client())
	p.endpoint = srv.URL

	resp, err := p.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok from model 2" {
		t.Errorf("unexpected content %q", resp.Choices[0].Message.Content)
	}
	if gotAuth != "Bearer groq-secret-key" {
		t.Errorf("expected groq Bearer key, got %q", gotAuth)
	}
	want := []string{"llama-3.3-70b-versatile", "qwen/qwen3-32b"}
	if len(gotModels) != 2 || gotModels[0] != want[0] || gotModels[1] != want[1] {
		t.Errorf("expected model fallback order %v, got %v", want, gotModels)
	}
}

// 9. A request WITHOUT tools must reach the provider with Tools == nil
// (protects the planner no-tool → direct answer path).
func TestChatCall_NoTools_StaysToolLess(t *testing.T) {
	groq := &fakeProvider{name: "groq", resp: okResp("direct answer")}
	b := &Brain{providers: []LLMProvider{groq}}

	req := ChatRequest{Messages: []Message{{Role: "user", Content: "Two Sum C++"}}}
	if _, err := b.chatCall(req); err != nil {
		t.Fatalf("chatCall returned error: %v", err)
	}
	if groq.lastReq.Tools != nil {
		t.Error("tool-less request reached the provider with tools attached")
	}
}

// 10. A tool-enabled request carries its ToolDefs all the way to the provider.
func TestChatCall_ToolsCarriedToProvider(t *testing.T) {
	groq := &fakeProvider{name: "groq", resp: okResp("answer")}
	b := &Brain{providers: []LLMProvider{groq}}

	req := ChatRequest{
		Messages: []Message{{Role: "user", Content: "bitcoin price"}},
		Tools:    GetAvailableTools(),
	}
	if _, err := b.chatCall(req); err != nil {
		t.Fatalf("chatCall returned error: %v", err)
	}
	if len(groq.lastReq.Tools) != len(GetAvailableTools()) {
		t.Errorf("expected %d tools on the wire, got %d", len(GetAvailableTools()), len(groq.lastReq.Tools))
	}
}

// No providers configured at all → clear failure rather than a panic.
func TestChatCall_NoProviders_Error(t *testing.T) {
	b := &Brain{}
	if _, err := b.chatCall(ChatRequest{}); err == nil {
		t.Error("expected error with no providers configured")
	}
}