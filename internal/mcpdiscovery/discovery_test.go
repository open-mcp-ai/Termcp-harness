package mcpdiscovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestClassifyCodexServersKeepsSecretsOutOfSnapshot(t *testing.T) {
	command := filepath.Join(t.TempDir(), "fake-mcp.exe")
	if err := os.WriteFile(command, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAFE_ENV", "secret-environment-value")
	t.Setenv("BEARER_TOKEN", "secret-bearer-token")

	entries := []codexServer{
		{
			Name:    "local",
			Enabled: true,
			Transport: codexTransport{
				Type:    "stdio",
				Command: command,
				Args:    []string{"serve", "--secret-argument"},
			},
			AuthStatus: "unsupported",
		},
		{
			Name:    "remote",
			Enabled: true,
			Transport: codexTransport{
				Type: "streamable_http",
				URL:  "https://example.com/mcp",
			},
		},
		{
			Name:    "disabled",
			Enabled: false,
			Transport: codexTransport{
				Type:    "stdio",
				Command: command,
			},
		},
		{
			Name:    "literal-env",
			Enabled: true,
			Transport: codexTransport{
				Type:    "stdio",
				Command: command,
				Env:     map[string]string{"TOKEN": "secret-literal-value"},
			},
		},
	}
	lookPath := func(string) (string, error) { return command, nil }
	snapshot := Snapshot{ScannedAt: time.Now().UTC(), Sources: []Source{}, Servers: []Server{}}
	specs := make(map[string]struct{})
	for _, entry := range entries {
		server, _, ok := classifyCodexServer(entry, lookPath)
		if !ok {
			t.Fatalf("entry %q was ignored", entry.Name)
		}
		snapshot.Servers = append(snapshot.Servers, server)
		specs[server.ID] = struct{}{}
	}
	if snapshot.Servers[0].State != ServerReady || !snapshot.Servers[0].Loadable {
		t.Fatalf("local server = %+v", snapshot.Servers[0])
	}
	if snapshot.Servers[1].State != ServerUnsupported {
		t.Fatalf("remote server = %+v", snapshot.Servers[1])
	}
	if snapshot.Servers[2].State != ServerDisabled {
		t.Fatalf("disabled server = %+v", snapshot.Servers[2])
	}
	if snapshot.Servers[3].State != ServerNeedsConfiguration {
		t.Fatalf("literal env server = %+v", snapshot.Servers[3])
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-environment-value", "secret-bearer-token", "secret-literal-value", "--secret-argument"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("snapshot leaked %q: %s", secret, encoded)
		}
	}
}

func TestClassifyCodexServerResolvesForwardedEnvironment(t *testing.T) {
	command := filepath.Join(t.TempDir(), "fake-mcp.exe")
	if err := os.WriteFile(command, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORWARDED_VALUE", "inside-live-spec")
	server, spec, ok := classifyCodexServer(codexServer{
		Name:    "forwarded",
		Enabled: true,
		Transport: codexTransport{
			Type:    "stdio",
			Command: command,
			EnvVars: []string{"FORWARDED_VALUE"},
		},
	}, func(string) (string, error) { return command, nil })
	if !ok || !server.Loadable || server.State != ServerReady {
		t.Fatalf("server = %+v, ok = %t", server, ok)
	}
	if len(spec.Env) != 1 || spec.Env[0] != "FORWARDED_VALUE=inside-live-spec" {
		t.Fatalf("spec env was not resolved internally: %#v", spec.Env)
	}
	encoded, err := json.Marshal(server)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "inside-live-spec") {
		t.Fatalf("public server leaked environment value: %s", encoded)
	}
}

func TestPrepareWindowsCommandResolvesNPMShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows command shims only apply on Windows")
	}
	root := t.TempDir()
	command := filepath.Join(root, "termcp.cmd")
	script := filepath.Join(root, "node_modules", "@open-mcp-ai", "termcp", "bin", "termcp.js")
	if err := os.MkdirAll(filepath.Dir(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("process.exit(0)"), 0o600); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(root, "node.exe")
	if err := os.WriteFile(node, []byte("test"), 0o700); err != nil {
		t.Fatal(err)
	}
	shim := `@ECHO off` + "\r\n" +
		`endLocal & "%_prog%" "%dp0%\node_modules\@open-mcp-ai\termcp\bin\termcp.js" %*`
	if err := os.WriteFile(command, []byte(shim), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, args, err := prepareStdioCommand(
		"termcp",
		[]string{"daemon", "stdio"},
		func(string) (string, error) { return command, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != node {
		t.Fatalf("resolved command = %q, want %q", resolved, node)
	}
	if len(args) != 3 || args[0] != script || args[1] != "daemon" || args[2] != "stdio" {
		t.Fatalf("resolved args = %#v", args)
	}
}
