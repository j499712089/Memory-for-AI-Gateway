package httpx

import (
	"database/sql"

	"gateway/internal/auth"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

// SetupRouter configures the HTTP router with all endpoints
func SetupRouter(db *sql.DB, secretsManager *secrets.Manager, memoryRoot string) *gin.Engine {
	// Create router
	r := gin.Default()

	// Create handlers
	healthHandler := NewHealthHandler(db)
	adminHandler := NewAdminHandler(db, secretsManager)
	gatewayHandler := NewGatewayHandler(db, secretsManager, memoryRoot)
	mcpHandler := NewMCPHandler(db, memoryRoot)

	// Create auth middleware
	authMgr := auth.NewMiddleware(db)

	// Health endpoint (no auth required)
	r.GET("/health", healthHandler.HandleHealth)

	// API group (requires auth)
	api := r.Group("/api")
	api.Use(AuthMiddleware(authMgr))
	{
		// Teams
		api.GET("/teams", adminHandler.HandleListTeams)
		api.POST("/teams", adminHandler.HandleCreateTeam)
		api.GET("/teams/:team_id", adminHandler.HandleGetTeam)

		// API Keys
		api.GET("/api-keys", adminHandler.HandleListAPIKeys)
		api.POST("/api-keys", adminHandler.HandleCreateAPIKey)
		api.GET("/recording-health", healthHandler.HandleRecordingHealth)

		// MCP internal service API (consumed by the MCP Server :8097).
		// Auth inherited from the /api group; scope check is route-local.
		mcp := api.Group("/mcp")
		mcp.Use(requireMCPScope(db))
		{
			mcp.POST("/memory/search", mcpHandler.HandleMemorySearch)
			mcp.POST("/memory/get", mcpHandler.HandleMemoryGet)
			mcp.POST("/memory/append", mcpHandler.HandleMemoryAppend)
			mcp.POST("/wiki/search", mcpHandler.HandleWikiSearch)
			mcp.POST("/codegraph/impact", mcpHandler.HandleCodeGraphImpact)
			mcp.POST("/skill/search", mcpHandler.HandleSkillSearch)
			mcp.POST("/binding/get", mcpHandler.HandleBindingGet)
			mcp.POST("/assets/list", mcpHandler.HandleAssetsList)
		}
	}

	// Gateway LLM endpoints (requires auth + idempotency)
	gateway := r.Group("")
	gateway.Use(AuthMiddleware(authMgr))
	gateway.Use(IdempotencyMiddleware(db))
	{
		// Anthropic Messages
		gateway.POST("/claude-code/:channel/v1/messages", gatewayHandler.HandleAnthropicMessages)
		gateway.POST("/v1/messages", gatewayHandler.HandleAnthropicMessages)

		// Chat Completions
		gateway.POST("/codebuddy/:channel/v1/chat/completions", gatewayHandler.HandleChatCompletions)

		// Responses
		gateway.POST("/codex/:channel/v1/responses", gatewayHandler.HandleResponses)
		// Responses (without /v1 - for Codex Runtime compatibility)
		gateway.POST("/codex/:channel/responses", gatewayHandler.HandleResponses)

		// DSH (defaults to chat_completions)
		gateway.POST("/dsh/:channel/v1/chat/completions", gatewayHandler.HandleChatCompletions)
	}

	return r
}
