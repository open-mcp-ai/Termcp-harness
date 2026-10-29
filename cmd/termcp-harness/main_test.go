package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
)

func TestVersionCommand(t *testing.T) {
	oldVersion := version
	version = "test-version"
	t.Cleanup(func() { version = oldVersion })

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "test-version" {
		t.Fatalf("version output = %q, want test-version", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"unknown"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("run() error = %v, want usage error", err)
	}
}

func TestMCPProbeRejectsNonLoopbackEndpoint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"mcp", "probe", "--endpoint", "https://example.com/mcp"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("run() error = %v, want loopback validation error", err)
	}
}

func TestCoreIdentifyCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/version":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "v0.2.8"})
		case "/api/daemon":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"daemon":     true,
				"pid":        14560,
				"started_at": "2026-10-28T13:28:08Z",
				"version":    "v0.2.8",
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"core", "identify", "--endpoint", server.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var snapshot coreinstance.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode identity output: %v\n%s", err, stdout.String())
	}
	if snapshot.PID != 14560 || snapshot.Ownership != coreinstance.OwnershipExternal || snapshot.InstanceKey == "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestCoreIdentifyRejectsNonLoopbackEndpoint(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"core", "identify", "--endpoint", "http://example.com:18765"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("run() error = %v, want loopback validation error", err)
	}
}

func TestCoreOriginFromMCP(t *testing.T) {
	got, err := coreOriginFromMCP("http://127.0.0.1:18765/stream?ignored=true")
	if err != nil {
		t.Fatalf("coreOriginFromMCP() error = %v", err)
	}
	if got != "http://127.0.0.1:18765" {
		t.Fatalf("coreOriginFromMCP() = %q", got)
	}
}
