// Package config reads the gateway's YAML configuration file.
//
// WHAT THIS DOES:
// - Reads config.yaml from disk
// - Converts it into Go structs (structured data) we can use in code
// - If the file is missing or broken, it returns an error
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ServerConfig holds info about one downstream MCP server.
// Think of it as one row in our "list of servers" table.
type ServerConfig struct {
	Name    string `yaml:"name"`    // Human-readable name like "github"
	URL     string `yaml:"url"`     // Where the server is running
	Enabled bool   `yaml:"enabled"` // Is it active?
}

// GatewayConfig holds the gateway's own settings.
type GatewayConfig struct {
	Port int    `yaml:"port"` // Port to listen on (e.g., 8080)
	Name string `yaml:"name"` // Display name
}

// MongoConfig holds MongoDB connection settings.
type MongoConfig struct {
	URI      string `yaml:"uri"`
	Database string `yaml:"database"`
}

// Config is the top-level structure that maps to the entire YAML file.
type Config struct {
	Gateway  GatewayConfig  `yaml:"gateway"`
	MongoDB  MongoConfig    `yaml:"mongodb"`
	Servers  []ServerConfig `yaml:"servers"`
}

// Load reads a YAML file from the given path and returns a Config.
//
// Example usage:
//
//	cfg, err := config.Load("config.yaml")
//	if err != nil { ... handle error ... }
//	fmt.Println(cfg.Gateway.Port) // 8080
func Load(path string) (*Config, error) {
	// Step 1: Read the file from disk (returns raw bytes)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Step 2: Parse the YAML bytes into our Config struct
	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Step 3: Env var overrides (so you can use different MongoDB for local vs production)
	if envURI := os.Getenv("MONGO_URI"); envURI != "" {
		cfg.MongoDB.URI = envURI
	}
	if envDB := os.Getenv("MONGO_DATABASE"); envDB != "" {
		cfg.MongoDB.Database = envDB
	}

	// Step 4: Basic validation
	if cfg.Gateway.Port == 0 {
		cfg.Gateway.Port = 8080 // Default port
	}

	return &cfg, nil
}
