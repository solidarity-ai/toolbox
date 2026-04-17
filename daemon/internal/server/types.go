package server

import "time"

const (
	ApprovalActionApprove = "approve"
	ApprovalActionReject  = "reject"
)

type PendingApprovalSnapshot struct {
	ToolCallID    string `json:"tool_call_id"`
	ToolName      string `json:"tool_name"`
	ParamsInspect string `json:"params_inspect,omitempty"`
	EffectID      string `json:"effect_id,omitempty"`
	Status        string `json:"status,omitempty"`
	Error         string `json:"error,omitempty"`
}

type ApprovalDecision struct {
	Action     string `json:"action"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Message    string `json:"message,omitempty"`
}

type SessionState struct {
	Mode             string                    `json:"mode"`
	WorkingDir       string                    `json:"working_dir"`
	PreparedTools    []string                  `json:"prepared_tools,omitempty"`
	PendingApprovals []PendingApprovalSnapshot `json:"pending_approvals,omitempty"`
}

type ClientSnapshot struct {
	PID              int                       `json:"pid"`
	Mode             string                    `json:"mode"`
	WorkingDir       string                    `json:"working_dir"`
	PreparedTools    []string                  `json:"prepared_tools,omitempty"`
	PendingApprovals []PendingApprovalSnapshot `json:"pending_approvals,omitempty"`
	ConnectedAt      time.Time                 `json:"connected_at"`
	LastSyncAt       time.Time                 `json:"last_sync_at"`
}
