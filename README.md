# MCP Gateway

A reverse proxy and aggregation layer for MCP (Model Context Protocol) servers. Routes AI tool calls to the correct downstream server, provides health monitoring, request logging, and a real-time dashboard.

## What It Does

```
AI Client ──→ MCP Gateway ──→ Weather Server (real wttr.in API)
                           ──→ Notes Server (real SQLite database)
                           ──→ GitHub Server (real GitHub API)
```

- **Tool Aggregation**: Merges tools from multiple MCP servers into one unified list
- **Intelligent Routing**: Automatically routes tool calls to the correct server
- **Health Monitoring**: Pings servers every 10 seconds, tracks uptime and latency
- **Request Logging**: Records every request with latency and status
- **Real-time Dashboard**: Web UI showing live traffic, server status, and stats
- **Try It Live**: Interactive buttons to test tools directly from the dashboard

## Quick Start

```bash
# Clone the repo
git clone https://github.com/YOUR_USERNAME/mcp-gateway.git
cd mcp-gateway

# Start everything (builds + launches all servers)
./start.sh

# Open dashboard
open http://localhost:8080
```

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  MCP GATEWAY (:8080)                 │
│                                                     │
│  ┌──────────┐ ┌──────────┐ ┌─────────┐ ┌────────┐ │
│  │  Router  │ │  Health  │ │ Logger  │ │ Dash-  │ │
│  │          │ │  Checker │ │         │ │ board  │ │
│  └──────────┘ └──────────┘ └─────────┘ └────────┘ │
└──────────┬────────────────────┬───────────────┬────┘
           │                    │               │
    ┌──────▼──────┐   ┌────────▼─────┐  ┌──────▼──────┐
    │   Weather   │   │    Notes     │  │   GitHub    │
    │   :3001     │   │    :3002     │  │   :3003     │
    │  (wttr.in)  │   │   (SQLite)   │  │ (GitHub API)│
    └─────────────┘   └──────────────┘  └─────────────┘
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Dashboard UI |
| `/health` | GET | Gateway health check |
| `/api/servers` | GET | List all servers with status |
| `/api/tools` | GET | List all aggregated tools |
| `/api/logs` | GET | Recent request logs |
| `/api/stats` | GET | Aggregate statistics |
| `/mcp/message` | POST | MCP JSON-RPC endpoint |

## Available Tools (8 total)

| Tool | Server | Description |
|------|--------|-------------|
| `get_weather` | weather | Real current weather for any city |
| `get_forecast` | weather | Real 3-day forecast |
| `add_note` | notes | Save a note to SQLite database |
| `list_notes` | notes | List all saved notes |
| `search_notes` | notes | Search notes by keyword |
| `get_user` | github | Real GitHub user profile |
| `list_repos` | github | Real GitHub repositories |
| `get_repo` | github | Real GitHub repo details |

## Configuration

Edit `config.yaml` to add or remove servers:

```yaml
gateway:
  port: 8080
  name: "MCP Gateway"

servers:
  - name: "weather"
    url: "http://localhost:3001"
    enabled: true

  - name: "notes"
    url: "http://localhost:3002"
    enabled: true

  - name: "github"
    url: "http://localhost:3003"
    enabled: true
```

## Tech Stack

- **Language**: Go
- **Protocol**: MCP (JSON-RPC over HTTP)
- **Database**: SQLite (for notes server)
- **Dashboard**: Embedded HTML/CSS/JS (no build step)
- **External APIs**: wttr.in (weather), GitHub API

## Project Structure

```
mcp-gateway/
├── main.go                     # Entry point
├── config.yaml                 # Server configuration
├── start.sh                    # One-command startup script
├── internal/
│   ├── config/config.go        # YAML config reader
│   ├── gateway/
│   │   ├── gateway.go          # Core: server registry + tool aggregation
│   │   ├── healthcheck.go      # Periodic server health checks
│   │   └── forwarder.go        # Request forwarding to downstream servers
│   ├── server/
│   │   ├── server.go           # HTTP server + API endpoints
│   │   └── dashboard.go        # Embedded dashboard HTML
│   └── logger/logger.go        # Request logging + stats
├── examples/
│   ├── weather-server/         # Real weather API MCP server
│   ├── notes-server/           # SQLite-backed notes MCP server
│   └── github-server/          # Real GitHub API MCP server
```

## License

MIT
