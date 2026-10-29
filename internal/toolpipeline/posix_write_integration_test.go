package toolpipeline_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

// TestTermcpCorePOSIXWriteEffectsIntegration is opt-in and requires a
// dedicated POSIX session. It writes only under /tmp by default and leaves
// session lifecycle to the operator.
func TestTermcpCorePOSIXWriteEffectsIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_POSIX_WRITE_TEST") != "1" {
		t.Skip("set TERMCP_CORE_POSIX_WRITE_TEST=1 and provide a dedicated POSIX Core session")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	sessionID := os.Getenv("TERMCP_CORE_TEST_SESSION_ID")
	handle := os.Getenv("TERMCP_CORE_TEST_HANDLE")
	tempDir := os.Getenv("TERMCP_CORE_TEST_TEMP_DIR")
	if endpoint == "" || sessionID == "" {
		t.Fatal("TERMCP_CORE_MCP_URL and TERMCP_CORE_TEST_SESSION_ID are required")
	}
	if handle == "" {
		handle = sessionID + ":1"
	}
	if tempDir == "" {
		tempDir = "/tmp"
	}

	host := connectCoreHost(t, endpoint)
	defer host.Close()
	journal := eventlog.New()
	runPOSIXWriteEffectTests(t, host, journal, sessionID, handle, tempDir)
}

// TestTermcpCorePOSIXTransportDisconnectIntegration is opt-in. It aborts the
// MCP client connection after a remote side effect has started, then reads the
// result through a separate MCP host and session.
func TestTermcpCorePOSIXTransportDisconnectIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_POSIX_DISCONNECT_TEST") != "1" {
		t.Skip("set TERMCP_CORE_POSIX_DISCONNECT_TEST=1 and provide dedicated effect/observer sessions")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	effectSessionID := os.Getenv("TERMCP_CORE_TEST_EFFECT_SESSION_ID")
	observerSessionID := os.Getenv("TERMCP_CORE_TEST_OBSERVER_SESSION_ID")
	effectHandle := os.Getenv("TERMCP_CORE_TEST_EFFECT_HANDLE")
	tempDir := os.Getenv("TERMCP_CORE_TEST_TEMP_DIR")
	if endpoint == "" || effectSessionID == "" || observerSessionID == "" {
		t.Fatal("TERMCP_CORE_MCP_URL, TERMCP_CORE_TEST_EFFECT_SESSION_ID and TERMCP_CORE_TEST_OBSERVER_SESSION_ID are required")
	}
	if effectHandle == "" {
		effectHandle = effectSessionID + ":1"
	}
	if tempDir == "" {
		tempDir = "/tmp"
	}

	path := path.Join(tempDir, fmt.Sprintf("termcp-harness-disconnect-%d.txt", time.Now().UnixNano()))
	token := "DISCONNECT_" + fmt.Sprint(time.Now().UnixNano())
	command := fmt.Sprintf("printf '%%s\\n' '%s' >> '%s'; i=0; while [ $i -lt 60 ]; do echo tick; sleep 0.1; i=$((i+1)); done", token, path)

	effectHost := connectCoreHost(t, endpoint)
	observerHost := connectCoreHost(t, endpoint)
	defer observerHost.Close()
	defer func() {
		removeRemoteFile(t, observerHost, observerSessionID, path)
	}()

	journal := eventlog.New()
	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Effect:    toolcall.EffectSideEffect,
		Arguments: termArguments("run $1 $2", effectHandle, command),
		Reason:    "POSIX transport disconnect test",
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy: %v", err)
	}
	pipeline, err := toolpipeline.New(policy, effectHost, journal)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	type outcomeResult struct {
		outcome toolpipeline.Outcome
		err     error
	}
	done := make(chan outcomeResult, 1)
	go func() {
		outcome, invokeErr := pipeline.Invoke(context.Background(), toolpipeline.Request{
			CallID:    "posix-transport-disconnect",
			ServerID:  "termcp-core",
			ToolName:  "term",
			Arguments: termArguments("run $1 $2", effectHandle, command),
		})
		done <- outcomeResult{outcome: outcome, err: invokeErr}
	}()

	if !waitForRemoteText(t, observerHost, observerSessionID, path, token, 5*time.Second) {
		t.Fatal("remote side effect did not produce the sentinel before disconnect")
	}
	if err := effectHost.Close(); err != nil {
		t.Fatalf("close effect MCP host: %v", err)
	}

	var got outcomeResult
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("tool pipeline did not converge after MCP transport disconnect")
	}
	if got.err == nil || got.outcome.Call.Status != toolcall.StatusUnknown {
		t.Fatalf("transport disconnect outcome = %+v, error = %v; want unknown", got.outcome.Call, got.err)
	}

	time.Sleep(2 * time.Second)
	readBack := directRead(t, observerHost, observerSessionID, path)
	if count := strings.Count(readBack, token); count != 1 {
		t.Fatalf("remote file contains token %d times after disconnect, want one: %q", count, readBack)
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("event query: %v", err)
	}
	unknownEvents := 0
	for _, event := range window.Events {
		if event.SubjectID == "posix-transport-disconnect" && event.Type == eventlog.TypeToolCallUnknown {
			unknownEvents++
		}
	}
	if unknownEvents != 1 {
		t.Fatalf("unknown event count = %d, want 1", unknownEvents)
	}
}

func waitForRemoteText(t *testing.T, host *mcpcontrol.Host, sessionID, path, token string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := host.CallTool(ctx, "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "term", Arguments: termArguments("read $1 file $2", sessionID, path),
		}})
		cancel()
		if err == nil && result != nil && !result.IsError {
			text := ""
			for _, content := range result.Content {
				if value, ok := mcp.AsTextContent(content); ok {
					text += value.Text
				}
			}
			if strings.Contains(text, token) {
				return true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func directRead(t *testing.T, host *mcpcontrol.Host, sessionID, path string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := host.CallTool(ctx, "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "term", Arguments: termArguments("read $1 file $2", sessionID, path),
	}})
	if err != nil || result == nil || result.IsError {
		t.Fatalf("direct read result=%+v error=%v", result, err)
	}
	return resultText(t, result)
}

func runPOSIXWriteEffectTests(t *testing.T, host *mcpcontrol.Host, journal *eventlog.Journal, sessionID, handle, tempDir string) {
	t.Helper()
	stamp := fmt.Sprint(time.Now().UnixNano())
	idempotentPath := path.Join(tempDir, "termcp-harness-idempotent-"+stamp+".txt")
	sidePath := path.Join(tempDir, "termcp-harness-side-success-"+stamp+".txt")
	unknownPath := path.Join(tempDir, "termcp-harness-side-unknown-"+stamp+".txt")
	defer func() {
		for _, file := range []string{idempotentPath, sidePath, unknownPath} {
			removeRemoteFile(t, host, sessionID, file)
		}
	}()

	writeToken := "IDEMPOTENT_" + stamp
	sideToken := "SIDE_OK_" + stamp
	unknownToken := "UNKNOWN_" + stamp
	writeArgs := termArguments("write $1 file $2 --data $3", sessionID, idempotentPath, writeToken)
	sideCommand := fmt.Sprintf("printf '%%s\\n' '%s' >> '%s'", sideToken, sidePath)
	unknownCommand := fmt.Sprintf("printf '%%s\\n' '%s' >> '%s'; i=0; while [ $i -lt 40 ]; do echo tick; sleep 0.1; i=$((i+1)); done", unknownToken, unknownPath)

	policy, err := toolpipeline.NewAllowlistPolicy(
		toolpipeline.Rule{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectIdempotentWrite, Arguments: writeArgs, Reason: "POSIX idempotent write"},
		toolpipeline.Rule{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly, Arguments: termArguments("read $1 file $2", sessionID, idempotentPath), Reason: "POSIX idempotent read"},
		toolpipeline.Rule{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectSideEffect, Arguments: termArguments("run $1 $2", handle, sideCommand), Reason: "POSIX side effect success"},
		toolpipeline.Rule{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly, Arguments: termArguments("read $1 file $2", sessionID, sidePath), Reason: "POSIX side effect read"},
		toolpipeline.Rule{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectSideEffect, Arguments: termArguments("run $1 $2", handle, unknownCommand), Reason: "POSIX side effect ambiguity"},
		toolpipeline.Rule{ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly, Arguments: termArguments("read $1 file $2", sessionID, unknownPath), Reason: "POSIX unknown read"},
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
			CallID: fmt.Sprintf("posix-write-%d", i), ServerID: "termcp-core", ToolName: "term", Arguments: writeArgs,
		})
		if err != nil || outcome.Call.Status != toolcall.StatusSucceeded {
			t.Fatalf("POSIX idempotent write %d = %+v, error = %v", i, outcome.Call, err)
		}
	}
	readWrite := invokeRead(t, pipeline, "posix-read-write", sessionID, idempotentPath)
	if got := strings.Count(readWrite, writeToken); got != 1 {
		t.Fatalf("POSIX idempotent file contains token %d times: %q", got, readWrite)
	}

	runSideEffect(t, pipeline, "posix-side-success", handle, sideCommand)
	readSide := invokeRead(t, pipeline, "posix-read-side", sessionID, sidePath)
	if got := strings.Count(readSide, sideToken); got != 1 {
		t.Fatalf("POSIX side-effect file contains token %d times: %q", got, readSide)
	}

	unknownContext, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	outcome, err := pipeline.Invoke(unknownContext, toolpipeline.Request{
		CallID: "posix-side-unknown", ServerID: "termcp-core", ToolName: "term",
		Arguments: termArguments("run $1 $2", handle, unknownCommand),
	})
	if err == nil || outcome.Call.Status != toolcall.StatusUnknown {
		t.Fatalf("POSIX unknown effect = %+v, error = %v; want unknown", outcome.Call, err)
	}
	time.Sleep(5 * time.Second)
	readUnknown := invokeRead(t, pipeline, "posix-read-unknown", sessionID, unknownPath)
	if got := strings.Count(readUnknown, unknownToken); got != 1 {
		t.Fatalf("POSIX unknown file contains token %d times, want one: %q", got, readUnknown)
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("event query: %v", err)
	}
	unknownEvents := 0
	for _, event := range window.Events {
		if event.SubjectID == "posix-side-unknown" && event.Type == eventlog.TypeToolCallUnknown {
			unknownEvents++
		}
	}
	if unknownEvents != 1 {
		t.Fatalf("unknown event count = %d, want 1", unknownEvents)
	}
}

func removeRemoteFile(t *testing.T, host *mcpcontrol.Host, sessionID, file string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := host.CallTool(ctx, "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "term", Arguments: termArguments("rm $1 file $2", sessionID, file),
	}})
	if err != nil || result == nil || result.IsError {
		t.Logf("cleanup %s failed: result=%+v error=%v", file, result, err)
	}
}
