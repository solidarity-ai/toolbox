package codemodesession

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/dop251/goja"
	repl "github.com/mackross/repljs"
	"github.com/mackross/repljs/jswire"
	"github.com/mackross/repljs/model"
	_ "modernc.org/sqlite"
)

const (
	factTypeToolCallStarted       = "ToolCallStarted"
	factTypeToolCallNeedsApproval = "ToolCallNeedsApproval"
	factTypeToolCallCompleted     = "ToolCallCompleted"
	factTypeToolCallFailed        = "ToolCallFailed"
	factTypeToolCallCancelled     = "ToolCallCancelled"
	factTypeToolCallUnknown       = "ToolCallUnknown"
)

type toolCallStatus string

const (
	toolCallStatusStarted       toolCallStatus = "started"
	toolCallStatusNeedsApproval toolCallStatus = "needsApproval"
	toolCallStatusSuccess       toolCallStatus = "success"
	toolCallStatusFailed        toolCallStatus = "failed"
	toolCallStatusCancelled     toolCallStatus = "cancelled"
	toolCallStatusUnknown       toolCallStatus = "unknown"
)

type toolCallSnapshot struct {
	ToolCallID string
	ToolName   string
	Status     toolCallStatus
	ApprovalID string
	Params     []byte
	Result     []byte
	Error      string
}

type toolCallStartedFact struct {
	Session    repl.SessionID `json:"session"`
	ToolCallID string         `json:"tool_call_id"`
	ToolName   string         `json:"tool_name"`
	Params     []byte         `json:"params,omitempty"`
	At         time.Time      `json:"at"`
}

func (toolCallStartedFact) FactType() string { return factTypeToolCallStarted }

type toolCallNeedsApprovalFact struct {
	Session    repl.SessionID `json:"session"`
	ToolCallID string         `json:"tool_call_id"`
	ApprovalID string         `json:"approval_id,omitempty"`
	ToolName   string         `json:"tool_name"`
	Params     []byte         `json:"params,omitempty"`
	At         time.Time      `json:"at"`
}

func (toolCallNeedsApprovalFact) FactType() string { return factTypeToolCallNeedsApproval }

type toolCallCompletedFact struct {
	Session    repl.SessionID `json:"session"`
	ToolCallID string         `json:"tool_call_id"`
	Result     []byte         `json:"result,omitempty"`
	At         time.Time      `json:"at"`
}

func (toolCallCompletedFact) FactType() string { return factTypeToolCallCompleted }

type toolCallFailedFact struct {
	Session    repl.SessionID `json:"session"`
	ToolCallID string         `json:"tool_call_id"`
	Error      string         `json:"error"`
	At         time.Time      `json:"at"`
}

func (toolCallFailedFact) FactType() string { return factTypeToolCallFailed }

type toolCallCancelledFact struct {
	Session    repl.SessionID `json:"session"`
	ToolCallID string         `json:"tool_call_id"`
	At         time.Time      `json:"at"`
}

func (toolCallCancelledFact) FactType() string { return factTypeToolCallCancelled }

type toolCallUnknownFact struct {
	Session    repl.SessionID `json:"session"`
	ToolCallID string         `json:"tool_call_id"`
	At         time.Time      `json:"at"`
}

func (toolCallUnknownFact) FactType() string { return factTypeToolCallUnknown }

type toolCallJournal interface {
	EnsureStarted(sessionID repl.SessionID, toolCallID, toolName string, params []byte) error
	EnsureNeedsApproval(sessionID repl.SessionID, toolCallID, approvalID, toolName string, params []byte) error
	EnsureCompleted(sessionID repl.SessionID, toolCallID string, result []byte) error
	EnsureFailed(sessionID repl.SessionID, toolCallID, errText string) error
	EnsureCancelled(sessionID repl.SessionID, toolCallID string) error
	EnsureUnknown(sessionID repl.SessionID, toolCallID string) error
	Snapshot(sessionID repl.SessionID, toolCallID string) (toolCallSnapshot, bool, error)
	Recover(ctx context.Context, sessionID repl.SessionID) error
}

func newMemoryToolCallJournal(st repl.Store) toolCallJournal {
	return &memoryToolCallJournal{
		store:     st,
		snapshots: make(map[repl.SessionID]map[string]toolCallSnapshot),
	}
}

func newSQLiteToolCallJournal(st repl.Store, dbPath string) toolCallJournal {
	return &sqliteToolCallJournal{
		store:     st,
		dbPath:    dbPath,
		snapshots: make(map[repl.SessionID]map[string]toolCallSnapshot),
		loaded:    make(map[repl.SessionID]bool),
	}
}

type memoryToolCallJournal struct {
	store repl.Store

	mu        sync.RWMutex
	snapshots map[repl.SessionID]map[string]toolCallSnapshot
}

func (j *memoryToolCallJournal) EnsureStarted(sessionID repl.SessionID, toolCallID, toolName string, params []byte) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if ok {
		switch current.Status {
		case toolCallStatusStarted:
			return nil
		case toolCallStatusNeedsApproval:
		default:
			return nil
		}
	}
	return j.appendFact(toolCallStartedFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Params:     cloneToolCallBytes(params),
		At:         time.Now().UTC(),
	})
}

func (j *memoryToolCallJournal) EnsureNeedsApproval(sessionID repl.SessionID, toolCallID, approvalID, toolName string, params []byte) error {
	return j.ensureInitialFact(sessionID, toolCallID, toolCallNeedsApprovalFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		ApprovalID: approvalID,
		ToolName:   toolName,
		Params:     cloneToolCallBytes(params),
		At:         time.Now().UTC(),
	})
}

func (j *memoryToolCallJournal) EnsureCompleted(sessionID repl.SessionID, toolCallID string, result []byte) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusSuccess || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallCompletedFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		Result:     cloneToolCallBytes(result),
		At:         time.Now().UTC(),
	})
}

func (j *memoryToolCallJournal) EnsureFailed(sessionID repl.SessionID, toolCallID, errText string) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusFailed || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallFailedFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		Error:      errText,
		At:         time.Now().UTC(),
	})
}

func (j *memoryToolCallJournal) EnsureCancelled(sessionID repl.SessionID, toolCallID string) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusCancelled || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallCancelledFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		At:         time.Now().UTC(),
	})
}

func (j *memoryToolCallJournal) EnsureUnknown(sessionID repl.SessionID, toolCallID string) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusUnknown || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallUnknownFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		At:         time.Now().UTC(),
	})
}

func (j *memoryToolCallJournal) Snapshot(sessionID repl.SessionID, toolCallID string) (toolCallSnapshot, bool, error) {
	j.mu.RLock()
	defer j.mu.RUnlock()
	sessionCalls := j.snapshots[sessionID]
	if sessionCalls == nil {
		return toolCallSnapshot{}, false, nil
	}
	snapshot, ok := sessionCalls[toolCallID]
	if !ok {
		return toolCallSnapshot{}, false, nil
	}
	return cloneToolCallSnapshot(snapshot), true, nil
}

func (j *memoryToolCallJournal) Recover(context.Context, repl.SessionID) error {
	return nil
}

func (j *memoryToolCallJournal) ensureInitialFact(sessionID repl.SessionID, toolCallID string, fact model.Fact) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if ok && current.Status != "" {
		return nil
	}
	return j.appendFact(fact)
}

func (j *memoryToolCallJournal) appendFact(fact model.Fact) error {
	if err := j.store.AppendFact(context.Background(), fact); err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	applyToolCallFact(j.snapshots, fact)
	return nil
}

type sqliteToolCallJournal struct {
	store  repl.Store
	dbPath string

	mu        sync.RWMutex
	snapshots map[repl.SessionID]map[string]toolCallSnapshot
	loaded    map[repl.SessionID]bool
}

func (j *sqliteToolCallJournal) EnsureStarted(sessionID repl.SessionID, toolCallID, toolName string, params []byte) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if ok {
		switch current.Status {
		case toolCallStatusStarted:
			return nil
		case toolCallStatusNeedsApproval:
		default:
			return nil
		}
	}
	return j.appendFact(toolCallStartedFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		ToolName:   toolName,
		Params:     cloneToolCallBytes(params),
		At:         time.Now().UTC(),
	})
}

func (j *sqliteToolCallJournal) EnsureNeedsApproval(sessionID repl.SessionID, toolCallID, approvalID, toolName string, params []byte) error {
	return j.ensureInitialFact(sessionID, toolCallID, toolCallNeedsApprovalFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		ApprovalID: approvalID,
		ToolName:   toolName,
		Params:     cloneToolCallBytes(params),
		At:         time.Now().UTC(),
	})
}

func (j *sqliteToolCallJournal) EnsureCompleted(sessionID repl.SessionID, toolCallID string, result []byte) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusSuccess || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallCompletedFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		Result:     cloneToolCallBytes(result),
		At:         time.Now().UTC(),
	})
}

func (j *sqliteToolCallJournal) EnsureFailed(sessionID repl.SessionID, toolCallID, errText string) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusFailed || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallFailedFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		Error:      errText,
		At:         time.Now().UTC(),
	})
}

func (j *sqliteToolCallJournal) EnsureCancelled(sessionID repl.SessionID, toolCallID string) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusCancelled || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallCancelledFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		At:         time.Now().UTC(),
	})
}

func (j *sqliteToolCallJournal) EnsureUnknown(sessionID repl.SessionID, toolCallID string) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("unknown tool call %q", toolCallID)
	}
	if current.Status == toolCallStatusUnknown || isTerminalToolCallStatus(current.Status) {
		return nil
	}
	return j.appendFact(toolCallUnknownFact{
		Session:    sessionID,
		ToolCallID: toolCallID,
		At:         time.Now().UTC(),
	})
}

func (j *sqliteToolCallJournal) Snapshot(sessionID repl.SessionID, toolCallID string) (toolCallSnapshot, bool, error) {
	if err := j.loadSession(sessionID); err != nil {
		return toolCallSnapshot{}, false, err
	}

	j.mu.RLock()
	defer j.mu.RUnlock()
	sessionCalls := j.snapshots[sessionID]
	if sessionCalls == nil {
		return toolCallSnapshot{}, false, nil
	}
	snapshot, ok := sessionCalls[toolCallID]
	if !ok {
		return toolCallSnapshot{}, false, nil
	}
	return cloneToolCallSnapshot(snapshot), true, nil
}

func (j *sqliteToolCallJournal) Recover(_ context.Context, sessionID repl.SessionID) error {
	if err := j.loadSession(sessionID); err != nil {
		return err
	}

	j.mu.RLock()
	sessionCalls := j.snapshots[sessionID]
	toolCallIDs := make([]string, 0, len(sessionCalls))
	for toolCallID, snapshot := range sessionCalls {
		if snapshot.Status == toolCallStatusStarted {
			toolCallIDs = append(toolCallIDs, toolCallID)
		}
	}
	j.mu.RUnlock()

	for _, toolCallID := range toolCallIDs {
		if err := j.EnsureUnknown(sessionID, toolCallID); err != nil {
			return err
		}
	}
	return nil
}

func (j *sqliteToolCallJournal) ensureInitialFact(sessionID repl.SessionID, toolCallID string, fact model.Fact) error {
	current, ok, err := j.Snapshot(sessionID, toolCallID)
	if err != nil {
		return err
	}
	if ok && current.Status != "" {
		return nil
	}
	return j.appendFact(fact)
}

func (j *sqliteToolCallJournal) appendFact(fact model.Fact) error {
	if err := j.store.AppendFact(context.Background(), fact); err != nil {
		return err
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	applyToolCallFact(j.snapshots, fact)
	return nil
}

func (j *sqliteToolCallJournal) loadSession(sessionID repl.SessionID) error {
	j.mu.RLock()
	if j.loaded[sessionID] {
		j.mu.RUnlock()
		return nil
	}
	j.mu.RUnlock()

	db, err := sql.Open("sqlite", j.dbPath)
	if err != nil {
		return fmt.Errorf("open tool call sqlite view: %w", err)
	}
	defer db.Close()

	const q = `
SELECT fact_type, payload
FROM facts
WHERE fact_type IN (?, ?, ?, ?, ?, ?)
ORDER BY id ASC`

	rows, err := db.QueryContext(context.Background(), q,
		factTypeToolCallStarted,
		factTypeToolCallNeedsApproval,
		factTypeToolCallCompleted,
		factTypeToolCallFailed,
		factTypeToolCallCancelled,
		factTypeToolCallUnknown,
	)
	if err != nil {
		return fmt.Errorf("load tool calls for session %q: %w", sessionID, err)
	}
	defer rows.Close()

	snapshots := make(map[repl.SessionID]map[string]toolCallSnapshot)
	for rows.Next() {
		var factType string
		var payload string
		if err := rows.Scan(&factType, &payload); err != nil {
			return fmt.Errorf("scan tool call fact: %w", err)
		}
		if err := applyToolCallJSONPayload(snapshots, factType, []byte(payload)); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate tool call facts: %w", err)
	}

	j.mu.Lock()
	defer j.mu.Unlock()
	if j.snapshots[sessionID] == nil {
		j.snapshots[sessionID] = snapshots[sessionID]
	} else if snapshots[sessionID] != nil {
		for toolCallID, snapshot := range snapshots[sessionID] {
			j.snapshots[sessionID][toolCallID] = snapshot
		}
	}
	j.loaded[sessionID] = true
	return nil
}

func applyToolCallFact(dst map[repl.SessionID]map[string]toolCallSnapshot, fact model.Fact) {
	switch f := fact.(type) {
	case toolCallStartedFact:
		setToolCallSnapshot(dst, f.Session, f.ToolCallID, func(snapshot *toolCallSnapshot) {
			snapshot.ToolCallID = f.ToolCallID
			snapshot.ToolName = f.ToolName
			snapshot.ApprovalID = ""
			snapshot.Params = cloneToolCallBytes(f.Params)
			snapshot.Result = nil
			snapshot.Error = ""
			snapshot.Status = toolCallStatusStarted
		})
	case toolCallNeedsApprovalFact:
		setToolCallSnapshot(dst, f.Session, f.ToolCallID, func(snapshot *toolCallSnapshot) {
			snapshot.ToolCallID = f.ToolCallID
			snapshot.ToolName = f.ToolName
			snapshot.ApprovalID = f.ApprovalID
			snapshot.Params = cloneToolCallBytes(f.Params)
			snapshot.Result = nil
			snapshot.Error = ""
			snapshot.Status = toolCallStatusNeedsApproval
		})
	case toolCallCompletedFact:
		setToolCallSnapshot(dst, f.Session, f.ToolCallID, func(snapshot *toolCallSnapshot) {
			snapshot.ToolCallID = f.ToolCallID
			snapshot.ApprovalID = ""
			snapshot.Result = cloneToolCallBytes(f.Result)
			snapshot.Error = ""
			snapshot.Status = toolCallStatusSuccess
		})
	case toolCallFailedFact:
		setToolCallSnapshot(dst, f.Session, f.ToolCallID, func(snapshot *toolCallSnapshot) {
			snapshot.ToolCallID = f.ToolCallID
			snapshot.ApprovalID = ""
			snapshot.Result = nil
			snapshot.Error = f.Error
			snapshot.Status = toolCallStatusFailed
		})
	case toolCallCancelledFact:
		setToolCallSnapshot(dst, f.Session, f.ToolCallID, func(snapshot *toolCallSnapshot) {
			snapshot.ToolCallID = f.ToolCallID
			snapshot.ApprovalID = ""
			snapshot.Result = nil
			snapshot.Error = ""
			snapshot.Status = toolCallStatusCancelled
		})
	case toolCallUnknownFact:
		setToolCallSnapshot(dst, f.Session, f.ToolCallID, func(snapshot *toolCallSnapshot) {
			snapshot.ToolCallID = f.ToolCallID
			snapshot.ApprovalID = ""
			snapshot.Result = nil
			snapshot.Error = ""
			snapshot.Status = toolCallStatusUnknown
		})
	}
}

func applyToolCallJSONPayload(dst map[repl.SessionID]map[string]toolCallSnapshot, factType string, payload []byte) error {
	switch factType {
	case factTypeToolCallStarted:
		var fact toolCallStartedFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return fmt.Errorf("decode %s: %w", factTypeToolCallStarted, err)
		}
		applyToolCallFact(dst, fact)
	case factTypeToolCallNeedsApproval:
		var fact toolCallNeedsApprovalFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return fmt.Errorf("decode %s: %w", factTypeToolCallNeedsApproval, err)
		}
		applyToolCallFact(dst, fact)
	case factTypeToolCallCompleted:
		var fact toolCallCompletedFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return fmt.Errorf("decode %s: %w", factTypeToolCallCompleted, err)
		}
		applyToolCallFact(dst, fact)
	case factTypeToolCallFailed:
		var fact toolCallFailedFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return fmt.Errorf("decode %s: %w", factTypeToolCallFailed, err)
		}
		applyToolCallFact(dst, fact)
	case factTypeToolCallCancelled:
		var fact toolCallCancelledFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return fmt.Errorf("decode %s: %w", factTypeToolCallCancelled, err)
		}
		applyToolCallFact(dst, fact)
	case factTypeToolCallUnknown:
		var fact toolCallUnknownFact
		if err := json.Unmarshal(payload, &fact); err != nil {
			return fmt.Errorf("decode %s: %w", factTypeToolCallUnknown, err)
		}
		applyToolCallFact(dst, fact)
	}
	return nil
}

func setToolCallSnapshot(dst map[repl.SessionID]map[string]toolCallSnapshot, sessionID repl.SessionID, toolCallID string, mutate func(*toolCallSnapshot)) {
	if dst[sessionID] == nil {
		dst[sessionID] = make(map[string]toolCallSnapshot)
	}
	snapshot := dst[sessionID][toolCallID]
	mutate(&snapshot)
	dst[sessionID][toolCallID] = snapshot
}

func cloneToolCallSnapshot(in toolCallSnapshot) toolCallSnapshot {
	out := in
	out.Params = cloneToolCallBytes(in.Params)
	out.Result = cloneToolCallBytes(in.Result)
	return out
}

func cloneToolCallBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	return append([]byte(nil), in...)
}

func toolCallSnapshotValue(snapshot toolCallSnapshot) (any, error) {
	view := map[string]any{
		"toolCallId": snapshot.ToolCallID,
		"toolName":   snapshot.ToolName,
		"status":     string(snapshot.Status),
	}
	if len(snapshot.Params) > 0 {
		params, err := decodeStoredJSWireValue(snapshot.Params)
		if err != nil {
			return nil, err
		}
		view["params"] = params
	}
	switch snapshot.Status {
	case toolCallStatusNeedsApproval:
		approval := map[string]any{}
		if snapshot.ApprovalID != "" {
			approval["approvalId"] = snapshot.ApprovalID
		}
		view["approval"] = approval
	case toolCallStatusSuccess:
		result, err := decodeStoredJSWireValue(snapshot.Result)
		if err != nil {
			return nil, err
		}
		view["result"] = result
	case toolCallStatusFailed:
		view["error"] = snapshot.Error
	}
	return view, nil
}

func isTerminalToolCallStatus(status toolCallStatus) bool {
	switch status {
	case toolCallStatusSuccess, toolCallStatusFailed, toolCallStatusCancelled, toolCallStatusUnknown:
		return true
	default:
		return false
	}
}

func decodeStoredJSWireValue(raw []byte) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	value, err := jswire.DecodeGoja(goja.New(), raw)
	if err != nil {
		return nil, err
	}
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil, nil
	}
	return value.Export(), nil
}
