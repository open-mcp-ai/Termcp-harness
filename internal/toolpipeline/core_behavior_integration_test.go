package toolpipeline_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func TestTermcpCoreCancellationIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_CANCEL_TEST") != "1" {
		t.Skip("set TERMCP_CORE_CANCEL_TEST=1 and provide dedicated effect/observer sessions")
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

	path := path.Join(tempDir, fmt.Sprintf("termcp-harness-cancel-%d.txt", time.Now().UnixNano()))
	token := "CANCEL_" + fmt.Sprint(time.Now().UnixNano())
	command := fmt.Sprintf("printf '%%s\\n' '%s' >> '%s'; i=0; while [ $i -lt 40 ]; do echo tick; sleep 0.1; i=$((i+1)); done", token, path)
	args := termArguments("run $1 $2", effectHandle, command)
	effectHost := connectCoreHost(t, endpoint)
	observerHost := connectCoreHost(t, endpoint)
	defer effectHost.Close()
	defer observerHost.Close()
	defer func() { removeRemoteFile(t, observerHost, observerSessionID, path) }()

	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectSideEffect,
		Arguments: args, Reason: "cancellation integration",
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy: %v", err)
	}
	journal := eventlog.New()
	pipeline, err := toolpipeline.New(policy, effectHost, journal)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		outcome toolpipeline.Outcome
		err     error
	}
	done := make(chan result, 1)
	go func() {
		outcome, invokeErr := pipeline.Invoke(ctx, toolpipeline.Request{
			CallID: "cancel-side-effect", ServerID: "termcp-core", ToolName: "term", Arguments: args,
		})
		done <- result{outcome: outcome, err: invokeErr}
	}()
	if !waitForRemoteText(t, observerHost, observerSessionID, path, token, 5*time.Second) {
		t.Fatal("cancellation side effect did not start")
	}
	cancel()
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline did not return after cancellation")
	}
	if got.err == nil || got.outcome.Call.Status != toolcall.StatusUnknown {
		t.Fatalf("cancellation outcome = %+v, error = %v; want unknown", got.outcome.Call, got.err)
	}
	time.Sleep(4 * time.Second)
	text := directRead(t, observerHost, observerSessionID, path)
	if strings.Count(text, token) != 1 {
		t.Fatalf("cancelled file text = %q, want one sentinel", text)
	}
	unknownEvents := countEvents(journal, "cancel-side-effect", eventlog.TypeToolCallUnknown)
	if unknownEvents != 1 {
		t.Fatalf("unknown event count = %d, want 1", unknownEvents)
	}
}

func TestTermcpCoreOutputFloodIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_OUTPUT_FLOOD_TEST") != "1" {
		t.Skip("set TERMCP_CORE_OUTPUT_FLOOD_TEST=1 and provide a dedicated POSIX session")
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
	host := connectCoreHost(t, endpoint)
	defer host.Close()

	args := termArgumentsWithOptions("run $1 $2", []string{handle, "yes 0123456789 | head -n 20000"}, map[string]any{
		"bytes_limit":       262144,
		"lines_limit":       5000,
		"total_bytes_limit": 262144,
		"timeout":           15,
	})
	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectSideEffect,
		Arguments: args, Reason: "output flood integration",
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy: %v", err)
	}
	journal := eventlog.New()
	pipeline, err := toolpipeline.New(policy, host, journal)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	outcome, err := pipeline.Invoke(ctx, toolpipeline.Request{
		CallID: "output-flood", ServerID: "termcp-core", ToolName: "term", Arguments: args,
	})
	if err != nil || outcome.Call.Status != toolcall.StatusSucceeded {
		t.Fatalf("output flood outcome = %+v, error = %v", outcome.Call, err)
	}
	text := resultText(t, outcome.Result)
	if len(text) < 1000 {
		t.Fatalf("output flood returned only %d bytes", len(text))
	}
	assertEventSequence(t, []eventlog.Event{
		findEvent(t, journal, "output-flood", eventlog.TypeToolCallProposed),
		findEvent(t, journal, "output-flood", eventlog.TypeToolCallReady),
		findEvent(t, journal, "output-flood", eventlog.TypeToolCallDispatching),
		findEvent(t, journal, "output-flood", eventlog.TypeToolCallSucceeded),
	}, []eventlog.Type{
		eventlog.TypeToolCallProposed,
		eventlog.TypeToolCallReady,
		eventlog.TypeToolCallDispatching,
		eventlog.TypeToolCallSucceeded,
	})
}

func TestTermcpCoreConcurrentControlTraceIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_CONCURRENT_TEST") != "1" {
		t.Skip("set TERMCP_CORE_CONCURRENT_TEST=1 to run against a real Core instance")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	if endpoint == "" {
		t.Fatal("TERMCP_CORE_MCP_URL is required")
	}
	host := connectCoreHost(t, endpoint)
	defer host.Close()
	args := json.RawMessage(`{"cmd":""}`)
	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID: "termcp-core", ToolName: "term", Effect: toolcall.EffectReadOnly,
		Arguments: args, Reason: "concurrent control trace",
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy: %v", err)
	}
	journal := eventlog.New()
	pipeline, err := toolpipeline.New(policy, host, journal)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const calls = 12
	type result struct {
		id  string
		err error
	}
	results := make(chan result, calls)
	for i := range calls {
		go func(index int) {
			id := fmt.Sprintf("concurrent-%02d", index)
			outcome, invokeErr := pipeline.Invoke(context.Background(), toolpipeline.Request{
				CallID: id, ServerID: "termcp-core", ToolName: "term", Arguments: args,
			})
			if invokeErr == nil && outcome.Call.Status != toolcall.StatusSucceeded {
				invokeErr = fmt.Errorf("status = %s", outcome.Call.Status)
			}
			results <- result{id: id, err: invokeErr}
		}(i)
	}
	for range calls {
		got := <-results
		if got.err != nil {
			t.Fatalf("concurrent call %s: %v", got.id, got.err)
		}
	}
	window, err := journal.Query(0, 1000)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(window.Events) != calls*4 {
		t.Fatalf("event count = %d, want %d", len(window.Events), calls*4)
	}
	for i := range calls {
		id := fmt.Sprintf("concurrent-%02d", i)
		assertEventSequence(t, eventsForSubject(window.Events, id), []eventlog.Type{
			eventlog.TypeToolCallProposed,
			eventlog.TypeToolCallReady,
			eventlog.TypeToolCallDispatching,
			eventlog.TypeToolCallSucceeded,
		})
	}
}

func termArgumentsWithOptions(cmd string, args []string, options map[string]any) json.RawMessage {
	values := map[string]any{"cmd": cmd, "args": args}
	for key, value := range options {
		values[key] = value
	}
	encoded, _ := json.Marshal(values)
	return encoded
}

func findEvent(t *testing.T, journal *eventlog.Journal, subjectID string, eventType eventlog.Type) eventlog.Event {
	t.Helper()
	window, err := journal.Query(0, 1000)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, event := range window.Events {
		if event.SubjectID == subjectID && event.Type == eventType {
			return event
		}
	}
	t.Fatalf("event %s for %s not found", eventType, subjectID)
	return eventlog.Event{}
}

func countEvents(journal *eventlog.Journal, subjectID string, eventType eventlog.Type) int {
	window, err := journal.Query(0, 1000)
	if err != nil {
		return -1
	}
	count := 0
	for _, event := range window.Events {
		if event.SubjectID == subjectID && event.Type == eventType {
			count++
		}
	}
	return count
}

func eventsForSubject(events []eventlog.Event, subjectID string) []eventlog.Event {
	result := make([]eventlog.Event, 0, 4)
	for _, event := range events {
		if event.SubjectID == subjectID {
			result = append(result, event)
		}
	}
	return result
}

func assertEventSequence(t *testing.T, events []eventlog.Event, want []eventlog.Type) {
	t.Helper()
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(want), events)
	}
	for i, eventType := range want {
		if events[i].Type != eventType {
			t.Fatalf("event %d type = %q, want %q", i, events[i].Type, eventType)
		}
	}
}
