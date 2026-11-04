package controlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpdiscovery"
)

type fakeMCPDiscovery struct {
	snapshot mcpdiscovery.Snapshot
	lastID   string
}

func (f *fakeMCPDiscovery) Discover(context.Context) (mcpdiscovery.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeMCPDiscovery) Register(_ context.Context, id string) (mcpcontrol.Snapshot, error) {
	f.lastID = id
	return mcpcontrol.Snapshot{ID: id, Name: "Discovered"}, nil
}

func TestMCPDiscoveryReturnsTypedSourceAndServerMetadata(t *testing.T) {
	discovery := &fakeMCPDiscovery{snapshot: mcpdiscovery.Snapshot{
		ScannedAt: time.Now().UTC(),
		Sources: []mcpdiscovery.Source{{
			Client: "codex",
			Scope:  "global",
			Tags:   []string{"global", "codex"},
			State:  mcpdiscovery.SourceReady,
		}},
		Servers: []mcpdiscovery.Server{{
			ID:        "codex:global:termcp",
			Name:      "termcp",
			Client:    "codex",
			Scope:     "global",
			Tags:      []string{"global", "codex"},
			Transport: "stdio",
			Enabled:   true,
			Loadable:  true,
			State:     mcpdiscovery.ServerReady,
		}},
	}}
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t), WithMCPDiscovery(discovery)).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/v1/mcp/discovery", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var snapshot mcpdiscovery.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Servers) != 1 || snapshot.Servers[0].ID != "codex:global:termcp" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestMCPDiscoveryRegistrationRequiresMutationControl(t *testing.T) {
	discovery := &fakeMCPDiscovery{}
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t), WithMCPDiscovery(discovery)).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodPost, "/v1/mcp/discovery/codex:global:termcp/register", nil),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
	if discovery.lastID != "" {
		t.Fatalf("registration ran without mutation control: %q", discovery.lastID)
	}
}

func TestMCPDiscoveryRegistrationUsesAuthenticatedFixedRoute(t *testing.T) {
	discovery := &fakeMCPDiscovery{}
	handler := NewHandler(
		runningRuntime(t),
		WithMCPDiscovery(discovery),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	request := httptest.NewRequest(http.MethodPost, "/v1/mcp/discovery/codex:global:termcp/register", nil)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", response.Code, response.Body.String())
	}
	if discovery.lastID != "codex:global:termcp" {
		t.Fatalf("registered ID = %q", discovery.lastID)
	}
}
