# Part 5: AI Chat System — Multi-Step Agent with Tool Orchestration

## 1. Overview

The AI Chat system transforms MCP Gateway from a simple API proxy into an intelligent assistant. It integrates with **Groq Cloud** (LLaMA 3.3 70B) to process natural language, decompose questions into tasks, call MCP tools in parallel, and synthesize results — all while supporting **conversation memory**, **human-in-the-loop approvals**, and a **rich web UI**.

```
User (Chat UI) → HTTP /api/chat → Brain (Groq) → Planner → Executor → MCP Tools → Synthesize → Response
                                     ↕                           ↕
                                  Memory Store            Approval Store (human-in-loop)
```

Source code: `internal/ai/` (brain, planner, executor, memory, orchestrator, agent) + `internal/server/chat.go` + `internal/server/chatui.go`

---

## 2. Brain (`brain.go`) — The AI Engine

**File:** `internal/ai/brain.go` (190 lines)

`Brain` is the central AI engine. It wraps the **Groq API** (LLaMA 3.3 70B, free-tier friendly) and exposes three main entry points:

### 2.1 Construction

```go
func NewBrain(apiKey string, toolSet []gateway.ToolInfo) (*Brain, error)
```

- Takes a Groq API key and the list of available MCP tools (registered servers + their tool definitions).
- Builds an internal tool registry (`toolSet`).
- Sets a generous 120s timeout for Groq API calls (complex plans take time).

### 2.2 Three Processing Modes

| Mode | Method | What it does |
|---|---|---|
| **Agent** (simple) | `RunAgent` / `RunAgentWithHistory` | Multi-turn tool-calling loop. AI decides each step. Max 5 rounds. |
| **Orchestrator** (advanced) | `ProcessWithOrchestrator` | Plan → execute → retry → summarize. Supports approvals & memory. |
| **Direct LLM** | `callGroq` | Single shot to Groq, used internally. |

### 2.3 Internal: Tool Definitions & Groq Chat

`GetAvailableTools()` returns a `[]ToolCallSchema` — the tool definitions in Groq's function-calling format. Each tool's JSON schema (from the MCP server's `tools/list` response) is wrapped into:

```go
type ToolCallSchema struct {
    Type     string       `json:"type"`     // "function"
    Function FunctionDef  `json:"function"`
}
```

The actual Groq API call happens in `executeChat(reqBody)`:

- **Endpoint:** `https://api.groq.com/openai/v1/chat/completions`
- **Model:** `llama-3.3-70b-versatile`
- **Tools:** the full tool registry (so Groq knows what it can call)
- **Response:** parsed into `ChatResponse` with choices, each containing a message with optional `ToolCalls[]`

### 2.4  Thinking Tag Stripping

`stripThinkTags(content)` removes  `...` blocks from model output before returning to the user.

---

## 3. Planner (`planner.go`) — Goal Decomposition

**File:** `internal/ai/planner.go` (94 lines)

The `Brain.DecomposeGoal(userMessage)` method asks Groq to turn a user question into a structured plan.

### 3.1 The Prompt

A dedicated system prompt instructs Groq to:

1. Analyze the user's question
2. Break it into independent tasks that can run in parallel
3. Return strict JSON (nothing else)

The prompt emphasizes **parallelism** wherever tasks are independent.

### 3.2 Plan / Task Data Structures

```go
type Plan struct {
    Tasks     []*Task        `json:"tasks"`
    Reasoning string         `json:"reasoning,omitempty"`
}
type Task struct {
    Tool        string         `json:"tool"`
    Arguments   map[string]any `json:"arguments"`
    Description string         `json:"description"`
    DependOn    []int          `json:"depend_on"` // indices of tasks to wait for
    status      TaskStatus     // internal
    result      string         // internal
    Error       string         // populated on failure
}
```

Tasks can declare dependencies (`DependOn`) forming a DAG. The executor uses this for ordering.

### 3.3 Task Statuses

```go
type TaskStatus int
const (
    TaskPending TaskStatus = iota
    TaskRunning
    TaskDone
    TaskFailed
)
```

### 3.4 JSON Parsing

Groq's response is cleaned (markdown fences removed via `cleanJSON`) and unmarshalled into a `Plan`. If no plan is returned or parsing fails, `DecomposeGoal` returns an empty plan — causing the orchestrator to fall back to the direct agent loop.

---

## 4. Executor (`executor.go`) — Parallel Task Execution

**File:** `internal/ai/executor.go` (118 lines)

`Brain.ExecutePlan(plan, callTool)` runs all tasks with dependency-aware concurrency.

### 4.1 Execution Strategy

- **Dependency graph:** Tasks with `DependOn` wait for their prerequisites to complete.
- **Concurrency:** All tasks whose dependencies are satisfied run in parallel via goroutines.
- **Polling loop:** Every 100ms the executor checks which tasks are ready and launches them.

### 4.2 The Run Loop

```
for all tasks not done {
    for each pending task:
        if all dependencies are done → launch goroutine
    sleep 100ms
    check for completions
}
```

### 4.3 Execution Report

```go
type ExecutionReport struct {
    TaskResults map[int]string `json:"task_results"`
    Errors      map[int]string `json:"errors"`
    Complete    bool           `json:"complete"`
    Duration    time.Duration  `json:"duration"`
}
```

`Complete` is `true` only when every task reached either `TaskDone` or `TaskFailed`.

---

## 5. Memory (`memory.go`) — Cross-Session Recall

**File:** `internal/ai/memory.go` (159 lines)

### 5.1 Interface

```go
type MemoryStore interface {
    Save(entry MemoryEntry) error
    QueryRelevant(query string, limit int) ([]MemoryEntry, error)
    GetRecent(limit int) ([]MemoryEntry, error)
    Clear() error
}
```

### 5.2 InMemoryStore

A simple ring-buffer implementation with **token-overlap relevance scoring**:

- `Save`: Appends entries; evicts oldest if exceeding `maxSize`
- `QueryRelevant`: Tokenizes query + entry answer, counts overlapping words (min 3 chars), returns top N by score
- `GetRecent`: Returns most recent N entries in reverse chronological order

### 5.3 Integration

`Brain.RetrieveRelevantMemories(query)` is called by the orchestrator to inject relevant past interactions as a **system message** before processing. This gives the AI context like:

```
Here are relevant past conversations for context:

Past interaction 1:
  User asked: What is the weather in Delhi?
  I answered: Current weather in Delhi: 32°C, partly cloudy...
  Tools used: get_weather
```

---

## 6. Orchestrator (`orchestrator.go`) — The Full Pipeline

**File:** `internal/ai/orchestrator.go` (356 lines)

`Brain.ProcessWithOrchestrator` is the main entry point for chat. It implements:

### 6.1 Full Flow

```
1. Build messages (system + history + user)
2. Inject relevant memories (if MemoryStore configured)
3. Inject pending approval info (if ApprovalStore configured)
4. DecomposeGoal() → Plan
5. If plan empty and no prior context → fallbackToDirect (agent loop)
6. If plan empty and has prior context → handleNoTools (answer from history or NEED_TOOL)
7. Check approvals (checkApprovals) → if needs approval, return early
8. ExecutePlan() → run all tasks
9. If any task failed → one retry with re-plan (uses different tools)
10. compileResults() → synthesize final answer
11. Save to memory if configured
```

### 6.2 Approval Flow (Human-in-the-Loop)

`checkApprovals(plan, cfg)` checks each task's tool against the `ApprovalStore`:

- If **already approved** (in `ApprovedTools` set) → skip
- If the tool is flagged as **risky** → create an approval request and return `NeedsApproval: true` with the `ApprovalID`

The front-end then shows Approve/Reject buttons. On approval, the same message is re-sent with `approval_id` and `approved_tools`, causing the orchestrator to skip those tools' approval checks.

### 6.3 Retry Logic (Self-Correction)

When tasks fail, the orchestrator gives the AI **one chance to re-plan**:

1. Collect all `"tool X failed: <error>"` messages
2. Call `DecomposeGoal()` with the failure information appended to the original query
3. Only use the retry plan if it **introduces new tools** (not the same failed ones)
4. Merge successful retry results into the original plan

This allows the AI to adapt — e.g., if `web_search` fails, it might try `wikipedia_summary` instead.

### 6.4 Fallback: Direct Agent

When planning fails (no tools needed or parsing error), the orchestrator falls back to `RunAgentWithHistory()` — the simpler multi-turn agent loop. This ensures the user always gets an answer.

### 6.5 handleNoTools (Contextual Answers)

When the planner returns no tasks but there IS conversation history:

1. Tell the AI to answer from context
2. **Pronoun resolution:** "he/she/it/his/her" → resolve from prior conversation
3. If a specific fact is genuinely missing → respond `NEED_TOOL`
4. On `NEED_TOOL` or blank answer → fall through to the tool agent

This enables follow-ups like "What was the temperature again?" without re-calling tools.

### 6.6 compileResults — Final Synthesis

After execution:

1. Collect all successful tool results and failed task descriptions
2. Build a summary prompt asking Groq to synthesize into a **natural answer**
3. **Retry synthesis** up to 2 times (Groq can return empty)
4. If synthesis still fails → strip `"Tool 'X' result: "` prefixes and return clean raw data

Rules enforced in the synthesis prompt:
- NEVER output raw tool result text
- Combine multiple tool results into ONE coherent answer
- Present lists as bullet points
- Acknowledge errors briefly and move on

---

## 7. Agent (`agent.go`) — Multi-Step Tool Loop

**File:** `internal/ai/agent.go` (227 lines)

The `Agent` is a simpler, more flexible alternative to the orchestrator. It lets the AI drive the entire conversation, deciding which tools to call at each step.

### 7.1 How It Works

```
Loop (max 5 iterations):
  1. Send all messages (system + history + user + previous tool results) to Groq
  2. If response has ToolCalls → execute each tool, append results to conversation
  3. If response has no ToolCalls → AI is done, return final answer
```

### 7.2 Key Features

- **Multi-tool per step:** Groq can request multiple tool calls in one response (e.g., get weather for 3 cities simultaneously)
- **Document-first routing:** If the user mentions a `*.pdf` / `*.txt` / etc. filename, the agent **force-calls `ask_document`** before giving the AI any choice (avoids hallucination from stale context)
- **Max steps guard:** At 5 iterations, the agent forces a final summary using whatever data was gathered
- **Error tolerance:** Tool errors are captured as text results, not failures — the AI can decide whether to retry or move on

### 7.3 Entry Points

```go
func (b *Brain) RunAgent(userMessage string, callTool func(...)) (*AgentResult, error)
func (b *Brain) RunAgentWithHistory(userMessage string, history []map[string]string, callTool func(...)) (*AgentResult, error)
```

---

## 8. Chat HTTP Handler (`chat.go`)

**File:** `internal/server/chat.go` (353 lines)

### 8.1 Request Structure

```json
{
  "message": "What's the weather in Delhi?",
  "session_id": "abc123",
  "approval_id": "apr_xxx",        // for continuing after approval
  "approved_tools": ["send_email"]  // tools user already approved
}
```

Validation: message required, max 10k chars, session_id required.

### 8.2 handler Flow

```
1. If brain not configured → 503 Service Unavailable
2. Parse request, validate
3. Get username from auth context
4. Load conversation history (MongoDB or in-memory fallback)
5. If continuing after approval → wait for approval result (500ms), capture approved tool
6. Build OrchestratorConfig (approval store + approved tools)
7. Create callToolFn → forwards to gateway.ForwardToolCall
8. ProcessWithOrchestrator → gets result
9. If approval needed → return status: "pending_approval" with approval_id
10. Store AI response + metadata (tools used, latency, steps)
11. Log everything
12. Return JSON response
```

### 8.3 Session Management

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/chat` | POST | Send message, get AI response |
| `/api/chat/sessions` | GET | List user's chat sessions |
| `/api/chat/sessions` | POST | Create new session |
| `/api/chat/sessions/{id}` | DELETE | Delete a session |
| `/api/chat/sessions/{id}/messages` | GET | Get all messages in a session |

Session persistence requires MongoDB (via `auth.ChatStore`). Without it, the system uses an **in-memory fallback** capped at 20 messages per session.

### 8.4 extractToolText

`extractToolText(response)` pulls the `text` content from the standard MCP tool response format:

```json
{
  "result": {
    "content": [{ "text": "..." }]
  }
}
```

---

## 9. Chat UI (`chatui.go`)

**File:** `internal/server/chatui.go` (810 lines)

A single-page embedded HTML/CSS/JS application served as a Go constant.

### 9.1 Tech Stack

- **Pure vanilla JS** — no frameworks, no build step
- **localStorage fallback** — works without backend persistence
- **Dark theme** — purple accent (#a855f7), dark background (#0f1117)
- **Mobile responsive** — hamburger sidebar, touch-friendly targets

### 9.2 Features

| Feature | Implementation |
|---|---|
| Session sidebar | Left panel with create/delete, sorted by date |
| Message bubbles | User (purple gradient) / AI (dark card) |
| Tool badges | Shows which tools were used per response |
| Step expansion | Expandable per-tool details |
| Typing indicator | Animated bouncing dots |
| Voice input | Web Speech API (webkitSpeechRecognition) |
| File upload | FormData → `/api/upload`, then auto-question |
| Approval dialogs | Approve/Reject buttons for risky tool calls |
| Token refresh | Silent JWT refresh every hour |
| Welcome screen | Quick-action capability buttons |
| Scroll-to-bottom | Floating button appears when scrolled up |
| Timestamps | Hover to see message time |

### 9.3 Architecture

```
Client State:
  - localStorage("chat_messages")   → {sessionId: [{role, content, meta}]}
  - localStorage("local_sessions")  → [{id, title, created_at}]
  - localStorage("local_session_id") → current session

Server Sync:
  - If server responds → use server sessions
  - If server 404/405 → use localStorage only
  - Hybrid: server sessions + local sessions merged in sidebar
```

### 9.4 Approval UI

When the orchestrator returns `pending_approval`, the UI:

1. Shows a yellow-bordered card with "Action Required"
2. Lists planned tasks with tool names and arguments
3. Shows **Approve** (green) and **Reject** (red) buttons
4. On approve → POST to `/api/approvals/{id}/approve`, re-send message
5. On reject → POST to `/api/approvals/{id}/reject`, show rejection message

### 9.5 Document Upload Flow

```
User clicks paperclip → file picker → FormData POST /api/upload 
  → success message appears as AI bubble
  → user can then ask questions about the document
  → agent.go forces ask_document when filename is detected in query
```

---

## 10. Data Flow Summary

```
┌─────────────┐     POST /api/chat      ┌─────────────────────────────────────┐
│  Chat UI    │ ──────────────────────→  │        Chat Handler (chat.go)        │
│ (chatui.go) │                          │                                     │
│             │ ←────────────────────── │ 1. Validate request                  │
│  Browser    │     JSON Response        │ 2. Load history (MongoDB/memory)    │
└─────────────┘                          │ 3. Build callToolFn → gateway        │
                                         │ 4. ProcessWithOrchestrator()        │
                                         │                                     │
                                         │  ┌─────────────────────────────────┐│
                                         │  │        Brain (brain.go)         ││
                                         │  │  ┌───────────┐ ┌────────────┐  ││
                                         │  │  │  Planner   │ │  Executor  │  ││
                                         │  │  │ (decompose)│ │ (parallel) │  ││
                                         │  │  └─────┬─────┘ └──────┬─────┘  ││
                                         │  │        │              │         ││
                                         │  │  ┌─────▼──────────────▼─────┐  ││
                                         │  │  │    Groq API (LLaMA 3.3)  │  ││
                                         │  │  └──────────────────────────┘  ││
                                         │  └─────────────────────────────────┘│
                                         │                                     │
                                         │  ┌─────────────────────────────────┐│
                                         │  │  Memory Store (memory.go)       ││
                                         │  │  → relevance-scored recall      ││
                                         │  └─────────────────────────────────┘│
                                         │                                     │
                                         │  ┌─────────────────────────────────┐│
                                         │  │  Approval Store                 ││
                                         │  │  → human-in-the-loop for tools  ││
                                         │  └─────────────────────────────────┘│
                                         └─────────────────────────────────────┘
                                                         │
                                                         ▼
                                              ┌────────────────────┐
                                              │  MCP Gateway       │
                                              │  (tool forwarding) │
                                              └────────────────────┘
                                                         │
                                          ┌──────────────┼──────────────┐
                                          ▼              ▼              ▼
                                     MCP Server 1  MCP Server 2  MCP Server 3
                                     (weather)     (github)      (news)
```

---

## 11. Key Design Decisions

1. **Orchestrator-first, Agent-fallback:** The orchestrator's plan-execute-synthesize pipeline handles most cases efficiently. The simpler agent loop is a fallback for when planning fails.

2. **Parallel execution:** Tasks without dependencies run concurrently, reducing latency for multi-tool queries (e.g., "Compare Tokyo and Delhi weather").

3. **Self-correction:** One automatic retry with re-planning when tools fail. The AI must choose *different* tools, avoiding infinite loops.

4. **Memory injection as system messages:** Past conversations are injected as context, not used for training. This keeps the architecture stateless from the AI's perspective.

5. **Approval as a gate, not a filter:** Approval happens before execution, not after. The plan is shown to the user, who can reject it before any tool runs.

6. **No-auth fallback:** Everything works without MongoDB or JWT. The UI degrades to localStorage-only persistence.

7. **Document-first routing:** For document questions, the tool call is forced before the AI decides — preventing hallucination from stale chat context.

---

## 12. Files Reference

| File | Lines | Role |
|---|---|---|
| `internal/ai/brain.go` | 190 | AI engine, Groq API client, tool definitions |
| `internal/ai/planner.go` | 94 | Goal decomposition → Plan with dependency DAG |
| `internal/ai/executor.go` | 118 | Parallel task execution with dependency resolution |
| `internal/ai/memory.go` | 159 | MemoryStore interface + InMemoryStore with relevance scoring |
| `internal/ai/orchestrator.go` | 356 | Full pipeline: plan → execute → retry → synthesize |
| `internal/ai/agent.go` | 227 | Multi-step tool-calling agent loop |
| `internal/ai/agent_test.go` | 29 | Tests for document name extraction |
| `internal/server/chat.go` | 353 | HTTP handlers for chat + session management |
| `internal/server/chatui.go` | 810 | Embedded HTML/CSS/JS single-page chat UI |