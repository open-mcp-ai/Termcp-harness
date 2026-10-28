package mcpcontrol

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestHostLifecycleAndCapabilityNotification(t *testing.T) {
	journal := eventlog.New()
	client := newFakeHostClient()
	host := NewHost(
		WithHostEventLog(journal),
		withHostClientFactory(func(string) (clientConnection, error) { return client, nil }),
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
		withHostClientFactory(func(string) (clientConnection, error) { return client, nil }),
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

func TestHostSerializesConcurrentConnect(t *testing.T) {
	client := newFakeHostClient()
	var factoryCalls atomic.Int32
	host := NewHost(withHostClientFactory(func(string) (clientConnection, error) {
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

type fakeHostClient struct {
	mu               sync.Mutex
	notificationFunc func(mcp.JSONRPCNotification)
	initializeResult *mcp.InitializeResult
	toolsResult      *mcp.ListToolsResult
	startErr         error
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

func (f *fakeHostClient) ListTools(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
	return f.toolsResult, nil
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
