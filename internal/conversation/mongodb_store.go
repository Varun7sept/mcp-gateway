package conversation

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDBStore struct {
	sessionsColl *mongo.Collection
	messagesColl *mongo.Collection
}

func NewMongoDBStore(client *mongo.Client, database string) *MongoDBStore {
	return &MongoDBStore{
		sessionsColl: client.Database(database).Collection("chat_sessions"),
		messagesColl: client.Database(database).Collection("chat_messages"),
	}
}

func (s *MongoDBStore) CreateSession(userID string) (*Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session := &Session{
		SessionID:   primitive.NewObjectID().Hex(),
		UserID:      userID,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		MessageCount: 0,
	}

	_, err := s.sessionsColl.InsertOne(ctx, session)
	if err != nil {
		return nil, err
	}

	return session, nil
}

func (s *MongoDBStore) GetSession(sessionID string) (*Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var session Session
	err := s.sessionsColl.FindOne(ctx, bson.M{"session_id": sessionID}).Decode(&session)
	if err != nil {
		return nil, err
	}

	return &session, nil
}

func (s *MongoDBStore) AddMessage(sessionID string, userID string, role string, content string) (*Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	message := &Message{
		MessageID: primitive.NewObjectID().Hex(),
		SessionID: sessionID,
		UserID:    userID,
		Role:      role,
		Content:   content,
		Timestamp: time.Now(),
	}

	_, err := s.messagesColl.InsertOne(ctx, message)
	if err != nil {
		return nil, err
	}

	s.sessionsColl.UpdateOne(ctx,
		bson.M{"session_id": sessionID},
		bson.M{
			"$inc":      bson.M{"message_count": 1},
			"$set":      bson.M{"updated_at": time.Now()},
		},
	)

	return message, nil
}

func (s *MongoDBStore) GetMessages(sessionID string, limit int) ([]Message, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"session_id": sessionID}
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}}).SetLimit(int64(limit))

	cursor, err := s.messagesColl.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var messages []Message
	if err := cursor.All(ctx, &messages); err != nil {
		return nil, err
	}

	return messages, nil
}

func (s *MongoDBStore) UpdateSummary(sessionID string, summary string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.sessionsColl.UpdateOne(ctx,
		bson.M{"session_id": sessionID},
		bson.M{"$set": bson.M{"summary": summary, "updated_at": time.Now()}},
	)
	return err
}

func (s *MongoDBStore) GetSummary(sessionID string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var session Session
	err := s.sessionsColl.FindOne(ctx, bson.M{"session_id": sessionID}).Decode(&session)
	if err != nil {
		return "", err
	}

	return session.Summary, nil
}

func (s *MongoDBStore) DeleteSession(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.sessionsColl.DeleteOne(ctx, bson.M{"session_id": sessionID})
	if err != nil {
		return err
	}

	_, err = s.messagesColl.DeleteMany(ctx, bson.M{"session_id": sessionID})
	return err
}