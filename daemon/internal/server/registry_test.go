package server

import "testing"

func TestQueueApprovalDecisionReplacesPendingDecisionForSameToolCall(t *testing.T) {
	registry := NewRegistry()
	clientID := registry.AddClient(1234)
	registry.UpdateClient(clientID, SessionState{
		PendingApprovals: []PendingApprovalSnapshot{{
			ToolCallID: "tc-1",
			ToolName:   "issues.get",
		}},
	})

	if err := registry.QueueApprovalDecision(ApprovalDecision{
		Action:     ApprovalActionApprove,
		ToolCallID: "tc-1",
	}); err != nil {
		t.Fatalf("QueueApprovalDecision(approve): %v", err)
	}
	if err := registry.QueueApprovalDecision(ApprovalDecision{
		Action:     ApprovalActionReject,
		ToolCallID: "tc-1",
		Message:    "blocked",
	}); err != nil {
		t.Fatalf("QueueApprovalDecision(reject): %v", err)
	}

	decisions := registry.ApprovalDecisions(clientID)
	if len(decisions) != 1 {
		t.Fatalf("ApprovalDecisions() len = %d, want 1", len(decisions))
	}
	if decisions[0].Action != ApprovalActionReject {
		t.Fatalf("ApprovalDecisions()[0].Action = %q, want %q", decisions[0].Action, ApprovalActionReject)
	}
	if decisions[0].ToolCallID != "tc-1" {
		t.Fatalf("ApprovalDecisions()[0].ToolCallID = %q, want %q", decisions[0].ToolCallID, "tc-1")
	}
	if decisions[0].Message != "blocked" {
		t.Fatalf("ApprovalDecisions()[0].Message = %q, want %q", decisions[0].Message, "blocked")
	}
}
