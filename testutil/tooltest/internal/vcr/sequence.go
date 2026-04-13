package vcr

import "sync"

// sequenceTracker returns the Nth matching episode for the Nth identical call.
type sequenceTracker struct {
	mu     sync.Mutex
	counts map[string]int
}

func newSequenceTracker() *sequenceTracker {
	return &sequenceTracker{counts: make(map[string]int)}
}

// next returns and advances the index for key.
func (s *sequenceTracker) next(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.counts[key]
	s.counts[key] = n + 1
	return n
}

func (s *sequenceTracker) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts = make(map[string]int)
}
