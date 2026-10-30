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

	"github.com/open-mcp-ai/termcp-harness/internal/controlapi"
	"github.com/open-mcp-ai/termcp-harness/internal/corebundle"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/coreprocess"
	"github.com/open-mcp-ai/termcp-harness/internal/daemon"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	runtimepkg "github.com/open-mcp-ai/termcp-harness/internal/runtime"
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
	default:
		return usageError()
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
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	journal := eventlog.New()
	host := mcpcontrol.NewHost(mcpcontrol.WithHostEventLog(journal))
	coreManager := coreinstance.NewManager(journal)
	rt, err := runtimepkg.New(version, runtimepkg.WithEventLog(journal))
	if err != nil {
		return err
	}
	server, err := daemon.NewServer(*listenAddress, controlapi.NewHandler(
		rt,
		controlapi.WithEventLog(journal),
		controlapi.WithMCPRegistry(host),
		controlapi.WithCore(coreManager),
	), rt)
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
	return errors.New("usage: termcp-harness <serve|mcp|core|version>")
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
