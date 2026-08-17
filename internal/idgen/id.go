package idgen

import (
	"github.com/google/uuid"
)

// NewID generates a new UUID v4 string
func NewID() string {
	return uuid.New().String()
}

// NewRequestID generates a new request ID
func NewRequestID() string {
	return "req_" + uuid.New().String()
}

// NewTurnID generates a new turn ID
func NewTurnID() string {
	return "turn_" + uuid.New().String()
}

// NewEventID generates a new event ID
func NewEventID() string {
	return "evt_" + uuid.New().String()
}
