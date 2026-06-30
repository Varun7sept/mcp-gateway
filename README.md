# MCP Gateway

An AI-powered agent platform built on the [Model Context Protocol (MCP)](https://modelcontextprotocol.io). It aggregates multiple real-time tool servers behind one endpoint, runs an orchestrated AI agent (LLaMA 3.3 70B via Groq), and exposes a full chat UI with session history, human-in-the-loop approvals, and document Q&A.

---

## What It Does

```
Browser / API Client
       │
       ▼
┌─────────────────────────────────────────────────────────┐
│                    MCP GATEWAY (:8080)                   │
│                                                         │
│  Chat UI  ·  Dashboard  ·  Auth  ·  Approval Flow       │
│                                                         │
│  ┌─────────────────────────────────────────────────┐   │
│  │              AI Orchestrator (Groq)              │   │
│  │  Plan → Execute (parallel) → Retry → Synthesize  │   │
│  └─────────────────────────────────────────────────┘   │
│         │          │         │         │        │       │
└─────────┼──────────┼─────────┼─────────┼────────┼───────┘
          ▼          ▼         ▼         ▼        ▼
       Weather    Crypto    GitHub     News    Documents
       :3001      :3002     :3003      :3004    :3008
      (wttr.in) (CoinGecko)(GH API)  (RSS)  (ChromaDB RAG)
```

---

## Features

### 🤖 AI Agent
- **Multi-step orchestration** — plans tasks, executes up to 6 in parallel, synthesizes results
- **Automatic retry** — if a tool fails, the AI replans with a different tool automatically
- **Conversation memory** — remembers previous messages (MongoDB or in-memory fallback)
- **Human-in-the-loop** — risky actions require user approval before executing
- **Self-correction** — on tool errors, suggests and tries alternative approaches

### 🛠️ 17 Real Tools
| Category | Tools |
|---|---|
| **Weather** | `get_weather`, `get_forecast` — real-time via wttr.in |
| **Crypto** | `get_crypto_price`, `get_top_cryptos` — live via CoinGecko |
| **GitHub** | `get_user`, `list_repos`, `get_repo` — real GitHub API |
| **News** | `get_top_news`, `search_news` — live RSS feeds |
| **Search** | `web_search` (DuckDuckGo), `wikipedia_summary` |
| **Notes** | `add_note`, `list_notes`, `search_notes` — SQLite |
| **URLs** | `shorten_url`, `generate_qr`, `expand_url` |
| **Documents** | `upload_document`, `ask_document`, `list_documents` — RAG |

### 💬 Chat UI
- Session-based chat with persistent history
- Mobile-friendly — portrait mode sidebar with backdrop overlay
- Silent JWT token refresh (no re-login required)
- Markdown rendering with syntax highlighting
- Approval prompts inline in the chat

### 📄 Document RAG
- Real vector embeddings using **all-MiniLM-L6-v2** (384 dimensions)
- **ChromaDB** vector store with cosine similarity search
- Smart chunking (1200 chars) with 150-char overlap between chunks
- PDF support via `pdfplumber`
- Hallucination guard — returns `NO_RELEVANT_PASSAGES` instead of guessing

### 🔐 Security
- JWT authentication (7-day tokens + silent refresh)
- Per-IP rate limiting on auth endpoints
- SSRF protection on URL expansion
- XSS-safe template rendering
- CORS preflight handling

### 📊 Dashboard
- Live server status with latency and tool count
- Request logs with filtering
- Aggregate stats (requests, latency, error rate)
- Interactive tool tester

---

## Quick Start

```bash
# Clone
git clone https://github.com/Varun7sept/mcp-gateway.git
cd mcp-gateway

# Set your Groq API key (free at console.groq.com)
export GROQ_API_KEY=your_key_here

# Start everything
./start.sh

# Open the chat UI
open http://localhost:8080/chat

# Open the dashboard
open http://localhost:8080
```

### With MongoDB (persistent chat history + auth)
```bash
export MONGODB_URI=mongodb+srv://...
export JWT_SECRET=your_secret_here
./start.sh
```

Without MongoDB the app still works fully — chat history is stored in memory per session.

---

## Architecture

### AI Orchestrator Flow
```
User message
     │
     ▼
DecomposeGoal()        ← LLM plans which tools to call + order
     │
     ▼
checkApprovals()       ← pause if any tool is in the risky list
     │
     ▼
ExecutePlan()          ← parallel execution with topological ordering
     │
     ├── task failed? → suggestAlternative() → retry with new tool
     │
     ▼
compileResults()       ← LLM synthesizes tool outputs into final answer
     │
     ▼
retry loop             ← if still incomplete, replan with different tools
```

### Project Structure
```
mcp-gateway/
├── main.go                          # Entry point
├── config.yaml                      # Server configuration
├── start.sh                         # One-command startup
├── internal/
│   ├── ai/
│   │   ├── brain.go                 # Groq API client + tool definitions
│   │   ├── orchestrator.go          # Plan → execute → synthesize loop
│   │   ├── planner.go               # Goal decomposition + task graph
│   │   ├── executor.go              # Parallel task execution (thread-safe)
│   │   ├── agent.go                 # Single-step agent loop
│   │   └── memory.go                # Cross-session memory store
│   ├── approval/
│   │   └── store.go                 # Human-in-the-loop approval (channel-based)
│   ├── auth/
│   │   ├── auth.go                  # JWT + bcrypt + MongoDB user store
│   │   ├── chat.go                  # Chat session + message persistence
│   │   └── middleware.go            # Auth middleware
│   ├── gateway/
│   │   ├── gateway.go               # Server registry + tool aggregation
│   │   ├── healthcheck.go           # Periodic health checks
│   │   └── forwarder.go             # Request forwarding
│   ├── server/
│   │   ├── server.go                # HTTP server + all API routes
│   │   ├── chat.go                  # Chat handler + session management
│   │   ├── chatui.go                # Embedded chat UI (HTML/CSS/JS)
│   │   └── dashboard.go             # Embedded dashboard UI
│   ├── mcpserver/
│   │   ├── weather.go               # Weather MCP server
│   │   ├── crypto.go                # Crypto MCP server
│   │   ├── github.go                # GitHub MCP server
│   │   ├── news.go                  # News MCP server
│   │   ├── search.go                # Web search + Wikipedia
│   │   └── urltools.go              # URL shortener + QR + expand
│   ├── notes/
│   │   └── notes.go                 # Notes MCP server (SQLite)
│   └── logger/
│       └── logger.go                # Request logging + stats
└── examples/
    └── docs-server/
        └── server.py                # Python RAG server (ChromaDB + Flask)
```

---

## API Reference

### Auth
| Endpoint | Method | Description |
|---|---|---|
| `/api/auth/signup` | POST | Create account |
| `/api/auth/login` | POST | Login, returns JWT |
| `/api/auth/refresh` | POST | Refresh JWT token |

### Chat
| Endpoint | Method | Description |
|---|---|---|
| `/api/chat` | POST | Send message to AI agent |
| `/api/sessions` | GET | List chat sessions |
| `/api/sessions` | POST | Create new session |
| `/api/sessions/:id` | DELETE | Delete session |
| `/api/sessions/:id/messages` | GET | Get session messages |

### Approvals
| Endpoint | Method | Description |
|---|---|---|
| `/api/approvals` | GET | List pending approvals |
| `/api/approvals/:id/approve` | POST | Approve an action |
| `/api/approvals/:id/reject` | POST | Reject an action |

### Gateway
| Endpoint | Method | Description |
|---|---|---|
| `/health` | GET | Gateway health |
| `/api/servers` | GET | Server status + latency |
| `/api/tools` | GET | All available tools |
| `/api/logs` | GET | Recent request logs |
| `/api/stats` | GET | Aggregate statistics |
| `/mcp/message` | POST | MCP JSON-RPC endpoint |

---

## Tech Stack

| Layer | Technology |
|---|---|
| **Language** | Go 1.22 |
| **AI Model** | LLaMA 3.3 70B via Groq API (free tier) |
| **Vector DB** | ChromaDB + all-MiniLM-L6-v2 embeddings |
| **Database** | MongoDB (auth + chat history) · SQLite (notes) |
| **Auth** | JWT (golang-jwt/jwt/v5) + bcrypt |
| **Protocol** | MCP — JSON-RPC over HTTP |
| **Frontend** | Embedded HTML/CSS/JS (no build step, no framework) |
| **RAG server** | Python · Flask · pdfplumber |
| **Deployment** | Render (auto-deploy from GitHub) |

---

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `GROQ_API_KEY` | ✅ | Groq API key (free at console.groq.com) |
| `MONGODB_URI` | Optional | MongoDB connection string for auth + history |
| `JWT_SECRET` | Optional | JWT signing secret (auto-generated if not set) |
| `PORT` | Optional | HTTP port (default: 8080) |
| `GROQ_MODELS` | Optional | Comma-separated model list for fallback |

---

## License

MIT
