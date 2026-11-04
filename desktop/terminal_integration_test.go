package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/coreterminal"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
)

func TestTerminalBridgeCoreIntegration(t *testing.T) {
	runtimeEndpoint := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_CONTROL"))
	agentSessionID := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_TEST_AGENT_ID"))
	shellID := strings.TrimSpace(os.Getenv("TERMCP_CORE_TEST_SHELL_ID"))
	if runtimeEndpoint == "" || agentSessionID == "" || shellID == "" {
		t.Skip("set TERMCP_HARNESS_CONTROL, TERMCP_HARNESS_TEST_AGENT_ID, and TERMCP_CORE_TEST_SHELL_ID")
	}

	client, err := controlclient.New(runtimeEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	configureTerminalCoreAuth(t, app)

	result, err := app.TerminalShells(agentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, shell := range result.Shells {
		if shell.ID == shellID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Shell %q not present in Agent Session %q", shellID, agentSessionID)
	}

	opened, err := app.TerminalOpen(agentSessionID, shellID, 31, 117)
	if err != nil {
		t.Fatal(err)
	}
	if opened.HistoryStart < 0 || opened.HistoryEnd < opened.HistoryStart {
		t.Fatalf("invalid Core history byte range: start=%d end=%d", opened.HistoryStart, opened.HistoryEnd)
	}
	t.Logf(
		"terminal history: start=%d end=%d bytes=%d truncated=%t",
		opened.HistoryStart,
		opened.HistoryEnd,
		opened.HistoryBytes,
		opened.HistoryTruncated,
	)
	defer func() {
		_ = app.TerminalClose(opened.ID)
	}()

	marker := fmt.Sprintf("harness-terminal-e2e-%d", time.Now().UnixNano())
	if err := app.TerminalInput(opened.ID, "echo "+marker+"\r"); err != nil {
		t.Fatal(err)
	}
	if err := app.TerminalResize(opened.ID, 35, 133); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var observed strings.Builder
	var last coreterminal.PollResult
	for time.Now().Before(deadline) {
		polled, err := app.TerminalPoll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		last = polled
		observed.WriteString(polled.Output)
		if strings.Contains(observed.String(), marker) {
			if polled.State != "connected" {
				t.Fatalf("terminal state = %q during live output", polled.State)
			}
			if polled.OutputCursor <= opened.HistoryEnd {
				t.Fatalf("output cursor = %d after history ended at %d", polled.OutputCursor, opened.HistoryEnd)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("marker did not appear in terminal output; cursor=%d observed %q", last.OutputCursor, observed.String())
}

func TestTerminalBridgeCoreShellExit(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TERMCP_CORE_SHELL_EXIT_TEST")) != "1" {
		t.Skip("set TERMCP_CORE_SHELL_EXIT_TEST=1 to terminate a dedicated Core shell")
	}
	runtimeEndpoint := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_CONTROL"))
	agentSessionID := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_TEST_AGENT_ID"))
	shellID := strings.TrimSpace(os.Getenv("TERMCP_CORE_TEST_SHELL_ID"))
	if runtimeEndpoint == "" || agentSessionID == "" || shellID == "" {
		t.Skip("Core terminal integration identifiers are not configured")
	}

	client, err := controlclient.New(runtimeEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	configureTerminalCoreAuth(t, app)

	opened, err := app.TerminalOpen(agentSessionID, shellID, 31, 117)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = app.TerminalClose(opened.ID)
	}()

	if err := app.TerminalInput(opened.ID, "exit\r"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var last coreterminal.PollResult
	for time.Now().Before(deadline) {
		polled, err := app.TerminalPoll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		last = polled
		if polled.State == "ended" {
			if polled.OutputCursor < opened.HistoryEnd {
				t.Fatalf("Shell ended behind its opening cursor: opened=%d ended=%d", opened.HistoryEnd, polled.OutputCursor)
			}
			t.Logf(
				"Shell ended at cursor=%d after opening at %d",
				polled.OutputCursor,
				opened.HistoryEnd,
			)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(
		"Shell did not reach ended state: state=%s cursor=%d last_error=%q",
		last.State,
		last.OutputCursor,
		last.LastError,
	)
}

func TestTerminalBridgeCoreOutputFlood(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TERMCP_CORE_OUTPUT_FLOOD_TEST")) != "1" {
		t.Skip("set TERMCP_CORE_OUTPUT_FLOOD_TEST=1 for the 20,000-line Core terminal flood")
	}
	runtimeEndpoint := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_CONTROL"))
	agentSessionID := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_TEST_AGENT_ID"))
	shellID := strings.TrimSpace(os.Getenv("TERMCP_CORE_TEST_SHELL_ID"))
	if runtimeEndpoint == "" || agentSessionID == "" || shellID == "" {
		t.Skip("Core terminal integration identifiers are not configured")
	}

	client, err := controlclient.New(runtimeEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	app := newApp(client, "")
	app.ctx = context.Background()
	defer app.shutdown(context.Background())
	configureTerminalCoreAuth(t, app)

	opened, err := app.TerminalOpen(agentSessionID, shellID, 31, 117)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = app.TerminalClose(opened.ID)
	}()

	command := `powershell -NoProfile -Command "1..20000 | ForEach-Object { 'flood-line' }"` + "\r"
	started := time.Now()
	if err := app.TerminalInput(opened.ID, command); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(30 * time.Second)
	var observed strings.Builder
	var firstOutput time.Duration
	polls := 0
	for time.Now().Before(deadline) {
		polled, err := app.TerminalPoll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		polls++
		if firstOutput == 0 && polled.Output != "" {
			firstOutput = time.Since(started)
		}
		observed.WriteString(polled.Output)
		if polled.DroppedOutputBytes > 0 {
			t.Fatalf("bridge dropped %d output bytes", polled.DroppedOutputBytes)
		}
		if strings.Count(observed.String(), "flood-line") >= 20001 {
			t.Logf(
				"terminal bridge flood: duration=%s first_output=%s bytes=%d polls=%d",
				time.Since(started).Round(time.Millisecond),
				firstOutput.Round(time.Millisecond),
				observed.Len(),
				polls,
			)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe 20,000 flood lines; bytes=%d", observed.Len())
}

func configureTerminalCoreAuth(t *testing.T, app *App) {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("TERMCP_CORE_AUTH_TOKEN_FILE"))
	if path == "" {
		return
	}
	token, err := tokenfile.Read(path)
	if err != nil {
		t.Fatalf("read Core auth token: %v", err)
	}
	app.terminals.SetAuthToken(token)
}
