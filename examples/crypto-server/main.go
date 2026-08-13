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

// maxBodyBytes caps how much of a CoinGecko response we read, so a runaway
// or error body cannot exhaust memory.
const maxBodyBytes = 4 * 1024 * 1024

// shortBody returns a trimmed, length-capped excerpt of a response body for
// use in errors and logs. Never log a full body.
func shortBody(body []byte) string {
	excerpt := strings.TrimSpace(string(body))
	if len(excerpt) > 200 {
		excerpt = excerpt[:200] + "..."
	}
	return excerpt
}

// coingeckoBaseURL and coingeckoClient are package-level so tests can point
// them at a fake HTTP server instead of hitting the real CoinGecko API.
var (
	coingeckoBaseURL = "https://api.coingecko.com/api/v3"
	coingeckoClient  = &http.Client{Timeout: 10 * time.Second}
)

func fetchCryptoPrice(coin string) (string, error) {
	return fetchCryptoPriceWithClient(coin, coingeckoClient)
}

func fetchCryptoPriceWithClient(coin string, client *http.Client) (string, error) {
	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=usd,inr&include_24hr_change=true&include_market_cap=true", coingeckoBaseURL, coin)
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("CoinGecko request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read CoinGecko response: %w", err)
	}

	log.Printf("[CRYPTO] CoinGecko status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CoinGecko API returned HTTP %d: %s", resp.StatusCode, shortBody(body))
	}

	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("invalid CoinGecko JSON response: %v (body: %s)", err, shortBody(body))
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
	return fetchTopCryptosWithClient(coingeckoClient)
}

func fetchTopCryptosWithClient(client *http.Client) (string, error) {
	url := coingeckoBaseURL + "/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=10&page=1"
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("CoinGecko request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read CoinGecko response: %w", err)
	}

	log.Printf("[CRYPTO] CoinGecko status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CoinGecko API returned HTTP %d: %s", resp.StatusCode, shortBody(body))
	}

	var coins []struct {
		Name       string  `json:"name"`
		Symbol     string  `json:"symbol"`
		Price      float64 `json:"current_price"`
		Change24h  float64 `json:"price_change_percentage_24h"`
		MarketCap  float64 `json:"market_cap"`
	}
	if err := json.Unmarshal(body, &coins); err != nil {
		return "", fmt.Errorf("invalid CoinGecko JSON response: %v (body: %s)", err, shortBody(body))
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
