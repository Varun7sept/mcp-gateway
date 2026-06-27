// GitHub MCP Server — uses the REAL GitHub API.
//
// Uses GitHub's public API (no token needed for public data).
// With a token, you can access private repos too.
//
// This server exposes 3 tools:
//   - get_user: Get real GitHub user profile info
//   - list_repos: List real repositories for any user
//   - get_repo: Get details about a specific repository
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
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

// GitHub API token (optional — set GITHUB_TOKEN env var for higher rate limits)
var githubToken = os.Getenv("GITHUB_TOKEN")

// --- Tool Definitions ---

var tools = []map[string]any{
	{
		"name":        "get_user",
		"description": "Get a real GitHub user's profile (name, bio, followers, repos count)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"username": map[string]any{
					"type":        "string",
					"description": "GitHub username (e.g., 'torvalds', 'octocat')",
				},
			},
			"required": []string{"username"},
		},
	},
	{
		"name":        "list_repos",
		"description": "List real public repositories for a GitHub user",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"username": map[string]any{
					"type":        "string",
					"description": "GitHub username",
				},
				"sort": map[string]any{
					"type":        "string",
					"description": "Sort by: 'stars', 'updated', 'name' (default: stars)",
				},
			},
			"required": []string{"username"},
		},
	},
	{
		"name":        "get_repo",
		"description": "Get details about a specific GitHub repository",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{
					"type":        "string",
					"description": "Repository owner (e.g., 'facebook')",
				},
				"repo": map[string]any{
					"type":        "string",
					"description": "Repository name (e.g., 'react')",
				},
			},
			"required": []string{"owner", "repo"},
		},
	},
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", handleMCPMessage)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	if githubToken != "" {
		log.Println("GitHub token found — higher rate limits enabled")
	} else {
		log.Println("No GITHUB_TOKEN set — using public rate limits (60 req/hour)")
	}

	log.Println("GitHub MCP Server (REAL API) running on http://localhost:3003")
	log.Fatal(http.ListenAndServe(":3003", mux))
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
			"serverInfo":      map[string]any{"name": "github-server", "version": "1.0.0"},
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
	case "get_user":
		username, _ := arguments["username"].(string)
		if username == "" {
			sendToolResult(w, req.ID, "Error: 'username' is required", true)
			return
		}
		result, err := fetchGitHubUser(username)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	case "list_repos":
		username, _ := arguments["username"].(string)
		sort, _ := arguments["sort"].(string)
		if username == "" {
			sendToolResult(w, req.ID, "Error: 'username' is required", true)
			return
		}
		if sort == "" {
			sort = "stars"
		}
		result, err := fetchUserRepos(username, sort)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	case "get_repo":
		owner, _ := arguments["owner"].(string)
		repo, _ := arguments["repo"].(string)
		if owner == "" || repo == "" {
			sendToolResult(w, req.ID, "Error: 'owner' and 'repo' are required", true)
			return
		}
		result, err := fetchRepoDetails(owner, repo)
		if err != nil {
			sendToolResult(w, req.ID, "Error: "+err.Error(), true)
			return
		}
		sendToolResult(w, req.ID, result, false)

	default:
		sendToolResult(w, req.ID, "Unknown tool: "+toolName, true)
	}
}

// --- Real GitHub API Calls ---

func githubAPI(path string) ([]byte, error) {
	url := "https://api.github.com" + path

	client := &http.Client{Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "mcp-gateway-github-server")

	if githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+githubToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("not found (404)")
	}
	if resp.StatusCode == 403 {
		return nil, fmt.Errorf("rate limited — set GITHUB_TOKEN env var for more requests")
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	return body, nil
}

func fetchGitHubUser(username string) (string, error) {
	body, err := githubAPI("/users/" + username)
	if err != nil {
		return "", err
	}

	var user struct {
		Login     string `json:"login"`
		Name      string `json:"name"`
		Bio       string `json:"bio"`
		Location  string `json:"location"`
		Followers int    `json:"followers"`
		Following int    `json:"following"`
		PublicRepos int  `json:"public_repos"`
		CreatedAt string `json:"created_at"`
	}

	if err := json.Unmarshal(body, &user); err != nil {
		return "", err
	}

	result := fmt.Sprintf(
		"GitHub User: %s (@%s)\n"+
			"  Bio: %s\n"+
			"  Location: %s\n"+
			"  Public repos: %d\n"+
			"  Followers: %d | Following: %d\n"+
			"  Joined: %s",
		user.Name, user.Login, user.Bio, user.Location,
		user.PublicRepos, user.Followers, user.Following, user.CreatedAt,
	)

	return result, nil
}

func fetchUserRepos(username string, sort string) (string, error) {
	sortParam := "pushed"
	if sort == "stars" {
		sortParam = "stars"
	} else if sort == "name" {
		sortParam = "full_name"
	}

	body, err := githubAPI(fmt.Sprintf("/users/%s/repos?sort=%s&per_page=10&direction=desc", username, sortParam))
	if err != nil {
		return "", err
	}

	var repos []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Stars       int    `json:"stargazers_count"`
		Language    string `json:"language"`
		Fork        bool   `json:"fork"`
		UpdatedAt   string `json:"updated_at"`
	}

	if err := json.Unmarshal(body, &repos); err != nil {
		return "", err
	}

	var lines []string
	for _, r := range repos {
		if r.Fork {
			continue // Skip forks
		}
		desc := r.Description
		if len(desc) > 60 {
			desc = desc[:60] + "..."
		}
		lines = append(lines, fmt.Sprintf("  %s — %s [%s, %d stars]",
			r.Name, desc, r.Language, r.Stars))
	}

	if len(lines) == 0 {
		return fmt.Sprintf("No repositories found for %s", username), nil
	}

	return fmt.Sprintf("Repositories for %s (sorted by %s):\n%s", username, sort, strings.Join(lines, "\n")), nil
}

func fetchRepoDetails(owner, repo string) (string, error) {
	body, err := githubAPI(fmt.Sprintf("/repos/%s/%s", owner, repo))
	if err != nil {
		return "", err
	}

	var r struct {
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		Stars       int    `json:"stargazers_count"`
		Forks       int    `json:"forks_count"`
		OpenIssues  int    `json:"open_issues_count"`
		Language    string `json:"language"`
		License     struct {
			Name string `json:"name"`
		} `json:"license"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
		Topics    []string `json:"topics"`
	}

	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}

	topics := "none"
	if len(r.Topics) > 0 {
		topics = strings.Join(r.Topics, ", ")
	}

	licenseName := "None"
	if r.License.Name != "" {
		licenseName = r.License.Name
	}

	result := fmt.Sprintf(
		"Repository: %s\n"+
			"  Description: %s\n"+
			"  Language: %s\n"+
			"  Stars: %d | Forks: %d | Open Issues: %d\n"+
			"  License: %s\n"+
			"  Topics: %s\n"+
			"  Created: %s\n"+
			"  Last updated: %s",
		r.FullName, r.Description, r.Language,
		r.Stars, r.Forks, r.OpenIssues,
		licenseName, topics, r.CreatedAt, r.UpdatedAt,
	)

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
