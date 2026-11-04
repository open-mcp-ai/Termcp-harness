package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinventory"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/recovery"
	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

func TestRecoveryEndpointReportsPartialStateWithoutPaths(t *testing.T) {
	rt, err := runtime.New("test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := recovery.Snapshot{
		ManifestConfigured: true,
		ManifestPresent:    true,
		ManifestVersion:    recovery.ManifestVersion,
		State:              recovery.StatePartial,
		Warning:            "store mismatch",
		Stores: []recovery.StoreSnapshot{{
			Name:       recovery.StoreEvents,
			Configured: true,
			State:      recovery.StorePresent,
			Exists:     true,
			Size:       10,
		}},
	}
	response := httptest.NewRecorder()
	NewHandler(rt, WithRecovery(recovery.NewReader(snapshot))).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/v1/recovery", nil),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d", response.Code)
	}
	var body recovery.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.State != recovery.StatePartial || body.Consistent || len(body.Stores) != 1 {
		t.Fatalf("body = %+v", body)
	}
}

func TestHealthReportsUnavailableBeforeStart(t *testing.T) {
	rt, err := runtime.New("test")
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}

	response := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	var body healthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode health response: %v", err)
	}
	if body.Status != "unavailable" || body.State != runtime.StateNew {
		t.Fatalf("health response = %+v", body)
	}
}

func TestHealthReportsRunning(t *testing.T) {
	rt := runningRuntime(t)

	response := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func TestRuntimeSnapshot(t *testing.T) {
	rt := runningRuntime(t)

	response := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/runtime", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	var body runtime.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode runtime response: %v", err)
	}
	if body.State != runtime.StateRunning || body.Version != "test" {
		t.Fatalf("runtime response = %+v", body)
	}
}

func TestRuntimeEndpointRejectsWrongMethod(t *testing.T) {
	rt := runningRuntime(t)
	response := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/runtime", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestEventEndpointReturnsBoundedWindow(t *testing.T) {
	journal := eventlog.New()
	if _, err := journal.Append(eventlog.Entry{
		Type:      eventlog.TypeRuntimeStarted,
		Class:     eventlog.ClassControl,
		SubjectID: "runtime-1",
	}); err != nil {
		t.Fatalf("Append(control) error = %v", err)
	}
	if _, err := journal.Append(eventlog.Entry{
		Type:      eventlog.Type("diagnostic.test"),
		Class:     eventlog.ClassDiagnostic,
		SubjectID: "runtime-1",
	}); err != nil {
		t.Fatalf("Append(diagnostic) error = %v", err)
	}
	rt := runningRuntime(t)
	handler := NewHandler(rt, WithEventLog(journal))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/events?after=0&limit=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	var window eventlog.Window
	if err := json.NewDecoder(response.Body).Decode(&window); err != nil {
		t.Fatalf("decode event window: %v", err)
	}
	if len(window.Events) != 1 || window.Events[0].Type != eventlog.TypeRuntimeStarted || !window.HasMore {
		t.Fatalf("event window = %+v", window)
	}
}

func TestEventEndpointRejectsInvalidCursorAndLimit(t *testing.T) {
	rt := runningRuntime(t)
	handler := NewHandler(rt, WithEventLog(eventlog.New()))
	for _, path := range []string{
		"/v1/events?after=bad",
		"/v1/events?limit=0",
		"/v1/events?limit=1001",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s status code = %d, want %d", path, response.Code, http.StatusBadRequest)
		}
	}
}

func TestEventEndpointRequiresConfiguration(t *testing.T) {
	rt := runningRuntime(t)
	response := httptest.NewRecorder()
	NewHandler(rt).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/events", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestMCPServersEndpointSeparatesRegistrationFromReadiness(t *testing.T) {
	registry := mcpcontrol.NewRegistry()
	server, err := mcpcontrol.New(mcpcontrol.Spec{
		ID:       "core",
		Name:     "Termcp Core",
		Endpoint: "http://127.0.0.1:18765/stream",
	})
	if err != nil {
		t.Fatalf("mcpcontrol.New() error = %v", err)
	}
	if err := registry.Add(server); err != nil {
		t.Fatalf("registry.Add() error = %v", err)
	}

	rt := runningRuntime(t)
	response := httptest.NewRecorder()
	NewHandler(rt, WithMCPRegistry(registry)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/mcp/servers", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	var body mcpServersResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode MCP response: %v", err)
	}
	if !body.ControlPlaneOn || body.Total != 1 || body.Connected != 0 || body.Ready != 0 {
		t.Fatalf("MCP response = %+v", body)
	}
	if len(body.Servers) != 1 || body.Servers[0].Registration != mcpcontrol.RegistrationDiscovered {
		t.Fatalf("MCP servers = %+v", body.Servers)
	}
}

func TestCoreEndpointReturnsIdentityAndOwnership(t *testing.T) {
	startedAt := time.Date(2026, 10, 28, 13, 28, 8, 0, time.UTC)
	reader := fakeCoreReader{
		configured: true,
		current: coreinstance.Snapshot{
			Endpoint:          "http://127.0.0.1:18765",
			State:             coreinstance.StateIdentified,
			Ownership:         coreinstance.OwnershipExternal,
			Version:           "v0.2.8",
			PID:               14560,
			StartedAt:         &startedAt,
			InstanceKey:       "instance-key",
			ObservedAt:        startedAt.Add(time.Minute),
			Revision:          1,
			OwnershipVerified: false,
		},
	}
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t), WithCore(reader)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/core", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", response.Code)
	}
	var body coreResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode core response: %v", err)
	}
	if !body.Configured || body.Core == nil || body.Core.PID != 14560 || body.Core.Ownership != coreinstance.OwnershipExternal {
		t.Fatalf("core response = %+v", body)
	}
}

func TestCoreEndpointReportsUnconfigured(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/core", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", response.Code)
	}
	var body coreResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode core response: %v", err)
	}
	if body.Configured || body.Core != nil {
		t.Fatalf("core response = %+v", body)
	}
}

func TestApprovalEndpointListsCoordinatedState(t *testing.T) {
	handler := NewHandler(runningRuntime(t), WithApprovals(fakeApprovalLister{
		approvals: []approval.Tracked{{
			Request: approval.Snapshot{
				ID: "approval-1", CallID: "call-1", Status: approval.StatusPending,
				Decision: approval.DecisionNone, Revision: 1,
			},
			Reference: approval.Reference{
				Correlation: approval.CorrelationTrusted,
				ID:          "approval-1",
				SessionID:   "session-1",
			},
		}},
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/approvals", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body approvalsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Configured || body.Total != 1 || len(body.Approvals) != 1 {
		t.Fatalf("response = %+v", body)
	}
	if body.CorrelationCapability != approval.CorrelationCapabilityUnverified {
		t.Fatalf("correlation capability = %q, want unverified", body.CorrelationCapability)
	}
	if body.Approvals[0].Request.ID != "approval-1" ||
		body.Approvals[0].Reference.Correlation != approval.CorrelationTrusted {
		t.Fatalf("approval = %+v", body.Approvals[0])
	}
}

func TestApprovalEndpointReportsUnavailable(t *testing.T) {
	handler := NewHandler(runningRuntime(t), WithApprovals(fakeApprovalLister{err: errors.New("query failed")}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/approvals", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

func TestApprovalEndpointReportsUnconfigured(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/approvals", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body approvalsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Configured || body.Total != 0 || body.Approvals == nil ||
		body.CorrelationCapability != approval.CorrelationCapabilityUnavailable {
		t.Fatalf("response = %+v", body)
	}
}

func TestApprovalEndpointDoesNotInferCapabilityFromVersion(t *testing.T) {
	handler := NewHandler(
		runningRuntime(t),
		WithApprovals(fakeApprovalLister{}),
		WithCore(fakeCoreReader{
			configured: true,
			current: coreinstance.Snapshot{
				Version:     "0.2.8",
				InstanceKey: "core-instance",
			},
		}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/approvals", nil))
	var body approvalsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.CorrelationCapability != approval.CorrelationCapabilityUnverified {
		t.Fatalf("correlation capability = %q, want unverified", body.CorrelationCapability)
	}
}

func TestApprovalEndpointReportsTrustedCorrelation(t *testing.T) {
	handler := NewHandler(
		runningRuntime(t),
		WithApprovals(fakeCapableApprovalLister{trusted: true}),
		WithCore(fakeCoreReader{
			configured: true,
			current: coreinstance.Snapshot{
				Version:     "0.2.8",
				InstanceKey: "core-instance",
			},
		}),
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/approvals", nil))
	var body approvalsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.CorrelationCapability != approval.CorrelationCapabilityTrusted {
		t.Fatalf("correlation capability = %q, want trusted", body.CorrelationCapability)
	}
}

func TestAgentSessionsEndpointListsSnapshots(t *testing.T) {
	handler := NewHandler(runningRuntime(t), WithAgentSessions(fakeSessionLister{sessions: []agentsession.Snapshot{{
		ID:            "agent-session-1",
		CoreSessionID: "core-session-1",
		State:         agentsession.StateWaitingApproval,
		Revision:      4,
		CurrentCallID: "call-1",
	}}}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agent/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body agentSessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Configured || body.Total != 1 || body.Sessions[0].State != agentsession.StateWaitingApproval {
		t.Fatalf("response = %+v", body)
	}
}

func TestAgentSessionsEndpointReportsUnconfigured(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/agent/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body agentSessionsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Configured || body.Total != 0 || body.Sessions == nil {
		t.Fatalf("response = %+v", body)
	}
}

func TestCoreSessionsEndpointListsBoundedMetadata(t *testing.T) {
	handler := NewHandler(runningRuntime(t), WithCoreSessions(fakeCoreSessionLister{
		snapshot: coreinventory.Snapshot{
			Configured: true,
			Total:      1,
			Sessions: []coreinventory.Session{{
				ID:        "core-session-1",
				Name:      "pi",
				Status:    "running",
				SSHConfig: "pi",
			}},
		},
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/core/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body coreinventory.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured || body.Total != 1 || body.Sessions[0].ID != "core-session-1" {
		t.Fatalf("response = %+v", body)
	}
}

func TestCoreSessionsEndpointReportsUnconfigured(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/core/sessions", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var body coreinventory.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Configured || body.Total != 0 || body.Sessions == nil {
		t.Fatalf("response = %+v", body)
	}
}

type fakeApprovalLister struct {
	approvals []approval.Tracked
	err       error
}

type fakeCapableApprovalLister struct {
	fakeApprovalLister
	trusted bool
}

func (f fakeCapableApprovalLister) TrustedCorrelationObserved() bool {
	return f.trusted
}

func (f fakeApprovalLister) List() ([]approval.Tracked, error) {
	return f.approvals, f.err
}

type fakeSessionLister struct {
	sessions []agentsession.Snapshot
}

type fakeCoreSessionLister struct {
	snapshot coreinventory.Snapshot
	err      error
}

func (f fakeCoreSessionLister) List(context.Context) (coreinventory.Snapshot, error) {
	return f.snapshot, f.err
}

func (f fakeSessionLister) List() []agentsession.Snapshot {
	return f.sessions
}

type fakeCoreReader struct {
	configured bool
	current    coreinstance.Snapshot
}

func (f fakeCoreReader) Configured() bool {
	return f.configured
}

func (f fakeCoreReader) Snapshot() (coreinstance.Snapshot, bool) {
	return f.current, f.current.InstanceKey != ""
}

func runningRuntime(t *testing.T) *runtime.Runtime {
	t.Helper()
	rt, err := runtime.New("test")
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	if err := rt.Start(); err != nil {
		t.Fatalf("runtime.Start() error = %v", err)
	}
	return rt
}
