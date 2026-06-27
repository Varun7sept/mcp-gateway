// News MCP Server — real headlines from Google News RSS feeds.
// Free, no API key needed. Runs on port 3005.
package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
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

var tools = []map[string]any{
	{
		"name":        "get_top_news",
		"description": "Get today's top news headlines from around the world",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{
					"type":        "string",
					"description": "News topic: general, technology, business, sports, science, health (default: general)",
				},
			},
		},
	},
	{
		"name":        "search_news",
		"description": "Search for news articles about any topic or keyword",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search keyword (e.g., 'AI', 'climate change', 'SpaceX')"},
			},
			"required": []string{"query"},
		},
	},
}

// RSS feed structure
type RSS struct {
	Channel struct {
		Items []RSSItem `xml:"item"`
	} `xml:"channel"`
}

type RSSItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	PubDate string `xml:"pubDate"`
	Source  string `xml:"source"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", handleMCP)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	log.Println("News MCP Server (Google News) running on http://localhost:3005")
	log.Fatal(http.ListenAndServe(":3005", mux))
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
			"serverInfo":      map[string]any{"name": "news-server", "version": "1.0.0"},
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
	case "get_top_news":
		topic, _ := args["topic"].(string)
		if topic == "" {
			topic = "general"
		}
		result, err := fetchTopNews(topic)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	case "search_news":
		query, _ := args["query"].(string)
		if query == "" {
			sendToolResult(w, req.ID, "Error: query is required", true)
			return
		}
		result, err := searchNews(query)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	default:
		sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func fetchTopNews(topic string) (string, error) {
	topicMap := map[string]string{
		"general":    "https://news.google.com/rss?hl=en-US&gl=US&ceid=US:en",
		"technology": "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGRqTVhZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"business":   "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGx6TVdZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"sports":     "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRFp1ZEdvU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"science":    "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRFp0Y1RjU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
		"health":     "https://news.google.com/rss/topics/CAAqIQgKIhtDQkFTRGdvSUwyMHZNR3QwTlRFU0FtVnVLQUFQAQ?hl=en-US&gl=US&ceid=US:en",
	}

	feedURL, exists := topicMap[strings.ToLower(topic)]
	if !exists {
		feedURL = topicMap["general"]
	}

	items, err := fetchRSS(feedURL)
	if err != nil {
		return "", err
	}

	var lines []string
	limit := 8
	if len(items) < limit {
		limit = len(items)
	}
	for i := 0; i < limit; i++ {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, items[i].Title))
	}

	return fmt.Sprintf("Top %s News Headlines:\n\n%s", strings.Title(topic), strings.Join(lines, "\n")), nil
}

func searchNews(query string) (string, error) {
	feedURL := fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", url.QueryEscape(query))

	items, err := fetchRSS(feedURL)
	if err != nil {
		return "", err
	}

	if len(items) == 0 {
		return fmt.Sprintf("No news found for '%s'", query), nil
	}

	var lines []string
	limit := 8
	if len(items) < limit {
		limit = len(items)
	}
	for i := 0; i < limit; i++ {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, items[i].Title))
	}

	return fmt.Sprintf("News about '%s':\n\n%s", query, strings.Join(lines, "\n")), nil
}

func fetchRSS(feedURL string) ([]RSSItem, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch news: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil {
		return nil, fmt.Errorf("failed to parse news feed: %w", err)
	}

	return rss.Channel.Items, nil
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
