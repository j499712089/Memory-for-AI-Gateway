package httpx

import (
	"database/sql"
	"net/http"

	"gateway/internal/auth"
	"gateway/internal/embedding"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

// SetupRouter configures the HTTP router with all endpoints
func SetupRouter(db *sql.DB, secretsManager *secrets.Manager, memoryRoot string, services ...*embedding.Service) *gin.Engine {
	// Create router
	r := gin.Default()

	// Create handlers
	healthHandler := NewHealthHandler(db)
	adminHandler := NewAdminHandler(db, secretsManager)
	gatewayHandler := NewGatewayHandler(db, secretsManager, memoryRoot, services...)
	mcpHandler := NewMCPHandler(db, memoryRoot, services...)
	systemHandler := NewSystemHandler(db)
	identityHandler := NewIdentityHandler(db)

	// Create auth middleware
	authMgr := auth.NewMiddleware(db)

	// Health endpoint (no auth required)
	r.GET("/health", healthHandler.HandleHealth)

	// API documentation (OpenAPI spec + Swagger UI). Only the OpenAPI spec and
	// the self-contained Swagger UI assets are served from the embedded docs
	// package; the rest of the docs directory (architecture, database schema,
	// deployment guides) is not exposed (ALL-272). Access Swagger UI at
	// /docs/swagger-ui/.
	docsGroup := r.Group("/docs")
	{
		docsGroup.GET("/openapi.yaml", serveDocFile("openapi.yaml"))
		docsGroup.GET("/swagger-ui/", serveDocFile("swagger-ui/index.html"))
		docsGroup.GET("/swagger-ui", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, "/docs/swagger-ui/") })
		for _, asset := range []string{
			"swagger-ui/index.html",
			"swagger-ui/swagger-ui.css",
			"swagger-ui/swagger-ui-bundle.js",
			"swagger-ui/swagger-ui-standalone-preset.js",
			"swagger-ui/swagger-ui-bundle.js.map",
			"swagger-ui/swagger-ui.css.map",
			"swagger-ui/favicon-16x16.png",
			"swagger-ui/favicon-32x32.png",
		} {
			docsGroup.GET("/"+asset, serveDocFile(asset))
		}
	}

	// API group (requires auth)
	api := r.Group("/api")
	api.Use(AuthMiddleware(authMgr))
	{
		// Admin-scoped management routes. Scope check runs behind
		// AuthMiddleware and rejects keys lacking the "admin" scope (ALL-272).
		admin := api.Group("")
		admin.Use(requireScope(db, "admin"))
		{
			// Teams
			admin.GET("/teams", adminHandler.HandleListTeams)
			admin.POST("/teams", adminHandler.HandleCreateTeam)
			admin.GET("/teams/:team_id", adminHandler.HandleGetTeam)

			// API Keys
			admin.GET("/api-keys", adminHandler.HandleListAPIKeys)
			admin.POST("/api-keys", adminHandler.HandleCreateAPIKey)
			admin.GET("/recording-health", healthHandler.HandleRecordingHealth)

			// System Management
			admin.POST("/system/service/start", systemHandler.HandleServiceStart)
			admin.POST("/system/service/stop", systemHandler.HandleServiceStop)
			admin.POST("/system/service/restart", systemHandler.HandleServiceRestart)
			admin.GET("/system/service/status", systemHandler.HandleServiceStatus)
			admin.PUT("/system/autostart", systemHandler.HandleSetAutostart)
			admin.GET("/system/logs", systemHandler.HandleGetLogs)
			admin.POST("/system/backup", systemHandler.HandleBackup)
			admin.POST("/system/restore", systemHandler.HandleRestore)
			admin.GET("/system/config", systemHandler.HandleGetConfig)

			// Identity Cards - Agent Binding
			admin.POST("/identity-cards/:id/bind-agent", identityHandler.HandleBindAgent)
			admin.POST("/identity-cards/:id/test-connection", identityHandler.HandleTestConnection)
		}

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

	// Gateway LLM endpoints (requires auth + gateway scope + idempotency)
	gateway := r.Group("")
	gateway.Use(AuthMiddleware(authMgr))
	gateway.Use(requireScope(db, "gateway"))
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
