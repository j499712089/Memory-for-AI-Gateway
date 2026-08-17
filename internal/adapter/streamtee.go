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
}

// NewStreamTee creates a new stream tee
func NewStreamTee(
	upstream io.ReadCloser,
	downstream io.Writer,
	db *sql.DB,
	memoryRoot, turnID, requestID, conversationID, sessionID, teamID string,
) *StreamTee {
	return &StreamTee{
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
			// Write final checkpoint if buffer has data
			if st.hasBufferedData() {
				st.writeCheckpoint()
			}

			if err != nil {
				// A client disconnect cancels the request context, which cancels
				// the upstream request; the upstream read then surfaces
				// context.Canceled / context.DeadlineExceeded. That is a stream
				// interruption, not an upstream protocol error (those are observed
				// before the stream is established), so record a partial terminal
				// instead of an error that would enqueue a spurious outbox retry.
				if st.isDisconnected() || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return turn.StatusPartial, err
				}
				return turn.StatusError, err
			}
			return turn.StatusComplete, nil

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

func (st *StreamTee) isDisconnected() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.disconnected
}
