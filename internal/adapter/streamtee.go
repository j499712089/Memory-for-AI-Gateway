package adapter

import (
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"time"

	"gateway/internal/hashutil"
	"gateway/internal/l0"
	"gateway/internal/turn"
)

// StreamTee intercepts SSE responses byte-by-byte
// Passes through to client while buffering for checkpoints
type StreamTee struct {
	upstream      io.ReadCloser
	downstream    io.Writer
	db            *sql.DB
	memoryRoot    string
	turnID        string
	requestID     string
	conversationID string
	sessionID     string
	teamID        string

	buffer        bytes.Buffer
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
					st.disconnected = true
					done <- fmt.Errorf("downstream write: %w", writeErr)
					return
				}

				// Buffer for checkpoint
				st.buffer.Write(buf[:n])
				st.totalBytes += int64(n)

				// Check if buffer exceeds 4KB
				if st.buffer.Len() >= 4096 {
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
			if st.buffer.Len() > 0 {
				st.writeCheckpoint()
			}

			if err != nil {
				if st.disconnected {
					return turn.StatusPartial, err
				}
				return turn.StatusError, err
			}
			return turn.StatusComplete, nil

		case <-ticker.C:
			// Time-based checkpoint (1 second)
			if st.buffer.Len() > 0 && time.Since(st.lastCheckpoint) >= 1*time.Second {
				if err := st.writeCheckpoint(); err != nil {
					fmt.Printf("checkpoint error: %v\n", err)
				}
			}
		}
	}
}

// writeCheckpoint writes buffered deltas to checkpoint file and database
func (st *StreamTee) writeCheckpoint() error {
	if st.buffer.Len() == 0 {
		return nil
	}

	// Write checkpoint file
	checkpointData := st.buffer.Bytes()
	_, err := l0.WriteCheckpointFile(st.memoryRoot, st.turnID, checkpointData)
	if err != nil {
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
		return fmt.Errorf("write checkpoint event: %w", err)
	}

	// Reset buffer and update state
	st.buffer.Reset()
	st.lastCheckpoint = time.Now()
	st.checkpointSeq++

	return nil
}

// GetTotalBytes returns total bytes streamed
func (st *StreamTee) GetTotalBytes() int64 {
	return st.totalBytes
}
