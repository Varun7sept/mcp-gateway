package memory

import (
	"testing"
	"time"
)

func TestImportanceScorer_Score(t *testing.T) {
	scorer := NewImportanceScorer()

	tests := []struct {
		name   string
		query  string
		answer string
		tools  []string
		wantMin float64
		wantMax float64
	}{
		{
			name:   "greeting has low importance",
			query:  "hello",
			answer: "Hi there!",
			wantMin: 0.1,
			wantMax: 0.5,
		},
		{
			name:   "important keywords get higher score",
			query:  "set up authentication",
			answer: "Authentication configured successfully.",
			wantMin: 0.5,
			wantMax: 1.0,
		},
		{
			name:   "tools used add importance",
			query:  "get weather",
			answer: "It is sunny.",
			tools:  []string{"get_weather"},
			wantMin: 0.6,
			wantMax: 1.0,
		},
		{
			name:   "critical keywords get high importance",
			query:  "remember my password is secret123",
			answer: "Password saved.",
			wantMin: 0.8,
			wantMax: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scorer.Score(tt.query, tt.answer, tt.tools)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("Score() = %f, want in range [%f, %f]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestImportanceScorer_ScoreBounds(t *testing.T) {
	scorer := NewImportanceScorer()

	score := scorer.Score("test", "test answer", nil)
	if score < 0.1 || score > 1.0 {
		t.Errorf("Score() = %f, want between 0.1 and 1.0", score)
	}
}

func TestGenerateMemoryID(t *testing.T) {
	id1 := GenerateMemoryID("hello", time.Now())
	id2 := GenerateMemoryID("hello", time.Now())
	id3 := GenerateMemoryID("different query", time.Now())

	if id1 == "" {
		t.Error("GenerateMemoryID returned empty string")
	}
	if len(id1) != 32 {
		t.Errorf("GenerateMemoryID returned ID of length %d, want 32", len(id1))
	}
	if id1 == id3 {
		t.Error("Different queries should produce different IDs")
	}
}

func TestFallbackSummary(t *testing.T) {
	summary := fallbackSummary("what is the weather", []string{"get_weather"})
	if summary == "" {
		t.Error("fallbackSummary should not return empty string")
	}
}

func TestTruncateForSummary(t *testing.T) {
	longText := "This is a very long text that exceeds the maximum allowed length for truncation in tests"
	result := truncateForSummary(longText, 20)
	if len(result) > 23 {
		t.Errorf("truncateForSummary returned text of length %d, want <= 23", len(result))
	}
}