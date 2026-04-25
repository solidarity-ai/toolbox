package codemodesession

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type ApprovalAwaitStatus string

const (
	ApprovalAwaitStatusNoOutstanding ApprovalAwaitStatus = "no_outstanding"
	ApprovalAwaitStatusApproved      ApprovalAwaitStatus = "approved"
	ApprovalAwaitStatusRejected      ApprovalAwaitStatus = "rejected"
	ApprovalAwaitStatusCancelled     ApprovalAwaitStatus = "cancelled"
)

type ApprovalAwaitResult struct {
	Status    ApprovalAwaitStatus
	ToolCall  PendingApproval
	Remaining int
	Message   string
}

func (r ApprovalAwaitResult) Text() string {
	switch r.Status {
	case ApprovalAwaitStatusNoOutstanding:
		return "(no outstanding approvals)."
	case ApprovalAwaitStatusCancelled:
		message := strings.TrimSpace(r.Message)
		if message == "" {
			message = "approval wait cancelled by new super_tool submit."
		}
		if r.Remaining == 0 {
			return message + "\n(no outstanding approvals)."
		}
		return fmt.Sprintf("%s\n%d still waiting, await again when ready.", message, r.Remaining)
	case ApprovalAwaitStatusApproved, ApprovalAwaitStatusRejected:
		var b strings.Builder
		if message := strings.TrimSpace(r.Message); message != "" {
			fmt.Fprint(&b, message)
		} else {
			name := strings.TrimSpace(r.ToolCall.ToolName)
			if name == "" {
				name = "tool call"
			}
			if r.ToolCall.ToolCallID != "" {
				fmt.Fprintf(&b, "%s [%s] %s", name, r.ToolCall.ToolCallID, r.Status)
			} else {
				fmt.Fprintf(&b, "%s %s", name, r.Status)
			}
		}
		if r.Status == ApprovalAwaitStatusRejected && strings.TrimSpace(r.ToolCall.Error) != "" {
			fmt.Fprintf(&b, ": %s", strings.TrimSpace(r.ToolCall.Error))
		}
		if strings.TrimSpace(r.Message) == "" {
			if params := strings.TrimSpace(r.ToolCall.ParamsInspect); params != "" {
				fmt.Fprintf(&b, "\n%s", params)
			}
		}
		if r.Remaining == 0 {
			fmt.Fprint(&b, "\n(no outstanding approvals).")
		} else {
			fmt.Fprintf(&b, "\n%d still waiting, await again when ready.", r.Remaining)
		}
		return b.String()
	default:
		if r.Remaining == 0 {
			return "(no outstanding approvals)."
		}
		return fmt.Sprintf("%d still waiting, await again when ready.", r.Remaining)
	}
}

func approvalAwaitResultFromBatch(results []appliedApprovalResult, remaining int) ApprovalAwaitResult {
	if len(results) == 0 {
		return ApprovalAwaitResult{Status: ApprovalAwaitStatusNoOutstanding, Remaining: remaining}
	}
	first := results[0]
	out := ApprovalAwaitResult{
		Status:    first.Status,
		ToolCall:  first.ToolCall,
		Remaining: remaining,
	}
	if len(results) == 1 {
		return out
	}

	approved := 0
	rejected := 0
	for _, result := range results {
		switch result.Status {
		case ApprovalAwaitStatusApproved:
			approved++
		case ApprovalAwaitStatusRejected:
			rejected++
		}
	}
	out.Message = fmt.Sprintf("%d approval decisions applied (%d approved, %d rejected).", len(results), approved, rejected)
	if rejected > 0 && approved == 0 {
		out.Status = ApprovalAwaitStatusRejected
	} else {
		out.Status = ApprovalAwaitStatusApproved
	}
	return out
}

type approvalAwaitDelegate struct {
	mu      sync.Mutex
	nextID  int
	waiters map[int]chan approvalAwaitEvent
}

func newApprovalAwaitDelegate() *approvalAwaitDelegate {
	return &approvalAwaitDelegate{waiters: make(map[int]chan approvalAwaitEvent)}
}

type approvalAwaitEvent struct {
	result ApprovalAwaitResult
	err    error
}

func (d *approvalAwaitDelegate) register(ch chan approvalAwaitEvent) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.nextID++
	id := d.nextID
	d.waiters[id] = ch
	return id
}

func (d *approvalAwaitDelegate) unregister(id int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.waiters, id)
}

func (d *approvalAwaitDelegate) Publish(result ApprovalAwaitResult) {
	if d == nil {
		return
	}
	d.mu.Lock()
	waiters := make([]chan approvalAwaitEvent, 0, len(d.waiters))
	for _, ch := range d.waiters {
		waiters = append(waiters, ch)
	}
	d.mu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- approvalAwaitEvent{result: result}:
		default:
		}
	}
}

func (d *approvalAwaitDelegate) PublishError(err error) {
	if d == nil || err == nil {
		return
	}
	d.mu.Lock()
	waiters := make([]chan approvalAwaitEvent, 0, len(d.waiters))
	for _, ch := range d.waiters {
		waiters = append(waiters, ch)
	}
	d.mu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- approvalAwaitEvent{err: err}:
		default:
		}
	}
}

func (s *Session) AwaitNextApproval(ctx context.Context) (ApprovalAwaitResult, error) {
	if s == nil {
		return ApprovalAwaitResult{Status: ApprovalAwaitStatusNoOutstanding}, nil
	}
	s.mu.Lock()
	lease := s.lease
	s.mu.Unlock()
	if lease != nil {
		if err := s.requireLease(ctx); err != nil {
			return ApprovalAwaitResult{}, err
		}
	}
	s.mu.Lock()
	if s.approvalAwaits == nil {
		s.mu.Unlock()
		return ApprovalAwaitResult{}, fmt.Errorf("approval awaiting is unavailable")
	}
	if s.terminalErr != nil {
		err := s.terminalErr
		s.mu.Unlock()
		return ApprovalAwaitResult{}, err
	}
	ch := make(chan approvalAwaitEvent, 1)
	waiterID := s.approvalAwaits.register(ch)
	approvals, err := s.pendingApprovalsLocked(ctx)
	if err != nil {
		s.approvalAwaits.unregister(waiterID)
		s.mu.Unlock()
		return ApprovalAwaitResult{}, err
	}
	if len(approvals) == 0 {
		s.approvalAwaits.unregister(waiterID)
		s.mu.Unlock()
		return ApprovalAwaitResult{Status: ApprovalAwaitStatusNoOutstanding}, nil
	}
	s.mu.Unlock()
	defer s.approvalAwaits.unregister(waiterID)

	select {
	case event := <-ch:
		if event.err != nil {
			return ApprovalAwaitResult{}, event.err
		}
		return event.result, nil
	case <-ctx.Done():
		return ApprovalAwaitResult{}, ctx.Err()
	}
}
