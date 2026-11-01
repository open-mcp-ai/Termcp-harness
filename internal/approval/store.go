package approval

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
)

const (
	approvalStoreVersion        = 1
	defaultApprovalStoreBytes   = 32 << 20
	maxPersistedApprovals       = 4096
	approvalRecoveryReason      = "runtime_restart"
	approvalStoreTempFilePrefix = ".termcp-approvals-*.tmp"
)

type PersistedApproval struct {
	Request   Snapshot  `json:"request"`
	Reference Reference `json:"reference"`
}

type Persistence interface {
	Load() ([]PersistedApproval, error)
	Save([]PersistedApproval) error
}

type persistedApprovalDocument struct {
	Version   int                 `json:"version"`
	Approvals []PersistedApproval `json:"approvals"`
}

// FileStore persists approval history and last-known execution metadata. It
// does not persist authority credentials or the authority's approval queue.
type FileStore struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

func NewFileStore(path string) (*FileStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("Approval history store path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve Approval history store path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("create Approval history store directory: %w", err)
	}
	if info, statErr := os.Stat(absolute); statErr == nil && info.IsDir() {
		return nil, errors.New("Approval history store path is a directory")
	}
	return &FileStore{path: absolute, maxBytes: defaultApprovalStoreBytes}, nil
}

func (s *FileStore) Load() ([]PersistedApproval, error) {
	if s == nil {
		return nil, errors.New("Approval history store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat Approval history store: %w", err)
	}
	if info.Size() > s.maxBytes {
		return nil, fmt.Errorf("Approval history store exceeds %d bytes", s.maxBytes)
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("read Approval history store: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return nil, fmt.Errorf("Approval history store exceeds %d bytes", s.maxBytes)
	}

	var document persistedApprovalDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode Approval history store: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("Approval history store contains multiple JSON values")
	}
	if document.Version != approvalStoreVersion {
		return nil, fmt.Errorf("unsupported Approval history store version %d", document.Version)
	}
	if err := validatePersistedApprovals(document.Approvals); err != nil {
		return nil, fmt.Errorf("validate Approval history store: %w", err)
	}
	return clonePersistedApprovals(document.Approvals), nil
}

func (s *FileStore) Save(approvals []PersistedApproval) error {
	if s == nil {
		return errors.New("Approval history store is required")
	}
	if err := validatePersistedApprovals(approvals); err != nil {
		return fmt.Errorf("validate Approval history state: %w", err)
	}
	document := persistedApprovalDocument{
		Version:   approvalStoreVersion,
		Approvals: clonePersistedApprovals(approvals),
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Approval history store: %w", err)
	}
	if int64(len(data)) > s.maxBytes {
		return fmt.Errorf("Approval history store exceeds %d bytes", s.maxBytes)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeApprovalStateAtomically(s.path, data)
}

func validatePersistedApprovals(approvals []PersistedApproval) error {
	if len(approvals) > maxPersistedApprovals {
		return fmt.Errorf("approval count %d exceeds persistence limit %d",
			len(approvals), maxPersistedApprovals)
	}
	ids := make(map[string]struct{}, len(approvals))
	callIDs := make(map[string]struct{}, len(approvals))
	for _, approval := range approvals {
		if _, err := restoreRequest(approval.Request); err != nil {
			return fmt.Errorf("approval %q: %w", approval.Request.ID, err)
		}
		if approval.Reference.Correlation != CorrelationTrusted {
			return fmt.Errorf("approval %q has non-trusted correlation", approval.Request.ID)
		}
		reference, err := TrustedReference(
			approval.Reference.ID,
			approval.Reference.SessionID,
			approval.Reference.ExpiresAt,
		)
		if err != nil {
			return err
		}
		if reference.ID != approval.Request.ID {
			return fmt.Errorf("request ID %q does not match reference ID %q",
				approval.Request.ID, reference.ID)
		}
		if len([]rune(approval.Reference.Summary)) > 1024 {
			return fmt.Errorf("approval %q summary exceeds 1024 runes", approval.Request.ID)
		}
		if _, exists := ids[approval.Request.ID]; exists {
			return fmt.Errorf("duplicate approval ID %q", approval.Request.ID)
		}
		ids[approval.Request.ID] = struct{}{}
		if _, exists := callIDs[approval.Request.CallID]; exists {
			return fmt.Errorf("duplicate approval call ID %q", approval.Request.CallID)
		}
		callIDs[approval.Request.CallID] = struct{}{}
	}
	return nil
}

func restoreRequest(snapshot Snapshot) (*Request, error) {
	if snapshot.Revision == 0 {
		return nil, errors.New("approval revision must be positive")
	}
	if _, err := New(snapshot.ID, snapshot.CallID); err != nil {
		return nil, err
	}
	if len(snapshot.CallID) > 128 {
		return nil, errors.New("tool call ID exceeds 128 characters")
	}
	switch snapshot.Decision {
	case DecisionNone, DecisionApprove, DecisionReject:
	default:
		return nil, fmt.Errorf("invalid approval decision %q", snapshot.Decision)
	}
	switch snapshot.Status {
	case StatusPending:
		if snapshot.Decision != DecisionNone {
			return nil, errors.New("pending approval must not have a decision")
		}
	case StatusSubmitting:
		if snapshot.Decision == DecisionNone {
			return nil, errors.New("submitting approval requires a decision")
		}
	case StatusApproved:
		if snapshot.Decision != DecisionApprove {
			return nil, errors.New("approved approval requires an approve decision")
		}
	case StatusRejected:
		if snapshot.Decision != DecisionReject {
			return nil, errors.New("rejected approval requires a reject decision")
		}
	case StatusExpired, StatusCancelled, StatusUnknown:
	default:
		return nil, fmt.Errorf("invalid approval status %q", snapshot.Status)
	}
	if snapshot.Execution != nil && snapshot.Status != StatusApproved {
		return nil, errors.New("approval execution requires approved status")
	}
	normalized, err := normalizeExecution(snapshot.Execution)
	if err != nil {
		return nil, err
	}
	snapshot.Execution = normalized
	if snapshot.Recovered && strings.TrimSpace(snapshot.RecoveryReason) == "" {
		return nil, errors.New("recovered approval requires a recovery reason")
	}
	if len(snapshot.RecoveryReason) > 128 {
		return nil, errors.New("approval recovery reason exceeds 128 characters")
	}
	return &Request{snapshot: cloneRequestSnapshot(snapshot)}, nil
}

func clonePersistedApprovals(approvals []PersistedApproval) []PersistedApproval {
	if approvals == nil {
		return nil
	}
	cloned := make([]PersistedApproval, len(approvals))
	for i, approval := range approvals {
		cloned[i] = PersistedApproval{
			Request:   cloneRequestSnapshot(approval.Request),
			Reference: cloneReference(approval.Reference),
		}
	}
	return cloned
}

func sortedTrackedApprovals(byID map[string]*trackedRequest) []PersistedApproval {
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	approvals := make([]PersistedApproval, 0, len(ids))
	for _, id := range ids {
		entry := byID[id]
		if entry == nil {
			continue
		}
		tracked := trackedSnapshot(entry)
		approvals = append(approvals, PersistedApproval{
			Request:   tracked.Request,
			Reference: tracked.Reference,
		})
	}
	return approvals
}

func writeApprovalStateAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, approvalStoreTempFilePrefix)
	if err != nil {
		return fmt.Errorf("create Approval history store temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set Approval history store permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write Approval history store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync Approval history store: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close Approval history store: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace Approval history store: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}
