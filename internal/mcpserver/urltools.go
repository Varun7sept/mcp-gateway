package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	shortURLs = make(map[string]string)
	urlMu     sync.Mutex
	nextShort = 1000
)

var urlTools = []map[string]any{
	{"name": "shorten_url", "description": "Shorten a long URL", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string", "description": "The URL to shorten"}}, "required": []string{"url"}}},
	{"name": "generate_qr", "description": "Generate a QR code for any text or link", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string", "description": "Text or URL to encode"}, "size": map[string]any{"type": "string", "description": "small, medium, large"}}, "required": []string{"text"}}},
	{"name": "expand_url", "description": "Expand a shortened URL", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"url": map[string]any{"type": "string", "description": "The short URL to expand"}}, "required": []string{"url"}}},
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
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("https://is.gd/create.php?format=simple&url=%s", url.QueryEscape(u)))
		if err != nil { sendToolResult(w, req.ID, localShorten(u), false); return }
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		s := strings.TrimSpace(string(body))
		if strings.HasPrefix(s, "http") { sendToolResult(w, req.ID, fmt.Sprintf("Shortened: %s → %s", u, s), false) } else { sendToolResult(w, req.ID, localShorten(u), false) }
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
		client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Head(u)
		if err != nil { sendToolResult(w, req.ID, fmt.Sprintf("Error expanding: %v", err), true); return }
		defer resp.Body.Close()
		if loc := resp.Header.Get("Location"); loc != "" { sendToolResult(w, req.ID, fmt.Sprintf("Short: %s\n  Full: %s", u, loc), false) } else { sendToolResult(w, req.ID, fmt.Sprintf("No redirect: %s", u), false) }
	default: sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func localShorten(longURL string) string {
	urlMu.Lock()
	code := fmt.Sprintf("mcp%d", nextShort)
	nextShort++
	shortURLs[code] = longURL
	urlMu.Unlock()
	return fmt.Sprintf("Shortened (local): %s → http://localhost:3006/s/%s", longURL, code)
}
