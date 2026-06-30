package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var urlToolsClient = &http.Client{Timeout: 10 * time.Second}

var urlTools = []map[string]any{
	{"name": "shorten_url", "description": "Shorten a long URL into a compact short link using TinyURL", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string", "description": "The full URL to shorten (must start with http:// or https://)"}}, "required": []string{"url"}}},
	{"name": "generate_qr", "description": "Generate a QR code image for any text, URL, or data. Returns an image URL.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "description": "Text or URL to encode into the QR code"}, "size": map[string]any{"type": "string", "description": "small, medium, large"}}, "required": []string{"text"}}},
	{"name": "expand_url", "description": "Resolve a shortened URL (e.g. bit.ly, tinyurl.com) to see the full destination URL", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string", "description": "The shortened URL to expand (must start with http:// or https://)"}}, "required": []string{"url"}}},
}

func StartURLTools(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { sendError(w, req.ID, -32700, "Parse error"); return }
		switch req.Method {
		case "initialize": sendResult(w, req.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "url-tools-server", "version": "1.0.0"}})
		case "tools/list": sendResult(w, req.ID, map[string]any{"tools": urlTools})
		case "tools/call": handleURLTool(w, req)
		default: sendError(w, req.ID, -32601, "Method not found")
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) })
	log.Printf("URL Tools MCP Server running on http://localhost%s", port)
	return http.ListenAndServe(port, mux)
}

func handleURLTool(w http.ResponseWriter, req MCPRequest) {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	switch name {
	case "shorten_url":
		u, _ := args["url"].(string)
		if u == "" { sendToolResult(w, req.ID, "Error: url required", true); return }
		resp, err := urlToolsClient.Get(fmt.Sprintf("https://is.gd/create.php?format=simple&url=%s", url.QueryEscape(u)))
		if err != nil { sendToolResult(w, req.ID, "Error: URL shortener unavailable: "+err.Error(), true); return }
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil { sendToolResult(w, req.ID, "Error: failed to read response: "+err.Error(), true); return }
		s := strings.TrimSpace(string(body))
		if strings.HasPrefix(s, "http") { sendToolResult(w, req.ID, fmt.Sprintf("Shortened: %s → %s", u, s), false) } else { sendToolResult(w, req.ID, "Error: URL shortener returned unexpected response", true) }
	case "generate_qr":
		text, _ := args["text"].(string); size, _ := args["size"].(string)
		if text == "" { sendToolResult(w, req.ID, "Error: text required", true); return }
		pixels := "250x250"
		switch size { case "small": pixels = "150x150"; case "large": pixels = "400x400" }
		sendToolResult(w, req.ID, fmt.Sprintf("QR Code:\n  Content: %s\n  Image URL: https://api.qrserver.com/v1/create-qr-code/?size=%s&data=%s", text, pixels, url.QueryEscape(text)), false)
	case "expand_url":
		u, _ := args["url"].(string)
		if u == "" { sendToolResult(w, req.ID, "Error: url required", true); return }
		// Validate scheme to prevent SSRF — only allow public http/https URLs.
		if parsed, err2 := url.Parse(u); err2 != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			sendToolResult(w, req.ID, "Error: only http:// and https:// URLs are supported", true)
			return
		}
		expandClient := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := expandClient.Head(u)
		if err != nil { sendToolResult(w, req.ID, fmt.Sprintf("Error expanding: %v", err), true); return }
		defer resp.Body.Close()
		if loc := resp.Header.Get("Location"); loc != "" { sendToolResult(w, req.ID, fmt.Sprintf("Short: %s\n  Full: %s", u, loc), false) } else { sendToolResult(w, req.ID, fmt.Sprintf("No redirect: %s", u), false) }
	default: sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

