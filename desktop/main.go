package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	defaultEndpoint := os.Getenv("TERMCP_HARNESS_CONTROL")
	if defaultEndpoint == "" {
		defaultEndpoint = "http://127.0.0.1:8787"
	}
	endpoint := flag.String("runtime", defaultEndpoint, "loopback Harness Runtime control endpoint")
	flag.Parse()

	client, err := controlclient.New(*endpoint)
	if err != nil {
		fmt.Fprintln(os.Stderr, "termcp-harness-desktop:", err)
		os.Exit(1)
	}
	app := newApp(client)

	err = wails.Run(&options.App{
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
		AssetServer: &assetserver.Options{Assets: assets},
		OnStartup:   app.startup,
		OnShutdown:  app.shutdown,
		Bind:        []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "ai.openmcp.termcp.harness.workspace",
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "termcp-harness-desktop:", err)
	}
}
