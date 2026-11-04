package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

const version = "v0.1.7"

func main() {
	endpoint := flag.String(
		"runtime",
		os.Getenv("TERMCP_HARNESS_CONTROL"),
		"optional external loopback Harness Runtime endpoint; empty starts the embedded Runtime",
	)
	approvalTokenFile := flag.String(
		"approval-token-file",
		os.Getenv("TERMCP_HARNESS_APPROVAL_TOKEN_FILE"),
		"optional approval bearer token file when using an external Runtime",
	)
	coreBundle := flag.String("core-bundle", "", "optional bundled Core directory; defaults to <desktop-dir>/core")
	coreDataDir := flag.String("core-data-dir", "", "optional bundled Core data directory")
	coreNoInternal := flag.Bool("core-no-internal", false, "disable bundled Core's built-in loopback SSH profile")
	mcpConfigFile := flag.String("mcp-config-file", "", "optional additional MCP server configuration file")
	toolPolicyFile := flag.String("tool-policy-file", "", "optional exact MCP tool policy file")
	modelEndpoint := flag.String("model-endpoint", "", "optional OpenAI-compatible model endpoint")
	modelID := flag.String("model-id", "", "optional model ID")
	modelTokenFile := flag.String("model-token-file", "", "optional model bearer token file")
	modelMaxOutputTokens := flag.Int("model-max-output-tokens", 1024, "model output token limit")
	eventLogFile := flag.String("event-log-file", "", "optional Event Journal path")
	sessionStoreFile := flag.String("session-store-file", "", "optional Agent Session store path")
	approvalStoreFile := flag.String("approval-store-file", "", "optional Approval history store path")
	recoveryManifestFile := flag.String("recovery-manifest-file", "", "optional recovery manifest path")
	desktopLogFile := flag.String("desktop-log", "", "optional Desktop and embedded Runtime log path")
	flag.Parse()

	endpointValue := strings.TrimSpace(*endpoint)
	var app *App
	if endpointValue == "" {
		options := embeddedRuntimeOptions{
			CoreBundleDir:       *coreBundle,
			CoreDataDir:         *coreDataDir,
			CoreNoInternal:      *coreNoInternal,
			MCPConfigFile:       *mcpConfigFile,
			ToolPolicyFile:      *toolPolicyFile,
			ModelEndpoint:       *modelEndpoint,
			ModelID:             *modelID,
			ModelTokenFile:      *modelTokenFile,
			ModelMaxOutputToken: *modelMaxOutputTokens,
			EventLogFile:        *eventLogFile,
			SessionStoreFile:    *sessionStoreFile,
			ApprovalStoreFile:   *approvalStoreFile,
			RecoveryManifest:    *recoveryManifestFile,
			DesktopLogFile:      *desktopLogFile,
		}
		app = newPendingApp(func(ctx context.Context) (*controlclient.Client, string, *runtimeapp.App, error) {
			ownedRuntime, approvalToken, err := startEmbeddedRuntime(ctx, options)
			if err != nil {
				return nil, "", nil, err
			}
			client, err := controlclient.New(ownedRuntime.Endpoint())
			if err != nil {
				closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = ownedRuntime.Close(closeContext)
				cancel()
				return nil, "", nil, err
			}
			return client, approvalToken, ownedRuntime, nil
		})
	} else {
		approvalToken := ""
		if strings.TrimSpace(*approvalTokenFile) != "" {
			var err error
			approvalToken, err = tokenfile.Read(*approvalTokenFile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "termcp-harness-desktop:", err)
				os.Exit(1)
			}
		}
		client, err := controlclient.New(endpointValue)
		if err != nil {
			fmt.Fprintln(os.Stderr, "termcp-harness-desktop:", err)
			os.Exit(1)
		}
		app = newAppWithRuntime(client, approvalToken, nil)
	}

	dataPath := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_WEBVIEW_DATA_PATH"))
	if dataPath == "" {
		if stateRoot, stateErr := desktopStateRoot(); stateErr == nil {
			dataPath = filepath.Join(stateRoot, "webview")
		}
	}
	var windowsOptions *windows.Options
	if dataPath != "" {
		windowsOptions = &windows.Options{WebviewUserDataPath: dataPath}
	}

	err := wails.Run(&options.App{
		Title:       "Termcp Harness",
		Width:       1280,
		Height:      800,
		MinWidth:    900,
		MinHeight:   600,
		Frameless:   runtime.GOOS == "windows",
		StartHidden: false,
		BackgroundColour: &options.RGBA{
			R: 233,
			G: 239,
			B: 240,
			A: 1,
		},
		AssetServer:   &assetserver.Options{Assets: assets},
		Windows:       windowsOptions,
		OnStartup:     app.startup,
		OnShutdown:    app.shutdown,
		OnBeforeClose: app.beforeClose,
		Bind:          []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ai.openmcp.termcp.harness.workspace",
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "termcp-harness-desktop:", err)
	}
}
