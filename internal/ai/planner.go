package ai

import (
	"encoding/json"
	"fmt"
	"strings"
)

type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskFailed     TaskStatus = "failed"
)

type TaskDefinition struct {
	ID           int               `json:"id"`
	Description  string            `json:"description"`
	Tool         string            `json:"tool"`
	Arguments    map[string]any    `json:"arguments"`
	Dependencies []int             `json:"depends_on"`
	Status       TaskStatus        `json:"-"`
	Result       string            `json:"-"`
	Error        string            `json:"-"`
	RetryCount   int               `json:"-"`
}

type Plan struct {
	Goal          string           `json:"goal"`
	Tasks         []*TaskDefinition `json:"tasks"`
}

func (b *Brain) DecomposeGoal(goal string, history []Message) (*Plan, error) {
	messages := []Message{
		{
			Role: "system",
			Content: "You are a task planning AI. Break down the user's goal into a plan of tool calls. " +
				"Analyze what tools are needed and in what order. " +
				"RULES:\n" +
				"1. Identify independent tasks — they can run in parallel\n" +
				"2. Use 'depends_on' to express ordering (tasks that depend on others must wait)\n" +
				"3. Each task must specify exactly ONE tool to call\n" +
				"4. If no tools are needed (simple Q&A), return an empty tasks array\n" +
				"5. For multi-part questions like 'weather in London and Paris', create separate tasks for each\n" +
				"6. For 'search and summarize' requests, create ONE search task. The summarization will be done automatically.\n" +
				"7. CHOOSE ONLY ONE search-style tool per search intent. Example: for 'AI news' use EITHER search_news OR web_search, not both. Pick just one.\n" +
				"8. MAXIMUM 3 TASKS TOTAL. Be focused and efficient.\n" +
				"9. NEVER create tasks for both search_news AND web_search with similar queries. Pick exactly one.\n" +
				"Available tools: get_weather, get_forecast, get_user, list_repos, get_repo, add_note, list_notes, " +
				"search_notes, get_crypto_price, get_top_cryptos, get_top_news, search_news, " +
				"shorten_url, generate_qr, expand_url, web_search, wikipedia_summary, " +
				"upload_document, ask_document, list_documents\n\n" +
				"Respond with ONLY valid JSON in this exact format. No markdown, no explanation:\n" +
				`{"tasks":[{"id":1,"description":"...","tool":"tool_name","arguments":{"key":"value"},"depends_on":[]}]}`,
		},
	}
	for _, h := range history {
		messages = append(messages, h)
	}
	messages = append(messages, Message{Role: "user", Content: goal})

	reqBody := ChatRequest{
		Messages: messages,
		Model:    b.models[0],
	}
	chatResp, err := b.executeChat(reqBody)
	if err != nil {
		return nil, fmt.Errorf("planning failed: %w", err)
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var plan struct {
		Tasks []struct {
			ID           int            `json:"id"`
			Description  string         `json:"description"`
			Tool         string         `json:"tool"`
			Arguments    map[string]any `json:"arguments"`
			Dependencies []int          `json:"depends_on"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(content), &plan); err != nil {
		return nil, fmt.Errorf("failed to parse plan JSON: %w\nRaw: %s", err, content)
	}

	result := &Plan{Goal: goal}
	for _, t := range plan.Tasks {
		if t.Tool == "" {
			continue
		}
		result.Tasks = append(result.Tasks, &TaskDefinition{
			ID:           t.ID,
			Description:  t.Description,
			Tool:         t.Tool,
			Arguments:    t.Arguments,
			Dependencies: t.Dependencies,
			Status:       TaskPending,
		})
	}
	return result, nil
}
