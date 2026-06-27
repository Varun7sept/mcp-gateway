package ai

import (
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

type MemoryEntry struct {
	Query     string    `json:"query"`
	Answer    string    `json:"answer"`
	ToolsUsed []string  `json:"tools_used"`
	Timestamp time.Time `json:"timestamp"`
}

type MemoryStore interface {
	Save(entry MemoryEntry) error
	QueryRelevant(query string, limit int) ([]MemoryEntry, error)
	GetRecent(limit int) ([]MemoryEntry, error)
	Clear() error
}

type InMemoryStore struct {
	mu      sync.RWMutex
	entries []MemoryEntry
	maxSize int
}

func NewInMemoryStore(maxSize int) *InMemoryStore {
	return &InMemoryStore{
		entries: make([]MemoryEntry, 0, maxSize),
		maxSize: maxSize,
	}
}

func (s *InMemoryStore) Save(entry MemoryEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry.Timestamp = time.Now()
	s.entries = append(s.entries, entry)
	if len(s.entries) > s.maxSize {
		s.entries = s.entries[len(s.entries)-s.maxSize:]
	}
	return nil
}

func (s *InMemoryStore) QueryRelevant(query string, limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type scored struct {
		entry MemoryEntry
		score int
	}

	queryWords := tokenize(query)
	var scoredEntries []scored

	for _, entry := range s.entries {
		score := 0
		entryWords := tokenize(entry.Query + " " + entry.Answer)
		for _, qw := range queryWords {
			for _, ew := range entryWords {
				if strings.EqualFold(qw, ew) {
					score++
				}
			}
		}
		if score > 0 {
			scoredEntries = append(scoredEntries, scored{entry, score})
		}
	}

	for i := 0; i < len(scoredEntries); i++ {
		for j := i + 1; j < len(scoredEntries); j++ {
			if scoredEntries[j].score > scoredEntries[i].score {
				scoredEntries[i], scoredEntries[j] = scoredEntries[j], scoredEntries[i]
			}
		}
	}

	if limit > len(scoredEntries) {
		limit = len(scoredEntries)
	}
	result := make([]MemoryEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = scoredEntries[i].entry
	}
	return result, nil
}

func (s *InMemoryStore) GetRecent(limit int) ([]MemoryEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if limit > len(s.entries) {
		limit = len(s.entries)
	}
	result := make([]MemoryEntry, limit)
	for i := 0; i < limit; i++ {
		result[i] = s.entries[len(s.entries)-1-i]
	}
	return result, nil
}

func (s *InMemoryStore) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = s.entries[:0]
	return nil
}

func (b *Brain) RetrieveRelevantMemories(query string) string {
	if b.memory == nil {
		return ""
	}

	entries, err := b.memory.QueryRelevant(query, 3)
	if err != nil || len(entries) == 0 {
		return ""
	}

	var parts []string
	for i, e := range entries {
		parts = append(parts, fmt.Sprintf("Past interaction %d:\n  User asked: %s\n  I answered: %s\n  Tools used: %s",
			i+1, e.Query, truncate(e.Answer, 200), strings.Join(e.ToolsUsed, ", ")))
	}
	return "Here are relevant past conversations for context:\n\n" + strings.Join(parts, "\n\n")
}

func tokenize(s string) []string {
	var words []string
	var current []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, unicode.ToLower(r))
		} else if len(current) > 0 {
			if len(current) > 2 {
				words = append(words, string(current))
			}
			current = nil
		}
	}
	if len(current) > 2 {
		words = append(words, string(current))
	}
	return words
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
