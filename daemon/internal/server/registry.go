package server

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type Registry struct {
	mu       sync.RWMutex
	nextID   uint64
	revision uint64
	clients  map[uint64]*trackedClient
}

type trackedClient struct {
	snapshot   ClientSnapshot
	decisions  []ApprovalDecision
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
	client.snapshot.Locked = state.Locked
	client.snapshot.BoundTBSession = state.BoundTBSession
	client.snapshot.IntentText = state.IntentText
	client.snapshot.IntentSource = state.IntentSource
	client.snapshot.IntentUpdatedAt = state.IntentUpdatedAt
	client.snapshot.WorkingDir = state.WorkingDir
	client.snapshot.PreparedTools = append([]string(nil), state.PreparedTools...)
	client.snapshot.PendingApprovals = clonePendingApprovals(state.PendingApprovals)
	client.snapshot.LastSyncAt = time.Now().UTC()
	client.decisions = filterApprovalDecisions(client.decisions, client.snapshot.PendingApprovals)
	client.registered = true
	r.revision++
}

func (r *Registry) RemoveClient(id uint64) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, id)
	r.revision++
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
		snapshot.PendingApprovals = clonePendingApprovals(snapshot.PendingApprovals)
		attachQueuedDecisions(snapshot.PendingApprovals, client.decisions)
		out = append(out, snapshot)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PID != out[j].PID {
			return out[i].PID < out[j].PID
		}
		if out[i].BoundTBSession != out[j].BoundTBSession {
			return out[i].BoundTBSession < out[j].BoundTBSession
		}
		if out[i].Mode != out[j].Mode {
			return out[i].Mode < out[j].Mode
		}
		return out[i].WorkingDir < out[j].WorkingDir
	})
	return out
}

func (r *Registry) PendingApprovals() []PendingApprovalSnapshot {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []PendingApprovalSnapshot
	for _, client := range r.clients {
		if !client.registered {
			continue
		}
		approvals := clonePendingApprovals(client.snapshot.PendingApprovals)
		attachQueuedDecisions(approvals, client.decisions)
		out = append(out, approvals...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ToolCallID < out[j].ToolCallID
	})
	return out
}

func (r *Registry) QueueApprovalDecision(decision ApprovalDecision) error {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	decision = normalizeApprovalDecision(decision)
	if decision.Action == "" || decision.ToolCallID == "" {
		return nil
	}
	for _, client := range r.clients {
		if !client.registered || !hasApprovalDecisionTarget(client.snapshot.PendingApprovals, decision) {
			continue
		}
		for i, queued := range client.decisions {
			if sameApprovalDecision(queued, decision) {
				return nil
			}
			if queued.ToolCallID == decision.ToolCallID {
				client.decisions[i] = decision
				r.revision++
				return nil
			}
		}
		client.decisions = append(client.decisions, decision)
		r.revision++
		return nil
	}
	return nil
}

func (r *Registry) Revision() uint64 {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.revision
}

func (r *Registry) ApprovalDecisions(id uint64) []ApprovalDecision {
	if r == nil || id == 0 {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[id]
	if !ok {
		return nil
	}
	return append([]ApprovalDecision(nil), client.decisions...)
}

func clonePendingApprovals(approvals []PendingApprovalSnapshot) []PendingApprovalSnapshot {
	if len(approvals) == 0 {
		return nil
	}
	out := make([]PendingApprovalSnapshot, len(approvals))
	copy(out, approvals)
	return out
}

func attachQueuedDecisions(approvals []PendingApprovalSnapshot, decisions []ApprovalDecision) {
	if len(approvals) == 0 || len(decisions) == 0 {
		return
	}
	byID := make(map[string]ApprovalDecision, len(decisions))
	for _, decision := range decisions {
		byID[decision.ToolCallID] = decision
	}
	for i := range approvals {
		decision, ok := byID[approvals[i].ToolCallID]
		if !ok {
			continue
		}
		approvals[i].QueuedDecision = &QueuedApprovalDecision{
			Action:           decision.Action,
			Message:          decision.Message,
			ClientDecisionID: decision.ClientDecisionID,
			QueuedAt:         decision.QueuedAt,
		}
	}
}

func filterApprovalDecisions(decisions []ApprovalDecision, approvals []PendingApprovalSnapshot) []ApprovalDecision {
	if len(decisions) == 0 {
		return nil
	}
	var out []ApprovalDecision
	for _, decision := range decisions {
		if hasApprovalDecisionTarget(approvals, decision) {
			out = append(out, decision)
		}
	}
	return out
}

func hasApprovalToolCall(approvals []PendingApprovalSnapshot, toolCallID string) bool {
	for _, approval := range approvals {
		if approval.ToolCallID == toolCallID {
			return true
		}
	}
	return false
}

func hasApprovalDecisionTarget(approvals []PendingApprovalSnapshot, decision ApprovalDecision) bool {
	return hasApprovalToolCall(approvals, decision.ToolCallID)
}

func normalizeApprovalDecision(decision ApprovalDecision) ApprovalDecision {
	decision.Action = strings.TrimSpace(decision.Action)
	decision.ToolCallID = strings.TrimSpace(decision.ToolCallID)
	decision.Message = strings.TrimSpace(decision.Message)
	decision.ClientDecisionID = strings.TrimSpace(decision.ClientDecisionID)
	if decision.QueuedAt.IsZero() {
		decision.QueuedAt = time.Now().UTC()
	}
	return decision
}

func sameApprovalDecision(a, b ApprovalDecision) bool {
	return a.Action == b.Action &&
		a.ToolCallID == b.ToolCallID &&
		a.Message == b.Message
}
