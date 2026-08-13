package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// callCounts tracks how many requests each provider's path received.
type callCounts struct {
	gecko, paprika, binance atomic.Int32
}

// fakeCoinGecko spins up an httptest server and points all three provider
// base URLs at it so fetchCrypto/fetchTop never hit real APIs in tests. The
// handlers route on r.URL.Path: /simple/price and /coins/markets are
// CoinGecko, /tickers is CoinPaprika, /ticker/24hr is Binance.
func fakeCoinGecko(t *testing.T, counts *callCounts, gecko, paprika, binance http.HandlerFunc) *http.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/ticker/24hr"):
			counts.binance.Add(1)
			binance(w, r)
		case strings.Contains(r.URL.Path, "/tickers"):
			counts.paprika.Add(1)
			paprika(w, r)
		default:
			counts.gecko.Add(1)
			gecko(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oldGecko := coingeckoBaseURL
	oldPaprika := coinPaprikaBaseURL
	oldBinance := binanceBaseURL
	coingeckoBaseURL = srv.URL
	coinPaprikaBaseURL = srv.URL
	binanceBaseURL = srv.URL
	t.Cleanup(func() {
		coingeckoBaseURL = oldGecko
		coinPaprikaBaseURL = oldPaprika
		binanceBaseURL = oldBinance
	})
	return srv.Client()
}

func paprikaJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"name":"Bitcoin","symbol":"BTC","quotes":{"USD":{"price":63400,"market_cap":1270000000000,"percent_change_24h":0.1},"INR":{"price":6050000,"market_cap":121000000000000,"percent_change_24h":0.1}}}`))
}

func paprikaTopJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`[
		{"name":"Bitcoin","symbol":"BTC","quotes":{"USD":{"price":63400,"percent_change_24h":0.1}}},
		{"name":"Ethereum","symbol":"ETH","quotes":{"USD":{"price":3500,"percent_change_24h":-0.4}}}
	]`))
}

func binanceJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"symbol":"BTCUSDT","lastPrice":"63400.00","priceChangePercent":"0.10","quoteVolume":"16000000000"}`))
}

func binanceTopJSON(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`[
		{"symbol":"BTCUSDT","lastPrice":"63400.00","priceChangePercent":"0.10","quoteVolume":"16000000000"},
		{"symbol":"ETHUSDT","lastPrice":"3500.00","priceChangePercent":"-0.40","quoteVolume":"8000000000"},
		{"symbol":"XRPUSDT","lastPrice":"0.50","priceChangePercent":"1.10","quoteVolume":"1000000"}
	]`))
}

// 1. Valid 200 JSON response from CoinGecko renders the price and never
// touches the fallback providers.
func TestFetchCrypto_Valid200(t *testing.T) {
	var counts callCounts
	client := fakeCoinGecko(t, &counts, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":97000.5,"inr":8100000,"usd_24h_change":2.5,"usd_market_cap":1900000000000}}`))
	}, paprikaJSON, binanceJSON)

	result, err := fetchCryptoWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Bitcoin Price", "USD: $97000.50", "INR: Rs.8100000.00", "24h: 2.50% (up)", "Market Cap: $1900000000000"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
	if counts.paprika.Load() != 0 || counts.binance.Load() != 0 {
		t.Errorf("fallbacks called on success: paprika=%d binance=%d", counts.paprika.Load(), counts.binance.Load())
	}
}

// 6. Successful Bitcoin response (explicit coverage of the required case).
func TestFetchCrypto_BitcoinSuccess(t *testing.T) {
	client := fakeCoinGecko(t, &callCounts{}, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/simple/price") {
			t.Errorf("expected /simple/price path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":50000,"inr":4200000,"usd_24h_change":-1.2,"usd_market_cap":950000000000}}`))
	}, paprikaJSON, binanceJSON)

	result, err := fetchCryptoWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "USD: $50000.00") || !strings.Contains(result, "24h: -1.20% (down)") {
		t.Errorf("unexpected result:\n%s", result)
	}
}

// 2. HTTP 429 surfaces the status code instead of a vague parse error.
func TestFetchCrypto_HTTP429(t *testing.T) {
	rateLimited := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"status":{"error_code":429,"error_message":"Too many requests"}}`))
	}
	client := fakeCoinGecko(t, &callCounts{}, rateLimited, rateLimited, rateLimited)

	_, err := fetchCryptoWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	for _, want := range []string{"HTTP 429", "CoinPaprika fallback also failed", "Binance fallback also failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q, got: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "parse error") {
		t.Errorf("vague parse error leaked through: %v", err)
	}
}

// 3. HTTP 500 surfaces the status code.
func TestFetchCrypto_HTTP500(t *testing.T) {
	serverError := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
	client := fakeCoinGecko(t, &callCounts{}, serverError, serverError, serverError)

	_, err := fetchCryptoWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error should mention HTTP 500, got: %v", err)
	}
}

// 4. Malformed JSON yields a diagnostic parse error.
func TestFetchCrypto_MalformedJSON(t *testing.T) {
	html := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`<html>upstream gateway error, please retry</html>`))
	}
	client := fakeCoinGecko(t, &callCounts{}, html, html, html)

	_, err := fetchCryptoWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "invalid CoinGecko JSON response") {
		t.Errorf("expected diagnostic parse error, got: %v", err)
	}
}

// 5. Missing requested coin in a valid response is reported clearly.
func TestFetchCrypto_CoinMissing(t *testing.T) {
	notFound := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`))
	}
	client := fakeCoinGecko(t, &callCounts{}, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}, notFound, notFound)

	_, err := fetchCryptoWithClient("notacoin", client)
	if err == nil {
		t.Fatal("expected error for missing coin")
	}
	if !strings.Contains(err.Error(), "coin 'notacoin' not found") {
		t.Errorf("expected coin-not-found error, got: %v", err)
	}
}

// 7. CoinGecko failure falls back to CoinPaprika.
func TestFetchCrypto_FallsBackToCoinPaprika(t *testing.T) {
	var counts callCounts
	rateLimited := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}
	client := fakeCoinGecko(t, &counts, rateLimited, paprikaJSON, binanceJSON)

	result, err := fetchCryptoWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("expected CoinPaprika fallback to succeed, got: %v", err)
	}
	if !strings.Contains(result, "Bitcoin Price (CoinPaprika)") || !strings.Contains(result, "INR: Rs.6050000.00") {
		t.Errorf("expected CoinPaprika-sourced result, got:\n%s", result)
	}
	if counts.binance.Load() != 0 {
		t.Errorf("Binance called %d times though CoinPaprika succeeded", counts.binance.Load())
	}
}

// 8. CoinGecko + CoinPaprika failure falls back to Binance (real data).
func TestFetchCrypto_FallsBackToBinance(t *testing.T) {
	rateLimited := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}
	client := fakeCoinGecko(t, &callCounts{}, rateLimited, rateLimited, binanceJSON)

	result, err := fetchCryptoWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("expected Binance fallback to succeed, got: %v", err)
	}
	if !strings.Contains(result, "Bitcoin Price (Binance)") {
		t.Errorf("expected Binance-sourced result, got:\n%s", result)
	}
	if !strings.Contains(result, "USD: $63400.00") || !strings.Contains(result, "24h Volume") {
		t.Errorf("unexpected Binance fallback result:\n%s", result)
	}
}

// 9. fetchTop renders a valid 200 response and never touches fallbacks.
func TestFetchTop_Valid200(t *testing.T) {
	var counts callCounts
	client := fakeCoinGecko(t, &counts, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/coins/markets") {
			t.Errorf("expected /coins/markets path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"Bitcoin","symbol":"btc","current_price":97000,"price_change_percentage_24h":2.5},
			{"name":"Ethereum","symbol":"eth","current_price":3500,"price_change_percentage_24h":-0.4}
		]`))
	}, paprikaTopJSON, binanceTopJSON)

	result, err := fetchTopWithClient(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Top 10", "Bitcoin (BTC)", "Ethereum (ETH)"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
	if counts.paprika.Load() != 0 || counts.binance.Load() != 0 {
		t.Errorf("fallbacks called on success: paprika=%d binance=%d", counts.paprika.Load(), counts.binance.Load())
	}
}

// 10. fetchTop surfaces HTTP errors from every provider.
func TestFetchTop_HTTP429(t *testing.T) {
	rateLimited := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}
	client := fakeCoinGecko(t, &callCounts{}, rateLimited, rateLimited, rateLimited)

	_, err := fetchTopWithClient(client)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error should mention HTTP 429, got: %v", err)
	}
}

// 11. fetchTop falls back to CoinPaprika on CoinGecko failure.
func TestFetchTop_FallsBackToCoinPaprika(t *testing.T) {
	boom := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	client := fakeCoinGecko(t, &callCounts{}, boom, paprikaTopJSON, binanceTopJSON)

	result, err := fetchTopWithClient(client)
	if err != nil {
		t.Fatalf("expected CoinPaprika fallback to succeed, got: %v", err)
	}
	if !strings.Contains(result, "Top 10") || !strings.Contains(result, "(CoinPaprika)") {
		t.Errorf("expected CoinPaprika-sourced top list, got:\n%s", result)
	}
}

// 12. fetchTop falls back to Binance when both CoinGecko and CoinPaprika fail.
func TestFetchTop_FallsBackToBinance(t *testing.T) {
	boom := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}
	client := fakeCoinGecko(t, &callCounts{}, boom, boom, binanceTopJSON)

	result, err := fetchTopWithClient(client)
	if err != nil {
		t.Fatalf("expected Binance fallback to succeed, got: %v", err)
	}
	if !strings.Contains(result, "Top 10") || !strings.Contains(result, "(Binance)") {
		t.Errorf("expected Binance-sourced top list, got:\n%s", result)
	}
	if !strings.Contains(result, "BTC") || !strings.Contains(result, "ETH") {
		t.Errorf("unexpected Binance top list:\n%s", result)
	}
}
