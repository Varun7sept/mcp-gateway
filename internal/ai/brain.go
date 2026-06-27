// Package ai provides the AI brain that decides which tools to call.
// Uses Groq's free API (LLaMA 3.3 70B) with tool calling support.
//
// HOW IT WORKS:
// 1. User asks a question in natural language
// 2. We send the question + list of available tools to Groq
// 3. Groq decides which tool to call (or just answers directly)
// 4. If a tool is needed, we call it via the gateway
// 5. We send the tool result back to Groq for a final answer
// 6. Return the natural language answer to the user
package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Brain is the AI engine that processes user questions.
type Brain struct {
	apiKey     string
	models     []string
	httpClient *http.Client
}

// New creates a new AI Brain with the given Groq API key.
func New(apiKey string) *Brain {
	models := []string{
		"qwen/qwen3-32b",
		"qwen/qwen3.6-27b",
		"openai/gpt-oss-20b",
		"openai/gpt-oss-120b",
	}
	if configured := strings.TrimSpace(os.Getenv("GROQ_MODELS")); configured != "" {
		models = nil
		for _, model := range strings.Split(configured, ",") {
			if model = strings.TrimSpace(model); model != "" {
				models = append(models, model)
			}
		}
	}

	return &Brain{
		apiKey:     apiKey,
		models:     models,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ToolDef defines a tool that the AI can choose to call.
type ToolDef struct {
	Type     string   `json:"type"`
	Function FuncDef  `json:"function"`
}

type FuncDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Message represents a chat message.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ChatRequest is what we send to Groq API.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Tools    []ToolDef `json:"tools,omitempty"`
}

// ChatResponse is what Groq API returns.
type ChatResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// executeChat tries the configured Groq models in order. Rate limits are
// model-specific, so a 429 from one model can be served by another model.
func (b *Brain) executeChat(request ChatRequest) (*ChatResponse, error) {
	var failures []string

	for _, model := range b.models {
		request.Model = model
		bodyBytes, err := json.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequest(
			http.MethodPost,
			"https://api.groq.com/openai/v1/chat/completions",
			bytes.NewReader(bodyBytes),
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+b.apiKey)

		resp, err := b.httpClient.Do(req)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", model, err))
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", model, readErr))
			continue
		}

		var chatResp ChatResponse
		if err := json.Unmarshal(respBody, &chatResp); err != nil {
			failures = append(failures, fmt.Sprintf("%s: invalid response", model))
			if resp.StatusCode >= 500 {
				continue
			}
			return nil, fmt.Errorf("Groq model %s returned an invalid response: %w", model, err)
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 && chatResp.Error == nil {
			if len(chatResp.Choices) == 0 {
				failures = append(failures, model+": empty response")
				continue
			}
			return &chatResp, nil
		}

		message := http.StatusText(resp.StatusCode)
		if chatResp.Error != nil && chatResp.Error.Message != "" {
			message = chatResp.Error.Message
		}
		if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
			message += " (retry after " + retryAfter + "s)"
		}
		failures = append(failures, fmt.Sprintf("%s: %s", model, message))

		// Try another model for rate limits, unavailable/deprecated models,
		// model permission failures, and temporary Groq server errors.
		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusNotFound ||
			resp.StatusCode >= 500 {
			continue
		}
		return nil, fmt.Errorf("Groq model %s failed: %s", model, message)
	}

	return nil, fmt.Errorf(
		"all Groq models failed: %s",
		strings.Join(failures, "; "),
	)
}

// ToolCallResult is returned when the AI wants to call a tool.
type ToolCallResult struct {
	NeedsTool bool   // Does the AI want to call a tool?
	ToolName  string // Which tool to call
	Arguments map[string]any // Arguments for the tool
	ToolCallID string // ID to reference in follow-up
	DirectAnswer string // If no tool needed, the AI's direct answer
}

// GetAvailableTools returns tool definitions formatted for Groq's API.
func GetAvailableTools() []ToolDef {
	return []ToolDef{
		makeTool("get_weather", "Get the current real weather for any city in the world", map[string]any{
			"city": map[string]any{"type": "string", "description": "The city name (e.g., London, Mumbai, New York)"},
		}, []string{"city"}),
		makeTool("get_forecast", "Get a real 3-day weather forecast for any city", map[string]any{
			"city": map[string]any{"type": "string", "description": "The city name"},
		}, []string{"city"}),
		makeTool("get_user", "Get a real GitHub user profile including followers repos and bio", map[string]any{
			"username": map[string]any{"type": "string", "description": "GitHub username such as torvalds or google"},
		}, []string{"username"}),
		makeTool("list_repos", "List real public repositories for a GitHub user sorted by stars", map[string]any{
			"username": map[string]any{"type": "string", "description": "GitHub username"},
		}, []string{"username"}),
		makeTool("get_repo", "Get details about a specific GitHub repository including stars forks and description", map[string]any{
			"owner": map[string]any{"type": "string", "description": "Repository owner username"},
			"repo":  map[string]any{"type": "string", "description": "Repository name"},
		}, []string{"owner", "repo"}),
		makeTool("add_note", "Save a note permanently in the database", map[string]any{
			"title":   map[string]any{"type": "string", "description": "Note title"},
			"content": map[string]any{"type": "string", "description": "Note body text"},
		}, []string{"title", "content"}),
		makeTool("list_notes", "List all saved notes from the database", map[string]any{}, nil),
		makeTool("search_notes", "Search notes by keyword in title or content", map[string]any{
			"query": map[string]any{"type": "string", "description": "Search keyword"},
		}, []string{"query"}),
		makeTool("get_crypto_price", "Get real-time price of any cryptocurrency like Bitcoin Ethereum Solana Dogecoin", map[string]any{
			"coin": map[string]any{"type": "string", "description": "Coin ID such as bitcoin, ethereum, solana, dogecoin, cardano"},
		}, []string{"coin"}),
		makeTool("get_top_cryptos", "Get top 10 cryptocurrencies by market cap with current prices", map[string]any{}, nil),
		makeTool("get_top_news", "Get today top news headlines", map[string]any{
			"topic": map[string]any{"type": "string", "description": "Topic: general, technology, business, sports, science, health"},
		}, nil),
		makeTool("search_news", "Search for news articles about any topic or keyword", map[string]any{
			"query": map[string]any{"type": "string", "description": "Search keyword like AI or SpaceX or climate"},
		}, []string{"query"}),
		makeTool("shorten_url", "Shorten a long URL into a short link", map[string]any{
			"url": map[string]any{"type": "string", "description": "The long URL to shorten"},
		}, []string{"url"}),
		makeTool("generate_qr", "Generate a QR code image URL for any text or link", map[string]any{
			"text": map[string]any{"type": "string", "description": "Text or URL to encode in QR code"},
		}, []string{"text"}),
		makeTool("web_search", "Search the internet for factual information statistics current events or any question you cannot answer from memory", map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query like Messi World Cup goals or population of Japan"},
		}, []string{"query"}),
		makeTool("wikipedia_summary", "Get a Wikipedia summary about any topic person place event or thing", map[string]any{
			"topic": map[string]any{"type": "string", "description": "Topic name like Lionel Messi or Bitcoin or Mars"},
		}, []string{"topic"}),
		makeTool("upload_document", "Upload a document to the knowledge base for later Q and A", map[string]any{
			"name":    map[string]any{"type": "string", "description": "Document name"},
			"content": map[string]any{"type": "string", "description": "Full text content of the document"},
		}, []string{"name", "content"}),
		makeTool("ask_document", "Ask a question about uploaded documents and get relevant passages. If the user mentions a specific document name, pass it as document_name to search only that document.", map[string]any{
			"question":      map[string]any{"type": "string", "description": "Question about the documents"},
			"document_name": map[string]any{"type": "string", "description": "Optional: search only within this specific document (e.g., '176_ngo_reg_cert'). Extract this from the user's message if they mention a document name or number."},
		}, []string{"question"}),
		makeTool("list_documents", "List all uploaded documents in the knowledge base", map[string]any{}, nil),
	}
}

func makeTool(name, description string, properties map[string]any, required []string) ToolDef {
	params := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if required != nil {
		params["required"] = required
	}
	return ToolDef{
		Type: "function",
		Function: FuncDef{
			Name:        name,
			Description: description,
			Parameters:  params,
		},
	}
}

// DecideAction sends the user's question to Groq and determines what to do.
func (b *Brain) DecideAction(userMessage string, conversationHistory []Message) (*ToolCallResult, error) {
	// Build messages: system prompt + history + new message
	messages := []Message{
		{
			Role: "system",
			Content: "You are a helpful AI assistant with access to real tools. " +
				"You have these capabilities: " +
				"1) get_weather/get_forecast - real weather for any city. " +
				"2) get_user/list_repos/get_repo - real GitHub data. " +
				"3) add_note/list_notes/search_notes - save and search notes in a database. " +
				"4) get_crypto_price/get_top_cryptos - real-time cryptocurrency prices. " +
				"5) get_top_news/search_news - real news headlines. Use search_news for ANY question about current events, people in news, sports, politics, etc. " +
				"6) shorten_url/generate_qr - shorten URLs and generate QR codes. " +
				"IMPORTANT RULES: " +
				"1) For factual questions about statistics, numbers, records, history - ALWAYS use web_search or wikipedia_summary. NEVER guess numbers from memory. " +
				"2) For current events, trending topics, recent news - use search_news. " +
				"3) For general knowledge about a person/place/thing - use wikipedia_summary. " +
				"4) Only answer directly from memory for simple common knowledge (capitals, basic definitions). " +
				"Be concise and friendly. Do not use <think> tags.",
		},
	}
	messages = append(messages, conversationHistory...)
	messages = append(messages, Message{Role: "user", Content: userMessage})

	// Call Groq API
	reqBody := ChatRequest{
		Messages: messages,
		Tools:    GetAvailableTools(),
	}
	chatResp, err := b.executeChat(reqBody)
	if err != nil {
		return nil, err
	}

	choice := chatResp.Choices[0]

	// Check if the AI wants to call a tool
	if len(choice.Message.ToolCalls) > 0 {
		tc := choice.Message.ToolCalls[0]
		var args map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &args)

		return &ToolCallResult{
			NeedsTool:  true,
			ToolName:   tc.Function.Name,
			Arguments:  args,
			ToolCallID: tc.ID,
		}, nil
	}

	// No tool needed — AI answered directly
	return &ToolCallResult{
		NeedsTool:    false,
		DirectAnswer: stripThinkTags(choice.Message.Content),
	}, nil
}

// stripThinkTags removes <think>...</think> blocks from model output.
var thinkRegex = regexp.MustCompile(`(?s)<think>.*?</think>\s*`)

func stripThinkTags(s string) string {
	return strings.TrimSpace(thinkRegex.ReplaceAllString(s, ""))
}

// GenerateFinalAnswer sends the tool result back to Groq for a natural language response.
func (b *Brain) GenerateFinalAnswer(userMessage string, toolName string, toolCallID string, toolResult string) (string, error) {
	messages := []Message{
		{
			Role: "system",
			Content: "You are a helpful AI assistant. Format the tool results in a clear, " +
				"conversational way. Be concise but informative.",
		},
		{Role: "user", Content: userMessage},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: toolCallID, Type: "function", Function: FunctionCall{Name: toolName, Arguments: "{}"}},
			},
		},
		{Role: "tool", Content: toolResult, ToolCallID: toolCallID},
	}

	reqBody := ChatRequest{
		Messages: messages,
	}
	chatResp, err := b.executeChat(reqBody)
	if err != nil {
		return toolResult, err
	}

	return stripThinkTags(chatResp.Choices[0].Message.Content), nil
}
