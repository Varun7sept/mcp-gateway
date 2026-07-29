package ai

import (
	"fmt"
	"strings"
)

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func (b *Brain) RetrieveRelevantMemories(query string, userID string) string {
	if b.memory == nil {
		return ""
	}

	entries, err := b.memory.Retrieve(query, userID, 3)
	if err != nil || len(entries) == 0 {
		return ""
	}

	var parts []string
	for i, e := range entries {
		parts = append(parts, fmt.Sprintf("Past interaction %d:\n  User asked: %s\n  I answered: %s\n  Summary: %s\n  Importance: %.1f\n  Tools used: %s",
			i+1, e.Query, truncate(e.Answer, 200), e.Summary, e.ImportanceScore, strings.Join(e.ToolsUsed, ", ")))
	}
	return "Here are relevant past conversations for context:\n\n" + strings.Join(parts, "\n\n")
}