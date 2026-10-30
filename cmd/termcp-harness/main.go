package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/controlapi"
	"github.com/open-mcp-ai/termcp-harness/internal/daemon"
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
	default:
		return usageError()
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
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listenAddress := flags.String("listen", "127.0.0.1:8787", "loopback address for the control API")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}

	rt, err := runtimepkg.New(version)
	if err != nil {
		return err
	}
	server, err := daemon.NewServer(*listenAddress, controlapi.NewHandler(rt), rt)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	logger.Info("starting runtime", "instance_id", rt.Snapshot().InstanceID, "listen", *listenAddress)
	if err := server.Run(ctx); err != nil {
		return err
	}
	logger.Info("runtime stopped", "instance_id", rt.Snapshot().InstanceID)
	return nil
}

func usageError() error {
	return errors.New("usage: termcp-harness <serve|mcp|version>")
}
