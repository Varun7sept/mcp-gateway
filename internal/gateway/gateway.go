// Package gateway is the core of the MCP Gateway.
//
// WHAT THIS DOES:
// - Holds a list of connected MCP servers
// - Routes incoming requests to the correct server
// - Tracks health status of each server
// - Aggregates all tools from all servers into one list
package gateway

import (
	"fmt"
	"sync"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/config"
)

// ServerStatus represents the current health of a downstream MCP server.
type ServerStatus string

const (
	StatusOnline  ServerStatus = "online"
	StatusOffline ServerStatus = "offline"
	StatusUnknown ServerStatus = "unknown"
)

// Tool represents one tool exposed by an MCP server.
// For example: { Name: "get_weather", Description: "Get weather for a city" }
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ServerName  string `json:"server_name"` // Which server owns this tool
}

// ConnectedServer represents one downstream MCP server with its status.
type ConnectedServer struct {
	Config    config.ServerConfig
	Status    ServerStatus
	Tools     []Tool
	LastCheck time.Time
	Latency   time.Duration // How long the last health check took
}

// Gateway is the main struct that coordinates everything.
type Gateway struct {
	servers map[string]*ConnectedServer // key = server name
	mu      sync.RWMutex               // Protects concurrent access to servers map
}

// New creates a new Gateway instance from the given config.
//
// WHAT HAPPENS HERE:
// 1. We loop through all servers in config
// 2. For each enabled server, we create a ConnectedServer entry
// 3. We set initial status to "unknown" (we haven't checked yet)
func New(cfg *config.Config) *Gateway {
	gw := &Gateway{
		servers: make(map[string]*ConnectedServer),
	}

	for _, serverCfg := range cfg.Servers {
		if !serverCfg.Enabled {
			continue // Skip disabled servers
		}

		gw.servers[serverCfg.Name] = &ConnectedServer{
			Config:    serverCfg,
			Status:    StatusUnknown,
			Tools:     []Tool{},
			LastCheck: time.Time{}, // Zero time = never checked
		}
	}

	return gw
}

// ListServers returns info about all connected servers.
// Used by the dashboard to show server status.
func (gw *Gateway) ListServers() []ConnectedServer {
	gw.mu.RLock()
	defer gw.mu.RUnlock()

	result := make([]ConnectedServer, 0, len(gw.servers))
	for _, s := range gw.servers {
		result = append(result, *s)
	}
	return result
}

// ListTools returns ALL tools from ALL online servers.
// This is the "aggregation" — the client sees one unified list.
func (gw *Gateway) ListTools() []Tool {
	gw.mu.RLock()
	defer gw.mu.RUnlock()

	var allTools []Tool
	for _, s := range gw.servers {
		if s.Status == StatusOnline {
			allTools = append(allTools, s.Tools...)
		}
	}
	return allTools
}

// GetServer returns a copy of a specific server by name.
// Used when routing a tool call to the right server.
func (gw *Gateway) GetServer(name string) (ConnectedServer, error) {
	gw.mu.RLock()
	defer gw.mu.RUnlock()

	s, exists := gw.servers[name]
	if !exists {
		return ConnectedServer{}, fmt.Errorf("server %q not found", name)
	}
	return *s, nil
}

// FindToolServer finds which server owns a given tool.
// When a client calls "get_weather", we need to know it belongs to the weather server.
func (gw *Gateway) FindToolServer(toolName string) (ConnectedServer, error) {
	gw.mu.RLock()
	defer gw.mu.RUnlock()

	for _, s := range gw.servers {
		for _, t := range s.Tools {
			if t.Name == toolName {
				return *s, nil
			}
		}
	}
	return ConnectedServer{}, fmt.Errorf("no server found for tool %q", toolName)
}

// UpdateServerStatus updates the health status of a server.
// Called by the health checker periodically.
func (gw *Gateway) UpdateServerStatus(name string, status ServerStatus, tools []Tool, latency time.Duration) {
	gw.mu.Lock()
	defer gw.mu.Unlock()

	if s, exists := gw.servers[name]; exists {
		s.Status = status
		s.LastCheck = time.Now()
		s.Latency = latency
		if tools != nil {
			s.Tools = tools
		}
	}
}
