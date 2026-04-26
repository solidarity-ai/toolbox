package codemodesession

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/solidarity-ai/toolbox/invoke"
	"github.com/solidarity-ai/toolbox/toolset"
	"golang.org/x/sync/singleflight"
)

type ManagerMode string

const (
	ManagerModeLocked   ManagerMode = "locked"
	ManagerModeUnlocked ManagerMode = "unlocked"
)

// approvalOwnerIndex maps a pending approval tool_call_id to the live session
// that currently owns it in this process.
type approvalOwnerIndex map[string]*Session

type Manager struct {
	mu sync.Mutex

	mode           ManagerMode
	currentDir     string
	prepared       toolset.PreparedToolset
	executor       *invoke.Executor
	boundTBSession string
	sessions       map[string]*Session
	approvalOwners approvalOwnerIndex
	openGroup      singleflight.Group
}

func NewUnlockedManager(currentDir string, cfgs ...SessionConfig) *Manager {
	cfg := firstConfig(cfgs)
	return &Manager{
		mode:           ManagerModeUnlocked,
		currentDir:     currentDir,
		prepared:       cfg.PreparedTools,
		executor:       invoke.NewExecutor(cfg.PreparedTools),
		sessions:       make(map[string]*Session),
		approvalOwners: make(approvalOwnerIndex),
	}
}

func OpenLockedManager(ctx context.Context, tbSession, currentDir string, cfgs ...SessionConfig) (*Manager, error) {
	cfg := firstConfig(cfgs)
	manager := &Manager{
		mode:           ManagerModeLocked,
		currentDir:     currentDir,
		prepared:       cfg.PreparedTools,
		executor:       invoke.NewExecutor(cfg.PreparedTools),
		boundTBSession: strings.TrimSpace(tbSession),
		sessions:       make(map[string]*Session),
		approvalOwners: make(approvalOwnerIndex),
	}
	if _, err := manager.openExisting(ctx, tbSession); err != nil {
		_ = manager.executor.Close()
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Mode() ManagerMode {
	if m == nil {
		return ManagerModeUnlocked
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode
}

func (m *Manager) Locked() bool {
	return m.Mode() == ManagerModeLocked
}

func (m *Manager) BoundTBSession() string {
	if m == nil {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boundTBSession
}

func (m *Manager) Instructions() string {
	if m == nil {
		return (&Session{}).Instructions()
	}
	meta := &Session{}
	prepared := m.preparedSnapshot()
	meta.SetPreparedTools(prepared)
	awaitAvailable := prepared.HasApprovalTools()
	if m.Locked() {
		return meta.InstructionsForSurface(ToolSurfaceModeLocked, awaitAvailable)
	}
	return meta.InstructionsForSurface(ToolSurfaceModeUnlocked, awaitAvailable)
}

func (m *Manager) SetPreparedTools(prepared toolset.PreparedToolset) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.prepared = prepared
	executor := m.executor
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()
	if executor != nil {
		executor.SetPrepared(prepared)
	}
	for _, session := range sessions {
		session.SetPreparedTools(prepared)
	}
}

func (m *Manager) Submit(ctx context.Context, tbSession, code string) (string, error) {
	session, err := m.sessionForRequest(ctx, tbSession)
	if err != nil {
		return "", err
	}
	out := session.Submit(ctx, code)
	if err := m.refreshApprovalOwners(context.Background(), session); err != nil {
		if errors.Is(err, ErrSessionLeaseLost) {
			m.dropSession(session, true)
		}
		return "", err
	}
	return out, nil
}

func (m *Manager) AwaitNextApproval(ctx context.Context, tbSession string) (ApprovalAwaitResult, error) {
	session, err := m.sessionForRequest(ctx, tbSession)
	if err != nil {
		return ApprovalAwaitResult{}, err
	}
	result, err := session.AwaitNextApproval(ctx)
	if err != nil && errors.Is(err, ErrSessionLeaseLost) {
		m.dropSession(session, true)
	}
	return result, err
}

func (m *Manager) EnsureSession(ctx context.Context, tbSession string) error {
	session, err := m.sessionForRequest(ctx, tbSession)
	if err != nil {
		return err
	}
	if err := m.refreshApprovalOwners(ctx, session); err != nil {
		if errors.Is(err, ErrSessionLeaseLost) {
			m.dropSession(session, true)
		}
		return err
	}
	return nil
}

func (m *Manager) PendingApprovals(ctx context.Context) ([]PendingApproval, error) {
	if m == nil {
		return nil, nil
	}
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.mu.Unlock()

	var out []PendingApproval
	for _, session := range sessions {
		approvals, err := session.PendingApprovals(ctx)
		if err != nil {
			if errors.Is(err, ErrSessionLeaseLost) {
				m.dropSession(session, true)
				continue
			}
			return nil, err
		}
		out = append(out, approvals...)
		if err := m.refreshApprovalOwners(ctx, session); err != nil {
			if errors.Is(err, ErrSessionLeaseLost) {
				m.dropSession(session, true)
				continue
			}
			return nil, err
		}
	}
	return out, nil
}

func (m *Manager) ApplyApprovals(ctx context.Context, decisions []ApprovalDecision) error {
	if m == nil || len(decisions) == 0 {
		return nil
	}
	grouped := make(map[*Session][]ApprovalDecision)
	for _, decision := range decisions {
		session, err := m.sessionForToolCall(ctx, decision.ToolCallID)
		if err != nil {
			return err
		}
		grouped[session] = append(grouped[session], decision)
	}
	for session, batch := range grouped {
		if err := session.ApplyApprovals(ctx, batch); err != nil {
			if errors.Is(err, ErrSessionLeaseLost) {
				m.dropSession(session, true)
			}
			return err
		}
		if err := m.refreshApprovalOwners(context.Background(), session); err != nil {
			if errors.Is(err, ErrSessionLeaseLost) {
				m.dropSession(session, true)
			}
			return err
		}
	}
	return nil
}

func (m *Manager) CreateFreshSession(ctx context.Context, intent string) (string, error) {
	if m == nil {
		return "", errors.New("session manager is unavailable")
	}
	session, err := CreateFreshWithIntent(ctx, m.currentDir, intent, SessionConfig{PreparedTools: m.preparedSnapshot(), Executor: m.executor})
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	oldBound := m.boundTBSession
	m.sessions[session.TBSession()] = session
	if m.mode == ManagerModeLocked {
		m.boundTBSession = session.TBSession()
	}
	m.mu.Unlock()

	if m.mode == ManagerModeLocked && oldBound != "" && oldBound != session.TBSession() {
		if old := m.lookupSession(oldBound); old != nil {
			m.dropSession(old, true)
		}
	}
	if err := m.refreshApprovalOwners(context.Background(), session); err != nil {
		if errors.Is(err, ErrSessionLeaseLost) {
			m.dropSession(session, true)
		}
		return "", err
	}
	return session.TBSession(), nil
}

func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*Session)
	m.approvalOwners = make(approvalOwnerIndex)
	m.boundTBSession = ""
	m.mu.Unlock()

	var closeErr error
	for _, session := range sessions {
		closeErr = errors.Join(closeErr, session.Close())
	}
	if m.executor != nil {
		closeErr = errors.Join(closeErr, m.executor.Close())
	}
	return closeErr
}

func (m *Manager) sessionForRequest(ctx context.Context, tbSession string) (*Session, error) {
	if m == nil {
		return nil, errors.New("session manager is unavailable")
	}
	resolved, err := m.resolveTBSession(tbSession)
	if err != nil {
		return nil, err
	}
	return m.openExisting(ctx, resolved)
}

func (m *Manager) resolveTBSession(tbSession string) (string, error) {
	tbSession = strings.TrimSpace(tbSession)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mode == ManagerModeLocked {
		if tbSession == "" {
			if m.boundTBSession == "" {
				return "", ErrTBSessionRequired
			}
			return m.boundTBSession, nil
		}
		if tbSession != m.boundTBSession {
			return "", fmt.Errorf("%w: got %q want %q", ErrTBSessionMismatch, tbSession, m.boundTBSession)
		}
		return tbSession, nil
	}
	if tbSession == "" {
		return "", ErrTBSessionRequired
	}
	return tbSession, nil
}

func (m *Manager) openExisting(ctx context.Context, tbSession string) (*Session, error) {
	if err := ValidateTBSession(tbSession); err != nil {
		return nil, err
	}
	if existing := m.lookupSession(tbSession); existing != nil {
		if existing.terminalError() == nil {
			return existing, nil
		}
		m.dropSession(existing, true)
	}

	resCh := m.openGroup.DoChan(tbSession, func() (any, error) {
		if existing := m.lookupSession(tbSession); existing != nil {
			if existing.terminalError() == nil {
				return existing, nil
			}
			m.dropSession(existing, true)
		}
		session, err := OpenExisting(ctx, tbSession, m.currentDir, SessionConfig{PreparedTools: m.preparedSnapshot(), Executor: m.executor})
		if err != nil {
			return nil, err
		}
		if err := m.refreshApprovalOwners(context.Background(), session); err != nil {
			if errors.Is(err, ErrSessionLeaseLost) {
				m.dropSession(session, true)
			}
			return nil, err
		}
		m.mu.Lock()
		m.sessions[tbSession] = session
		m.mu.Unlock()
		return session, nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resCh:
		if result.Err != nil {
			return nil, result.Err
		}
		session, ok := result.Val.(*Session)
		if !ok || session == nil {
			return nil, errors.New("session manager open returned no session")
		}
		return session, nil
	}
}

func (m *Manager) sessionForToolCall(ctx context.Context, toolCallID string) (*Session, error) {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return nil, fmt.Errorf("approval decision missing tool call id")
	}
	m.mu.Lock()
	session := m.approvalOwners[toolCallID]
	m.mu.Unlock()
	if session != nil && session.terminalError() == nil {
		return session, nil
	}

	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, live := range m.sessions {
		sessions = append(sessions, live)
	}
	m.mu.Unlock()
	for _, live := range sessions {
		if err := m.refreshApprovalOwners(ctx, live); err != nil {
			if errors.Is(err, ErrSessionLeaseLost) {
				m.dropSession(live, true)
				continue
			}
			return nil, err
		}
		m.mu.Lock()
		session = m.approvalOwners[toolCallID]
		m.mu.Unlock()
		if session != nil {
			return session, nil
		}
	}
	return nil, fmt.Errorf("unknown approval tool call %q", toolCallID)
}

func (m *Manager) refreshApprovalOwners(ctx context.Context, session *Session) error {
	if m == nil || session == nil {
		return nil
	}
	approvals, err := session.PendingApprovals(ctx)
	if err != nil {
		return err
	}
	active := make(map[string]struct{}, len(approvals))
	for _, approval := range approvals {
		active[approval.ToolCallID] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for toolCallID, owner := range m.approvalOwners {
		if owner != session {
			continue
		}
		if _, ok := active[toolCallID]; !ok {
			delete(m.approvalOwners, toolCallID)
		}
	}
	for _, approval := range approvals {
		m.approvalOwners[approval.ToolCallID] = session
	}
	return nil
}

func (m *Manager) lookupSession(tbSession string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[tbSession]
}

func (m *Manager) dropSession(session *Session, closeSession bool) {
	if m == nil || session == nil {
		return
	}
	tbSession := session.TBSession()
	m.mu.Lock()
	delete(m.sessions, tbSession)
	for toolCallID, owner := range m.approvalOwners {
		if owner == session {
			delete(m.approvalOwners, toolCallID)
		}
	}
	m.mu.Unlock()
	if closeSession {
		_ = session.Close()
	}
}

func (m *Manager) preparedSnapshot() toolset.PreparedToolset {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prepared
}
