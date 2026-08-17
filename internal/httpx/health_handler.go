package httpx

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
)

type HealthHandler struct {
	db *sql.DB
}

func NewHealthHandler(db *sql.DB) *HealthHandler {
	return &HealthHandler{db: db}
}

// HandleHealth returns the health status of the gateway
func (h *HealthHandler) HandleHealth(c *gin.Context) {
	// Check database connectivity
	if err := h.db.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":  "unhealthy",
			"error":   "database connection failed",
			"message": err.Error(),
		})
		return
	}

	// Get journal mode
	var journalMode string
	err := h.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"status": "unhealthy",
			"error":  "failed to check journal mode",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":       "healthy",
		"journal_mode": journalMode,
		"version":      "1.0.0",
	})
}
