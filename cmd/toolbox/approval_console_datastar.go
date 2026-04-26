package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/solidarity-ai/toolbox/daemon"
)

type approvalConsoleFragments struct {
	Topbar      string
	SecretPanel string
	Main        string
}

func renderApprovalConsoleFragments(state approvalConsoleState) approvalConsoleFragments {
	page := approvalConsolePageDataFromState(state)
	return approvalConsoleFragments{
		Topbar:      componentHTML(ApprovalTopbar(state)),
		SecretPanel: componentHTML(SecretPanel(page)),
		Main:        componentHTML(ApprovalMain(state)),
	}
}

func applyApprovalConsoleDrafts(ctx context.Context, control daemonHTTPControl, req approvalConsoleSubmitRequest) approvalConsoleSubmitResult {
	result := approvalConsoleSubmitResult{}
	if control == nil {
		result.Errors = append(result.Errors, "approvals unavailable")
		return result
	}

	state := approvalConsoleStateForHTTP(control)
	pendingSessions := make(map[string]string)
	for _, session := range state.Sessions {
		for _, group := range session.PackageGroups {
			for _, call := range group.ToolCalls {
				pendingSessions[call.ToolCallID] = session.TBSession
			}
		}
	}

	ids := make([]string, 0, len(req.Drafts))
	for id := range req.Drafts {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	queuedAt := time.Now().UTC()
	clientDecisionID := fmt.Sprintf("approval-console-%d", queuedAt.UnixNano())
	reason := strings.TrimSpace(req.Reason)
	var decisions []daemon.ApprovalDecision
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if req.Session != "" && pendingSessions[id] != req.Session {
			continue
		}
		action := ""
		switch strings.TrimSpace(req.Drafts[id]) {
		case "approve":
			action = daemon.ApprovalActionApprove
		case "reject":
			action = daemon.ApprovalActionReject
		default:
			continue
		}
		if _, ok := pendingSessions[id]; !ok {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: approval no longer pending", id))
			continue
		}
		decision := daemon.ApprovalDecision{
			Action:           action,
			ToolCallID:       id,
			ClientDecisionID: clientDecisionID,
			QueuedAt:         queuedAt,
		}
		if action == daemon.ApprovalActionReject {
			decision.Message = reason
		}
		decisions = append(decisions, decision)
	}

	if len(decisions) == 0 {
		if len(result.Errors) == 0 {
			result.Errors = append(result.Errors, "no approve or reject choices to submit")
		}
		return result
	}
	if err := control.ApplyApprovals(ctx, decisions); err != nil {
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	result.Accepted = len(decisions)
	return result
}

func approvalConsoleDraftResetPatch(drafts map[string]string) map[string]any {
	reset := make(map[string]any, len(drafts))
	for id := range drafts {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		reset[id] = "leave"
	}
	return reset
}

func writeDatastarHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
}

func writeDatastarPatchElements(w io.Writer, selector, mode, html string) {
	_, _ = io.WriteString(w, "event: datastar-patch-elements\n")
	if selector != "" {
		_, _ = fmt.Fprintf(w, "data: selector %s\n", selector)
	}
	if mode != "" {
		_, _ = fmt.Fprintf(w, "data: mode %s\n", mode)
	}
	for _, line := range strings.Split(html, "\n") {
		_, _ = fmt.Fprintf(w, "data: elements %s\n", line)
	}
	_, _ = io.WriteString(w, "\n")
}

func writeDatastarPatchSignals(w io.Writer, signals map[string]any) {
	payload, err := json.Marshal(signals)
	if err != nil {
		payload = []byte(`{}`)
	}
	_, _ = io.WriteString(w, "event: datastar-patch-signals\n")
	_, _ = fmt.Fprintf(w, "data: signals %s\n\n", payload)
}

func isDatastarRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Datastar-Request"), "true") || strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

func writeApprovalConsoleDatastarResponse(w http.ResponseWriter, control daemonHTTPControl, signals map[string]any) {
	writeDatastarHeaders(w)
	patch := make(map[string]any, len(signals)+1)
	patch["liveState"] = "Live"
	for key, value := range signals {
		patch[key] = value
	}
	if len(patch) > 0 {
		writeDatastarPatchSignals(w, patch)
	}
	writeApprovalConsolePatches(w, nil, approvalConsoleStateForHTTP(control))
}

func writeApprovalConsolePatches(w io.Writer, previous *approvalConsoleFragments, state approvalConsoleState) approvalConsoleFragments {
	next := renderApprovalConsoleFragments(state)
	if previous == nil || previous.Topbar != next.Topbar {
		writeDatastarPatchElements(w, "#approval-topbar", "outer", next.Topbar)
	}
	if previous == nil || previous.SecretPanel != next.SecretPanel {
		writeDatastarPatchElements(w, "#secret-panel", "outer", next.SecretPanel)
	}
	if previous == nil || previous.Main != next.Main {
		writeDatastarPatchElements(w, "#approval-main", "outer", next.Main)
	}
	return next
}

func approvalConsoleErrorMessage(result approvalConsoleSubmitResult) string {
	if len(result.Errors) > 0 {
		return strings.Join(result.Errors, "\n")
	}
	return ""
}
