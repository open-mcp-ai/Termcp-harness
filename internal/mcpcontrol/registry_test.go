package mcpcontrol

import (
	"testing"
)

func TestRegistryKeepsMCPLifecycleIndependent(t *testing.T) {
	registry := NewRegistry()
	server := newServer(t, ManagementExternal)
	if err := registry.Add(server); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := registry.Add(server); err == nil {
		t.Fatal("duplicate Add() error = nil")
	}

	list := registry.List()
	if len(list) != 1 {
		t.Fatalf("List() = %+v", list)
	}
	if list[0].Registration != RegistrationDiscovered || list[0].Connection != ConnectionNotAttempted || list[0].Ready {
		t.Fatalf("registered server implied readiness: %+v", list[0])
	}
}

func TestRegistrySortsByDisplayName(t *testing.T) {
	registry := NewRegistry()
	for _, spec := range []Spec{
		{ID: "z", Name: "Zulu", Endpoint: "http://127.0.0.1:1/stream"},
		{ID: "a", Name: "Alpha", Endpoint: "http://127.0.0.1:2/stream"},
	} {
		server, err := New(spec)
		if err != nil {
			t.Fatalf("New(%+v) error = %v", spec, err)
		}
		if err := registry.Add(server); err != nil {
			t.Fatalf("Add(%s) error = %v", spec.ID, err)
		}
	}
	list := registry.List()
	if len(list) != 2 || list[0].Name != "Alpha" || list[1].Name != "Zulu" {
		t.Fatalf("List() = %+v", list)
	}
}
