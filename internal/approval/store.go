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
	notify   map[string]chan struct{} // closed when a request is approved/rejected
	nextID   int
	timeout  time.Duration
	stopCh   chan struct{}
}

func NewStore(timeout time.Duration) *Store {
	s := &Store{
		pending: make(map[string]*ApprovalRequest),
		notify:  make(map[string]chan struct{}),
		timeout: timeout,
		stopCh:  make(chan struct{}),
	}
	go s.reapLoop()
	return s
}

// Close stops the background reaper goroutine.
func (s *Store) Close() {
	close(s.stopCh)
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
	s.notify[id] = make(chan struct{})
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
	if ch, ok := s.notify[id]; ok {
		close(ch)
		delete(s.notify, id)
	}
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
	if ch, ok := s.notify[id]; ok {
		close(ch)
		delete(s.notify, id)
	}
	return req, nil
}

func (s *Store) GetPending(username string) []ApprovalRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []ApprovalRequest
	for _, req := range s.pending {
		if req.Username == username && req.Status == StatusPending {
			result = append(result, *req) // return copy, not pointer into shared map
		}
	}
	return result
}

// WaitForApproval blocks until the request is approved, rejected, or times out.
// It uses a channel notification instead of busy-polling, so it consumes zero
// CPU while waiting and responds instantly when the user acts.
func (s *Store) WaitForApproval(id, username string, _ time.Duration) (*ApprovalRequest, error) {
	// Grab the notify channel and expiry under the lock.
	s.mu.RLock()
	req, ok := s.pending[id]
	if !ok {
		s.mu.RUnlock()
		return nil, fmt.Errorf("approval request %q not found", id)
	}
	ch := s.notify[id]
	expiresAt := req.ExpiresAt
	s.mu.RUnlock()

	// Wait for signal or timeout — zero CPU burn.
	timer := time.NewTimer(time.Until(expiresAt))
	defer timer.Stop()

	select {
	case <-ch:
		// Approved or rejected — read final status.
	case <-timer.C:
		s.mu.Lock()
		if r, exists := s.pending[id]; exists && r.Status == StatusPending {
			r.Status = StatusTimedOut
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("approval request timed out")
	}

	s.mu.RLock()
	req, ok = s.pending[id]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("approval request %q not found after notification", id)
	}

	switch req.Status {
	case StatusApproved:
		copy := *req
		return &copy, nil
	case StatusRejected:
		return nil, fmt.Errorf("action rejected by user")
	default:
		return nil, fmt.Errorf("approval request %q ended with unexpected status: %s", id, req.Status)
	}
}

func (s *Store) reapLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for id, req := range s.pending {
				if req.Status == StatusPending && now.After(req.ExpiresAt) {
					req.Status = StatusTimedOut
					if ch, ok := s.notify[id]; ok {
						close(ch)
						delete(s.notify, id)
					}
				}
				if req.Status != StatusPending && now.After(req.CreatedAt.Add(24*time.Hour)) {
					delete(s.pending, id)
				}
			}
			s.mu.Unlock()
		}
	}
}
