package mcpserver

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

type RSS struct { Channel struct { Items []RSSItem `xml:"item"` } `xml:"channel"` }
type RSSItem struct { Title string `xml:"title"`; Link string `xml:"link"`; PubDate string `xml:"pubDate"`; Source string `xml:"source"` }

var newsClient = &http.Client{Timeout: 10 * time.Second}

var newsTools = []map[string]any{
	{"name": "get_top_news", "description": "Get today's top news headlines for a given topic. Use for general browsing. For specific events or people in the news use search_news instead.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"topic": map[string]any{"type": "string", "description": "Topic category — one of: general, technology, business, sports, science, health (default: general)"}}}},
	{"name": "search_news", "description": "Search news articles by keyword. Best for current events, breaking news, sports scores, people in the news, or politics.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search keyword or phrase e.g. Messi World Cup or OpenAI GPT"}}, "required": []string{"query"}}},
}

func StartNews(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { sendError(w, req.ID, -32700, "Parse error"); return }
		switch req.Method {
		case "initialize": sendResult(w, req.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "news-server", "version": "1.0.0"}})
		case "tools/list": sendResult(w, req.ID, map[string]any{"tools": newsTools})
		case "tools/call": handleNewsTool(w, req)
		default: sendError(w, req.ID, -32601, "Method not found")
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) })
	log.Printf("News MCP Server running on http://localhost%s", port)
	return http.ListenAndServe(port, mux)
}

func handleNewsTool(w http.ResponseWriter, req MCPRequest) {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	switch name {
	case "get_top_news":
		topic, _ := args["topic"].(string)
		if topic == "" { topic = "general" }
		r, err := fetchNews(topic)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	case "search_news":
		q, _ := args["query"].(string)
		if q == "" { sendToolResult(w, req.ID, "Error: query required", true); return }
		r, err := searchNews(q)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	default: sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

var newsFeeds = map[string]string{
	"general": "https://news.google.com/rss?hl=en-US&gl=US&ceid=US:en",
	"technology": "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGRqTVhZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
	"business": "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRGx6TVdZU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
	"sports": "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRFp1ZEdvU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
	"science": "https://news.google.com/rss/topics/CAAqJggKIiBDQkFTRWdvSUwyMHZNRFp0Y1RjU0FtVnVHZ0pWVXlnQVAB?hl=en-US&gl=US&ceid=US:en",
	"health": "https://news.google.com/rss/topics/CAAqIQgKIhtDQkFTRGdvSUwyMHZNR3QwTlRFU0FtVnVLQUFQAQ?hl=en-US&gl=US&ceid=US:en",
}

func fetchNews(topic string) (string, error) {
	feed, ok := newsFeeds[strings.ToLower(topic)]
	if !ok { feed = newsFeeds["general"] }
	items, err := fetchRSS(feed)
	if err != nil { return "", err }
	limit := 8
	if len(items) < limit { limit = len(items) }
	var lines []string
	for i := 0; i < limit; i++ { lines = append(lines, fmt.Sprintf("  %d. %s", i+1, items[i].Title)) }
	return fmt.Sprintf("Top %s News:\n\n%s", strings.Title(topic), strings.Join(lines, "\n")), nil
}

func searchNews(query string) (string, error) {
	items, err := fetchRSS(fmt.Sprintf("https://news.google.com/rss/search?q=%s&hl=en-US&gl=US&ceid=US:en", url.QueryEscape(query)))
	if err != nil { return "", err }
	if len(items) == 0 { return fmt.Sprintf("No news for '%s'", query), nil }
	limit := 8
	if len(items) < limit { limit = len(items) }
	var lines []string
	for i := 0; i < limit; i++ { lines = append(lines, fmt.Sprintf("  %d. %s", i+1, items[i].Title)) }
	return fmt.Sprintf("News about '%s':\n\n%s", query, strings.Join(lines, "\n")), nil
}

func fetchRSS(feedURL string) ([]RSSItem, error) {
	resp, err := newsClient.Get(feedURL)
	if err != nil { return nil, fmt.Errorf("fetch failed: %w", err) }
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil { return nil, fmt.Errorf("read error: %w", err) }
	var rss RSS
	if err := xml.Unmarshal(body, &rss); err != nil { return nil, fmt.Errorf("parse error") }
	return rss.Channel.Items, nil
}
