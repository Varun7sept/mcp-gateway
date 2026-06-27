package auth

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type ChatSession struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Title     string    `json:"title"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatMessage struct {
	ID        string         `json:"id"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Meta      map[string]any `json:"meta,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

type ChatStore struct {
	sessions *mongo.Collection
	messages *mongo.Collection
}

func (a *Auth) ChatStore() *ChatStore {
	db := a.users.Database()
	return &ChatStore{
		sessions: db.Collection("chat_sessions"),
		messages: db.Collection("chat_messages"),
	}
}

func (cs *ChatStore) CreateSession(username, title string) (*ChatSession, error) {
	now := time.Now()
	oid := primitive.NewObjectID()
	_, err := cs.sessions.InsertOne(context.Background(), bson.M{
		"_id":        oid,
		"username":   username,
		"title":      title,
		"created_at": now,
		"updated_at": now,
	})
	if err != nil {
		return nil, err
	}
	return &ChatSession{
		ID:        oid.Hex(),
		Username:  username,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (cs *ChatStore) ListSessions(username string) ([]ChatSession, error) {
	ctx := context.Background()
	cursor, err := cs.sessions.Find(ctx, bson.M{"username": username},
		options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}))
	if err != nil {
		return nil, err
	}
	var raw []bson.M
	if err := cursor.All(ctx, &raw); err != nil {
		return nil, err
	}
	sessions := make([]ChatSession, 0, len(raw))
	for _, r := range raw {
		sessions = append(sessions, bsonToSession(r))
	}
	return sessions, nil
}

func (cs *ChatStore) GetSession(id, username string) (*ChatSession, error) {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return nil, err
	}
	var raw bson.M
	err = cs.sessions.FindOne(context.Background(), bson.M{"_id": oid, "username": username}).Decode(&raw)
	if err != nil {
		return nil, err
	}
	s := bsonToSession(raw)
	return &s, nil
}

func (cs *ChatStore) DeleteSession(id, username string) error {
	oid, err := primitive.ObjectIDFromHex(id)
	if err != nil {
		return err
	}
	if _, err := cs.sessions.DeleteOne(context.Background(), bson.M{"_id": oid, "username": username}); err != nil {
		return err
	}
	_, err = cs.messages.DeleteMany(context.Background(), bson.M{"session_id": oid})
	return err
}

func (cs *ChatStore) UpdateSessionTitle(sessionID, username, title string) error {
	oid, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		return err
	}
	_, err = cs.sessions.UpdateOne(context.Background(),
		bson.M{"_id": oid, "username": username},
		bson.M{"$set": bson.M{"title": title, "updated_at": time.Now()}})
	return err
}

func (cs *ChatStore) AddMessage(sessionID, role, content string, meta map[string]any) error {
	oid, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		return err
	}
	_, err = cs.messages.InsertOne(context.Background(), bson.M{
		"session_id": oid,
		"role":       role,
		"content":    content,
		"meta":       meta,
		"created_at": time.Now(),
	})
	if err != nil {
		return err
	}
	cs.sessions.UpdateByID(context.Background(), oid, bson.M{"$set": bson.M{"updated_at": time.Now()}})
	return nil
}

func (cs *ChatStore) GetMessages(sessionID string) ([]ChatMessage, error) {
	oid, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	cursor, err := cs.messages.Find(ctx, bson.M{"session_id": oid},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var raw []bson.M
	if err := cursor.All(ctx, &raw); err != nil {
		return nil, err
	}
	msgs := make([]ChatMessage, 0, len(raw))
	for _, r := range raw {
		msgs = append(msgs, bsonToMessage(r))
	}
	return msgs, nil
}

func (cs *ChatStore) GetRecentMessages(sessionID string, limit int) ([]ChatMessage, error) {
	oid, err := primitive.ObjectIDFromHex(sessionID)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	cursor, err := cs.messages.Find(ctx, bson.M{"session_id": oid},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit)))
	if err != nil {
		return nil, err
	}
	var raw []bson.M
	if err := cursor.All(ctx, &raw); err != nil {
		return nil, err
	}
	// Reverse to chronological order
	msgs := make([]ChatMessage, 0, len(raw))
	for i := len(raw) - 1; i >= 0; i-- {
		msgs = append(msgs, bsonToMessage(raw[i]))
	}
	return msgs, nil
}

func bsonToSession(r bson.M) ChatSession {
	s := ChatSession{
		Username:  getStr(r, "username"),
		Title:     getStr(r, "title"),
		CreatedAt: getTime(r, "created_at"),
		UpdatedAt: getTime(r, "updated_at"),
	}
	if id, ok := r["_id"].(primitive.ObjectID); ok {
		s.ID = id.Hex()
	}
	return s
}

func bsonToMessage(r bson.M) ChatMessage {
	m := ChatMessage{
		Role:      getStr(r, "role"),
		Content:   getStr(r, "content"),
		CreatedAt: getTime(r, "created_at"),
	}
	if id, ok := r["_id"].(primitive.ObjectID); ok {
		m.ID = id.Hex()
	}
	if meta, ok := r["meta"]; ok {
		if mm, ok := meta.(map[string]any); ok {
			m.Meta = mm
		} else if mm, ok := meta.(bson.M); ok {
			m.Meta = mm
		}
	}
	return m
}

func getStr(m bson.M, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getTime(m bson.M, key string) time.Time {
	if v, ok := m[key]; ok {
		if t, ok := v.(time.Time); ok {
			return t
		}
	}
	return time.Time{}
}
