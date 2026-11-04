package coreinstance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

const (
	defaultHTTPTimeout        = 5 * time.Second
	defaultRefreshInterval    = 10 * time.Second
	defaultStartedAtTolerance = 10 * time.Second
	maxResponseBytes          = 1 << 20
)

type Ownership string

const (
	OwnershipExternal   Ownership = "external"
	OwnershipManaged    Ownership = "managed"
	OwnershipUnverified Ownership = "unverified"
)

type State string

const (
	StateIdentified  State = "identified"
	StateUnavailable State = "unavailable"
	StateConflict    State = "conflict"
)

type ManagedProcess struct {
	PID       int
	StartedAt time.Time
}

type Binding struct {
	Endpoint  string
	Ownership Ownership
	Managed   *ManagedProcess
	// AuthToken is held only in memory and is never included in snapshots or events.
	AuthToken string
}

type Snapshot struct {
	Endpoint            string     `json:"endpoint"`
	State               State      `json:"state"`
	Ownership           Ownership  `json:"ownership"`
	OwnershipVerified   bool       `json:"ownership_verified"`
	Version             string     `json:"version,omitempty"`
	Daemon              bool       `json:"daemon"`
	PID                 int        `json:"pid,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	LogPath             string     `json:"log_path,omitempty"`
	IdleTimeoutMS       int64      `json:"idle_timeout_ms,omitempty"`
	InstanceKey         string     `json:"instance_key,omitempty"`
	ObservedAt          time.Time  `json:"observed_at"`
	Revision            uint64     `json:"revision"`
	Changed             bool       `json:"changed"`
	PreviousInstanceKey string     `json:"previous_instance_key,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
}

type Option func(*Manager)

func WithHTTPClient(client *http.Client) Option {
	return func(manager *Manager) {
		manager.client = client
	}
}

func WithNow(now func() time.Time) Option {
	return func(manager *Manager) {
		manager.now = now
	}
}

func WithStartedAtTolerance(tolerance time.Duration) Option {
	return func(manager *Manager) {
		manager.startedAtTolerance = tolerance
	}
}

type Manager struct {
	mu                 sync.RWMutex
	refreshMu          sync.Mutex
	binding            *Binding
	current            *Snapshot
	journal            *eventlog.Journal
	client             *http.Client
	now                func() time.Time
	startedAtTolerance time.Duration
}

func NewManager(journal *eventlog.Journal, options ...Option) *Manager {
	manager := &Manager{
		journal:            journal,
		client:             &http.Client{Timeout: defaultHTTPTimeout},
		now:                time.Now,
		startedAtTolerance: defaultStartedAtTolerance,
	}
	for _, option := range options {
		option(manager)
	}
	return manager
}

func ValidateEndpoint(raw string) (string, error) {
	return normalizeEndpoint(raw)
}

func (m *Manager) Bind(ctx context.Context, binding Binding) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("Core instance manager is required")
	}
	normalized, err := normalizeBinding(binding)
	if err != nil {
		return Snapshot{}, err
	}

	m.mu.Lock()
	m.binding = &normalized
	m.mu.Unlock()
	return m.Refresh(ctx)
}

func (m *Manager) Refresh(ctx context.Context) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("Core instance manager is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	m.mu.RLock()
	binding := cloneBinding(m.binding)
	current := cloneSnapshot(m.current)
	m.mu.RUnlock()
	if binding == nil {
		return Snapshot{}, errors.New("Core instance is not configured")
	}

	observed, err := m.identify(ctx, *binding)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if current != nil {
				return *current, err
			}
			return Snapshot{}, err
		}
		if observed.State != StateConflict {
			observed = m.unavailableSnapshot(binding, current, err)
		}
		observed, recordErr := m.apply(observed, current)
		return observed, errors.Join(err, recordErr)
	}
	observed, recordErr := m.apply(observed, current)
	return observed, recordErr
}

func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	if m == nil {
		return
	}
	if interval <= 0 {
		interval = defaultRefreshInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = m.Refresh(ctx)
		}
	}
}

func (m *Manager) Configured() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.binding != nil
}

func (m *Manager) Snapshot() (Snapshot, bool) {
	if m == nil {
		return Snapshot{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.current == nil {
		return Snapshot{}, false
	}
	return *cloneSnapshot(m.current), true
}

func (m *Manager) identify(ctx context.Context, binding Binding) (Snapshot, error) {
	apiVersion, err := m.readVersion(ctx, binding.Endpoint, binding.AuthToken)
	if err != nil {
		return Snapshot{}, err
	}
	daemon, err := m.readDaemon(ctx, binding.Endpoint, binding.AuthToken)
	if err != nil {
		return Snapshot{}, err
	}
	if strings.TrimSpace(daemon.Version) == "" {
		return Snapshot{}, errors.New("Core daemon response is missing version")
	}
	if apiVersion != daemon.Version {
		return Snapshot{}, fmt.Errorf("Core version mismatch: /api/version=%q /api/daemon=%q", apiVersion, daemon.Version)
	}
	if daemon.PID <= 0 {
		return Snapshot{}, errors.New("Core daemon response has invalid PID")
	}
	startedAt, err := time.Parse(time.RFC3339Nano, daemon.StartedAt)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse Core started_at: %w", err)
	}
	startedAt = startedAt.UTC()

	snapshot := Snapshot{
		Endpoint:      binding.Endpoint,
		State:         StateIdentified,
		Ownership:     OwnershipExternal,
		Version:       daemon.Version,
		Daemon:        daemon.Daemon,
		PID:           daemon.PID,
		StartedAt:     &startedAt,
		LogPath:       daemon.Log,
		IdleTimeoutMS: daemon.IdleTimeoutMS,
		ObservedAt:    m.now().UTC(),
	}
	snapshot.InstanceKey = instanceKey(snapshot, startedAt)

	if binding.Ownership == OwnershipManaged {
		managed := binding.Managed
		if managed == nil || managed.PID <= 0 || managed.StartedAt.IsZero() {
			snapshot.State = StateConflict
			snapshot.Ownership = OwnershipUnverified
			snapshot.LastError = "managed Core binding is missing its process identity"
			return snapshot, errors.New(snapshot.LastError)
		}
		delta := startedAt.Sub(managed.StartedAt.UTC())
		if delta < 0 {
			delta = -delta
		}
		if managed.PID != daemon.PID || delta > m.startedAtTolerance {
			snapshot.State = StateConflict
			snapshot.Ownership = OwnershipUnverified
			snapshot.LastError = fmt.Sprintf(
				"managed Core identity mismatch: bound pid=%d started_at=%s, observed pid=%d started_at=%s",
				managed.PID, managed.StartedAt.UTC().Format(time.RFC3339Nano), daemon.PID, startedAt.Format(time.RFC3339Nano),
			)
			return snapshot, errors.New(snapshot.LastError)
		}
		snapshot.Ownership = OwnershipManaged
		snapshot.OwnershipVerified = true
	}
	return snapshot, nil
}

type versionResponse struct {
	Version string `json:"version"`
}

type daemonResponse struct {
	Daemon        bool   `json:"daemon"`
	PID           int    `json:"pid"`
	StartedAt     string `json:"started_at"`
	Version       string `json:"version"`
	Log           string `json:"log"`
	IdleTimeoutMS int64  `json:"idle_timeout_ms"`
}

func (m *Manager) readVersion(ctx context.Context, endpoint, authToken string) (string, error) {
	var response versionResponse
	if err := m.getJSON(ctx, endpoint+"/api/version", authToken, &response); err != nil {
		return "", fmt.Errorf("read Core version: %w", err)
	}
	if strings.TrimSpace(response.Version) == "" {
		return "", errors.New("Core version response is empty")
	}
	return response.Version, nil
}

func (m *Manager) readDaemon(ctx context.Context, endpoint, authToken string) (daemonResponse, error) {
	var response daemonResponse
	if err := m.getJSON(ctx, endpoint+"/api/daemon", authToken, &response); err != nil {
		return daemonResponse{}, fmt.Errorf("read Core daemon identity: %w", err)
	}
	return response, nil
}

func (m *Manager) getJSON(ctx context.Context, target, authToken string, result any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if authToken != "" {
		request.Header.Set("Authorization", "Bearer "+authToken)
	}
	response, err := m.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	return nil
}

func (m *Manager) unavailableSnapshot(binding *Binding, previous *Snapshot, cause error) Snapshot {
	snapshot := Snapshot{
		Endpoint:   binding.Endpoint,
		State:      StateUnavailable,
		Ownership:  binding.Ownership,
		ObservedAt: m.now().UTC(),
		LastError:  cause.Error(),
	}
	if previous != nil {
		snapshot.Version = previous.Version
		snapshot.Daemon = previous.Daemon
		snapshot.PID = previous.PID
		snapshot.StartedAt = previous.StartedAt
		snapshot.LogPath = previous.LogPath
		snapshot.IdleTimeoutMS = previous.IdleTimeoutMS
		snapshot.InstanceKey = previous.InstanceKey
		snapshot.Ownership = previous.Ownership
		snapshot.OwnershipVerified = previous.OwnershipVerified
		snapshot.Revision = previous.Revision
	}
	return snapshot
}

func (m *Manager) apply(snapshot Snapshot, previous *Snapshot) (Snapshot, error) {
	m.mu.Lock()
	if previous != nil {
		snapshot.Revision = previous.Revision + 1
		if previous.InstanceKey != "" && snapshot.InstanceKey != "" && previous.InstanceKey != snapshot.InstanceKey {
			snapshot.Changed = true
			snapshot.PreviousInstanceKey = previous.InstanceKey
		}
	} else {
		snapshot.Revision = 1
	}
	m.current = &snapshot
	m.mu.Unlock()

	if previous == nil {
		recordErrors := []error{m.record(eventlog.TypeCoreInstanceBound, snapshot)}
		if snapshot.State == StateConflict {
			recordErrors = append(recordErrors, m.record(eventlog.TypeCoreOwnershipConflict, snapshot))
		}
		if snapshot.State == StateUnavailable {
			recordErrors = append(recordErrors, m.record(eventlog.TypeCoreInstanceUnavailable, snapshot))
		}
		return snapshot, errors.Join(recordErrors...)
	}
	var recordErrors []error
	if snapshot.State == StateConflict && previous.State != StateConflict {
		recordErrors = append(recordErrors, m.record(eventlog.TypeCoreOwnershipConflict, snapshot))
	}
	if snapshot.Changed {
		recordErrors = append(recordErrors, m.record(eventlog.TypeCoreInstanceChanged, snapshot))
	}
	if snapshot.State == StateUnavailable && (previous.State != StateUnavailable || previous.LastError != snapshot.LastError) {
		recordErrors = append(recordErrors, m.record(eventlog.TypeCoreInstanceUnavailable, snapshot))
	}
	return snapshot, errors.Join(recordErrors...)
}

func (m *Manager) record(eventType eventlog.Type, snapshot Snapshot) error {
	if m.journal == nil {
		return nil
	}
	payload := map[string]any{
		"endpoint":           snapshot.Endpoint,
		"state":              snapshot.State,
		"ownership":          snapshot.Ownership,
		"ownership_verified": snapshot.OwnershipVerified,
		"version":            snapshot.Version,
		"daemon":             snapshot.Daemon,
		"pid":                snapshot.PID,
		"instance_key":       snapshot.InstanceKey,
		"changed":            snapshot.Changed,
	}
	if snapshot.StartedAt != nil {
		payload["started_at"] = snapshot.StartedAt.UTC()
	}
	if snapshot.PreviousInstanceKey != "" {
		payload["previous_instance_key"] = snapshot.PreviousInstanceKey
	}
	if snapshot.LastError != "" {
		payload["last_error"] = snapshot.LastError
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = m.journal.Append(eventlog.Entry{
		Type:      eventType,
		Class:     eventlog.ClassControl,
		SubjectID: snapshot.Endpoint,
		Data:      encoded,
	})
	return err
}

func normalizeBinding(binding Binding) (Binding, error) {
	endpoint, err := normalizeEndpoint(binding.Endpoint)
	if err != nil {
		return Binding{}, err
	}
	if binding.Ownership == "" {
		binding.Ownership = OwnershipExternal
	}
	switch binding.Ownership {
	case OwnershipExternal:
		if binding.Managed != nil {
			return Binding{}, errors.New("external Core binding must not include a managed process")
		}
	case OwnershipManaged:
		if binding.Managed == nil {
			return Binding{}, errors.New("managed Core binding requires a managed process")
		}
		if binding.Managed.PID <= 0 || binding.Managed.StartedAt.IsZero() {
			return Binding{}, errors.New("managed Core binding requires a positive PID and start time")
		}
		copy := *binding.Managed
		binding.Managed = &copy
	default:
		return Binding{}, fmt.Errorf("unsupported Core ownership %q", binding.Ownership)
	}
	binding.Endpoint = endpoint
	return binding, nil
}

func normalizeEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse Core endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("Core endpoint must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Core endpoint must not contain credentials, query, or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("Core endpoint must be the HTTP origin without a path")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("Core endpoint host is required")
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("Core endpoint must be loopback in v1")
		}
	}
	parsed.Path = ""
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func instanceKey(snapshot Snapshot, startedAt time.Time) string {
	value := strings.Join([]string{
		snapshot.Endpoint,
		fmt.Sprint(snapshot.PID),
		startedAt.Format(time.RFC3339Nano),
		snapshot.Version,
	}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cloneBinding(binding *Binding) *Binding {
	if binding == nil {
		return nil
	}
	copy := *binding
	if binding.Managed != nil {
		managed := *binding.Managed
		copy.Managed = &managed
	}
	return &copy
}

func cloneSnapshot(snapshot *Snapshot) *Snapshot {
	if snapshot == nil {
		return nil
	}
	copy := *snapshot
	if snapshot.StartedAt != nil {
		startedAt := *snapshot.StartedAt
		copy.StartedAt = &startedAt
	}
	return &copy
}
