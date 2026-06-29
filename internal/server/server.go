// Package server provides the HTTP server for the MCP Gateway.
package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/ai" // Used for Brain type
	"github.com/varunbanda/mcp-gateway/internal/approval"
	"github.com/varunbanda/mcp-gateway/internal/auth"
	"github.com/varunbanda/mcp-gateway/internal/gateway"
	"github.com/varunbanda/mcp-gateway/internal/logger"
)

// Ensure packages are used.
var _ *ai.Brain

// Server is our HTTP server that wraps the Gateway logic.
type Server struct {
	gateway        *gateway.Gateway
	logger         *logger.Logger
	brain          *ai.Brain
	auth           *auth.Auth
	port           int
	approvalStore  *approval.Store
}

// New creates a new HTTP server.
func New(gw *gateway.Gateway, reqLogger *logger.Logger, aiBrain *ai.Brain, authenticator *auth.Auth, port int) *Server {
	return &Server{
		gateway: gw,
		logger:  reqLogger,
		brain:   aiBrain,
		auth:    authenticator,
		port:    port,
	}
}

// WithApprovalStore attaches a human-in-the-loop approval store.
func (s *Server) WithApprovalStore(as *approval.Store) *Server {
	s.approvalStore = as
	return s
}

// Start begins listening for HTTP requests.
func (s *Server) Start() error {
	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /chat", s.handleChatPage)

	// Auth endpoints (public — signup/login don't need a token)
	mux.HandleFunc("POST /api/auth/signup", s.handleSignup)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/auth/me", s.handleAuthMe)
	if s.auth == nil {
		log.Println("Auth disabled — all API routes are public")
	}

	// Protected API endpoints
	mux.HandleFunc("GET /api/servers", s.handleListServers)
	mux.HandleFunc("GET /api/tools", s.handleListTools)
	mux.HandleFunc("GET /api/logs", s.handleLogs)
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("POST /mcp/message", s.handleMCPMessage)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("GET /api/chat/sessions", s.handleListSessions)
	mux.HandleFunc("POST /api/chat/sessions", s.handleCreateSession)
	mux.HandleFunc("DELETE /api/chat/sessions/{id}", s.handleDeleteSession)
	mux.HandleFunc("GET /api/chat/sessions/{id}/messages", s.handleGetMessages)
	if s.approvalStore != nil {
		mux.HandleFunc("GET /api/approvals/pending", s.handlePendingApprovals)
		mux.HandleFunc("POST /api/approvals/{id}/approve", s.handleApproveAction)
		mux.HandleFunc("POST /api/approvals/{id}/reject", s.handleRejectAction)
	}
	mux.HandleFunc("POST /api/upload", s.handleFileUpload)

	// Wrap with middleware
	handler := s.loggingMiddleware(mux)
	handler = s.corsMiddleware(handler)

	if s.auth != nil {
		handler = s.auth.Middleware(handler)
		log.Println("Auth middleware enabled — API routes require JWT token")
	}

	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("MCP Gateway listening on http://localhost%s", addr)
	log.Printf("Dashboard available at http://localhost%s/", addr)

	return http.ListenAndServe(addr, handler)
}

// handleHealth responds with a simple "I'm alive" message.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"status":  "healthy",
		"time":    time.Now().Format(time.RFC3339),
		"servers": len(s.gateway.ListServers()),
		"tools":   len(s.gateway.ListTools()),
	})
}

// handleListServers returns the status of all connected MCP servers.
func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers := s.gateway.ListServers()
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"servers": servers,
		"count":   len(servers),
	})
}

// handleListTools returns all tools aggregated from all online servers.
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	tools := s.gateway.ListTools()
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"tools": tools,
		"count": len(tools),
	})
}

// handleLogs returns recent request logs for the current user.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	username, _ := auth.UserFromContext(r.Context())

	// Use MongoDB for historical logs when auth is enabled
	if s.auth != nil {
		logs := s.auth.RecentLogs(50, username)
		s.jsonResponse(w, http.StatusOK, map[string]any{
			"logs":  logs,
			"count": len(logs),
		})
		return
	}

	logs := s.logger.Recent(50, username)
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"logs":  logs,
		"count": len(logs),
	})
}

// handleStats returns aggregate statistics for the current user.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	username, _ := auth.UserFromContext(r.Context())

	// Use MongoDB for historical stats when auth is enabled
	if s.auth != nil {
		stats := s.auth.GetRequestStats(username)
		s.jsonResponse(w, http.StatusOK, stats)
		return
	}

	stats := s.logger.GetStats(username)
	s.jsonResponse(w, http.StatusOK, stats)
}

// handleMCPMessage receives an MCP JSON-RPC message and forwards it.
func (s *Server) handleMCPMessage(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var request MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON: " + err.Error(),
		})
		return
	}

	// Get username from auth context for logging
	username, _ := auth.UserFromContext(r.Context())

	// tools/list → return aggregated tools
	if request.Method == "tools/list" {
		tools := s.gateway.ListTools()
		latency := time.Since(start)
		s.logger.Log("tools/list", "", "", username, "success", "", latency)
		if s.auth != nil {
			s.auth.LogRequest(username, "tools/list", "", "", "success", "", latency)
		}
		s.jsonResponse(w, http.StatusOK, MCPResponse{
			JSONRPC: "2.0",
			ID:      request.ID,
			Result:  map[string]any{"tools": tools},
		})
		return
	}

	// tools/call → forward to correct server
	if request.Method == "tools/call" {
		// Inject username for downstream user isolation
		if username != "" {
			request.Params["_user"] = username
		}

		toolName, _ := request.Params["name"].(string)

		fwdReq := gateway.ForwardRequest{
			JSONRPC: "2.0",
			ID:      request.ID,
			Method:  request.Method,
			Params:  request.Params,
		}

		result, err := s.gateway.ForwardToolCall(fwdReq)
		latency := time.Since(start)

		if err != nil {
			s.logger.Log("tools/call", toolName, "", username, "error", err.Error(), latency)
			if s.auth != nil {
				s.auth.LogRequest(username, "tools/call", toolName, "", "error", err.Error(), latency)
			}
			s.jsonResponse(w, http.StatusBadGateway, map[string]string{
				"error": err.Error(),
			})
			return
		}

		s.logger.Log("tools/call", toolName, result.ServerName, username, "success", "", latency)
		if s.auth != nil {
			s.auth.LogRequest(username, "tools/call", toolName, result.ServerName, "success", "", latency)
		}
		s.jsonResponse(w, http.StatusOK, result.Response)
		return
	}

	// Unknown method
	s.jsonResponse(w, http.StatusBadRequest, map[string]string{
		"error": "unsupported method: " + request.Method,
	})
}

// handleFileUpload proxies file uploads to the documents RAG server.
func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	// Find the documents server URL from config
	server, err := s.gateway.GetServer("documents")
	if err != nil {
		s.jsonResponse(w, http.StatusBadGateway, map[string]string{"error": "Documents server not found"})
		return
	}

	// Forward the multipart form to the docs server
	uploadURL := server.Config.URL + "/upload"

	// Read the incoming request body and forward it
	proxyReq, err := http.NewRequest("POST", uploadURL, r.Body)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Failed to create proxy request"})
		return
	}
	proxyReq.Header = r.Header

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		s.jsonResponse(w, http.StatusBadGateway, map[string]string{"error": "Documents server unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	// Forward response back
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	body := make([]byte, 1024*1024) // 1MB max
	n, _ := resp.Body.Read(body)
	w.Write(body[:n])
}

// handleDashboard serves the embedded HTML dashboard.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(dashboardHTML))
}

// handleChatPage serves the AI chat interface.
func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(chatPageHTML))
}

// --- Helper types ---

type MCPRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type MCPResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result"`
}

func (s *Server) jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// --- Approval Handlers (Human-in-the-Loop) ---

func (s *Server) handlePendingApprovals(w http.ResponseWriter, r *http.Request) {
	username, _ := auth.UserFromContext(r.Context())
	pending := s.approvalStore.GetPending(username)
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"approvals": pending,
		"count":     len(pending),
	})
}

func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	username, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	if id == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing approval id"})
		return
	}
	req, err := s.approvalStore.Approve(id, username)
	if err != nil {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"status":      "approved",
		"approval_id": req.ID,
		"tool":        req.Tool,
		"description": req.Description,
	})
}

func (s *Server) handleRejectAction(w http.ResponseWriter, r *http.Request) {
	username, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	if id == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing approval id"})
		return
	}
	req, err := s.approvalStore.Reject(id, username)
	if err != nil {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"status":      "rejected",
		"approval_id": req.ID,
		"tool":        req.Tool,
		"description": req.Description,
	})
}

func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("→ %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("← %s %s (%s)", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// --- Auth handlers ---

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "Authentication is not configured (no MongoDB)"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "username, email, and password are required"})
		return
	}
	if len(req.Password) < 6 {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 6 characters"})
		return
	}

	token, err := s.auth.Signup(req.Username, req.Email, req.Password)
	if err != nil {
		s.jsonResponse(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	s.jsonResponse(w, http.StatusCreated, map[string]any{
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "Authentication is not configured (no MongoDB)"})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.Username == "" || req.Password == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "username and password are required"})
		return
	}

	token, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		s.jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]any{
		"token":    token,
		"username": req.Username,
	})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusServiceUnavailable, map[string]string{"error": "Authentication is not configured (no MongoDB)"})
		return
	}
	username, ok := auth.UserFromContext(r.Context())
	if !ok {
		s.jsonResponse(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	user, err := s.auth.GetUser(username)
	if err != nil {
		s.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "user not found"})
		return
	}

	s.jsonResponse(w, http.StatusOK, map[string]any{
		"username":  user.Username,
		"email":     user.Email,
		"createdAt": user.CreatedAt,
	})
}
