package audit

import (
	"fmt"
	"strings"
	"time"
)

// EventName is the canonical audit vocabulary for shared execution events.
type EventName string

const (
	EventCredentialInjected EventName = "credential_injected"
	EventCredentialRefresh  EventName = "credential_refresh"
	EventCredentialDenied   EventName = "credential_denied"
)

// Event is a concrete, redaction-safe audit record.
//
// It intentionally carries only operator-safe metadata. Secret values, header
// payloads, query values, tokens, and raw authorization material must never be
// added here.
type Event struct {
	Name    EventName `json:"name"`
	Payload Payload   `json:"payload"`
}

// Payload is implemented by concrete secret-safe audit payloads.
type Payload interface {
	validate() error
}

// CredentialInjected captures successful transport-managed credential mutation.
type CredentialInjected struct {
	Host         string `json:"host"`
	Credential   string `json:"credential"`
	InjectMethod string `json:"inject_method"`
}

// CredentialRefresh captures one OAuth2 refresh attempt outcome.
type CredentialRefresh struct {
	Credential string    `json:"credential"`
	CacheKey   string    `json:"cache_key"`
	Outcome    string    `json:"outcome"`
	Stage      string    `json:"stage"`
	Reason     string    `json:"reason,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
}

// CredentialDenied captures transport allowlist denial.
type CredentialDenied struct {
	Host         string `json:"host"`
	Reason       string `json:"reason"`
	Credential   string `json:"credential"`
	InjectMethod string `json:"inject_method"`
}

func NewCredentialInjected(host, credential, injectMethod string) (Event, error) {
	event := Event{
		Name: EventCredentialInjected,
		Payload: CredentialInjected{
			Host:         normalizeAuditField(host),
			Credential:   normalizeAuditField(credential),
			InjectMethod: normalizeAuditField(injectMethod),
		},
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func NewCredentialRefreshSuccess(credential, cacheKey string, expiresAt time.Time) (Event, error) {
	event := Event{
		Name: EventCredentialRefresh,
		Payload: CredentialRefresh{
			Credential: normalizeAuditField(credential),
			CacheKey:   normalizeAuditField(cacheKey),
			Outcome:    "success",
			Stage:      "token_refresh",
			ExpiresAt:  expiresAt.UTC(),
		},
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func NewCredentialRefreshFailure(credential, cacheKey, stage, reason string) (Event, error) {
	event := Event{
		Name: EventCredentialRefresh,
		Payload: CredentialRefresh{
			Credential: normalizeAuditField(credential),
			CacheKey:   normalizeAuditField(cacheKey),
			Outcome:    "failure",
			Stage:      normalizeAuditToken(stage),
			Reason:     normalizeAuditToken(reason),
		},
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func NewCredentialDenied(host, reason, credential, injectMethod string) (Event, error) {
	event := Event{
		Name: EventCredentialDenied,
		Payload: CredentialDenied{
			Host:         normalizeAuditField(host),
			Reason:       normalizeAuditField(reason),
			Credential:   normalizeAuditField(credential),
			InjectMethod: normalizeAuditField(injectMethod),
		},
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (e Event) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("audit event name is required")
	}
	if e.Payload == nil {
		return fmt.Errorf("audit event %q payload is required", e.Name)
	}
	if err := e.Payload.validate(); err != nil {
		return fmt.Errorf("audit event %q: %w", e.Name, err)
	}
	switch e.Name {
	case EventCredentialInjected:
		if _, ok := e.Payload.(CredentialInjected); !ok {
			return fmt.Errorf("audit event %q requires CredentialInjected payload", e.Name)
		}
	case EventCredentialRefresh:
		if _, ok := e.Payload.(CredentialRefresh); !ok {
			return fmt.Errorf("audit event %q requires CredentialRefresh payload", e.Name)
		}
	case EventCredentialDenied:
		if _, ok := e.Payload.(CredentialDenied); !ok {
			return fmt.Errorf("audit event %q requires CredentialDenied payload", e.Name)
		}
	default:
		return fmt.Errorf("unknown audit event %q", e.Name)
	}
	return nil
}

func (p CredentialInjected) validate() error {
	if p.Host == "" {
		return fmt.Errorf("host is required")
	}
	if p.Credential == "" {
		return fmt.Errorf("credential is required")
	}
	if p.InjectMethod == "" {
		return fmt.Errorf("inject method is required")
	}
	return nil
}

func (p CredentialRefresh) validate() error {
	if p.Credential == "" {
		return fmt.Errorf("credential is required")
	}
	if p.CacheKey == "" {
		return fmt.Errorf("cache key is required")
	}
	switch p.Outcome {
	case "success":
		if p.Stage == "" {
			return fmt.Errorf("stage is required")
		}
		if p.Reason != "" {
			return fmt.Errorf("reason must be empty on success")
		}
		if p.ExpiresAt.IsZero() {
			return fmt.Errorf("expires at is required on success")
		}
	case "failure":
		if p.Stage == "" {
			return fmt.Errorf("stage is required")
		}
		if p.Reason == "" {
			return fmt.Errorf("reason is required on failure")
		}
		if !p.ExpiresAt.IsZero() {
			return fmt.Errorf("expires at must be empty on failure")
		}
	default:
		return fmt.Errorf("outcome must be success or failure")
	}
	return nil
}

func (p CredentialDenied) validate() error {
	if p.Host == "" {
		return fmt.Errorf("host is required")
	}
	if p.Reason == "" {
		return fmt.Errorf("reason is required")
	}
	return nil
}

func normalizeAuditField(value string) string {
	return strings.TrimSpace(value)
}

func normalizeAuditToken(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_")
	return replacer.Replace(trimmed)
}
