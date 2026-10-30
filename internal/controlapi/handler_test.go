package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
