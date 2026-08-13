package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeCoinGecko spins up an httptest server and points coingeckoBaseURL at it.
// The handler can inspect the request path and return scripted responses.
func fakeCoinGecko(t *testing.T, handler http.HandlerFunc) *http.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	oldBase := coingeckoBaseURL
	coingeckoBaseURL = srv.URL
	t.Cleanup(func() { coingeckoBaseURL = oldBase })
	return srv.Client()
}

// 1. Valid 200 JSON response renders the price.
func TestFetchCryptoPrice_Valid200(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":97000.5,"inr":8100000,"usd_24h_change":2.5,"usd_market_cap":1900000000000}}`))
	})

	result, err := fetchCryptoPriceWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Bitcoin Price", "USD: $97000.50", "INR: Rs.8100000.00", "24h Change: 2.50% (up)", "Market Cap: $1900000000000"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

// 6. Successful Bitcoin response (explicit coverage of the required case).
func TestFetchCryptoPrice_BitcoinSuccess(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/simple/price") {
			t.Errorf("expected /simple/price path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"bitcoin":{"usd":50000,"inr":4200000,"usd_24h_change":-1.2,"usd_market_cap":950000000000}}`))
	})

	result, err := fetchCryptoPriceWithClient("bitcoin", client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "USD: $50000.00") || !strings.Contains(result, "24h Change: -1.20% (down)") {
		t.Errorf("unexpected result:\n%s", result)
	}
}

// 2. HTTP 429 surfaces the status code instead of a vague parse error.
func TestFetchCryptoPrice_HTTP429(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"status":{"error_code":429,"error_message":"Too many requests"}}`))
	})

	_, err := fetchCryptoPriceWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error should mention HTTP 429, got: %v", err)
	}
	if strings.Contains(err.Error(), "parse error") || strings.Contains(err.Error(), "failed to parse response") {
		t.Errorf("vague parse error leaked through: %v", err)
	}
}

// 3. HTTP 500 surfaces the status code.
func TestFetchCryptoPrice_HTTP500(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	})

	_, err := fetchCryptoPriceWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error should mention HTTP 500, got: %v", err)
	}
}

// 4. Malformed JSON yields a diagnostic parse error.
func TestFetchCryptoPrice_MalformedJSON(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(`<html>upstream gateway error, please retry</html>`))
	})

	_, err := fetchCryptoPriceWithClient("bitcoin", client)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "invalid CoinGecko JSON response") {
		t.Errorf("expected diagnostic parse error, got: %v", err)
	}
}

// 5. Missing requested coin in a valid response is reported clearly.
func TestFetchCryptoPrice_CoinMissing(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	})

	_, err := fetchCryptoPriceWithClient("notacoin", client)
	if err == nil {
		t.Fatal("expected error for missing coin")
	}
	if !strings.Contains(err.Error(), "coin 'notacoin' not found") {
		t.Errorf("expected coin-not-found error, got: %v", err)
	}
}

// 7. fetchTopCryptos renders a valid 200 response.
func TestFetchTopCryptos_Valid200(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/coins/markets") {
			t.Errorf("expected /coins/markets path, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"name":"Bitcoin","symbol":"btc","current_price":97000,"price_change_percentage_24h":2.5,"market_cap":1900000000000},
			{"name":"Ethereum","symbol":"eth","current_price":3500,"price_change_percentage_24h":-0.4,"market_cap":420000000000}
		]`))
	})

	result, err := fetchTopCryptosWithClient(client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"Top 10", "Bitcoin (BTC)", "Ethereum (ETH)"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

// 8. fetchTopCryptos surfaces HTTP errors too.
func TestFetchTopCryptos_HTTP429(t *testing.T) {
	client := fakeCoinGecko(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	})

	_, err := fetchTopCryptosWithClient(client)
	if err == nil {
		t.Fatal("expected error for HTTP 429")
	}
	if !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error should mention HTTP 429, got: %v", err)
	}
}
