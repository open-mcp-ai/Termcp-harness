package coreinstance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestManagerIdentifiesExternalCore(t *testing.T) {
	server := newIdentityServer(t)
	defer server.Close()
	journal := eventlog.New()
	manager := NewManager(journal)

	snapshot, err := manager.Bind(context.Background(), Binding{
		Endpoint:  server.URL,
		Ownership: OwnershipExternal,
	})
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if snapshot.State != StateIdentified || snapshot.Ownership != OwnershipExternal || snapshot.OwnershipVerified {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.PID != 4242 || snapshot.Version != "v0.2.8" || snapshot.InstanceKey == "" {
		t.Fatalf("identity fields = %+v", snapshot)
	}
	if !manager.Configured() {
		t.Fatal("manager is not configured")
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if len(window.Events) != 1 || window.Events[0].Type != eventlog.TypeCoreInstanceBound {
		t.Fatalf("events = %+v", window.Events)
	}
}

func TestManagerSendsBindingAuthToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": "v0.2.8"})
	})
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"daemon": true, "pid": 4242,
			"started_at": "2026-10-28T13:28:08Z", "version": "v0.2.8",
		})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	manager := NewManager(nil)
	snapshot, err := manager.Bind(context.Background(), Binding{
		Endpoint: server.URL, Ownership: OwnershipExternal, AuthToken: "secret-token",
	})
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	if snapshot.State != StateIdentified {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestManagerVerifiesManagedProcessIdentity(t *testing.T) {
	server := newIdentityServer(t)
	defer server.Close()
	startedAt := time.Date(2026, 10, 28, 13, 28, 8, 0, time.UTC)

	verified := NewManager(nil, WithStartedAtTolerance(5*time.Second))
	snapshot, err := verified.Bind(context.Background(), Binding{
		Endpoint:  server.URL,
		Ownership: OwnershipManaged,
		Managed:   &ManagedProcess{PID: 4242, StartedAt: startedAt.Add(-2 * time.Second)},
	})
	if err != nil {
		t.Fatalf("verified Bind() error = %v", err)
	}
	if snapshot.Ownership != OwnershipManaged || !snapshot.OwnershipVerified || snapshot.State != StateIdentified {
		t.Fatalf("verified snapshot = %+v", snapshot)
	}

	conflict := NewManager(nil, WithStartedAtTolerance(5*time.Second))
	snapshot, err = conflict.Bind(context.Background(), Binding{
		Endpoint:  server.URL,
		Ownership: OwnershipManaged,
		Managed:   &ManagedProcess{PID: 9999, StartedAt: startedAt},
	})
	if err == nil || snapshot.State != StateConflict || snapshot.Ownership != OwnershipUnverified {
		t.Fatalf("conflict snapshot = %+v, error = %v", snapshot, err)
	}
}

func TestManagerDetectsInstanceChange(t *testing.T) {
	server := newIdentityServer(t)
	defer server.Close()
	journal := eventlog.New()
	manager := NewManager(journal)
	first, err := manager.Bind(context.Background(), Binding{Endpoint: server.URL, Ownership: OwnershipExternal})
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	server.SetIdentity(5353, "2026-10-29T01:00:00Z")
	second, err := manager.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if !second.Changed || second.InstanceKey == first.InstanceKey || second.PreviousInstanceKey != first.InstanceKey {
		t.Fatalf("changed snapshot = %+v; first = %+v", second, first)
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	want := []eventlog.Type{eventlog.TypeCoreInstanceBound, eventlog.TypeCoreInstanceChanged}
	if len(window.Events) != len(want) {
		t.Fatalf("events = %+v", window.Events)
	}
	for i, eventType := range want {
		if window.Events[i].Type != eventType {
			t.Fatalf("event %d = %s, want %s", i, window.Events[i].Type, eventType)
		}
	}
}

func TestManagerMarksUnavailableWithoutDiscardingLastIdentity(t *testing.T) {
	server := newIdentityServer(t)
	journal := eventlog.New()
	manager := NewManager(journal)
	first, err := manager.Bind(context.Background(), Binding{Endpoint: server.URL, Ownership: OwnershipExternal})
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	server.Close()

	second, err := manager.Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh() error = nil")
	}
	if second.State != StateUnavailable || second.InstanceKey != first.InstanceKey || second.LastError == "" {
		t.Fatalf("unavailable snapshot = %+v", second)
	}
	window, queryErr := journal.Query(0, 10)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	if window.Events[len(window.Events)-1].Type != eventlog.TypeCoreInstanceUnavailable {
		t.Fatalf("events = %+v", window.Events)
	}
}

func TestManagerRecordsInitialUnavailableState(t *testing.T) {
	server := newIdentityServer(t)
	endpoint := server.URL
	server.Close()
	journal := eventlog.New()
	manager := NewManager(journal)
	snapshot, err := manager.Bind(context.Background(), Binding{Endpoint: endpoint, Ownership: OwnershipExternal})
	if err == nil {
		t.Fatal("Bind() error = nil")
	}
	if snapshot.State != StateUnavailable {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	window, queryErr := journal.Query(0, 10)
	if queryErr != nil {
		t.Fatalf("Query() error = %v", queryErr)
	}
	want := []eventlog.Type{eventlog.TypeCoreInstanceBound, eventlog.TypeCoreInstanceUnavailable}
	if len(window.Events) != len(want) {
		t.Fatalf("events = %+v", window.Events)
	}
	for i, eventType := range want {
		if window.Events[i].Type != eventType {
			t.Fatalf("event %d = %s, want %s", i, window.Events[i].Type, eventType)
		}
	}
}

func TestBindingValidation(t *testing.T) {
	manager := NewManager(nil)
	tests := []Binding{
		{Endpoint: "https://example.com", Ownership: OwnershipExternal},
		{Endpoint: "http://127.0.0.1:18765/api", Ownership: OwnershipExternal},
		{Endpoint: "http://127.0.0.1:18765", Ownership: OwnershipManaged},
		{Endpoint: "http://127.0.0.1:18765", Ownership: OwnershipExternal, Managed: &ManagedProcess{PID: 1, StartedAt: time.Now()}},
	}
	for _, binding := range tests {
		if _, err := manager.Bind(context.Background(), binding); err == nil {
			t.Fatalf("Bind(%+v) error = nil", binding)
		}
	}
}

type identityServer struct {
	*httptest.Server
	mu        sync.Mutex
	pid       int
	startedAt string
}

func newIdentityServer(t *testing.T) *identityServer {
	t.Helper()
	server := &identityServer{pid: 4242, startedAt: "2026-10-28T13:28:08Z"}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "v0.2.8"})
		case "/api/daemon":
			server.mu.Lock()
			pid := server.pid
			startedAt := server.startedAt
			server.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"daemon":          true,
				"pid":             pid,
				"started_at":      startedAt,
				"version":         "v0.2.8",
				"log":             "/tmp/termcp.log",
				"idle_timeout_ms": 30000,
			})
		default:
			http.NotFound(w, request)
		}
	}))
	return server
}

func (s *identityServer) SetIdentity(pid int, startedAt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pid = pid
	s.startedAt = startedAt
}

func TestRefreshRequiresBinding(t *testing.T) {
	manager := NewManager(nil)
	if _, err := manager.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Refresh() error = %v", err)
	}
}

func TestUnavailableOnCanceledContextDoesNotManufactureFailure(t *testing.T) {
	server := newIdentityServer(t)
	defer server.Close()
	manager := NewManager(nil)
	first, err := manager.Bind(context.Background(), Binding{Endpoint: server.URL, Ownership: OwnershipExternal})
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snapshot, err := manager.Refresh(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh() error = %v, want canceled", err)
	}
	if snapshot.InstanceKey != first.InstanceKey || snapshot.State != StateIdentified {
		t.Fatalf("canceled refresh snapshot = %+v", snapshot)
	}
}
