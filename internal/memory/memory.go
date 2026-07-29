package memory

import (
	"time"
)

type MemoryEntry struct {
	MemoryID        string    `json:"memory_id" bson:"memory_id"`
	UserID          string    `json:"user_id" bson:"user_id"`
	SessionID       string    `json:"session_id" bson:"session_id"`
	Query           string    `json:"query" bson:"query"`
	Answer          string    `json:"answer" bson:"answer"`
	Summary         string    `json:"summary" bson:"summary"`
	ImportanceScore float64   `json:"importance_score" bson:"importance_score"`
	ToolsUsed       []string  `json:"tools_used" bson:"tools_used"`
	CreatedAt       time.Time `json:"created_at" bson:"created_at"`
}

type MemoryStore interface {
	Save(entry MemoryEntry) error
	Retrieve(query string, userID string, limit int) ([]MemoryEntry, error)
	GetRecent(sessionID string, limit int) ([]MemoryEntry, error)
	Delete(memoryID string) error
	ListByUser(userID string, limit int) ([]MemoryEntry, error)
	ListAll(limit int) ([]MemoryEntry, error)
	Clear() error
}