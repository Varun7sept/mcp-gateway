#!/bin/bash
# MCP Gateway — Start all components
# Usage: export GROQ_API_KEY="gsk_..." && ./start.sh

set -e

if [ -z "$GROQ_API_KEY" ]; then
    echo "WARNING: GROQ_API_KEY not set. AI Chat will be disabled."
    echo "Run: export GROQ_API_KEY=\"gsk_your_key_here\""
    echo ""
fi

if [ -z "$JWT_SECRET" ]; then
    echo "WARNING: JWT_SECRET not set. Using default (not for production)."
    echo "Run: export JWT_SECRET=\"your-random-secret\""
    echo ""
fi

echo "NOTE: MongoDB must be running on localhost:27017 for authentication."
echo "      To disable auth, set mongodb.uri to empty in config.yaml."
echo ""

echo "Building all components..."
go build -o mcp-gateway .
go build -o examples/weather-server/weather-server ./examples/weather-server/
CGO_ENABLED=1 go build -o examples/notes-server/notes-server ./examples/notes-server/
go build -o examples/github-server/github-server ./examples/github-server/
go build -o examples/crypto-server/crypto-server ./examples/crypto-server/
go build -o examples/news-server/news-server ./examples/news-server/
go build -o examples/url-server/url-server ./examples/url-server/
go build -o examples/search-server/search-server ./examples/search-server/
# docs-server is Python (ChromaDB + HuggingFace) — no build needed

echo ""
echo "Stopping any existing processes..."
lsof -ti:3001 -ti:3002 -ti:3003 -ti:3004 -ti:3005 -ti:3006 -ti:3007 -ti:3008 -ti:8080 2>/dev/null | xargs kill 2>/dev/null || true
sleep 1

echo "Starting 8 MCP servers..."
./examples/weather-server/weather-server &
./examples/notes-server/notes-server &
./examples/github-server/github-server &
./examples/crypto-server/crypto-server &
./examples/news-server/news-server &
./examples/url-server/url-server &
./examples/search-server/search-server &
python3 ./examples/docs-server/server.py &
sleep 2

echo "Starting MCP Gateway..."
./mcp-gateway &
sleep 2

echo ""
echo "================================================"
echo "  MCP Gateway is running!"
echo ""
echo "  Dashboard:  http://localhost:8080"
echo "  AI Chat:    http://localhost:8080/chat"
echo ""
echo "  8 servers | 20 tools | Multi-step Agent | RAG"
echo "================================================"
echo ""
echo "Press Ctrl+C to stop all servers."

trap 'echo ""; echo "Shutting down..."; kill $(jobs -p) 2>/dev/null; exit 0' INT
wait
