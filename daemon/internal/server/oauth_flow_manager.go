package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	connect "connectrpc.com/connect"
)

const defaultOAuthFlowTTL = 5 * time.Minute

type OAuthCallbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
	ErrorURI         string
}

type oauthFlowStatus uint8

const (
	oauthFlowPending oauthFlowStatus = iota
	oauthFlowCompleted
	oauthFlowCanceled
	oauthFlowExpired
)

type oauthFlow struct {
	flowID           string
	state            string
	authorizationURL string
	label            string
	createdAt        time.Time
	expiresAt        time.Time
	status           oauthFlowStatus
	result           OAuthCallbackResult
	done             chan struct{}
	timer            oauthFlowTimer
}

type oauthFlowTimer interface {
	Stop() bool
}

type oauthFlowManager struct {
	mu        sync.Mutex
	ttl       time.Duration
	now       func() time.Time
	afterFunc func(time.Duration, func()) oauthFlowTimer
	random    io.Reader
	byID      map[string]*oauthFlow
	byState   map[string]*oauthFlow
}

func newOAuthFlowManager(ttl time.Duration) *oauthFlowManager {
	if ttl <= 0 {
		ttl = defaultOAuthFlowTTL
	}
	return &oauthFlowManager{
		ttl:       ttl,
		now:       time.Now,
		afterFunc: func(d time.Duration, fn func()) oauthFlowTimer { return time.AfterFunc(d, fn) },
		random:    rand.Reader,
		byID:      make(map[string]*oauthFlow),
		byState:   make(map[string]*oauthFlow),
	}
}

func (m *oauthFlowManager) Begin(state, authorizationURL, label string) (*oauthFlow, error) {
	if m == nil {
		return nil, newOAuthFlowError(oauthFlowErrorUnavailable, "daemon OAuth flow manager is unavailable")
	}
	state = strings.TrimSpace(state)
	if state == "" {
		return nil, newOAuthFlowError(oauthFlowErrorInvalid, "OAuth state is required")
	}
	authorizationURL = strings.TrimSpace(authorizationURL)
	if err := validateOAuthAuthorizationURL(authorizationURL); err != nil {
		return nil, err
	}
	label = strings.TrimSpace(label)

	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.expireDueLocked(now)
	if existing := m.byState[state]; existing != nil {
		return nil, newOAuthFlowError(oauthFlowErrorAlreadyExists, "OAuth state is already registered")
	}
	flowID, err := m.newFlowIDLocked()
	if err != nil {
		return nil, err
	}
	flow := &oauthFlow{
		flowID:           flowID,
		state:            state,
		authorizationURL: authorizationURL,
		label:            label,
		createdAt:        now,
		expiresAt:        now.Add(m.ttl),
		status:           oauthFlowPending,
		done:             make(chan struct{}),
	}
	m.byID[flow.flowID] = flow
	m.byState[flow.state] = flow
	flow.timer = m.afterFunc(m.ttl, func() {
		m.expire(flowID)
	})
	return flow, nil
}

func (m *oauthFlowManager) Wait(ctx context.Context, flowID string) (OAuthCallbackResult, error) {
	if m == nil {
		return OAuthCallbackResult{}, newOAuthFlowError(oauthFlowErrorUnavailable, "daemon OAuth flow manager is unavailable")
	}
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return OAuthCallbackResult{}, newOAuthFlowError(oauthFlowErrorInvalid, "OAuth flow_id is required")
	}
	m.mu.Lock()
	flow := m.byID[flowID]
	if flow == nil {
		m.mu.Unlock()
		return OAuthCallbackResult{}, newOAuthFlowError(oauthFlowErrorNotFound, "OAuth flow was not found")
	}
	if flow.status == oauthFlowPending && !m.now().Before(flow.expiresAt) {
		m.expirePendingFlowLocked(flow)
	}
	done := flow.done
	m.mu.Unlock()

	select {
	case <-done:
		return m.waitResult(flow)
	case <-ctx.Done():
		return OAuthCallbackResult{}, ctx.Err()
	}
}

func (m *oauthFlowManager) Complete(state string, result OAuthCallbackResult) error {
	if m == nil {
		return newOAuthFlowError(oauthFlowErrorUnavailable, "daemon OAuth flow manager is unavailable")
	}
	state = strings.TrimSpace(state)
	if state == "" {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth state is required")
	}
	if result.State != "" && result.State != state {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth callback state does not match the registered state")
	}
	if strings.TrimSpace(result.Code) == "" && strings.TrimSpace(result.Error) == "" {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth callback must include code or error")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireDueLocked(m.now())
	flow := m.byState[state]
	if flow == nil {
		return newOAuthFlowError(oauthFlowErrorNotFound, "OAuth flow was not found")
	}
	if flow.status != oauthFlowPending {
		return newOAuthFlowError(oauthFlowErrorAlreadyCompleted, "OAuth flow is no longer pending")
	}
	result.State = flow.state
	flow.result = result
	flow.status = oauthFlowCompleted
	delete(m.byState, flow.state)
	// Keep the completed result addressable by flow_id until Wait consumes it.
	// A browser callback can arrive before the CLI has entered Wait. The
	// original expiry timer deliberately remains as bounded cleanup for a CLI
	// crash/no-waiter case; Wait stops it as soon as the result is consumed.
	close(flow.done)
	return nil
}

func (m *oauthFlowManager) Cancel(flowID string) error {
	if m == nil {
		return newOAuthFlowError(oauthFlowErrorUnavailable, "daemon OAuth flow manager is unavailable")
	}
	flowID = strings.TrimSpace(flowID)
	if flowID == "" {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth flow_id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	flow := m.byID[flowID]
	if flow == nil {
		return nil
	}
	if flow.status != oauthFlowPending {
		return nil
	}
	flow.status = oauthFlowCanceled
	delete(m.byID, flow.flowID)
	delete(m.byState, flow.state)
	if flow.timer != nil {
		flow.timer.Stop()
	}
	close(flow.done)
	return nil
}

func (m *oauthFlowManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, flow := range m.byID {
		if flow == nil {
			continue
		}
		if flow.timer != nil {
			flow.timer.Stop()
		}
		if flow.status == oauthFlowPending {
			flow.status = oauthFlowCanceled
			close(flow.done)
		}
	}
	m.byID = make(map[string]*oauthFlow)
	m.byState = make(map[string]*oauthFlow)
}

func (m *oauthFlowManager) Pending() []OAuthFlowSnapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.expireDueLocked(m.now())
	snapshots := make([]OAuthFlowSnapshot, 0, len(m.byID))
	for _, flow := range m.byID {
		if flow == nil || flow.status != oauthFlowPending {
			continue
		}
		snapshots = append(snapshots, OAuthFlowSnapshot{
			FlowID:           flow.flowID,
			State:            flow.state,
			AuthorizationURL: flow.authorizationURL,
			Label:            flow.label,
			CreatedAt:        flow.createdAt,
			ExpiresAt:        flow.expiresAt,
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if !snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
		}
		return snapshots[i].FlowID < snapshots[j].FlowID
	})
	return snapshots
}

func (m *oauthFlowManager) expireDueLocked(now time.Time) {
	for _, flow := range m.byID {
		if flow == nil || flow.status != oauthFlowPending || now.Before(flow.expiresAt) {
			continue
		}
		m.expirePendingFlowLocked(flow)
	}
}

func (m *oauthFlowManager) expire(flowID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	flow := m.byID[flowID]
	if flow == nil {
		return
	}
	switch flow.status {
	case oauthFlowPending:
		m.expirePendingFlowLocked(flow)
	case oauthFlowCompleted, oauthFlowCanceled, oauthFlowExpired:
		delete(m.byID, flow.flowID)
		delete(m.byState, flow.state)
	}
}

func (m *oauthFlowManager) expirePendingFlowLocked(flow *oauthFlow) {
	flow.status = oauthFlowExpired
	delete(m.byID, flow.flowID)
	delete(m.byState, flow.state)
	if flow.timer != nil {
		flow.timer.Stop()
	}
	close(flow.done)
}

func (m *oauthFlowManager) waitResult(flow *oauthFlow) (OAuthCallbackResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch flow.status {
	case oauthFlowCompleted:
		result := flow.result
		m.removeFlowLocked(flow)
		return result, nil
	case oauthFlowCanceled:
		m.removeFlowLocked(flow)
		return OAuthCallbackResult{}, newOAuthFlowError(oauthFlowErrorCanceled, "OAuth flow was canceled")
	case oauthFlowExpired:
		m.removeFlowLocked(flow)
		return OAuthCallbackResult{}, newOAuthFlowError(oauthFlowErrorExpired, "OAuth flow expired")
	default:
		return OAuthCallbackResult{}, newOAuthFlowError(oauthFlowErrorUnavailable, "OAuth flow ended unexpectedly")
	}
}

func (m *oauthFlowManager) removeFlowLocked(flow *oauthFlow) {
	if current := m.byID[flow.flowID]; current == flow {
		delete(m.byID, flow.flowID)
	}
	if current := m.byState[flow.state]; current == flow {
		delete(m.byState, flow.state)
	}
	if flow.timer != nil {
		flow.timer.Stop()
	}
}

func (m *oauthFlowManager) newFlowIDLocked() (string, error) {
	for range 16 {
		raw := make([]byte, 18)
		if _, err := io.ReadFull(m.random, raw); err != nil {
			return "", newOAuthFlowError(oauthFlowErrorUnavailable, "could not generate OAuth flow_id")
		}
		flowID := base64.RawURLEncoding.EncodeToString(raw)
		if _, exists := m.byID[flowID]; !exists {
			return flowID, nil
		}
	}
	return "", newOAuthFlowError(oauthFlowErrorUnavailable, "could not generate a unique OAuth flow_id")
}

func validateOAuthAuthorizationURL(raw string) error {
	if raw == "" {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth authorization_url is required")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth authorization_url must be an absolute http:// or https:// URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return newOAuthFlowError(oauthFlowErrorInvalid, "OAuth authorization_url must start with http:// or https://")
	}
	return nil
}

type oauthFlowErrorKind uint8

const (
	oauthFlowErrorInvalid oauthFlowErrorKind = iota
	oauthFlowErrorNotFound
	oauthFlowErrorAlreadyExists
	oauthFlowErrorAlreadyCompleted
	oauthFlowErrorCanceled
	oauthFlowErrorExpired
	oauthFlowErrorUnavailable
)

type oauthFlowError struct {
	kind    oauthFlowErrorKind
	message string
}

func newOAuthFlowError(kind oauthFlowErrorKind, message string) *oauthFlowError {
	return &oauthFlowError{kind: kind, message: message}
}

func (e *oauthFlowError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func oauthFlowConnectError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return connect.NewError(connect.CodeCanceled, err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.NewError(connect.CodeDeadlineExceeded, err)
	}
	var flowErr *oauthFlowError
	if errors.As(err, &flowErr) {
		switch flowErr.kind {
		case oauthFlowErrorInvalid:
			return connect.NewError(connect.CodeInvalidArgument, err)
		case oauthFlowErrorNotFound:
			return connect.NewError(connect.CodeNotFound, err)
		case oauthFlowErrorAlreadyExists, oauthFlowErrorAlreadyCompleted:
			return connect.NewError(connect.CodeAlreadyExists, err)
		case oauthFlowErrorCanceled:
			return connect.NewError(connect.CodeCanceled, err)
		case oauthFlowErrorExpired:
			return connect.NewError(connect.CodeDeadlineExceeded, err)
		case oauthFlowErrorUnavailable:
			return connect.NewError(connect.CodeUnavailable, err)
		}
	}
	return connect.NewError(connect.CodeInternal, fmt.Errorf("daemon OAuth flow error: %w", err))
}
