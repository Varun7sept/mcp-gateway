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

var searchClient = &http.Client{Timeout: 10 * time.Second}

var searchTools = []map[string]any{
	{"name": "web_search", "description": "Search the internet for real-time or niche information. Use for current stats, prices, recent events, or topics not well covered by Wikipedia.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": "Search query e.g. population of Japan 2024 or latest iPhone price India"}}, "required": []string{"query"}}},
	{"name": "wikipedia_summary", "description": "Get a structured Wikipedia summary for any well-known person, place, historical event, or concept. Prefer this over web_search for encyclopedic topics.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"topic": map[string]any{"type": "string", "description": "Topic name e.g. Lionel Messi or Black Hole or French Revolution"}}, "required": []string{"topic"}}},
}

func StartSearch(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { sendError(w, req.ID, -32700, "Parse error"); return }
		switch req.Method {
		case "initialize": sendResult(w, req.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "search-server", "version": "1.0.0"}})
		case "tools/list": sendResult(w, req.ID, map[string]any{"tools": searchTools})
		case "tools/call": handleSearchTool(w, req)
		default: sendError(w, req.ID, -32601, "Method not found")
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) })
	log.Printf("Search MCP Server running on http://localhost%s", port)
	return http.ListenAndServe(port, mux)
}

func handleSearchTool(w http.ResponseWriter, req MCPRequest) {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	switch name {
	case "web_search":
		q, _ := args["query"].(string)
		if q == "" { sendToolResult(w, req.ID, "Error: query required", true); return }
		r, err := duckDuckGo(q)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	case "wikipedia_summary":
		t, _ := args["topic"].(string)
		if t == "" { sendToolResult(w, req.ID, "Error: topic required", true); return }
		r, err := wikiSummary(t)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	default: sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func duckDuckGo(query string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1", url.QueryEscape(query)), nil)
	if err != nil { return "", err }
	req.Header.Set("User-Agent", "MCP-Gateway/1.0 (https://github.com/varun7sept/mcp-gateway)")
	resp, err := searchClient.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil { return "", fmt.Errorf("read error: %w", err) }
	var data struct { Abstract, AbstractSource, AbstractURL, Answer, AnswerType, Heading string; RelatedTopics []struct { Text, FirstURL string } `json:"RelatedTopics"` }
	if err := json.Unmarshal(body, &data); err != nil { return "", fmt.Errorf("parse error") }
	var result strings.Builder
	if data.Answer != "" { result.WriteString("Answer: " + data.Answer + "\n\n") }
	if data.Abstract != "" { result.WriteString("Summary: " + data.Abstract + "\n"); if data.AbstractSource != "" { result.WriteString("Source: " + data.AbstractSource + "\n") } }
	if data.Abstract == "" && data.Answer == "" && len(data.RelatedTopics) > 0 {
		result.WriteString("Related results:\n")
		limit := 5; if len(data.RelatedTopics) < limit { limit = len(data.RelatedTopics) }
		for i := 0; i < limit; i++ { if data.RelatedTopics[i].Text != "" { result.WriteString(fmt.Sprintf("  %d. %s\n", i+1, data.RelatedTopics[i].Text)) } }
	}
	if result.Len() == 0 { return fmt.Sprintf("No results for '%s'. Try being more specific.", query), nil }
	return result.String(), nil
}

func wikiSummary(topic string) (string, error) {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://en.wikipedia.org/api/rest_v1/page/summary/%s", url.PathEscape(strings.ReplaceAll(topic, " ", "_"))), nil)
	if err != nil { return "", err }
	req.Header.Set("User-Agent", "MCP-Gateway/1.0 (https://github.com/varun7sept/mcp-gateway)")
	resp, err := searchClient.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()
	if resp.StatusCode == 404 { return fmt.Sprintf("No Wikipedia article for '%s'", topic), nil }
	body, err := io.ReadAll(resp.Body)
	if err != nil { return "", fmt.Errorf("read error: %w", err) }
	var data struct { Title string `json:"title"`; Extract string `json:"extract"`; Description string `json:"description"`; ContentURLs struct { Desktop struct { Page string `json:"page"` } `json:"desktop"` } `json:"content_urls"` }
	if err := json.Unmarshal(body, &data); err != nil { return "", fmt.Errorf("parse error") }
	result := fmt.Sprintf("Wikipedia: %s\n", data.Title)
	if data.Description != "" { result += fmt.Sprintf("Description: %s\n\n", data.Description) }
	if data.Extract != "" { if len(data.Extract) > 500 { data.Extract = data.Extract[:500] + "..." }; result += data.Extract + "\n" }
	if data.ContentURLs.Desktop.Page != "" { result += fmt.Sprintf("\nFull article: %s", data.ContentURLs.Desktop.Page) }
	return result, nil
}
