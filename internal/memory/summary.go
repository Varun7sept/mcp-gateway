package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type LLMClient interface {
	GenerateSummary(prompt string) (string, error)
}

type InteractionSummaryGenerator struct {
	llm LLMClient
}

func NewInteractionSummaryGenerator(llm LLMClient) *InteractionSummaryGenerator {
	return &InteractionSummaryGenerator{llm: llm}
}

func (s *InteractionSummaryGenerator) Generate(query string, answer string, toolsUsed []string) (summary string, err error) {
	prompt := buildSummaryPrompt(query, answer, toolsUsed)
	summary, err = s.llm.GenerateSummary(prompt)
	if err != nil {
		return "", fmt.Errorf("failed to generate interaction summary: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return fallbackSummary(query, toolsUsed), nil
	}
	return summary, nil
}

func buildSummaryPrompt(query string, answer string, toolsUsed []string) string {
	truncatedAnswer := truncateForSummary(answer, 500)
	return fmt.Sprintf(
		"Summarize this AI interaction in 1-3 sentences. Focus only on important concepts. Remove conversational noise.\n\nUser query: %s\nAssistant answer: %s\nTools used: %s\n\nSummary:",
		query, truncatedAnswer, strings.Join(toolsUsed, ", "),
	)
}

func truncateForSummary(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func fallbackSummary(query string, toolsUsed []string) string {
	parts := []string{"Interaction about " + query}
	if len(toolsUsed) > 0 {
		parts = append(parts, "involved using "+strings.Join(toolsUsed, ", "))
	}
	return strings.Join(parts, " ") + "."
}

func GenerateMemoryID(query string, createdAt time.Time) string {
	data := query + createdAt.UTC().Format(time.RFC3339Nano)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:16])
}