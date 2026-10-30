package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
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

func TestReadApprovalToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approval-token")
	const token = "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := readApprovalToken(path)
	if err != nil {
		t.Fatalf("readApprovalToken() error = %v", err)
	}
	if got != token {
		t.Fatalf("token = %q, want %q", got, token)
	}
}

func TestReadApprovalTokenRejectsEmptyAndOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(empty) error = %v", err)
	}
	if _, err := readApprovalToken(empty); err == nil {
		t.Fatal("empty token file was accepted")
	}

	oversized := filepath.Join(dir, "oversized")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", 4097)), 0o600); err != nil {
		t.Fatalf("WriteFile(oversized) error = %v", err)
	}
	if _, err := readApprovalToken(oversized); err == nil {
		t.Fatal("oversized token file was accepted")
	}
}

func TestApprovalsDecideCommand(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/approvals/approval-1/decision" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization header mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"approval": map[string]any{
				"id": "approval-1", "call_id": "call-1", "status": "approved",
				"decision": "approve", "revision": 3,
			},
			"outcome": "confirmed",
		})
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "approval-token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"approvals", "decide",
		"--runtime", server.URL,
		"--id", "approval-1",
		"--revision", "1",
		"--decision", "approve",
		"--token-file", tokenPath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Fatal("approval token appeared in command output")
	}
	var result controlclient.ApprovalDecisionResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if result.Outcome != "confirmed" || result.Approval.ID != "approval-1" {
		t.Fatalf("result = %+v", result)
	}
}

func TestApprovalsDecideRejectsInvalidDecision(t *testing.T) {
	tokenPath := filepath.Join(t.TempDir(), "approval-token")
	if err := os.WriteFile(tokenPath, []byte("0123456789abcdef0123456789abcdef"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"approvals", "decide",
		"--id", "approval-1",
		"--revision", "1",
		"--decision", "maybe",
		"--token-file", tokenPath,
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("invalid decision was accepted")
	}
}

func TestLoadToolPolicyRequiresExactAllowlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tool-policy.json")
	if err := os.WriteFile(path, []byte(`{
		"rules":[{
			"server_id":"termcp-core",
			"tool_name":"term",
			"effect":"read_only",
			"arguments":{"cmd":""}
		}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policy, err := loadToolPolicy(path)
	if err != nil {
		t.Fatalf("loadToolPolicy() error = %v", err)
	}
	allowed, err := policy.Decide(context.Background(), toolpipeline.Request{
		ServerID: "termcp-core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":""}`),
	})
	if err != nil || !allowed.Allowed {
		t.Fatalf("allowed decision = %+v, error = %v", allowed, err)
	}
	blocked, err := policy.Decide(context.Background(), toolpipeline.Request{
		ServerID: "termcp-core", ToolName: "term",
		Arguments: json.RawMessage(`{"cmd":"printf different"}`),
	})
	if err != nil || blocked.Allowed {
		t.Fatalf("blocked decision = %+v, error = %v", blocked, err)
	}
}

func TestLoadAgentSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "steps.json")
	if err := os.WriteFile(path, []byte(`{"steps":[{
		"call_id":"call-1",
		"server_id":"termcp-core",
		"tool_name":"term",
		"arguments":{"cmd":""}
	}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	steps, err := loadAgentSteps(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].CallID != "call-1" ||
		steps[0].ServerID != "termcp-core" || steps[0].ToolName != "term" {
		t.Fatalf("steps = %+v", steps)
	}
}
