package mcpcontrol

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

type Snapshotter interface {
	Snapshot() Snapshot
}

// Registry is the Harness-local directory of MCP servers. Registration does
// not imply connection, capability loading, enablement, or readiness.
type Registry struct {
	mu      sync.RWMutex
	servers map[string]Snapshotter
}

func NewRegistry() *Registry {
	return &Registry{servers: make(map[string]Snapshotter)}
}

func (r *Registry) Add(server Snapshotter) error {
	if server == nil {
		return errors.New("MCP server is required")
	}
	snapshot := server.Snapshot()
	if strings.TrimSpace(snapshot.ID) == "" {
		return errors.New("MCP server ID is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.servers[snapshot.ID]; exists {
		return errors.New("MCP server ID is already registered")
	}
	r.servers[snapshot.ID] = server
	return nil
}

func (r *Registry) Get(id string) (Snapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	server, ok := r.servers[id]
	if !ok {
		return Snapshot{}, false
	}
	return server.Snapshot(), true
}

func (r *Registry) List() []Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	servers := make([]Snapshot, 0, len(r.servers))
	for _, server := range r.servers {
		servers = append(servers, server.Snapshot())
	}
	sortSnapshots(servers)
	return servers
}

func sortSnapshots(servers []Snapshot) {
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].Name == servers[j].Name {
			return servers[i].ID < servers[j].ID
		}
		return servers[i].Name < servers[j].Name
	})
}
