// Crypto MCP Server — real-time cryptocurrency prices from CoinGecko API.
// Free, no API key needed. Runs on port 3004.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
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
		"name":        "get_crypto_price",
		"description": "Get real-time price of any cryptocurrency (Bitcoin, Ethereum, Solana, etc)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"coin": map[string]any{"type": "string", "description": "Coin name or ID (e.g., bitcoin, ethereum, solana, dogecoin)"},
			},
			"required": []string{"coin"},
		},
	},
	{
		"name":        "get_top_cryptos",
		"description": "Get top 10 cryptocurrencies by market cap with prices",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", handleMCP)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	log.Println("Crypto MCP Server (CoinGecko) running on http://localhost:3004")
	log.Fatal(http.ListenAndServe(":3004", mux))
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
			"serverInfo":      map[string]any{"name": "crypto-server", "version": "1.0.0"},
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
	case "get_crypto_price":
		coin, _ := args["coin"].(string)
		if coin == "" {
			sendToolResult(w, req.ID, "Error: coin name required", true)
			return
		}
		result, err := fetchCryptoPrice(strings.ToLower(coin))
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	case "get_top_cryptos":
		result, err := fetchTopCryptos()
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	default:
		sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func fetchCryptoPrice(coin string) (string, error) {
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd,inr&include_24hr_change=true&include_market_cap=true", coin)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse response")
	}

	coinData, exists := data[coin]
	if !exists {
		return "", fmt.Errorf("coin '%s' not found. Try: bitcoin, ethereum, solana, dogecoin", coin)
	}

	usd := coinData["usd"]
	inr := coinData["inr"]
	change := coinData["usd_24h_change"]
	mcap := coinData["usd_market_cap"]

	changeDir := "up"
	if change < 0 {
		changeDir = "down"
	}

	return fmt.Sprintf("%s Price:\n  USD: $%.2f\n  INR: Rs.%.2f\n  24h Change: %.2f%% (%s)\n  Market Cap: $%.0f",
		strings.Title(coin), usd, inr, change, changeDir, mcap), nil
}

func fetchTopCryptos() (string, error) {
	url := "https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=10&page=1"
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var coins []struct {
		Name       string  `json:"name"`
		Symbol     string  `json:"symbol"`
		Price      float64 `json:"current_price"`
		Change24h  float64 `json:"price_change_percentage_24h"`
		MarketCap  float64 `json:"market_cap"`
	}
	if err := json.Unmarshal(body, &coins); err != nil {
		return "", fmt.Errorf("failed to parse response")
	}

	var lines []string
	for i, c := range coins {
		dir := "+"
		if c.Change24h < 0 {
			dir = ""
		}
		lines = append(lines, fmt.Sprintf("  %d. %s (%s) — $%.2f (%s%.1f%%)",
			i+1, c.Name, strings.ToUpper(c.Symbol), c.Price, dir, c.Change24h))
	}

	return "Top 10 Cryptocurrencies by Market Cap:\n" + strings.Join(lines, "\n"), nil
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
