package httpx

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

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
	if h == nil || h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unhealthy", "error": "database connection failed"})
		return
	}
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

type RecordingHealthEntry struct {
	RequestID          string `json:"request_id"`
	ConversationID     string `json:"conversation_id"`
	SessionID          string `json:"session_id,omitempty"`
	InboundAt          string `json:"inbound_at,omitempty"`
	TerminalAt         string `json:"terminal_at,omitempty"`
	TerminalStatus     string `json:"terminal_status,omitempty"`
	ContentHash        string `json:"content_hash,omitempty"`
	L0Path             string `json:"l0_path,omitempty"`
	BindingVersion     int    `json:"binding_version,omitempty"`
	CompensationStatus string `json:"compensation_status"`
	LastSuccessAt      string `json:"last_success_at,omitempty"`
}

// HandleRecordingHealth serves the durable recording projection used by the
// panel. Rows are scoped to the authenticated team through their session.
func (h *HealthHandler) HandleRecordingHealth(c *gin.Context) {
	if err := h.projectLedgerHealth(c); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "internal_error", "message": "failed to project recording health"}})
		return
	}
	limit := parsePositiveInt(c.Query("limit"), 50)
	if limit > 100 {
		limit = 100
	}
	offset := parseNonNegativeInt(c.Query("offset"), 0)
	status := strings.TrimSpace(c.Query("terminal_status"))
	if status == "" {
		status = strings.TrimSpace(c.Query("status"))
	}
	teamID, scoped := GetTeamID(c)

	where := []string{"1=1"}
	args := make([]any, 0, 4)
	if scoped {
		where = append(where, "COALESCE((SELECT tl.team_id FROM turn_ledger tl WHERE tl.request_id = rh.request_id ORDER BY tl.created_at DESC LIMIT 1), s.team_id) = ?")
		args = append(args, teamID)
	}
	if status != "" {
		where = append(where, "rh.terminal_status = ?")
		args = append(args, status)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM recording_health rh LEFT JOIN sessions s ON s.id = rh.session_id WHERE `+whereSQL, args...).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "internal_error", "message": "failed to count recording health"}})
		return
	}
	query := `SELECT rh.request_id, rh.conversation_id, COALESCE(rh.session_id,''), COALESCE(rh.inbound_at,''), COALESCE(rh.terminal_at,''), COALESCE(rh.terminal_status,''), COALESCE(rh.content_hash,''), COALESCE(rh.l0_path,''), COALESCE(rh.binding_version,0), COALESCE(rh.compensation_status,'ok'), COALESCE(rh.last_success_at,'') FROM recording_health rh LEFT JOIN sessions s ON s.id = rh.session_id WHERE ` + whereSQL + ` ORDER BY COALESCE(rh.inbound_at, rh.created_at) DESC, rh.request_id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := h.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "internal_error", "message": "failed to query recording health"}})
		return
	}
	defer rows.Close()
	items := make([]RecordingHealthEntry, 0)
	for rows.Next() {
		var item RecordingHealthEntry
		if err := rows.Scan(&item.RequestID, &item.ConversationID, &item.SessionID, &item.InboundAt, &item.TerminalAt, &item.TerminalStatus, &item.ContentHash, &item.L0Path, &item.BindingVersion, &item.CompensationStatus, &item.LastSuccessAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "internal_error", "message": "failed to decode recording health"}})
			return
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": gin.H{"type": "internal_error", "message": fmt.Sprintf("recording health rows: %v", err)}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// projectLedgerHealth keeps the dashboard table useful even when a turn was
// written by a lower-level ledger path that did not explicitly update the
// projection. The request_id uniqueness constraint makes this idempotent and
// the upsert refreshes rows that were projected before a terminal event.
func (h *HealthHandler) projectLedgerHealth(c *gin.Context) error {
	if h == nil || h.db == nil {
		return fmt.Errorf("health database is nil")
	}
	_, err := h.db.ExecContext(c.Request.Context(), `
		INSERT INTO recording_health (
			id, request_id, conversation_id, session_id, inbound_at, terminal_at,
			terminal_status, content_hash, l0_path, binding_version,
			compensation_status, last_success_at
		)
		SELECT
			'ledger:' || tl.turn_id,
			tl.request_id,
			tl.conversation_id,
			tl.session_id,
			COALESCE((SELECT MIN(te.created_at) FROM turn_events te WHERE te.id = tl.inbound_event_id), tl.created_at),
			tl.completed_at,
			tl.final_status,
			tl.content_hash,
			tl.l0_request_path,
			tl.binding_version,
			CASE WHEN tl.recording_state = 'compensating' THEN 'pending' ELSE 'ok' END,
			CASE WHEN tl.final_status = 'complete' THEN tl.completed_at ELSE NULL END
		FROM turn_ledger tl
		WHERE 1=1
		ON CONFLICT(request_id) DO UPDATE SET
			conversation_id=excluded.conversation_id,
			session_id=excluded.session_id,
			inbound_at=COALESCE(excluded.inbound_at, recording_health.inbound_at),
			terminal_at=COALESCE(excluded.terminal_at, recording_health.terminal_at),
			terminal_status=COALESCE(excluded.terminal_status, recording_health.terminal_status),
			content_hash=COALESCE(excluded.content_hash, recording_health.content_hash),
			l0_path=COALESCE(excluded.l0_path, recording_health.l0_path),
			binding_version=COALESCE(excluded.binding_version, recording_health.binding_version),
			compensation_status=CASE WHEN recording_health.compensation_status='pending' AND excluded.compensation_status='ok' THEN recording_health.compensation_status ELSE excluded.compensation_status END,
			last_success_at=COALESCE(excluded.last_success_at, recording_health.last_success_at)`)
	return err
}

func parsePositiveInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseNonNegativeInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}
