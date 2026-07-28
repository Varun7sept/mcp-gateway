# Part 5: AI Chat System — Multi-Step Agent with Tool Orchestration

## 1. Overview

The AI Chat system transforms MCP Gateway from a simple API proxy into an intelligent assistant. It integrates with **Groq Cloud** (LLaMA 3.3 70B) to process natural language, decompose questions into tasks, call MCP tools in parallel, and synthesize results — all while supporting **conversation memory**, **human-in-the-loop approvals**, and a **rich web UI**.

```
User (Chat UI) → HTTP /api/chat → Brain (Groq) → Planner → Executor → MCP Tools → Synthesize → Response
                                     ↕                           ↕
                                  Memory Store            Approval Store (human-in-loop)
```

**Source code:** `internal/ai/` (brain, planner, executor, memory, orchestrator, agent) + `internal/server/chat.go` + `internal/server/chatui.go`

---

## 2. Brain (`brain.go`) — The AI Engine

**File:** `internal/ai/brain.go` (381 lines)

`Brain` is the central AI engine. It wraps the **Groq API** with a **3-model fallback chain** and exposes multiple entry points for different processing strategies.

### 2.1 Brain Struct

```go
type Brain struct {
    apiKey     string
    models     []string                  // ordered fallback chain
    httpClient *http.Client
    memory     MemoryStore               // optional cross-session memory
}
```

### 2.2 Constructor — `New(apiKey string) *Brain`

- Sets up 3 Groq models in a fallback chain: `llama-3.3-70b-versatile` → `qwen/qwen3-32b` → `qwen/qwen3.6-27b`
- 30-second HTTP timeout per call
- Supports `GROQ_MODELS` env var to override the default model list
- Models are tried in order; rate limits (429), auth failures (403), and server errors (5xx) trigger automatic fallback to the next model

### 2.3 Three Processing Modes

| Mode | Method | What it does |
|---|---|---|
| **Direct tool call** | `DecideAction` | Single round: send question + tools → Groq → returns tool call or direct answer |
| **Agent** (simple) | `RunAgent` / `RunAgentWithHistory` | Multi-turn tool-calling loop. AI decides each step. Max 5 rounds. |
| **Orchestrator** (advanced) | `ProcessWithOrchestrator` | Plan → execute → retry → summarize. Supports approvals & memory. |

### 2.4 Post-Tool-Entry Points

In addition to the multi-turn loops, `brain.go` provides single-step helpers that were used earlier in the project's simpler approach:

- **`DecideAction(userMessage, history)`** — sends the user's message to Groq with available tools. Returns a `ToolCallResult` containing either a tool to call (with name, args, toolCallID) or a direct natural-language answer. Used by the simple single-tool path and `generate_final_answer.go`.

- **`GenerateFinalAnswer(userMessage, toolName, toolCallID, toolResult)`** — after a tool executes, feeds the result back to Groq as a conversation turn and asks for a natural synthesis. Returns the cleaned answer (think tags stripped).

### 2.5 Tool Definitions — Hardcoded Registry

`GetAvailableTools()` returns **18 `ToolDef` entries** as a **hardcoded list** (not dynamically built from MCP server registrations):

| Category | Tools |
|---|---|
| Weather | `get_weather`, `get_forecast` |
| GitHub | `get_user`, `list_repos`, `get_repo` |
| Notes | `add_note`, `list_notes`, `search_notes` |
| Crypto | `get_crypto_price`, `get_top_cryptos` |
| News | `get_top_news`, `search_news` |
| Web/Info | `web_search`, `wikipedia_summary` |
| Utilities | `shorten_url`, `generate_qr` |
| Documents | `upload_document`, `ask_document`, `list_documents` |

Each `ToolDef` includes `Type: "function"`, a `FuncDef` with `Name`, `Description`, and JSON `Parameters` schema with `Required` fields. The helper `makeTool()` builds these structs.

### 2.6 Multi-Model Fallback in `executeChat`

`executeChat(reqBody)` implements a **chain of fallback models**:

1. Tries each model in order: `llama-3.3-70b-versatile` → `qwen/qwen3-32b` → `qwen/qwen3.6-27b`
2. On `429`, `403`, `404`, or `>=500` → tries the next model
3. On other `4xx` → returns the error immediately (bad request, not retryable)
4. Returns the first successful response, or an error listing all failures

### 2.7 Core Data Types

```go
type Message struct {
    Role       string     `json:"role"`
    Content    string     `json:"content,omitempty"`
    ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
    ID       string       `json:"id"`
    Type     string       `json:"type"`
    Function FunctionCall `json:"function"`
}

type FunctionCall struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"`
}

type ChatRequest struct {
    Model    string    `json:"model"`
    Messages []Message `json:"messages"`
    Tools    []ToolDef `json:"tools,omitempty"`
}
```

The Groq endpoint is `https://api.groq.com/openai/v1/chat/completions`. The response is `ChatResponse` with `Choices[].Message` (which may contain `ToolCalls[]`).

### 2.8 Thinking Tag Stripping

`stripThinkTags(content)` removes `...` blocks from model output before returning to the user. Uses a regex `(?s)...*?...` with the `s` flag (dotall mode) to match across newlines.

---

## 3. Planner (`planner.go`) — Goal Decomposition

**File:** `internal/ai/planner.go` (94 lines)

### 3.1 The Prompt & JSON Output

`Brain.DecomposeGoal(userMessage, messages)` builds a Groq conversation and asks the model to break the question into independent tasks that can potentially run in parallel. The system prompt tells Groq to:

1. Analyze the user's question
2. Break it into independent tasks that can run in parallel
3. Return strict JSON (nothing else — no markdown fences, no explanation)

The prompt emphasizes **parallelism** wherever tasks are independent.

`DecomposeGoal` returns a `*Plan`. On error or empty result, the orchestrator falls back to the direct agent loop.

Sample plan JSON output:

```json
{
  "tasks": [
    {"tool": "get_weather", "arguments": {"city": "Tokyo"}, "description": "Get Tokyo weather"},
    {"tool": "get_weather", "arguments": {"city": "Delhi"}, "description": "Get Delhi weather"}
  ],
  "reasoning": "Fetching weather for both cities in parallel since they are independent"
}
```

### 3.2 Plan / Task Data Structures

```go
type Plan struct {
    Tasks     []*Task  `json:"tasks"`
    Reasoning string   `json:"reasoning,omitempty"`
}

type Task struct {
    Tool        string         `json:"tool"`
    Arguments   map[string]any `json:"arguments"`
    Description string         `json:"description"`
    DependOn    []int          `json:"depend_on"` // indices of tasks to wait for
    status      TaskStatus     // internal, not serialized
    result      string         // internal, not serialized
    Error       string         // populated on failure
}
```

Tasks can declare `DependOn` forming a DAG. The executor uses this for ordering.

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

Groq's response is cleaned using `cleanJSON` (strips markdown code fences) and unmarshalled into a `Plan`. If no plan is returned or parsing fails, `DecomposeGoal` returns an empty plan — causing the orchestrator to fall back to the direct `RunAgentWithHistory` loop.

---

## 4. Executor (`executor.go`) — Parallel Task Execution

**File:** `internal/ai/executor.go` (118 lines)

`Brain.ExecutePlan(plan, callTool)` runs all tasks with dependency-aware concurrency.

### 4.1 Execution Strategy

- **Dependency graph:** Tasks with `DependOn` wait for their prerequisites to complete.
- **Concurrency:** All tasks whose dependencies are satisfied launch as goroutines in parallel.
- **Polling loop:** The executor sleeps 100ms between checks, scanning pending tasks for which all dependencies are now `TaskDone`.
- **`callTool` callback:** Signature `func(name string, args map[string]any) (string, error)` — provided by the orchestrator/chat handler, routes tool calls through `gateway.ForwardToolCall`.

### 4.2 The Run Loop

```
for any task not in terminal state {
    for each pending task:
        if all DependOn indices are TaskDone or TaskFailed:
            launch goroutine: call task.Tool(task.Arguments)
            mark task as TaskRunning
    sleep 100ms
}
return ExecutionReport
```

Each goroutine calls `callTool(task.Tool, task.Arguments)`. On success, sets `task.result` and `status = TaskDone`. On error, sets `task.Error` and `status = TaskFailed`. The executor waits for ALL tasks to reach a terminal state before returning.

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

type MemoryEntry struct {
    Query     string    `json:"query"`
    Answer    string    `json:"answer"`
    ToolsUsed []string  `json:"tools_used"`
    Timestamp time.Time `json:"timestamp"`
}
```

### 5.2 InMemoryStore

A thread-safe ring-buffer implementation with **token-overlap relevance scoring**:

- **`Save`:** Appends entries with current timestamp; evicts oldest if exceeding `maxSize` (uses mutex `RLock` for reads, write lock for appends)
- **`QueryRelevant`:** Tokenizes both the query and each entry's query+answer text (lowercase, letters/digits only, min 3 chars). Counts word overlaps and returns the top N entries sorted by score (descending, using manual bubble sort)
- **`GetRecent`:** Returns the most recent N entries in reverse chronological order
- **`Clear`:** Truncates the entries slice to zero length

### 5.3 Integration with Brain

`Brain.RetrieveRelevantMemories(query)` is called by the orchestrator to inject relevant past interactions as a **system message** before processing:

```
Here are relevant past conversations for context:

Past interaction 1:
  User asked: What is the weather in Delhi?
  I answered: Current weather in Delhi: 32°C, partly cloudy...
  Tools used: get_weather
```

This is appended as a system message in `ProcessWithOrchestrator` so the AI has cross-session context.

---

## 6. Orchestrator (`orchestrator.go`) — The Full Pipeline

**File:** `internal/ai/orchestrator.go` (356 lines)

`Brain.ProcessWithOrchestrator` is the main entry point for chat (used by `chat.go` handler). It implements the complete AI pipeline with planning, execution, self-correction, approval gating, and memory persistence.

### 6.1 Full Flow

```
1. Build messages (system + history + user) via buildAgentMessages()
2. Inject relevant memories (if MemoryStore configured)
3. Inject pending approval info (if ApprovalStore configured)
4. DecomposeGoal() → Plan
5. If plan empty and ≤1 prior turns → fallbackToDirect (agent loop)
6. If plan empty and has prior context → handleNoTools (answer from history or NEED_TOOL)
7. DecomposeGoal() → Plan
8. checkApprovals(plan, cfg) → if needs approval, return early with ApprovalID
9. ExecutePlan(plan, callTool) → run all tasks
10. If any task failed → one retry with re-plan (uses different tools)
11. compileResults() → synthesize final answer
12. Save to memory if configured
13. Return OrchestratorResult{Answer, Steps, Plan, Report}
```

### 6.2 `buildAgentMessages` — Conversation History Builder

Constructs the message list with a system prompt describing capabilities and behaviour, then appends conversation history (mapping `"ai"` role to `"assistant"` for Groq API compatibility), and finally appends the current user message.

### 6.3 Approval Flow (Human-in-the-Loop)

`checkApprovals(plan, cfg)` checks each task's tool against the `ApprovalStore`:

- If **already approved** (in `ApprovedTools` set) → skip permanently for this request (avoids repetitive approval prompts)
- If the tool is flagged as **risky** → create an approval request via `ApprovalStore.CreateRequest()` and return `NeedsApproval: true` with the `ApprovalID` and the full `Plan`

The front-end then shows Approve/Reject buttons. On user approval, the same message is re-sent with `approval_id` and `approved_tools` (accumulated across rounds), causing the orchestrator's `checkApprovals` to skip those tools' checks.

### 6.4 Retry Logic (Self-Correction)

When tasks fail after initial execution:

1. Collect all `"tool X failed: <error>"` messages
2. Append them to the original user query as a retry hint
3. Call `DecomposeGoal()` with the retry hint — the AI must produce a different plan using different tools
4. Only use the retry plan if it **introduces new tools** (not the same failed ones — prevents infinite loops)
5. Merge any successful retry task results into the original plan
6. Use the retry's `ExecutionReport` if it's better

This allows the AI to adapt — e.g., if `web_search` fails, it might try `wikipedia_summary` instead.

### 6.5 Fallback: `fallbackToDirect`

When planning fails (no tools needed or parsing error), converts the `[]Message` to `[]map[string]string` history format and calls `RunAgentWithHistory()`. This ensures the user always gets an answer, even if the planner errors out.

### 6.6 `handleNoTools` — Contextual Answers

When the planner returns no tasks but there IS conversation history:

1. Tell the AI to answer from context with strict rules
2. **Pronoun resolution:** "he/she/it/his/her" must be resolved from prior conversation — never ask "who do you mean?"
3. Answer from history if the fact is clearly mentioned (dates, names, facts)
4. If a specific fact is genuinely missing → respond `NEED_TOOL`
5. On `NEED_TOOL` or blank/empty answer → fall through to tool agent

This enables natural follow-ups like "What was the temperature again?" without re-calling tools unnecessarily.

### 6.7 `compileResults` — Final Synthesis

After execution completes:

1. Collect all successful tool results and failed task descriptions into a formatted string
2. Build a summary prompt asking Groq to synthesize into a **natural answer** with strict rules:
   - NEVER output raw tool result text like `"Tool X result: ..."`
   - Answer the user's question directly using tool data
   - Present lists as bullet points with the most important details
   - Combine multiple tool results into ONE coherent answer — don't repeat the question
   - If a tool returned an error, acknowledge briefly and move on
   - Be concise but complete (2–6 sentences)
3. **Retry synthesis up to 2 times** (Groq can return empty on first call)
4. If synthesis still fails/empty → strip `"Tool 'X' result: "` prefixes and return clean raw data

The final `OrchestratorResult` contains `Answer`, `Steps`, `Plan`, and `Report` for the HTTP response.

---

## 7. Agent (`agent.go`) — Multi-Step Tool Loop

**File:** `internal/ai/agent.go` (227 lines)

The `Agent` is a simpler, more flexible alternative to the orchestrator. It lets the AI drive the entire conversation, deciding which tools to call at each step.

### 7.1 How It Works

```
Loop (max 5 iterations):
  1. Send all messages (system + history + user + previous tool results) to Groq
  2. If response has ToolCalls → for EACH tool call:
     a. Execute tool via callTool callback
     b. Append assistant tool call + tool result to conversation
  3. If response has NO ToolCalls → AI is done, return final answer
  4. If max steps reached → ask AI to summarize gathered data
```

### 7.2 Key Features

- **Multi-tool per step:** Groq can request multiple tool calls in one response (e.g., get weather for Tokyo, Delhi, and Mumbai simultaneously)
- **Document-first routing:** If the user message contains a `*.pdf` / `*.txt` / `*.md` / etc. filename, the agent **force-calls `ask_document`** before the AI gets any choice (prevents hallucination from stale context)
- **Max steps guard:** At 5 iterations, the agent forces a final summary using whatever data was gathered
- **Error tolerance:** Tool errors are captured as text results (not exceptions) — the AI can decide whether to retry or move on
- **Pronoun resolution in tool context:** The system prompt instructs the AI to resolve pronouns from conversation history

### 7.3 `AgentStep` and `AgentResult`

```go
type AgentStep struct {
    ToolName   string         `json:"tool_name"`
    Arguments  map[string]any `json:"arguments"`
    Result     string         `json:"result"`
    ToolCallID string         `json:"-"`
}

type AgentResult struct {
    Answer string      `json:"answer"`
    Steps  []AgentStep `json:"steps"`
}
```

### 7.4 Entry Points

```go
func (b *Brain) RunAgent(userMessage string, callTool func(name string, args map[string]any) (string, error)) (*AgentResult, error)
func (b *Brain) RunAgentWithHistory(userMessage string, history []map[string]string, callTool func(name string, args map[string]any) (string, error)) (*AgentResult, error)
```

`RunAgent` is a convenience wrapper that calls `RunAgentWithHistory` with nil history.

---

## 8. Chat HTTP Handler (`chat.go`)

**File:** `internal/server/chat.go` (353 lines)

### 8.1 Request Structure

```json
{
  "message": "What's the weather in Delhi?",
  "session_id": "abc123",
  "approval_id": "apr_xxx",
  "approved_tools": ["send_email"]
}
```

Validation: message required, max 10,000 chars, session_id required.

### 8.2 `handleChat` Flow

```
1. If brain not configured (s.brain == nil) → 503 Service Unavailable
2. Parse JSON request body, validate
3. Get username from JWT auth context
4. Load conversation history:
   - If MongoDB auth exists → load from ChatStore, verify session ownership
   - If no MongoDB → use in-memory (s.memHistory), capped at 20 messages per session
5. If continuing after approval → wait for ApprovalStore.WaitForApproval (500ms timeout)
6. Build OrchestratorConfig (approval store + approved tools)
7. Create callToolFn → forwards to gateway.ForwardToolCall
8. s.brain.ProcessWithOrchestrator(req.Message, history, callToolFn, orchCfg)
9. If NeedsApproval → return {"status": "pending_approval", "approval_id": "...", "plan_tasks": [...]}
10. Store AI response + metadata in ChatStore or in-memory
11. Log agent tool calls and chat latency
12. Return JSON: {answer, steps, tools_used, num_steps, latency, num_tasks, all_completed}
```

### 8.3 `extractToolText` Helper

Extracts plain text from the standard MCP tool response JSON:

```json
{
  "result": {
    "content": [{"text": "The actual text content here"}]
  }
}
```

### 8.4 Session Management Endpoints

| Endpoint | Method | Purpose |
|---|---|---|
| `/api/chat` | POST | Send message, get AI response |
| `/api/chat/sessions` | GET | List user's chat sessions |
| `/api/chat/sessions` | POST | Create new session |
| `/api/chat/sessions/{id}` | DELETE | Delete a session |
| `/api/chat/sessions/{id}/messages` | GET | Get all messages in a session |

Session persistence requires MongoDB (via `auth.ChatStore`). Without it, the system uses an **in-memory fallback** capped at 20 messages per session, with thread-safe mutex protection (`s.memHistoryMu`).

---

## 9. Chat UI (`chatui.go`)

**File:** `internal/server/chatui.go` (810 lines)

A single-page embedded HTML/CSS/JS application served as a Go string constant `chatPageHTML`. No frameworks, no build step — pure vanilla JS.

### 9.1 Tech Stack & Theme

- **Pure vanilla JS** — no React, Vue, or build tools
- **Dark theme** — purple accent (#a855f7), dark background (#0f1117), card surfaces (#1a1b23)
- **Responsive** — hamburger sidebar toggle on mobile (≤768px), touch-friendly tap targets
- **localStorage fallback** — works entirely without a backend once connected

### 9.2 Core Features

| Feature | Implementation Detail |
|---|---|
| Session sidebar | Left panel with create/delete, sorted by date, shows last message preview |
| Message bubbles | User (purple gradient, right-aligned) / AI (dark card, left-aligned) |
| Tool badges | Purple pill badges showing which tools were used per response |
| Step expansion | Expandable per-tool details showing arguments |
| Typing indicator | Animated bouncing dots while waiting for AI response |
| Voice input | Web Speech API (`webkitSpeechRecognition`), pulsing red button while recording |
| File upload | Paperclip button → file picker → `FormData` POST `/api/upload` |
| Approval dialogs | Yellow-bordered card with "Action Required", Approve (green) / Reject (red) buttons |
| Token refresh | Silent JWT refresh every hour via `/api/auth/refresh` |
| Welcome screen | Quick-action buttons for common queries (weather, crypto, news, etc.) |
| Scroll-to-bottom | Floating arrow button appears when scrolled up |
| Timestamps | Hover over any message to see the time |

### 9.3 State Management Architecture

```
Client State (localStorage):
  - chat_messages    → {sessionId: [{role, content, meta}]}
  - local_sessions   → [{id, title, created_at}]
  - local_session_id → currently active session ID

Server Sync:
  - If server responds (200)  → use server sessions + messages
  - If server 404/405          → use localStorage only (no backend)
  - Hybrid mode                → merge server + local sessions in sidebar
```

### 9.4 Approval UI Flow

1. User message is sent → orchestrator returns `pending_approval`
2. UI removes typing indicator, shows yellow approval card
3. Card lists planned tasks with tool names and their arguments
4. User clicks **Approve** (green) → POST to `/api/approval/{id}/approve` → re-send original message with `approval_id`
5. User clicks **Reject** (red) → POST to `/api/approval/{id}/reject` → show "Action rejected" message
6. The `pendingApprovedTools` array accumulates across multiple approval rounds so the same tool isn't re-requested

### 9.5 Document Upload & RAG Flow

```
User clicks paperclip → file picker → FormData POST /api/upload
  → success message appears as AI bubble: "Document uploaded: filename.pdf"
  → user can then ask questions about the document
  → agent.go detects filename via regex → force-calls ask_document
  → RAG retrieves passages → AI answers from document content only
```

### 9.6 `formatText` HTML Sanitizer

The JS `formatText()` function safely renders AI markdown in the browser:
- Escapes all HTML entities first
- Re-introduces safe tags in order: images → QR codes → bold/italic → inline code → links
- Uses `_escHtml()` for all user-generated and URL content to prevent XSS
- Converts `\n` to `<br>`

---

## 10. Data Flow Summary

```
┌─────────────┐     POST /api/chat      ┌─────────────────────────────────────────┐
│  Chat UI    │ ──────────────────────→   │         Chat Handler (chat.go)          │
│ (chatui.go) │                           │                                         │
│  Browser    │ ←──────────────────────   │ 1. Validate request (message, session) │
└─────────────┘     JSON Response         │ 2. Load history (MongoDB or memory)   │
                                          │ 3. Build OrchestratorConfig            │
                                          │ 4. ProcessWithOrchestrator()           │
                                          │                                         │
                                          │  ┌───────────────────────────────────┐  │
                                          │  │          Brain (brain.go)         │  │
                                          │  │  ┌───────────┐ ┌──────────────┐  │  │
                                          │  │  │  Planner   │ │  Executor    │  │  │
                                          │  │  │ (decompose)│ │ (parallel)   │  │  │
                                          │  │  └─────┬─────┘ └───────┬────────┘  │  │
                                          │  │        │                │            │  │
                                          │  │  ┌─────▼────────────────▼────────┐  │  │
                                          │  │  │      Groq API (LLaMA 3.3)     │  │  │
                                          │  │  │  3-model fallback chain        │  │  │
                                          │  │  └───────────────────────────────┘  │  │
                                          │  └───────────────────────────────────┘  │
                                          │                                         │
                                          │  ┌───────────────────────────────────┐  │
                                          │  │  Memory Store (memory.go)         │  │
                                          │  │  → token-overlap relevance scoring│  │
                                          │  └───────────────────────────────────┘  │
                                          │                                         │
                                          │  ┌───────────────────────────────────┐  │
                                          │  │  Approval Store (human-in-loop)  │  │
                                          │  └───────────────────────────────────┘  │
                                          └─────────────────────────────────────────┘
                                                          │
                                                          ▼
                                               ┌──────────────────────┐
                                               │  MCP Gateway          │
                                               │  (tool forwarding)    │
                                               └──────────────────────┘
                                                          │
                                           ┌──────────────┼──────────────┐
                                           ▼              ▼              ▼
                                      MCP Server 1  MCP Server 2  MCP Server 3
                                      (weather)     (github)      (news)
```

---

## 11. Key Design Decisions

1. **Orchestrator-first, Agent-fallback:** The orchestrator's plan-execute-synthesize pipeline handles most cases efficiently and enables approvals and memory. The simpler agent loop is a fallback for when planning fails or no tools are needed.

2. **Parallel execution:** Tasks without dependencies run concurrently in goroutines, reducing latency for multi-tool queries (e.g., "Compare Tokyo and Delhi weather").

3. **Self-correction with one retry:** When tasks fail, the AI gets one chance to re-plan using *different* tools. The retry plan is rejected if it uses the same failed tools, preventing infinite loops.

4. **Memory injection as system messages:** Past conversations are injected as context messages, not used for model training. This keeps the architecture stateless from the AI's perspective.

5. **Approval as a gate:** Approval happens *before* execution, not after. The full plan is shown to the user before any risky tool runs.

6. **No-auth fallback:** Everything works without MongoDB or JWT — the UI degrades gracefully to localStorage-only persistence for sessions and messages.

7. **Document-first routing:** For document questions, `ask_document` is called deterministically before the AI's tool choice — preventing hallucination from stale chat context or another document.

8. **Multi-model resilience:** The 3-model fallback chain ensures availability even when one model is rate-limited or temporarily unavailable.

---

## 12. Files Reference

| File | Lines | Role |
|---|---|---|
| `internal/ai/brain.go` | 381 | AI engine, Groq API client, 3-model fallback, 18 hardcoded tools |
| `internal/ai/planner.go` | 94 | Goal decomposition → Plan with dependency DAG |
| `internal/ai/executor.go` | 118 | Parallel task execution with dependency resolution |
| `internal/ai/memory.go` | 159 | MemoryStore interface + InMemoryStore with relevance scoring |
| `internal/ai/orchestrator.go` | 356 | Full pipeline: plan → execute → retry → summarize + approval |
| `internal/ai/agent.go` | 227 | Multi-step tool-calling agent loop |
| `internal/ai/agent_test.go` | 29 | Tests for document name extraction |
| `internal/server/chat.go` | 353 | HTTP handlers for chat + session management |
| `internal/server/chatui.go` | 810 | Embedded HTML/CSS/JS single-page chat UI |