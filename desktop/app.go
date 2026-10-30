package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	client        *controlclient.Client
	approvalToken string
}

func newApp(client *controlclient.Client, approvalToken string) *App {
	return &App{client: client, approvalToken: strings.TrimSpace(approvalToken)}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(context.Context) {
	a.approvalToken = ""
}

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

func (a *App) ApprovalControlAvailable() bool {
	return a.approvalToken != ""
}

func (a *App) DecideApproval(
	id string,
	revision uint64,
	decisionText string,
) (controlclient.ApprovalDecisionResult, error) {
	if a.approvalToken == "" {
		return controlclient.ApprovalDecisionResult{}, errors.New("approval control is not configured")
	}
	var decision approval.Decision
	switch strings.ToLower(strings.TrimSpace(decisionText)) {
	case string(approval.DecisionApprove):
		decision = approval.DecisionApprove
	case string(approval.DecisionReject):
		decision = approval.DecisionReject
	default:
		return controlclient.ApprovalDecisionResult{}, errors.New("decision must be approve or reject")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 25*time.Second)
	defer cancel()
	return a.client.DecideApproval(ctx, id, approval.Revision(revision), decision, a.approvalToken)
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
