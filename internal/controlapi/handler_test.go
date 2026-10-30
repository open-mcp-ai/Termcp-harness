package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

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
