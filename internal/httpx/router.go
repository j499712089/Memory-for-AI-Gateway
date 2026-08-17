package httpx

import (
	"database/sql"

	"gateway/internal/auth"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

// SetupRouter configures the HTTP router with all endpoints
func SetupRouter(db *sql.DB, secretsManager *secrets.Manager) *gin.Engine {
	// Create router
	r := gin.Default()

	// Create handlers
	healthHandler := NewHealthHandler(db)
	adminHandler := NewAdminHandler(db, secretsManager)

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
	}

	return r
}
