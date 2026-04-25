package codemodesession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	replsqlite "github.com/mackross/repljs/store/sqlite"
	"github.com/solidarity-ai/toolbox/assembler"
	"github.com/solidarity-ai/toolbox/testutil/tooltest"
	tooldef "github.com/solidarity-ai/toolbox/tool"
	"github.com/solidarity-ai/toolbox/toolset"
)

func TestValidateTBSession(t *testing.T) {
	for _, id := range []string{"abcdef", "123abc", "00ff10"} {
		if err := ValidateTBSession(id); err != nil {
			t.Fatalf("ValidateTBSession(%q): %v", id, err)
		}
	}
	for _, id := range []string{"", "ABCDEF", "abc12", "abc1234", "abc12g"} {
		if err := ValidateTBSession(id); err == nil {
			t.Fatalf("ValidateTBSession(%q) unexpectedly succeeded", id)
		}
	}
}

func TestSessionDBPathUsesOverride(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	path, err := SessionDBPath("abc123")
	if err != nil {
		t.Fatalf("SessionDBPath(): %v", err)
	}
	if path != filepath.Join(root, "abc123.db") {
		t.Fatalf("SessionDBPath() = %q, want %q", path, filepath.Join(root, "abc123.db"))
	}
	info, err := os.Stat(root)
	if err != nil {
		t.Fatalf("Stat(%q): %v", root, err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("sessions dir perms = %#o, want 0700", info.Mode().Perm())
	}
}

func TestCreateFreshRetriesCollision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", root, err)
	}
	collisionPath := filepath.Join(root, "abc123.db")
	if err := os.WriteFile(collisionPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", collisionPath, err)
	}

	previousReader := tbSessionReader
	tbSessionReader = bytes.NewReader([]byte{
		0xab, 0xc1, 0x23, // abc123
		0xde, 0xf4, 0x56, // def456
	})
	t.Cleanup(func() {
		tbSessionReader = previousReader
	})

	session, err := CreateFresh(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("CreateFresh(): %v", err)
	}
	defer session.Close()

	if got := session.TBSession(); got != "def456" {
		t.Fatalf("CreateFresh().TBSession() = %q, want %q", got, "def456")
	}
	path, err := SessionDBPath(session.TBSession())
	if err != nil {
		t.Fatalf("SessionDBPath(%q): %v", session.TBSession(), err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session db perms = %#o, want 0600", info.Mode().Perm())
	}
}

func TestOpenExistingReopensNotebookByTBSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()
	session, err := CreateNew(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}
	if got := session.Submit(ctx, "const value: number = 1"); !strings.Contains(got, "cell 1") {
		t.Fatalf("first submit = %q, want first cell output", got)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	reopened, err := OpenExisting(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("OpenExisting(): %v", err)
	}
	defer reopened.Close()
	if !reopened.Resumed() {
		t.Fatal("OpenExisting().Resumed() = false, want true")
	}
	out := reopened.Submit(ctx, "value + 2")
	if !strings.Contains(out, "=> 3") {
		t.Fatalf("reopened submit = %q, want completion preview 3", out)
	}
}

func TestOpenExistingUsesRequestedTBSessionNotLatest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()

	first, err := CreateNew(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("CreateNew(first): %v", err)
	}
	if got := first.Submit(ctx, "const notebook = \"first\"\nnotebook"); !strings.Contains(got, `"first"`) {
		t.Fatalf("first submit = %q, want first notebook state", got)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	second, err := CreateNew(ctx, "def456", currentDir)
	if err != nil {
		t.Fatalf("CreateNew(second): %v", err)
	}
	if got := second.Submit(ctx, "const notebook = \"second\"\nnotebook"); !strings.Contains(got, `"second"`) {
		t.Fatalf("second submit = %q, want second notebook state", got)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("Close(second): %v", err)
	}

	reopenFirst, err := OpenExisting(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("OpenExisting(first): %v", err)
	}
	defer reopenFirst.Close()
	if got := reopenFirst.Submit(ctx, "notebook"); !strings.Contains(got, `"first"`) {
		t.Fatalf("reopened first notebook = %q, want first notebook state", got)
	}

	reopenSecond, err := OpenExisting(ctx, "def456", currentDir)
	if err != nil {
		t.Fatalf("OpenExisting(second): %v", err)
	}
	defer reopenSecond.Close()
	if got := reopenSecond.Submit(ctx, "notebook"); !strings.Contains(got, `"second"`) {
		t.Fatalf("reopened second notebook = %q, want second notebook state", got)
	}
}

func TestOpenExistingFailsWhileLeaseIsLive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()

	session, err := CreateNew(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}
	defer session.Close()

	_, err = OpenExisting(ctx, "abc123", currentDir)
	if !errors.Is(err, ErrSessionAlreadyActive) {
		t.Fatalf("OpenExisting() error = %v, want %v", err, ErrSessionAlreadyActive)
	}
}

func TestLeaseCloseBeforeStartDoesNotBlock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	dbPath, err := reserveSessionDB("abc123")
	if err != nil {
		t.Fatalf("reserveSessionDB(): %v", err)
	}
	st, err := replsqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open sqlite store: %v", err)
	}
	defer st.Close()
	if err := ensurePersistentSessionSchema(ctx, st.DB()); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}

	lease, err := acquireSessionLease(ctx, st.DB(), "abc123")
	if err != nil {
		t.Fatalf("acquireSessionLease(): %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- lease.Close()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close(): %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close() blocked before lease heartbeat was started")
	}
}

func TestExpiredLeaseCanBeTakenOver(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	previousHeartbeat := leaseHeartbeat
	leaseHeartbeat = time.Hour
	t.Cleanup(func() {
		leaseHeartbeat = previousHeartbeat
	})

	ctx := context.Background()
	currentDir := t.TempDir()

	first, err := CreateNew(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}
	defer first.Close()

	forceLeaseState(t, "abc123", "stale-owner", leaseNow().Add(-time.Minute))

	second, err := OpenExisting(ctx, "abc123", currentDir)
	if err != nil {
		t.Fatalf("OpenExisting() after expiry: %v", err)
	}
	defer second.Close()

	if got := second.Submit(ctx, "const recovered = \"ok\"\nrecovered"); !strings.Contains(got, `"ok"`) {
		t.Fatalf("second submit = %q, want recovered notebook state", got)
	}
	if _, err := first.PendingApprovals(ctx); !errors.Is(err, ErrSessionLeaseLost) {
		t.Fatalf("first PendingApprovals() error = %v, want %v", err, ErrSessionLeaseLost)
	}
}

func TestLeaseLossFencesOperationsAndWakesAwaiter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()

	session, err := CreateNew(ctx, "abc123", currentDir, SessionConfig{
		PreparedTools: tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{
			ToolApprovals: map[string]bool{
				"calc.calc.add": true,
			},
		}),
	})
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}
	defer session.Close()

	if got := session.Submit(ctx, `calc.calc.add(2, 3)`); !strings.Contains(got, "cell 1") {
		t.Fatalf("Submit() = %q, want pending approval cell", got)
	}
	approvals, err := session.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(before loss): %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("PendingApprovals(before loss) len = %d, want 1", len(approvals))
	}

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	waitErrCh := make(chan error, 1)
	go func() {
		_, err := session.AwaitNextApproval(waitCtx)
		waitErrCh <- err
	}()

	time.Sleep(100 * time.Millisecond)
	forceLeaseState(t, "abc123", "other-owner", leaseNow().Add(time.Minute))

	if _, err := session.PendingApprovals(ctx); !errors.Is(err, ErrSessionLeaseLost) {
		t.Fatalf("PendingApprovals(after loss) error = %v, want %v", err, ErrSessionLeaseLost)
	}

	select {
	case err := <-waitErrCh:
		if !errors.Is(err, ErrSessionLeaseLost) {
			t.Fatalf("AwaitNextApproval() error = %v, want %v", err, ErrSessionLeaseLost)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AwaitNextApproval() did not wake after lease loss")
	}

	if got := session.Submit(ctx, "1"); !strings.Contains(got, "session lease lost") {
		t.Fatalf("Submit(after loss) = %q, want lease lost failure", got)
	}
	if _, err := session.AwaitNextApproval(ctx); !errors.Is(err, ErrSessionLeaseLost) {
		t.Fatalf("AwaitNextApproval(after loss) error = %v, want %v", err, ErrSessionLeaseLost)
	}
	if err := session.ApplyApprovals(ctx, []ApprovalDecision{{
		ToolCallID: approvals[0].ToolCallID,
		Approved:   true,
	}}); !errors.Is(err, ErrSessionLeaseLost) {
		t.Fatalf("ApplyApprovals(after loss) error = %v, want %v", err, ErrSessionLeaseLost)
	}
}

func TestUnlockedManagerPendingApprovalsStayHiddenAfterRestartUntilSessionReloads(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()
	prepared := prepareIssuesApprovalToolset(t)

	first := NewUnlockedManager(currentDir, SessionConfig{PreparedTools: prepared})
	tbSession, err := first.CreateFreshSession(ctx)
	if err != nil {
		t.Fatalf("CreateFreshSession(): %v", err)
	}
	if _, err := first.Submit(ctx, tbSession, `issues.get("I-1")`); err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	restarted := NewUnlockedManager(currentDir, SessionConfig{PreparedTools: prepared})
	defer restarted.Close()

	approvals, err := restarted.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(): %v", err)
	}
	if len(approvals) != 0 {
		t.Fatalf("PendingApprovals() = %#v, want none before session reload", approvals)
	}

	if _, err := restarted.Submit(ctx, tbSession, `"reload"`); err != nil {
		t.Fatalf("Submit(reload): %v", err)
	}
	approvals, err = restarted.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(after reload): %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("PendingApprovals(after reload) len = %d, want 1", len(approvals))
	}
	if approvals[0].TBSession != tbSession {
		t.Fatalf("PendingApprovals(after reload)[0].TBSession = %q, want %q", approvals[0].TBSession, tbSession)
	}
}

func TestUnlockedManagerApplyApprovalsRequiresLoadedSessionAfterRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()
	prepared := prepareIssuesApprovalToolset(t)

	first := NewUnlockedManager(currentDir, SessionConfig{PreparedTools: prepared})
	tbSession, err := first.CreateFreshSession(ctx)
	if err != nil {
		t.Fatalf("CreateFreshSession(): %v", err)
	}
	if _, err := first.Submit(ctx, tbSession, `issues.get("I-1")`); err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	approvals, err := first.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(before close): %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("PendingApprovals(before close) len = %d, want 1", len(approvals))
	}
	toolCallID := approvals[0].ToolCallID
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	restarted := NewUnlockedManager(currentDir, SessionConfig{PreparedTools: prepared})
	defer restarted.Close()

	if err := restarted.ApplyApprovals(ctx, []ApprovalDecision{{
		ToolCallID: toolCallID,
		Approved:   false,
		Reason:     "recovered after restart",
	}}); err == nil || !strings.Contains(err.Error(), `unknown approval tool call "`) {
		t.Fatalf("ApplyApprovals() error = %v, want unknown approval tool call", err)
	}

	if _, err := restarted.Submit(ctx, tbSession, `"reload"`); err != nil {
		t.Fatalf("Submit(reload): %v", err)
	}

	if err := restarted.ApplyApprovals(ctx, []ApprovalDecision{{
		ToolCallID: toolCallID,
		Approved:   false,
		Reason:     "recovered after restart",
	}}); err != nil {
		t.Fatalf("ApplyApprovals(after reload): %v", err)
	}
	remaining, err := restarted.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(after apply): %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("PendingApprovals(after apply) = %#v, want none", remaining)
	}
}

func TestPersistentSessionRecoversExecutingApprovalAsUnknown(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()
	prepared := prepareIssuesApprovalToolset(t)

	first, err := CreateNew(ctx, "abc124", currentDir, SessionConfig{PreparedTools: prepared})
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}
	if got := first.Submit(ctx, `var task = issues.get("I-1");
task.toolCallId`); !strings.Contains(got, "cell 1") {
		t.Fatalf("Submit() = %q, want committed task cell", got)
	}
	approvals, err := first.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(before close): %v", err)
	}
	if len(approvals) != 1 {
		t.Fatalf("PendingApprovals(before close) len = %d, want 1", len(approvals))
	}
	toolCallID := approvals[0].ToolCallID
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	forceApprovalCallStatus(t, "abc124", toolCallID, approvalCallStatusExecuting)

	second, err := OpenExisting(ctx, "abc124", currentDir, SessionConfig{PreparedTools: prepared})
	if err != nil {
		t.Fatalf("OpenExisting(): %v", err)
	}
	defer second.Close()

	recovered, err := second.PendingApprovals(ctx)
	if err != nil {
		t.Fatalf("PendingApprovals(after reopen): %v", err)
	}
	if len(recovered) != 0 {
		t.Fatalf("PendingApprovals(after reopen) = %#v, want none", recovered)
	}

	inspect := second.Submit(ctx, `console.log(JSON.stringify($tool_call("`+toolCallID+`")));
"done"`)
	if !strings.Contains(inspect, `"status":"unknown"`) {
		t.Fatalf("inspect(after reopen) = %q, want unknown status", inspect)
	}
	if !strings.Contains(inspect, `"toolCallId":"`+toolCallID+`"`) {
		t.Fatalf("inspect(after reopen) = %q, want toolCallId %q", inspect, toolCallID)
	}
}

func TestPersistentSessionRecoversOrphanedStartedInlineToolCallAsUnknown(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	originalTimeout := DefaultSubmitTimeout
	DefaultSubmitTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		DefaultSubmitTimeout = originalTimeout
	})

	ctx := context.Background()
	currentDir := t.TempDir()
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	prepared := prepareBlockingToolset(t, false, started, release)

	first, err := CreateNew(ctx, "abc125", currentDir, SessionConfig{PreparedTools: prepared})
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}

	submitDone := make(chan string, 1)
	go func() {
		submitDone <- first.Submit(ctx, `await blocker.blocker.wait()`)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for inline tool to start")
	}

	var out string
	select {
	case out = <-submitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for inline submit to return")
	}
	if !strings.Contains(out, "context deadline exceeded") {
		t.Fatalf("Submit() = %q, want context deadline exceeded", out)
	}

	toolCallID := latestToolCallIDForFact(t, "abc125", factTypeToolCallStarted)
	if toolCallID == "" {
		t.Fatal("latest started tool call id = empty, want durable tool call start")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first): %v", err)
	}

	second, err := OpenExisting(ctx, "abc125", currentDir, SessionConfig{PreparedTools: prepared})
	if err != nil {
		t.Fatalf("OpenExisting(): %v", err)
	}
	defer second.Close()
	defer close(release)

	inspect := second.Submit(ctx, `console.log(JSON.stringify($tool_call("`+toolCallID+`")));
"done"`)
	if !strings.Contains(inspect, `"status":"unknown"`) {
		t.Fatalf("inspect(after reopen) = %q, want unknown status", inspect)
	}
	if !strings.Contains(inspect, `"toolCallId":"`+toolCallID+`"`) {
		t.Fatalf("inspect(after reopen) = %q, want toolCallId %q", inspect, toolCallID)
	}
}

func TestManagerSerializesFirstOpenPerTBSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	t.Setenv(sessionsDirEnv, root)

	ctx := context.Background()
	currentDir := t.TempDir()
	prepared := tooltest.PrepareToolset(t, tooltest.LocalPackageDecl("calc"), toolset.Config{})

	session, err := CreateNew(ctx, "abc123", currentDir, SessionConfig{PreparedTools: prepared})
	if err != nil {
		t.Fatalf("CreateNew(): %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close(seed): %v", err)
	}

	manager := NewUnlockedManager(currentDir, SessionConfig{PreparedTools: prepared})
	defer manager.Close()

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := manager.Submit(ctx, "abc123", `(globalThis as any).__openCount = ((globalThis as any).__openCount || 0) + 1; (globalThis as any).__openCount`)
			results <- err
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	for err := range results {
		if err != nil {
			t.Fatalf("concurrent Submit() error = %v, want nil", err)
		}
	}
}

func forceLeaseState(t *testing.T, tbSession, ownerToken string, expiresAt time.Time) {
	t.Helper()

	dbPath, err := SessionDBPath(tbSession)
	if err != nil {
		t.Fatalf("SessionDBPath(%q): %v", tbSession, err)
	}
	store, err := replsqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer store.Close()

	if _, err := store.DB().ExecContext(context.Background(), `
UPDATE session_lease
SET owner_token = ?, expires_at = ?, updated_at = ?
WHERE singleton_id = ?`,
		ownerToken,
		expiresAt.UTC().Format(time.RFC3339Nano),
		leaseNow().Format(time.RFC3339Nano),
		leaseSingletonID,
	); err != nil {
		t.Fatalf("update session_lease: %v", err)
	}
}

func forceApprovalCallStatus(t *testing.T, tbSession, toolCallID, status string) {
	t.Helper()

	dbPath, err := SessionDBPath(tbSession)
	if err != nil {
		t.Fatalf("SessionDBPath(%q): %v", tbSession, err)
	}
	store, err := replsqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer store.Close()

	result, err := store.DB().ExecContext(context.Background(), `
UPDATE approval_tool_calls
SET status = ?, updated_at = ?
WHERE tool_call_id = ?`,
		status,
		time.Now().UTC().Format(time.RFC3339Nano),
		toolCallID,
	)
	if err != nil {
		t.Fatalf("update approval_tool_calls: %v", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected(update approval_tool_calls): %v", err)
	}
	if rows != 1 {
		t.Fatalf("update approval_tool_calls affected %d rows, want 1", rows)
	}
}

func latestToolCallIDForFact(t *testing.T, tbSession string, factType string) string {
	t.Helper()

	dbPath, err := SessionDBPath(tbSession)
	if err != nil {
		t.Fatalf("SessionDBPath(%q): %v", tbSession, err)
	}
	store, err := replsqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open(%q): %v", dbPath, err)
	}
	defer store.Close()

	row := store.DB().QueryRowContext(context.Background(), `
SELECT payload
FROM facts
WHERE fact_type = ?
ORDER BY id DESC
LIMIT 1`,
		factType,
	)
	var payload string
	if err := row.Scan(&payload); err != nil {
		t.Fatalf("load latest %s payload: %v", factType, err)
	}

	switch factType {
	case factTypeToolCallStarted:
		var fact toolCallStartedFact
		if err := json.Unmarshal([]byte(payload), &fact); err != nil {
			t.Fatalf("decode %s payload: %v", factType, err)
		}
		return fact.ToolCallID
	default:
		t.Fatalf("unsupported fact type %q", factType)
		return ""
	}
}

func prepareIssuesApprovalToolset(t *testing.T) toolset.PreparedToolset {
	t.Helper()
	dir := writeIssuesApprovalPackage(t)
	return tooltest.PrepareToolset(t, tooltest.LocalPackageDecl(dir), toolset.Config{
		ToolApprovals: map[string]bool{
			"issues.get": true,
		},
	})
}

func prepareBlockingToolset(t *testing.T, needsApproval bool, started chan struct{}, release chan struct{}) toolset.PreparedToolset {
	t.Helper()

	cfg := toolset.Config{}
	if needsApproval {
		cfg.ToolApprovals = map[string]bool{
			"blocker.wait": true,
		}
	}

	prepared, err := toolset.PrepareTools(context.Background(), []assembler.LoadedTool{{
		Name: "blocker.wait",
		PackageMeta: &tooldef.Package{
			Module:  "example.com/blocker",
			Name:    "blocker",
			Runtime: tooldef.RuntimeBuiltin,
		},
		BuiltIn: func(context.Context, map[string]any) (string, error) {
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			return "released", nil
		},
	}}, cfg)
	if err != nil {
		t.Fatalf("PrepareTools(blocker): %v", err)
	}
	return prepared
}

func writeIssuesApprovalPackage(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"tool/package.json": `{"name":"issues","version":"0.0.1"}`,
		"tool/toolbox.devpkg.json": `{
  "module": "example.com/issues",
  "name": "issues",
  "runtime": "typescript-sandbox",
  "tools": [
    { "entry_ts": "tools/get.ts" }
  ]
}`,
		"tool/tools/get.ts": `export default async function tool(id: string): Promise<{ id: string }> {
  return { id };
}
`,
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}
	return filepath.Join(dir, "tool")
}
