package server

import (
	"fmt"
	"sync/atomic"
	"time"
)

type SecretEpoch struct {
	startedAtUnixNano int64
	counter           atomic.Uint64
}

func NewSecretEpoch() *SecretEpoch {
	return &SecretEpoch{startedAtUnixNano: time.Now().UTC().UnixNano()}
}

func (e *SecretEpoch) Current() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", e.startedAtUnixNano, e.counter.Load())
}

func (e *SecretEpoch) Increment() string {
	if e == nil {
		return ""
	}
	counter := e.counter.Add(1)
	return fmt.Sprintf("%d-%d", e.startedAtUnixNano, counter)
}
