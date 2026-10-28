package main

import (
	"context"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx    context.Context
	client *controlclient.Client
}

func newApp(client *controlclient.Client) *App {
	return &App{client: client}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(context.Context) {}

func (a *App) Bootstrap() (controlclient.Bootstrap, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	return a.client.Bootstrap(ctx)
}

func (a *App) Events(after uint64, limit int) (eventlog.Window, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
	defer cancel()
	return a.client.Events(ctx, eventlog.Sequence(after), limit)
}

func (a *App) RuntimeEndpoint() string {
	return a.client.Endpoint()
}

func (a *App) WindowMinimise() {
	runtime.WindowMinimise(a.ctx)
}

func (a *App) WindowToggleMaximise() {
	runtime.WindowToggleMaximise(a.ctx)
}

func (a *App) WindowClose() {
	runtime.Quit(a.ctx)
}
