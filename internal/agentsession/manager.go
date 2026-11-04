package agentsession

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

var (
	ErrSessionNotFound            = errors.New("Agent Session not found")
	ErrSessionExists              = errors.New("Agent Session already exists")
	ErrApprovalSessionAmbiguous   = errors.New("approval call matches multiple Agent Sessions")
	ErrToolResultSessionNotFound  = errors.New("tool result has no waiting Agent Session")
	ErrToolResultSessionAmbiguous = errors.New("tool result matches multiple Agent Sessions")
)

type Manager struct {
	mu          sync.RWMutex
	persistMu   sync.Mutex
	byID        map[string]*Session
	journal     *eventlog.Journal
	persistence Persistence
}

func NewManager(journal *eventlog.Journal) (*Manager, error) {
	return NewManagerWithPersistence(journal, nil)
}

func (m *Manager) Create(id, coreSessionID string) (*Session, error) {
	if m == nil {
		return nil, errors.New("Agent Session manager is required")
	}
	session, err := New(id, coreSessionID, m.journal)
	if err != nil {
		return nil, err
	}
	id = session.Snapshot().ID
	m.mu.Lock()
	if _, exists := m.byID[id]; exists {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrSessionExists, id)
	}
	m.byID[id] = session
	m.mu.Unlock()
	session.setPersistence(m.persist)
	if err := m.persist(); err != nil {
		m.mu.Lock()
		delete(m.byID, id)
		m.mu.Unlock()
		return nil, err
	}
	return session, nil
}

func (m *Manager) Get(id string) (*Session, error) {
	if m == nil {
		return nil, errors.New("Agent Session manager is required")
	}
	id = strings.TrimSpace(id)
	m.mu.RLock()
	session := m.byID[id]
	m.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	return session, nil
}

func (m *Manager) List() []Snapshot {
	if m == nil {
		return []Snapshot{}
	}
	m.mu.RLock()
	ids := make([]string, 0, len(m.byID))
	for id := range m.byID {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	sort.Strings(ids)

	result := make([]Snapshot, 0, len(ids))
	for _, id := range ids {
		session, err := m.Get(id)
		if err != nil {
			continue
		}
		result = append(result, session.Snapshot())
	}
	return result
}

func (m *Manager) Transcript(id string) (Transcript, error) {
	session, err := m.Get(id)
	if err != nil {
		return Transcript{}, err
	}
	return session.Transcript(), nil
}

func (m *Manager) Run(
	ctx context.Context,
	id string,
	invoker Invoker,
	steps []Step,
) (Result, error) {
	session, err := m.Get(id)
	if err != nil {
		return Result{}, err
	}
	return session.Run(ctx, invoker, steps)
}

func (m *Manager) Cancel(_ context.Context, id string, expectedRevision uint64) (Snapshot, error) {
	session, err := m.Get(id)
	if err != nil {
		return Snapshot{}, err
	}
	return session.Cancel(expectedRevision)
}

func (m *Manager) RunModel(
	ctx context.Context,
	id string,
	adapter model.Adapter,
	invoker Invoker,
	tools []ModelTool,
	input string,
	options ModelRunOptions,
) (ModelResult, error) {
	session, err := m.Get(id)
	if err != nil {
		return ModelResult{}, err
	}
	return session.RunModel(ctx, adapter, invoker, tools, input, options)
}

func (m *Manager) ObserveApproval(callID string, observed approval.Snapshot) error {
	if m == nil {
		return errors.New("Agent Session manager is required")
	}
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.byID))
	for _, session := range m.byID {
		sessions = append(sessions, session)
	}
	m.mu.RUnlock()

	matches := make([]*Session, 0, 1)
	for _, session := range sessions {
		snapshot := session.Snapshot()
		if snapshot.CurrentCallID == callID &&
			(snapshot.State == StateWaitingApproval || snapshot.State == StateWaitingExecution) {
			matches = append(matches, session)
		}
	}
	if len(matches) == 0 {
		return nil
	}
	if len(matches) > 1 {
		return fmt.Errorf("%w: call %q", ErrApprovalSessionAmbiguous, callID)
	}
	_, err := matches[0].ObserveApproval(callID, observed)
	if err == nil {
		matches[0].startAutomaticResume()
	}
	return err
}

func (m *Manager) ObserveToolResult(
	ctx context.Context,
	invoker Invoker,
	observed toolpipeline.Outcome,
) (Result, error) {
	if m == nil {
		return Result{}, errors.New("Agent Session manager is required")
	}
	callID := strings.TrimSpace(observed.Call.ID)
	if callID == "" {
		return Result{}, errors.New("tool result call ID is required")
	}

	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.byID))
	for _, session := range m.byID {
		sessions = append(sessions, session)
	}
	m.mu.RUnlock()

	matches := make([]*Session, 0, 1)
	for _, session := range sessions {
		snapshot := session.Snapshot()
		if snapshot.State == StateWaitingExecution && snapshot.CurrentCallID == callID {
			matches = append(matches, session)
		}
	}
	if len(matches) == 0 {
		for _, session := range sessions {
			if session.matchesToolResult(callID, observed.Call.Status) {
				matches = append(matches, session)
			}
		}
	}
	switch len(matches) {
	case 0:
		return Result{}, fmt.Errorf("%w: call %q", ErrToolResultSessionNotFound, callID)
	case 1:
		return matches[0].ObserveToolResult(ctx, invoker, observed)
	default:
		return Result{}, fmt.Errorf("%w: call %q", ErrToolResultSessionAmbiguous, callID)
	}
}
