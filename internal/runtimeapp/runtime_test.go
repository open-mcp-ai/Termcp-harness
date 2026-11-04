package runtimeapp

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func TestStartReportsDynamicEndpointAndCloseReleasesRuntime(t *testing.T) {
	root := t.TempDir()
	app, err := Start(context.Background(), Config{
		Version:          "test",
		ListenAddr:       "127.0.0.1:0",
		ApprovalToken:    "0123456789abcdef0123456789abcdef",
		EventLogFile:     filepath.Join(root, "events.json"),
		SessionStoreFile: filepath.Join(root, "sessions.json"),
		RecoveryManifest: filepath.Join(root, "recovery.json"),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if app.Endpoint() == "" || app.Endpoint() == "http://127.0.0.1:0" {
		t.Fatalf("Endpoint() = %q", app.Endpoint())
	}

	response, err := http.Get(app.Endpoint() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	var health struct {
		Status string `json:"status"`
		State  string `json:"state"`
	}
	if err := json.NewDecoder(response.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" || health.State != "running" {
		t.Fatalf("health = %+v", health)
	}

	closeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := app.Close(closeContext); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestCoreOriginFromMCPRequiresHTTPAndDropsPath(t *testing.T) {
	origin, err := CoreOriginFromMCP("http://127.0.0.1:18765/stream?ignored=true")
	if err != nil {
		t.Fatal(err)
	}
	if origin != "http://127.0.0.1:18765" {
		t.Fatalf("origin = %q", origin)
	}
	if _, err := CoreOriginFromMCP("file:///tmp/core"); err == nil {
		t.Fatal("non-HTTP MCP endpoint was accepted")
	}
}
