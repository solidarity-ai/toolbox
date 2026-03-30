package audit

import (
	"fmt"
	"strings"
)

// EventName is the canonical audit vocabulary for shared execution events.
type EventName string

const (
	EventCredentialInjected EventName = "credential_injected"
	EventCredentialDenied   EventName = "credential_denied"
)

// Event is a concrete, redaction-safe audit record.
//
// It intentionally carries only operator-safe metadata. Secret values, header
// payloads, query values, tokens, and raw authorization material must never be
// added here.
type Event struct {
	Name    EventName
	Payload Payload
}

// Payload is implemented by concrete secret-safe audit payloads.
type Payload interface {
	validate() error
}

// CredentialInjected captures successful transport-managed credential mutation.
type CredentialInjected struct {
	Host         string
	Credential   string
	InjectMethod string
}

// CredentialDenied captures transport allowlist denial.
type CredentialDenied struct {
	Host         string
	Reason       string
	Credential   string
	InjectMethod string
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
