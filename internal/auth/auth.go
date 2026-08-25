package auth

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"gateway/internal/hashutil"
)

// Middleware provides authentication for API requests
type Middleware struct {
	db *sql.DB
}

// NewMiddleware creates a new auth middleware
func NewMiddleware(db *sql.DB) *Middleware {
	return &Middleware{db: db}
}

// ValidateAPIKey checks if the provided API key is valid
func (m *Middleware) ValidateAPIKey(bearerToken string) (teamID string, apiKeyID string, err error) {
	// Extract token from Bearer prefix if present
	token := strings.TrimPrefix(bearerToken, "Bearer ")
	token = strings.TrimSpace(token)

	if token == "" {
		return "", "", fmt.Errorf("empty API key")
	}

	// Hash the token to compare with stored key_hash
	hash := hashutil.SHA256(token)

	// Query database for matching API key
	query := `
		SELECT id, team_id, enabled, expires_at, revoked_at
		FROM api_keys
		WHERE key_hash = ?
	`

	var id, team string
	var enabled int
	var expiresAt, revokedAt sql.NullString

	err = m.db.QueryRow(query, hash).Scan(&id, &team, &enabled, &expiresAt, &revokedAt)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("invalid API key")
	}
	if err != nil {
		return "", "", fmt.Errorf("database error: %w", err)
	}

	// Check if key is enabled
	if enabled != 1 {
		return "", "", fmt.Errorf("API key is disabled")
	}

	// Check if key is revoked
	if revokedAt.Valid {
		return "", "", fmt.Errorf("API key has been revoked")
	}

	// Check if key is expired
	if expiresAt.Valid {
		expiry, err := time.Parse(time.RFC3339, expiresAt.String)
		if err == nil && time.Now().After(expiry) {
			return "", "", fmt.Errorf("API key has expired")
		}
	}

	// Update last_used_at
	_, _ = m.db.Exec("UPDATE api_keys SET last_used_at = ? WHERE id = ?",
		time.Now().UTC().Format(time.RFC3339), id)

	return team, id, nil
}
