package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

// State is the local lifecycle state of a Harness Runtime instance. It does
// not describe the lifecycle of Core, an MCP server, or a tool invocation.
type State string

const (
	StateNew      State = "new"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
)

var ErrInvalidTransition = errors.New("invalid runtime state transition")

// Snapshot is a consistent point-in-time view of one Runtime instance.
type Snapshot struct {
	InstanceID string     `json:"instance_id"`
	Version    string     `json:"version"`
	State      State      `json:"state"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	StoppedAt  *time.Time `json:"stopped_at,omitempty"`
}

// Runtime owns only Harness-local lifecycle state. Core and MCP states remain
// separate and authoritative at their own boundaries.
type Runtime struct {
	mu         sync.RWMutex
	instanceID string
	version    string
	state      State
	startedAt  *time.Time
	stoppedAt  *time.Time
	now        func() time.Time
	events     *eventlog.Journal
}

type Option func(*Runtime)

func WithEventLog(journal *eventlog.Journal) Option {
	return func(r *Runtime) {
		r.events = journal
	}
}

func New(version string, options ...Option) (*Runtime, error) {
	if version == "" {
		version = "dev"
	}

	instanceID, err := newInstanceID()
	if err != nil {
		return nil, fmt.Errorf("create runtime instance ID: %w", err)
	}

	runtime := &Runtime{
		instanceID: instanceID,
		version:    version,
		state:      StateNew,
		now:        time.Now,
	}
	for _, option := range options {
		option(runtime)
	}
	return runtime, nil
}

func (r *Runtime) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateNew {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, r.state, StateRunning)
	}
	if err := r.record(eventlog.TypeRuntimeStarted); err != nil {
		return err
	}

	now := r.now().UTC()
	r.state = StateRunning
	r.startedAt = &now
	return nil
}

// MarkStopping makes the draining state visible before resources are closed.
func (r *Runtime) MarkStopping() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateRunning {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, r.state, StateStopping)
	}
	if err := r.record(eventlog.TypeRuntimeStopping); err != nil {
		return err
	}

	r.state = StateStopping
	return nil
}

func (r *Runtime) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state != StateStopping {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, r.state, StateStopped)
	}
	if err := r.record(eventlog.TypeRuntimeStopped); err != nil {
		return err
	}

	now := r.now().UTC()
	r.state = StateStopped
	r.stoppedAt = &now
	return nil
}

func (r *Runtime) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var startedAt *time.Time
	if r.startedAt != nil {
		value := *r.startedAt
		startedAt = &value
	}

	var stoppedAt *time.Time
	if r.stoppedAt != nil {
		value := *r.stoppedAt
		stoppedAt = &value
	}

	return Snapshot{
		InstanceID: r.instanceID,
		Version:    r.version,
		State:      r.state,
		StartedAt:  startedAt,
		StoppedAt:  stoppedAt,
	}
}

func newInstanceID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (r *Runtime) record(eventType eventlog.Type) error {
	if r.events == nil {
		return nil
	}
	_, err := r.events.Append(eventlog.Entry{
		Type:      eventType,
		Class:     eventlog.ClassControl,
		SubjectID: r.instanceID,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", eventType, err)
	}
	return nil
}
