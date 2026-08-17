package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gateway/internal/config"
	"gateway/internal/db"
	"gateway/internal/httpx"
	"gateway/internal/secrets"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("Starting Memory Gateway...")

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Config loaded: host=%s port=%d", cfg.Server.Host, cfg.Server.Port)

	// Open database
	database, err := db.Open(cfg.Database.GlobalDBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	log.Printf("Database opened: %s", cfg.Database.GlobalDBPath)

	// Run migrations
	schemaPath := filepath.Join(filepath.Dir(cfg.Database.GlobalDBPath), "..", "gateway", "schema", "schema.sql")
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		// Try alternative path
		schemaPath = "F:\\memory_plus\\gateway\\schema\\schema.sql"
	}

	log.Printf("Running migrations from: %s", schemaPath)
	if err := db.Migrate(database.Global, schemaPath); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	log.Println("Migrations completed successfully")

	// Check journal mode
	journalMode, err := database.GetJournalMode()
	if err != nil {
		log.Fatalf("Failed to get journal mode: %v", err)
	}
	log.Printf("Database journal mode: %s", journalMode)

	if journalMode != "wal" {
		log.Println("WARNING: Journal mode is not WAL")
	}

	// Initialize secrets manager
	secretsManager, err := secrets.NewManager(cfg.Secrets.SecretsDir, cfg.Secrets.UseStub)
	if err != nil {
		log.Fatalf("Failed to initialize secrets manager: %v", err)
	}

	log.Println("Secrets manager initialized")

	// Load models
	models, err := config.LoadModels(cfg.Models.ModelsJSONPath)
	if err != nil {
		log.Fatalf("Failed to load models: %v", err)
	}

	log.Printf("Loaded %d models from %s", len(models), cfg.Models.ModelsJSONPath)

	// TODO: Import models into upstream_channels table
	// This will be implemented in the admin handler

	// Setup HTTP router
	router := httpx.SetupRouter(database.Global, secretsManager)

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("Starting server on %s", addr)

	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
