package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const maxRetries = 2

type ExecutionReport struct {
	Tasks    []*TaskDefinition `json:"tasks"`
	Complete bool              `json:"complete"`
}

func (b *Brain) ExecutePlan(plan *Plan, callTool func(name string, args map[string]any) (string, error)) *ExecutionReport {
	report := &ExecutionReport{Tasks: plan.Tasks}

	taskMap := make(map[int]*TaskDefinition)
	for _, t := range plan.Tasks {
		taskMap[t.ID] = t
	}

	groups := topologicalSort(plan.Tasks, taskMap)

	for _, group := range groups {
		var wg sync.WaitGroup
		errChan := make(chan *TaskDefinition, len(group))

		for _, task := range group {
			task.Status = TaskInProgress
			wg.Add(1)

			go func(t *TaskDefinition) {
				defer wg.Done()
				result, err := b.executeWithRetry(t, callTool)
				if err != nil {
					t.Status = TaskFailed
					t.Error = err.Error()
					errChan <- t
				} else {
					t.Status = TaskDone
					t.Result = result
				}
			}(task)
		}

		wg.Wait()
		close(errChan)

		for failedTask := range errChan {
			for _, t := range plan.Tasks {
				for _, dep := range t.Dependencies {
					if dep == failedTask.ID {
						t.Status = TaskFailed
						t.Error = fmt.Sprintf("dependency %d (%s) failed: %s", failedTask.ID, failedTask.Tool, failedTask.Error)
					}
				}
			}
		}
	}

	report.Complete = true
	for _, t := range plan.Tasks {
		if t.Status == TaskFailed {
			report.Complete = false
			break
		}
	}
	return report
}

func (b *Brain) executeWithRetry(task *TaskDefinition, callTool func(name string, args map[string]any) (string, error)) (string, error) {
	result, err := callTool(task.Tool, task.Arguments)
	if err == nil {
		return result, nil
	}

	for attempt := 1; attempt <= maxRetries; attempt++ {
		task.RetryCount = attempt

		alternative, altErr := b.suggestAlternative(task, err.Error())
		if altErr != nil {
			return "", fmt.Errorf("task %d (%s) failed after %d retries: %v (correction failed: %v)",
				task.ID, task.Tool, attempt, err, altErr)
		}
		if alternative == nil {
			return "", fmt.Errorf("task %d (%s) failed: %v", task.ID, task.Tool, err)
		}

		result, err = callTool(alternative.Tool, alternative.Arguments)
		if err == nil {
			task.Tool = alternative.Tool
			task.Arguments = alternative.Arguments
			task.Description = alternative.Description
			return result, nil
		}
	}

	return "", fmt.Errorf("task %d (%s) failed after %d retries: %v", task.ID, task.Tool, maxRetries, err)
}

type alternativeAction struct {
	Tool        string
	Arguments   map[string]any
	Description string
}

func (b *Brain) suggestAlternative(task *TaskDefinition, errMsg string) (*alternativeAction, error) {
	messages := []Message{
		{
			Role: "system",
			Content: "A tool call failed. Suggest an alternative approach to accomplish the same goal. " +
				"If the error is 'not found' or 'invalid input', try with different input. " +
				"If no alternative exists, respond with: {\"alternative\":false}\n" +
				"Otherwise respond with JSON: {\"alternative\":true,\"tool\":\"tool_name\",\"arguments\":{...},\"description\":\"...\"}",
		},
		{
			Role: "user",
			Content: fmt.Sprintf("Task: %s\nTool called: %s\nArguments: %v\nError: %s\nSuggest an alternative.",
				task.Description, task.Tool, task.Arguments, errMsg),
		},
	}

	reqBody := ChatRequest{
		Messages: messages,
		Model:    b.models[0],
	}
	chatResp, err := b.executeChat(reqBody)
	if err != nil {
		return nil, err
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var resp struct {
		Alternative bool           `json:"alternative"`
		Tool        string         `json:"tool"`
		Arguments   map[string]any `json:"arguments"`
		Description string         `json:"description"`
	}
	if err := parseJSON([]byte(content), &resp); err != nil || !resp.Alternative || resp.Tool == "" {
		return nil, nil
	}
	return &alternativeAction{
		Tool:        resp.Tool,
		Arguments:   resp.Arguments,
		Description: resp.Description,
	}, nil
}

func topologicalSort(tasks []*TaskDefinition, taskMap map[int]*TaskDefinition) [][]*TaskDefinition {
	inDegree := make(map[int]int)
	children := make(map[int][]int)

	for _, t := range tasks {
		inDegree[t.ID] = len(t.Dependencies)
		for _, dep := range t.Dependencies {
			children[dep] = append(children[dep], t.ID)
		}
	}

	var groups [][]*TaskDefinition
	for {
		var current []int
		for _, t := range tasks {
			if t.Status == TaskPending && inDegree[t.ID] == 0 {
				current = append(current, t.ID)
			}
		}
		if len(current) == 0 {
			break
		}

		var group []*TaskDefinition
		for _, id := range current {
			group = append(group, taskMap[id])
			inDegree[id] = -1
			for _, child := range children[id] {
				inDegree[child]--
			}
		}
		groups = append(groups, group)
	}
	return groups
}

func parseJSON(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("json parse error: %w", err)
	}
	return nil
}
