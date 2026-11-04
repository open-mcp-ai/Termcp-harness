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
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/recovery"
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

func TestSameHTTPOriginScopesCredentialReuse(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  bool
	}{
		{"http://127.0.0.1:18765/stream", "http://127.0.0.1:18765", true},
		{"http://127.0.0.1:18765/stream", "http://127.0.0.1:18766", false},
		{"http://127.0.0.1:18765/stream", "https://127.0.0.1:18765", false},
		{"http://127.0.0.1:18765/stream", "http://localhost:18765", false},
	}
	for _, test := range tests {
		if got := sameHTTPOrigin(test.left, test.right); got != test.want {
			t.Fatalf("sameHTTPOrigin(%q, %q) = %v, want %v", test.left, test.right, got, test.want)
		}
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

func TestServeApprovalStoreRequiresCore(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "approvals.json")
	var stderr bytes.Buffer
	err := runServe(
		context.Background(),
		[]string{"--approval-store-file", storePath},
		&stderr,
	)
	if err == nil || !strings.Contains(err.Error(), "requires a configured Core") {
		t.Fatalf("runServe() error = %v", err)
	}
}

func TestServeRecoveryManifestRequiresPersistentStore(t *testing.T) {
	manifestPath := filepath.Join(t.TempDir(), "recovery.json")
	var stderr bytes.Buffer
	err := runServe(
		context.Background(),
		[]string{"--recovery-manifest-file", manifestPath},
		&stderr,
	)
	if err == nil || !strings.Contains(err.Error(), "requires at least one persistent store") {
		t.Fatalf("runServe() error = %v", err)
	}
}

func TestServeWritesAndRefreshesRecoveryManifest(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "events.json")
	sessionPath := filepath.Join(root, "sessions.json")
	manifestPath := filepath.Join(root, "recovery.json")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var stderr bytes.Buffer
	go func() {
		done <- runServe(ctx, []string{
			"--listen", "127.0.0.1:0",
			"--event-log-file", eventPath,
			"--session-store-file", sessionPath,
			"--recovery-manifest-file", manifestPath,
		}, &stderr)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(manifestPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("recovery manifest was not created; stderr=%s", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServe() error = %v; stderr=%s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServe() did not stop after cancellation")
	}

	snapshot := recovery.Inspect(recovery.Config{
		ManifestPath: manifestPath,
		Stores: map[recovery.StoreName]string{
			recovery.StoreEvents:   eventPath,
			recovery.StoreSessions: sessionPath,
		},
	})
	if snapshot.State != recovery.StateComplete || !snapshot.Consistent {
		t.Fatalf("recovery snapshot = %+v; stderr=%s", snapshot, stderr.String())
	}
}

func TestRecoveryStatusReportsUnverifiedWithoutWritingManifest(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "events.json")
	manifestPath := filepath.Join(root, "recovery.json")
	if err := os.WriteFile(eventPath, []byte("events"), 0o600); err != nil {
		t.Fatalf("WriteFile(events) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{
		"recovery", "status",
		"--recovery-manifest-file", manifestPath,
		"--event-log-file", eventPath,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var snapshot recovery.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if snapshot.State != recovery.StateUnverified || snapshot.Consistent {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if _, err := os.Stat(manifestPath); !os.IsNotExist(err) {
		t.Fatalf("status created manifest: %v", err)
	}
	if strings.Contains(stdout.String(), root) || strings.Contains(stderr.String(), root) {
		t.Fatal("recovery status leaked a store path")
	}
}

func TestRecoveryRebaselineRequiresConfirmation(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "events.json")
	manifestPath := filepath.Join(root, "recovery.json")
	if err := os.WriteFile(eventPath, []byte("events"), 0o600); err != nil {
		t.Fatalf("WriteFile(events) error = %v", err)
	}
	const sentinel = `{"sentinel":true}`
	if err := os.WriteFile(manifestPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"recovery", "rebaseline",
		"--recovery-manifest-file", manifestPath,
		"--event-log-file", eventPath,
	}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("run() error = %v, want confirmation error", err)
	}
	data, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatalf("ReadFile(manifest) error = %v", readErr)
	}
	if string(data) != sentinel {
		t.Fatalf("manifest changed without confirmation: %q", data)
	}
}

func TestRecoveryRebaselineReplacesMismatchedGeneration(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "events.json")
	sessionPath := filepath.Join(root, "sessions.json")
	manifestPath := filepath.Join(root, "recovery.json")
	if err := os.WriteFile(eventPath, []byte("events-v1"), 0o600); err != nil {
		t.Fatalf("WriteFile(events) error = %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("sessions-v1"), 0o600); err != nil {
		t.Fatalf("WriteFile(sessions) error = %v", err)
	}
	config := recovery.Config{
		ManifestPath: manifestPath,
		Stores: map[recovery.StoreName]string{
			recovery.StoreEvents:   eventPath,
			recovery.StoreSessions: sessionPath,
		},
	}
	if _, err := recovery.WriteManifest(config); err != nil {
		t.Fatalf("WriteManifest() error = %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("sessions-v2"), 0o600); err != nil {
		t.Fatalf("WriteFile(changed sessions) error = %v", err)
	}
	if snapshot := recovery.Inspect(config); snapshot.State != recovery.StatePartial {
		t.Fatalf("precondition snapshot = %+v", snapshot)
	}

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{
		"recovery", "rebaseline",
		"--recovery-manifest-file", manifestPath,
		"--event-log-file", eventPath,
		"--session-store-file", sessionPath,
		"--confirm",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var result struct {
		Previous recovery.Snapshot `json:"previous"`
		Current  recovery.Snapshot `json:"current"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if result.Previous.State != recovery.StatePartial ||
		result.Current.State != recovery.StateComplete ||
		!result.Current.Consistent {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(stderr.String(), "stop the Harness Runtime") {
		t.Fatalf("stderr = %q, want stopped-runtime warning", stderr.String())
	}
	if strings.Contains(stdout.String(), root) || strings.Contains(stderr.String(), root) {
		t.Fatal("recovery rebaseline leaked a store path")
	}
	if snapshot := recovery.Inspect(config); snapshot.State != recovery.StateComplete {
		t.Fatalf("restored snapshot = %+v", snapshot)
	}
}

func TestRecoveryRebaselineRejectsUnreadableStoreWithoutOverwritingManifest(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "events.json")
	manifestPath := filepath.Join(root, "recovery.json")
	if err := os.Mkdir(eventPath, 0o700); err != nil {
		t.Fatalf("Mkdir(events) error = %v", err)
	}
	const sentinel = `{"sentinel":true}`
	if err := os.WriteFile(manifestPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"recovery", "rebaseline",
		"--recovery-manifest-file", manifestPath,
		"--event-log-file", eventPath,
		"--confirm",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("rebaseline accepted an unreadable store")
	}
	data, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatalf("ReadFile(manifest) error = %v", readErr)
	}
	if string(data) != sentinel {
		t.Fatalf("manifest was overwritten after inspection failure: %q", data)
	}
}

func TestRecoveryCommandsRequireManifestAndStore(t *testing.T) {
	tests := [][]string{
		{"recovery", "status"},
		{"recovery", "status", "--recovery-manifest-file", filepath.Join(t.TempDir(), "recovery.json")},
		{"recovery", "rebaseline", "--confirm", "--event-log-file", filepath.Join(t.TempDir(), "events.json")},
	}
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if err := run(context.Background(), args, &stdout, &stderr); err == nil {
			t.Fatalf("run(%v) succeeded", args)
		}
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

func TestApprovalsRefreshCommand(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/approvals/approval-1/execution/refresh" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization header mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"approval": map[string]any{
				"request": map[string]any{
					"id": "approval-1", "call_id": "call-1", "status": "approved",
					"decision": "approve", "revision": 3,
					"execution": map[string]any{"state": "succeeded"},
				},
				"reference": map[string]any{"correlation": "trusted", "approval_id": "approval-1"},
			},
			"changed": true, "complete": true,
		})
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "approval-token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"approvals", "refresh",
		"--runtime", server.URL,
		"--id", "approval-1",
		"--token-file", tokenPath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Fatal("approval token appeared in command output")
	}
	var result controlclient.ApprovalRefreshResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if !result.Changed || !result.Complete ||
		result.Approval.Request.Execution == nil ||
		result.Approval.Request.Execution.State != "succeeded" {
		t.Fatalf("result = %+v", result)
	}
}

func TestAgentLoopCommand(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization header mismatch")
		}
		switch request.URL.Path {
		case "/v1/agent/sessions":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session": map[string]any{"id": "session-1", "state": "new", "revision": 1},
			})
		case "/v1/agent/sessions/session-1/loop":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if body["input"] != "inspect the workspace" ||
				body["max_iterations"] != float64(2) ||
				body["max_tool_calls"] != float64(3) {
				t.Errorf("body = %+v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"session": map[string]any{
					"id": "session-1", "state": "completed", "revision": 5,
					"model_turns": 2, "model_tool_calls": 1,
				},
				"output": "finished",
				"calls":  []any{},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "approval-token")
	inputPath := filepath.Join(dir, "prompt.txt")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, []byte("  inspect the workspace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"agent", "loop",
		"--runtime", server.URL,
		"--id", "session-1",
		"--input-file", inputPath,
		"--token-file", tokenPath,
		"--max-iterations", "2",
		"--max-tool-calls", "3",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Fatal("control token appeared in command output")
	}
	var result controlclient.AgentLoopResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if result.Output != "finished" || result.Session.State != "completed" ||
		result.Session.ModelTurns != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestServeRequiresCompleteModelLoopConfiguration(t *testing.T) {
	tests := [][]string{
		{"--model-endpoint", "http://127.0.0.1:11434/v1/chat/completions"},
		{"--model-id", "test-model"},
		{"--model-endpoint", "http://127.0.0.1:11434/v1/chat/completions", "--model-id", "test-model"},
		{"--model-token-file", "token"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		if err := runServe(context.Background(), args, &stderr); err == nil {
			t.Fatalf("runServe(%v) error = nil", args)
		}
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

func TestApprovalsReconcileCommand(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/approvals/approval-1/reconcile" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization header mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"approval": map[string]any{
				"request": map[string]any{
					"id": "approval-1", "call_id": "call-1", "status": "rejected",
					"decision": "reject", "revision": 3,
				},
				"reference": map[string]any{"correlation": "trusted", "approval_id": "approval-1"},
			},
			"changed": true, "resolved": true,
		})
	}))
	defer server.Close()

	tokenPath := filepath.Join(t.TempDir(), "approval-token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"approvals", "reconcile",
		"--runtime", server.URL,
		"--id", "approval-1",
		"--token-file", tokenPath,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var result controlclient.ApprovalReconciliationResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if !result.Changed || !result.Resolved ||
		result.Approval.Request.Status != approval.StatusRejected {
		t.Fatalf("result = %+v", result)
	}
}

func TestAgentResumeCommand(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost ||
			request.URL.Path != "/v1/agent/sessions/session-1/loop/resume" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("authorization mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session": map[string]any{
				"id": "session-1", "state": "completed", "revision": 9,
				"model_turns": 3, "model_tool_calls": 1,
			},
			"output": "resumed",
			"calls": []any{map[string]any{
				"call_id": "model-call-1", "status": "succeeded",
			}},
		})
	}))
	defer server.Close()
	tokenPath := filepath.Join(t.TempDir(), "control-token")
	if err := os.WriteFile(tokenPath, []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{
		"agent", "resume",
		"--runtime", server.URL,
		"--id", "session-1",
		"--token-file", tokenPath,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var result controlclient.AgentLoopResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode resume output: %v\n%s", err, stdout.String())
	}
	if result.Output != "resumed" || result.Session.State != "completed" {
		t.Fatalf("result = %+v", result)
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
