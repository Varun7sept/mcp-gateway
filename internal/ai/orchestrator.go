package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/approval"
	"github.com/varunbanda/mcp-gateway/internal/memory"
)

type OrchestratorConfig struct {
	Memory        memory.MemoryStore
	ApprovalStore *approval.Store
	ApprovalUser  string
	UserID        string
	SessionID     string
	// ApprovedTools lists tool names the user has already approved this request.
	// checkApprovals skips these so the user is never asked twice.
	ApprovedTools []string
}

type OrchestratorResult struct {
	Answer        string           `json:"answer"`
	Steps         []AgentStep      `json:"steps"`
	Plan          *Plan            `json:"plan,omitempty"`
	Report        *ExecutionReport `json:"report,omitempty"`
	ApprovalID    string           `json:"approval_id,omitempty"`
	NeedsApproval bool             `json:"needs_approval,omitempty"`
}

func (b *Brain) ProcessWithOrchestrator(
	userMessage string,
	history []map[string]string,
	callTool func(name string, args map[string]any) (string, error),
	cfg *OrchestratorConfig,
) (*OrchestratorResult, error) {
	start := time.Now()

	// Build planner-only messages: planner system prompt + conversation history ONLY.
	// Do NOT include buildAgentMessages()'s tool-enabled system prompt, which would
	// create conflicting instructions for the planner.
	plannerMessages := b.buildPlannerMessages(history)

	relevantMemories := ""
	if cfg != nil && cfg.Memory != nil {
		relevantMemories = b.RetrieveRelevantMemories(userMessage, cfg.UserID)
	}

	if relevantMemories != "" {
		memoryMsg := Message{
			Role:    "system",
			Content: relevantMemories,
		}
		plannerMessages = append(plannerMessages, memoryMsg)
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
			plannerMessages = append(plannerMessages, Message{
				Role:    "system",
				Content: fmt.Sprintf("You have pending approvals:\n%s\nContinue waiting for user approval.", strings.Join(pendingInfo, "\n")),
			})
		}
	}

	plan, err := b.DecomposeGoal(userMessage, plannerMessages)
	if err != nil {
		log.Printf("[ROUTING] planner FAILED -> fallbackToDirect: %v", err)
		return b.fallbackToDirect(userMessage, b.buildAgentMessages(userMessage, history), callTool, start)
	}
	log.Printf("[ROUTING] planner OK -> %d tasks", len(plan.Tasks))

	if len(plan.Tasks) == 0 {
		log.Printf("[ROUTING] zero tasks -> generateDirectAnswer (ChatRequest Tools=nil)")
		return b.generateDirectAnswer(userMessage, history, start)
	}
	log.Printf("[ROUTING] %d tasks -> ExecutePlan (tools WILL run)", len(plan.Tasks))

	tasksWithApproval, err := b.checkApprovals(plan, cfg)
	if err != nil {
		return nil, err
	}
	if tasksWithApproval != nil {
		return tasksWithApproval, nil
	}

	report := b.ExecutePlan(plan, callTool)

	// Validate planner output: reject tool plans that obviously contradict
	// the user's intent category. This is a safety guard — the planner's prompt
	// should correctly distinguish programming questions (→ zero tasks) from
	// current-info questions (→ tools), but we add a deterministic check so an
	// occasional LLM slip doesn't execute irrelevant tools.
	if plan.Tasks != nil && len(plan.Tasks) > 0 {
		plan = validatePlannerPlan(plan, userMessage)
	}

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
			retryPlan, retryErr := b.DecomposeGoal(retryHint, plannerMessages)
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
		cfg.Memory.Save(memory.MemoryEntry{
			MemoryID:        memory.GenerateMemoryID(userMessage, time.Now()),
			UserID:          cfg.UserID,
			SessionID:       cfg.SessionID,
			Query:           userMessage,
			Answer:          finalAnswer,
			Summary:         "",
			ImportanceScore: 0,
			ToolsUsed:       toolsUsed,
			CreatedAt:       time.Now(),
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

// buildPlannerMessages returns ONLY the planner system prompt + actual conversation
// history messages. It does NOT include the agent's tool-enabled system prompt
// from buildAgentMessages(), ensuring the planner receives isolated, unambiguous
// instructions about when to return zero tasks vs. when to use tools.
func (b *Brain) buildPlannerMessages(history []map[string]string) []Message {
	messages := []Message{
		{
			Role:    "system",
			Content: plannerSystemPrompt(),
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
	messages = append(messages, Message{Role: "user", Content: ""})
	return messages
}

func (b *Brain) fallbackToDirect(userMessage string, messages []Message, callTool func(name string, args map[string]any) (string, error), start time.Time) (*OrchestratorResult, error) {
	log.Printf("[ROUTING] fallbackToDirect: entering tool-enabled agent (RunAgentWithHistory)")
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

// directSystemPrompt returns the system instructions for the no-tool path.
func directSystemPrompt() string {
	return "You are a helpful AI assistant. Answer the user's question directly from your knowledge and the conversation history.\n\n" +
		"RULES:\n" +
		"1. No tools are available to you — do not mention, pretend, or attempt to call any tool.\n" +
		"2. Use conversation history when relevant, resolving pronouns from context.\n" +
		"3. Provide code examples, explanations, and definitions when appropriate.\n" +
		"4. Be concise, factual, and conversational.\n" +
		"5. Do not output  thinking tags."
}

// generateDirectAnswer handles the successful planner zero-task path. When the
// planner returns tasks = [], the decision is final: no external tool is
// required. The answer comes from a plain LLM completion that carries NO tools,
// so the model is never offered (or able to attempt) web_search or any other
// tool. This path must NOT enter the tool-enabled agent (RunAgent,
// RunAgentWithHistory, callGroq) or any other second tool-selection logic.
//
// IMPORTANT: This function builds messages from scratch using ONLY the direct
// system prompt and actual conversation history. It does NOT reuse
// buildAgentMessages() output, which would carry the tool-enabled system prompt.
// The ChatRequest must have Tools == nil.
func (b *Brain) generateDirectAnswer(userMessage string, history []map[string]string, start time.Time) (*OrchestratorResult, error) {
	log.Printf("[ROUTING] generateDirectAnswer: sending ChatRequest with NO tools")
	// Build messages from scratch: direct system prompt + actual conversation history.
	// Do NOT include any system prompt that advertises tools (i.e. do not use
	// buildAgentMessages() output or messages[1:] which may contain it).
	directMessages := []Message{{Role: "system", Content: directSystemPrompt()}}

	// Add actual conversation history (user/assistant messages only)
	for _, h := range history {
		role := h["role"]
		content := h["content"]
		if role == "" || content == "" {
			continue
		}
		if role == "ai" {
			role = "assistant"
		}
		directMessages = append(directMessages, Message{Role: role, Content: content})
	}

	directMessages = append(directMessages, Message{Role: "user", Content: userMessage})

	var finalAnswer string
	// Retry once — Groq can occasionally return an empty response.
	for attempt := 0; attempt < 2; attempt++ {
		chatResp, err := b.chatCall(ChatRequest{Messages: directMessages})
		if err == nil && len(chatResp.Choices) > 0 && strings.TrimSpace(chatResp.Choices[0].Message.Content) != "" {
			finalAnswer = stripThinkTags(chatResp.Choices[0].Message.Content)
			break
		}
	}

	if strings.TrimSpace(finalAnswer) == "" {
		return nil, fmt.Errorf("direct answer generation failed: no response from LLM")
	}

	return &OrchestratorResult{Answer: finalAnswer}, nil
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
			step.Result = task.Error // Error field not written concurrently after done
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

// validatePlannerPlan checks for obvious contradictions between the planned
// tools and the user's intent category. This is a deterministic guard that
// catches common LLM slips without hardcoding individual questions.
//
// Rules:
//  1. Programming/coding/algorithm questions must result in zero tasks,
//     even if the planner erroneously suggests a tool.
//  2. General knowledge/definition questions should not trigger tools
//     unless the user explicitly requests current/external data.
//  3. If a tool is planned but the intent is clearly programming/algorithmic,
//     the plan is corrected to zero tasks.
func validatePlannerPlan(plan *Plan, userMessage string) *Plan {
	tasks := plan.Tasks
	userLower := strings.ToLower(userMessage)

	// Detect programming/coding/algorithm intent
	progKeywords := []string{"implement", "write", "code", "program", "function", "algorithm",
		"data structure", "leetcode", "two sum", "binary search", "sort", "linked list",
		"stack", "queue", "tree", "graph", "recursion", "mutex", "generics",
		"in go", "in c++", "in rust", "in python", "cpp", "go ", "rust ", "python "}

	// Detect general knowledge/definition intent
	knowKeywords := []string{"what is", "explain", "define", "meaning of", "vs ", "difference between"}

	isProg := func() bool {
		for _, kw := range progKeywords {
			if strings.Contains(userLower, kw) {
				return true
			}
		}
		// Check if the message contains typical code-style questions
		if strings.Contains(userLower, "c++") || strings.Contains(userLower, "go") ||
			strings.Contains(userLower, "rust") || strings.Contains(userLower, "python") {
			return true
		}
		return false
	}()

	isKnow := func() bool {
		for _, kw := range knowKeywords {
			if strings.Contains(userLower, kw) {
				return true
			}
		}
		return false
	}()

	// If programming intent and planner suggested tools → zero tasks
	if isProg && len(tasks) > 0 {
		// Check if any planned tool is completely unrelated to programming
		// (this is a simple check - the planner should already handle this,
		// but the guard catches slips)
		nonProgTools := []string{"get_weather", "get_crypto_price", "get_top_cryptos",
			"get_forecast", "search_news", "web_search", "wikipedia_summary"}
		hasNonProgTool := false
		for _, t := range tasks {
			for _, nt := range nonProgTools {
				if t.Tool == nt {
					hasNonProgTool = true
					break
				}
			}
			if hasNonProgTool {
				break
			}
		}
		if hasNonProgTool {
			log.Printf("[VALIDATION] Programming intent (%q) but tools %v → zero tasks", userMessage, tasks)
			return &Plan{Goal: plan.Goal}
		}
	}

	// If general knowledge intent and planner suggested non-knowledge tools
	if isKnow {
		// Could add more validation here if needed
	}

	return plan
}
