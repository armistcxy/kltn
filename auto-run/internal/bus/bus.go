// Package bus provides a simple pub/sub event bus for streaming benchmark
// logs and status changes to SSE clients.
package bus

import (
	"sync"
)

// Event represents one log line or status change from an orchestrator step.
type Event struct {
	RunID   string `json:"run_id"`
	Type    string `json:"type"`    // "log" | "status" | "step"
	Payload string `json:"payload"` // log line or new status value
}

// Bus is a thread-safe broadcast channel. Any number of subscribers can
// receive events; slow subscribers are dropped (non-blocking send).
type Bus struct {
	mu   sync.RWMutex
	subs map[string][]chan Event // key: run ID ("" = global)
}

// New creates an initialised Bus.
func New() *Bus {
	return &Bus{subs: make(map[string][]chan Event)}
}

// Subscribe returns a channel that receives events for the given run ID.
// Pass "" to receive all events. The caller must call Unsubscribe when done.
func (b *Bus) Subscribe(runID string) chan Event {
	ch := make(chan Event, 256)
	b.mu.Lock()
	b.subs[runID] = append(b.subs[runID], ch)
	b.mu.Unlock()
	return ch
}

// Unsubscribe removes a channel previously returned by Subscribe.
func (b *Bus) Unsubscribe(runID string, ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	subs := b.subs[runID]
	out := subs[:0]
	for _, s := range subs {
		if s != ch {
			out = append(out, s)
		}
	}
	b.subs[runID] = out
	close(ch)
}

// Publish sends an event to all subscribers of the run AND global ("")
// subscribers. Non-blocking: full subscriber channels are skipped.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs[e.RunID] {
		select {
		case ch <- e:
		default:
		}
	}
	if e.RunID != "" {
		for _, ch := range b.subs[""] {
			select {
			case ch <- e:
			default:
			}
		}
	}
}

// PublishLog is a convenience wrapper for log-line events.
func (b *Bus) PublishLog(runID, line string) {
	b.Publish(Event{RunID: runID, Type: "log", Payload: line})
}

// PublishStatus is a convenience wrapper for status-change events.
func (b *Bus) PublishStatus(runID, status string) {
	b.Publish(Event{RunID: runID, Type: "status", Payload: status})
}

// PublishStep is a convenience wrapper for step-status events.
// payload format: "<step-name>:<step-status>"
func (b *Bus) PublishStep(runID, stepName, stepStatus string) {
	b.Publish(Event{RunID: runID, Type: "step", Payload: stepName + ":" + stepStatus})
}
