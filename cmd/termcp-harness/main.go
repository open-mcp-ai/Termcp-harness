package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/corebundle"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/coreservice"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/recovery"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

var version = "v0.1.10"

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
	case "recovery":
		return runRecovery(args[1:], stdout, stderr)
	default:
		return usageError()
	}
}

func runRecovery(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: termcp-harness recovery <status|rebaseline>")
	}
	switch args[0] {
	case "status":
		return runRecoveryStatus(args[1:], stdout, stderr)
	case "rebaseline":
		return runRecoveryRebaseline(args[1:], stdout, stderr)
	default:
		return errors.New("usage: termcp-harness recovery <status|rebaseline>")
	}
}

func runRecoveryStatus(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("recovery status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	config, err := parseRecoveryFlags(flags, args)
	if err != nil {
		return err
	}
	return encodeJSON(stdout, recovery.Inspect(config))
}

func runRecoveryRebaseline(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("recovery rebaseline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	confirm := flags.Bool("confirm", false, "confirm replacing the recovery baseline with the current readable store generation")
	config, err := parseRecoveryFlags(flags, args)
	if err != nil {
		return err
	}
	if !*confirm {
		return errors.New("recovery rebaseline requires --confirm")
	}
	fmt.Fprintln(stderr, "warning: recovery rebaseline records the current store generation; stop the Harness Runtime before running it")
	previous := recovery.Inspect(config)
	current, err := recovery.WriteManifest(config)
	if err != nil {
		return fmt.Errorf("rebaseline recovery manifest: %w", err)
	}
	return encodeJSON(stdout, struct {
		Previous recovery.Snapshot `json:"previous"`
		Current  recovery.Snapshot `json:"current"`
	}{
		Previous: previous,
		Current:  current,
	})
}

func parseRecoveryFlags(flags *flag.FlagSet, args []string) (recovery.Config, error) {
	manifest := flags.String("recovery-manifest-file", "", "JSON recovery manifest to inspect or replace")
	events := flags.String("event-log-file", "", "Event Journal Store")
	sessions := flags.String("session-store-file", "", "Agent Session Store")
	approvals := flags.String("approval-store-file", "", "Approval History Store")
	if err := flags.Parse(args); err != nil {
		return recovery.Config{}, err
	}
	if flags.NArg() != 0 {
		return recovery.Config{}, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*manifest) == "" {
		return recovery.Config{}, errors.New("recovery-manifest-file is required")
	}
	stores := map[recovery.StoreName]string{
		recovery.StoreEvents:    *events,
		recovery.StoreSessions:  *sessions,
		recovery.StoreApprovals: *approvals,
	}
	configured := 0
	for _, path := range stores {
		if strings.TrimSpace(path) != "" {
			configured++
		}
	}
	if configured == 0 {
		return recovery.Config{}, errors.New("recovery requires at least one persistent store file")
	}
	return recovery.Config{
		ManifestPath: strings.TrimSpace(*manifest),
		Stores:       stores,
	}, nil
}

func encodeJSON(destination io.Writer, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
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
	if args[0] == "cancel" {
		return runAgentCancel(ctx, args[1:], stdout, stderr)
	}
	if args[0] != "run" {
		return errors.New("usage: termcp-harness agent <run|loop|resume|cancel>")
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

func runAgentCancel(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("agent cancel", flag.ContinueOnError)
	flags.SetOutput(stderr)
	runtimeEndpoint := flags.String("runtime", "http://127.0.0.1:8787", "Harness Runtime control API origin")
	id := flags.String("id", "", "Agent Session ID")
	revision := flags.Uint64("revision", 0, "current Agent Session revision")
	tokenFile := flags.String("token-file", "", "file containing the control bearer token")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if strings.TrimSpace(*id) == "" || *revision == 0 || strings.TrimSpace(*tokenFile) == "" {
		return errors.New("id, revision, and token-file are required")
	}
	token, err := readApprovalToken(*tokenFile)
	if err != nil {
		return err
	}
	client, err := controlclient.New(*runtimeEndpoint)
	if err != nil {
		return err
	}
	result, err := client.CancelAgentSession(ctx, *id, *revision, token)
	if err != nil {
		return fmt.Errorf("cancel Agent Session: %w", err)
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
		return errors.New("usage: termcp-harness core <identify|bundle|service>")
	}
	switch args[0] {
	case "identify":
		return runCoreIdentify(ctx, args[1:], stdout, stderr)
	case "bundle":
		return runCoreBundle(args[1:], stdout, stderr)
	case "service":
		return runCoreService(ctx, args[1:], stdout, stderr)
	default:
		return errors.New("usage: termcp-harness core <identify|bundle|service>")
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

func runCoreService(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: termcp-harness core service <install|start|status|stop>")
	}
	defaultProfile, err := coreservice.DefaultProfilePath()
	if err != nil {
		return err
	}
	switch args[0] {
	case "install":
		flags := flag.NewFlagSet("core service install", flag.ContinueOnError)
		flags.SetOutput(stderr)
		binary := flags.String("binary", "", "prebuilt Core executable; defaults to the adjacent Core bundle")
		profilePath := flags.String("profile", defaultProfile, "Core service profile path")
		endpoint := flags.String("endpoint", coreservice.DefaultEndpoint(), "fixed loopback Core HTTP origin")
		dataDir := flags.String("data-dir", "", "persistent Core data directory; defaults to ~/.termcp")
		authTokenFile := flags.String("auth-token-file", "", "optional file containing the Core bearer token")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected arguments: %v", flags.Args())
		}
		binaryPath := strings.TrimSpace(*binary)
		if binaryPath == "" {
			bundleDir, configured, discoverErr := corebundle.Discover("")
			if discoverErr != nil {
				return discoverErr
			}
			if !configured {
				return errors.New("--binary is required when no adjacent Core bundle exists")
			}
			bundle, loadErr := corebundle.Load(bundleDir)
			if loadErr != nil {
				return loadErr
			}
			binaryPath = bundle.BinaryPath
		}
		if strings.TrimSpace(*dataDir) == "" {
			*dataDir, err = coreservice.DefaultDataDir()
			if err != nil {
				return err
			}
		}
		coreVersion, digest, err := coreservice.InspectBinary(ctx, binaryPath)
		if err != nil {
			return err
		}
		saved, err := coreservice.SaveProfile(*profilePath, coreservice.Profile{
			Schema:          coreservice.ProfileSchema,
			BinaryPath:      binaryPath,
			ExpectedVersion: coreVersion,
			BinarySHA256:    digest,
			Endpoint:        *endpoint,
			DataDir:         *dataDir,
			AuthTokenFile:   *authTokenFile,
		})
		if err != nil {
			return err
		}
		return encodeJSON(stdout, saved)
	case "start", "status", "stop":
		flags := flag.NewFlagSet("core service "+args[0], flag.ContinueOnError)
		flags.SetOutput(stderr)
		profilePath := flags.String("profile", defaultProfile, "Core service profile path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected arguments: %v", flags.Args())
		}
		profile, present, err := coreservice.LoadProfile(*profilePath)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("Core service profile not found: %s", *profilePath)
		}
		switch args[0] {
		case "start":
			snapshot, err := coreservice.Start(ctx, profile)
			if err != nil {
				return err
			}
			return encodeJSON(stdout, snapshot)
		case "status":
			snapshot, err := coreservice.Status(ctx, profile)
			if err != nil {
				return err
			}
			return encodeJSON(stdout, snapshot)
		default:
			if err := coreservice.Stop(ctx, profile); err != nil {
				return err
			}
			return encodeJSON(stdout, map[string]bool{"stopped": true})
		}
	default:
		return errors.New("usage: termcp-harness core service <install|start|status|stop>")
	}
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
	return runRuntimeApp(ctx, args, stderr)
}
func usageError() error {
	return errors.New("usage: termcp-harness <serve|mcp|core|agent|approvals|recovery|version>")
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
	return runtimeapp.CoreOriginFromMCP(raw)
}

func sameHTTPOrigin(left, right string) bool {
	return runtimeapp.SameHTTPOrigin(left, right)
}

func loadToolPolicy(path string) (*toolpipeline.AllowlistPolicy, error) {
	return runtimeapp.LoadToolPolicy(path)
}

func readApprovalToken(path string) (string, error) {
	return runtimeapp.ReadApprovalToken(path)
}
