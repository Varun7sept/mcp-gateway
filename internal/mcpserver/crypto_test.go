package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeCoinGecko spins up an httptest server and points coingeckoBaseURL and
// coinPaprikaBaseURL at it so fetchCrypto/fetchTop never hit the real APIs in
// tests. The handler should route on r.URL.Path: /simple/price and
// /coins/markets are CoinGecko, /tickers is CoinPaprika.
func fakeCoinGecko(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	oldGecko := coingeckoBaseURL
	oldPaprika := coinPaprikaBaseURL
	coingeckoBaseURL = srv.URL
	coinPaprikaBaseURL = srv.URL
	t.Cleanup(func() {
		coingeckoBaseURL = oldGecko
		coinPaprikaBaseURL = oldPaprika
	})
	return srv.Client()
}

// paprikaCallCount returns a handler that serves the given responses and
// counts how many times the CoinPaprika /tickers endpoint was hit.
func paprikaCallCount(paprikaCalls *atomic.Int32, gecko http.HandlerFunc, paprika http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/tickers") {
			paprikaCalls.Add(1)
			paprika(w, r)
			return
		}
		gecko(w, r)
	}
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

// 1. Valid 200 JSON response from CoinGecko renders the price and never
// touches CoinPaprika.
func TestFetchCrypto_Valid200(t *testing.T) {
	var paprikaCalls atomic.Int32
	client := fakeCoinGecko(t, paprikaCallCount(&paprikaCalls, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":97000.5,"inr":8100000,"usd_24h_change":2.5,"usd_market_cap":1900000000000}}`))
	}, paprikaJSON))

	result, err := fetchCryptoWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Bitcoin Price", "USD: $97000.50", "INR: Rs.8100000.00", "24h: 2.50% (up)", "Market Cap: $1900000000000"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
	if paprikaCalls.Load() != 0 {
		t.Errorf("CoinPaprika called %d times on a successful CoinGecko response", paprikaCalls.Load())
	}
}

// 6. Successful Bitcoin response (explicit coverage of the required case).
func TestFetchCrypto_BitcoinSuccess(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/simple/price") {
			t.Errorf("expected /simple/price path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":50000,"inr":4200000,"usd_24h_change":-1.2,"usd_market_cap":950000000000}}`))
	})

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
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, rateLimited, rateLimited))

	_, err := fetchCryptoWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error should mention HTTP 429, got: %v", err)
	}
	if strings.Contains(err.Error(), "parse error") {
		t.Errorf("vague parse error leaked through: %v", err)
	}
	if !strings.Contains(err.Error(), "CoinPaprika fallback also failed") {
		t.Errorf("expected CoinPaprika fallback attempt to be reported, got: %v", err)
	}
}

// 3. HTTP 500 surfaces the status code.
func TestFetchCrypto_HTTP500(t *testing.T) {
	serverError := func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, serverError, serverError))

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
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, html, html))

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
		if strings.Contains(r.URL.Path, "/tickers") {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"id not found"}`))
			return
		}
		w.Write([]byte(`{}`))
	}
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, notFound, notFound))

	_, err := fetchCryptoWithClient("notacoin", client)
	if err == nil {
		t.Fatal("expected error for missing coin")
	}
	if !strings.Contains(err.Error(), "coin 'notacoin' not found") {
		t.Errorf("expected coin-not-found error, got: %v", err)
	}
}

// 9. CoinGecko failure automatically falls back to CoinPaprika (real data).
func TestFetchCrypto_FallsBackToCoinPaprika(t *testing.T) {
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}, paprikaJSON))

	result, err := fetchCryptoWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("expected CoinPaprika fallback to succeed, got: %v", err)
	}
	if !strings.Contains(result, "Bitcoin Price (CoinPaprika)") {
		t.Errorf("expected CoinPaprika-sourced result, got:\n%s", result)
	}
	if !strings.Contains(result, "USD: $63400.00") || !strings.Contains(result, "INR: Rs.6050000.00") {
		t.Errorf("unexpected fallback result:\n%s", result)
	}
}

// 7. fetchTop renders a valid 200 response and never touches CoinPaprika.
func TestFetchTop_Valid200(t *testing.T) {
	var paprikaCalls atomic.Int32
	client := fakeCoinGecko(t, paprikaCallCount(&paprikaCalls, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/coins/markets") {
			t.Errorf("expected /coins/markets path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"Bitcoin","symbol":"btc","current_price":97000,"price_change_percentage_24h":2.5},
			{"name":"Ethereum","symbol":"eth","current_price":3500,"price_change_percentage_24h":-0.4}
		]`))
	}, paprikaTopJSON))

	result, err := fetchTopWithClient(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Top 10", "Bitcoin (BTC)", "Ethereum (ETH)"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
	if paprikaCalls.Load() != 0 {
		t.Errorf("CoinPaprika called %d times on a successful CoinGecko response", paprikaCalls.Load())
	}
}

// 8. fetchTop surfaces HTTP errors too.
func TestFetchTop_HTTP429(t *testing.T) {
	rateLimited := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, rateLimited, rateLimited))

	_, err := fetchTopWithClient(client)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error should mention HTTP 429, got: %v", err)
	}
}

// 10. fetchTop falls back to CoinPaprika on CoinGecko failure.
func TestFetchTop_FallsBackToCoinPaprika(t *testing.T) {
	client := fakeCoinGecko(t, paprikaCallCount(&atomic.Int32{}, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}, paprikaTopJSON))

	result, err := fetchTopWithClient(client)
	if err != nil {
		t.Fatalf("expected CoinPaprika fallback to succeed, got: %v", err)
	}
	if !strings.Contains(result, "Top 10") || !strings.Contains(result, "(CoinPaprika)") {
		t.Errorf("expected CoinPaprika-sourced top list, got:\n%s", result)
	}
}
