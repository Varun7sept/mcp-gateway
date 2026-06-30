package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

var weatherClient = &http.Client{Timeout: 10 * time.Second}

var weatherTools = []map[string]any{
	{"name": "get_weather", "description": "Get the current real-time weather for any city worldwide — temperature, humidity, wind speed, and conditions", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string", "description": "City name, e.g. London, Mumbai, New York"}}, "required": []string{"city"}}},
	{"name": "get_forecast", "description": "Get a 3-day weather forecast for any city — daily high/low, conditions, and precipitation", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string", "description": "City name, e.g. London, Mumbai, New York"}}, "required": []string{"city"}}},
}

type wttrResponse struct {
	CurrentCondition []struct {
		TempC string `json:"temp_C"`; TempF string `json:"temp_F"`; Humidity string `json:"humidity"`
		WindspeedK string `json:"windspeedKmph"`; FeelsLikeC string `json:"FeelsLikeC"`
		Desc []struct{ Value string `json:"value"` } `json:"weatherDesc"`
	} `json:"current_condition"`
	Weather []struct {
		Date string `json:"date"`; MaxTempC string `json:"maxtempC"`; MinTempC string `json:"mintempC"`
		Hourly []struct { TempC string `json:"tempC"`; Desc []struct{ Value string `json:"value"` } `json:"weatherDesc"` } `json:"hourly"`
	} `json:"weather"`
}

func StartWeather(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { sendError(w, req.ID, -32700, "Parse error"); return }
		switch req.Method {
		case "initialize": sendResult(w, req.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "weather-server", "version": "2.0.0"}})
		case "tools/list": sendResult(w, req.ID, map[string]any{"tools": weatherTools})
		case "tools/call": handleWeatherTool(w, req)
		default: sendError(w, req.ID, -32601, "Method not found")
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) })
	log.Printf("Weather MCP Server running on http://localhost%s", port)
	return http.ListenAndServe(port, mux)
}

func handleWeatherTool(w http.ResponseWriter, req MCPRequest) {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	switch name {
	case "get_weather":
		city, _ := args["city"].(string)
		if city == "" { sendToolResult(w, req.ID, "Error: 'city' is required", true); return }
		r, err := fetchWeather(city)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	case "get_forecast":
		city, _ := args["city"].(string)
		if city == "" { sendToolResult(w, req.ID, "Error: 'city' is required", true); return }
		r, err := fetchForecast(city)
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		sendToolResult(w, req.ID, r, false)
	default: sendToolResult(w, req.ID, "Unknown tool: "+name, true)
	}
}

func fetchWeather(city string) (string, error) {
	resp, err := weatherClient.Get(fmt.Sprintf("https://wttr.in/%s?format=j1", url.QueryEscape(city)))
	if err != nil { return "", err }
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil { return "", fmt.Errorf("read error: %w", err) }
	var data wttrResponse
	if err := json.Unmarshal(body, &data); err != nil { return "", fmt.Errorf("parse error") }
	if len(data.CurrentCondition) == 0 { return "", fmt.Errorf("no data for '%s'", city) }
	c := data.CurrentCondition[0]
	desc := "Unknown"
	if len(c.Desc) > 0 { desc = c.Desc[0].Value }
	return fmt.Sprintf("Current weather in %s:\n  Temp: %s°C (%s°F)\n  Feels like: %s°C\n  Condition: %s\n  Humidity: %s%%\n  Wind: %s km/h", city, c.TempC, c.TempF, c.FeelsLikeC, desc, c.Humidity, c.WindspeedK), nil
}

func fetchForecast(city string) (string, error) {
	resp, err := weatherClient.Get(fmt.Sprintf("https://wttr.in/%s?format=j1", url.QueryEscape(city)))
	if err != nil { return "", err }
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil { return "", fmt.Errorf("read error: %w", err) }
	var data wttrResponse
	if err := json.Unmarshal(body, &data); err != nil { return "", fmt.Errorf("parse error") }
	if len(data.Weather) == 0 { return "", fmt.Errorf("no forecast for '%s'", city) }
	result := fmt.Sprintf("3-Day Forecast for %s:\n", city)
	for _, day := range data.Weather {
		desc := "Unknown"
		if len(day.Hourly) > 4 && len(day.Hourly[4].Desc) > 0 { desc = day.Hourly[4].Desc[0].Value }
		result += fmt.Sprintf("  %s: %s°C to %s°C — %s\n", day.Date, day.MinTempC, day.MaxTempC, desc)
	}
	return result, nil
}
