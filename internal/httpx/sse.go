package httpx

import (
	"context"

	"github.com/gin-gonic/gin"
)

// SetSSEHeaders sets the headers for Server-Sent Events
func SetSSEHeaders(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // Disable nginx buffering
}

// SSEWriter wraps gin context for SSE writing
type SSEWriter struct {
	ctx *gin.Context
}

// NewSSEWriter creates a new SSE writer
func NewSSEWriter(c *gin.Context) *SSEWriter {
	SetSSEHeaders(c)
	return &SSEWriter{ctx: c}
}

// Write writes raw bytes to the SSE stream
// Used for byte-level passthrough from upstream
func (w *SSEWriter) Write(data []byte) (int, error) {
	n, err := w.ctx.Writer.Write(data)
	if err != nil {
		return n, err
	}

	// Flush to ensure immediate delivery
	w.ctx.Writer.Flush()
	return n, nil
}

// Flush flushes the SSE stream
func (w *SSEWriter) Flush() {
	w.ctx.Writer.Flush()
}

// streamTerminalResult applies the same dual-write degradation gate
// (Constitution §1.5) to a streaming terminal event: SQLite first, then the
// local durable buffer. The SSE stream is already established by the time this
// runs, so a recording failure must never interrupt the client stream — the
// event is buffered when SQLite is unwritable, and only errRecordingUnavailable
// (both sinks down) is surfaced here for the caller to log.
func (h *GatewayHandler) streamTerminalResult(ctx context.Context, tr terminalRecord) (string, error) {
	return h.recordTerminalResult(ctx, tr)
}
