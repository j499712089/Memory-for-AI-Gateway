package httpx

import (
	"net/http"

	"gateway/internal/auth"

	"github.com/gin-gonic/gin"
)

// AuthMiddleware provides authentication for API endpoints
func AuthMiddleware(authMgr *auth.Middleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Extract token from Authorization header or x-api-key header
		token := c.GetHeader("Authorization")
		if token == "" {
			token = c.GetHeader("x-api-key")
			if token != "" {
				token = "Bearer " + token
			}
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"type":    "authentication_error",
					"message": "Missing API key",
				},
			})
			c.Abort()
			return
		}

		// Validate token
		teamID, apiKeyID, err := authMgr.ValidateAPIKey(token)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"type":    "authentication_error",
					"message": "Invalid or expired API key",
				},
			})
			c.Abort()
			return
		}

		// Store in context
		c.Set("team_id", teamID)
		c.Set("api_key_id", apiKeyID)
		c.Next()
	}
}

// OptionalAuthMiddleware provides optional authentication (doesn't abort on missing auth)
func OptionalAuthMiddleware(authMgr *auth.Middleware) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			token = c.GetHeader("x-api-key")
			if token != "" {
				token = "Bearer " + token
			}
		}

		if token != "" {
			teamID, apiKeyID, err := authMgr.ValidateAPIKey(token)
			if err == nil {
				c.Set("team_id", teamID)
				c.Set("api_key_id", apiKeyID)
			}
		}

		c.Next()
	}
}

// GetTeamID retrieves the team_id from the context
func GetTeamID(c *gin.Context) (string, bool) {
	teamID, exists := c.Get("team_id")
	if !exists {
		return "", false
	}
	return teamID.(string), true
}

// GetAPIKeyID retrieves the api_key_id from the context
func GetAPIKeyID(c *gin.Context) (string, bool) {
	apiKeyID, exists := c.Get("api_key_id")
	if !exists {
		return "", false
	}
	return apiKeyID.(string), true
}

// IdempotencyMiddleware checks for duplicate requests using Idempotency-Key header
// Phase 2: Basic implementation that checks idempotency_keys table
func IdempotencyMiddleware(db interface{}) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get idempotency key from header
		idempotencyKey := c.GetHeader("Idempotency-Key")
		if idempotencyKey == "" {
			// Also check lowercase variant used by Responses protocol
			idempotencyKey = c.GetHeader("idempotency-key")
		}

		if idempotencyKey != "" {
			// Store in context for later use by handler
			c.Set("idempotency_key", idempotencyKey)

			// Phase 2: Basic check - actual idempotency logic will be in gateway handler
			// This middleware just extracts and stores the key
		}

		c.Next()
	}
}

// GetIdempotencyKey retrieves the idempotency_key from the context
func GetIdempotencyKey(c *gin.Context) (string, bool) {
	key, exists := c.Get("idempotency_key")
	if !exists {
		return "", false
	}
	return key.(string), true
}
