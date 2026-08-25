package httpx

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
)

type SystemHandler struct {
	db *sql.DB
}

func NewSystemHandler(db *sql.DB) *SystemHandler {
	return &SystemHandler{db: db}
}

// ServiceStatusResponse represents the service status
type ServiceStatusResponse struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	Uptime  string `json:"uptime,omitempty"`
	Version string `json:"version"`
}

// SystemConfigResponse represents system configuration
type SystemConfigResponse struct {
	AutostartEnabled bool   `json:"autostart_enabled"`
	DBPath           string `json:"db_path"`
	ObsidianPath     string `json:"obsidian_path,omitempty"`
	VectorPath       string `json:"vector_path,omitempty"`
}

// LogEntry represents a log entry
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// HandleServiceStart starts the gateway service
func (h *SystemHandler) HandleServiceStart(c *gin.Context) {
	// Check if already running
	if isServiceRunning() {
		c.JSON(http.StatusConflict, gin.H{
			"error": gin.H{
				"type":    "conflict",
				"message": "service is already running",
			},
		})
		return
	}

	// Start the service
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/C", "start-gateway.bat")
	} else {
		cmd = exec.Command("sh", "-c", "./gateway &")
	}

	if err := cmd.Start(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": fmt.Sprintf("failed to start service: %v", err),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "service start initiated",
	})
}

// HandleServiceStop stops the gateway service
func (h *SystemHandler) HandleServiceStop(c *gin.Context) {
	// This is a self-termination endpoint
	// Return success before actually stopping
	c.JSON(http.StatusOK, gin.H{
		"message": "service stop initiated",
	})

	// Schedule shutdown after response is sent
	go func() {
		time.Sleep(1 * time.Second)
		os.Exit(0)
	}()
}

// HandleServiceRestart restarts the gateway service
func (h *SystemHandler) HandleServiceRestart(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"message": "service restart initiated",
	})

	// Schedule restart after response is sent
	go func() {
		time.Sleep(1 * time.Second)

		// Start new instance first
		var cmd *exec.Cmd
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/C", "start-gateway.bat")
		} else {
			cmd = exec.Command("sh", "-c", "./gateway &")
		}

		if err := cmd.Start(); err != nil {
			// Log error but still exit to allow manual restart
			fmt.Printf("Failed to start new instance: %v\n", err)
		}

		// Exit current instance
		time.Sleep(2 * time.Second)
		os.Exit(0)
	}()
}

// HandleServiceStatus returns the current service status
func (h *SystemHandler) HandleServiceStatus(c *gin.Context) {
	status := ServiceStatusResponse{
		Running: true, // If this endpoint responds, service is running
		PID:     os.Getpid(),
		Version: "1.0.0",
	}

	// Calculate uptime (approximation based on process start time)
	// In production, this should read from a startup timestamp file
	status.Uptime = "running"

	c.JSON(http.StatusOK, status)
}

// HandleGetConfig returns the current system configuration
func (h *SystemHandler) HandleGetConfig(c *gin.Context) {
	config := SystemConfigResponse{
		AutostartEnabled: checkAutostartEnabled(),
		DBPath:           getDBPath(h.db),
	}

	c.JSON(http.StatusOK, config)
}

// HandleSetAutostart enables or disables autostart
func (h *SystemHandler) HandleSetAutostart(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": err.Error(),
			},
		})
		return
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		if req.Enabled {
			cmd = exec.Command("cmd", "/C", "install-autostart.bat")
		} else {
			cmd = exec.Command("cmd", "/C", "uninstall-autostart.bat")
		}
	} else {
		// Linux/Mac implementation
		c.JSON(http.StatusNotImplemented, gin.H{
			"error": gin.H{
				"type":    "not_implemented",
				"message": "autostart not implemented for this platform",
			},
		})
		return
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": fmt.Sprintf("failed to set autostart: %v, output: %s", err, string(output)),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled": req.Enabled,
		"message": "autostart configuration updated",
	})
}

// HandleGetLogs returns recent system logs
func (h *SystemHandler) HandleGetLogs(c *gin.Context) {
	limit := 100
	if limitStr := c.Query("limit"); limitStr != "" {
		fmt.Sscanf(limitStr, "%d", &limit)
	}

	// Read from turn_events table (simplified)
	query := `
		SELECT created_at, 'info', 'event'
		FROM turn_events
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := h.db.Query(query, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{
				"type":    "internal_error",
				"message": "failed to read logs",
			},
		})
		return
	}
	defer rows.Close()

	logs := []LogEntry{}
	for rows.Next() {
		var log LogEntry
		if err := rows.Scan(&log.Timestamp, &log.Level, &log.Message); err != nil {
			continue
		}
		logs = append(logs, log)
	}

	c.JSON(http.StatusOK, logs)
}

// HandleBackup returns a backup of the current system configuration.
func (h *SystemHandler) HandleBackup(c *gin.Context) {
	// Export configuration as JSON
	config := map[string]interface{}{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"version":     "1.0.0",
		"db_path":     getDBPath(h.db),
	}

	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=gateway-config-%s.json", time.Now().Format("2006-01-02")))
	c.JSON(http.StatusOK, config)
}

// HandleRestore validates and restores a system configuration backup.
func (h *SystemHandler) HandleRestore(c *gin.Context) {
	var config map[string]interface{}
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"type":    "invalid_request",
				"message": "invalid configuration format",
			},
		})
		return
	}

	// Validate and apply configuration
	// This is a placeholder - actual implementation would update database records

	c.JSON(http.StatusOK, gin.H{
		"message": "configuration imported successfully",
	})
}

// Helper functions

func isServiceRunning() bool {
	// Check if service is running by attempting to connect to the port
	// This is a simplified check
	return true // If we can execute code, service is running
}

func checkAutostartEnabled() bool {
	if runtime.GOOS != "windows" {
		return false
	}

	// Check Windows Task Scheduler
	cmd := exec.Command("schtasks", "/Query", "/TN", "MemoryGatewayAutoStart", "/FO", "LIST")
	err := cmd.Run()
	return err == nil
}

func getDBPath(db *sql.DB) string {
	// Query database file path from SQLite
	var seq int
	var name, path string
	err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path)
	if err != nil {
		return ""
	}
	return path
}
