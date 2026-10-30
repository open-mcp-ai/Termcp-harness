package controlclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBootstrapReadsOnlyFixedControlEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/healthz":
			writeTestJSON(w, http.StatusOK, map[string]any{"status": "ok", "state": "running", "version": "test"})
		case "/v1/runtime":
			writeTestJSON(w, http.StatusOK, map[string]any{"instance_id": "runtime-1", "version": "test", "state": "running"})
		case "/v1/events":
			if request.URL.Query().Get("after") != "0" || request.URL.Query().Get("limit") != "200" {
				t.Errorf("unexpected event query = %s", request.URL.RawQuery)
			}
			writeTestJSON(w, http.StatusOK, map[string]any{"events": []any{}, "latest_sequence": 0})
		case "/v1/mcp/servers":
			writeTestJSON(w, http.StatusOK, map[string]any{"servers": []any{}, "total": 0, "control_plane_configured": true})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := client.Bootstrap(context.Background())
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if result.Runtime.InstanceID != "runtime-1" || result.Health.Status != "ok" || !result.MCP.ControlPlaneOn {
		t.Fatalf("Bootstrap() = %+v", result)
	}
}

func TestEventsValidatesLimit(t *testing.T) {
	client, err := New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := client.Events(context.Background(), 0, limit); err == nil {
			t.Fatalf("Events(limit=%d) error = nil", limit)
		}
	}
}

func TestClientRejectsUnsafeEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"https://127.0.0.1:8787",
		"http://example.com:8787",
		"http://token@127.0.0.1:8787",
		"http://127.0.0.1:8787/api",
		"http://127.0.0.1:8787?token=secret",
		"127.0.0.1:8787",
	} {
		if _, err := New(endpoint); err == nil {
			t.Fatalf("New(%q) error = nil", endpoint)
		}
	}
}

func TestClientLimitsResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":"` + strings.Repeat("x", maxResponseBytes) + `"}`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := client.Events(context.Background(), 0, 10); err == nil {
		t.Fatal("Events() error = nil for oversized response")
	}
}

func writeTestJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
