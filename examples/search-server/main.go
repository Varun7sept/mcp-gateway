// Web Search MCP Server — searches the internet for answers.
// Uses DuckDuckGo instant answer API (free, no key needed).
// Runs on port 3007.
package main

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
		"name":        "web_search",
		"description": "Search the internet for any factual information, statistics, current events, or answers to questions",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query (e.g., 'Messi total World Cup goals', 'population of India 2024')"},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "wikipedia_summary",
		"description": "Get a summary from Wikipedia about any topic, person, place, or event",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{"type": "string", "description": "Topic to look up (e.g., 'Lionel Messi', 'Bitcoin', 'Mars')"},
			},
			"required": []string{"topic"},
		},
	},
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", handleMCP)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	log.Println("Web Search MCP Server running on http://localhost:3007")
	log.Fatal(http.ListenAndServe(":3007", mux))
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
			"serverInfo":      map[string]any{"name": "search-server", "version": "1.0.0"},
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
	case "web_search":
		query, _ := args["query"].(string)
		if query == "" {
			sendToolResult(w, req.ID, "Error: query is required", true)
			return
		}
		result, err := duckduckgoSearch(query)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	case "wikipedia_summary":
		topic, _ := args["topic"].(string)
		if topic == "" {
			sendToolResult(w, req.ID, "Error: topic is required", true)
			return
		}
		result, err := wikipediaSummary(topic)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	default:
		sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func duckduckgoSearch(query string) (string, error) {
	apiURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("search failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Abstract       string `json:"Abstract"`
		AbstractSource string `json:"AbstractSource"`
		AbstractURL    string `json:"AbstractURL"`
		Answer         string `json:"Answer"`
		AnswerType     string `json:"AnswerType"`
		Heading        string `json:"Heading"`
		RelatedTopics  []struct {
			Text string `json:"Text"`
			FirstURL string `json:"FirstURL"`
		} `json:"RelatedTopics"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse search results")
	}

	var result strings.Builder

	// Direct answer
	if data.Answer != "" {
		result.WriteString("Answer: " + data.Answer + "\n\n")
	}

	// Abstract (main summary)
	if data.Abstract != "" {
		result.WriteString("Summary: " + data.Abstract + "\n")
		if data.AbstractSource != "" {
			result.WriteString("Source: " + data.AbstractSource + "\n")
		}
		if data.AbstractURL != "" {
			result.WriteString("URL: " + data.AbstractURL + "\n")
		}
	}

	// Related topics (if no direct answer)
	if data.Abstract == "" && data.Answer == "" && len(data.RelatedTopics) > 0 {
		result.WriteString("Related results for '" + query + "':\n")
		limit := 5
		if len(data.RelatedTopics) < limit {
			limit = len(data.RelatedTopics)
		}
		for i := 0; i < limit; i++ {
			if data.RelatedTopics[i].Text != "" {
				result.WriteString(fmt.Sprintf("  %d. %s\n", i+1, data.RelatedTopics[i].Text))
			}
		}
	}

	if result.Len() == 0 {
		return fmt.Sprintf("No direct search results found for '%s'. The query might need to be more specific.", query), nil
	}

	return result.String(), nil
}

func wikipediaSummary(topic string) (string, error) {
	apiURL := fmt.Sprintf("https://en.wikipedia.org/api/rest_v1/page/summary/%s",
		url.PathEscape(strings.ReplaceAll(topic, " ", "_")))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("Wikipedia request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return fmt.Sprintf("No Wikipedia article found for '%s'. Try a different spelling or topic name.", topic), nil
	}

	body, _ := io.ReadAll(resp.Body)

	var data struct {
		Title       string `json:"title"`
		Extract     string `json:"extract"`
		Description string `json:"description"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse Wikipedia response")
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Wikipedia: %s\n", data.Title))
	if data.Description != "" {
		result.WriteString(fmt.Sprintf("Description: %s\n\n", data.Description))
	}
	if data.Extract != "" {
		// Limit to first 500 chars for conciseness
		extract := data.Extract
		if len(extract) > 500 {
			extract = extract[:500] + "..."
		}
		result.WriteString(extract + "\n")
	}
	if data.ContentURLs.Desktop.Page != "" {
		result.WriteString(fmt.Sprintf("\nFull article: %s", data.ContentURLs.Desktop.Page))
	}

	return result.String(), nil
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
