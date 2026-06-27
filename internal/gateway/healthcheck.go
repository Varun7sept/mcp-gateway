// healthcheck.go periodically checks if downstream MCP servers are alive
// and discovers what tools they offer.
//
// HOW IT WORKS:
// 1. Every 10 seconds, it loops through all registered servers
// 2. For each server, it sends an "initialize" request (MCP handshake)
// 3. Then it sends a "tools/list" request to discover tools
// 4. If both succeed → server is "online" and tools are registered
// 5. If either fails → server is "offline"
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// mcpRequest is used to send JSON-RPC requests to downstream servers.
type mcpRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      int            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// mcpResponse is used to parse JSON-RPC responses from downstream servers.
type mcpResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// StartHealthChecker starts a background goroutine that checks all servers periodically.
//
// WHAT IS A GOROUTINE?
// Think of it as a lightweight background thread. It runs independently
// without blocking the main program. The "go" keyword launches it.
func (gw *Gateway) StartHealthChecker(interval time.Duration) {
	// Run one check immediately on startup
	gw.checkAllServers()

	// Then run periodically in the background
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			gw.checkAllServers()
		}
	}()

	log.Printf("Health checker started (interval: %s)", interval)
}

// checkAllServers iterates through all servers and checks each one.
func (gw *Gateway) checkAllServers() {
	gw.mu.RLock()
	serverNames := make([]string, 0, len(gw.servers))
	for name := range gw.servers {
		serverNames = append(serverNames, name)
	}
	gw.mu.RUnlock()

	for _, name := range serverNames {
		gw.mu.RLock()
		server := gw.servers[name]
		gw.mu.RUnlock()

		tools, latency, err := gw.checkServer(server)
		if err != nil {
			log.Printf("  [%s] OFFLINE — %v", name, err)
			gw.UpdateServerStatus(name, StatusOffline, nil, 0)
		} else {
			log.Printf("  [%s] ONLINE — %d tools, latency %s", name, len(tools), latency)
			gw.UpdateServerStatus(name, StatusOnline, tools, latency)
		}
	}
}

// checkServer performs the actual health check against a single server.
// Returns the list of tools if successful, or an error if the server is unreachable.
func (gw *Gateway) checkServer(server *ConnectedServer) ([]Tool, time.Duration, error) {
	start := time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	mcpURL := server.Config.URL + "/mcp/message"

	// Step 1: Send "initialize" request (MCP handshake)
	initReq := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "mcp-gateway",
				"version": "1.0.0",
			},
		},
	}

	_, err := sendMCPRequest(client, mcpURL, initReq)
	if err != nil {
		return nil, 0, fmt.Errorf("initialize failed: %w", err)
	}

	// Step 2: Send "tools/list" to discover available tools
	toolsReq := mcpRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	}

	resp, err := sendMCPRequest(client, mcpURL, toolsReq)
	if err != nil {
		return nil, 0, fmt.Errorf("tools/list failed: %w", err)
	}

	latency := time.Since(start)

	// Step 3: Parse the tools from the response
	tools, err := parseTools(resp, server.Config.Name)
	if err != nil {
		return nil, latency, fmt.Errorf("failed to parse tools: %w", err)
	}

	return tools, latency, nil
}

// sendMCPRequest sends a JSON-RPC request to a URL and returns the parsed response.
func sendMCPRequest(client *http.Client, url string, req mcpRequest) (*mcpResponse, error) {
	// Convert request to JSON bytes
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send HTTP POST request
	httpResp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", httpResp.StatusCode)
	}

	// Parse the JSON response
	var resp mcpResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("MCP error: %v", resp.Error)
	}

	return &resp, nil
}

// parseTools extracts tool definitions from an MCP tools/list response.
func parseTools(resp *mcpResponse, serverName string) ([]Tool, error) {
	// The result is a map with a "tools" key containing an array
	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected result type")
	}

	toolsRaw, ok := resultMap["tools"].([]any)
	if !ok {
		return []Tool{}, nil // No tools is okay
	}

	var tools []Tool
	for _, t := range toolsRaw {
		toolMap, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _ := toolMap["name"].(string)
		desc, _ := toolMap["description"].(string)
		tools = append(tools, Tool{
			Name:        name,
			Description: desc,
			ServerName:  serverName,
		})
	}

	return tools, nil
}
