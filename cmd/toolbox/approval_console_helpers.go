package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/a-h/templ"
)

func renderComponentToString(component templ.Component) (string, error) {
	var b bytes.Buffer
	if err := component.Render(context.Background(), &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func mustRenderComponent(component templ.Component) string {
	out, err := renderComponentToString(component)
	if err != nil {
		return ""
	}
	return out
}

func approvalConsolePageDataFromState(state approvalConsoleState) daemonIndexPageData {
	status := strings.TrimSpace(state.SecretStore.Status)
	page := daemonIndexPageData{
		StatusText: firstNonEmpty(status, "unavailable"),
		Available:  status != "" && status != "unavailable",
		Locked:     status == "locked",
	}
	if page.StatusText == "" {
		page.StatusText = "Unavailable"
	}
	return page
}

func approvalConsoleInitialSignals() string {
	return `{"liveState":"Connecting","settingsOpen":false,"drafts":{},"rejectReason":"","rejectDialogOpen":false,"rejectSession":"","unlockKey":"","message":""}`
}

func approvalDOMID(prefix, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "unknown"
	}
	return prefix + base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func approvalSessionID(session approvalConsoleSession) string {
	return approvalDOMID("approval-session-", session.TBSession)
}

func approvalPackageID(session approvalConsoleSession, group approvalConsolePackageGroup) string {
	return approvalDOMID("approval-package-", session.TBSession+"/"+group.PackageKey)
}

func approvalRowID(callID string) string {
	return approvalDOMID("approval-row-", callID)
}

func approvalDetailsID(callID string) string {
	return approvalDOMID("approval-details-", callID)
}

func jsQuote(s string) string {
	return strconv.Quote(s)
}

func sessionToolCallIDs(session approvalConsoleSession) []string {
	var ids []string
	for _, group := range session.PackageGroups {
		for _, call := range group.ToolCalls {
			ids = append(ids, call.ToolCallID)
		}
	}
	return ids
}

func jsStringArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, jsQuote(value))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

func sessionDecisionCountExpr(session approvalConsoleSession, decision string) string {
	ids := jsStringArray(sessionToolCallIDs(session))
	return fmt.Sprintf("Object.entries($drafts).filter(([k,v]) => %s.includes(k) && v === %s).length", ids, jsQuote(decision))
}

func sessionUnchangedCountExpr(session approvalConsoleSession) string {
	approve := sessionDecisionCountExpr(session, "approve")
	reject := sessionDecisionCountExpr(session, "reject")
	return fmt.Sprintf("%d - (%s) - (%s)", countConsoleSessionCalls(session), approve, reject)
}

func sessionSubmitLabelExpr(session approvalConsoleSession) string {
	approve := sessionDecisionCountExpr(session, "approve")
	reject := sessionDecisionCountExpr(session, "reject")
	return fmt.Sprintf("'Submit ' + ((%s) + (%s)) + ' decisions'", approve, reject)
}

func sessionSubmitDisabledExpr(session approvalConsoleSession) string {
	approve := sessionDecisionCountExpr(session, "approve")
	reject := sessionDecisionCountExpr(session, "reject")
	return fmt.Sprintf("((%s) + (%s)) === 0", approve, reject)
}

func setSessionDraftsExpr(session approvalConsoleSession, decision string) string {
	parts := []string{"...$drafts"}
	for _, id := range sessionToolCallIDs(session) {
		parts = append(parts, jsQuote(id)+":"+jsQuote(decision))
	}
	return "$drafts = {" + strings.Join(parts, ",") + "}"
}

func clearSessionDraftsExpr(session approvalConsoleSession) string {
	return setSessionDraftsExpr(session, "leave")
}

func decisionClickExpr(toolCallID, decision string) string {
	return fmt.Sprintf("$drafts = {...$drafts,%s:%s}", jsQuote(toolCallID), jsQuote(decision))
}

func decisionActiveExpr(toolCallID, decision string) string {
	ref := "$drafts[" + jsQuote(toolCallID) + "]"
	if decision == "leave" {
		return ref + " === undefined || " + ref + " === 'leave'"
	}
	return ref + " === " + jsQuote(decision)
}

func submitSessionExpr(session approvalConsoleSession) string {
	reject := sessionDecisionCountExpr(session, "reject")
	sessionID := jsQuote(session.TBSession)
	payload := "{payload:{session:" + sessionID + ",drafts:$drafts,reason:''}}"
	return fmt.Sprintf("if ((%s) > 0) { $rejectSession = %s; $rejectDialogOpen = true } else { $rejectReason = ''; @post('/approval-console/decisions', %s) }", reject, sessionID, payload)
}

func rejectDialogTitleExpr() string {
	count := "Object.values($drafts).filter(v => v === 'reject').length"
	return fmt.Sprintf("'Reject ' + (%s) + ' approval' + ((%s) === 1 ? '?' : 's?')", count, count)
}

func submitRejectDialogExpr() string {
	return "@post('/approval-console/decisions', {payload:{session:$rejectSession,drafts:$drafts,reason:$rejectReason}})"
}

func cancelRejectDialogExpr() string {
	return "$rejectDialogOpen = false; $rejectReason = ''; $rejectSession = ''"
}

func liveDotWarnExpr() string {
	return "$liveState === 'Connecting'"
}

func liveDotOffExpr() string {
	return "$liveState === 'Disconnected'"
}

func datastarFetchStateExpr() string {
	return "if (evt.detail.el === document.body && evt.detail.type === 'started') { $liveState = 'Connecting' } else if (evt.detail.el === document.body && (evt.detail.type === 'retrying' || evt.detail.type === 'error' || evt.detail.type === 'retries-failed')) { $liveState = 'Disconnected' } else if (evt.detail.el !== document.body && evt.detail.type === 'finished') { $liveState = 'Live' }"
}

func componentHTML(component templ.Component) string {
	return mustRenderComponent(component)
}

func jsonSignalPatch(values map[string]any) string {
	payload, err := json.Marshal(values)
	if err != nil {
		return "{}"
	}
	return string(payload)
}
