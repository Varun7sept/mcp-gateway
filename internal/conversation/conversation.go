package conversation

import (
	"time"
)

type Message struct {
	MessageID string    `json:"message_id" bson:"message_id"`
	SessionID string    `json:"session_id" bson:"session_id"`
	UserID    string    `json:"user_id" bson:"user_id"`
	Role      string    `json:"role" bson:"role"`
	Content   string    `json:"content" bson:"content"`
	Timestamp time.Time `json:"timestamp" bson:"timestamp"`
}

type Session struct {
	SessionID   string    `json:"session_id" bson:"session_id"`
	UserID      string    `json:"user_id" bson:"user_id"`
	CreatedAt   time.Time `json:"created_at" bson:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" bson:"updated_at"`
	Summary     string    `json:"summary" bson:"summary"`
	MessageCount int      `json:"message_count" bson:"message_count"`
}

type ConversationStore interface {
	CreateSession(userID string) (*Session, error)
	GetSession(sessionID string) (*Session, error)
	AddMessage(sessionID string, userID string, role string, content string) (*Message, error)
	GetMessages(sessionID string, limit int) ([]Message, error)
	UpdateSummary(sessionID string, summary string) error
	GetSummary(sessionID string) (string, error)
	DeleteSession(sessionID string) error
}