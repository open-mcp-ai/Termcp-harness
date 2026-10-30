package coreprocess

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCoreArgs(t *testing.T) {
	args := coreArgs(Config{Host: "127.0.0.1", DataDir: `C:\data`, NoInternal: true}, 34567)
	for _, wanted := range []string{"--host", "127.0.0.1", "--port", "34567", "--data-dir", `C:\data`, "--idle-timeout", "0", "--no-internal"} {
		if !slices.Contains(args, wanted) {
			t.Fatalf("args = %v, missing %q", args, wanted)
		}
	}
}

func TestCoreEnvironmentFiltersCredentials(t *testing.T) {
	t.Setenv("TERMCP_AUTH_TOKEN", "inherited")
	t.Setenv("TERMCP_DAEMON_CHILD", "1")
	t.Setenv("TERMCP_DATA_DIR", "inherited-data")
	env := coreEnvironment("fresh-token")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "TERMCP_AUTH_TOKEN=inherited") || strings.Contains(joined, "TERMCP_DATA_DIR=inherited-data") {
		t.Fatalf("environment leaked inherited values: %v", env)
	}
	if !slices.Contains(env, "TERMCP_AUTH_TOKEN=fresh-token") || !slices.Contains(env, "TERMCP_DAEMON_CHILD=1") {
		t.Fatalf("environment = %v", env)
	}
}

func TestAllocateLoopbackPort(t *testing.T) {
	port, err := allocateLoopbackPort("127.0.0.1")
	if err != nil {
		t.Fatalf("allocateLoopbackPort() error = %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port = %d", port)
	}
}

func TestDefaultDataDir(t *testing.T) {
	dir, err := DefaultDataDir()
	if err != nil {
		t.Fatalf("DefaultDataDir() error = %v", err)
	}
	if !strings.Contains(filepath.Clean(dir), filepath.Join("termcp-harness", "core")) {
		t.Fatalf("DefaultDataDir() = %q", dir)
	}
}

func TestStartIntegration(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_CORE_BINARY"))
	if binary == "" {
		t.Skip("set TERMCP_HARNESS_CORE_BINARY to run the bundled Core integration test")
	}
	version := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_CORE_VERSION"))
	if version == "" {
		version = "v0.2.8"
	}
	process, err := Start(t.Context(), Config{
		BinaryPath: binary,
		Version:    version,
		DataDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if process.Endpoint() == "" || process.PID() <= 0 {
		t.Fatalf("process info = endpoint=%q pid=%d", process.Endpoint(), process.PID())
	}
	if err := process.Stop(t.Context()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
