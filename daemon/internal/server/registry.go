package server

import (
	"sort"
	"sync"
	"time"
)

type Registry struct {
	mu      sync.RWMutex
	nextID  uint64
	clients map[uint64]*trackedClient
}

type trackedClient struct {
	snapshot   ClientSnapshot
	registered bool
}

func NewRegistry() *Registry {
	return &Registry{clients: make(map[uint64]*trackedClient)}
}

func (r *Registry) AddClient(pid int) uint64 {
	if r == nil {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	id := r.nextID
	r.clients[id] = &trackedClient{
		snapshot: ClientSnapshot{
			PID:         pid,
			ConnectedAt: time.Now().UTC(),
		},
	}
	return id
}

func (r *Registry) UpdateClient(id uint64, state SessionState) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	client, ok := r.clients[id]
	if !ok {
		return
	}
	client.snapshot.Mode = state.Mode
	client.snapshot.WorkingDir = state.WorkingDir
	client.snapshot.PreparedTools = append([]string(nil), state.PreparedTools...)
	client.snapshot.LastSyncAt = time.Now().UTC()
	client.registered = true
}

func (r *Registry) RemoveClient(id uint64) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, id)
}

func (r *Registry) Clients() []ClientSnapshot {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]ClientSnapshot, 0, len(r.clients))
	for _, client := range r.clients {
		if !client.registered {
			continue
		}
		snapshot := client.snapshot
		snapshot.PreparedTools = append([]string(nil), snapshot.PreparedTools...)
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PID != out[j].PID {
			return out[i].PID < out[j].PID
		}
		if out[i].Mode != out[j].Mode {
			return out[i].Mode < out[j].Mode
		}
		return out[i].WorkingDir < out[j].WorkingDir
	})
	return out
}
