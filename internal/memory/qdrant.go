package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type QdrantConfig struct {
	URL       string
	APIKey    string
	Collection string
}

type QdrantClient struct {
	client    *http.Client
	config    QdrantConfig
	vectorDim int
}

type qdrantPoint struct {
	ID     string                 `json:"id"`
	Vector []float64              `json:"vector"`
	Payload map[string]interface{} `json:"payload"`
}

type qdrantSearchRequest struct {
	CollectionName string          `json:"collection_name"`
	Vector         []float64       `json:"vector"`
	Limit          int             `json:"limit"`
	WithPayload    bool            `json:"with_payload"`
	WithVector     bool            `json:"with_vector"`
	Filter         *qdrantFilter   `json:"filter,omitempty"`
}

type qdrantFilter struct {
	Must []qdrantCondition `json:"must,omitempty"`
}

type qdrantCondition struct {
	Key      string   `json:"key"`
	Match    qdrantMatch `json:"match"`
}

type qdrantMatch struct {
	Keyword string `json:"keyword,omitempty"`
}

type qdrantSearchResponse struct {
	Result []qdrantSearchResult `json:"result"`
}

type qdrantSearchResult struct {
	ID      string                 `json:"id"`
	Payload map[string]interface{} `json:"payload"`
	Score   float64                `json:"score"`
}

func NewQdrantClient(url, apiKey, collection string, vectorDim int) *QdrantClient {
	return &QdrantClient{
		client:    &http.Client{Timeout: 30 * time.Second},
		config:    QdrantConfig{URL: url, APIKey: apiKey, Collection: collection},
		vectorDim: vectorDim,
	}
}

func (q *QdrantClient) UpsertVector(memoryID string, userID string, vector []float64) error {
	point := qdrantPoint{
		ID:      memoryID,
		Vector:  vector,
		Payload: map[string]interface{}{"user_id": userID},
	}
	return q.upsertPoints([]qdrantPoint{point})
}

func (q *QdrantClient) Search(vector []float64, userID string, limit int) ([]QdrantSearchResult, error) {
	req := qdrantSearchRequest{
		CollectionName: q.config.Collection,
		Vector:         vector,
		Limit:          limit,
		WithPayload:    true,
		WithVector:     false,
		Filter: &qdrantFilter{
			Must: []qdrantCondition{
				{
					Key:   "user_id",
					Match: qdrantMatch{Keyword: userID},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Qdrant search request: %w", err)
	}

	url := q.config.URL + "/collections/" + q.config.Collection + "/points/search"
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create Qdrant request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if q.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+q.config.APIKey)
	}

	resp, err := q.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("Qdrant search failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Qdrant response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Qdrant API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var searchResp qdrantSearchResponse
	if err := json.Unmarshal(respBody, &searchResp); err != nil {
		return nil, fmt.Errorf("failed to parse Qdrant response: %w", err)
	}

	results := make([]QdrantSearchResult, 0, len(searchResp.Result))
	for _, r := range searchResp.Result {
		results = append(results, QdrantSearchResult{
			ID:      r.ID,
			Payload: r.Payload,
			Score:   r.Score,
		})
	}

	return results, nil
}

func (q *QdrantClient) DeleteVectors(memoryIDs []string) error {
	if len(memoryIDs) == 0 {
		return nil
	}
	return q.deletePoints(memoryIDs)
}

func (q *QdrantClient) upsertPoints(points []qdrantPoint) error {
	body, err := json.Marshal(map[string]any{
		"points": points,
	})
	if err != nil {
		return err
	}
	url := q.config.URL + "/collections/" + q.config.Collection + "/points"
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if q.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+q.config.APIKey)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func (q *QdrantClient) deletePoints(ids []string) error {
	body, err := json.Marshal(map[string]any{
		"points": ids,
	})
	if err != nil {
		return err
	}
	url := q.config.URL + "/collections/" + q.config.Collection + "/points/delete"
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if q.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+q.config.APIKey)
	}
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

type QdrantSearchResult struct {
	ID      string
	Payload map[string]interface{}
	Score   float64
}