package eventlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/open-mcp-ai/termcp-harness/internal/atomicfile"
)

const (
	eventStoreVersion        = 1
	defaultEventStoreBytes   = 64 << 20
	eventStoreTempFilePrefix = ".termcp-events-*.tmp"
)

type persistedDocument struct {
	Version int              `json:"version"`
	State   PersistenceState `json:"state"`
}

// FileStore persists the bounded Event Journal retention window using atomic
// replacement. It does not archive events that have left that window.
type FileStore struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

func NewFileStore(path string) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("Event Journal store path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Event Journal store path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create Event Journal store directory: %w", err)
	}
	if info, statErr := os.Stat(absolute); statErr == nil && info.IsDir() {
		return nil, errors.New("Event Journal store path is a directory")
	}
	return &FileStore{path: absolute, maxBytes: defaultEventStoreBytes}, nil
}

func (s *FileStore) Load() (*PersistenceState, error) {
	if s == nil {
		return nil, errors.New("Event Journal store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat Event Journal store: %w", err)
	}
	if info.Size() > s.maxBytes {
		return nil, fmt.Errorf("Event Journal store exceeds %d bytes", s.maxBytes)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read Event Journal store: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("Event Journal store exceeds %d bytes", s.maxBytes)
	}

	var document persistedDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode Event Journal store: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("Event Journal store contains multiple JSON values")
	}
	if document.Version != eventStoreVersion {
		return nil, fmt.Errorf("unsupported Event Journal store version %d", document.Version)
	}
	if err := validatePersistenceState(document.State); err != nil {
		return nil, fmt.Errorf("validate Event Journal store: %w", err)
	}
	state := clonePersistenceState(document.State)
	return &state, nil
}

func (s *FileStore) Save(state PersistenceState) error {
	if s == nil {
		return errors.New("Event Journal store is required")
	}
	if err := validatePersistenceState(state); err != nil {
		return fmt.Errorf("validate Event Journal state: %w", err)
	}
	document := persistedDocument{
		Version: eventStoreVersion,
		State:   clonePersistenceState(state),
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Event Journal store: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("Event Journal store exceeds %d bytes", s.maxBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeEventStateAtomically(s.path, data)
}

func clonePersistenceState(state PersistenceState) PersistenceState {
	state.ControlEvents = cloneEvents(state.ControlEvents)
	state.DiagnosticEvents = cloneEvents(state.DiagnosticEvents)
	return state
}

func writeEventStateAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, eventStoreTempFilePrefix)
	if err != nil {
		return fmt.Errorf("create Event Journal store temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set Event Journal store permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write Event Journal store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync Event Journal store: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close Event Journal store: %w", err)
	}
	if err := atomicfile.Replace(tempPath, path); err != nil {
		return fmt.Errorf("replace Event Journal store: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}
