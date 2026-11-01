package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/controlapi"
	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/coreapproval"
	"github.com/open-mcp-ai/termcp-harness/internal/corebundle"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/coreprocess"
	"github.com/open-mcp-ai/termcp-harness/internal/daemon"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	runtimepkg "github.com/open-mcp-ai/termcp-harness/internal/runtime"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "termcp-harness:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "serve":
		return runServe(ctx, args[1:], stderr)
	case "mcp":
		return runMCP(ctx, args[1:], stdout, stderr)
	case "core":
		return runCore(ctx, args[1:], stdout, stderr)
	case "approvals":
		return runApprovals(ctx, args[1:], stdout, stderr)
	case "agent":
		return runAgent(ctx, args[1:], stdout, stderr)
	default:
		return usageError()
	}
}

func runAgent(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: termcp-harness agent <run|loop|resume>")
	}
	if args[0] == "loop" {
		return runAgentLoop(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "resume" {
		return runAgentResume(ctx, args[1:], stdout, stderr)
	}
	if args[0] != "run" {
		return errors.New("usage: termcp-harness agent <run|loop|resume>")
	}
	flags := flag.NewFlagSet("agent run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "Agent Session ID")
	coreSessionID := flags.String("core-session-id", "", "optional trusted Core Session ID")
	stepsFile := flags.String("steps-file", "", "JSON file containing ordered Agent Session steps")
	tokenFile := flags.String("token-file", "", "file containing the control bearer token")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*stepsFile) == "" ||
		strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id, steps-file, and token-file are required")
	}
	steps, err := loadAgentSteps(*stepsFile)
	if err != nil {
		return err
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	if _, err := client.CreateAgentSession(ctx, *id, *coreSessionID, token); err != nil {
		return fmt.Errorf("create Agent Session: %w", err)
	}
	result, err := client.RunAgentSession(ctx, *id, steps, token)
	if err != nil {
		return fmt.Errorf("run Agent Session: %w", err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runAgentLoop(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("agent loop", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "Agent Session ID")
	coreSessionID := flags.String("core-session-id", "", "optional trusted Core Session ID")
	inputFile := flags.String("input-file", "", "file containing the user message for the model Agent Loop")
	tokenFile := flags.String("token-file", "", "file containing the control bearer token")
	maxIterations := flags.Int("max-iterations", 8, "maximum model turns")
	maxToolCalls := flags.Int("max-tool-calls", 16, "maximum total tool calls")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*inputFile) == "" ||
		strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id, input-file, and token-file are required")
	}
	input, err := readBoundedTextFile(*inputFile, 256<<10, "Agent Loop input")
	if err != nil {
		return err
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	if _, err := client.CreateAgentSession(ctx, *id, *coreSessionID, token); err != nil {
		return fmt.Errorf("create Agent Session: %w", err)
	}
	result, err := client.RunAgentLoop(
		ctx,
		*id,
		input,
		*maxIterations,
		*maxToolCalls,
		token,
	)
	if err != nil {
		return fmt.Errorf("run model Agent Loop: %w", err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runAgentResume(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("agent resume", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "Agent Session ID")
	tokenFile := flags.String("token-file", "", "file containing the control bearer token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id and token-file are required")
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	result, err := client.ResumeAgentLoop(ctx, *id, token)
	if err != nil {
		return fmt.Errorf("resume model Agent Loop: %w", err)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func readBoundedTextFile(path string, maxBytes int64, label string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s file: %w", label, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read %s file: %w", label, err)
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("%s file exceeds %d bytes", label, maxBytes)
	}
	return strings.TrimSpace(string(data)), nil
}

func loadAgentSteps(path string) ([]toolpipeline.Request, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open Agent steps file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, fmt.Errorf("read Agent steps file: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, errors.New("Agent steps file exceeds 1 MiB")
	}
	var document struct {
		Steps []toolpipeline.Request `json:"steps"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode Agent steps file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("Agent steps file contains multiple JSON values")
	}
	if len(document.Steps) == 0 || len(document.Steps) > 64 {
		return nil, errors.New("Agent steps file must contain between 1 and 64 steps")
	}
	return document.Steps, nil
}

func runApprovals(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: termcp-harness approvals <decide|refresh|reconcile>")
	}
	if args[0] == "refresh" {
		return runApprovalsRefresh(ctx, args[1:], stdout, stderr)
	}
	if args[0] == "reconcile" {
		return runApprovalsReconcile(ctx, args[1:], stdout, stderr)
	}
	if args[0] != "decide" {
		return errors.New("usage: termcp-harness approvals <decide|refresh|reconcile>")
	}
	flags := flag.NewFlagSet("approvals decide", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "approval request ID")
	revision := flags.Uint64("revision", 0, "observed approval revision")
	decisionText := flags.String("decision", "", "approve or reject")
	tokenFile := flags.String("token-file", "", "file containing the approval bearer token")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || *revision == 0 || strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id, revision, decision, and token-file are required")
	}
	decision, err := parseApprovalDecision(*decisionText)
	if err != nil {
		return err
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	result, err := client.DecideApproval(
		ctx,
		*id,
		approval.Revision(*revision),
		decision,
		token,
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runApprovalsRefresh(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := flag.NewFlagSet("approvals refresh", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "approval request ID")
	tokenFile := flags.String("token-file", "", "file containing the approval bearer token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id and token-file are required")
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	result, err := client.RefreshApprovalExecution(ctx, *id, token)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func runApprovalsReconcile(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	flags := flag.NewFlagSet("approvals reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "unknown approval request ID")
	tokenFile := flags.String("token-file", "", "file containing the approval bearer token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id and token-file are required")
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	result, err := client.ReconcileApproval(ctx, *id, token)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func parseApprovalDecision(raw string) (approval.Decision, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(approval.DecisionApprove):
		return approval.DecisionApprove, nil
	case string(approval.DecisionReject):
		return approval.DecisionReject, nil
	default:
		return approval.DecisionNone, errors.New("decision must be approve or reject")
	}
}

func runCore(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: termcp-harness core <identify|bundle>")
	}
	switch args[0] {
	case "identify":
		return runCoreIdentify(ctx, args[1:], stdout, stderr)
	case "bundle":
		return runCoreBundle(args[1:], stdout, stderr)
	default:
		return errors.New("usage: termcp-harness core <identify|bundle>")
	}
}

func runCoreIdentify(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("core identify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "http://127.0.0.1:18765", "Core HTTP origin")
	timeout := flags.Duration("timeout", 10*time.Second, "identity probe timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	normalized, err := coreinstance.ValidateEndpoint(*endpoint)
	if err != nil {
		return err
	}
	manager := coreinstance.NewManager(nil)
	probeContext, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	snapshot, err := manager.Bind(probeContext, coreinstance.Binding{
		Endpoint:  normalized,
		Ownership: coreinstance.OwnershipExternal,
	})
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(snapshot)
}

func runCoreBundle(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("core bundle", flag.ContinueOnError)
	flags.SetOutput(stderr)
	binary := flags.String("binary", "", "prebuilt Core executable to package")
	coreVersion := flags.String("version", "", "Core version reported by the bundled executable")
	out := flags.String("out", "", "bundle output directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	bundle, err := corebundle.Prepare(*binary, *coreVersion, *out)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(bundle.Manifest)
}

func runMCP(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "probe" {
		return errors.New("usage: termcp-harness mcp probe --endpoint <loopback-url>")
	}

	flags := flag.NewFlagSet("mcp probe", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("endpoint", "http://127.0.0.1:18765/stream", "loopback MCP streamable HTTP endpoint")
	timeout := flags.Duration("timeout", 10*time.Second, "probe timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	observation, err := mcpcontrol.NewProber().Probe(ctx, mcpcontrol.ProbeOptions{
		Endpoint:      *endpoint,
		ClientName:    "termcp-harness",
		ClientVersion: version,
		Timeout:       *timeout,
	})
	if err != nil {
		return err
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(observation)
}

func runServe(ctx context.Context, args []string, stderr io.Writer) error {
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
	coreEndpoint := flags.String("core-endpoint", "", "Core HTTP origin for instance identity; defaults to the origin of --mcp-endpoint")
	coreBundleDir := flags.String("core-bundle", "", "directory containing manifest.json and a prebuilt Core binary")
	coreDataDir := flags.String("core-data-dir", "", "persistent data directory for bundled Core")
	coreLogPath := flags.String("core-log", "", "bundled Core log path; defaults to <data-dir>/logs/core.log")
	coreStartTimeout := flags.Duration("core-start-timeout", 20*time.Second, "bundled Core readiness timeout")
	coreNoInternal := flags.Bool("core-no-internal", false, "disable bundled Core's built-in loopback SSH profile")
	approvalTokenFile := flags.String("approval-token-file", "", "file containing a bearer token required for approval decisions")
	approvalStoreFile := flags.String("approval-store-file", "", "optional JSON file for persisted approval history")
	toolPolicyFile := flags.String("tool-policy-file", "", "JSON file containing exact MCP tool allowlist rules")
	modelEndpoint := flags.String("model-endpoint", "", "OpenAI-compatible chat completions endpoint for the Agent Loop")
	modelID := flags.String("model-id", "", "model ID sent to the OpenAI-compatible endpoint")
	modelTokenFile := flags.String("model-token-file", "", "optional file containing the model bearer token")
	modelTimeout := flags.Duration("model-timeout", 60*time.Second, "model completion timeout")
	modelMaxOutputTokens := flags.Int("model-max-output-tokens", 1024, "model output token limit")
	eventLogFile := flags.String("event-log-file", "", "optional JSON file for the persisted bounded Event Journal")
	sessionStoreFile := flags.String("session-store-file", "", "optional JSON file for persisted Agent Sessions and model transcripts")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*toolPolicyFile) != "" && strings.TrimSpace(*approvalTokenFile) == "" {
		return errors.New("--tool-policy-file requires --approval-token-file for protected Agent Session execution")
	}
	if (strings.TrimSpace(*modelEndpoint) == "") != (strings.TrimSpace(*modelID) == "") {
		return errors.New("--model-endpoint and --model-id must be configured together")
	}
	if strings.TrimSpace(*modelEndpoint) != "" && strings.TrimSpace(*toolPolicyFile) == "" {
		return errors.New("--model-endpoint requires --tool-policy-file for the model Agent Loop")
	}
	if strings.TrimSpace(*modelTokenFile) != "" && strings.TrimSpace(*modelEndpoint) == "" {
		return errors.New("--model-token-file requires --model-endpoint")
	}
	if *modelTimeout <= 0 {
		return errors.New("--model-timeout must be positive")
	}
	if *modelMaxOutputTokens < 0 || *modelMaxOutputTokens > 32768 {
		return errors.New("--model-max-output-tokens must be between 0 and 32768")
	}
	if *mcpReconnectMin <= 0 || *mcpReconnectMax < *mcpReconnectMin || *mcpHealthInterval <= 0 {
		return errors.New("MCP reconnect and health intervals must be positive with max >= min")
	}
	transport := mcpcontrol.TransportKind(strings.TrimSpace(*mcpTransport))
	switch transport {
	case mcpcontrol.TransportStreamableHTTP, mcpcontrol.TransportSSE:
		if strings.TrimSpace(*mcpStdioCommand) != "" || len(*mcpStdioArgs) != 0 || strings.TrimSpace(*mcpStdioWorkDir) != "" {
			return errors.New("HTTP MCP transport cannot include stdio command settings")
		}
	case mcpcontrol.TransportStdio:
		if strings.TrimSpace(*mcpEndpoint) != "" || strings.TrimSpace(*mcpAuthTokenFile) != "" {
			return errors.New("stdio MCP transport cannot include --mcp-endpoint or --mcp-auth-token-file")
		}
		if strings.TrimSpace(*mcpStdioCommand) == "" {
			return errors.New("stdio MCP transport requires --mcp-stdio-command")
		}
	default:
		return fmt.Errorf("unsupported MCP transport %q", transport)
	}
	additionalMCPServers, err := loadMCPConfigFile(*mcpConfigFile)
	if err != nil {
		return err
	}

	var journal *eventlog.Journal
	if strings.TrimSpace(*eventLogFile) != "" {
		eventStore, storeErr := eventlog.NewFileStore(*eventLogFile)
		if storeErr != nil {
			return fmt.Errorf("configure Event Journal store: %w", storeErr)
		}
		journal, err = eventlog.NewWithPersistence(eventStore)
		if err != nil {
			return fmt.Errorf("load Event Journal store: %w", err)
		}
	} else {
		journal = eventlog.New()
	}
	host := mcpcontrol.NewHost(
		mcpcontrol.WithHostEventLog(journal),
		mcpcontrol.WithHostSupervision(*mcpReconnectMin, *mcpReconnectMax, *mcpHealthInterval),
	)
	coreManager := coreinstance.NewManager(journal)
	var agentSessions *agentsession.Manager
	if strings.TrimSpace(*sessionStoreFile) != "" {
		sessionStore, storeErr := agentsession.NewFileStore(*sessionStoreFile)
		if storeErr != nil {
			return fmt.Errorf("configure Agent Session store: %w", storeErr)
		}
		agentSessions, err = agentsession.NewManagerWithPersistence(journal, sessionStore)
	} else {
		agentSessions, err = agentsession.NewManager(journal)
	}
	if err != nil {
		return err
	}
	rt, err := runtimepkg.New(version, runtimepkg.WithEventLog(journal))
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	serveContext, cancelServe := context.WithCancel(ctx)
	defer cancelServe()

	bundleDir, bundleConfigured, err := corebundle.Discover(*coreBundleDir)
	if err != nil {
		return err
	}
	resolvedCoreEndpoint := strings.TrimSpace(*coreEndpoint)
	resolvedMCPEndpoint := strings.TrimSpace(*mcpEndpoint)
	mcpAuthToken := ""
	var bundledCore *coreprocess.Process
	if bundleConfigured && (resolvedCoreEndpoint != "" || resolvedMCPEndpoint != "" ||
		strings.TrimSpace(*mcpStdioCommand) != "") {
		return errors.New("--core-bundle cannot be combined with --core-endpoint or --mcp-endpoint")
	}
	if bundleConfigured {
		if transport != mcpcontrol.TransportStreamableHTTP {
			return errors.New("--core-bundle requires streamable-http MCP transport")
		}
		if strings.TrimSpace(*mcpAuthTokenFile) != "" {
			return errors.New("--mcp-auth-token-file cannot be combined with --core-bundle")
		}
		bundle, loadErr := corebundle.Load(bundleDir)
		if loadErr != nil {
			return fmt.Errorf("load bundled Core: %w", loadErr)
		}
		dataDir := strings.TrimSpace(*coreDataDir)
		if dataDir == "" {
			dataDir, err = coreprocess.DefaultDataDir()
			if err != nil {
				return err
			}
		}
		bundledCore, err = coreprocess.Start(serveContext, coreprocess.Config{
			BinaryPath:   bundle.BinaryPath,
			Version:      bundle.Manifest.Version,
			DataDir:      dataDir,
			LogPath:      strings.TrimSpace(*coreLogPath),
			NoInternal:   *coreNoInternal,
			StartTimeout: *coreStartTimeout,
		})
		if err != nil {
			return fmt.Errorf("start bundled Core: %w", err)
		}
		resolvedCoreEndpoint = bundledCore.Endpoint()
		resolvedMCPEndpoint = bundledCore.MCPEndpoint()
		mcpAuthToken = bundledCore.AuthToken()
		logger.Info("bundled Core started", "endpoint", resolvedCoreEndpoint, "pid", bundledCore.PID(), "version", bundledCore.Version(), "log_path", bundledCore.LogPath())
	}
	if strings.TrimSpace(*mcpAuthTokenFile) != "" {
		if resolvedMCPEndpoint == "" {
			return errors.New("--mcp-auth-token-file requires --mcp-endpoint")
		}
		mcpAuthToken, err = tokenfile.Read(*mcpAuthTokenFile)
		if err != nil {
			return fmt.Errorf("read MCP auth token: %w", err)
		}
	}
	coreAuthToken := ""
	if mcpAuthToken != "" && sameHTTPOrigin(resolvedCoreEndpoint, resolvedMCPEndpoint) {
		coreAuthToken = mcpAuthToken
	}
	defer func() {
		_ = host.Close()
		if bundledCore != nil {
			stopContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			if stopErr := bundledCore.Stop(stopContext); stopErr != nil {
				logger.Warn("bundled Core stop failed", "error", stopErr)
			}
		}
	}()

	if resolvedCoreEndpoint == "" && resolvedMCPEndpoint != "" {
		resolvedCoreEndpoint, err = coreOriginFromMCP(resolvedMCPEndpoint)
		if err != nil {
			return fmt.Errorf("derive Core endpoint: %w", err)
		}
	}
	if resolvedCoreEndpoint != "" {
		resolvedCoreEndpoint, err = coreinstance.ValidateEndpoint(resolvedCoreEndpoint)
		if err != nil {
			return fmt.Errorf("validate Core endpoint: %w", err)
		}
		if bundledCore != nil {
			identityContext, cancel := context.WithTimeout(serveContext, 15*time.Second)
			snapshot, bindErr := coreManager.Bind(identityContext, coreinstance.Binding{
				Endpoint:  resolvedCoreEndpoint,
				Ownership: coreinstance.OwnershipManaged,
				Managed: &coreinstance.ManagedProcess{
					PID:       bundledCore.PID(),
					StartedAt: bundledCore.StartedAt(),
				},
				AuthToken: mcpAuthToken,
			})
			cancel()
			if bindErr != nil {
				return fmt.Errorf("bind bundled Core identity: %w", bindErr)
			}
			logger.Info("bundled Core identity verified", "endpoint", snapshot.Endpoint, "pid", snapshot.PID, "ownership", snapshot.Ownership, "instance_key", snapshot.InstanceKey)
			go coreManager.Run(serveContext, 10*time.Second)
			go func() {
				select {
				case <-bundledCore.Done():
					if !bundledCore.Stopping() {
						if waitErr := bundledCore.WaitErr(); waitErr != nil {
							logger.Error("bundled Core exited unexpectedly", "error", waitErr)
						} else {
							logger.Warn("bundled Core exited unexpectedly")
						}
					}
					cancelServe()
				case <-serveContext.Done():
				}
			}()
		} else {
			go func() {
				identityContext, cancel := context.WithTimeout(serveContext, 15*time.Second)
				snapshot, bindErr := coreManager.Bind(identityContext, coreinstance.Binding{
					Endpoint:  resolvedCoreEndpoint,
					Ownership: coreinstance.OwnershipExternal,
					AuthToken: coreAuthToken,
				})
				cancel()
				if bindErr != nil {
					logger.Warn("Core identity unavailable", "endpoint", resolvedCoreEndpoint, "error", bindErr)
				} else {
					logger.Info("Core instance identified", "endpoint", snapshot.Endpoint, "pid", snapshot.PID, "ownership", snapshot.Ownership)
				}
				coreManager.Run(serveContext, 10*time.Second)
			}()
		}
	}

	controlOptions := []controlapi.Option{
		controlapi.WithEventLog(journal),
		controlapi.WithMCPRegistry(host),
		controlapi.WithCore(coreManager),
		controlapi.WithAgentSessions(agentSessions),
		controlapi.WithAgentSessionTranscripts(agentSessions),
	}
	var approvalCoordinator *approval.Coordinator
	if resolvedCoreEndpoint != "" {
		approvalClient, clientErr := coreapproval.NewClient(resolvedCoreEndpoint, coreAuthToken, nil)
		if clientErr != nil {
			return fmt.Errorf("create Core approval client: %w", clientErr)
		}
		coordinatorOptions := []approval.CoordinatorOption{
			approval.WithDecisionObserver(agentSessions),
		}
		if strings.TrimSpace(*approvalStoreFile) != "" {
			approvalStore, storeErr := approval.NewFileStore(*approvalStoreFile)
			if storeErr != nil {
				return fmt.Errorf("configure approval history store: %w", storeErr)
			}
			coordinatorOptions = append(coordinatorOptions, approval.WithPersistence(approvalStore))
		}
		var coordinatorErr error
		approvalCoordinator, coordinatorErr = approval.NewCoordinator(
			journal,
			approvalClient,
			coordinatorOptions...,
		)
		if coordinatorErr != nil {
			return fmt.Errorf("create approval coordinator: %w", coordinatorErr)
		}
		controlOptions = append(controlOptions, controlapi.WithApprovals(approvalCoordinator))
	} else if strings.TrimSpace(*approvalStoreFile) != "" {
		return errors.New("--approval-store-file requires a configured Core endpoint or bundle")
	}
	if strings.TrimSpace(*approvalTokenFile) != "" {
		token, tokenErr := readApprovalToken(*approvalTokenFile)
		if tokenErr != nil {
			return tokenErr
		}
		authorizer, authorizerErr := controlapi.NewApprovalAuthorizer(token)
		if authorizerErr != nil {
			return fmt.Errorf("load approval authorizer: %w", authorizerErr)
		}
		controlOptions = append(controlOptions, controlapi.WithApprovalAuthorizer(authorizer))
		if approvalCoordinator != nil {
			controlOptions = append(
				controlOptions,
				controlapi.WithApprovalDecider(approvalCoordinator),
				controlapi.WithApprovalExecutionRefresher(approvalCoordinator),
				controlapi.WithApprovalReconciler(approvalCoordinator),
			)
		}
	}
	var toolInvoker agentsession.Invoker
	var toolPolicy *toolpipeline.AllowlistPolicy
	mcpConfigured := resolvedMCPEndpoint != "" || transport == mcpcontrol.TransportStdio ||
		len(additionalMCPServers) > 0
	if strings.TrimSpace(*toolPolicyFile) != "" {
		if !mcpConfigured {
			return errors.New("--tool-policy-file requires a configured MCP server")
		}
		policy, policyErr := loadToolPolicy(*toolPolicyFile)
		if policyErr != nil {
			return policyErr
		}
		toolPolicy = policy
		options := []toolpipeline.Option{}
		if approvalCoordinator != nil {
			options = append(options, toolpipeline.WithApprovalTracker(approvalCoordinator))
		}
		pipeline, pipelineErr := toolpipeline.New(policy, host, journal, options...)
		if pipelineErr != nil {
			return fmt.Errorf("create tool pipeline: %w", pipelineErr)
		}
		toolInvoker = pipeline
	}
	controlOptions = append(controlOptions, controlapi.WithAgentSessionRunner(agentSessions, toolInvoker))
	if strings.TrimSpace(*modelEndpoint) != "" {
		modelToken := ""
		if strings.TrimSpace(*modelTokenFile) != "" {
			var tokenErr error
			modelToken, tokenErr = tokenfile.Read(*modelTokenFile)
			if tokenErr != nil {
				return fmt.Errorf("read model token: %w", tokenErr)
			}
		}
		adapter, adapterErr := model.NewOpenAICompatible(model.OpenAIConfig{
			Endpoint:        *modelEndpoint,
			Model:           *modelID,
			APIToken:        modelToken,
			HTTPClient:      &http.Client{Timeout: *modelTimeout},
			MaxOutputTokens: *modelMaxOutputTokens,
		})
		if adapterErr != nil {
			return fmt.Errorf("configure model adapter: %w", adapterErr)
		}
		controlOptions = append(controlOptions, controlapi.WithAgentModelLoop(
			agentSessions,
			adapter,
			&policyModelToolProvider{host: host, policy: toolPolicy},
		))
	}
	server, err := daemon.NewServer(
		*listenAddress,
		controlapi.NewHandler(rt, controlOptions...),
		rt,
	)
	if err != nil {
		return err
	}

	if mcpConfigured {
		legacyConfigured := resolvedMCPEndpoint != "" || transport == mcpcontrol.TransportStdio
		if legacyConfigured {
			if _, err := host.Register(mcpcontrol.Spec{
				ID:        *mcpID,
				Name:      *mcpName,
				Endpoint:  resolvedMCPEndpoint,
				Transport: transport,
				Command:   *mcpStdioCommand,
				Args:      append([]string(nil), (*mcpStdioArgs)...),
				WorkDir:   *mcpStdioWorkDir,
				AuthToken: mcpAuthToken,
			}); err != nil {
				return fmt.Errorf("register MCP server: %w", err)
			}
			if err := host.SetDesiredEnabled(*mcpID, *mcpEnable); err != nil {
				return fmt.Errorf("set MCP server desired state: %w", err)
			}
		}
		for _, configured := range additionalMCPServers {
			if _, err := host.Register(configured.Spec); err != nil {
				return fmt.Errorf("register MCP server %q: %w", configured.Spec.ID, err)
			}
			if err := host.SetDesiredEnabled(configured.Spec.ID, configured.Enabled); err != nil {
				return fmt.Errorf("set MCP server %q desired state: %w", configured.Spec.ID, err)
			}
		}
		go host.Run(serveContext)
	}
	logger.Info("starting runtime", "instance_id", rt.Snapshot().InstanceID, "listen", *listenAddress)
	if err := server.Run(serveContext); err != nil {
		return err
	}
	logger.Info("runtime stopped", "instance_id", rt.Snapshot().InstanceID)
	return nil
}

func usageError() error {
	return errors.New("usage: termcp-harness <serve|mcp|core|agent|approvals|version>")
}

type stringList []string

func (values *stringList) String() string {
	return strings.Join(*values, ",")
}

func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func coreOriginFromMCP(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse MCP endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("MCP endpoint must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("MCP endpoint host is required")
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func sameHTTPOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	return leftURL.Scheme != "" &&
		leftURL.Scheme == rightURL.Scheme &&
		leftURL.Host != "" &&
		leftURL.Host == rightURL.Host
}

func loadToolPolicy(path string) (*toolpipeline.AllowlistPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open tool policy file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, fmt.Errorf("read tool policy file: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, errors.New("tool policy file exceeds 1 MiB")
	}
	var document struct {
		Rules []toolpipeline.Rule `json:"rules"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode tool policy file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("tool policy file contains multiple JSON values")
	}
	if len(document.Rules) == 0 {
		return nil, errors.New("tool policy file requires at least one rule")
	}
	policy, err := toolpipeline.NewAllowlistPolicy(document.Rules...)
	if err != nil {
		return nil, fmt.Errorf("compile tool policy: %w", err)
	}
	return policy, nil
}

func readApprovalToken(path string) (string, error) {
	return tokenfile.Read(path)
}
