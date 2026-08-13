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

// maxCryptoBodyBytes caps how much of a CoinGecko response we read.
const maxCryptoBodyBytes = 4 * 1024 * 1024

// shortCryptoBody returns a trimmed, length-capped excerpt of a response body
// for errors and logs. Never log a full body.
func shortCryptoBody(body []byte) string {
	excerpt := strings.TrimSpace(string(body))
	if len(excerpt) > 200 {
		excerpt = excerpt[:200] + "..."
	}
	return excerpt
}

// coingeckoBaseURL and coinPaprikaBaseURL are package-level so tests can point
// them at a fake HTTP server instead of hitting the real APIs. CoinPaprika is
// the automatic fallback because CoinGecko can block datacenter IPs (e.g.
// Render) with HTTP 400.
var (
	coingeckoBaseURL   = "https://api.coingecko.com/api/v3"
	coinPaprikaBaseURL = "https://api.coinpaprika.com/v1"
)

// paprikaCoinIDs maps CoinGecko-style coin names to CoinPaprika ticker IDs.
var paprikaCoinIDs = map[string]string{
	"bitcoin":  "btc-bitcoin",
	"ethereum": "eth-ethereum",
	"solana":   "sol-solana",
	"dogecoin": "doge-dogecoin",
	"cardano":  "ada-cardano",
	"xrp":      "xrp-xrp",
}

func fetchCrypto(coin string) (string, error) {
	return fetchCryptoWithClient(coin, cryptoClient)
}

func fetchCryptoWithClient(coin string, client *http.Client) (string, error) {
	result, err := fetchCryptoFromCoinGecko(coin, client)
	if err == nil {
		return result, nil
	}
	log.Printf("[CRYPTO] CoinGecko failed (%v) — falling back to CoinPaprika", err)
	paprikaResult, paprikaErr := fetchCryptoFromCoinPaprika(coin, client)
	if paprikaErr != nil {
		return "", fmt.Errorf("%v; CoinPaprika fallback also failed: %w", err, paprikaErr)
	}
	return paprikaResult, nil
}

func fetchCryptoFromCoinGecko(coin string, client *http.Client) (string, error) {
	url := fmt.Sprintf("%s/simple/price?ids=%s&vs_currencies=usd,inr&include_24hr_change=true&include_market_cap=true", coingeckoBaseURL, strings.ToLower(coin))
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("CoinGecko request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCryptoBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read CoinGecko response: %w", err)
	}

	log.Printf("[CRYPTO] CoinGecko status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CoinGecko API returned HTTP %d: %s", resp.StatusCode, shortCryptoBody(body))
	}

	var data map[string]map[string]float64
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("invalid CoinGecko JSON response: %v (body: %s)", err, shortCryptoBody(body))
	}
	d, ok := data[strings.ToLower(coin)]
	if !ok {
		return "", fmt.Errorf("coin '%s' not found. Try: bitcoin, ethereum, solana, dogecoin, cardano", coin)
	}
	dir := "up"
	if d["usd_24h_change"] < 0 {
		dir = "down"
	}
	return fmt.Sprintf("%s Price:\n  USD: $%.2f\n  INR: Rs.%.2f\n  24h: %.2f%% (%s)\n  Market Cap: $%.0f", strings.Title(coin), d["usd"], d["inr"], d["usd_24h_change"], dir, d["usd_market_cap"]), nil
}

func fetchCryptoFromCoinPaprika(coin string, client *http.Client) (string, error) {
	coinID, ok := paprikaCoinIDs[strings.ToLower(coin)]
	if !ok {
		coinID = strings.ToLower(coin) + "-" + strings.ToLower(coin)
	}
	url := fmt.Sprintf("%s/tickers/%s?quotes=USD,INR", coinPaprikaBaseURL, coinID)
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("CoinPaprika request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCryptoBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read CoinPaprika response: %w", err)
	}

	log.Printf("[CRYPTO] CoinPaprika status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CoinPaprika API returned HTTP %d: %s", resp.StatusCode, shortCryptoBody(body))
	}

	var data struct {
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
		Quotes map[string]struct {
			Price     float64 `json:"price"`
			MarketCap float64 `json:"market_cap"`
			Change24h float64 `json:"percent_change_24h"`
		} `json:"quotes"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("invalid CoinPaprika JSON response: %v (body: %s)", err, shortCryptoBody(body))
	}
	usd := data.Quotes["USD"]
	inr := data.Quotes["INR"]
	if usd.Price == 0 {
		return "", fmt.Errorf("coin '%s' not found on CoinPaprika", coin)
	}
	dir := "up"
	if usd.Change24h < 0 {
		dir = "down"
	}
	inrLine := ""
	if inr.Price > 0 {
		inrLine = fmt.Sprintf("  INR: Rs.%.2f\n", inr.Price)
	}
	name := data.Name
	if name == "" {
		name = strings.Title(coin)
	}
	return fmt.Sprintf("%s Price (CoinPaprika):\n  USD: $%.2f\n%s  24h: %.2f%% (%s)\n  Market Cap: $%.0f", name, usd.Price, inrLine, usd.Change24h, dir, usd.MarketCap), nil
}

func fetchTop() (string, error) {
	return fetchTopWithClient(cryptoClient)
}

func fetchTopWithClient(client *http.Client) (string, error) {
	result, err := fetchTopFromCoinGecko(client)
	if err == nil {
		return result, nil
	}
	log.Printf("[CRYPTO] CoinGecko failed (%v) — falling back to CoinPaprika", err)
	paprikaResult, paprikaErr := fetchTopFromCoinPaprika(client)
	if paprikaErr != nil {
		return "", fmt.Errorf("%v; CoinPaprika fallback also failed: %w", err, paprikaErr)
	}
	return paprikaResult, nil
}

func fetchTopFromCoinGecko(client *http.Client) (string, error) {
	resp, err := client.Get(coingeckoBaseURL + "/coins/markets?vs_currency=usd&order=market_cap_desc&per_page=10&page=1")
	if err != nil {
		return "", fmt.Errorf("CoinGecko request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCryptoBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read CoinGecko response: %w", err)
	}

	log.Printf("[CRYPTO] CoinGecko status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CoinGecko API returned HTTP %d: %s", resp.StatusCode, shortCryptoBody(body))
	}

	var coins []struct {
		Name      string  `json:"name"`
		Symbol    string  `json:"symbol"`
		Price     float64 `json:"current_price"`
		Change24h float64 `json:"price_change_percentage_24h"`
	}
	if err := json.Unmarshal(body, &coins); err != nil {
		return "", fmt.Errorf("invalid CoinGecko JSON response: %v (body: %s)", err, shortCryptoBody(body))
	}
	var lines []string
	for i, c := range coins {
		d := "+"
		if c.Change24h < 0 { d = "" }
		lines = append(lines, fmt.Sprintf("  %d. %s (%s) — $%.2f (%s%.1f%%)", i+1, c.Name, strings.ToUpper(c.Symbol), c.Price, d, c.Change24h))
	}
	return "Top 10 Cryptocurrencies:\n" + strings.Join(lines, "\n"), nil
}

func fetchTopFromCoinPaprika(client *http.Client) (string, error) {
	resp, err := client.Get(coinPaprikaBaseURL + "/tickers?quotes=USD&limit=10")
	if err != nil {
		return "", fmt.Errorf("CoinPaprika request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCryptoBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to read CoinPaprika response: %w", err)
	}

	log.Printf("[CRYPTO] CoinPaprika status=%d content-type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("CoinPaprika API returned HTTP %d: %s", resp.StatusCode, shortCryptoBody(body))
	}

	var coins []struct {
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
		Quotes map[string]struct {
			Price     float64 `json:"price"`
			Change24h float64 `json:"percent_change_24h"`
		} `json:"quotes"`
	}
	if err := json.Unmarshal(body, &coins); err != nil {
		return "", fmt.Errorf("invalid CoinPaprika JSON response: %v (body: %s)", err, shortCryptoBody(body))
	}

	var lines []string
	for i, c := range coins {
		d := "+"
		if c.Quotes["USD"].Change24h < 0 {
			d = ""
		}
		lines = append(lines, fmt.Sprintf("  %d. %s (%s) — $%.2f (%s%.1f%%)", i+1, c.Name, strings.ToUpper(c.Symbol), c.Quotes["USD"].Price, d, c.Quotes["USD"].Change24h))
	}
	return "Top 10 Cryptocurrencies (CoinPaprika):\n" + strings.Join(lines, "\n"), nil
}
