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
		case "/v1/core":
			writeJSON(t, w, map[string]any{
				"configured": true,
				"core": map[string]any{
					"endpoint":     "http://127.0.0.1:18765",
					"state":        "identified",
					"ownership":    "external",
					"version":      "v0.2.8",
					"pid":          14560,
					"instance_key": "instance-key",
					"observed_at":  "2026-10-29T01:00:00Z",
					"revision":     1,
				},
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
	if bootstrap.Runtime.InstanceID != "runtime-1" || !bootstrap.MCP.ControlPlaneOn || bootstrap.Core.Core == nil {
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
