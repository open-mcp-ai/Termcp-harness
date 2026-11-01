package agentsession

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/open-mcp-ai/termcp-harness/internal/atomicfile"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

const (
	persistenceVersion        = 2
	minPersistenceVersion     = 1
	defaultPersistenceBytes   = 64 << 20
	maxPersistedSessions      = 1024
	persistenceRecoveryReason = "runtime_restart"
)

type PersistedSession struct {
	Snapshot            Snapshot             `json:"snapshot"`
	Messages            []model.Message      `json:"messages,omitempty"`
	Output              string               `json:"output,omitempty"`
	ModelLoopCheckpoint *modelLoopCheckpoint `json:"model_loop_checkpoint,omitempty"`
}

type Persistence interface {
	Load() ([]PersistedSession, error)
	Save([]PersistedSession) error
}

type persistedDocument struct {
	Version  int                `json:"version"`
	Sessions []PersistedSession `json:"sessions"`
}

// FileStore persists bounded Agent Session snapshots and model transcripts.
// It deliberately does not persist the event journal or remote approval state.
type FileStore struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

func NewFileStore(path string) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("Agent Session store path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Agent Session store path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create Agent Session store directory: %w", err)
	}
	if info, statErr := os.Stat(absolute); statErr == nil && info.IsDir() {
		return nil, errors.New("Agent Session store path is a directory")
	}
	return &FileStore{path: absolute, maxBytes: defaultPersistenceBytes}, nil
}

func (s *FileStore) Load() ([]PersistedSession, error) {
	if s == nil {
		return nil, errors.New("Agent Session store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat Agent Session store: %w", err)
	}
	if info.Size() > s.maxBytes {
		return nil, fmt.Errorf("Agent Session store exceeds %d bytes", s.maxBytes)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read Agent Session store: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("Agent Session store exceeds %d bytes", s.maxBytes)
	}
	var document persistedDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode Agent Session store: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("Agent Session store contains multiple JSON values")
	}
	if document.Version < minPersistenceVersion || document.Version > persistenceVersion {
		return nil, fmt.Errorf("unsupported Agent Session store version %d", document.Version)
	}
	if len(document.Sessions) > maxPersistedSessions {
		return nil, fmt.Errorf("Agent Session store contains %d sessions, limit is %d",
			len(document.Sessions), maxPersistedSessions)
	}
	return document.Sessions, nil
}

func (s *FileStore) Save(sessions []PersistedSession) error {
	if s == nil {
		return errors.New("Agent Session store is required")
	}
	if len(sessions) > maxPersistedSessions {
		return fmt.Errorf("Agent Session count %d exceeds persistence limit %d",
			len(sessions), maxPersistedSessions)
	}
	document := persistedDocument{Version: persistenceVersion, Sessions: sessions}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Agent Session store: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("Agent Session store exceeds %d bytes", s.maxBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeFileAtomically(s.path, data)
}

func writeFileAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".termcp-sessions-*.tmp")
	if err != nil {
		return fmt.Errorf("create Agent Session store temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set Agent Session store permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write Agent Session store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync Agent Session store: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close Agent Session store: %w", err)
	}
	if err := atomicfile.Replace(tempPath, path); err != nil {
		return fmt.Errorf("replace Agent Session store: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func NewManagerWithPersistence(journal *eventlog.Journal, persistence Persistence) (*Manager, error) {
	if journal == nil {
		return nil, errors.New("event log is required for Agent Session manager")
	}
	manager := &Manager{
		byID:        make(map[string]*Session),
		journal:     journal,
		persistence: persistence,
	}
	if persistence == nil {
		return manager, nil
	}
	persisted, err := persistence.Load()
	if err != nil {
		return nil, err
	}
	recovered := make([]*Session, 0)
	for _, item := range persisted {
		session, err := restoreSession(journal, item)
		if err != nil {
			return nil, err
		}
		if _, exists := manager.byID[session.Snapshot().ID]; exists {
			return nil, fmt.Errorf("duplicate persisted Agent Session %q", session.Snapshot().ID)
		}
		if session.Snapshot().Recovered {
			recovered = append(recovered, session)
		}
		manager.byID[session.Snapshot().ID] = session
		session.setPersistence(manager.persist)
	}
	for _, session := range recovered {
		data, _ := json.Marshal(map[string]any{
			"reason":           persistenceRecoveryReason,
			"revision":         session.Snapshot().Revision,
			"state":            StateUnknown,
			"checkpoint_stage": session.Snapshot().ModelLoopRecovery,
		})
		_, err := journal.Append(eventlog.Entry{
			Type:          eventlog.TypeAgentSessionUnknown,
			Class:         eventlog.ClassControl,
			SubjectID:     session.Snapshot().ID,
			CorrelationID: session.Snapshot().CurrentCallID,
			Data:          data,
		})
		if err != nil {
			return nil, fmt.Errorf("record recovered Agent Session: %w", err)
		}
	}
	if len(persisted) > 0 {
		if err := manager.persist(); err != nil {
			return nil, err
		}
	}
	return manager, nil
}

func restoreSession(journal *eventlog.Journal, item PersistedSession) (*Session, error) {
	if item.Snapshot.ID == "" {
		return nil, errors.New("persisted Agent Session ID is required")
	}
	session, err := New(item.Snapshot.ID, item.Snapshot.CoreSessionID, journal)
	if err != nil {
		return nil, err
	}
	if item.Snapshot.State == "" {
		return nil, fmt.Errorf("persisted Agent Session %q has no state", item.Snapshot.ID)
	}
	session.snapshot = cloneSnapshot(item.Snapshot)
	session.transcript = cloneModelMessages(item.Messages)
	session.finalOutput = item.Output
	if err := validateModelLoopCheckpoint(item.ModelLoopCheckpoint); err != nil {
		return nil, fmt.Errorf("restore Agent Session %q checkpoint: %w", item.Snapshot.ID, err)
	}
	session.modelLoopCheckpoint = cloneModelLoopCheckpoint(item.ModelLoopCheckpoint)
	session.snapshot.ModelLoopRecovery = session.modelLoopCheckpoint.summary()
	session.snapshot.TranscriptMessages = len(session.transcript)
	session.snapshot.OutputBytes = len(session.finalOutput)

	if item.Snapshot.State != StateNew && !item.Snapshot.State.IsTerminal() {
		now := session.now().UTC()
		session.snapshot.State = StateUnknown
		session.snapshot.Revision++
		session.snapshot.FinishedAt = &now
		session.snapshot.CurrentToolStatus = toolcall.StatusUnknown
		session.snapshot.Recovered = true
		session.snapshot.RecoveryReason = persistenceRecoveryReason
		session.snapshot.CurrentCompletion = ""
	}
	return session, nil
}

func (m *Manager) persist() error {
	if m == nil || m.persistence == nil {
		return nil
	}
	// Serialize the full snapshot-and-save transaction. FileStore protects the
	// file itself, but without this lock an older cross-Session snapshot could
	// finish writing after a newer snapshot and roll the persisted state back.
	m.persistMu.Lock()
	defer m.persistMu.Unlock()
	m.mu.RLock()
	sessions := make([]*Session, 0, len(m.byID))
	for _, session := range m.byID {
		sessions = append(sessions, session)
	}
	m.mu.RUnlock()
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Snapshot().ID < sessions[j].Snapshot().ID
	})
	items := make([]PersistedSession, 0, len(sessions))
	for _, session := range sessions {
		transcript := session.Transcript()
		items = append(items, PersistedSession{
			Snapshot:            transcript.Session,
			Messages:            transcript.Messages,
			Output:              transcript.Output,
			ModelLoopCheckpoint: session.modelLoopCheckpointCopy(),
		})
	}
	return m.persistence.Save(items)
}
