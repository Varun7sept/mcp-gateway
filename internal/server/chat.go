package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/ai"
	"github.com/varunbanda/mcp-gateway/internal/auth"
	"github.com/varunbanda/mcp-gateway/internal/gateway"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if s.brain == nil {
		s.jsonResponse(w, http.StatusServiceUnavailable, map[string]string{
			"error": "AI brain not configured. Set GROQ_API_KEY environment variable.",
		})
		return
	}

	var req struct {
		Message      string `json:"message"`
		SessionID    string `json:"session_id"`
		ApprovalID   string `json:"approval_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if req.Message == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}
	if len(req.Message) > 10000 {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "message too long (max 10000 characters)"})
		return
	}
	if req.SessionID == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
		return
	}

	start := time.Now()
	username, _ := auth.UserFromContext(r.Context())

	var history []map[string]string
	var chatStore *auth.ChatStore

	// Chat persistence requires MongoDB auth; skip if not configured
	if s.auth != nil {
		chatStore = s.auth.ChatStore()

		// Verify session ownership before any read/write
		if _, err := chatStore.GetSession(req.SessionID, username); err != nil {
			s.jsonResponse(w, http.StatusForbidden, map[string]string{
				"error": "session not found or access denied",
			})
			return
		}

		// If continuing after approval, wait for approval before proceeding
		if req.ApprovalID != "" && s.approvalStore != nil {
			_, err := s.approvalStore.WaitForApproval(req.ApprovalID, username, 500*time.Millisecond)
			if err != nil {
				s.jsonResponse(w, http.StatusForbidden, map[string]string{
					"error":   err.Error(),
					"message": "The action was not approved. Try rephrasing your request.",
				})
				return
			}
		}

		// Store user message; auto-update title on first message
		chatStore.AddMessage(req.SessionID, "user", req.Message, nil)

		if sess, _ := chatStore.GetSession(req.SessionID, username); sess != nil && sess.Title == "New Chat" {
			title := req.Message
			if len(title) > 50 {
				title = title[:50] + "..."
			}
			chatStore.UpdateSessionTitle(req.SessionID, username, title)
		}

		// Load recent messages for context (last 10)
		history = buildHistoryFromMessages(chatStore, req.SessionID)
	}

	// Build orchestrator config with memory and approval store
	orchCfg := &ai.OrchestratorConfig{}
	if s.approvalStore != nil && username != "" {
		orchCfg.ApprovalStore = s.approvalStore
		orchCfg.ApprovalUser = username
	}

	// Process via orchestrator (planning + parallel execution + self-correction + approval)
	callToolFn := func(toolName string, args map[string]any) (string, error) {
		fwdReq := gateway.ForwardRequest{
			JSONRPC: "2.0",
			ID:      1,
			Method:  "tools/call",
			Params: map[string]any{
				"name":      toolName,
				"arguments": args,
				"_user":     username,
			},
		}
		result, err := s.gateway.ForwardToolCall(fwdReq)
		if err != nil {
			return "", err
		}
		return extractToolText(result.Response), nil
	}

	orchResult, err := s.brain.ProcessWithOrchestrator(req.Message, history, callToolFn, orchCfg)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": "Agent error: " + err.Error()})
		return
	}

	// If human approval is needed, return early with approval info
	if orchResult.NeedsApproval {
		s.jsonResponse(w, http.StatusOK, map[string]any{
			"status":            "pending_approval",
			"approval_id":       orchResult.ApprovalID,
			"message":           "This action needs your approval before proceeding.",
			"plan_tasks":        orchResult.Plan.Tasks,
		})
		return
	}

	// Store AI response with tool metadata
	meta := map[string]any{}
	var toolsUsed []string
	chatLatency := time.Since(start)
	for _, step := range orchResult.Steps {
		toolsUsed = append(toolsUsed, step.ToolName)
		s.logger.Log("agent", step.ToolName, "", username, "success", "", 0)
		if s.auth != nil {
			s.auth.LogRequest(username, "agent", step.ToolName, "", "success", "", 0)
		}
	}
	meta["tools"] = toolsUsed
	meta["latency"] = time.Since(start).Milliseconds()
	meta["steps"] = orchResult.Steps
	if orchResult.Plan != nil {
		meta["num_tasks_planned"] = len(orchResult.Plan.Tasks)
	}
	if chatStore != nil {
		chatStore.AddMessage(req.SessionID, "ai", orchResult.Answer, meta)
	}

	s.logger.Log("chat", "", "", username, "success", "", chatLatency)
	if s.auth != nil {
		s.auth.LogRequest(username, "chat", "", "", "success", "", chatLatency)
	}

	numTasks := 0
	if orchResult.Plan != nil {
		numTasks = len(orchResult.Plan.Tasks)
	}
	allCompleted := false
	if orchResult.Report != nil {
		allCompleted = orchResult.Report.Complete
	}
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"answer":          orchResult.Answer,
		"steps":           orchResult.Steps,
		"tools_used":      toolsUsed,
		"num_steps":       len(orchResult.Steps),
		"latency":         time.Since(start).Milliseconds(),
		"num_tasks":       numTasks,
		"all_completed":   allCompleted,
	})
}

func buildHistoryFromMessages(cs *auth.ChatStore, sessionID string) []map[string]string {
	msgs, err := cs.GetRecentMessages(sessionID, 10)
	if err != nil || len(msgs) <= 1 {
		return nil
	}
	// Exclude the last message (which is the one we just stored)
	history := make([]map[string]string, 0, len(msgs)-1)
	for i := 0; i < len(msgs)-1; i++ {
		m := msgs[i]
		role := m.Role
		if role == "ai" {
			role = "assistant"
		}
		history = append(history, map[string]string{
			"role":    role,
			"content": m.Content,
		})
	}
	return history
}

// --- Chat Session Management ---

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusOK, map[string]any{
			"sessions": []any{},
			"count":    0,
		})
		return
	}
	username, _ := auth.UserFromContext(r.Context())
	sessions, err := s.auth.ChatStore().ListSessions(username)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusCreated, map[string]any{
			"id":    "local-" + fmt.Sprintf("%d", time.Now().UnixNano()),
			"title": "New Chat",
		})
		return
	}
	username, _ := auth.UserFromContext(r.Context())

	var req struct {
		Title string `json:"title"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.Title == "" {
		req.Title = "New Chat"
	}

	session, err := s.auth.ChatStore().CreateSession(username, req.Title)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.jsonResponse(w, http.StatusCreated, session)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
		return
	}
	username, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	if id == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	if err := s.auth.ChatStore().DeleteSession(id, username); err != nil {
		s.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	s.jsonResponse(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleGetMessages(w http.ResponseWriter, r *http.Request) {
	if s.auth == nil {
		s.jsonResponse(w, http.StatusOK, map[string]any{
			"messages": []any{},
			"count":    0,
		})
		return
	}
	username, _ := auth.UserFromContext(r.Context())
	id := r.PathValue("id")
	if id == "" {
		s.jsonResponse(w, http.StatusBadRequest, map[string]string{"error": "missing session id"})
		return
	}

	// Verify session belongs to user
	if _, err := s.auth.ChatStore().GetSession(id, username); err != nil {
		s.jsonResponse(w, http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}

	messages, err := s.auth.ChatStore().GetMessages(id)
	if err != nil {
		s.jsonResponse(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.jsonResponse(w, http.StatusOK, map[string]any{
		"messages": messages,
		"count":    len(messages),
	})
}

func extractToolText(response any) string {
	respMap, ok := response.(map[string]any)
	if !ok {
		return ""
	}
	result, ok := respMap["result"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := result["content"].([]any)
	if !ok {
		return ""
	}
	var text string
	for _, c := range content {
		cMap, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if t, ok := cMap["text"].(string); ok {
			text += t
		}
	}
	return text
}
