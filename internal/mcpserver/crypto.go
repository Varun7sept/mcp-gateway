package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

var cryptoClient = &http.Client{Timeout: 10 * time.Second}

var cryptoTools = []map[string]any{
	{"name": "get_crypto_price", "description": "Get the live price, 24h change, and market cap for any cryptocurrency (Bitcoin, Ethereum, Solana, etc.)", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"coin": map[string]any{"type": "string", "description": "Coin ID in lowercase, e.g. bitcoin, ethereum, solana, dogecoin, cardano"}}, "required": []string{"coin"}}},
	{"name": "get_top_cryptos", "description": "Get the top 10 cryptocurrencies ranked by market cap with live prices and 24h % change", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}},
}

func StartCrypto(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { sendError(w, req.ID, -32700, "Parse error"); return }
		switch req.Method {
		case "initialize": sendResult(w, req.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "crypto-server", "version": "1.0.0"}})
		case "tools/list": sendResult(w, req.ID, map[string]any{"tools": cryptoTools})
		case "tools/call": handleCryptoTool(w, req)
		default: sendError(w, req.ID, -32601, "Method not found")
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) })
	log.Printf("Crypto MCP Server running on http://localhost%s", port)
	return http.ListenAndServe(port, mux)
}

func handleCryptoTool(w http.ResponseWriter, req MCPRequest) {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	switch name {
	case "get_crypto_price":
		coin, _ := args["coin"].(string)
		if coin == "" { sendToolResult(w, req.ID, "Error: coin required", true); return }
		r, err := fetchCrypto(coin)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	case "get_top_cryptos":
		r, err := fetchTop()
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	default: sendToolResult(w, req.ID, "Unknown tool", true)
	}
}

func fetchCrypto(coin string) (string, error) {
	resp, err := cryptoClient.Get(fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd,inr&include_24hr_change=true&include_market_cap=true", strings.ToLower(coin)))
	if err != nil { return "", err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil { return "", fmt.Errorf("parse error") }
	d, ok := data[strings.ToLower(coin)]
	if !ok { return "", fmt.Errorf("coin '%s' not found", coin) }
	dir := "up"
	if d["usd_24h_change"] < 0 { dir = "down" }
	return fmt.Sprintf("%s Price:\n  USD: $%.2f\n  INR: Rs.%.2f\n  24h: %.2f%% (%s)\n  Market Cap: $%.0f", strings.Title(coin), d["usd"], d["inr"], d["usd_24h_change"], dir, d["usd_market_cap"]), nil
}

func fetchTop() (string, error) {
	resp, err := cryptoClient.Get("https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=10&page=1")
	if err != nil { return "", err }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var coins []struct { Name string `json:"name"`; Symbol string `json:"symbol"`; Price float64 `json:"current_price"`; Change24h float64 `json:"price_change_percentage_24h"` }
	if err := json.Unmarshal(body, &coins); err != nil { return "", fmt.Errorf("parse error") }
	var lines []string
	for i, c := range coins {
		d := "+"
		if c.Change24h < 0 { d = "" }
		lines = append(lines, fmt.Sprintf("  %d. %s (%s) — $%.2f (%s%.1f%%)", i+1, c.Name, strings.ToUpper(c.Symbol), c.Price, d, c.Change24h))
	}
	return "Top 10 Cryptocurrencies:\n" + strings.Join(lines, "\n"), nil
}
