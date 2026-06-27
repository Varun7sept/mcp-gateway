package approval

import (
	"fmt"
	"sync"
	"time"
)

type ApprovalStatus string

const (
	StatusPending  ApprovalStatus = "pending"
	StatusApproved ApprovalStatus = "approved"
	StatusRejected ApprovalStatus = "rejected"
	StatusTimedOut ApprovalStatus = "timed_out"
)

type ApprovalRequest struct {
	ID          string         `json:"id"`
	Username    string         `json:"username"`
	Description string         `json:"description"`
	Tool        string         `json:"tool"`
	Arguments   map[string]any `json:"arguments"`
	Status      ApprovalStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
}

type RiskLevel int

const (
	RiskLow    RiskLevel = 0
	RiskMedium RiskLevel = 1
	RiskHigh   RiskLevel = 2
)

var riskyTools = map[string]RiskLevel{
	"add_note":         RiskMedium,
	"upload_document":  RiskHigh,
	"shorten_url":      RiskLow,
}

type Store struct {
	mu       sync.RWMutex
	pending  map[string]*ApprovalRequest
	nextID   int
	timeout  time.Duration
}

func NewStore(timeout time.Duration) *Store {
	s := &Store{
		pending: make(map[string]*ApprovalRequest),
		timeout: timeout,
	}
	go s.reapLoop()
	return s
}

func (s *Store) IsRiskyTool(toolName string) (RiskLevel, bool) {
	level, ok := riskyTools[toolName]
	return level, ok
}

func (s *Store) CreateRequest(username, description, tool string, args map[string]any) *ApprovalRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nextID++
	id := fmt.Sprintf("aprv_%d_%d", time.Now().Unix(), s.nextID)

	req := &ApprovalRequest{
		ID:          id,
		Username:    username,
		Description: description,
		Tool:        tool,
		Arguments:   args,
		Status:      StatusPending,
		CreatedAt:   time.Now(),
		ExpiresAt:   time.Now().Add(s.timeout),
	}
	s.pending[id] = req
	return req
}

func (s *Store) Approve(id, username string) (*ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.pending[id]
	if !ok {
		return nil, fmt.Errorf("approval request %q not found", id)
	}
	if req.Username != username {
		return nil, fmt.Errorf("approval request %q belongs to a different user", id)
	}
	if req.Status != StatusPending {
		return nil, fmt.Errorf("approval request %q is already %s", id, req.Status)
	}
	if time.Now().After(req.ExpiresAt) {
		req.Status = StatusTimedOut
		return nil, fmt.Errorf("approval request %q has expired", id)
	}

	req.Status = StatusApproved
	return req, nil
}

func (s *Store) Reject(id, username string) (*ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.pending[id]
	if !ok {
		return nil, fmt.Errorf("approval request %q not found", id)
	}
	if req.Username != username {
		return nil, fmt.Errorf("approval request %q belongs to a different user", id)
	}
	if req.Status != StatusPending {
		return nil, fmt.Errorf("approval request %q is already %s", id, req.Status)
	}

	req.Status = StatusRejected
	return req, nil
}

func (s *Store) GetPending(username string) []*ApprovalRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*ApprovalRequest
	for _, req := range s.pending {
		if req.Username == username && req.Status == StatusPending {
			result = append(result, req)
		}
	}
	return result
}

func (s *Store) WaitForApproval(id, username string, pollInterval time.Duration) (*ApprovalRequest, error) {
	for {
		s.mu.RLock()
		req, ok := s.pending[id]
		if !ok {
			s.mu.RUnlock()
			return nil, fmt.Errorf("approval request %q not found", id)
		}
		status := req.Status
		s.mu.RUnlock()

		switch status {
		case StatusApproved:
			return req, nil
		case StatusRejected:
			return nil, fmt.Errorf("action rejected by user")
		case StatusTimedOut:
			return nil, fmt.Errorf("approval request timed out")
		}

		if time.Now().After(req.ExpiresAt) {
			s.mu.Lock()
			req.Status = StatusTimedOut
			s.mu.Unlock()
			return nil, fmt.Errorf("approval request timed out")
		}

		time.Sleep(pollInterval)
	}
}

func (s *Store) reapLoop() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for id, req := range s.pending {
			if req.Status == StatusPending && now.After(req.ExpiresAt) {
				req.Status = StatusTimedOut
			}
			if req.Status != StatusPending && now.After(req.CreatedAt.Add(24 * time.Hour)) {
				delete(s.pending, id)
			}
		}
		s.mu.Unlock()
	}
}
