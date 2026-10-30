package mcpcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

const (
	defaultHostTimeout       = 15 * time.Second
	defaultHostClientName    = "termcp-harness"
	defaultHostClientVersion = "dev"
)

type HostOptions struct {
	ClientName     string
	ClientVersion  string
	Timeout        time.Duration
	MaxTools       int
	MaxSchemaBytes int
}

type HostOption func(*hostConfig)

type hostConfig struct {
	journal *eventlog.Journal
	factory clientFactory
	options HostOptions
}

func WithHostEventLog(journal *eventlog.Journal) HostOption {
	return func(config *hostConfig) {
		config.journal = journal
	}
}

func withHostClientFactory(factory clientFactory) HostOption {
	return func(config *hostConfig) {
		config.factory = factory
	}
}

// Host owns live MCP clients and serializes lifecycle transitions per server.
// Registration, connection, capability loading, enablement, and readiness stay
// separate states.
type Host struct {
	mu      sync.RWMutex
	servers map[string]*managedServer
	options hostConfig
}

type managedServer struct {
	mu          sync.Mutex
	server      *Server
	client      clientConnection
	initialized *mcp.InitializeResult
	tools       []ToolDescriptor
}

func NewHost(options ...HostOption) *Host {
	config := hostConfig{
		factory: defaultClientFactory,
		options: HostOptions{
			ClientName:     defaultHostClientName,
			ClientVersion:  defaultHostClientVersion,
			Timeout:        defaultHostTimeout,
			MaxTools:       defaultMaxTools,
			MaxSchemaBytes: defaultMaxSchemaBytes,
		},
	}
	for _, option := range options {
		option(&config)
	}
	return &Host{
		servers: make(map[string]*managedServer),
		options: config,
	}
}

func (h *Host) Register(spec Spec) (Snapshot, error) {
	if h == nil {
		return Snapshot{}, errors.New("MCP host is required")
	}
	server, err := New(spec)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot, err := server.Register(1)
	if err != nil {
		return Snapshot{}, err
	}

	h.mu.Lock()
	if _, exists := h.servers[spec.ID]; exists {
		h.mu.Unlock()
		return Snapshot{}, fmt.Errorf("MCP server %q is already registered", spec.ID)
	}
	h.servers[spec.ID] = &managedServer{server: server}
	h.mu.Unlock()

	if err := h.record(eventlog.TypeMCPRegistered, snapshot, nil); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (h *Host) Connect(ctx context.Context, id string) error {
	managed, err := h.managed(id)
	if err != nil {
		return err
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()

	current := managed.server.Snapshot()
	if current.Connection == ConnectionConnected {
		return nil
	}
	next, err := managed.server.BeginConnect(current.Revision)
	if err != nil {
		return err
	}
	if err := h.record(eventlog.TypeMCPConnecting, next, nil); err != nil {
		return err
	}

	operationContext, cancel := context.WithTimeout(ctx, h.options.options.Timeout)
	defer cancel()

	connection, err := h.options.factory(next.Endpoint)
	if err != nil {
		return h.fail(managed, classifyFailure(err), fmt.Errorf("create MCP client: %w", err))
	}
	if err := connection.Start(operationContext); err != nil {
		_ = connection.Close()
		return h.fail(managed, classifyFailure(err), fmt.Errorf("start MCP client: %w", err))
	}
	initialized, err := connection.Initialize(operationContext, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    h.options.options.ClientName,
				Version: h.options.options.ClientVersion,
			},
		},
	})
	if err != nil {
		_ = connection.Close()
		return h.fail(managed, classifyFailure(err), fmt.Errorf("initialize MCP server: %w", err))
	}
	if err := validateInitializeResult(initialized); err != nil {
		_ = connection.Close()
		return h.fail(managed, FailureProtocol, err)
	}

	managed.client = connection
	managed.initialized = initialized
	managed.tools = nil
	connected, err := managed.server.MarkConnected(managed.server.Snapshot().Revision)
	if err != nil {
		_ = connection.Close()
		managed.client = nil
		managed.initialized = nil
		return err
	}
	connection.OnNotification(func(notification mcp.JSONRPCNotification) {
		h.handleNotification(id, notification)
	})
	return h.record(eventlog.TypeMCPConnected, connected, map[string]any{
		"name":             connected.Name,
		"protocol_version": initialized.ProtocolVersion,
		"server_version":   initialized.ServerInfo.Version,
	})
}

func (h *Host) LoadCapabilities(ctx context.Context, id string) error {
	managed, err := h.managed(id)
	if err != nil {
		return err
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()

	if managed.client == nil || managed.initialized == nil {
		return fmt.Errorf("MCP server %q is not connected", id)
	}
	current := managed.server.Snapshot()
	loading, err := managed.server.BeginCapabilityLoad(current.Revision)
	if err != nil {
		return err
	}
	if err := h.record(eventlog.TypeMCPCapabilitiesLoading, loading, nil); err != nil {
		return err
	}

	operationContext, cancel := context.WithTimeout(ctx, h.options.options.Timeout)
	defer cancel()
	toolsSupported := managed.initialized.Capabilities.Tools != nil
	if toolsSupported {
		tools, err := readTools(operationContext, managed.client, h.options.options.MaxTools, h.options.options.MaxSchemaBytes)
		if err != nil {
			return h.fail(managed, classifyFailure(err), err)
		}
		managed.tools = tools
	} else {
		managed.tools = nil
	}

	capabilities := Capabilities{
		ProtocolVersion: managed.initialized.ProtocolVersion,
		ServerName:      managed.initialized.ServerInfo.Name,
		ServerVersion:   managed.initialized.ServerInfo.Version,
		ToolsSupported:  toolsSupported,
		ToolCount:       len(managed.tools),
	}
	loaded, err := managed.server.LoadCapabilities(managed.server.Snapshot().Revision, capabilities)
	if err != nil {
		return err
	}
	return h.record(eventlog.TypeMCPCapabilitiesLoaded, loaded, map[string]any{
		"tool_count":       capabilities.ToolCount,
		"tools_supported":  capabilities.ToolsSupported,
		"protocol_version": capabilities.ProtocolVersion,
	})
}

func (h *Host) Enable(id string) (Snapshot, error) {
	managed, err := h.managed(id)
	if err != nil {
		return Snapshot{}, err
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()

	snapshot, err := managed.server.Enable(managed.server.Snapshot().Revision)
	if err != nil {
		return Snapshot{}, err
	}
	if err := h.record(eventlog.TypeMCPEnabled, snapshot, nil); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (h *Host) Disable(id string) (Snapshot, error) {
	managed, err := h.managed(id)
	if err != nil {
		return Snapshot{}, err
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()

	snapshot, err := managed.server.Disable(managed.server.Snapshot().Revision)
	if err != nil {
		return Snapshot{}, err
	}
	if err := h.record(eventlog.TypeMCPDisabled, snapshot, nil); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (h *Host) Disconnect(id string) error {
	managed, err := h.managed(id)
	if err != nil {
		return err
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()

	if managed.client != nil {
		_ = managed.client.Close()
	}
	managed.client = nil
	managed.initialized = nil
	managed.tools = nil
	snapshot, err := managed.server.MarkDisconnected(managed.server.Snapshot().Revision)
	if err != nil {
		return err
	}
	return h.record(eventlog.TypeMCPDisconnected, snapshot, nil)
}

func (h *Host) List() []Snapshot {
	if h == nil {
		return []Snapshot{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	servers := make([]Snapshot, 0, len(h.servers))
	for _, managed := range h.servers {
		servers = append(servers, managed.server.Snapshot())
	}
	sortSnapshots(servers)
	return servers
}

func (h *Host) Snapshot(id string) (Snapshot, bool) {
	managed, err := h.managed(id)
	if err != nil {
		return Snapshot{}, false
	}
	return managed.server.Snapshot(), true
}

func (h *Host) Tools(id string) []ToolDescriptor {
	managed, err := h.managed(id)
	if err != nil {
		return nil
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()
	snapshot := managed.server.Snapshot()
	if snapshot.Connection != ConnectionConnected || snapshot.CapabilityState != CapabilitiesLoaded {
		return nil
	}
	tools := make([]ToolDescriptor, len(managed.tools))
	for i, tool := range managed.tools {
		tools[i] = ToolDescriptor{
			Name:        tool.Name,
			Description: tool.Description,
			Schema:      append(json.RawMessage(nil), tool.Schema...),
		}
	}
	return tools
}

func (h *Host) Close() error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	ids := make([]string, 0, len(h.servers))
	for id := range h.servers {
		ids = append(ids, id)
	}
	h.mu.RUnlock()

	var closeErrors []error
	for _, id := range ids {
		managed, err := h.managed(id)
		if err != nil {
			continue
		}
		managed.mu.Lock()
		if managed.client != nil {
			if err := managed.client.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("close MCP server %q: %w", id, err))
			}
		}
		managed.client = nil
		managed.initialized = nil
		managed.tools = nil
		snapshot := managed.server.Snapshot()
		if snapshot.Connection == ConnectionConnected || snapshot.Connection == ConnectionUnavailable {
			if disconnected, err := managed.server.MarkDisconnected(snapshot.Revision); err == nil {
				_ = h.record(eventlog.TypeMCPDisconnected, disconnected, nil)
			}
		}
		managed.mu.Unlock()
	}
	return errors.Join(closeErrors...)
}

func (h *Host) handleNotification(id string, notification mcp.JSONRPCNotification) {
	if notification.Method != string(mcp.MethodNotificationToolsListChanged) {
		return
	}
	managed, err := h.managed(id)
	if err != nil {
		return
	}
	managed.mu.Lock()
	defer managed.mu.Unlock()

	for range 4 {
		current := managed.server.Snapshot()
		if current.Connection != ConnectionConnected || current.CapabilityState != CapabilitiesLoaded {
			return
		}
		stale, err := managed.server.MarkCapabilitiesStale(current.Revision)
		if errors.Is(err, ErrStaleRevision) {
			continue
		}
		if err != nil {
			return
		}
		managed.tools = nil
		_ = h.record(eventlog.TypeMCPCapabilitiesStale, stale, map[string]any{
			"notification": notification.Method,
		})
		return
	}
}

func (h *Host) fail(managed *managedServer, reason FailureReason, cause error) error {
	if reason == FailureNone {
		reason = FailureUnknown
	}
	if managed.client != nil {
		_ = managed.client.Close()
		managed.client = nil
	}
	managed.initialized = nil
	managed.tools = nil
	snapshot, stateErr := managed.server.MarkUnavailable(managed.server.Snapshot().Revision, reason)
	if stateErr == nil {
		_ = h.record(eventlog.TypeMCPUnavailable, snapshot, map[string]any{
			"failure_reason": reason,
		})
	}
	if stateErr != nil {
		return fmt.Errorf("%w (record unavailable state: %v)", cause, stateErr)
	}
	return cause
}

func (h *Host) record(eventType eventlog.Type, snapshot Snapshot, data map[string]any) error {
	if h.options.journal == nil {
		return nil
	}
	payload := map[string]any{"name": snapshot.Name}
	for key, value := range data {
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode MCP event data: %w", err)
	}
	_, err = h.options.journal.Append(eventlog.Entry{
		Type:      eventType,
		Class:     eventlog.ClassControl,
		SubjectID: snapshot.ID,
		Data:      encoded,
	})
	if err != nil {
		return fmt.Errorf("record %s event: %w", eventType, err)
	}
	return nil
}

func (h *Host) managed(id string) (*managedServer, error) {
	if h == nil {
		return nil, errors.New("MCP host is required")
	}
	h.mu.RLock()
	managed, ok := h.servers[id]
	h.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("MCP server %q is not registered", id)
	}
	return managed, nil
}

func validateInitializeResult(result *mcp.InitializeResult) error {
	if result == nil {
		return errors.New("MCP initialize returned no result")
	}
	if result.ProtocolVersion == "" || result.ServerInfo.Name == "" || result.ServerInfo.Version == "" {
		return errors.New("MCP initialize response is missing protocol version or server identity")
	}
	return nil
}

func classifyFailure(err error) FailureReason {
	if err == nil {
		return FailureNone
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return FailureNetwork
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return FailureNetwork
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "401"), strings.Contains(text, "unauthorized"):
		return FailureAuthentication
	case strings.Contains(text, "403"), strings.Contains(text, "forbidden"):
		return FailurePermission
	case strings.Contains(text, "protocol"), strings.Contains(text, "initialize"):
		return FailureProtocol
	default:
		return FailureUnknown
	}
}
