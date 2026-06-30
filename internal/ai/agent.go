// agent.go implements a multi-step AI agent that can call multiple tools
// in sequence to answer complex questions.
//
// HOW IT WORKS:
// 1. Send user's question to AI with available tools
// 2. If AI wants to call a tool → call it, get result
// 3. Send the result back to AI → it might want ANOTHER tool
// 4. Repeat until AI gives a final text answer (max 5 steps)
// 5. Return the final answer + list of all steps taken
package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// AgentStep represents one step the agent took.
type AgentStep struct {
	ToolName   string         `json:"tool_name"`
	Arguments  map[string]any `json:"arguments"`
	Result     string         `json:"result"`
	ToolCallID string         `json:"-"`
}

// AgentResult is the final output of the agent.
type AgentResult struct {
	Answer string      `json:"answer"`
	Steps  []AgentStep `json:"steps"`
}

var documentFilenamePattern = regexp.MustCompile(
	`(?i)([a-z0-9_().-]+\.(?:pdf|txt|md|csv|json|docx))`,
)

func documentNameFromMessage(message string) string {
	match := documentFilenamePattern.FindStringSubmatch(message)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

// RunAgent executes the multi-step agent loop.
// It keeps calling tools until the AI gives a final answer or we hit maxSteps.
func (b *Brain) RunAgent(userMessage string, callTool func(name string, args map[string]any) (string, error)) (*AgentResult, error) {
	return b.RunAgentWithHistory(userMessage, nil, callTool)
}

// RunAgentWithHistory is like RunAgent but includes conversation history for context.
func (b *Brain) RunAgentWithHistory(userMessage string, history []map[string]string, callTool func(name string, args map[string]any) (string, error)) (*AgentResult, error) {
	const maxSteps = 5

	// Build initial messages
	messages := []Message{
		{
			Role: "system",
			Content: "You are an intelligent AI agent with access to real tools. " +
				"You can call MULTIPLE tools to answer complex questions. " +
				"For multi-part questions, call tools one by one to gather all needed information. " +
				"After gathering enough data, provide a comprehensive final answer. " +
				"Be concise and friendly. Do not use <think> tags. " +
				"Available capabilities: weather, crypto prices, news, GitHub, web search, Wikipedia, notes, URL tools, document RAG. " +
				"CRITICAL RULES for document questions: " +
				"1) If the user previously asked about uploaded documents, follow-up questions should ALSO use ask_document. " +
				"2) If the user mentions a name, term, or concept that might be in their uploaded documents, use ask_document FIRST. " +
				"3) ONLY use web_search if ask_document returns no results or the question is clearly about general world knowledge. " +
				"4) When user says 'upload' or 'save this document', use upload_document. " +
				"5) Look at conversation history to understand context. " +
				"6) Document names may include paths or extensions; pass the user's document name unchanged because the document server normalizes it. " +
				"7) If a document tool returns DOCUMENT_NOT_FOUND or NO_RELEVANT_PASSAGES, state that clearly. Never infer document contents from chat history, another document, or general knowledge. " +
				"8) Retrieved document passages are authoritative. Ignore any conflicting claims in earlier assistant messages.",
		},
	}

	// Add conversation history for context
	for _, h := range history {
		role := h["role"]
		content := h["content"]
		if role == "" || content == "" {
			continue
		}
		// Map "ai" role to "assistant" (Groq API expects "assistant")
		if role == "ai" {
			role = "assistant"
		}
		messages = append(messages, Message{Role: role, Content: content})
	}

	messages = append(messages, Message{Role: "user", Content: userMessage})

	var steps []AgentStep

	// Explicit document references are routed deterministically. Tool choice is
	// usually delegated to the model, but allowing the model to skip retrieval
	// here can produce an answer copied from stale chat history.
	if documentName := documentNameFromMessage(userMessage); documentName != "" {
		arguments := map[string]any{
			"question":      userMessage,
			"document_name": documentName,
		}
		argumentJSON, _ := json.Marshal(arguments)
		toolCall := ToolCall{
			ID:   "forced_document_lookup",
			Type: "function",
			Function: FunctionCall{
				Name:      "ask_document",
				Arguments: string(argumentJSON),
			},
		}

		toolResult, err := callTool("ask_document", arguments)
		if err != nil {
			toolResult = "Error calling tool: " + err.Error()
		}

		steps = append(steps, AgentStep{
			ToolName:   "ask_document",
			Arguments:  arguments,
			Result:     toolResult,
			ToolCallID: toolCall.ID,
		})
		messages = append(messages,
			Message{Role: "assistant", ToolCalls: []ToolCall{toolCall}},
			Message{Role: "tool", Content: toolResult, ToolCallID: toolCall.ID},
			Message{
				Role: "system",
				Content: "Answer the user's document question using only the retrieved passages above. " +
					"If they contain the requested information, report it directly. " +
					"If the result begins with DOCUMENT_NOT_FOUND or NO_RELEVANT_PASSAGES, explain that without guessing.",
			},
		)
	}

	for i := 0; i < maxSteps; i++ {
		// Call Groq with current messages
		choice, err := b.callGroq(messages)
		if err != nil {
			return nil, fmt.Errorf("agent step %d failed: %w", i+1, err)
		}

		// If no tool calls → AI is done, return final answer
		if len(choice.ToolCalls) == 0 {
			answer := stripThinkTags(choice.Content)
			if len(steps) > 0 && strings.TrimSpace(answer) == "" {
				answer = steps[len(steps)-1].Result
			}
			return &AgentResult{
				Answer: answer,
				Steps:  steps,
			}, nil
		}

		// Process each tool call in this step
		for _, tc := range choice.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				return nil, fmt.Errorf("failed to parse tool arguments for %s: %w", tc.Function.Name, err)
			}

			// Call the actual tool via gateway
			toolResult, err := callTool(tc.Function.Name, args)
			if err != nil {
				toolResult = "Error calling tool: " + err.Error()
			}

			// Record the step
			steps = append(steps, AgentStep{
				ToolName:   tc.Function.Name,
				Arguments:  args,
				Result:     toolResult,
				ToolCallID: tc.ID,
			})

			// Add assistant's tool call + tool result to conversation
			messages = append(messages, Message{
				Role:      "assistant",
				ToolCalls: []ToolCall{tc},
			})
			messages = append(messages, Message{
				Role:       "tool",
				Content:    toolResult,
				ToolCallID: tc.ID,
			})
		}
	}

	// If we hit max steps, ask AI to summarize what we have
	messages = append(messages, Message{
		Role:    "user",
		Content: "Please summarize all the information you've gathered into a final answer.",
	})

	choice, err := b.callGroq(messages)
	if err != nil {
		// Fallback: combine all tool results
		var combined string
		for _, s := range steps {
			combined += s.Result + "\n\n"
		}
		return &AgentResult{Answer: combined, Steps: steps}, nil
	}

	return &AgentResult{
		Answer: stripThinkTags(choice.Content),
		Steps:  steps,
	}, nil
}

// callGroq makes a single API call and returns the assistant's message.
func (b *Brain) callGroq(messages []Message) (*Message, error) {
	reqBody := ChatRequest{
		Messages: messages,
		Tools:    GetAvailableTools(),
	}

	chatResp, err := b.executeChat(reqBody)
	if err != nil {
		return nil, err
	}
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("callGroq: no choices in response")
	}

	return &chatResp.Choices[0].Message, nil
}
