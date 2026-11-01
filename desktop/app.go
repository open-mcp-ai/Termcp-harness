package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/coreterminal"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/eventstream"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	client        *controlclient.Client
	terminals     *coreterminal.Manager
	eventMu       sync.Mutex
	eventStream   *eventstream.Subscription
	approvalToken string
}

func newApp(client *controlclient.Client, approvalToken string) *App {
	return &App{
		client:        client,
		terminals:     coreterminal.NewManager(),
		approvalToken: strings.TrimSpace(approvalToken),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) shutdown(context.Context) {
	a.approvalToken = ""
	if a.terminals != nil {
		a.terminals.Shutdown()
	}
	a.eventMu.Lock()
	if a.eventStream != nil {
		a.eventStream.Close()
		a.eventStream = nil
	}
	a.eventMu.Unlock()
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

func (a *App) OpenEventStream(after uint64) (eventstream.Snapshot, error) {
	if a.client == nil {
		return eventstream.Snapshot{}, errors.New("control client is required")
	}
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	if a.eventStream != nil {
		a.eventStream.Close()
	}
	subscription, err := eventstream.Open(parent, a.client.Endpoint(), eventlog.Sequence(after))
	if err != nil {
		a.eventStream = nil
		return eventstream.Snapshot{}, err
	}
	a.eventStream = subscription
	return subscription.Snapshot(), nil
}

func (a *App) PollEventStream() (eventstream.PollResult, error) {
	a.eventMu.Lock()
	subscription := a.eventStream
	a.eventMu.Unlock()
	if subscription == nil {
		return eventstream.PollResult{
			Snapshot: eventstream.Snapshot{State: eventstream.StateStopped},
			Events:   []eventlog.Event{},
		}, nil
	}
	return subscription.Poll(), nil
}

func (a *App) CloseEventStream() {
	a.eventMu.Lock()
	if a.eventStream != nil {
		a.eventStream.Close()
		a.eventStream = nil
	}
	a.eventMu.Unlock()
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

func (a *App) RefreshApprovalExecution(
	id string,
) (controlclient.ApprovalRefreshResult, error) {
	if a.approvalToken == "" {
		return controlclient.ApprovalRefreshResult{}, errors.New("approval control is not configured")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 25*time.Second)
	defer cancel()
	return a.client.RefreshApprovalExecution(ctx, id, a.approvalToken)
}

func (a *App) ReconcileApproval(
	id string,
) (controlclient.ApprovalReconciliationResult, error) {
	if a.approvalToken == "" {
		return controlclient.ApprovalReconciliationResult{}, errors.New("approval control is not configured")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 25*time.Second)
	defer cancel()
	return a.client.ReconcileApproval(ctx, id, a.approvalToken)
}

func (a *App) AgentSessionTranscript(
	id string,
) (controlclient.AgentSessionTranscriptEnvelope, error) {
	if a.approvalToken == "" {
		return controlclient.AgentSessionTranscriptEnvelope{}, errors.New("Agent Session transcript is not configured")
	}
	ctx, cancel := a.operationContext(20 * time.Second)
	defer cancel()
	return a.client.AgentSessionTranscript(ctx, id, a.approvalToken)
}

type ModelLoopLaunchResult struct {
	AgentSessionID string                           `json:"agent_session_id"`
	Session        agentsession.Snapshot            `json:"session"`
	Output         string                           `json:"output,omitempty"`
	Calls          []controlclient.AgentCallSummary `json:"calls"`
}

// StartModelLoop creates one new Agent Session and runs the protected model
// loop. It is a fixed bridge: callers cannot choose endpoints, tools, or a
// generic HTTP target.
func (a *App) StartModelLoop(
	coreSessionID string,
	input string,
	maxIterations int,
	maxToolCalls int,
) (ModelLoopLaunchResult, error) {
	if a.client == nil {
		return ModelLoopLaunchResult{}, errors.New("control client is required")
	}
	if a.approvalToken == "" {
		return ModelLoopLaunchResult{}, errors.New("model loop control is not configured")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return ModelLoopLaunchResult{}, errors.New("model input is required")
	}
	if len(input) > 256<<10 {
		return ModelLoopLaunchResult{}, errors.New("model input exceeds 256 KiB")
	}
	if maxIterations < 1 || maxIterations > 32 {
		return ModelLoopLaunchResult{}, errors.New("max iterations must be between 1 and 32")
	}
	if maxToolCalls < 1 || maxToolCalls > 64 {
		return ModelLoopLaunchResult{}, errors.New("max tool calls must be between 1 and 64")
	}
	coreSessionID = strings.TrimSpace(coreSessionID)
	if len(coreSessionID) > 256 {
		return ModelLoopLaunchResult{}, errors.New("Core Session ID exceeds 256 characters")
	}
	agentSessionID, err := newDesktopAgentSessionID()
	if err != nil {
		return ModelLoopLaunchResult{}, err
	}
	ctx, cancel := a.operationContext(5*time.Minute + 30*time.Second)
	defer cancel()
	if _, err := a.client.CreateAgentSession(ctx, agentSessionID, coreSessionID, a.approvalToken); err != nil {
		return ModelLoopLaunchResult{AgentSessionID: agentSessionID}, fmt.Errorf("create Agent Session: %w", err)
	}
	loop, loopErr := a.client.RunAgentLoop(
		ctx,
		agentSessionID,
		input,
		maxIterations,
		maxToolCalls,
		a.approvalToken,
	)
	return ModelLoopLaunchResult{
		AgentSessionID: agentSessionID,
		Session:        loop.Session,
		Output:         loop.Output,
		Calls:          loop.Calls,
	}, loopErr
}

// ResumeModelLoop claims one safe Runtime-restarted model checkpoint through a
// fixed bridge. Callers cannot replace the Runtime's model endpoint, credential,
// tool policy, or tool invoker.
func (a *App) ResumeModelLoop(agentSessionID string) (ModelLoopLaunchResult, error) {
	if a.client == nil {
		return ModelLoopLaunchResult{}, errors.New("control client is required")
	}
	if a.approvalToken == "" {
		return ModelLoopLaunchResult{}, errors.New("model loop control is not configured")
	}
	agentSessionID = strings.TrimSpace(agentSessionID)
	if agentSessionID == "" || len(agentSessionID) > 256 {
		return ModelLoopLaunchResult{}, errors.New("Agent Session ID is required")
	}
	ctx, cancel := a.operationContext(5*time.Minute + 30*time.Second)
	defer cancel()
	loop, err := a.client.ResumeAgentLoop(ctx, agentSessionID, a.approvalToken)
	return ModelLoopLaunchResult{
		AgentSessionID: agentSessionID,
		Session:        loop.Session,
		Output:         loop.Output,
		Calls:          loop.Calls,
	}, err
}

func newDesktopAgentSessionID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create Agent Session ID: %w", err)
	}
	return "desktop-" + hex.EncodeToString(value[:]), nil
}

type TerminalShellsResult struct {
	AgentSessionID string               `json:"agent_session_id"`
	CoreSessionID  string               `json:"core_session_id"`
	Shells         []coreterminal.Shell `json:"shells"`
}

func (a *App) TerminalShells(agentSessionID string) (TerminalShellsResult, error) {
	ctx, cancel := a.operationContext(8 * time.Second)
	defer cancel()
	target, shells, err := a.terminalTarget(ctx, agentSessionID)
	if err != nil {
		return TerminalShellsResult{}, err
	}
	return TerminalShellsResult{
		AgentSessionID: target.session.Session.ID,
		CoreSessionID:  target.coreSessionID,
		Shells:         shells,
	}, nil
}

func (a *App) TerminalOpen(
	agentSessionID string,
	shellID string,
	rows int,
	cols int,
) (coreterminal.OpenResult, error) {
	ctx, cancel := a.operationContext(12 * time.Second)
	defer cancel()
	target, shells, err := a.terminalTarget(ctx, agentSessionID)
	if err != nil {
		return coreterminal.OpenResult{}, err
	}
	shellID = strings.TrimSpace(shellID)
	valid := false
	for _, shell := range shells {
		if shell.ID == shellID && shell.Status == "running" {
			valid = true
			break
		}
	}
	if !valid {
		return coreterminal.OpenResult{}, errors.New("Shell is not a running child of the selected Core Session")
	}
	return a.terminals.Open(ctx, target.coreEndpoint, coreterminal.OpenOptions{
		AgentSessionID: target.session.Session.ID,
		CoreSessionID:  target.coreSessionID,
		ShellID:        shellID,
		Rows:           rows,
		Cols:           cols,
	})
}

func (a *App) TerminalPoll(id string) (coreterminal.PollResult, error) {
	return a.terminals.Poll(id)
}

func (a *App) TerminalInput(id string, data string) error {
	return a.terminals.Input(id, data)
}

func (a *App) TerminalResize(id string, rows int, cols int) error {
	return a.terminals.Resize(id, rows, cols)
}

func (a *App) TerminalClose(id string) error {
	return a.terminals.Close(id)
}

type terminalTarget struct {
	session       controlclient.AgentSessionEnvelope
	coreSessionID string
	coreEndpoint  string
}

func (a *App) terminalTarget(
	ctx context.Context,
	agentSessionID string,
) (terminalTarget, []coreterminal.Shell, error) {
	if a.client == nil {
		return terminalTarget{}, nil, errors.New("control client is required")
	}
	core, err := a.client.Core(ctx)
	if err != nil {
		return terminalTarget{}, nil, fmt.Errorf("read Core identity: %w", err)
	}
	if !core.Configured || core.Core == nil || core.Core.State != "identified" {
		return terminalTarget{}, nil, errors.New("Core is not identified")
	}
	session, err := a.client.AgentSession(ctx, agentSessionID)
	if err != nil {
		return terminalTarget{}, nil, fmt.Errorf("read Agent Session: %w", err)
	}
	coreSessionID := strings.TrimSpace(session.Session.CoreSessionID)
	if coreSessionID == "" {
		return terminalTarget{}, nil, errors.New("Agent Session has no trusted Core Session association")
	}
	shells, err := a.terminals.Shells(ctx, core.Core.Endpoint, coreSessionID)
	if err != nil {
		return terminalTarget{}, nil, fmt.Errorf("read Core Session shells: %w", err)
	}
	return terminalTarget{
		session:       session,
		coreSessionID: coreSessionID,
		coreEndpoint:  core.Core.Endpoint,
	}, shells, nil
}

func (a *App) operationContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
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
