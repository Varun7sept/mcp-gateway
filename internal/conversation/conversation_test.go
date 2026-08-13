package conversation

import (
	"testing"
	"time"
)

func TestConversationSummaryGenerator_ShouldSummarize(t *testing.T) {
	gen := NewConversationSummaryGenerator(nil, ConversationSummaryConfig{
		MessageThreshold: 20,
		TokenThreshold:   8000,
	})

	messages := make([]Message, 30)
	for i := range messages {
		messages[i] = Message{
			MessageID: string(rune('a' + i)),
			SessionID: "test-session",
			UserID:    "test-user",
			Role:      "user",
			Content:   "test message content",
			Timestamp: time.Now(),
		}
	}

	if !gen.ShouldSummarize(30, messages) {
		t.Error("ShouldSummarize should return true when message count exceeds threshold")
	}

	if gen.ShouldSummarize(5, messages[:5]) {
		t.Error("ShouldSummarize should return false when message count is below threshold")
	}
}

func TestConversationSummaryGenerator_Generate(t *testing.T) {
	gen := NewConversationSummaryGenerator(nil, ConversationSummaryConfig{
		MessageThreshold: 20,
		TokenThreshold:   8000,
	})

	messages := []Message{
		{Role: "user", Content: "explain memory subsystem"},
		{Role: "assistant", Content: "the memory subsystem has two parts: retrieval memory and conversation history"},
	}

	summary, err := gen.Generate("Existing summary", messages)
	if err != nil {
		t.Errorf("Generate() returned error: %v", err)
	}
	if summary == "" {
		t.Error("Generate() should return non-empty summary")
	}
}

func TestConversationSummaryGenerator_Fallback(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "hello"},
	}

	summary := fallbackConversationSummary(messages)
	if summary == "" {
		t.Error("fallbackConversationSummary should not return empty string")
	}
}