package httpx

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gateway/internal/idgen"
	"gateway/internal/secrets"

	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	db            *sql.DB
	secretsManager *secrets.Manager
}

func NewAdminHandler(db *sql.DB, secretsManager *secrets.Manager) *AdminHandler {
	return &AdminHandler{
		db:             db,
		secretsManager: secretsManager,
	}
}

// Team represents a team entity
type Team struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Status      string `json:"status"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// CreateTeamRequest represents the request body for creating a team
type CreateTeamRequest struct {
	Name        string `json:"name" binding:"required"`
	Slug        string `json:"slug" binding:"required"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
}

// HandleCreateTeam creates a new team
func (h *AdminHandler) HandleCreateTeam(c *gin.Context) {
	var req CreateTeamRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": err.Error(),
			},
		})
		return
	}

	// Set defaults
	if req.Visibility == "" {
		req.Visibility = "private"
	}

	// Generate ID
	teamID := idgen.NewID()
	now := time.Now().UTC().Format(time.RFC3339)

	// Insert team
	query := `
		INSERT INTO teams (id, name, slug, description, visibility, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'active', ?, ?)
	`
	_, err := h.db.Exec(query, teamID, req.Name, req.Slug, req.Description, req.Visibility, now, now)
	if err != nil {
		// Check for slug conflict
		if strings.Contains(err.Error(), "UNIQUE constraint failed") || strings.Contains(err.Error(), "slug") {
			c.JSON(http.StatusConflict, gin.H{
				"error": gin.H{
					"type":    "conflict",
					"message": "slug already exists",
				},
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to create team",
			},
		})
		return
	}

	team := Team{
		ID:          teamID,
		Name:        req.Name,
		Slug:        req.Slug,
		Description: req.Description,
		Visibility:  req.Visibility,
		Status:      "active",
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	c.JSON(http.StatusCreated, team)
}

// HandleListTeams lists all teams
func (h *AdminHandler) HandleListTeams(c *gin.Context) {
	query := `
		SELECT id, name, slug, description, visibility, status, created_at, updated_at
		FROM teams
		WHERE status != 'archived'
		ORDER BY created_at DESC
	`

	rows, err := h.db.Query(query)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to list teams",
			},
		})
		return
	}
	defer rows.Close()

	teams := []Team{}
	for rows.Next() {
		var team Team
		err := rows.Scan(&team.ID, &team.Name, &team.Slug, &team.Description,
			&team.Visibility, &team.Status, &team.CreatedAt, &team.UpdatedAt)
		if err != nil {
			continue
		}
		teams = append(teams, team)
	}

	c.JSON(http.StatusOK, gin.H{
		"teams": teams,
		"total": len(teams),
	})
}

// HandleGetTeam gets a single team by ID
func (h *AdminHandler) HandleGetTeam(c *gin.Context) {
	teamID := c.Param("team_id")

	query := `
		SELECT id, name, slug, description, visibility, status, created_at, updated_at
		FROM teams
		WHERE id = ?
	`

	var team Team
	err := h.db.QueryRow(query, teamID).Scan(
		&team.ID, &team.Name, &team.Slug, &team.Description,
		&team.Visibility, &team.Status, &team.CreatedAt, &team.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found",
				"message": "team not found",
			},
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to get team",
			},
		})
		return
	}

	c.JSON(http.StatusOK, team)
}

// APIKey represents an API key entity
type APIKey struct {
	ID        string `json:"id"`
	TeamID    string `json:"team_id"`
	KeyHash   string `json:"key_hash_prefix"` // Only first 8 chars
	Scopes    string `json:"scopes"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// CreateAPIKeyRequest represents the request body for creating an API key
type CreateAPIKeyRequest struct {
	TeamID string   `json:"team_id" binding:"required"`
	Scopes []string `json:"scopes"`
}

// CreateAPIKeyResponse includes the plaintext key (one-time only)
type CreateAPIKeyResponse struct {
	ID        string `json:"id"`
	TeamID    string `json:"team_id"`
	Key       string `json:"key"` // Plaintext, only returned once
	KeyHash   string `json:"key_hash_prefix"`
	Scopes    string `json:"scopes"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at"`
}

// HandleCreateAPIKey creates a new API key
func (h *AdminHandler) HandleCreateAPIKey(c *gin.Context) {
	var req CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": err.Error(),
			},
		})
		return
	}

	// Generate API key
	keyID := idgen.NewID()
	plaintextKey := "gw_" + idgen.NewID()

	// Store secret and get hash + ref
	keyHash, keyRef, err := h.secretsManager.StoreSecret(plaintextKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to store API key",
			},
		})
		return
	}

	// Persist the requested scopes. When omitted, fall back to the documented
	// default. Scopes are validated against the declared set so a caller can
	// never mint an undeclared scope (ALL-272).
	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"gateway", "mcp"}
	}
	for _, s := range scopes {
		if s != "gateway" && s != "mcp" && s != "admin" {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"type":    "invalid_request",
					"message": fmt.Sprintf("unknown scope %q (allowed: gateway, mcp, admin)", s),
				},
			})
			return
		}
	}
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to serialize scopes",
			},
		})
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Insert API key
	query := `
		INSERT INTO api_keys (id, team_id, key_hash, key_ref, scopes, enabled, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?)
	`
	_, err = h.db.Exec(query, keyID, req.TeamID, keyHash, keyRef, string(scopesJSON), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to create API key",
			},
		})
		return
	}

	// Return plaintext key (one-time only)
	resp := CreateAPIKeyResponse{
		ID:        keyID,
		TeamID:    req.TeamID,
		Key:       plaintextKey,
		KeyHash:   keyHash[:8],
		Scopes:    string(scopesJSON),
		Enabled:   true,
		CreatedAt: now,
	}

	c.JSON(http.StatusCreated, resp)
}

// HandleListAPIKeys lists API keys (without plaintext)
func (h *AdminHandler) HandleListAPIKeys(c *gin.Context) {
	teamID := c.Query("team_id")

	query := `
		SELECT id, team_id, key_hash, scopes, enabled, created_at
		FROM api_keys
		WHERE revoked_at IS NULL
	`
	args := []interface{}{}

	if teamID != "" {
		query += " AND team_id = ?"
		args = append(args, teamID)
	}

	query += " ORDER BY created_at DESC"

	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to list API keys",
			},
		})
		return
	}
	defer rows.Close()

	keys := []APIKey{}
	for rows.Next() {
		var key APIKey
		var keyHash string
		var enabled int
		err := rows.Scan(&key.ID, &key.TeamID, &keyHash, &key.Scopes, &enabled, &key.CreatedAt)
		if err != nil {
			continue
		}
		key.KeyHash = keyHash[:8] + "..." // Only show prefix
		key.Enabled = enabled == 1
		keys = append(keys, key)
	}

	c.JSON(http.StatusOK, gin.H{
		"api_keys": keys,
		"total":    len(keys),
	})
}
