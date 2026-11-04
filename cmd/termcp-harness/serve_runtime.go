package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/coreservice"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
)

func runRuntimeApp(ctx context.Context, args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", "127.0.0.1:8787", "loopback address for the control API")
	mcpEndpoint := flags.String("mcp-endpoint", "", "optional loopback MCP Streamable HTTP endpoint")
	mcpConfigFile := flags.String("mcp-config-file", "", "JSON file containing additional MCP servers")
	mcpTransport := flags.String("mcp-transport", string(mcpcontrol.TransportStreamableHTTP), "MCP transport: streamable-http, sse, or stdio")
	mcpID := flags.String("mcp-id", "termcp-core", "MCP server ID for --mcp-endpoint")
	mcpName := flags.String("mcp-name", "Termcp Core", "MCP server display name for --mcp-endpoint")
	mcpEnable := flags.Bool("mcp-enable", true, "enable the configured MCP server after capabilities load")
	mcpAuthTokenFile := flags.String("mcp-auth-token-file", "", "file containing a bearer token for --mcp-endpoint")
	mcpStdioCommand := flags.String("mcp-stdio-command", "", "executable for the stdio MCP server")
	mcpStdioWorkDir := flags.String("mcp-stdio-workdir", "", "working directory for the stdio MCP server")
	mcpStdioArgs := &stringList{}
	flags.Var(mcpStdioArgs, "mcp-stdio-arg", "stdio MCP server argument; repeat for each argument")
	mcpReconnectMin := flags.Duration("mcp-reconnect-min", time.Second, "minimum MCP reconnect backoff")
	mcpReconnectMax := flags.Duration("mcp-reconnect-max", 30*time.Second, "maximum MCP reconnect backoff")
	mcpHealthInterval := flags.Duration("mcp-health-interval", 30*time.Second, "MCP health check interval")
	coreEndpoint := flags.String("core-endpoint", "", "Core HTTP origin for instance identity")
	coreBundleDir := flags.String("core-bundle", "", "directory containing manifest.json and a prebuilt Core binary")
	coreServiceProfile := flags.String("core-service-profile", "", "standalone Core service profile; defaults to the user profile when present")
	coreDataDir := flags.String("core-data-dir", "", "persistent data directory for bundled Core")
	coreLogPath := flags.String("core-log", "", "bundled Core log path")
	coreStartTimeout := flags.Duration("core-start-timeout", 20*time.Second, "bundled Core readiness timeout")
	coreNoInternal := flags.Bool("core-no-internal", false, "disable bundled Core's built-in loopback SSH profile")
	approvalTokenFile := flags.String("approval-token-file", "", "file containing a bearer token required for approval decisions")
	approvalStoreFile := flags.String("approval-store-file", "", "optional JSON file for persisted approval history")
	toolPolicyFile := flags.String("tool-policy-file", "", "JSON file containing exact MCP tool allowlist rules")
	modelEndpoint := flags.String("model-endpoint", "", "OpenAI-compatible chat completions endpoint")
	modelID := flags.String("model-id", "", "model ID sent to the compatible endpoint")
	modelTokenFile := flags.String("model-token-file", "", "optional file containing the model bearer token")
	modelTimeout := flags.Duration("model-timeout", 60*time.Second, "model completion timeout")
	modelMaxOutputTokens := flags.Int("model-max-output-tokens", 1024, "model output token limit")
	eventLogFile := flags.String("event-log-file", "", "optional JSON file for the persisted bounded Event Journal")
	sessionStoreFile := flags.String("session-store-file", "", "optional JSON file for persisted Agent Sessions")
	recoveryManifestFile := flags.String("recovery-manifest-file", "", "optional JSON manifest for recovery consistency")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	recoveryStores := []string{*eventLogFile, *sessionStoreFile, *approvalStoreFile}
	recoveryStoreConfigured := false
	for _, path := range recoveryStores {
		if strings.TrimSpace(path) != "" {
			recoveryStoreConfigured = true
			break
		}
	}
	if strings.TrimSpace(*recoveryManifestFile) != "" && !recoveryStoreConfigured {
		return fmt.Errorf("--recovery-manifest-file requires at least one persistent store file")
	}

	profile, profileConfigured, err := coreservice.LoadProfile(*coreServiceProfile)
	if err != nil {
		return err
	}
	if !profileConfigured && strings.TrimSpace(*coreServiceProfile) != "" {
		return fmt.Errorf("Core service profile not found: %s", *coreServiceProfile)
	}
	if profileConfigured && (*coreEndpoint != "" || *coreBundleDir != "" || *mcpEndpoint != "" ||
		*mcpStdioCommand != "") {
		return fmt.Errorf("--core-service-profile cannot be combined with another Core or MCP endpoint")
	}
	config := runtimeapp.Config{
		Version:             version,
		ListenAddr:          *listenAddress,
		LogWriter:           stderr,
		MCPEndpoint:         *mcpEndpoint,
		MCPConfigFile:       *mcpConfigFile,
		MCPTransport:        mcpcontrol.TransportKind(*mcpTransport),
		MCPID:               *mcpID,
		MCPName:             *mcpName,
		MCPEnable:           *mcpEnable,
		MCPAuthTokenFile:    *mcpAuthTokenFile,
		MCPStdioCommand:     *mcpStdioCommand,
		MCPStdioWorkDir:     *mcpStdioWorkDir,
		MCPStdioArgs:        append([]string(nil), (*mcpStdioArgs)...),
		MCPReconnectMin:     *mcpReconnectMin,
		MCPReconnectMax:     *mcpReconnectMax,
		MCPHealthInterval:   *mcpHealthInterval,
		CoreEndpoint:        *coreEndpoint,
		CoreBundleDir:       *coreBundleDir,
		CoreDataDir:         *coreDataDir,
		CoreLogPath:         *coreLogPath,
		CoreStartTimeout:    *coreStartTimeout,
		CoreNoInternal:      *coreNoInternal,
		ApprovalTokenFile:   *approvalTokenFile,
		ApprovalStoreFile:   *approvalStoreFile,
		ToolPolicyFile:      *toolPolicyFile,
		ModelEndpoint:       *modelEndpoint,
		ModelID:             *modelID,
		ModelTokenFile:      *modelTokenFile,
		ModelTimeout:        *modelTimeout,
		ModelMaxOutputToken: *modelMaxOutputTokens,
		EventLogFile:        *eventLogFile,
		SessionStoreFile:    *sessionStoreFile,
		RecoveryManifest:    *recoveryManifestFile,
	}
	if profileConfigured {
		startContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = coreservice.Start(startContext, profile)
		cancel()
		if err != nil {
			return err
		}
		config.CoreEndpoint = profile.Endpoint
		config.MCPEndpoint = profile.Endpoint + "/stream"
		config.MCPAuthTokenFile = profile.AuthTokenFile
		config.DisableCoreBundleDiscovery = true
	}
	return runtimeapp.Run(ctx, config)
}
