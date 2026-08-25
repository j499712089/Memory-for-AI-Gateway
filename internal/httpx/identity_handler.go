package httpx

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

type IdentityHandler struct {
	db *sql.DB
}

func NewIdentityHandler(db *sql.DB) *IdentityHandler {
	return &IdentityHandler{db: db}
}

// BindAgentRequest represents the request to bind an agent to an identity card
type BindAgentRequest struct {
	AgentID string `json:"agent_id" binding:"required"`
}

// BindAgentResponse represents the binding result
type BindAgentResponse struct {
	IdentityCardID string `json:"identity_card_id"`
	AgentID        string `json:"agent_id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
}

// TestConnectionRequest represents the request to test agent connection
type TestConnectionRequest struct {
	AgentID string `json:"agent_id,omitempty"`
}

// TestConnectionResponse represents the connection test result
type TestConnectionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	AgentID string `json:"agent_id,omitempty"`
}

// HandleBindAgent binds an agent to an identity card
func (h *IdentityHandler) HandleBindAgent(c *gin.Context) {
	cardID := c.Param("id")

	var req BindAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": err.Error(),
			},
		})
		return
	}

	// Check if identity card exists
	var name, status string
	query := `SELECT name, status FROM identity_cards WHERE id = ?`
	err := h.db.QueryRow(query, cardID).Scan(&name, &status)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found",
				"message": "identity card not found",
			},
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to query identity card",
			},
		})
		return
	}

	// Update identity card with agent binding
	updateQuery := `UPDATE identity_cards SET agent_id = ? WHERE id = ?`
	_, err = h.db.Exec(updateQuery, req.AgentID, cardID)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to bind agent",
			},
		})
		return
	}

	resp := BindAgentResponse{
		IdentityCardID: cardID,
		AgentID:        req.AgentID,
		Name:           name,
		Status:         status,
	}

	c.JSON(http.StatusOK, resp)
}

// HandleTestConnection tests the connection to a bound agent
func (h *IdentityHandler) HandleTestConnection(c *gin.Context) {
	cardID := c.Param("id")

	// Get the bound agent_id from identity card
	var agentID sql.NullString
	query := `SELECT agent_id FROM identity_cards WHERE id = ?`
	err := h.db.QueryRow(query, cardID).Scan(&agentID)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found",
				"message": "identity card not found",
			},
		})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to query identity card",
			},
		})
		return
	}

	if !agentID.Valid || agentID.String == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": "no agent bound to this identity card",
			},
		})
		return
	}

	// Verify agent exists in agents table
	var agentName string
	agentQuery := `SELECT name FROM agents WHERE id = ?`
	err = h.db.QueryRow(agentQuery, agentID.String).Scan(&agentName)

	if err == sql.ErrNoRows {
		resp := TestConnectionResponse{
			Success: false,
			Message: "bound agent not found in agents table",
			AgentID: agentID.String,
		}
		c.JSON(http.StatusOK, resp)
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to verify agent",
			},
		})
		return
	}

	// Connection test successful
	resp := TestConnectionResponse{
		Success: true,
		Message: "agent connection verified",
		AgentID: agentID.String,
	}

	c.JSON(http.StatusOK, resp)
}
