// Package runtimeapp assembles the headless Harness Runtime for both the CLI
// and the Desktop. Desktop embeds this assembly in-process while Core remains
// a separately supervised process reached only through public contracts.
package runtimeapp

import (
	"io"
	"log/slog"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
)

type Config struct {
	Version    string
	ListenAddr string
	LogWriter  io.Writer
	Logger     *slog.Logger
	Cleanup    func()

	MCPEndpoint         string
	MCPConfigFile       string
	MCPTransport        mcpcontrol.TransportKind
	MCPID               string
	MCPName             string
	MCPEnable           bool
	MCPAuthTokenFile    string
	MCPStdioCommand     string
	MCPStdioWorkDir     string
	MCPStdioArgs        []string
	MCPReconnectMin     time.Duration
	MCPReconnectMax     time.Duration
	MCPHealthInterval   time.Duration
	CoreEndpoint        string
	CoreBundleDir       string
	CoreDataDir         string
	CoreLogPath         string
	CoreStartTimeout    time.Duration
	CoreNoInternal      bool
	ApprovalTokenFile   string
	ApprovalToken       string
	ApprovalStoreFile   string
	ToolPolicyFile      string
	ModelEndpoint       string
	ModelID             string
	ModelTokenFile      string
	ModelTimeout        time.Duration
	ModelMaxOutputToken int
	EventLogFile        string
	SessionStoreFile    string
	RecoveryManifest    string
}

func (c Config) defaults() Config {
	if c.Version == "" {
		c.Version = "dev"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:8787"
	}
	if c.MCPTransport == "" {
		c.MCPTransport = mcpcontrol.TransportStreamableHTTP
	}
	if c.MCPID == "" {
		c.MCPID = "termcp-core"
	}
	if c.MCPName == "" {
		c.MCPName = "Termcp Core"
	}
	if c.MCPReconnectMin <= 0 {
		c.MCPReconnectMin = time.Second
	}
	if c.MCPReconnectMax <= 0 {
		c.MCPReconnectMax = 30 * time.Second
	}
	if c.MCPHealthInterval <= 0 {
		c.MCPHealthInterval = 30 * time.Second
	}
	if c.CoreStartTimeout <= 0 {
		c.CoreStartTimeout = 20 * time.Second
	}
	if c.ModelTimeout <= 0 {
		c.ModelTimeout = 60 * time.Second
	}
	if c.ModelMaxOutputToken == 0 {
		c.ModelMaxOutputToken = 1024
	}
	return c
}
