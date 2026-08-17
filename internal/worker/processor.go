package worker

import (
	"context"
	"fmt"
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
	for {
		if _, err := p.ProcessOnce(ctx, workerID); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
