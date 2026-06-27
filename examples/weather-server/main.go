// Weather MCP Server — uses the REAL wttr.in weather API.
//
// wttr.in is a free weather service. No API key needed!
// Example: https://wttr.in/London?format=j1 returns real weather data.
//
// This server exposes 2 tools:
//   - get_weather: Get real current weather for any city
//   - get_forecast: Get real 3-day forecast for any city
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

// --- MCP Protocol Types ---

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

// --- Tool Definitions ---

var tools = []map[string]any{
	{
		"name":        "get_weather",
		"description": "Get the REAL current weather for any city in the world",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name (e.g., 'London', 'New York', 'Mumbai')",
				},
			},
			"required": []string{"city"},
		},
	},
	{
		"name":        "get_forecast",
		"description": "Get a real 3-day weather forecast for any city",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{
					"type":        "string",
					"description": "City name (e.g., 'London', 'New York', 'Mumbai')",
				},
			},
			"required": []string{"city"},
		},
	},
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", handleMCPMessage)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	log.Println("Weather MCP Server (REAL API) running on http://localhost:3001")
	log.Fatal(http.ListenAndServe(":3001", mux))
}

func handleMCPMessage(w http.ResponseWriter, r *http.Request) {
	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(w, req.ID, -32700, "Parse error")
		return
	}

	log.Printf("Received: method=%s", req.Method)

	switch req.Method {
	case "initialize":
		sendResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "weather-server", "version": "2.0.0"},
		})

	case "tools/list":
		sendResult(w, req.ID, map[string]any{"tools": tools})

	case "tools/call":
		handleToolCall(w, req)

	default:
		sendError(w, req.ID, -32601, "Method not found: "+req.Method)
	}
}

func handleToolCall(w http.ResponseWriter, req MCPRequest) {
	toolName, _ := req.Params["name"].(string)
	arguments, _ := req.Params["arguments"].(map[string]any)

	switch toolName {
	case "get_weather":
		city, _ := arguments["city"].(string)
		if city == "" {
			sendToolResult(w, req.ID, "Error: 'city' parameter is required", true)
			return
		}
		result, err := fetchCurrentWeather(city)
		if err != nil {
			sendToolResult(w, req.ID, "Error fetching weather: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	case "get_forecast":
		city, _ := arguments["city"].(string)
		if city == "" {
			sendToolResult(w, req.ID, "Error: 'city' parameter is required", true)
			return
		}
		result, err := fetchForecast(city)
		if err != nil {
			sendToolResult(w, req.ID, "Error fetching forecast: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	default:
		sendToolResult(w, req.ID, "Unknown tool: "+toolName, true)
	}
}

// --- Real API Calls to wttr.in ---

// wttrResponse represents the JSON response from wttr.in
type wttrResponse struct {
	CurrentCondition []struct {
		TempC      string `json:"temp_C"`
		TempF      string `json:"temp_F"`
		Humidity   string `json:"humidity"`
		WindspeedK string `json:"windspeedKmph"`
		Desc       []struct {
			Value string `json:"value"`
		} `json:"weatherDesc"`
		FeelsLikeC string `json:"FeelsLikeC"`
	} `json:"current_condition"`
	Weather []struct {
		Date       string `json:"date"`
		MaxTempC   string `json:"maxtempC"`
		MinTempC   string `json:"mintempC"`
		Hourly     []struct {
			TempC string `json:"tempC"`
			Desc  []struct {
				Value string `json:"value"`
			} `json:"weatherDesc"`
		} `json:"hourly"`
	} `json:"weather"`
}

func fetchCurrentWeather(city string) (string, error) {
	// Call the real wttr.in API
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.QueryEscape(city))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("failed to reach weather API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var data wttrResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse weather data: %w", err)
	}

	if len(data.CurrentCondition) == 0 {
		return "", fmt.Errorf("no weather data found for '%s'", city)
	}

	c := data.CurrentCondition[0]
	desc := "Unknown"
	if len(c.Desc) > 0 {
		desc = c.Desc[0].Value
	}

	result := fmt.Sprintf(
		"Current weather in %s:\n"+
			"  Temperature: %s°C (%s°F)\n"+
			"  Feels like: %s°C\n"+
			"  Condition: %s\n"+
			"  Humidity: %s%%\n"+
			"  Wind: %s km/h",
		city, c.TempC, c.TempF, c.FeelsLikeC, desc, c.Humidity, c.WindspeedK,
	)

	return result, nil
}

func fetchForecast(city string) (string, error) {
	apiURL := fmt.Sprintf("https://wttr.in/%s?format=j1", url.QueryEscape(city))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("failed to reach weather API: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var data wttrResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("failed to parse weather data: %w", err)
	}

	if len(data.Weather) == 0 {
		return "", fmt.Errorf("no forecast data found for '%s'", city)
	}

	result := fmt.Sprintf("3-Day Forecast for %s:\n", city)
	for _, day := range data.Weather {
		desc := "Unknown"
		if len(day.Hourly) > 4 && len(day.Hourly[4].Desc) > 0 {
			desc = day.Hourly[4].Desc[0].Value
		}
		result += fmt.Sprintf("  %s: %s°C to %s°C — %s\n", day.Date, day.MinTempC, day.MaxTempC, desc)
	}

	return result, nil
}

// --- Response Helpers ---

func sendResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func sendError(w http.ResponseWriter, id any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(MCPResponse{JSONRPC: "2.0", ID: id, Error: map[string]any{"code": code, "message": msg}})
}

func sendToolResult(w http.ResponseWriter, id any, text string, isError bool) {
	sendResult(w, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	})
}
