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
			if task.Status == TaskFailed {
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
					if t.Status == TaskFailed {
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
						if retryTask.Status == TaskDone {
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
			if t.Status == TaskDone {
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
	result, err := b.RunAgentWithHistory(userMessage, nil, callTool)
	if err != nil {
		return nil, err
	}
	return &OrchestratorResult{
		Answer: result.Answer,
		Steps:  result.Steps,
	}, nil
}

func (b *Brain) handleNoTools(userMessage string, messages []Message, callTool func(name string, args map[string]any) (string, error), start time.Time) (*OrchestratorResult, error) {
	choice, err := b.callGroq(messages)
	if err != nil {
		return nil, err
	}
	answer := stripThinkTags(choice.Content)
	if strings.TrimSpace(answer) == "" {
		answer = "I understand your question but I don't have the tools needed to answer it fully. Could you rephrase or ask something I can help with using my available tools?"
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
		if task.Status == TaskDone {
			step.Result = task.Result
			results = append(results, fmt.Sprintf("Tool '%s' result: %s", task.Tool, task.Result))
		} else {
			step.Result = task.Error
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

	summaryResp, err := b.callGroq(summaryMessages)
	if err == nil && summaryResp.Content != "" {
		finalAnswer = stripThinkTags(summaryResp.Content)
	}

	return finalAnswer, steps
}
