// forwarder.go handles forwarding MCP tool calls to downstream servers.
//
// WHAT THIS DOES:
// When someone calls a tool (e.g., "get_weather"), the gateway:
// 1. Looks up which server owns that tool
// 2. Forwards the exact request to that server
// 3. Returns the server's response back to the caller
//
// This is the "proxy" part — the gateway sits in the middle.
package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ForwardRequest represents an incoming request to forward.
type ForwardRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// ForwardResult contains the response from the downstream server.
type ForwardResult struct {
	ServerName string        `json:"server_name"`
	Response   any           `json:"response"`
	Latency    time.Duration `json:"latency_ms"`
}

// ForwardToolCall takes a tools/call request, finds the right server, and forwards it.
func (gw *Gateway) ForwardToolCall(req ForwardRequest) (*ForwardResult, error) {
	// Step 1: Extract the tool name from params
	toolName, _ := req.Params["name"].(string)
	if toolName == "" {
		return nil, fmt.Errorf("missing tool name in request params")
	}

	// Step 2: Find which server owns this tool
	server, err := gw.FindToolServer(toolName)
	if err != nil {
		return nil, fmt.Errorf("routing failed: %w", err)
	}

	// Step 3: Forward the request to that server
	start := time.Now()
	response, err := gw.forwardToServer(server, req)
	latency := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("forward to %s failed: %w", server.Config.Name, err)
	}

	return &ForwardResult{
		ServerName: server.Config.Name,
		Response:   response,
		Latency:    latency,
	}, nil
}

// forwardClient is reused across all forwarded tool calls to enable connection pooling.
var forwardClient = &http.Client{Timeout: 30 * time.Second}

// forwardToServer sends the request to a specific downstream server and returns the raw response.
func (gw *Gateway) forwardToServer(server ConnectedServer, req ForwardRequest) (any, error) {
	// Convert request to JSON
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Send to the server's MCP endpoint
	url := server.Config.URL + "/mcp/message"

	httpResp, err := forwardClient.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer httpResp.Body.Close()

	// Read the response body with a size cap to prevent memory exhaustion
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned status %d: %s", httpResp.StatusCode, string(respBody))
	}

	// Parse the JSON response into a generic structure
	var response any
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	return response, nil
}
