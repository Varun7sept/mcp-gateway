package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
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
	mu           sync.RWMutex      // protects Status, Result, Error
	Status       TaskStatus        `json:"-"`
	Result       string            `json:"-"`
	Error        string            `json:"-"`
	RetryCount   int               `json:"-"`
}

type Plan struct {
	Goal          string           `json:"goal"`
	Tasks         []*TaskDefinition `json:"tasks"`
}

// Thread-safe accessors for TaskDefinition status/result fields.
func (t *TaskDefinition) GetStatus() TaskStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Status
}

func (t *TaskDefinition) SetStatus(s TaskStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = s
}

func (t *TaskDefinition) SetDone(result string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = TaskDone
	t.Result = result
}

func (t *TaskDefinition) SetFailed(errMsg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Status = TaskFailed
	t.Error = errMsg
}

func (t *TaskDefinition) GetResult() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.Result
}

func (b *Brain) DecomposeGoal(goal string, history []Message) (*Plan, error) {
	messages := []Message{
		{
			Role: "system",
			Content: "You are a task planning AI. Your job is to decompose the user's goal into the minimum number of tool calls needed.\n\n" +
				"PLANNING RULES:\n" +
				"1. Return an EMPTY tasks array [] for: greetings, simple math, or ANY follow-up question where the answer is already present in the conversation history. IMPORTANT: If the previous assistant message already contains the relevant information (e.g. user asked about a person and now asks a follow-up about that same person's dates/stats/facts), return [] — do NOT call a tool again.\n" +
				"2. Independent tasks (no shared data) CAN run in parallel — leave depends_on empty [].\n" +
				"3. Tasks that need a prior task's output MUST set depends_on and reference the result with ${result:N} (N = task ID). Never use other placeholder formats.\n" +
				"4. Each task calls EXACTLY ONE tool.\n" +
				"5. For multi-location queries (e.g. 'weather in London and Paris'), make one task per location.\n" +
				"6. For 'search and summarize' — ONE search task only. Summarization is automatic.\n" +
				"7. NEVER use both search_news AND web_search for the same intent. Pick exactly one:\n" +
				"   • Breaking news / current events / sports / politics → search_news\n" +
				"   • Factual, historical, encyclopedic topics → wikipedia_summary\n" +
				"   • Niche, real-time, or non-Wikipedia topics → web_search\n" +
				"8. MAXIMUM 6 TASKS. If more are needed, pick the 6 most important.\n" +
				"9. Do NOT create a task just to repeat information already in the conversation history.\n\n" +
				"Available tools: get_weather, get_forecast, get_user, list_repos, get_repo, add_note, list_notes, " +
				"search_notes, get_crypto_price, get_top_cryptos, get_top_news, search_news, " +
				"shorten_url, generate_qr, expand_url, web_search, wikipedia_summary, " +
				"upload_document, ask_document, list_documents\n\n" +
				"OUTPUT: Respond with ONLY valid JSON — no markdown, no explanation:\n" +
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
	if len(filtered) > 6 {
		filtered = filtered[:6]
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
