package worker

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Handler func(context.Context, *Claim) error

type Processor struct {
	Queue    *Queue
	Handlers map[string]Handler
}

func NewProcessor(queue *Queue) *Processor {
	return &Processor{Queue: queue, Handlers: make(map[string]Handler)}
}

func (p *Processor) Register(queue string, handler Handler) {
	if p.Handlers == nil {
		p.Handlers = make(map[string]Handler)
	}
	p.Handlers[queue] = handler
}

// RegisteredQueues returns the names of the queues this processor has a
// handler for. ProcessOnce only claims jobs in these queues.
func (p *Processor) RegisteredQueues() []string {
	if p == nil || p.Handlers == nil {
		return nil
	}
	queues := make([]string, 0, len(p.Handlers))
	for name := range p.Handlers {
		queues = append(queues, name)
	}
	return queues
}

func (p *Processor) ProcessOnce(ctx context.Context, workerID string) (bool, error) {
	if p == nil || p.Queue == nil {
		return false, fmt.Errorf("worker processor queue is nil")
	}
	queues := p.RegisteredQueues()
	if len(queues) == 0 {
		// Nothing this worker can process; leave foreign queues untouched.
		return false, nil
	}
	claim, err := p.Queue.Claim(ctx, workerID, queues...)
	if err != nil || claim == nil {
		return claim != nil, err
	}
	handler := p.Handlers[claim.Queue]
	if handler == nil {
		// Unreachable: the claim filter only selects registered queues.
		return true, p.Queue.Fail(ctx, claim, "no handler registered for queue "+claim.Queue)
	}
	if err := handler(ctx, claim); err != nil {
		return true, p.Queue.Fail(ctx, claim, err.Error())
	}
	return true, p.Queue.Complete(ctx, claim)
}

func (p *Processor) Run(ctx context.Context, workerID string, interval time.Duration) error {
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	busyBackoff := 100 * time.Millisecond
	for {
		if _, err := p.ProcessOnce(ctx, workerID); err != nil {
			// SQLite can briefly reject BEGIN IMMEDIATE/UPDATE while the gateway,
			// watchdog or another worker is committing. A transient lock must not
			// kill the long-lived worker; retry the poll and let the queue lease
			// recovery handle any claim whose failure update was also blocked.
			if !isTransientSQLiteBusy(err) {
				return err
			}
			timer := time.NewTimer(busyBackoff)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return ctx.Err()
			case <-timer.C:
			}
			if busyBackoff < time.Second {
				busyBackoff *= 2
				if busyBackoff > time.Second {
					busyBackoff = time.Second
				}
			}
			continue
		}
		busyBackoff = 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// isTransientSQLiteBusy recognizes the modernc SQLite errors returned when a
// concurrent writer briefly owns the database lock. Keep this deliberately
// narrow so permanent handler failures still stop a misconfigured worker.
func isTransientSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") ||
		strings.Contains(message, "sqlite_locked") ||
		strings.Contains(message, "database is locked") ||
		strings.Contains(message, "database table is locked") ||
		strings.Contains(message, "database is busy")
}
