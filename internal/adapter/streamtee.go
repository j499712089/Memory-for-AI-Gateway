package adapter

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"gateway/internal/hashutil"
	"gateway/internal/l0"
	"gateway/internal/turn"
)

// StreamTee intercepts SSE responses byte-by-byte
// Passes through to client while buffering for checkpoints
type StreamTee struct {
	ctx            context.Context
	upstream       io.ReadCloser
	downstream     io.Writer
	db             *sql.DB
	memoryRoot     string
	turnID         string
	requestID      string
	conversationID string
	sessionID      string
	teamID         string

	mu             sync.Mutex
	checkpointMu   sync.Mutex
	buffer         bytes.Buffer
	lastCheckpoint time.Time
	checkpointSeq  int
	totalBytes     int64
	disconnected   bool
	complete       bool
}

// disconnectGracePeriod is how long Stream() waits for the upstream read loop
// to drain remaining bytes and reach EOF after the client cancels the request
// context. Clients routinely close the connection right after the protocol
// terminal marker; without the grace window a fully delivered stream would be
// misclassified as partial because context cancellation surfaces before EOF.
const disconnectGracePeriod = 500 * time.Millisecond

// NewStreamTee creates a new stream tee. ctx is the client request context:
// its cancellation marks a client disconnect/abort and drives the partial /
// cancelled terminal classification.
func NewStreamTee(
	ctx context.Context,
	upstream io.ReadCloser,
	downstream io.Writer,
	db *sql.DB,
	memoryRoot, turnID, requestID, conversationID, sessionID, teamID string,
) *StreamTee {
	if ctx == nil {
		ctx = context.Background()
	}
	return &StreamTee{
		ctx:            ctx,
		upstream:       upstream,
		downstream:     downstream,
		db:             db,
		memoryRoot:     memoryRoot,
		turnID:         turnID,
		requestID:      requestID,
		conversationID: conversationID,
		sessionID:      sessionID,
		teamID:         teamID,
		lastCheckpoint: time.Now(),
		checkpointSeq:  2, // Sequence 1 is response_started
	}
}

// Stream performs the streaming with checkpointing
// Returns terminal status and any error
func (st *StreamTee) Stream() (turn.TerminalStatus, error) {
	defer st.upstream.Close()

	// Start checkpoint ticker (1 second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	reader := bufio.NewReader(st.upstream)
	done := make(chan error, 1)

	// Read loop
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				// Write to downstream (client)
				if _, writeErr := st.downstream.Write(buf[:n]); writeErr != nil {
					st.mu.Lock()
					st.disconnected = true
					st.mu.Unlock()
					done <- fmt.Errorf("downstream write: %w", writeErr)
					return
				}

				// Buffer for checkpoint
				st.mu.Lock()
				st.buffer.Write(buf[:n])
				st.totalBytes += int64(n)
				shouldCheckpoint := st.buffer.Len() >= 4096
				st.mu.Unlock()

				// Check if buffer exceeds 4KB
				if shouldCheckpoint {
					if cpErr := st.writeCheckpoint(); cpErr != nil {
						// Log but don't fail stream
						fmt.Printf("checkpoint error: %v\n", cpErr)
					}
				}
			}

			if err != nil {
				if err == io.EOF {
					st.mu.Lock()
					st.complete = true
					st.mu.Unlock()
					done <- nil
					return
				}
				done <- err
				return
			}
		}
	}()

	// Checkpoint ticker loop
	for {
		select {
		case err := <-done:
			// Stream finished
			return st.finalize(err)

		case <-st.ctx.Done():
			// Client cancelled or disconnected. The upstream may have already
			// delivered the full stream (clients often close the connection
			// right after the protocol terminal marker), so give the read loop
			// a short grace period to drain the remaining bytes and reach EOF.
			// A fully delivered stream then records 'complete' instead of a
			// spurious 'partial' — the terminal classification must depend on
			// whether the stream actually finished, not on which of
			// context-cancellation / downstream-write-failure surfaces first.
			select {
			case err := <-done:
				return st.finalize(err)
			case <-time.After(disconnectGracePeriod):
				// The read loop is still blocked on the upstream: record the
				// interruption now. The deferred upstream.Close() unblocks the
				// reader; its error is discarded (buffered channel).
				return st.finalize(st.ctx.Err())
			}

		case <-ticker.C:
			// Time-based checkpoint (1 second)
			if st.checkpointDue() {
				if err := st.writeCheckpoint(); err != nil {
					fmt.Printf("checkpoint error: %v\n", err)
				}
			}
		}
	}
}

// finalize flushes any buffered data into a checkpoint and decides the stable
// terminal status. The decision is deterministic:
//   - the stream reached EOF (fully delivered)          -> complete
//   - the client is gone (context cancelled / write fail) and the stream did
//     not finish; some bytes were delivered              -> partial
//   - the client is gone and nothing was delivered       -> cancelled
//   - any other upstream error                           -> error
func (st *StreamTee) finalize(err error) (turn.TerminalStatus, error) {
	if st.hasBufferedData() {
		if cpErr := st.writeCheckpoint(); cpErr != nil {
			fmt.Printf("checkpoint error: %v\n", cpErr)
		}
	}

	st.mu.Lock()
	complete := st.complete
	total := st.totalBytes
	disconnected := st.disconnected
	st.mu.Unlock()

	if err == nil || complete {
		return turn.StatusComplete, nil
	}

	// The client is gone when the request context is cancelled, a downstream
	// write failed, or the upstream read surfaced a cancellation error (the
	// last one keeps callers that pass a non-cancelled context working).
	clientGone := disconnected ||
		(st.ctx != nil && st.ctx.Err() != nil) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
	if clientGone {
		if total == 0 {
			return turn.StatusCancelled, err
		}
		return turn.StatusPartial, err
	}

	return turn.StatusError, err
}

// writeCheckpoint writes buffered deltas to checkpoint file and database
func (st *StreamTee) writeCheckpoint() error {
	st.checkpointMu.Lock()
	defer st.checkpointMu.Unlock()

	st.mu.Lock()
	if st.buffer.Len() == 0 {
		st.mu.Unlock()
		return nil
	}
	checkpointData := append([]byte(nil), st.buffer.Bytes()...)
	st.buffer.Reset()
	st.mu.Unlock()

	// Write checkpoint file
	_, err := l0.WriteCheckpointFile(st.memoryRoot, st.turnID, checkpointData)
	if err != nil {
		st.restoreBufferedData(checkpointData)
		return fmt.Errorf("write checkpoint file: %w", err)
	}

	// Calculate content hash
	contentHash := "sha256:" + hashutil.SHA256Bytes(checkpointData)

	// Write delta_checkpoint event
	err = turn.WriteDeltaCheckpoint(
		st.db,
		st.turnID,
		st.requestID,
		st.conversationID,
		st.sessionID,
		st.teamID,
		contentHash,
		st.checkpointSeq,
	)
	if err != nil {
		st.restoreBufferedData(checkpointData)
		return fmt.Errorf("write checkpoint event: %w", err)
	}

	st.mu.Lock()
	st.lastCheckpoint = time.Now()
	st.checkpointSeq++
	st.mu.Unlock()

	return nil
}

// GetTotalBytes returns total bytes streamed
func (st *StreamTee) GetTotalBytes() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.totalBytes
}

func (st *StreamTee) hasBufferedData() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.buffer.Len() > 0
}

func (st *StreamTee) checkpointDue() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.buffer.Len() > 0 && time.Since(st.lastCheckpoint) >= time.Second
}

func (st *StreamTee) restoreBufferedData(data []byte) {
	st.mu.Lock()
	defer st.mu.Unlock()
	current := append([]byte(nil), st.buffer.Bytes()...)
	st.buffer.Reset()
	st.buffer.Write(data)
	st.buffer.Write(current)
}
