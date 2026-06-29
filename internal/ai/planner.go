package ai

import (
	"encoding/json"
	"fmt"
	"log"
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
				"5. If a task depends on another task's result, reference it using ${result:N} syntax where N is the task ID. Do NOT use {placeholder} or other formats.\n" +
				"6. For multi-part questions like 'weather in London and Paris', create separate tasks for each\n" +
				"7. For 'search and summarize' requests, create ONE search task. The summarization will be done automatically.\n" +
				"8. CHOOSE ONLY ONE search-style tool per search intent. Example: for 'AI news' use EITHER search_news OR web_search, not both. Pick just one.\n" +
				"9. MAXIMUM 3 TASKS TOTAL. Be focused and efficient.\n" +
				"10. NEVER create tasks for both search_news AND web_search with similar queries. Pick exactly one.\n" +
				"11. For queries about people, places, historical events, or factual topics — prefer wikipedia_summary over web_search. " +
				"wikipedia_summary gives structured, reliable information. Use web_search only for news, current events, or niche topics unlikely to be on Wikipedia.\n" +
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

	log.Printf("[PLANNER] Raw response: %s", content)
	log.Printf("[PLANNER] Tasks before filter: %d", len(plan.Tasks))
	for _, t := range plan.Tasks {
		argsJSON, _ := json.Marshal(t.Arguments)
		log.Printf("[PLANNER]   Task %d: tool=%s args=%s", t.ID, t.Tool, string(argsJSON))
	}

	searchTools := map[string]bool{"search_news": true, "web_search": true, "wikipedia_summary": true}
	coinAliases := map[string]string{"BTC": "bitcoin", "ETH": "ethereum", "SOL": "solana", "XRP": "xrp", "DOGE": "dogecoin", "ADA": "cardano", "DOT": "polkadot", "LINK": "chainlink", "AVAX": "avalanche-2", "MATIC": "matic-network"}
	requiredArgs := map[string][]string{"add_note": {"title", "content"}, "get_crypto_price": {"coin"}, "get_weather": {"city"}, "get_forecast": {"city"}, "get_repo": {"owner", "repo"}, "get_user": {"username"}, "list_repos": {"username"}, "search_news": {"query"}, "web_search": {"query"}, "wikipedia_summary": {"topic"}, "shorten_url": {"url"}, "upload_document": {"file"}}
	seenSearch := map[string]bool{}
	var filtered []struct {
		ID           int            `json:"id"`
		Description  string         `json:"description"`
		Tool         string         `json:"tool"`
		Arguments    map[string]any `json:"arguments"`
		Dependencies []int          `json:"depends_on"`
	}
	for _, t := range plan.Tasks {
		if t.Tool == "" {
			continue
		}

		if t.Arguments == nil {
			t.Arguments = map[string]any{}
		}

		// Normalize common argument key aliases (model often uses wrong keys)
		argAliases := map[string]map[string]string{
			"get_crypto_price":  {"symbol": "coin", "coin_name": "coin", "crypto": "coin"},
			"add_note":          {"note": "content", "text": "content", "body": "content", "name": "title"},
			"wikipedia_summary": {"query": "topic", "search": "topic"},
			"get_weather":       {"location": "city", "city_name": "city"},
			"get_forecast":      {"location": "city", "city_name": "city"},
		}
		if aliases, ok := argAliases[t.Tool]; ok {
			for wrongKey, rightKey := range aliases {
				if v, exists := t.Arguments[wrongKey]; exists {
					if _, already := t.Arguments[rightKey]; !already {
						t.Arguments[rightKey] = v
					}
					delete(t.Arguments, wrongKey)
				}
			}
		}

		// Normalize coin ticker symbols to full names
		if t.Tool == "get_crypto_price" {
			if v, ok := t.Arguments["coin"].(string); ok {
				if normalized, found := coinAliases[strings.ToUpper(v)]; found {
					t.Arguments["coin"] = normalized
				}
			}
		}

		// Auto-populate missing required args where possible
		if t.Tool == "add_note" {
			if _, has := t.Arguments["title"]; !has {
				t.Arguments["title"] = t.Description
			}
		}

		// Validate required args are present and non-empty
		if reqs, ok := requiredArgs[t.Tool]; ok {
			missing := false
			for _, arg := range reqs {
				v, has := t.Arguments[arg]
				if !has {
					missing = true
					break
				}
				if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
					missing = true
					break
				}
			}
			if missing {
				continue
			}
		}

		if searchTools[t.Tool] {
			var searchQuery string
			if q, ok := t.Arguments["query"].(string); ok && q != "" {
				searchQuery = q
			} else if q, ok := t.Arguments["topic"].(string); ok && q != "" {
				searchQuery = q
			}
			if searchQuery == "" {
				continue
			}
			key := t.Tool + ":" + searchQuery
			if seenSearch[key] {
				continue
			}
			seenSearch[key] = true
		}
		filtered = append(filtered, t)
	}
	if len(filtered) > 3 {
		filtered = filtered[:3]
	}

	log.Printf("[PLANNER] Tasks after filter: %d", len(filtered))

	result := &Plan{Goal: goal}
	for _, t := range filtered {
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
