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
			Content: "You are an intelligent AI agent with access to real tools. " +
				"You can call MULTIPLE tools to answer complex questions. " +
				"For multi-part questions, call tools one by one to gather all needed information. " +
				"After gathering enough data, provide a comprehensive final answer. " +
				"Be concise and friendly. Do not use <think> tags. " +
				"Available capabilities: weather, crypto prices, news, GitHub, web search, Wikipedia, notes, URL tools, document RAG.",
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
			Content: "You are given tool results for the user's question. " +
				"If the question asked you to search and summarize, extract key information from the search results and present a clear summary. " +
				"If search results contain article titles, snippets, and URLs, use them to form a useful answer. " +
				"Format information cleanly with bullet points where appropriate. " +
				"If a search returned no results, say so and suggest an alternative. " +
				"Be conversational and concise but informative.",
		},
		{Role: "user", Content: fmt.Sprintf("Question: %s\n\nResults:\n%s", userMessage, finalAnswer)},
	}

	summaryResp, err := b.callGroq(summaryMessages)
	if err == nil && summaryResp.Content != "" {
		finalAnswer = stripThinkTags(summaryResp.Content)
	}

	return finalAnswer, steps
}
