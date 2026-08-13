package ai

import (
	"errors"
	"strings"
	"testing"
)

// chatChoice aliases the anonymous Choices element type of ChatResponse so
// test fakes can construct valid responses.
type chatChoice = struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// fakeChatClient records every ChatRequest and returns queued responses (or
// queued errors), letting tests drive and assert the routing decisions.
type fakeChatClient struct {
	t      *testing.T
	calls  []ChatRequest
	queue  []string
	errors []error
}

func (f *fakeChatClient) handle(req ChatRequest) (*ChatResponse, error) {
	f.calls = append(f.calls, req)
	var err error
	if len(f.errors) > 0 {
		err = f.errors[0]
		f.errors = f.errors[1:]
	}
	if err != nil {
		return nil, err
	}
	var content string
	if len(f.queue) > 0 {
		content = f.queue[0]
		f.queue = f.queue[1:]
	}
	return &ChatResponse{
		Choices: []chatChoice{{Message: Message{Content: content}}},
	}, nil
}

func newTestBrain(t *testing.T, fake *fakeChatClient) *Brain {
	t.Helper()
	b := New("test-api-key")
	b.chatClient = fake.handle
	return b
}

// The most important regression test: the direct-answer ChatRequest must NOT
// carry tools, and no tool may be executed, when the planner returns [].
func TestOrchestrator_ZeroTaskPath_SendsNoTools(t *testing.T) {
	fake := &fakeChatClient{
		t: t,
		queue: []string{
			`{"tasks":[]}`,              // planner: no tool required
			"C++ code for Two Sum:\n...", // direct LLM answer
		},
	}
	b := newTestBrain(t, fake)

	var callToolCalled bool
	result, err := b.ProcessWithOrchestrator(
		"Give me C++ code for Two Sum",
		nil,
		func(name string, args map[string]any) (string, error) {
			callToolCalled = true
			return "", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ProcessWithOrchestrator returned error: %v", err)
	}
	if callToolCalled {
		t.Error("callTool was invoked on the zero-task path — no tools may run")
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected exactly 2 LLM calls (planner + direct answer), got %d", len(fake.calls))
	}
	if fake.calls[1].Tools != nil {
		t.Errorf("direct-answer ChatRequest must have Tools == nil, got %d tools", len(fake.calls[1].Tools))
	}
	if result.Answer != "C++ code for Two Sum:\n..." {
		t.Errorf("unexpected answer: %q", result.Answer)
	}
	if !strings.Contains(fake.calls[1].Messages[0].Content, "No tools are available") {
		t.Error("direct-answer system prompt must state that no tools are available")
	}
}

func TestOrchestrator_ZeroTaskPath_GeneralKnowledge(t *testing.T) {
	fake := &fakeChatClient{
		t: t,
		queue: []string{
			`{"tasks":[]}`,
			"Binary search runs in O(log n) time.",
		},
	}
	b := newTestBrain(t, fake)

	var callToolCalled bool
	result, err := b.ProcessWithOrchestrator(
		"Explain binary search",
		nil,
		func(name string, args map[string]any) (string, error) {
			callToolCalled = true
			return "", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ProcessWithOrchestrator returned error: %v", err)
	}
	if callToolCalled {
		t.Error("callTool was invoked on the zero-task path")
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", len(fake.calls))
	}
	if fake.calls[1].Tools != nil {
		t.Errorf("direct-answer ChatRequest must have Tools == nil, got %d", len(fake.calls[1].Tools))
	}
	if result.Answer != "Binary search runs in O(log n) time." {
		t.Errorf("unexpected answer: %q", result.Answer)
	}
}

func TestOrchestrator_ToolPath_CurrentInformationStillExecutesTools(t *testing.T) {
	fake := &fakeChatClient{
		t: t,
		queue: []string{
			`{"tasks":[{"id":1,"description":"get bitcoin price","tool":"get_crypto_price","arguments":{"coin":"bitcoin"},"depends_on":[]}]}`,
			"The current Bitcoin price is about $100,000.",
		},
	}
	b := newTestBrain(t, fake)

	var called []string
	result, err := b.ProcessWithOrchestrator(
		"What is the current Bitcoin price?",
		nil,
		func(name string, args map[string]any) (string, error) {
			called = append(called, name)
			return `{"bitcoin":{"usd":100000}}`, nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ProcessWithOrchestrator returned error: %v", err)
	}
	if len(called) != 1 || called[0] != "get_crypto_price" {
		t.Errorf("expected get_crypto_price to be executed, got %v", called)
	}
	if len(result.Steps) != 1 || result.Steps[0].ToolName != "get_crypto_price" {
		t.Errorf("expected a get_crypto_price step, got %+v", result.Steps)
	}
}

func TestOrchestrator_ToolPath_ExplicitWebSearchStillExecutesTools(t *testing.T) {
	fake := &fakeChatClient{
		t: t,
		queue: []string{
			`{"tasks":[{"id":1,"description":"search the web","tool":"web_search","arguments":{"query":"latest MCP developments"},"depends_on":[]}]}`,
			"Here are the latest MCP developments.",
		},
	}
	b := newTestBrain(t, fake)

	var called []string
	result, err := b.ProcessWithOrchestrator(
		"Search the web for the latest MCP developments",
		nil,
		func(name string, args map[string]any) (string, error) {
			called = append(called, name)
			return "Latest MCP developments summary", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ProcessWithOrchestrator returned error: %v", err)
	}
	if len(called) != 1 || called[0] != "web_search" {
		t.Errorf("expected web_search to be executed, got %v", called)
	}
	if len(result.Steps) != 1 || result.Steps[0].ToolName != "web_search" {
		t.Errorf("expected a web_search step, got %+v", result.Steps)
	}
}

// Planner failure (CASE 2) must keep the existing tool-enabled fallback.
func TestOrchestrator_PlannerFailure_KeepsExistingFallback(t *testing.T) {
	fake := &fakeChatClient{
		t: t,
		errors: []error{
			errors.New("planner API error"),
		},
		queue: []string{
			"fallback answer from agent",
		},
	}
	b := newTestBrain(t, fake)

	result, err := b.ProcessWithOrchestrator(
		"Give me C++ code for Two Sum",
		nil,
		func(name string, args map[string]any) (string, error) {
			t.Error("callTool should not be invoked for a plain direct answer")
			return "", nil
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ProcessWithOrchestrator returned error: %v", err)
	}
	if result.Answer != "fallback answer from agent" {
		t.Errorf("unexpected answer: %q", result.Answer)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("expected 2 LLM calls (planner failure + fallback), got %d", len(fake.calls))
	}
	// The planner-failure fallback is the existing tool-enabled agent path,
	// which is allowed to carry tools per CASE 2.
	if fake.calls[1].Tools == nil {
		t.Error("planner-failure fallback should keep the tool-enabled agent path")
	}
}

func TestDirectSystemPrompt_NoTools(t *testing.T) {
	prompt := directSystemPrompt()
	if !strings.Contains(prompt, "No tools are available") {
		t.Error("direct system prompt must state no tools are available")
	}
	if strings.Contains(prompt, "web_search") {
		t.Error("direct system prompt must not advertise any tool")
	}
}