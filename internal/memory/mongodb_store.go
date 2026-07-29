package memory

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDBStore struct {
	collection *mongo.Collection
	llm        LLMClient
	embedding  EmbeddingGenerator
	qdrant     *QdrantClient
}

type MongoDBStoreConfig struct {
	CollectionName string
}

func NewMongoDBStore(client *mongo.Client, database, collectionName string, llm LLMClient, embedding EmbeddingGenerator, qdrant *QdrantClient) *MongoDBStore {
	return &MongoDBStore{
		collection: client.Database(database).Collection(collectionName),
		llm:        llm,
		embedding:  embedding,
		qdrant:     qdrant,
	}
}

func (s *MongoDBStore) Save(entry MemoryEntry) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if entry.MemoryID == "" {
		entry.MemoryID = GenerateMemoryID(entry.Query, entry.CreatedAt)
	}

	if entry.Summary == "" {
		summary, err := s.generateSummary(entry.Query, entry.Answer, entry.ToolsUsed)
		if err != nil {
			entry.Summary = fallbackSummary(entry.Query, entry.ToolsUsed)
		} else {
			entry.Summary = summary
		}
	}

	if entry.ImportanceScore == 0 {
		scorer := NewImportanceScorer()
		entry.ImportanceScore = scorer.Score(entry.Query, entry.Answer, entry.ToolsUsed)
	}

	_, err := s.collection.InsertOne(ctx, entry)
	return err
}

func (s *MongoDBStore) generateSummary(query string, answer string, toolsUsed []string) (string, error) {
	if s.llm == nil {
		return fallbackSummary(query, toolsUsed), nil
	}
	prompt := buildSummaryPrompt(query, answer, toolsUsed)
	return s.llm.GenerateSummary(prompt)
}

func (s *MongoDBStore) Retrieve(query string, userID string, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 5
	}

	embedding, err := s.embedding.Generate(query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	qdrantResults, err := s.searchQdrant(embedding, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("qdrant search failed: %w", err)
	}

	if len(qdrantResults) == 0 {
		return []MemoryEntry{}, nil
	}

	memoryIDs := make([]string, 0, len(qdrantResults))
	for _, r := range qdrantResults {
		if memID, ok := r.Payload["memory_id"].(string); ok && memID != "" {
			memoryIDs = append(memoryIDs, memID)
		}
	}

	if len(memoryIDs) == 0 {
		return []MemoryEntry{}, nil
	}

	entries, err := s.fetchFromMongoDB(memoryIDs, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch memories from MongoDB: %w", err)
	}

	ranked := s.rankMemories(entries, qdrantResults)

	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	return ranked, nil
}

func (s *MongoDBStore) searchQdrant(embedding []float64, userID string, limit int) ([]QdrantSearchResult, error) {
	if s.qdrant == nil {
		return nil, nil
	}
	return s.qdrant.Search(embedding, userID, limit)
}

func (s *MongoDBStore) fetchFromMongoDB(memoryIDs []string, userID string) ([]MemoryEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{
		"memory_id": bson.M{"$in": memoryIDs},
		"user_id":   userID,
	}

	cursor, err := s.collection.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var entries []MemoryEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

func (s *MongoDBStore) rankMemories(entries []MemoryEntry, qdrantResults []QdrantSearchResult) []MemoryEntry {
	scoreMap := make(map[string]float64)
	for _, r := range qdrantResults {
		if memID, ok := r.Payload["memory_id"].(string); ok {
			scoreMap[memID] = r.Score
		}
	}

	type scoredEntry struct {
		entry MemoryEntry
		score float64
	}

	var scored []scoredEntry
	for _, entry := range entries {
		semanticScore := scoreMap[entry.MemoryID]
		score := (0.6 * semanticScore) + (0.4 * entry.ImportanceScore)
		scored = append(scored, scoredEntry{entry, score})
	}

	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	result := make([]MemoryEntry, 0, len(scored))
	for _, s := range scored {
		result = append(result, s.entry)
	}

	return result
}

func (s *MongoDBStore) GetRecent(sessionID string, limit int) ([]MemoryEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"session_id": sessionID}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit))

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var entries []MemoryEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

func (s *MongoDBStore) Delete(memoryID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.collection.DeleteOne(ctx, bson.M{"memory_id": memoryID})
	return err
}

func (s *MongoDBStore) ListByUser(userID string, limit int) ([]MemoryEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	filter := bson.M{"user_id": userID}
	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit))

	cursor, err := s.collection.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var entries []MemoryEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

func (s *MongoDBStore) ListAll(limit int) ([]MemoryEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(int64(limit))

	cursor, err := s.collection.Find(ctx, bson.M{}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var entries []MemoryEntry
	if err := cursor.All(ctx, &entries); err != nil {
		return nil, err
	}

	return entries, nil
}

func (s *MongoDBStore) Clear() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := s.collection.Drop(ctx)
	return err
}