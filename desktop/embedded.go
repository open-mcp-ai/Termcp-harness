package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/corebundle"
	"github.com/open-mcp-ai/termcp-harness/internal/coreservice"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
)

type embeddedRuntimeOptions struct {
	CoreBundleDir       string
	CoreServiceProfile  string
	CoreDataDir         string
	CoreNoInternal      bool
	MCPConfigFile       string
	ToolPolicyFile      string
	ModelEndpoint       string
	ModelID             string
	ModelTokenFile      string
	EventLogFile        string
	SessionStoreFile    string
	ApprovalStoreFile   string
	RecoveryManifest    string
	DesktopLogFile      string
	ModelMaxOutputToken int
}

func startEmbeddedRuntime(
	ctx context.Context,
	options embeddedRuntimeOptions,
) (*runtimeapp.App, string, error) {
	approvalToken, err := randomToken()
	if err != nil {
		return nil, "", err
	}
	root, err := desktopStateRoot()
	if err != nil {
		return nil, "", err
	}
	coreProfile, profileConfigured, err := coreservice.LoadProfile(options.CoreServiceProfile)
	if err != nil {
		return nil, "", err
	}
	if !profileConfigured && strings.TrimSpace(options.CoreServiceProfile) != "" {
		return nil, "", fmt.Errorf("Core service profile not found: %s", options.CoreServiceProfile)
	}
	if profileConfigured && (strings.TrimSpace(options.CoreBundleDir) != "" ||
		strings.TrimSpace(options.CoreDataDir) != "") {
		return nil, "", fmt.Errorf("Core service profile cannot be combined with --core-bundle or --core-data-dir")
	}
	if profileConfigured {
		startContext, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = coreservice.Start(startContext, coreProfile)
		cancel()
		if err != nil {
			return nil, "", fmt.Errorf("start standalone Core service: %w", err)
		}
	}
	bundleDir := ""
	bundleConfigured := false
	if !profileConfigured {
		bundleDir, bundleConfigured, err = corebundle.Discover(options.CoreBundleDir)
		if err != nil {
			return nil, "", err
		}
	}
	if !profileConfigured && !bundleConfigured {
		executable, executableErr := os.Executable()
		if executableErr != nil {
			return nil, "", fmt.Errorf("locate Desktop executable: %w", executableErr)
		}
		return nil, "", fmt.Errorf(
			"bundled Core was not found; place it in %q or pass --core-bundle",
			filepath.Join(filepath.Dir(executable), "core"),
		)
	}
	logPath := options.DesktopLogFile
	if logPath == "" {
		logPath = filepath.Join(root, "logs", "desktop.log")
	}
	logFile, err := openDesktopLog(logPath)
	if err != nil {
		return nil, "", err
	}
	logger := slog.New(slog.NewJSONHandler(logFile, nil))
	runtimeConfig := runtimeapp.Config{
		Version:             version,
		ListenAddr:          "127.0.0.1:0",
		Logger:              logger,
		MCPConfigFile:       options.MCPConfigFile,
		MCPID:               "termcp-core",
		MCPName:             "Termcp Core",
		MCPEnable:           true,
		MCPReconnectMin:     time.Second,
		MCPReconnectMax:     30 * time.Second,
		MCPHealthInterval:   30 * time.Second,
		CoreDataDir:         options.CoreDataDir,
		CoreNoInternal:      options.CoreNoInternal,
		ApprovalToken:       approvalToken,
		ApprovalStoreFile:   defaultPath(options.ApprovalStoreFile, filepath.Join(root, "approvals.json")),
		ToolPolicyFile:      options.ToolPolicyFile,
		ModelEndpoint:       options.ModelEndpoint,
		ModelID:             options.ModelID,
		ModelTokenFile:      options.ModelTokenFile,
		ModelMaxOutputToken: options.ModelMaxOutputToken,
		EventLogFile:        defaultPath(options.EventLogFile, filepath.Join(root, "events.json")),
		SessionStoreFile:    defaultPath(options.SessionStoreFile, filepath.Join(root, "sessions.json")),
		RecoveryManifest:    defaultPath(options.RecoveryManifest, filepath.Join(root, "recovery.json")),
		Cleanup:             func() { _ = logFile.Close() },
	}
	if profileConfigured {
		runtimeConfig.CoreEndpoint = coreProfile.Endpoint
		runtimeConfig.MCPEndpoint = coreProfile.Endpoint + "/stream"
		runtimeConfig.MCPAuthTokenFile = coreProfile.AuthTokenFile
		runtimeConfig.DisableCoreBundleDiscovery = true
	} else {
		runtimeConfig.CoreBundleDir = bundleDir
	}
	app, err := runtimeapp.Start(ctx, runtimeConfig)
	if err != nil {
		return nil, "", err
	}
	return app, approvalToken, nil
}

func desktopStateRoot() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve desktop state directory: %w", err)
	}
	return filepath.Join(root, "termcp-harness"), nil
}

func openDesktopLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create desktop log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open desktop log: %w", err)
	}
	return file, nil
}

func randomToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate approval token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func defaultPath(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
