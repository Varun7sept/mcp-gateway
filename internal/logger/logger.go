// Package logger records every request that flows through the gateway.
// Stores the last N requests in memory for the dashboard to display.
package logger

import (
	"sync"
	"time"
)

// RequestLog represents one logged request.
type RequestLog struct {
	ID         int           `json:"id"`
	Timestamp  time.Time     `json:"timestamp"`
	Method     string        `json:"method"`
	ToolName   string        `json:"tool_name"`
	ServerName string        `json:"server_name"`
	Username   string        `json:"username"`
	Latency    time.Duration `json:"latency_ms"`
	Status     string        `json:"status"`
	Error      string        `json:"error,omitempty"`
}

// Stats holds aggregate statistics.
type Stats struct {
	TotalRequests  int            `json:"total_requests"`
	SuccessCount   int            `json:"success_count"`
	ErrorCount     int            `json:"error_count"`
	AvgLatency     time.Duration  `json:"avg_latency_ms"`
	RequestsByTool map[string]int `json:"requests_by_tool"`
	RequestsByServer map[string]int `json:"requests_by_server"`
}

// Logger stores request logs in memory.
type Logger struct {
	logs    []RequestLog
	maxLogs int
	nextID  int
	mu      sync.RWMutex
}

// New creates a logger that stores up to maxLogs entries.
func New(maxLogs int) *Logger {
	return &Logger{
		logs:    make([]RequestLog, 0, maxLogs),
		maxLogs: maxLogs,
		nextID:  1,
	}
}

// Log records a new request.
func (l *Logger) Log(method, toolName, serverName, username, status, errMsg string, latency time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry := RequestLog{
		ID:         l.nextID,
		Timestamp:  time.Now(),
		Method:     method,
		ToolName:   toolName,
		ServerName: serverName,
		Username:   username,
		Latency:    latency,
		Status:     status,
		Error:      errMsg,
	}
	l.nextID++

	l.logs = append(l.logs, entry)

	if len(l.logs) > l.maxLogs {
		l.logs = l.logs[len(l.logs)-l.maxLogs:]
	}
}

// Recent returns the last N log entries (newest first) for the given user.
// Pass empty string to get all logs (unfiltered).
func (l *Logger) Recent(n int, username string) []RequestLog {
	l.mu.RLock()
	defer l.mu.RUnlock()

	var filtered []RequestLog
	for i := len(l.logs) - 1; i >= 0; i-- {
		if username == "" || l.logs[i].Username == username {
			filtered = append(filtered, l.logs[i])
		}
		if len(filtered) >= n {
			break
		}
	}
	return filtered
}

// GetStats returns aggregate statistics, optionally filtered by username.
func (l *Logger) GetStats(username string) Stats {
	l.mu.RLock()
	defer l.mu.RUnlock()

	stats := Stats{
		TotalRequests:    0,
		RequestsByTool:   make(map[string]int),
		RequestsByServer: make(map[string]int),
	}

	var totalLatency time.Duration
	for _, log := range l.logs {
		if username != "" && log.Username != username {
			continue
		}
		stats.TotalRequests++
		if log.Status == "success" {
			stats.SuccessCount++
		} else {
			stats.ErrorCount++
		}
		totalLatency += log.Latency
		if log.ToolName != "" {
			stats.RequestsByTool[log.ToolName]++
		}
		if log.ServerName != "" {
			stats.RequestsByServer[log.ServerName]++
		}
	}

	if stats.TotalRequests > 0 {
		stats.AvgLatency = totalLatency / time.Duration(stats.TotalRequests)
	}

	return stats
}
