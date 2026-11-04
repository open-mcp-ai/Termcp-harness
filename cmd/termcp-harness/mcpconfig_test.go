package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
)

func TestLoadMCPConfigFileScopesCredentialsAndEnabledState(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server.token"), []byte("mcp-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := `{
	  "servers": [
	    {
	      "id": "external-http",
	      "name": "External HTTP",
	      "transport": "streamable-http",
	      "endpoint": "http://127.0.0.1:18888/stream",
	      "auth_token_file": "server.token"
	    },
	    {
	      "id": "external-stdio",
	      "name": "External Stdio",
	      "transport": "stdio",
	      "command": "external-mcp",
	      "args": ["--stdio"],
	      "workdir": "sandbox",
	      "enabled": false
	    }
	  ]
	}`
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, err := runtimeapp.LoadMCPConfigFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %+v", servers)
	}
	if servers[0].Spec.AuthToken != "mcp-token" || !servers[0].Enabled {
		t.Fatalf("HTTP server = %+v", servers[0])
	}
	if servers[1].Spec.Transport != mcpcontrol.TransportStdio || servers[1].Enabled {
		t.Fatalf("stdio server = %+v", servers[1])
	}
	if servers[1].Spec.WorkDir != filepath.Join(dir, "sandbox") {
		t.Fatalf("workdir = %q", servers[1].Spec.WorkDir)
	}
}

func TestLoadMCPConfigRejectsDuplicateIDsAndUnknownFields(t *testing.T) {
	tests := []string{
		`{"servers":[{"id":"a","name":"A","transport":"stdio","command":"a"},{"id":"a","name":"B","transport":"stdio","command":"b"}]}`,
		`{"servers":[{"id":"a","name":"A","transport":"stdio","command":"a","token":"secret"}]}`,
	}
	for _, document := range tests {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := runtimeapp.LoadMCPConfigFile(path); err == nil {
			t.Fatalf("config was accepted: %s", document)
		}
	}
}
