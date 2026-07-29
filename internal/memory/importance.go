package memory

import (
	"strings"
)

type ImportanceScorer struct {
	highValueKeywords []string
	mediumValueKeywords []string
}

func NewImportanceScorer() *ImportanceScorer {
	return &ImportanceScorer{
		highValueKeywords: []string{
			"preference", "never", "always", "important", "urgent", "critical",
			"must", "required", "essential", "fundamental", "permanent",
			"remember", "configure", "setup", "authenticate", "secret",
			"password", "token", "key", "credential",
		},
		mediumValueKeywords: []string{
			"preference", "want", "like", "prefer", "usually",
			"often", "sometimes", "typically", "generally",
			"architecture", "design", "pattern", "strategy",
			"plan", "decision", "conclusion",
		},
	}
}

func (s *ImportanceScorer) Score(query string, answer string, toolsUsed []string) float64 {
	text := strings.ToLower(query + " " + answer + " " + strings.Join(toolsUsed, " "))
	score := 0.5

	for _, kw := range s.highValueKeywords {
		if strings.Contains(text, kw) {
			score += 0.2
		}
	}

	for _, kw := range s.mediumValueKeywords {
		if strings.Contains(text, kw) {
			score += 0.1
		}
	}

	if len(toolsUsed) > 0 {
		score += 0.1
	}

	if score > 1.0 {
		score = 1.0
	}
	if score < 0.1 {
		score = 0.1
	}

	return score
}