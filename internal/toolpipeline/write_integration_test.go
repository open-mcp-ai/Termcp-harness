package toolpipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

// TestTermcpCoreWriteEffectsIntegration is opt-in because it writes and reads
// disposable files through a real terminal session. The session is provided by
// the operator and must be dedicated to this test; the test removes its own
// files but does not create or close the session.
func TestTermcpCoreWriteEffectsIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_WRITE_TEST") != "1" {
		t.Skip("set TERMCP_CORE_WRITE_TEST=1 and provide a dedicated Core session")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	sessionID := os.Getenv("TERMCP_CORE_TEST_SESSION_ID")
	handle := os.Getenv("TERMCP_CORE_TEST_HANDLE")
	if endpoint == "" || sessionID == "" {
		t.Fatal("TERMCP_CORE_MCP_URL and TERMCP_CORE_TEST_SESSION_ID are required")
	}
	if handle == "" {
		handle = sessionID + ":1"
	}
	tempDir := os.Getenv("TERMCP_CORE_TEST_TEMP_DIR")
	if tempDir == "" {
		tempDir = os.TempDir()
	}

	host := connectCoreHost(t, endpoint)
	defer host.Close()
	runWriteEffectTests(t, host, eventlog.New(), sessionID, handle, tempDir)
}

func runWriteEffectTests(t *testing.T, host *mcpcontrol.Host, journal *eventlog.Journal, sessionID, handle, tempDir string) {
	t.Helper()
	idempotentPath := filepath.Join(tempDir, fmt.Sprintf("termcp-harness-idempotent-%d.txt", time.Now().UnixNano()))
	sidePath := filepath.Join(tempDir, fmt.Sprintf("termcp-harness-side-success-%d.txt", time.Now().UnixNano()))
	unknownPath := filepath.Join(tempDir, fmt.Sprintf("termcp-harness-side-unknown-%d.txt", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = os.Remove(idempotentPath)
		_ = os.Remove(sidePath)
		_ = os.Remove(unknownPath)
	})
	wireIdempotent := wirePath(idempotentPath)
	wireSide := wirePath(sidePath)
	wireUnknown := wirePath(unknownPath)
	writeToken := "IDEMPOTENT_" + fmt.Sprint(time.Now().UnixNano())
	sideToken := "SIDE_OK_" + fmt.Sprint(time.Now().UnixNano())
	unknownToken := "UNKNOWN_" + fmt.Sprint(time.Now().UnixNano())
	writeArgs := termArguments("write $1 file $2 --data $3", sessionID, wireIdempotent, writeToken)
	sideCommand := fmt.Sprintf("Add-Content -LiteralPath '%s' -Value '%s'", windowsPath(sidePath), sideToken)
	unknownCommand := fmt.Sprintf("Add-Content -LiteralPath '%s' -Value '%s'; 1..40 | ForEach-Object { Write-Output 'tick'; Start-Sleep -Milliseconds 100 }", windowsPath(unknownPath), unknownToken)

	policy, err := toolpipeline.NewAllowlistPolicy(
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectIdempotentWrite,
			Arguments: writeArgs, Reason: "test idempotent write",
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: termArguments("read $1 file $2", sessionID, wireIdempotent), Reason: "test idempotent read",
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectSideEffect,
			Arguments: termArguments("run $1 $2", handle, sideCommand), Reason: "test side effect success",
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: termArguments("read $1 file $2", sessionID, wireSide), Reason: "test side effect read",
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectSideEffect,
			Arguments: termArguments("run $1 $2", handle, unknownCommand), Reason: "test side effect ambiguity",
		},
		toolpipeline.Rule{
			ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
			Arguments: termArguments("read $1 file $2", sessionID, wireUnknown), Reason: "test unknown result read",
		},
	)
	if err != nil {
		t.Fatalf("NewAllowlistPolicy: %v", err)
	}
	pipeline, err := toolpipeline.New(policy, host, journal)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := range 2 {
		outcome, err := pipeline.Invoke(context.Background(), toolpipeline.Request{
			CallID:    fmt.Sprintf("write-idempotent-%d", i),
			ServerID:  "termcp-core",
			ToolName:  "term",
			Arguments: writeArgs,
		})
		if err != nil || outcome.Call.Status != toolcall.StatusSucceeded {
			t.Fatalf("idempotent write %d = %+v, error = %v", i, outcome.Call, err)
		}
	}
	readWrite := invokeRead(t, pipeline, "read-idempotent", sessionID, wireIdempotent)
	if got := strings.Count(readWrite, writeToken); got != 1 {
		t.Fatalf("idempotent file contains token %d times: %q", got, readWrite)
	}

	runSideEffect(t, pipeline, "side-success", handle, sideCommand)
	readSide := invokeRead(t, pipeline, "read-side-success", sessionID, wireSide)
	if got := strings.Count(readSide, sideToken); got != 1 {
		t.Fatalf("side-effect file contains token %d times: %q", got, readSide)
	}

	unknownContext, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	outcome, err := pipeline.Invoke(unknownContext, toolpipeline.Request{
		CallID:    "side-unknown",
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: termArguments("run $1 $2", handle, unknownCommand),
	})
	if err == nil {
		t.Fatal("unknown side effect should return a transport/deadline error")
	}
	if outcome.Call.Status != toolcall.StatusUnknown {
		t.Fatalf("unknown side effect status = %s, want unknown", outcome.Call.Status)
	}
	time.Sleep(4 * time.Second)
	readUnknown := invokeRead(t, pipeline, "read-side-unknown", sessionID, wireUnknown)
	if got := strings.Count(readUnknown, unknownToken); got != 1 {
		t.Fatalf("unknown side-effect file contains token %d times, want exactly one (no retry): %q", got, readUnknown)
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("event query: %v", err)
	}
	unknownEvents := 0
	for _, event := range window.Events {
		if event.SubjectID == "side-unknown" && event.Type == eventlog.TypeToolCallUnknown {
			unknownEvents++
		}
	}
	if unknownEvents != 1 {
		t.Fatalf("unknown event count = %d, want 1", unknownEvents)
	}
}

func connectCoreHost(t *testing.T, endpoint string) *mcpcontrol.Host {
	t.Helper()
	host := mcpcontrol.NewHost()
	spec := mcpcontrol.Spec{
		ID:        "termcp-core",
		Name:      "Termcp Core",
		Endpoint:  endpoint,
		AuthToken: coreAuthToken(t),
	}
	if _, err := host.Register(spec); err != nil {
		t.Fatalf("register: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := host.Connect(ctx, spec.ID); err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := host.LoadCapabilities(ctx, spec.ID); err != nil {
		t.Fatalf("load capabilities: %v", err)
	}
	if _, err := host.Enable(spec.ID); err != nil {
		t.Fatalf("enable: %v", err)
	}
	return host
}

func coreAuthToken(t *testing.T) string {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("TERMCP_CORE_MCP_AUTH_TOKEN_FILE"))
	if path == "" {
		return ""
	}
	token, err := tokenfile.Read(path)
	if err != nil {
		t.Fatalf("read Core MCP auth token: %v", err)
	}
	return token
}

func invokeRead(t *testing.T, pipeline *toolpipeline.Pipeline, callID, sessionID, path string) string {
	t.Helper()
	outcome, err := pipeline.Invoke(context.Background(), toolpipeline.Request{
		CallID: callID, ServerID: "termcp-core", ToolName: "term",
		Arguments: termArguments("read $1 file $2", sessionID, path),
	})
	if err != nil || outcome.Call.Status != toolcall.StatusSucceeded {
		t.Fatalf("read %s = %+v, error = %v", callID, outcome.Call, err)
	}
	return resultText(t, outcome.Result)
}

func runSideEffect(t *testing.T, pipeline *toolpipeline.Pipeline, callID, handle, command string) {
	t.Helper()
	outcome, err := pipeline.Invoke(context.Background(), toolpipeline.Request{
		CallID: callID, ServerID: "termcp-core", ToolName: "term",
		Arguments: termArguments("run $1 $2", handle, command),
	})
	if err != nil || outcome.Call.Status != toolcall.StatusSucceeded {
		t.Fatalf("side effect %s = %+v, error = %v", callID, outcome.Call, err)
	}
}

func termArguments(cmd string, args ...string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{"cmd": cmd, "args": args})
	return encoded
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if result == nil {
		t.Fatal("nil tool result")
	}
	var parts []string
	for _, content := range result.Content {
		if text, ok := mcp.AsTextContent(content); ok {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) == 0 {
		t.Fatalf("tool result has no text content: %+v", result)
	}
	return strings.Join(parts, "\n")
}

func wirePath(path string) string {
	path = filepath.ToSlash(path)
	if len(path) >= 2 && path[1] == ':' {
		return "/" + path
	}
	return path
}

func windowsPath(path string) string {
	return strings.ReplaceAll(path, "/", `\`)
}
