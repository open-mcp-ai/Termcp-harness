package mcpcontrol

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestHostLifecycleAndCapabilityNotification(t *testing.T) {
	journal := eventlog.New()
	client := newFakeHostClient()
	host := NewHost(
		WithHostEventLog(journal),
		withHostClientFactory(func(clientConfig) (clientConnection, error) { return client, nil }),
	)
	registerTestServer(t, host)

	if err := host.Connect(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	snapshot := mustSnapshot(t, host, "termcp-core")
	if snapshot.Connection != ConnectionConnected || snapshot.Ready {
		t.Fatalf("connected snapshot = %+v", snapshot)
	}

	if err := host.LoadCapabilities(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("LoadCapabilities() error = %v", err)
	}
	snapshot = mustSnapshot(t, host, "termcp-core")
	if snapshot.CapabilityState != CapabilitiesLoaded || snapshot.Capabilities.ToolCount != 1 || snapshot.Ready {
		t.Fatalf("loaded snapshot = %+v", snapshot)
	}
	if tools := host.Tools("termcp-core"); len(tools) != 1 || tools[0].Name != "term" {
		t.Fatalf("Tools() = %+v", tools)
	}

	if _, err := host.Enable("termcp-core"); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	if snapshot = mustSnapshot(t, host, "termcp-core"); !snapshot.Ready {
		t.Fatalf("enabled snapshot = %+v", snapshot)
	}

	client.notify(mcp.JSONRPCNotification{
		JSONRPC: "2.0",
		Notification: mcp.Notification{
			Method: mcp.MethodNotificationToolsListChanged,
		},
	})
	snapshot = mustSnapshot(t, host, "termcp-core")
	if snapshot.Ready || snapshot.CapabilityState != CapabilitiesStale || snapshot.Enablement != EnablementEnabled {
		t.Fatalf("stale snapshot = %+v", snapshot)
	}
	if tools := host.Tools("termcp-core"); tools != nil {
		t.Fatalf("Tools() after stale = %+v, want nil", tools)
	}

	if err := host.LoadCapabilities(context.Background(), "termcp-core"); err != nil {
		t.Fatalf("refresh LoadCapabilities() error = %v", err)
	}
	if _, err := host.Enable("termcp-core"); err != nil {
		t.Fatalf("re-Enable() error = %v", err)
	}

	if _, err := host.Disable("termcp-core"); err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if err := host.Disconnect("termcp-core"); err != nil {
		t.Fatalf("Disconnect() error = %v", err)
	}
	snapshot = mustSnapshot(t, host, "termcp-core")
	if snapshot.Connection != ConnectionDisconnected || snapshot.Ready {
		t.Fatalf("disconnected snapshot = %+v", snapshot)
	}
	if !client.closed.Load() {
		t.Fatal("live MCP client was not closed")
	}

	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	want := []eventlog.Type{
		eventlog.TypeMCPRegistered,
		eventlog.TypeMCPConnecting,
		eventlog.TypeMCPConnected,
		eventlog.TypeMCPCapabilitiesLoading,
		eventlog.TypeMCPCapabilitiesLoaded,
		eventlog.TypeMCPEnabled,
		eventlog.TypeMCPCapabilitiesStale,
		eventlog.TypeMCPCapabilitiesLoading,
		eventlog.TypeMCPCapabilitiesLoaded,
		eventlog.TypeMCPEnabled,
		eventlog.TypeMCPDisabled,
		eventlog.TypeMCPDisconnected,
	}
	if len(window.Events) != len(want) {
		t.Fatalf("events = %+v", window.Events)
	}
	for i, eventType := range want {
		if window.Events[i].Type != eventType || window.Events[i].Class != eventlog.ClassControl {
			t.Fatalf("event %d = %+v, want %s", i, window.Events[i], eventType)
		}
	}
}

func TestHostMarksConnectionFailureUnavailable(t *testing.T) {
	journal := eventlog.New()
	client := newFakeHostClient()
	client.startErr = context.DeadlineExceeded
	host := NewHost(
		WithHostEventLog(journal),
		withHostClientFactory(func(clientConfig) (clientConnection, error) { return client, nil }),
	)
	registerTestServer(t, host)

	if err := host.Connect(context.Background(), "termcp-core"); err == nil {
		t.Fatal("Connect() error = nil")
	}
	snapshot := mustSnapshot(t, host, "termcp-core")
	if snapshot.Connection != ConnectionUnavailable || snapshot.FailureReason != FailureNetwork || snapshot.Ready {
		t.Fatalf("failed snapshot = %+v", snapshot)
	}
	if !client.closed.Load() {
		t.Fatal("failed MCP client was not closed")
	}
}

func TestHostPassesAuthTokenOnlyToClientFactory(t *testing.T) {
	client := newFakeHostClient()
	received := make(chan string, 1)
	host := NewHost(withHostClientFactory(func(config clientConfig) (clientConnection, error) {
		received <- config.AuthToken
		return client, nil
	}))
	if _, err := host.Register(Spec{
		ID:        "termcp-core",
		Name:      "Termcp Core",
		Endpoint:  "http://127.0.0.1:18765/stream",
		AuthToken: "credential-reference-value",
	}); err != nil {
		t.Fatal(err)
	}
	if err := host.Connect(context.Background(), "termcp-core"); err != nil {
		t.Fatal(err)
	}
	if token := <-received; token != "credential-reference-value" {
		t.Fatalf("factory token = %q", token)
	}
	snapshot := mustSnapshot(t, host, "termcp-core")
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "credential-reference-value") {
		t.Fatal("MCP token leaked into snapshot")
	}
}

func TestHostSerializesConcurrentConnect(t *testing.T) {
	client := newFakeHostClient()
	var factoryCalls atomic.Int32
	host := NewHost(withHostClientFactory(func(clientConfig) (clientConnection, error) {
		factoryCalls.Add(1)
		return client, nil
	}))
	registerTestServer(t, host)

	const workers = 12
	var wg sync.WaitGroup
	wg.Add(workers)
	errorsSeen := make(chan error, workers)
	for range workers {
		go func() {
			defer wg.Done()
			errorsSeen <- host.Connect(context.Background(), "termcp-core")
		}()
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Connect() error = %v", err)
		}
	}
	if got := factoryCalls.Load(); got != 1 {
		t.Fatalf("factory calls = %d, want 1", got)
	}
}

func TestHostKeepsDuplicateToolNamesScopedByServerID(t *testing.T) {
	clientA := newFakeHostClient()
	clientB := newFakeHostClient()
	host := NewHost(withHostClientFactory(func(config clientConfig) (clientConnection, error) {
		if config.Endpoint == "http://127.0.0.1:18888/stream" {
			return clientA, nil
		}
		return clientB, nil
	}))
	for _, spec := range []Spec{
		{ID: "server-a", Name: "Server A", Endpoint: "http://127.0.0.1:18888/stream"},
		{ID: "server-b", Name: "Server B", Endpoint: "http://127.0.0.1:18889/stream"},
	} {
		if _, err := host.Register(spec); err != nil {
			t.Fatal(err)
		}
		if err := host.Connect(context.Background(), spec.ID); err != nil {
			t.Fatal(err)
		}
		if err := host.LoadCapabilities(context.Background(), spec.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := host.Enable(spec.ID); err != nil {
			t.Fatal(err)
		}
	}
	if snapshot := mustSnapshot(t, host, "server-a"); !snapshot.Ready {
		t.Fatalf("server-a = %+v", snapshot)
	}
	if snapshot := mustSnapshot(t, host, "server-b"); !snapshot.Ready {
		t.Fatalf("server-b = %+v", snapshot)
	}
	if tools := host.Tools("server-a"); len(tools) != 1 || tools[0].Name != "term" {
		t.Fatalf("server-a tools = %+v", tools)
	}
	if tools := host.Tools("server-b"); len(tools) != 1 || tools[0].Name != "term" {
		t.Fatalf("server-b tools = %+v", tools)
	}
}

func TestHostTermcpCoreIntegration(t *testing.T) {
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	if endpoint == "" {
		t.Skip("set TERMCP_CORE_MCP_URL to run against a real Core instance")
	}
	host := NewHost()
	spec := Spec{ID: "termcp-core", Name: "Termcp Core", Endpoint: endpoint}
	if _, err := host.Register(spec); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := host.Connect(context.Background(), spec.ID); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := host.LoadCapabilities(context.Background(), spec.ID); err != nil {
		t.Fatalf("LoadCapabilities() error = %v", err)
	}
	if _, err := host.Enable(spec.ID); err != nil {
		t.Fatalf("Enable() error = %v", err)
	}
	snapshot := mustSnapshot(t, host, spec.ID)
	if !snapshot.Ready || snapshot.Capabilities.ServerName != "termcp" || snapshot.Capabilities.ToolCount < 1 {
		t.Fatalf("ready integration snapshot = %+v", snapshot)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestHostSupervisorReconnectsAfterHealthFailure(t *testing.T) {
	journal := eventlog.New()
	first := newFakeHostClient()
	second := newFakeHostClient()
	var factoryCalls atomic.Int32
	host := NewHost(
		WithHostEventLog(journal),
		WithHostSupervision(20*time.Millisecond, 80*time.Millisecond, 20*time.Millisecond),
		withHostClientFactory(func(clientConfig) (clientConnection, error) {
			if factoryCalls.Add(1) == 1 {
				return first, nil
			}
			return second, nil
		}),
	)
	registerTestServer(t, host)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go host.Run(ctx)

	waitFor(t, func() bool {
		snapshot := mustSnapshot(t, host, "termcp-core")
		return snapshot.Ready
	})
	first.mu.Lock()
	first.pingErr = context.DeadlineExceeded
	first.mu.Unlock()

	waitFor(t, func() bool {
		snapshot := mustSnapshot(t, host, "termcp-core")
		return factoryCalls.Load() >= 2 && snapshot.Ready && snapshot.Connection == ConnectionConnected
	})
	if !first.closed.Load() {
		t.Fatal("failed connection was not closed")
	}
	window, err := journal.Query(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var unavailable, reconnected int
	for _, event := range window.Events {
		switch event.Type {
		case eventlog.TypeMCPUnavailable:
			unavailable++
		case eventlog.TypeMCPConnected:
			reconnected++
		}
	}
	if unavailable < 1 || reconnected < 2 {
		t.Fatalf("unavailable=%d connected=%d events=%+v", unavailable, reconnected, window.Events)
	}
}

func TestHostSupervisorRefreshesCapabilitiesAfterListChanged(t *testing.T) {
	client := newFakeHostClient()
	host := NewHost(
		WithHostSupervision(20*time.Millisecond, 80*time.Millisecond, time.Second),
		withHostClientFactory(func(clientConfig) (clientConnection, error) { return client, nil }),
	)
	registerTestServer(t, host)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go host.Run(ctx)
	waitFor(t, func() bool {
		return mustSnapshot(t, host, "termcp-core").Ready
	})

	client.notify(mcp.JSONRPCNotification{
		JSONRPC: "2.0",
		Notification: mcp.Notification{
			Method: mcp.MethodNotificationToolsListChanged,
		},
	})
	waitFor(t, func() bool {
		snapshot := mustSnapshot(t, host, "termcp-core")
		return snapshot.Ready && snapshot.CapabilityState == CapabilitiesLoaded
	})
}

func TestHostSupervisorConnectsDisabledServerWithoutEnabling(t *testing.T) {
	client := newFakeHostClient()
	host := NewHost(
		WithHostSupervision(20*time.Millisecond, 80*time.Millisecond, time.Second),
		withHostClientFactory(func(clientConfig) (clientConnection, error) { return client, nil }),
	)
	registerTestServer(t, host)
	if err := host.SetDesiredEnabled("termcp-core", false); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go host.Run(ctx)

	waitFor(t, func() bool {
		snapshot := mustSnapshot(t, host, "termcp-core")
		return snapshot.Connection == ConnectionConnected &&
			snapshot.CapabilityState == CapabilitiesLoaded
	})
	snapshot := mustSnapshot(t, host, "termcp-core")
	if snapshot.Ready || snapshot.Enablement != EnablementDisabled {
		t.Fatalf("disabled supervisor snapshot = %+v", snapshot)
	}
}

func registerTestServer(t *testing.T, host *Host) {
	t.Helper()
	if _, err := host.Register(Spec{
		ID:       "termcp-core",
		Name:     "Termcp Core",
		Endpoint: "http://127.0.0.1:18765/stream",
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
}

func mustSnapshot(t *testing.T, host *Host, id string) Snapshot {
	t.Helper()
	snapshot, ok := host.Snapshot(id)
	if !ok {
		t.Fatalf("Snapshot(%q) not found", id)
	}
	return snapshot
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not satisfied before timeout")
}

type fakeHostClient struct {
	mu               sync.Mutex
	notificationFunc func(mcp.JSONRPCNotification)
	initializeResult *mcp.InitializeResult
	toolsResult      *mcp.ListToolsResult
	toolCallResult   *mcp.CallToolResult
	toolCallErr      error
	toolCallFunc     func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)
	toolCallRequests []mcp.CallToolRequest
	startErr         error
	pingErr          error
	closed           atomic.Bool
}

func newFakeHostClient() *fakeHostClient {
	return &fakeHostClient{
		initializeResult: &mcp.InitializeResult{
			ProtocolVersion: "2025-06-18",
			Capabilities: mcp.ServerCapabilities{Tools: &struct {
				ListChanged bool `json:"listChanged,omitempty"`
			}{ListChanged: true}},
			ServerInfo: mcp.Implementation{Name: "termcp", Version: "v0.2.8"},
		},
		toolsResult: &mcp.ListToolsResult{Tools: []mcp.Tool{{
			Name:        "term",
			Description: "Terminal command DSL",
			InputSchema: mcp.ToolInputSchema{Type: "object", Properties: map[string]any{}},
		}}},
		toolCallResult: mcp.NewToolResultText("ok"),
	}
}

func (f *fakeHostClient) Start(context.Context) error {
	if f.startErr != nil {
		return f.startErr
	}
	return nil
}

func (f *fakeHostClient) Initialize(context.Context, mcp.InitializeRequest) (*mcp.InitializeResult, error) {
	return f.initializeResult, nil
}

func (f *fakeHostClient) Ping(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pingErr
}

func (f *fakeHostClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return f.toolsResult, nil
}

func (f *fakeHostClient) CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	f.mu.Lock()
	f.toolCallRequests = append(f.toolCallRequests, request)
	call := f.toolCallFunc
	result := f.toolCallResult
	callErr := f.toolCallErr
	f.mu.Unlock()
	if call != nil {
		return call(ctx, request)
	}
	return result, callErr
}

func (f *fakeHostClient) OnNotification(handler func(mcp.JSONRPCNotification)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notificationFunc = handler
}

func (f *fakeHostClient) Close() error {
	f.closed.Store(true)
	return nil
}

func (f *fakeHostClient) notify(notification mcp.JSONRPCNotification) {
	f.mu.Lock()
	handler := f.notificationFunc
	f.mu.Unlock()
	if handler != nil {
		handler(notification)
	}
}

var _ clientConnection = (*fakeHostClient)(nil)
