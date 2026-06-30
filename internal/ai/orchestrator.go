package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/approval"
)

type OrchestratorConfig struct {
	Memory         MemoryStore
	ApprovalStore  *approval.Store
	ApprovalUser   string
	// ApprovedTools lists tool names the user has already approved this request.
	// checkApprovals skips these so the user is never asked twice.
	ApprovedTools  []string
}

type OrchestratorResult struct {
	Answer     string           `json:"answer"`
	Steps      []AgentStep      `json:"steps"`
	Plan       *Plan            `json:"plan,omitempty"`
	Report     *ExecutionReport `json:"report,omitempty"`
	ApprovalID string           `json:"approval_id,omitempty"`
	NeedsApproval bool          `json:"needs_approval,omitempty"`
}

func (b *Brain) ProcessWithOrchestrator(
	userMessage string,
	history []map[string]string,
	callTool func(name string, args map[string]any) (string, error),
	cfg *OrchestratorConfig,
) (*OrchestratorResult, error) {
	start := time.Now()

	messages := b.buildAgentMessages(userMessage, history)

	relevantMemories := ""
	if cfg != nil && cfg.Memory != nil {
		relevantMemories = b.RetrieveRelevantMemories(userMessage)
	}

	if relevantMemories != "" {
		memoryMsg := Message{
			Role:    "system",
			Content: relevantMemories,
		}
		messages = append(messages, memoryMsg)
	}

	if cfg != nil && cfg.ApprovalStore != nil {
		pendingApprovals := cfg.ApprovalStore.GetPending(cfg.ApprovalUser)
		if len(pendingApprovals) > 0 {
			var pendingInfo []string
			for _, pa := range pendingApprovals {
				argsJSON, _ := json.Marshal(pa.Arguments)
				pendingInfo = append(pendingInfo, fmt.Sprintf("- %s (tool: %s, args: %s) [ID: %s]",
					pa.Description, pa.Tool, string(argsJSON), pa.ID))
			}
			messages = append(messages, Message{
				Role:    "system",
				Content: fmt.Sprintf("You have pending approvals:\n%s\nContinue waiting for user approval.", strings.Join(pendingInfo, "\n")),
			})
		}
	}

	plan, err := b.DecomposeGoal(userMessage, messages)
	if err != nil {
		return b.fallbackToDirect(userMessage, messages, callTool, start)
	}

	if len(plan.Tasks) == 0 {
		return b.handleNoTools(userMessage, messages, callTool, start)
	}

	tasksWithApproval, err := b.checkApprovals(plan, cfg)
	if err != nil {
		return nil, err
	}
	if tasksWithApproval != nil {
		return tasksWithApproval, nil
	}

	report := b.ExecutePlan(plan, callTool)

	// Retry loop: if any tasks failed, give the AI one chance to re-plan
	// with knowledge of what failed and why, so it can try a different tool.
	if report != nil && !report.Complete {
		var failedDescriptions []string
		for _, task := range plan.Tasks {
			if task.GetStatus() == TaskFailed {
				failedDescriptions = append(failedDescriptions,
					fmt.Sprintf("tool '%s' failed: %s", task.Tool, task.Error))
			}
		}
		if len(failedDescriptions) > 0 {
			retryHint := fmt.Sprintf(
				"%s\n\nThe following tools failed: %s\n\nPlease replan using DIFFERENT tools to accomplish the same goal. Do not retry the same failed tools.",
				userMessage, strings.Join(failedDescriptions, "; "),
			)
			retryPlan, retryErr := b.DecomposeGoal(retryHint, messages)
			if retryErr == nil && len(retryPlan.Tasks) > 0 {
				// Only use retry plan if it actually uses different tools
				usesNewTools := false
				failedTools := make(map[string]bool)
				for _, t := range plan.Tasks {
					if t.GetStatus() == TaskFailed {
						failedTools[t.Tool] = true
					}
				}
				for _, t := range retryPlan.Tasks {
					if !failedTools[t.Tool] {
						usesNewTools = true
						break
					}
				}
				if usesNewTools {
					retryReport := b.ExecutePlan(retryPlan, callTool)
					// Merge successful retry results into original plan
					for _, retryTask := range retryPlan.Tasks {
						if retryTask.GetStatus() == TaskDone {
							plan.Tasks = append(plan.Tasks, retryTask)
						}
					}
					if retryReport != nil {
						report = retryReport
					}
				}
			}
		}
	}

	finalAnswer, steps := b.compileResults(plan, report, userMessage, start)

	if cfg != nil && cfg.Memory != nil {
		var toolsUsed []string
		for _, t := range plan.Tasks {
			if t.GetStatus() == TaskDone {
				toolsUsed = append(toolsUsed, t.Tool)
			}
		}
		cfg.Memory.Save(MemoryEntry{
			Query:     userMessage,
			Answer:    finalAnswer,
			ToolsUsed: toolsUsed,
			Timestamp: time.Now(),
		})
	}

	return &OrchestratorResult{
		Answer: finalAnswer,
		Steps:  steps,
		Plan:   plan,
		Report: report,
	}, nil
}

func (b *Brain) buildAgentMessages(userMessage string, history []map[string]string) []Message {
	messages := []Message{
		{
			Role: "system",
			Content: "You are an intelligent AI assistant with access to real-time tools.\n\n" +
				"Capabilities: weather forecasts, GitHub data, crypto prices, news, web search, Wikipedia, notes, URL shortener/QR, document Q&A.\n\n" +
				"BEHAVIOUR:\n" +
				"• Use conversation history to answer follow-up questions without re-calling tools unnecessarily.\n" +
				"• When you do need a tool, choose the most specific one (e.g. wikipedia_summary for facts, search_news for current events).\n" +
				"• Never output raw tool JSON or <think> tags — always respond in natural language.\n" +
				"• If the user's question can be answered from prior context, answer directly without tools.",
		},
	}
	for _, h := range history {
		role := h["role"]
		content := h["content"]
		if role == "" || content == "" {
			continue
		}
		if role == "ai" {
			role = "assistant"
		}
		messages = append(messages, Message{Role: role, Content: content})
	}
	messages = append(messages, Message{Role: "user", Content: userMessage})
	return messages
}

func (b *Brain) checkApprovals(plan *Plan, cfg *OrchestratorConfig) (*OrchestratorResult, error) {
	if cfg == nil || cfg.ApprovalStore == nil {
		return nil, nil
	}

	// Build a set of already-approved tool names so we never ask twice.
	approved := make(map[string]bool, len(cfg.ApprovedTools))
	for _, t := range cfg.ApprovedTools {
		approved[t] = true
	}

	for _, task := range plan.Tasks {
		if approved[task.Tool] {
			continue // user already approved this tool for this request
		}
		if _, risky := cfg.ApprovalStore.IsRiskyTool(task.Tool); risky {
			req := cfg.ApprovalStore.CreateRequest(
				cfg.ApprovalUser,
				task.Description,
				task.Tool,
				task.Arguments,
			)
			return &OrchestratorResult{
				NeedsApproval: true,
				ApprovalID:    req.ID,
				Plan:          plan,
			}, nil
		}
	}
	return nil, nil
}

func (b *Brain) fallbackToDirect(userMessage string, messages []Message, callTool func(name string, args map[string]any) (string, error), start time.Time) (*OrchestratorResult, error) {
	// Convert []Message → []map[string]string so RunAgentWithHistory gets full context.
	// This ensures pronoun resolution works — "he" → correct person from history.
	var history []map[string]string
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			history = append(history, map[string]string{
				"role":    m.Role,
				"content": m.Content,
			})
		}
	}
	result, err := b.RunAgentWithHistory(userMessage, history, callTool)
	if err != nil {
		return nil, err
	}
	return &OrchestratorResult{
		Answer: result.Answer,
		Steps:  result.Steps,
	}, nil
}

func (b *Brain) handleNoTools(userMessage string, messages []Message, callTool func(name string, args map[string]any) (string, error), start time.Time) (*OrchestratorResult, error) {
	// Count how many user/assistant turns exist (excluding the current user message).
	// If this is the very first message there's no history to draw from — go straight
	// to the tool agent so it can call a tool rather than saying "I don't know".
	historyCount := 0
	for _, m := range messages {
		if m.Role == "user" || m.Role == "assistant" {
			historyCount++
		}
	}
	// historyCount includes the current user message, so <=1 means no prior turns.
	if historyCount <= 1 {
		return b.fallbackToDirect(userMessage, messages, callTool, start)
	}

	// There IS prior context — ask the model to answer from it, but signal
	// NEED_TOOL if a specific fact isn't in the history.
	contextMessages := append([]Message{}, messages...)
	contextMessages[0] = Message{
		Role: "system",
		Content: "You are a helpful AI assistant. Answer the user's question using the conversation history above.\n\n" +
			"RULES:\n" +
			"1. Always resolve pronouns from context — 'he/she/they/it/his/her' refer to the subject of the prior conversation. Never ask 'who do you mean?'.\n" +
			"2. If the answer is clearly in the history (e.g. a date, name, fact was mentioned), answer directly and concisely.\n" +
			"3. If the question asks for a specific fact (date, stat, number, event) that is genuinely NOT anywhere in the history, respond with exactly: NEED_TOOL\n" +
			"4. Never make up facts. Never say 'I don't have tools' or ask for clarification. Either answer from history or respond NEED_TOOL.",
	}

	choice, err := b.callGroq(contextMessages)
	if err != nil {
		return b.fallbackToDirect(userMessage, messages, callTool, start)
	}
	answer := stripThinkTags(choice.Content)

	// If model signals it needs a tool, or answer is blank, fall through to tool agent.
	if strings.TrimSpace(answer) == "" || strings.Contains(strings.ToUpper(answer), "NEED_TOOL") {
		return b.fallbackToDirect(userMessage, messages, callTool, start)
	}

	return &OrchestratorResult{
		Answer: answer,
	}, nil
}

func (b *Brain) compileResults(plan *Plan, report *ExecutionReport, userMessage string, start time.Time) (string, []AgentStep) {
	var steps []AgentStep
	var results []string
	var failedTasks []string

	for _, task := range plan.Tasks {
		step := AgentStep{
			ToolName:  task.Tool,
			Arguments: task.Arguments,
		}
		if task.GetStatus() == TaskDone {
			step.Result = task.GetResult()
			results = append(results, fmt.Sprintf("Tool '%s' result: %s", task.Tool, task.GetResult()))
		} else {
			step.Result = task.Error  // Error field not written concurrently after done
			failedTasks = append(failedTasks, fmt.Sprintf("'%s' (error: %s)", task.Description, task.Error))
		}
		steps = append(steps, step)
	}

	var finalAnswer string
	if len(failedTasks) > 0 {
		finalAnswer = fmt.Sprintf("Completed %d tasks. The following tasks failed: %s.\n\nResults from successful tasks:\n%s",
			len(plan.Tasks), strings.Join(failedTasks, "; "), strings.Join(results, "\n\n"))
	} else {
		finalAnswer = strings.Join(results, "\n\n")
	}

	summaryMessages := []Message{
		{
			Role: "system",
			Content: "You are an AI assistant that synthesizes tool results into a helpful, natural answer.\n\n" +
				"RULES:\n" +
				"1. NEVER output raw tool result text like 'Tool X result: ...' — always synthesize into a proper answer.\n" +
				"2. Answer the user's question directly using the data from the tool results.\n" +
				"3. If results include lists (news articles, repos, etc.), present them as clean bullet points with the most important details.\n" +
				"4. If multiple tools ran, combine their results into ONE coherent answer — don't repeat the question back.\n" +
				"5. If a tool returned an error or empty result, acknowledge it briefly and move on.\n" +
				"6. Be concise but complete. Aim for 2–6 sentences or a short bulleted list.\n" +
				"7. Do not include <think> tags or meta-commentary about what tools were used.",
		},
		{Role: "user", Content: fmt.Sprintf("User asked: %s\n\nTool results:\n%s\n\nNow write a helpful answer:", userMessage, finalAnswer)},
	}

	// Retry synthesis up to 2 times — Groq can occasionally return empty on first call.
	for attempt := 0; attempt < 2; attempt++ {
		summaryResp, err := b.callGroq(summaryMessages)
		if err == nil && strings.TrimSpace(summaryResp.Content) != "" {
			finalAnswer = stripThinkTags(summaryResp.Content)
			break
		}
	}

	// If synthesis still failed/empty, strip the "Tool 'X' result: " prefixes and
	// return clean raw data rather than the labelled dump.
	if strings.TrimSpace(finalAnswer) == "" || strings.HasPrefix(finalAnswer, "Tool '") {
		var cleaned []string
		for _, r := range results {
			if idx := strings.Index(r, " result: "); idx != -1 {
				cleaned = append(cleaned, strings.TrimSpace(r[idx+9:]))
			} else {
				cleaned = append(cleaned, r)
			}
		}
		if len(cleaned) > 0 {
			finalAnswer = strings.Join(cleaned, "\n\n")
		}
	}

	return finalAnswer, steps
}
