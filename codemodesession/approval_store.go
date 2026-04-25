package codemodesession

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	repl "github.com/mackross/repljs"
	"github.com/mackross/repljs/jswire"
	"github.com/mackross/repljs/model"
	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
)

const (
	approvalCallStatusPending   = "pending"
	approvalCallStatusExecuting = "executing"
	approvalCallStatusCompleted = "completed"
	approvalCallStatusFailed    = "failed"
)

type PendingApproval struct {
	TBSession     string
	ToolCallID    string
	CellID        string
	ToolName      string
	ParamsInspect string
	EffectID      string
	Status        string
	Error         string
}

type ApprovalDecision struct {
	ToolCallID string
	Approved   bool
	Reason     string
}

type approvalStore interface {
	BeginSubmit(sessionID repl.SessionID)
	AbortSubmit(sessionID repl.SessionID)
	RecordPendingToolCall(sessionID repl.SessionID, toolCallID, effectID, toolName, reviewedToolKey string, params []byte) error
	CommitSubmit(sessionID repl.SessionID, cellID repl.CellID) error
	PendingApprovals(ctx context.Context, sessionID repl.SessionID) ([]PendingApproval, error)
	ApplyDecisions(ctx context.Context, sessionID repl.SessionID, decisions []ApprovalDecision, prepared toolset.PreparedToolset, st repl.Store, toolCalls toolCallJournal, executor *invoke.Executor) ([]appliedApprovalResult, error)
	RecoverSession(ctx context.Context, sessionID repl.SessionID, toolCalls toolCallJournal) error
}

type approvalCallState struct {
	ToolCallID      string
	EffectID        string
	CellID          repl.CellID
	ToolName        string
	ReviewedToolKey string
	Params          []byte
	Status          string
	Error           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type approvalSubmitCollection struct {
	Session repl.SessionID
	Calls   []approvalCallState
}

type appliedApprovalResult struct {
	Status   ApprovalAwaitStatus
	ToolCall PendingApproval
}

func newMemoryApprovalStore() approvalStore {
	return &memoryApprovalStore{
		calls: make(map[repl.SessionID]map[string]approvalCallState),
	}
}

type memoryApprovalStore struct {
	mu     sync.Mutex
	active *approvalSubmitCollection
	calls  map[repl.SessionID]map[string]approvalCallState
}

func (s *memoryApprovalStore) BeginSubmit(sessionID repl.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = &approvalSubmitCollection{Session: sessionID}
}

func (s *memoryApprovalStore) AbortSubmit(sessionID repl.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active.Session == sessionID {
		s.active = nil
	}
}

func (s *memoryApprovalStore) RecordPendingToolCall(sessionID repl.SessionID, toolCallID, effectID, toolName, reviewedToolKey string, params []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.Session != sessionID {
		return nil
	}
	now := time.Now().UTC()
	s.active.Calls = append(s.active.Calls, approvalCallState{
		ToolCallID:      toolCallID,
		EffectID:        effectID,
		ToolName:        toolName,
		ReviewedToolKey: reviewedToolKey,
		Params:          append([]byte(nil), params...),
		Status:          approvalCallStatusPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	return nil
}

func (s *memoryApprovalStore) CommitSubmit(sessionID repl.SessionID, cellID repl.CellID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.Session != sessionID {
		return nil
	}
	active := s.active
	s.active = nil
	if len(active.Calls) == 0 {
		return nil
	}
	if s.calls[sessionID] == nil {
		s.calls[sessionID] = make(map[string]approvalCallState)
	}
	for _, call := range active.Calls {
		call.CellID = cellID
		s.calls[sessionID][call.ToolCallID] = call
	}
	return nil
}

func (s *memoryApprovalStore) PendingApprovals(_ context.Context, sessionID repl.SessionID) ([]PendingApproval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clonePendingApprovals(s.calls[sessionID]), nil
}

func (s *memoryApprovalStore) ApplyDecisions(ctx context.Context, sessionID repl.SessionID, decisions []ApprovalDecision, prepared toolset.PreparedToolset, st repl.Store, toolCalls toolCallJournal, executor *invoke.Executor) ([]appliedApprovalResult, error) {
	s.mu.Lock()
	ordered, err := validateApprovalDecisionsLocked(s.calls[sessionID], decisions)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	results := make([]appliedApprovalResult, 0, len(ordered))
	for _, item := range ordered {
		if item.decision.Approved {
			if err := s.setCallStatus(sessionID, item.call.ToolCallID, approvalCallStatusExecuting); err != nil {
				return nil, err
			}
		}
		result, err := applyApprovalDecision(ctx, sessionID, item.call, item.decision, prepared, st, toolCalls, executor)
		if err != nil {
			if item.decision.Approved && shouldReturnApprovalToPending(toolCalls, sessionID, item.call.ToolCallID) {
				_ = s.setCallStatus(sessionID, item.call.ToolCallID, approvalCallStatusPending)
			}
			return nil, err
		}
		s.deleteCall(sessionID, item.call.ToolCallID)
		results = append(results, result)
	}
	return results, nil
}

func (s *memoryApprovalStore) RecoverSession(context.Context, repl.SessionID, toolCallJournal) error {
	return nil
}

func newSQLiteApprovalStore(db *sql.DB) (approvalStore, error) {
	store := &sqliteApprovalStore{db: db}
	if err := store.ensureSchema(); err != nil {
		return nil, err
	}
	return store, nil
}

type sqliteApprovalStore struct {
	db *sql.DB

	mu     sync.Mutex
	active *approvalSubmitCollection
}

func (s *sqliteApprovalStore) BeginSubmit(sessionID repl.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = &approvalSubmitCollection{Session: sessionID}
}

func (s *sqliteApprovalStore) AbortSubmit(sessionID repl.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active.Session == sessionID {
		s.active = nil
	}
}

func (s *sqliteApprovalStore) RecordPendingToolCall(sessionID repl.SessionID, toolCallID, effectID, toolName, reviewedToolKey string, params []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil || s.active.Session != sessionID {
		return nil
	}
	now := time.Now().UTC()
	s.active.Calls = append(s.active.Calls, approvalCallState{
		ToolCallID:      toolCallID,
		EffectID:        effectID,
		ToolName:        toolName,
		ReviewedToolKey: reviewedToolKey,
		Params:          append([]byte(nil), params...),
		Status:          approvalCallStatusPending,
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	return nil
}

func (s *sqliteApprovalStore) CommitSubmit(sessionID repl.SessionID, cellID repl.CellID) error {
	s.mu.Lock()
	if s.active == nil || s.active.Session != sessionID {
		s.mu.Unlock()
		return nil
	}
	active := s.active
	s.active = nil
	s.mu.Unlock()
	if len(active.Calls) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin approval tx: %w", err)
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	for _, call := range active.Calls {
		if _, err := tx.ExecContext(context.Background(), `
INSERT OR REPLACE INTO approval_tool_calls
  (tool_call_id, session, cell_id, effect_id, tool_name, reviewed_tool_key, params, status, error, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			call.ToolCallID,
			string(sessionID),
			string(cellID),
			call.EffectID,
			call.ToolName,
			call.ReviewedToolKey,
			call.Params,
			call.Status,
			call.Error,
			call.CreatedAt.Format(time.RFC3339Nano),
			call.UpdatedAt.Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("insert approval call %q: %w", call.ToolCallID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit approval tx: %w", err)
	}
	tx = nil
	return nil
}

func (s *sqliteApprovalStore) PendingApprovals(ctx context.Context, sessionID repl.SessionID) ([]PendingApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT tool_call_id, cell_id, tool_name, params, effect_id, status, error, created_at, updated_at
FROM approval_tool_calls
WHERE session = ? AND status = ?
ORDER BY created_at ASC, tool_call_id ASC`,
		string(sessionID),
		approvalCallStatusPending,
	)
	if err != nil {
		return nil, fmt.Errorf("query pending approvals: %w", err)
	}
	defer rows.Close()

	var out []PendingApproval
	for rows.Next() {
		var (
			call      approvalCallState
			cell      string
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(
			&call.ToolCallID,
			&cell,
			&call.ToolName,
			&call.Params,
			&call.EffectID,
			&call.Status,
			&call.Error,
			&createdAt,
			&updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending approvals: %w", err)
		}
		call.CellID = repl.CellID(cell)
		call.CreatedAt = parseApprovalTime(createdAt)
		call.UpdatedAt = parseApprovalTime(updatedAt)
		out = append(out, pendingApprovalFromState(call))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending approvals: %w", err)
	}
	return out, nil
}

func (s *sqliteApprovalStore) ApplyDecisions(ctx context.Context, sessionID repl.SessionID, decisions []ApprovalDecision, prepared toolset.PreparedToolset, st repl.Store, toolCalls toolCallJournal, executor *invoke.Executor) ([]appliedApprovalResult, error) {
	ordered, err := s.loadDecisionCalls(ctx, sessionID, decisions)
	if err != nil {
		return nil, err
	}

	results := make([]appliedApprovalResult, 0, len(ordered))
	for _, item := range ordered {
		if item.decision.Approved {
			if err := s.setCallStatus(ctx, sessionID, item.call.ToolCallID, approvalCallStatusExecuting); err != nil {
				return nil, err
			}
		}
		result, err := applyApprovalDecision(ctx, sessionID, item.call, item.decision, prepared, st, toolCalls, executor)
		if err != nil {
			if item.decision.Approved && shouldReturnApprovalToPending(toolCalls, sessionID, item.call.ToolCallID) {
				_ = s.setCallStatus(ctx, sessionID, item.call.ToolCallID, approvalCallStatusPending)
			}
			return nil, err
		}
		if err := s.deleteCall(ctx, sessionID, item.call.ToolCallID); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *sqliteApprovalStore) RecoverSession(ctx context.Context, sessionID repl.SessionID, toolCalls toolCallJournal) error {
	rows, err := s.db.QueryContext(ctx, `
SELECT tool_call_id
FROM approval_tool_calls
WHERE session = ? AND status = ?
ORDER BY created_at ASC, tool_call_id ASC`,
		string(sessionID),
		approvalCallStatusExecuting,
	)
	if err != nil {
		return fmt.Errorf("query executing approval calls: %w", err)
	}
	defer rows.Close()

	var toolCallIDs []string
	for rows.Next() {
		var toolCallID string
		if err := rows.Scan(&toolCallID); err != nil {
			return fmt.Errorf("scan executing approval call: %w", err)
		}
		toolCallIDs = append(toolCallIDs, strings.TrimSpace(toolCallID))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate executing approval calls: %w", err)
	}

	for _, toolCallID := range toolCallIDs {
		if toolCalls != nil {
			snapshot, ok, err := toolCalls.Snapshot(sessionID, toolCallID)
			if err != nil {
				return err
			}
			if ok && !isTerminalToolCallStatus(snapshot.Status) {
				if err := toolCalls.EnsureUnknown(sessionID, toolCallID); err != nil {
					return err
				}
			}
		}
		if err := s.deleteCall(ctx, sessionID, toolCallID); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteApprovalStore) ensureSchema() error {
	if s == nil || s.db == nil {
		return nil
	}
	_, err := s.db.ExecContext(context.Background(), `
CREATE TABLE IF NOT EXISTS approval_tool_calls (
  tool_call_id      TEXT PRIMARY KEY,
  session           TEXT NOT NULL,
  cell_id           TEXT NOT NULL,
  effect_id         TEXT NOT NULL DEFAULT '',
  tool_name         TEXT NOT NULL,
  reviewed_tool_key TEXT NOT NULL DEFAULT '',
  params            BLOB NOT NULL,
  status            TEXT NOT NULL DEFAULT 'pending',
  error             TEXT NOT NULL DEFAULT '',
  created_at        TEXT NOT NULL,
  updated_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_approval_tool_calls_session ON approval_tool_calls (session, created_at, tool_call_id);
`)
	if err != nil {
		return fmt.Errorf("migrate approval tables: %w", err)
	}
	return nil
}

type decisionWithCall struct {
	decision ApprovalDecision
	call     approvalCallState
}

func validateApprovalDecisionsLocked(calls map[string]approvalCallState, decisions []ApprovalDecision) ([]decisionWithCall, error) {
	normalized := normalizeApprovalDecisions(decisions)
	if len(normalized) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(normalized))
	out := make([]decisionWithCall, 0, len(normalized))
	for _, decision := range normalized {
		if decision.ToolCallID == "" {
			return nil, fmt.Errorf("approval decision missing tool call id")
		}
		if _, ok := seen[decision.ToolCallID]; ok {
			return nil, fmt.Errorf("duplicate approval decision for %q", decision.ToolCallID)
		}
		seen[decision.ToolCallID] = struct{}{}
		call, ok := calls[decision.ToolCallID]
		if !ok || call.Status != approvalCallStatusPending {
			return nil, fmt.Errorf("unknown approval tool call %q", decision.ToolCallID)
		}
		out = append(out, decisionWithCall{decision: decision, call: cloneApprovalCallState(call)})
	}
	return out, nil
}

func (s *sqliteApprovalStore) loadDecisionCalls(ctx context.Context, sessionID repl.SessionID, decisions []ApprovalDecision) ([]decisionWithCall, error) {
	normalized := normalizeApprovalDecisions(decisions)
	if len(normalized) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(normalized))
	out := make([]decisionWithCall, 0, len(normalized))
	for _, decision := range normalized {
		if decision.ToolCallID == "" {
			return nil, fmt.Errorf("approval decision missing tool call id")
		}
		if _, ok := seen[decision.ToolCallID]; ok {
			return nil, fmt.Errorf("duplicate approval decision for %q", decision.ToolCallID)
		}
		seen[decision.ToolCallID] = struct{}{}
		call, err := s.loadCall(ctx, sessionID, decision.ToolCallID)
		if err != nil {
			return nil, err
		}
		out = append(out, decisionWithCall{decision: decision, call: call})
	}
	return out, nil
}

func (s *sqliteApprovalStore) loadCall(ctx context.Context, sessionID repl.SessionID, toolCallID string) (approvalCallState, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT tool_call_id, cell_id, effect_id, tool_name, reviewed_tool_key, params, status, error, created_at, updated_at
FROM approval_tool_calls
WHERE session = ? AND tool_call_id = ? AND status = ?`,
		string(sessionID),
		toolCallID,
		approvalCallStatusPending,
	)

	var (
		call      approvalCallState
		cell      string
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&call.ToolCallID,
		&cell,
		&call.EffectID,
		&call.ToolName,
		&call.ReviewedToolKey,
		&call.Params,
		&call.Status,
		&call.Error,
		&createdAt,
		&updatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return approvalCallState{}, fmt.Errorf("unknown approval tool call %q", toolCallID)
		}
		return approvalCallState{}, fmt.Errorf("load approval tool call %q: %w", toolCallID, err)
	}
	call.CellID = repl.CellID(cell)
	call.CreatedAt = parseApprovalTime(createdAt)
	call.UpdatedAt = parseApprovalTime(updatedAt)
	return call, nil
}

func normalizeApprovalDecisions(decisions []ApprovalDecision) []ApprovalDecision {
	if len(decisions) == 0 {
		return nil
	}
	out := make([]ApprovalDecision, 0, len(decisions))
	for _, decision := range decisions {
		normalized := ApprovalDecision{
			ToolCallID: strings.TrimSpace(decision.ToolCallID),
			Approved:   decision.Approved,
			Reason:     strings.TrimSpace(decision.Reason),
		}
		out = append(out, normalized)
	}
	return out
}

func cloneApprovalCallState(in approvalCallState) approvalCallState {
	out := in
	out.Params = append([]byte(nil), in.Params...)
	return out
}

func clonePendingApprovals(calls map[string]approvalCallState) []PendingApproval {
	if len(calls) == 0 {
		return nil
	}
	states := make([]approvalCallState, 0, len(calls))
	for _, call := range calls {
		if call.Status != approvalCallStatusPending {
			continue
		}
		states = append(states, cloneApprovalCallState(call))
	}
	sort.Slice(states, func(i, j int) bool {
		if !states[i].CreatedAt.Equal(states[j].CreatedAt) {
			return states[i].CreatedAt.Before(states[j].CreatedAt)
		}
		return states[i].ToolCallID < states[j].ToolCallID
	})
	out := make([]PendingApproval, 0, len(states))
	for _, call := range states {
		out = append(out, pendingApprovalFromState(call))
	}
	return out
}

func pendingApprovalFromState(call approvalCallState) PendingApproval {
	return PendingApproval{
		ToolCallID:    call.ToolCallID,
		CellID:        string(call.CellID),
		ToolName:      call.ToolName,
		ParamsInspect: inspectApprovalParams(call.Params),
		EffectID:      call.EffectID,
		Status:        call.Status,
		Error:         call.Error,
	}
}

func inspectApprovalParams(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	inspection, err := jswire.Describe(raw)
	if err != nil {
		return ""
	}
	return inspection.Full
}

func parseApprovalTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func applyApprovalDecision(ctx context.Context, sessionID repl.SessionID, call approvalCallState, decision ApprovalDecision, prepared toolset.PreparedToolset, st repl.Store, toolCalls toolCallJournal, executor *invoke.Executor) (appliedApprovalResult, error) {
	result := appliedApprovalResult{
		Status: ApprovalAwaitStatusRejected,
		ToolCall: PendingApproval{
			ToolCallID:    call.ToolCallID,
			CellID:        string(call.CellID),
			ToolName:      call.ToolName,
			ParamsInspect: inspectApprovalParams(call.Params),
			EffectID:      call.EffectID,
		},
	}

	if decision.Approved {
		if toolCalls == nil {
			return appliedApprovalResult{}, fmt.Errorf("tool call journal unavailable")
		}
		if err := toolCalls.EnsureStarted(sessionID, call.ToolCallID, call.ToolName, call.Params); err != nil {
			return appliedApprovalResult{}, err
		}
		outcome, err := executeApprovedToolCall(ctx, sessionID, call.CellID, call, prepared, st, toolCalls, executor)
		if err != nil {
			return appliedApprovalResult{}, err
		}
		result.Status = ApprovalAwaitStatusApproved
		result.ToolCall.Error = outcome.Error
		return result, nil
	}

	outcome, err := rejectPendingToolCall(sessionID, call.ToolCallID, decision.Reason, toolCalls)
	if err != nil {
		return appliedApprovalResult{}, err
	}
	result.ToolCall.Error = outcome.Error
	return result, nil
}

func shouldReturnApprovalToPending(toolCalls toolCallJournal, sessionID repl.SessionID, toolCallID string) bool {
	if toolCalls == nil {
		return true
	}
	snapshot, ok, err := toolCalls.Snapshot(sessionID, toolCallID)
	if err != nil || !ok {
		return true
	}
	return snapshot.Status == toolCallStatusNeedsApproval
}

type approvalExecutionOutcome struct {
	EffectID string
	Status   string
	Error    string
}

func executeApprovedToolCall(ctx context.Context, sessionID repl.SessionID, cellID repl.CellID, call approvalCallState, prepared toolset.PreparedToolset, st repl.Store, toolCalls toolCallJournal, executor *invoke.Executor) (approvalExecutionOutcome, error) {
	if st == nil {
		return approvalExecutionOutcome{}, fmt.Errorf("approval execution store unavailable")
	}

	effectID := repl.EffectID(uuid.NewString())
	replayPolicy := repl.ReplayNonReplayable
	tool, ok := prepared.Tool(call.ToolName)
	matchesReviewed := ok && call.ReviewedToolKey != "" && tool.ApprovalFingerprint() == call.ReviewedToolKey
	if matchesReviewed {
		replayPolicy = replayPolicyForTool(tool.Effect, tool.Idempotent)
	}
	if err := st.AppendFact(ctx, model.EffectStarted{
		Session:      sessionID,
		Effect:       model.EffectID(effectID),
		Cell:         model.CellID(cellID),
		FunctionName: call.ToolName,
		Params:       append([]byte(nil), call.Params...),
		ReplayPolicy: model.ReplayPolicy(replayPolicy),
		At:           time.Now().UTC(),
	}); err != nil {
		return approvalExecutionOutcome{}, fmt.Errorf("journal approval effect start for %q: %w", call.ToolCallID, err)
	}

	fail := func(errText string) (approvalExecutionOutcome, error) {
		if appendErr := st.AppendFact(ctx, model.EffectFailed{
			Session:      sessionID,
			Effect:       model.EffectID(effectID),
			ErrorMessage: errText,
			At:           time.Now().UTC(),
		}); appendErr != nil {
			return approvalExecutionOutcome{}, fmt.Errorf("journal approval effect failure for %q: %w", call.ToolCallID, appendErr)
		}
		if toolCalls != nil {
			var err error
			if isToolCallContextCancellation(errText) {
				err = toolCalls.EnsureCancelled(sessionID, call.ToolCallID)
			} else {
				err = toolCalls.EnsureFailed(sessionID, call.ToolCallID, errText)
			}
			if err != nil {
				return approvalExecutionOutcome{}, err
			}
		}
		return approvalExecutionOutcome{
			EffectID: string(effectID),
			Status:   approvalCallStatusFailed,
			Error:    errText,
		}, nil
	}

	if !matchesReviewed {
		return fail(approvedToolReviewMismatchMessage(call, tool, ok))
	}
	if !ok {
		return fail(fmt.Sprintf("tool %s unavailable for approved execution", call.ToolName))
	}
	args, err := decodeRuntimeArgs(call.Params)
	if err != nil {
		return fail(fmt.Sprintf("tool %s args: %v", call.ToolName, err))
	}
	raw, err := runPreparedTool(ctx, executor, prepared, call.ToolName, args)
	if err != nil {
		return fail(err.Error())
	}
	result, err := encodeRuntimeResult(currentReturnType(tool), raw)
	if err != nil {
		return fail(fmt.Sprintf("tool %s result: %v", call.ToolName, err))
	}
	if err := st.AppendFact(ctx, model.EffectCompleted{
		Session: sessionID,
		Effect:  model.EffectID(effectID),
		Result:  append([]byte(nil), result...),
		At:      time.Now().UTC(),
	}); err != nil {
		return approvalExecutionOutcome{}, fmt.Errorf("journal approval effect completion for %q: %w", call.ToolCallID, err)
	}
	if toolCalls != nil {
		if err := toolCalls.EnsureCompleted(sessionID, call.ToolCallID, result); err != nil {
			return approvalExecutionOutcome{}, err
		}
	}
	return approvalExecutionOutcome{
		EffectID: string(effectID),
		Status:   approvalCallStatusCompleted,
	}, nil
}

func rejectPendingToolCall(sessionID repl.SessionID, toolCallID, message string, toolCalls toolCallJournal) (approvalExecutionOutcome, error) {
	errText := normalizeApprovalRejectMessage(message)
	if toolCalls == nil {
		return approvalExecutionOutcome{}, fmt.Errorf("tool call journal unavailable")
	}
	if err := toolCalls.EnsureFailed(sessionID, toolCallID, errText); err != nil {
		return approvalExecutionOutcome{}, err
	}
	return approvalExecutionOutcome{
		Status: approvalCallStatusFailed,
		Error:  errText,
	}, nil
}

func normalizeApprovalRejectMessage(message string) string {
	errText := strings.TrimSpace(message)
	if errText == "" {
		return "approval rejected"
	}
	return errText
}

func approvedToolReviewMismatchMessage(call approvalCallState, tool toolset.PreparedTool, ok bool) string {
	label := strings.TrimSpace(call.ToolName)
	if ok {
		if key := strings.TrimSpace(tool.ToolApprovalKey()); key != "" {
			label = key
		} else if name := strings.TrimSpace(tool.Name); name != "" {
			label = name
		}
	}
	if label == "" {
		label = "tool"
	}
	return fmt.Sprintf("approved tool %s no longer matches the reviewed version; resubmit to review the current tool", label)
}

func (s *memoryApprovalStore) setCallStatus(sessionID repl.SessionID, toolCallID, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionCalls := s.calls[sessionID]
	call, ok := sessionCalls[toolCallID]
	if !ok {
		return fmt.Errorf("unknown approval tool call %q", toolCallID)
	}
	call.Status = status
	call.UpdatedAt = time.Now().UTC()
	sessionCalls[toolCallID] = call
	return nil
}

func (s *memoryApprovalStore) deleteCall(sessionID repl.SessionID, toolCallID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sessionCalls := s.calls[sessionID]; sessionCalls != nil {
		delete(sessionCalls, toolCallID)
		if len(sessionCalls) == 0 {
			delete(s.calls, sessionID)
		}
	}
}

func (s *sqliteApprovalStore) setCallStatus(ctx context.Context, sessionID repl.SessionID, toolCallID, status string) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE approval_tool_calls
SET status = ?, updated_at = ?
WHERE session = ? AND tool_call_id = ?`,
		status,
		time.Now().UTC().Format(time.RFC3339Nano),
		string(sessionID),
		toolCallID,
	)
	if err != nil {
		return fmt.Errorf("update approval tool call %q status: %w", toolCallID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("update approval tool call %q status: %w", toolCallID, err)
	}
	if rows == 0 {
		return fmt.Errorf("unknown approval tool call %q", toolCallID)
	}
	return nil
}

func (s *sqliteApprovalStore) deleteCall(ctx context.Context, sessionID repl.SessionID, toolCallID string) error {
	if _, err := s.db.ExecContext(ctx, `
DELETE FROM approval_tool_calls
WHERE session = ? AND tool_call_id = ?`,
		string(sessionID),
		toolCallID,
	); err != nil {
		return fmt.Errorf("delete approval tool call %q: %w", toolCallID, err)
	}
	return nil
}
