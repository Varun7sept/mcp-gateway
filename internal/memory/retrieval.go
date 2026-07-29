package memory

import "time"

type RetrievalPipeline struct {
	store       *MongoDBStore
	topK        int
	recencyWeight float64
}

type RetrievalConfig struct {
	TopK           int
	RecencyWeight  float64
}

func NewRetrievalPipeline(store *MongoDBStore, config RetrievalConfig) *RetrievalPipeline {
	if config.TopK <= 0 {
		config.TopK = 5
	}
	if config.RecencyWeight < 0 {
		config.RecencyWeight = 0.2
	}
	return &RetrievalPipeline{
		store:         store,
		topK:          config.TopK,
		recencyWeight: config.RecencyWeight,
	}
}

func (p *RetrievalPipeline) Retrieve(query string, userID string, sessionID string) ([]MemoryEntry, error) {
	entries, err := p.store.Retrieve(query, userID, p.topK*2)
	if err != nil {
		return nil, err
	}

	if p.recencyWeight > 0 {
		entries = p.rerankByRecency(entries)
	}

	if len(entries) > p.topK {
		entries = entries[:p.topK]
	}

	return entries, nil
}

func (p *RetrievalPipeline) rerankByRecency(entries []MemoryEntry) []MemoryEntry {
	if len(entries) == 0 {
		return entries
	}

	now := entries[0].CreatedAt
	for _, e := range entries {
		if e.CreatedAt.After(now) {
			now = e.CreatedAt
		}
	}

	type scoredEntry struct {
		entry MemoryEntry
		score float64
	}

	var scored []scoredEntry
	maxAge := time.Hour * 24 * 30
	if len(entries) > 1 {
		age := now.Sub(entries[len(entries)-1].CreatedAt)
		if age > 0 {
			maxAge = age
		}
	}

	for _, e := range entries {
		age := now.Sub(e.CreatedAt)
		recencyScore := 1.0 - (float64(age)/float64(maxAge)) * p.recencyWeight
		if recencyScore < 0 {
			recencyScore = 0
		}
		scored = append(scored, scoredEntry{e, recencyScore})
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