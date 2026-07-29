package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/varunbanda/mcp-gateway/internal/ai"
	"github.com/varunbanda/mcp-gateway/internal/approval"
	"github.com/varunbanda/mcp-gateway/internal/auth"
	"github.com/varunbanda/mcp-gateway/internal/config"
	"github.com/varunbanda/mcp-gateway/internal/conversation"
	"github.com/varunbanda/mcp-gateway/internal/gateway"
	"github.com/varunbanda/mcp-gateway/internal/logger"
	"github.com/varunbanda/mcp-gateway/internal/memory"
	"github.com/varunbanda/mcp-gateway/internal/mcpserver"
	"github.com/varunbanda/mcp-gateway/internal/notes"
	"github.com/varunbanda/mcp-gateway/internal/server"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
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

	// Connect to MongoDB for memory, auth, and conversation storage
	var mongoClient *mongo.Client
	if cfg.MongoDB.URI != "" {
		mongoClient, err = mongo.Connect(context.Background(), options.Client().ApplyURI(cfg.MongoDB.URI))
		if err != nil {
			log.Printf("WARNING: MongoDB connection failed: %v", err)
			mongoClient = nil
		} else {
			err = mongoClient.Ping(context.Background(), nil)
			if err != nil {
				log.Printf("WARNING: MongoDB ping failed: %v", err)
				mongoClient = nil
			} else {
				log.Println("MongoDB connected")
			}
		}
	}

	// Create memory subsystem (retrieval memory)
	var memStore memory.MemoryStore
	if mongoClient != nil {
		groqKey := os.Getenv("GROQ_API_KEY")
		if groqKey == "" {
			groqKey = cfg.Memory.GroqAPIKey
		}

		embedder := memory.NewGroqEmbeddingGenerator(groqKey, cfg.Memory.EmbeddingModel)

		qdrantClient := memory.NewQdrantClient(
			cfg.Memory.QdrantURL,
			cfg.Memory.QdrantAPIKey,
			cfg.Memory.QdrantCollection,
			1536,
		)

		memStore = memory.NewMongoDBStore(
			mongoClient,
			cfg.MongoDB.Database,
			cfg.Memory.MongoDBMemoryCollection,
			nil,
			embedder,
			qdrantClient,
		)
		log.Println("Retrieval memory subsystem initialized (MongoDB + Qdrant)")
	} else {
		log.Println("Retrieval memory disabled (MongoDB not configured)")
	}

	// Create memory for AI brain
	var brain *ai.Brain
	groqKey := os.Getenv("GROQ_API_KEY")
	if groqKey != "" {
		brain = ai.New(groqKey)
		if memStore != nil {
			brain.WithMemory(memStore)
		}
		log.Println("AI Chat enabled (Groq API) with retrieval memory")
	} else {
		log.Println("AI Chat disabled (set GROQ_API_KEY to enable)")
	}

	// Create conversation history store
	var convStore conversation.ConversationStore
	if mongoClient != nil {
		convStore = conversation.NewMongoDBStore(mongoClient, cfg.MongoDB.Database)
		log.Println("Conversation history store initialized (MongoDB)")
	}

	// Create auth handler (MongoDB + JWT)
	var authenticator *auth.Auth
	if cfg.MongoDB.URI != "" && mongoClient != nil {
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
	pythonCmd := "python3"
	if _, err := exec.LookPath("python3"); err != nil {
		pythonCmd = "python"
	}
	startMCP("documents", func() error {
		cmd := exec.Command(pythonCmd, "examples/docs-server/server.py")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	})

	// Create a context that cancels on SIGINT/SIGTERM for graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Start health checker (every 10 seconds)
	gw.StartHealthChecker(ctx, 10*time.Second)

	// Start HTTP server (use PORT env var for Fly.io/Railway compatibility)
	port := cfg.Gateway.Port
	if p := os.Getenv("PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			port = n
		}
	}
	// Create approval store for human-in-the-loop (5 min timeout)
	approvalStore := approval.NewStore(5 * time.Minute)
	log.Println("Human-in-the-loop approval store initialized")

	srv := server.New(gw, reqLogger, brain, authenticator, port)
	srv.WithApprovalStore(approvalStore)
	if convStore != nil {
		srv.WithConversationStore(convStore, &cfg.Conversation)
	}
	if err := srv.Start(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
