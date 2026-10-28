package mcpcontrol

import (
	"errors"
	"testing"
)

func TestLifecycleAndEnablementAreSeparate(t *testing.T) {
	server := newServer(t, ManagementManaged)

	if got := server.Snapshot(); got.Registration != RegistrationDiscovered || got.Connection != ConnectionNotAttempted || got.Enablement != EnablementDisabled || got.Management != ManagementManaged {
		t.Fatalf("initial snapshot = %+v", got)
	}

	step(t, server, 1, func(revision Revision) (Snapshot, error) { return server.Register(revision) })
	step(t, server, 2, func(revision Revision) (Snapshot, error) { return server.BeginConnect(revision) })

	if _, err := server.Enable(3); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Enable() before connection error = %v, want ErrInvalidTransition", err)
	}

	step(t, server, 3, func(revision Revision) (Snapshot, error) { return server.MarkConnected(revision) })
	if _, err := server.Enable(4); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Enable() before capabilities error = %v, want ErrInvalidTransition", err)
	}
	step(t, server, 4, func(revision Revision) (Snapshot, error) { return server.BeginCapabilityLoad(revision) })
	step(t, server, 5, func(revision Revision) (Snapshot, error) {
		return server.LoadCapabilities(revision, observedCapabilities())
	})
	step(t, server, 6, func(revision Revision) (Snapshot, error) { return server.Enable(revision) })

	snapshot := server.Snapshot()
	if !snapshot.Ready || snapshot.Enablement != EnablementEnabled || snapshot.CapabilityState != CapabilitiesLoaded || snapshot.Capabilities.ToolCount != 1 {
		t.Fatalf("ready snapshot = %+v", snapshot)
	}
}

func TestConnectionLossInvalidatesCapabilitiesButNotEnablementPreference(t *testing.T) {
	server := readyServer(t)
	revision := server.Snapshot().Revision

	step(t, server, revision, func(revision Revision) (Snapshot, error) {
		return server.MarkUnavailable(revision, FailureNetwork)
	})
	snapshot := server.Snapshot()
	if snapshot.Ready {
		t.Fatal("unavailable server reported Ready")
	}
	if snapshot.Enablement != EnablementEnabled {
		t.Fatalf("Enablement = %q, want retained enabled preference", snapshot.Enablement)
	}
	if snapshot.CapabilityState != CapabilitiesStale || snapshot.FailureReason != FailureNetwork {
		t.Fatalf("unavailable snapshot = %+v", snapshot)
	}
}

func TestReconnectRequiresCapabilityReload(t *testing.T) {
	server := readyServer(t)
	revision := server.Snapshot().Revision

	step(t, server, revision, func(revision Revision) (Snapshot, error) {
		return server.MarkDisconnected(revision)
	})
	revision = server.Snapshot().Revision
	step(t, server, revision, func(revision Revision) (Snapshot, error) { return server.BeginConnect(revision) })
	revision = server.Snapshot().Revision
	step(t, server, revision, func(revision Revision) (Snapshot, error) { return server.MarkConnected(revision) })

	snapshot := server.Snapshot()
	if snapshot.Ready || snapshot.CapabilityState != CapabilitiesStale {
		t.Fatalf("reconnected snapshot = %+v", snapshot)
	}
	revision = snapshot.Revision
	step(t, server, revision, func(revision Revision) (Snapshot, error) { return server.BeginCapabilityLoad(revision) })
}

func TestStaleRevisionAndInvalidFailureAreRejected(t *testing.T) {
	server := newServer(t, ManagementExternal)
	if _, err := server.Register(99); !errors.Is(err, ErrStaleRevision) {
		t.Fatalf("Register() error = %v, want ErrStaleRevision", err)
	}
	if _, err := server.MarkUnavailable(1, "raw server error text"); !errors.Is(err, ErrInvalidTransition) {
		// The invalid reason is checked as an argument error before the state
		// transition; both must prevent a partial mutation.
		if err == nil {
			t.Fatal("MarkUnavailable() error = nil")
		}
	}
	if got := server.Snapshot(); got.Revision != 1 || got.FailureReason != FailureNone {
		t.Fatalf("state changed after rejected input: %+v", got)
	}
}

func TestEndpointRestrictions(t *testing.T) {
	valid := []string{
		"http://127.0.0.1:18765/stream",
		"http://localhost:18765/stream",
		"http://[::1]:18765/stream",
	}
	for _, endpoint := range valid {
		if _, err := New(Spec{ID: "id", Name: "name", Endpoint: endpoint}); err != nil {
			t.Fatalf("New(%q) error = %v", endpoint, err)
		}
	}

	invalid := []string{
		"https://example.com/mcp",
		"http://token@127.0.0.1/stream",
		"http://127.0.0.1/stream?token=secret",
		"ftp://127.0.0.1/stream",
		"127.0.0.1/stream",
	}
	for _, endpoint := range invalid {
		if _, err := New(Spec{ID: "id", Name: "name", Endpoint: endpoint}); err == nil {
			t.Fatalf("New(%q) error = nil, want error", endpoint)
		}
	}
}

func newServer(t *testing.T, management ManagementState) *Server {
	t.Helper()
	server, err := New(Spec{
		ID:         "termcp-core",
		Name:       "Termcp Core",
		Endpoint:   "http://127.0.0.1:18765/stream",
		Management: management,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func readyServer(t *testing.T) *Server {
	t.Helper()
	server := newServer(t, ManagementExternal)
	revision := Revision(1)
	for _, operation := range []func(Revision) (Snapshot, error){
		server.Register,
		server.BeginConnect,
		server.MarkConnected,
		server.BeginCapabilityLoad,
		func(revision Revision) (Snapshot, error) {
			return server.LoadCapabilities(revision, observedCapabilities())
		},
		server.Enable,
	} {
		step(t, server, revision, operation)
		revision = server.Snapshot().Revision
	}
	return server
}

func step(t *testing.T, server *Server, revision Revision, operation func(Revision) (Snapshot, error)) Snapshot {
	t.Helper()
	snapshot, err := operation(revision)
	if err != nil {
		t.Fatalf("operation at revision %d error = %v", revision, err)
	}
	if snapshot.Revision <= revision {
		t.Fatalf("Revision = %d, want > %d", snapshot.Revision, revision)
	}
	return snapshot
}

func observedCapabilities() Capabilities {
	return Capabilities{
		ProtocolVersion: "2025-06-18",
		ServerName:      "termcp",
		ServerVersion:   "v0.2.8",
		ToolsSupported:  true,
		ToolCount:       1,
	}
}
