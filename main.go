package main

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/ai"
	"github.com/varunbanda/mcp-gateway/internal/auth"
	"github.com/varunbanda/mcp-gateway/internal/config"
	"github.com/varunbanda/mcp-gateway/internal/gateway"
	"github.com/varunbanda/mcp-gateway/internal/logger"
	"github.com/varunbanda/mcp-gateway/internal/mcpserver"
	"github.com/varunbanda/mcp-gateway/internal/notes"
	"github.com/varunbanda/mcp-gateway/internal/server"
)

func main() {
	log.Println("Starting MCP Gateway...")

	// Load configuration
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	log.Printf("Loaded config: %d servers configured", len(cfg.Servers))

	// Create the gateway
	gw := gateway.New(cfg)
	log.Printf("Gateway initialized with %d servers", len(gw.ListServers()))

	// Create request logger (keeps last 1000 requests)
	reqLogger := logger.New(1000)

	// Create AI brain (optional — needs GROQ_API_KEY)
	var brain *ai.Brain
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey != "" {
		brain = ai.New(groqKey)
		log.Println("AI Chat enabled (Groq API)")
	} else {
		log.Println("AI Chat disabled (set GROQ_API_KEY to enable)")
	}

	// Create auth handler (MongoDB + JWT)
	var authenticator *auth.Auth
	if cfg.MongoDB.URI != "" {
		var err error
		authenticator, err = auth.New(auth.MongoConfig{
			URI:      cfg.MongoDB.URI,
			Database: cfg.MongoDB.Database,
		})
		if err != nil {
			log.Printf("WARNING: MongoDB auth not available: %v", err)
			log.Println("Proceeding without authentication...")
		} else {
			log.Println("MongoDB connected — authentication enabled")
		}
	} else {
		log.Println("MongoDB not configured — authentication disabled")
	}

	// Start embedded MCP servers (no separate processes needed)
	startMCP := func(name string, fn func() error) {
		go func() {
			if err := fn(); err != nil {
				log.Printf("%s server exited: %v", name, err)
			}
		}()
	}
	if s, err := notes.New(":3002"); err == nil { startMCP("notes", s.Start); defer s.Close() }
	startMCP("weather", func() error { return mcpserver.StartWeather(":3001") })
	startMCP("github", func() error { return mcpserver.StartGitHub(":3003") })
	startMCP("crypto", func() error { return mcpserver.StartCrypto(":3004") })
	startMCP("news", func() error { return mcpserver.StartNews(":3005") })
	startMCP("url-tools", func() error { return mcpserver.StartURLTools(":3006") })
	startMCP("search", func() error { return mcpserver.StartSearch(":3007") })

	// Start Python RAG server (ChromaDB + Flask)
	startMCP("documents", func() error {
		cmd := exec.Command("python3", "examples/docs-server/server.py")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	})

	// Start health checker (every 10 seconds)
	gw.StartHealthChecker(10 * time.Second)

	// Start HTTP server (use PORT env var for Fly.io/Railway compatibility)
	port := cfg.Gateway.Port
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	srv := server.New(gw, reqLogger, brain, authenticator, port)
	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
