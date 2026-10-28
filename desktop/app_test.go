package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
)

func TestAppBootstrapReadsRuntimeContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/healthz":
			writeJSON(t, w, map[string]any{"status": "ok", "state": "running", "version": "test"})
		case "/v1/runtime":
			writeJSON(t, w, map[string]any{"instance_id": "runtime-1", "version": "test", "state": "running"})
		case "/v1/events":
			writeJSON(t, w, map[string]any{"events": []any{}, "latest_sequence": 0})
		case "/v1/mcp/servers":
			writeJSON(t, w, map[string]any{
				"servers":                  []any{},
				"total":                    0,
				"control_plane_configured": true,
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client, err := controlclient.New(server.URL)
	if err != nil {
		t.Fatalf("controlclient.New() error = %v", err)
	}
	app := newApp(client)
	app.ctx = context.Background()

	bootstrap, err := app.Bootstrap()
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	if bootstrap.Runtime.InstanceID != "runtime-1" || !bootstrap.MCP.ControlPlaneOn {
		t.Fatalf("Bootstrap() = %+v", bootstrap)
	}
	if got := app.RuntimeEndpoint(); got != server.URL {
		t.Fatalf("RuntimeEndpoint() = %q, want %q", got, server.URL)
	}
}

func TestAppEventsRejectsInvalidLimit(t *testing.T) {
	client, err := controlclient.New("http://127.0.0.1:8787")
	if err != nil {
		t.Fatalf("controlclient.New() error = %v", err)
	}
	app := newApp(client)
	app.ctx = context.Background()
	if _, err := app.Events(0, 0); err == nil {
		t.Fatal("Events(limit=0) error = nil")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
