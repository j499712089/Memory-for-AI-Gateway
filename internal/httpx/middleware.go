package httpx

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

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

// requireScope is a route middleware asserting the authenticated API key's
// scopes include the named scope. It must run behind AuthMiddleware. The
// admin scope uses the flat admin error shape (no request_id), matching the
// admin/system/identity handlers; other scopes reuse the request_id-bearing
// errorResponse used by the MCP and gateway handlers.
func requireScope(database *sql.DB, scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKeyID, ok := GetAPIKeyID(c)
		if !ok {
			writeScopeError(c, scope, http.StatusUnauthorized, "unauthorized", "missing api key context")
			c.Abort()
			return
		}
		var scopesJSON string
		err := database.QueryRowContext(c.Request.Context(), `SELECT scopes FROM api_keys WHERE id = ?`, apiKeyID).Scan(&scopesJSON)
		if err != nil {
			writeScopeError(c, scope, http.StatusUnauthorized, "unauthorized", "api key not found")
			c.Abort()
			return
		}
		var scopes []string
		if json.Unmarshal([]byte(scopesJSON), &scopes) != nil {
			writeScopeError(c, scope, http.StatusForbidden, "forbidden", "api key scopes unreadable")
			c.Abort()
			return
		}
		for _, s := range scopes {
			if s == scope {
				c.Next()
				return
			}
		}
		writeScopeError(c, scope, http.StatusForbidden, "forbidden", "api key scopes do not include '"+scope+"'")
		c.Abort()
	}
}

func writeScopeError(c *gin.Context, scope string, status int, errType, message string) {
	if scope == "admin" {
		c.JSON(status, gin.H{"error": gin.H{"type": errType, "message": message}})
		return
	}
	errorResponse(c, status, errType, message)
}

// IdempotencyMiddleware extracts the HTTP idempotency key and removes expired
// reservations. The handler performs body-aware conflict detection and replay.
func IdempotencyMiddleware(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if db != nil {
			_, _ = db.Exec("DELETE FROM idempotency_keys WHERE expires_at <= ?", time.Now().UTC().Format(time.RFC3339Nano))
		}

		// Get idempotency key from header
		idempotencyKey := c.GetHeader("Idempotency-Key")
		if idempotencyKey == "" {
			// Also check lowercase variant used by Responses protocol
			idempotencyKey = c.GetHeader("idempotency-key")
		}

		if idempotencyKey != "" {
			// Store in context for later use by handler
			c.Set("idempotency_key", idempotencyKey)

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
