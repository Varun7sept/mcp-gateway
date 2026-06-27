package notes

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	_ "modernc.org/sqlite"
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
		"name":        "add_note",
		"description": "Create a new note (permanently saved in database)",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title":   map[string]any{"type": "string", "description": "Note title"},
				"content": map[string]any{"type": "string", "description": "Note body/content"},
				"tags":    map[string]any{"type": "string", "description": "Comma-separated tags (optional, e.g., 'work,important')"},
			},
			"required": []string{"title", "content"},
		},
	},
	{
		"name":        "list_notes",
		"description": "List all saved notes from the database",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"limit": map[string]any{"type": "number", "description": "Maximum notes to return (default: 20)"},
			},
		},
	},
	{
		"name":        "search_notes",
		"description": "Search notes by keyword in title or content",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search keyword"},
			},
			"required": []string{"query"},
		},
	},
}

type Server struct {
	db     *sql.DB
	server *http.Server
	port   string
}

func New(port string) (*Server, error) {
	database, err := sql.Open("sqlite", "./notes.db")
	if err != nil {
		return nil, err
	}

	createTable := `
	CREATE TABLE IF NOT EXISTS notes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		content TEXT NOT NULL,
		tags TEXT DEFAULT '',
		username TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err = database.Exec(createTable); err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	var colCount int
	database.QueryRow("SELECT COUNT(*) FROM pragma_table_info('notes') WHERE name='username'").Scan(&colCount)
	if colCount == 0 {
		database.Exec("ALTER TABLE notes ADD COLUMN username TEXT DEFAULT ''")
		log.Println("Migrated notes database: added username column")
	}

	log.Println("Notes database initialized (notes.db)")

	return &Server{db: database, port: port}, nil
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp/message", s.handleMCPMessage)
	mux.HandleFunc("GET /health", s.handleHealth)

	s.server = &http.Server{Addr: s.port, Handler: mux}
	log.Printf("Notes MCP Server (SQLite) running on http://localhost%s", s.port)
	return s.server.ListenAndServe()
}

func (s *Server) Close() error {
	return s.db.Close()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM notes").Scan(&count)
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "notes_count": count})
}

func (s *Server) handleMCPMessage(w http.ResponseWriter, r *http.Request) {
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
			"serverInfo":      map[string]any{"name": "notes-server", "version": "2.0.0"},
		})
	case "tools/list":
		sendResult(w, req.ID, map[string]any{"tools": tools})
	case "tools/call":
		s.handleToolCall(w, req)
	default:
		sendError(w, req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (s *Server) handleToolCall(w http.ResponseWriter, req MCPRequest) {
	toolName, _ := req.Params["name"].(string)
	arguments, _ := req.Params["arguments"].(map[string]any)
	username, _ := req.Params["_user"].(string)

	switch toolName {
	case "add_note":
		title, _ := arguments["title"].(string)
		content, _ := arguments["content"].(string)
		tags, _ := arguments["tags"].(string)
		if title == "" || content == "" {
			sendToolResult(w, req.ID, "Error: title and content are required", true)
			return
		}
		result, err := s.db.Exec(
			"INSERT INTO notes (title, content, tags, username) VALUES (?, ?, ?, ?)",
			title, content, tags, username,
		)
		if err != nil {
			sendToolResult(w, req.ID, "Database error: "+err.Error(), true)
			return
		}
		id, _ := result.LastInsertId()
		sendToolResult(w, req.ID, fmt.Sprintf("Note saved! ID: %d, Title: '%s'", id, title), false)

	case "list_notes":
		limit := 20
		if l, ok := arguments["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		var rows *sql.Rows
		var err error
		if username != "" {
			rows, err = s.db.Query(
				"SELECT id, title, content, tags, created_at FROM notes WHERE username=? ORDER BY created_at DESC LIMIT ?",
				username, limit,
			)
		} else {
			rows, err = s.db.Query(
				"SELECT id, title, content, tags, created_at FROM notes ORDER BY created_at DESC LIMIT ?",
				limit,
			)
		}
		if err != nil {
			sendToolResult(w, req.ID, "Database error: "+err.Error(), true)
			return
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var id int
			var title, content, tags, createdAt string
			rows.Scan(&id, &title, &content, &tags, &createdAt)
			line := fmt.Sprintf("#%d [%s] %s", id, createdAt, title)
			if tags != "" {
				line += fmt.Sprintf(" (tags: %s)", tags)
			}
			line += fmt.Sprintf("\n    %s", content)
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			sendToolResult(w, req.ID, "No notes found. Use add_note to create one!", false)
		} else {
			sendToolResult(w, req.ID, fmt.Sprintf("Found %d notes:\n\n%s", len(lines), strings.Join(lines, "\n\n")), false)
		}

	case "search_notes":
		query, _ := arguments["query"].(string)
		if query == "" {
			sendToolResult(w, req.ID, "Error: 'query' parameter is required", true)
			return
		}
		searchTerm := "%" + query + "%"
		var rows *sql.Rows
		var err error
		if username != "" {
			rows, err = s.db.Query(
				"SELECT id, title, content, tags FROM notes WHERE username=? AND (title LIKE ? OR content LIKE ? OR tags LIKE ?)",
				username, searchTerm, searchTerm, searchTerm,
			)
		} else {
			rows, err = s.db.Query(
				"SELECT id, title, content, tags FROM notes WHERE title LIKE ? OR content LIKE ? OR tags LIKE ?",
				searchTerm, searchTerm, searchTerm,
			)
		}
		if err != nil {
			sendToolResult(w, req.ID, "Database error: "+err.Error(), true)
			return
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var id int
			var title, content, tags string
			rows.Scan(&id, &title, &content, &tags)
			lines = append(lines, fmt.Sprintf("#%d: %s — %s", id, title, content))
		}
		if len(lines) == 0 {
			sendToolResult(w, req.ID, fmt.Sprintf("No notes matching '%s'", query), false)
		} else {
			sendToolResult(w, req.ID, fmt.Sprintf("Found %d notes matching '%s':\n%s", len(lines), query, strings.Join(lines, "\n")), false)
		}

	default:
		sendToolResult(w, req.ID, "Unknown tool: "+toolName, true)
	}
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
	sendResult(w, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	})
}
