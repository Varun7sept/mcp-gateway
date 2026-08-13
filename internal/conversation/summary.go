package conversation

import (
	"strings"
)

type LLMClient interface {
	GenerateSummary(prompt string) (string, error)
}

type ConversationSummaryGenerator struct {
	llm            LLMClient
	messageThreshold int
	tokenThreshold   int
}

type ConversationSummaryConfig struct {
	MessageThreshold int
	TokenThreshold   int
}

func NewConversationSummaryGenerator(llm LLMClient, config ConversationSummaryConfig) *ConversationSummaryGenerator {
	if config.MessageThreshold <= 0 {
		config.MessageThreshold = 20
	}
	if config.TokenThreshold <= 0 {
		config.TokenThreshold = 8000
	}
	return &ConversationSummaryGenerator{
		llm:              llm,
		messageThreshold: config.MessageThreshold,
		tokenThreshold:   config.TokenThreshold,
	}
}

func (g *ConversationSummaryGenerator) ShouldSummarize(messageCount int, currentMessages []Message) bool {
	if messageCount >= g.messageThreshold {
		return true
	}
	totalTokens := 0
	for _, m := range currentMessages {
		totalTokens += len(m.Content) / 4
	}
	if totalTokens >= g.tokenThreshold {
		return true
	}
	return false
}

func (g *ConversationSummaryGenerator) Generate(summary string, recentMessages []Message) (string, error) {
	prompt := buildConversationSummaryPrompt(summary, recentMessages)

	if g.llm == nil {
		return fallbackConversationSummary(recentMessages), nil
	}

	result, err := g.llm.GenerateSummary(prompt)
	if err != nil {
		return "", err
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return fallbackConversationSummary(recentMessages), nil
	}

	return result, nil
}

func buildConversationSummaryPrompt(existingSummary string, messages []Message) string {
	var recentContent []string
	for _, m := range messages {
		recentContent = append(recentContent, m.Role+": "+m.Content)
	}

	return "Update the following conversation summary based on the recent messages. Keep it concise (1-3 sentences). Include important topics, decisions, conclusions, and unresolved questions.\n\n" +
		"Current summary: " + existingSummary + "\n\n" +
		"Recent messages:\n" + strings.Join(recentContent, "\n") + "\n\n" +
		"Updated summary:"
}

func fallbackConversationSummary(messages []Message) string {
	var topics []string
	for _, m := range messages {
		content := strings.TrimSpace(m.Content)
		if len(content) > 20 {
			if len(content) > 100 {
				content = content[:100]
			}
			topics = append(topics, content)
		}
	}
	if len(topics) == 0 {
		return "Conversation with no significant content."
	}
	return "Recent discussion: " + strings.Join(topics, "; ")
}