package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
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
	if len(args) == 0 || args[0] != "run" {
		return errors.New("usage: termcp-harness agent run --id <session-id> --steps-file <path> --token-file <path>")
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
	if len(args) == 0 || args[0] != "decide" {
		return errors.New("usage: termcp-harness approvals decide --id <approval-id> --revision <n> --decision <approve|reject> --token-file <path>")
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
	mcpID := flags.String("mcp-id", "termcp-core", "MCP server ID for --mcp-endpoint")
	mcpName := flags.String("mcp-name", "Termcp Core", "MCP server display name for --mcp-endpoint")
	mcpEnable := flags.Bool("mcp-enable", true, "enable the configured MCP server after capabilities load")
	coreEndpoint := flags.String("core-endpoint", "", "Core HTTP origin for instance identity; defaults to the origin of --mcp-endpoint")
	coreBundleDir := flags.String("core-bundle", "", "directory containing manifest.json and a prebuilt Core binary")
	coreDataDir := flags.String("core-data-dir", "", "persistent data directory for bundled Core")
	coreLogPath := flags.String("core-log", "", "bundled Core log path; defaults to <data-dir>/logs/core.log")
	coreStartTimeout := flags.Duration("core-start-timeout", 20*time.Second, "bundled Core readiness timeout")
	coreNoInternal := flags.Bool("core-no-internal", false, "disable bundled Core's built-in loopback SSH profile")
	approvalTokenFile := flags.String("approval-token-file", "", "file containing a bearer token required for approval decisions")
	toolPolicyFile := flags.String("tool-policy-file", "", "JSON file containing exact MCP tool allowlist rules")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*toolPolicyFile) != "" && strings.TrimSpace(*approvalTokenFile) == "" {
		return errors.New("--tool-policy-file requires --approval-token-file for protected Agent Session execution")
	}

	journal := eventlog.New()
	host := mcpcontrol.NewHost(mcpcontrol.WithHostEventLog(journal))
	coreManager := coreinstance.NewManager(journal)
	agentSessions, err := agentsession.NewManager(journal)
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
	if bundleConfigured && (resolvedCoreEndpoint != "" || resolvedMCPEndpoint != "") {
		return errors.New("--core-bundle cannot be combined with --core-endpoint or --mcp-endpoint")
	}
	if bundleConfigured {
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
	}
	var approvalCoordinator *approval.Coordinator
	if resolvedCoreEndpoint != "" {
		approvalClient, clientErr := coreapproval.NewClient(resolvedCoreEndpoint, mcpAuthToken, nil)
		if clientErr != nil {
			return fmt.Errorf("create Core approval client: %w", clientErr)
		}
		var coordinatorErr error
		approvalCoordinator, coordinatorErr = approval.NewCoordinator(
			journal,
			approvalClient,
			approval.WithDecisionObserver(agentSessions),
		)
		if coordinatorErr != nil {
			return fmt.Errorf("create approval coordinator: %w", coordinatorErr)
		}
		controlOptions = append(controlOptions, controlapi.WithApprovals(approvalCoordinator))
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
			controlOptions = append(controlOptions, controlapi.WithApprovalDecider(approvalCoordinator))
		}
	}
	var toolInvoker agentsession.Invoker
	if strings.TrimSpace(*toolPolicyFile) != "" {
		if resolvedMCPEndpoint == "" {
			return errors.New("--tool-policy-file requires an MCP endpoint")
		}
		policy, policyErr := loadToolPolicy(*toolPolicyFile)
		if policyErr != nil {
			return policyErr
		}
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
	server, err := daemon.NewServer(
		*listenAddress,
		controlapi.NewHandler(rt, controlOptions...),
		rt,
	)
	if err != nil {
		return err
	}

	if resolvedMCPEndpoint != "" {
		if _, err := host.Register(mcpcontrol.Spec{
			ID:        *mcpID,
			Name:      *mcpName,
			Endpoint:  resolvedMCPEndpoint,
			AuthToken: mcpAuthToken,
		}); err != nil {
			return fmt.Errorf("register MCP server: %w", err)
		}
		go func() {
			mcpContext, cancel := context.WithTimeout(serveContext, 35*time.Second)
			defer cancel()
			if err := host.Connect(mcpContext, *mcpID); err != nil {
				logger.Error("MCP server connection failed", "id", *mcpID, "error", err)
				return
			}
			if err := host.LoadCapabilities(mcpContext, *mcpID); err != nil {
				logger.Error("MCP capability load failed", "id", *mcpID, "error", err)
				return
			}
			if !*mcpEnable {
				logger.Info("MCP server connected but disabled", "id", *mcpID)
				return
			}
			if _, err := host.Enable(*mcpID); err != nil {
				logger.Error("MCP server enable failed", "id", *mcpID, "error", err)
				return
			}
			logger.Info("MCP server ready", "id", *mcpID)
		}()
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
