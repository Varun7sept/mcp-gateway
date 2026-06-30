package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var githubHTTPClient = &http.Client{Timeout: 10 * time.Second}

var githubToken = os.Getenv("GITHUB_TOKEN")

var githubTools = []map[string]any{
	{"name": "get_user", "description": "Get a GitHub user's public profile: bio, follower/following count, public repo count, and account creation date", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"username": map[string]any{"type": "string", "description": "GitHub username, e.g. torvalds or google"}}, "required": []string{"username"}}},
	{"name": "list_repos", "description": "List public repositories for a GitHub user, sorted by stars. Returns name, description, language, and star count.", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"username": map[string]any{"type": "string", "description": "GitHub username, e.g. torvalds or google"}, "sort": map[string]any{"type": "string", "description": "Sort order: stars (default), updated, or name"}}, "required": []string{"username"}}},
	{"name": "get_repo", "description": "Get details about a GitHub repository: description, stars, forks, open issues, language, and license", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"owner": map[string]any{"type": "string", "description": "GitHub username or organization that owns the repository"}, "repo": map[string]any{"type": "string", "description": "Repository name, e.g. linux or react"}}, "required": []string{"owner", "repo"}}},
}

func StartGitHub(port string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil { sendError(w, req.ID, -32700, "Parse error"); return }
		switch req.Method {
		case "initialize": sendResult(w, req.ID, map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "github-server", "version": "1.0.0"}})
		case "tools/list": sendResult(w, req.ID, map[string]any{"tools": githubTools})
		case "tools/call": handleGitHubTool(w, req)
		default: sendError(w, req.ID, -32601, "Method not found")
		}
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) })
	log.Printf("GitHub MCP Server running on http://localhost%s", port)
	return http.ListenAndServe(port, mux)
}

func handleGitHubTool(w http.ResponseWriter, req MCPRequest) {
	name, _ := req.Params["name"].(string)
	args, _ := req.Params["arguments"].(map[string]any)
	switch name {
	case "get_user":
		u, _ := args["username"].(string)
		if u == "" { sendToolResult(w, req.ID, "Error: 'username' required", true); return }
		r, err := githubAPI("/users/" + url.PathEscape(u))
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		var user struct { Login, Name, Bio, Location, CreatedAt string; Followers, Following, PublicRepos int }
		if err := json.Unmarshal(r, &user); err != nil { sendToolResult(w, req.ID, "Parse error", true); return }
		sendToolResult(w, req.ID, fmt.Sprintf("GitHub User: %s (@%s)\n  Bio: %s\n  Location: %s\n  Repos: %d\n  Followers: %d | Following: %d", user.Name, user.Login, user.Bio, user.Location, user.PublicRepos, user.Followers, user.Following), false)
	case "list_repos":
		u, _ := args["username"].(string); sort, _ := args["sort"].(string)
		if u == "" { sendToolResult(w, req.ID, "Error: 'username' required", true); return }
		if sort == "" { sort = "stars" }
		sp := "pushed"
		if sort == "stars" { sp = "stars" } else if sort == "name" { sp = "full_name" }
		body, err := githubAPI(fmt.Sprintf("/users/%s/repos?sort=%s&per_page=10", url.PathEscape(u), url.QueryEscape(sp)))
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		var repos []struct { Name, Description, Language, UpdatedAt string; Stars int; Fork bool }
		if err := json.Unmarshal(body, &repos); err != nil { sendToolResult(w, req.ID, "Parse error", true); return }
		var lines []string
		for _, r := range repos { if !r.Fork { d := r.Description; if len(d) > 60 { d = d[:60] + "..." }; lines = append(lines, fmt.Sprintf("  %s — %s [%s, %d stars]", r.Name, d, r.Language, r.Stars)) } }
		if len(lines) == 0 { sendToolResult(w, req.ID, fmt.Sprintf("No repos for %s", u), false) } else { sendToolResult(w, req.ID, fmt.Sprintf("Repos for %s:\n%s", u, strings.Join(lines, "\n")), false) }
	case "get_repo":
		owner, _ := args["owner"].(string); repo, _ := args["repo"].(string)
		if owner == "" || repo == "" { sendToolResult(w, req.ID, "Error: 'owner' and 'repo' required", true); return }
		body, err := githubAPI(fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo)))
		if err != nil { sendToolResult(w, req.ID, "Error: "+err.Error(), true); return }
		var r struct { FullName, Description, Language, CreatedAt, UpdatedAt string; Stars, Forks, OpenIssues int; License struct{ Name string } `json:"license"`; Topics []string }
		if err := json.Unmarshal(body, &r); err != nil { sendToolResult(w, req.ID, "Parse error", true); return }
		topics := "none"; if len(r.Topics) > 0 { topics = strings.Join(r.Topics, ", ") }
		license := "None"; if r.License.Name != "" { license = r.License.Name }
		sendToolResult(w, req.ID, fmt.Sprintf("Repo: %s\n  Description: %s\n  Stars: %d | Forks: %d | Issues: %d\n  License: %s\n  Topics: %s", r.FullName, r.Description, r.Stars, r.Forks, r.OpenIssues, license, topics), false)
	default: sendToolResult(w, req.ID, "Unknown tool: "+name, true)
	}
}

func githubAPI(path string) ([]byte, error) {
	req, err := http.NewRequest("GET", "https://api.github.com"+path, nil)
	if err != nil { return nil, fmt.Errorf("failed to create request: %w", err) }
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "mcp-gateway")
	if githubToken != "" { req.Header.Set("Authorization", "Bearer "+githubToken) }
	resp, err := githubHTTPClient.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil { return nil, fmt.Errorf("read error: %w", err) }
	if resp.StatusCode == 404 { return nil, fmt.Errorf("not found") }
	if resp.StatusCode == 403 { return nil, fmt.Errorf("rate limited — set GITHUB_TOKEN env var") }
	if resp.StatusCode != 200 { return nil, fmt.Errorf("status %d", resp.StatusCode) }
	return body, nil
}
