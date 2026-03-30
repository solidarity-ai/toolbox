package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileSink appends validated audit events to a JSONL file.
type FileSink struct {
	path string
	mu   sync.Mutex
}

// NewFileSink creates a file-backed audit sink that writes one JSON object per line.
func NewFileSink(path string) (*FileSink, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("audit file path is required")
	}
	return &FileSink{path: trimmed}, nil
}

func (s *FileSink) TryEmit(event Event) bool {
	if s == nil {
		return false
	}
	if err := event.Validate(); err != nil {
		return false
	}
	record := map[string]any{
		"name":    event.Name,
		"payload": event.Payload,
	}
	line, err := json.Marshal(record)
	if err != nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return false
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	defer file.Close()
	if _, err := file.Write(append(line, '\n')); err != nil {
		return false
	}
	return true
}
