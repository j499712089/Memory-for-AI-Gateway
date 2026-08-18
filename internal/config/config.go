package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"gateway/internal/paths"
)

type Config struct {
	Server   ServerConfig   `json:"server"`
	Database DatabaseConfig `json:"database"`
	Secrets  SecretsConfig  `json:"secrets"`
	Models   ModelsConfig   `json:"models"`
}

type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type DatabaseConfig struct {
	GlobalDBPath string `json:"global_db_path"`
	TeamsDir     string `json:"teams_dir"`
}

type SecretsConfig struct {
	SecretsDir string `json:"secrets_dir"`
	UseStub    bool   `json:"use_stub"` // For non-Windows environments
}

type ModelsConfig struct {
	ModelsJSONPath string `json:"models_json_path"`
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() (*Config, error) {
	baseDir := os.Getenv("MEMORY_PLUS_DIR")
	if baseDir == "" {
		baseDir = "F:\\memory_plus"
	}

	port := 8096
	if portStr := os.Getenv("GATEWAY_PORT"); portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}

	modelsPath := os.Getenv("MODELS_JSON_PATH")
	if modelsPath == "" {
		modelsPath = filepath.Join(baseDir, "00_系统", "models.json")
	}

	cfg := &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: port,
		},
		Database: DatabaseConfig{
			GlobalDBPath: filepath.Join(baseDir, ".runtime", "memory-gateway.db"),
			TeamsDir:     paths.TeamsDir(baseDir),
		},
		Secrets: SecretsConfig{
			SecretsDir: filepath.Join(baseDir, ".runtime", "secrets"),
			UseStub:    os.Getenv("GOOS") != "windows",
		},
		Models: ModelsConfig{
			ModelsJSONPath: modelsPath,
		},
	}

	return cfg, nil
}

// Model represents an LLM model configuration
type Model struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Provider     string                 `json:"provider"`
	Protocol     string                 `json:"protocol"`
	BaseURL      string                 `json:"base_url"`
	Capabilities map[string]interface{} `json:"capabilities"`
	Priority     int                    `json:"priority"`
	Enabled      bool                   `json:"enabled"`
}

type ModelsFile struct {
	Models []Model `json:"models"`
}

// LoadModels loads models from models.json
func LoadModels(path string) ([]Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read models.json: %w", err)
	}

	var modelsFile ModelsFile
	if err := json.Unmarshal(data, &modelsFile); err != nil {
		return nil, fmt.Errorf("failed to parse models.json: %w", err)
	}

	return modelsFile.Models, nil
}
