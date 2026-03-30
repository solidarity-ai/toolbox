package audit

import "sync"

// Sink receives best-effort secret-safe audit events.
//
// TryEmit must never block request execution. Callers treat false as a dropped
// event and continue transport processing.
type Sink interface {
	TryEmit(Event) bool
}

// NoopSink drops all audit events.
type NoopSink struct{}

func (NoopSink) TryEmit(Event) bool { return false }

// SinkOrNoop returns sink when non-nil, otherwise a noop sink.
func SinkOrNoop(sink Sink) Sink {
	if sink == nil {
		return NoopSink{}
	}
	return sink
}

// Emit validates and best-effort emits one event.
func Emit(sink Sink, event Event) bool {
	if err := event.Validate(); err != nil {
		return false
	}
	return SinkOrNoop(sink).TryEmit(event)
}

// Collector stores audit events in memory for deterministic inspection in tests.
type Collector struct {
	mu     sync.Mutex
	events []Event
}

func NewCollector() *Collector {
	return &Collector{}
}

func (c *Collector) TryEmit(event Event) bool {
	if c == nil {
		return false
	}
	if err := event.Validate(); err != nil {
		return false
	}
	c.mu.Lock()
	c.events = append(c.events, event)
	c.mu.Unlock()
	return true
}

func (c *Collector) Events() []Event {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}
