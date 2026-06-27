// URL Tools MCP Server — URL shortener + QR code generator.
// Uses free APIs (cleanuri.com for shortening, goqr.me for QR codes).
// Runs on port 3006.
package main

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

type MCPRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

// Simple in-memory URL shortener
var (
	shortURLs = make(map[string]string)
	urlMu     sync.Mutex
	nextShort = 1000
)

var tools = []map[string]any{
	{
		"name":        "shorten_url",
		"description": "Shorten a long URL into a short link",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "The URL to shorten (e.g., https://github.com/torvalds/linux)"},
			},
			"required": []string{"url"},
		},
	},
	{
		"name":        "generate_qr",
		"description": "Generate a QR code URL for any text or link (returns an image URL you can open)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string", "description": "Text or URL to encode in QR code"},
				"size": map[string]any{"type": "string", "description": "QR code size: small, medium, large (default: medium)"},
			},
			"required": []string{"text"},
		},
	},
	{
		"name":        "expand_url",
		"description": "Expand a shortened URL to see where it actually leads",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "The short URL to expand"},
			},
			"required": []string{"url"},
		},
	},
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", handleMCP)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	log.Println("URL Tools MCP Server running on http://localhost:3006")
	log.Fatal(http.ListenAndServe(":3006", mux))
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, req.ID, -32700, "Parse error")
		return
	}
	switch req.Method {
	case "initialize":
		sendResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "url-tools-server", "version": "1.0.0"},
		})
	case "tools/list":
		sendResult(w, req.ID, map[string]any{"tools": tools})
	case "tools/call":
		handleToolCall(w, req)
	default:
		sendError(w, req.ID, -32601, "Method not found")
	}
}

func handleToolCall(w http.ResponseWriter, req MCPRequest) {
	toolName, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)

	switch toolName {
	case "shorten_url":
		longURL, _ := args["url"].(string)
		if longURL == "" {
			sendToolResult(w, req.ID, "Error: url is required", true)
			return
		}
		result := shortenURL(longURL)
		sendToolResult(w, req.ID, result, false)

	case "generate_qr":
		text, _ := args["text"].(string)
		size, _ := args["size"].(string)
		if text == "" {
			sendToolResult(w, req.ID, "Error: text is required", true)
			return
		}
		result := generateQR(text, size)
		sendToolResult(w, req.ID, result, false)

	case "expand_url":
		shortURL, _ := args["url"].(string)
		if shortURL == "" {
			sendToolResult(w, req.ID, "Error: url is required", true)
			return
		}
		result := expandURL(shortURL)
		sendToolResult(w, req.ID, result, false)

	default:
		sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func shortenURL(longURL string) string {
	// Use is.gd free URL shortener API
	apiURL := fmt.Sprintf("https://is.gd/create.php?format=simple&url=%s", url.QueryEscape(longURL))
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		// Fallback to local shortener
		return localShorten(longURL)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	shortened := strings.TrimSpace(string(body))

	if strings.HasPrefix(shortened, "http") {
		return fmt.Sprintf("Shortened URL:\n  Original: %s\n  Short: %s\n\nThe short link redirects to the original URL.", longURL, shortened)
	}
	return localShorten(longURL)
}

func localShorten(longURL string) string {
	urlMu.Lock()
	code := fmt.Sprintf("mcp%d", nextShort)
	nextShort++
	shortURLs[code] = longURL
	urlMu.Unlock()

	return fmt.Sprintf("Shortened URL (local):\n  Original: %s\n  Short: http://localhost:3006/s/%s\n\nNote: This is a local short URL for demo purposes.", longURL, code)
}

func generateQR(text string, size string) string {
	pixels := "200x200"
	switch size {
	case "small":
		pixels = "150x150"
	case "large":
		pixels = "400x400"
	default:
		pixels = "250x250"
	}

	qrURL := fmt.Sprintf("https://api.qrserver.com/v1/create-qr-code/?size=%s&data=%s",
		pixels, url.QueryEscape(text))

	return fmt.Sprintf("QR Code generated!\n  Content: %s\n  Size: %s\n  Image URL: %s\n\nOpen the image URL in a browser to see your QR code.", text, pixels, qrURL)
}

func expandURL(shortURL string) string {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // Don't follow redirects
		},
	}

	resp, err := client.Head(shortURL)
	if err != nil {
		return fmt.Sprintf("Could not expand URL: %s\nError: %v", shortURL, err)
	}
	defer resp.Body.Close()

	location := resp.Header.Get("Location")
	if location != "" {
		return fmt.Sprintf("Expanded URL:\n  Short: %s\n  Full: %s\n  Status: %d", shortURL, location, resp.StatusCode)
	}

	return fmt.Sprintf("URL does not redirect:\n  URL: %s\n  Status: %d (no redirect)", shortURL, resp.StatusCode)
}

func sendResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: id, Result: result})
}
func sendError(w http.ResponseWriter, id any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: id, Error: map[string]any{"code": code, "message": msg}})
}
func sendToolResult(w http.ResponseWriter, id any, text string, isError bool) {
	sendResult(w, id, map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isError})
}
