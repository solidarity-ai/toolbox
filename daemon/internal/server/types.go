package server

import "time"

const (
	ApprovalActionApprove = "approve"
	ApprovalActionReject  = "reject"
)

type PendingApprovalSnapshot struct {
	ToolCallID          string                  `json:"tool_call_id"`
	TBSession           string                  `json:"tb_session,omitempty"`
	IntentText          string                  `json:"intent_text,omitempty"`
	IntentSource        string                  `json:"intent_source,omitempty"`
	IntentUpdatedAt     string                  `json:"intent_updated_at,omitempty"`
	ToolName            string                  `json:"tool_name"`
	FullToolName        string                  `json:"full_tool_name,omitempty"`
	PackageKey          string                  `json:"package_key,omitempty"`
	PackageLabel        string                  `json:"package_label,omitempty"`
	ToolLabel           string                  `json:"tool_label,omitempty"`
	Description         string                  `json:"description,omitempty"`
	RequiresCredentials bool                    `json:"requires_credentials,omitempty"`
	ParamsInspect       string                  `json:"params_inspect,omitempty"`
	Presentation        string                  `json:"presentation,omitempty"`
	EffectID            string                  `json:"effect_id,omitempty"`
	CellID              string                  `json:"cell_id,omitempty"`
	Status              string                  `json:"status,omitempty"`
	Error               string                  `json:"error,omitempty"`
	CreatedAt           string                  `json:"created_at,omitempty"`
	UpdatedAt           string                  `json:"updated_at,omitempty"`
	QueuedDecision      *QueuedApprovalDecision `json:"queued_decision,omitempty"`
}

type QueuedApprovalDecision struct {
	Action           string    `json:"action"`
	Message          string    `json:"message,omitempty"`
	ClientDecisionID string    `json:"client_decision_id,omitempty"`
	QueuedAt         time.Time `json:"queued_at"`
}

type ApprovalDecision struct {
	Action           string    `json:"action"`
	ToolCallID       string    `json:"tool_call_id,omitempty"`
	Message          string    `json:"message,omitempty"`
	ClientDecisionID string    `json:"client_decision_id,omitempty"`
	QueuedAt         time.Time `json:"queued_at,omitempty"`
}

type SessionState struct {
	Mode             string                    `json:"mode"`
	Locked           bool                      `json:"locked,omitempty"`
	BoundTBSession   string                    `json:"bound_tb_session,omitempty"`
	IntentText       string                    `json:"intent_text,omitempty"`
	IntentSource     string                    `json:"intent_source,omitempty"`
	IntentUpdatedAt  string                    `json:"intent_updated_at,omitempty"`
	WorkingDir       string                    `json:"working_dir"`
	PreparedTools    []string                  `json:"prepared_tools,omitempty"`
	PendingApprovals []PendingApprovalSnapshot `json:"pending_approvals,omitempty"`
}

type ClientSnapshot struct {
	PID              int                       `json:"pid"`
	Mode             string                    `json:"mode"`
	Locked           bool                      `json:"locked,omitempty"`
	BoundTBSession   string                    `json:"bound_tb_session,omitempty"`
	IntentText       string                    `json:"intent_text,omitempty"`
	IntentSource     string                    `json:"intent_source,omitempty"`
	IntentUpdatedAt  string                    `json:"intent_updated_at,omitempty"`
	WorkingDir       string                    `json:"working_dir"`
	PreparedTools    []string                  `json:"prepared_tools,omitempty"`
	PendingApprovals []PendingApprovalSnapshot `json:"pending_approvals,omitempty"`
	ConnectedAt      time.Time                 `json:"connected_at"`
	LastSyncAt       time.Time                 `json:"last_sync_at"`
}
