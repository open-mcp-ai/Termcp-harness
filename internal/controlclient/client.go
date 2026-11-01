package controlclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

const (
	defaultTimeout     = 5 * time.Second
	decisionTimeout    = 20 * time.Second
	maxResponseBytes   = 4 << 20
	maxTranscriptBytes = 16 << 20
	defaultEventLimit  = 200
	maxEventLimit      = 1000
)

var ErrApprovalTokenRequired = errors.New("approval token is required")

type Health struct {
	Status  string        `json:"status"`
	State   runtime.State `json:"state"`
	Version string        `json:"version"`
}

type MCPServers struct {
	Servers        []mcpcontrol.Snapshot `json:"servers"`
	Total          int                   `json:"total"`
	Connected      int                   `json:"connected"`
	Ready          int                   `json:"ready"`
	ControlPlaneOn bool                  `json:"control_plane_configured"`
}

type CoreState struct {
	Configured bool                   `json:"configured"`
	Core       *coreinstance.Snapshot `json:"core,omitempty"`
}

type ApprovalsState struct {
	Configured            bool                           `json:"configured"`
	CorrelationCapability approval.CorrelationCapability `json:"correlation_capability"`
	Total                 int                            `json:"total"`
	Approvals             []approval.Tracked             `json:"approvals"`
}

type AgentSessionsState struct {
	Configured bool                    `json:"configured"`
	Total      int                     `json:"total"`
	Sessions   []agentsession.Snapshot `json:"sessions"`
}

type ApprovalDecisionResult struct {
	Approval approval.Snapshot `json:"approval"`
	Outcome  string            `json:"outcome"`
	Warning  string            `json:"warning,omitempty"`
}

type ApprovalRefreshResult struct {
	Approval approval.Tracked `json:"approval"`
	Changed  bool             `json:"changed"`
	Complete bool             `json:"complete"`
	Warning  string           `json:"warning,omitempty"`
}

type ApprovalReconciliationResult struct {
	Approval approval.Tracked `json:"approval"`
	Changed  bool             `json:"changed"`
	Resolved bool             `json:"resolved"`
	Warning  string           `json:"warning,omitempty"`
}

type AgentSessionEnvelope struct {
	Session agentsession.Snapshot `json:"session"`
}

type AgentSessionTranscriptEnvelope struct {
	Transcript agentsession.Transcript `json:"transcript"`
}

type AgentCallSummary struct {
	CallID      string          `json:"call_id"`
	Status      toolcall.Status `json:"status"`
	ResultBytes int             `json:"result_bytes"`
	IsError     bool            `json:"is_error"`
}

type AgentSessionRunResult struct {
	Session agentsession.Snapshot `json:"session"`
	Calls   []AgentCallSummary    `json:"calls"`
}

type AgentLoopResult struct {
	Session agentsession.Snapshot `json:"session"`
	Output  string                `json:"output,omitempty"`
	Calls   []AgentCallSummary    `json:"calls"`
}

type Bootstrap struct {
	Endpoint      string             `json:"endpoint"`
	Health        Health             `json:"health"`
	Runtime       runtime.Snapshot   `json:"runtime"`
	Events        eventlog.Window    `json:"events"`
	MCP           MCPServers         `json:"mcp"`
	Core          CoreState          `json:"core"`
	Approvals     ApprovalsState     `json:"approvals"`
	AgentSessions AgentSessionsState `json:"agent_sessions"`
}

type Client struct {
	endpoint       string
	http           *http.Client
	decisionHTTP   *http.Client
	agentHTTP      *http.Client
	transcriptHTTP *http.Client
}

func New(endpoint string) (*Client, error) {
	normalized, err := validateLoopbackBaseURL(endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{
		endpoint:       normalized,
		http:           &http.Client{Timeout: defaultTimeout},
		decisionHTTP:   &http.Client{Timeout: decisionTimeout},
		agentHTTP:      &http.Client{Timeout: 5 * time.Minute},
		transcriptHTTP: &http.Client{Timeout: 20 * time.Second},
	}, nil
}

func (c *Client) Endpoint() string {
	return c.endpoint
}

func (c *Client) Bootstrap(ctx context.Context) (Bootstrap, error) {
	if c == nil {
		return Bootstrap{}, errors.New("control client is required")
	}

	bootstrap := Bootstrap{Endpoint: c.endpoint}
	if _, err := c.getJSON(ctx, "/healthz", []int{http.StatusOK, http.StatusServiceUnavailable}, &bootstrap.Health); err != nil {
		return Bootstrap{}, fmt.Errorf("read runtime health: %w", err)
	}
	if _, err := c.getJSON(ctx, "/v1/runtime", []int{http.StatusOK}, &bootstrap.Runtime); err != nil {
		return Bootstrap{}, fmt.Errorf("read runtime snapshot: %w", err)
	}
	if _, err := c.getJSON(ctx, fmt.Sprintf("/v1/events?after=0&limit=%d", defaultEventLimit), []int{http.StatusOK}, &bootstrap.Events); err != nil {
		return Bootstrap{}, fmt.Errorf("read event window: %w", err)
	}
	if _, err := c.getJSON(ctx, "/v1/mcp/servers", []int{http.StatusOK}, &bootstrap.MCP); err != nil {
		return Bootstrap{}, fmt.Errorf("read MCP servers: %w", err)
	}
	if _, err := c.getJSON(ctx, "/v1/core", []int{http.StatusOK}, &bootstrap.Core); err != nil {
		return Bootstrap{}, fmt.Errorf("read Core identity: %w", err)
	}
	if _, err := c.getJSON(ctx, "/v1/approvals", []int{http.StatusOK}, &bootstrap.Approvals); err != nil {
		return Bootstrap{}, fmt.Errorf("read approvals: %w", err)
	}
	if _, err := c.getJSON(ctx, "/v1/agent/sessions", []int{http.StatusOK}, &bootstrap.AgentSessions); err != nil {
		return Bootstrap{}, fmt.Errorf("read Agent Sessions: %w", err)
	}
	return bootstrap, nil
}

func (c *Client) Events(ctx context.Context, after eventlog.Sequence, limit int) (eventlog.Window, error) {
	if c == nil {
		return eventlog.Window{}, errors.New("control client is required")
	}
	if limit < 1 || limit > maxEventLimit {
		return eventlog.Window{}, fmt.Errorf("event limit must be between 1 and %d", maxEventLimit)
	}

	var window eventlog.Window
	path := fmt.Sprintf("/v1/events?after=%d&limit=%d", after, limit)
	if _, err := c.getJSON(ctx, path, []int{http.StatusOK}, &window); err != nil {
		return eventlog.Window{}, err
	}
	return window, nil
}

func (c *Client) Core(ctx context.Context) (CoreState, error) {
	if c == nil {
		return CoreState{}, errors.New("control client is required")
	}
	var state CoreState
	if _, err := c.getJSON(ctx, "/v1/core", []int{http.StatusOK}, &state); err != nil {
		return CoreState{}, err
	}
	return state, nil
}

func (c *Client) AgentSession(ctx context.Context, id string) (AgentSessionEnvelope, error) {
	if c == nil {
		return AgentSessionEnvelope{}, errors.New("control client is required")
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 {
		return AgentSessionEnvelope{}, errors.New("Agent Session ID is required")
	}
	var result AgentSessionEnvelope
	if _, err := c.getJSON(
		ctx,
		"/v1/agent/sessions/"+url.PathEscape(id),
		[]int{http.StatusOK},
		&result,
	); err != nil {
		return AgentSessionEnvelope{}, err
	}
	return result, nil
}

func (c *Client) AgentSessionTranscript(
	ctx context.Context,
	id string,
	token string,
) (AgentSessionTranscriptEnvelope, error) {
	if c == nil {
		return AgentSessionTranscriptEnvelope{}, errors.New("control client is required")
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 {
		return AgentSessionTranscriptEnvelope{}, errors.New("Agent Session ID is required")
	}
	if token == "" || strings.TrimSpace(token) != token {
		return AgentSessionTranscriptEnvelope{}, ErrApprovalTokenRequired
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.endpoint+"/v1/agent/sessions/"+url.PathEscape(id)+"/transcript",
		nil,
	)
	if err != nil {
		return AgentSessionTranscriptEnvelope{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.transcriptHTTP.Do(request)
	if err != nil {
		return AgentSessionTranscriptEnvelope{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxTranscriptBytes+1))
	if err != nil {
		return AgentSessionTranscriptEnvelope{}, err
	}
	if len(responseBody) > maxTranscriptBytes {
		return AgentSessionTranscriptEnvelope{}, fmt.Errorf("transcript response exceeds %d bytes", maxTranscriptBytes)
	}
	if response.StatusCode != http.StatusOK {
		return AgentSessionTranscriptEnvelope{}, fmt.Errorf("Agent Session transcript returned HTTP %d", response.StatusCode)
	}
	var result AgentSessionTranscriptEnvelope
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return AgentSessionTranscriptEnvelope{}, fmt.Errorf("decode Agent Session transcript: %w", err)
	}
	return result, nil
}

func (c *Client) DecideApproval(
	ctx context.Context,
	id string,
	expected approval.Revision,
	decision approval.Decision,
	token string,
) (ApprovalDecisionResult, error) {
	if c == nil {
		return ApprovalDecisionResult{}, errors.New("control client is required")
	}
	if _, err := approval.TrustedReference(id, "", nil); err != nil {
		return ApprovalDecisionResult{}, err
	}
	if expected == 0 || (decision != approval.DecisionApprove && decision != approval.DecisionReject) {
		return ApprovalDecisionResult{}, errors.New("approval decision and expected revision are required")
	}
	if token == "" || strings.TrimSpace(token) != token {
		return ApprovalDecisionResult{}, ErrApprovalTokenRequired
	}

	body, err := json.Marshal(map[string]any{
		"decision":          decision,
		"expected_revision": expected,
	})
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint+"/v1/approvals/"+url.PathEscape(id)+"/decision",
		bytes.NewReader(body),
	)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := c.decisionHTTP.Do(request)
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return ApprovalDecisionResult{}, err
	}
	if len(responseBody) > maxResponseBytes {
		return ApprovalDecisionResult{}, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		return ApprovalDecisionResult{}, fmt.Errorf("approval decision returned HTTP %d", response.StatusCode)
	}
	var result ApprovalDecisionResult
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return ApprovalDecisionResult{}, fmt.Errorf("decode approval decision response: %w", err)
	}
	return result, nil
}

// RefreshApprovalExecution asks the Runtime to read the authority's execution
// state once. It never submits a decision or replays the approved tool call.
func (c *Client) RefreshApprovalExecution(
	ctx context.Context,
	id string,
	token string,
) (ApprovalRefreshResult, error) {
	if c == nil {
		return ApprovalRefreshResult{}, errors.New("control client is required")
	}
	if _, err := approval.TrustedReference(id, "", nil); err != nil {
		return ApprovalRefreshResult{}, err
	}
	if token == "" || strings.TrimSpace(token) != token {
		return ApprovalRefreshResult{}, ErrApprovalTokenRequired
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint+"/v1/approvals/"+url.PathEscape(id)+"/execution/refresh",
		bytes.NewReader([]byte(`{}`)),
	)
	if err != nil {
		return ApprovalRefreshResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := c.decisionHTTP.Do(request)
	if err != nil {
		return ApprovalRefreshResult{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return ApprovalRefreshResult{}, err
	}
	if len(responseBody) > maxResponseBytes {
		return ApprovalRefreshResult{}, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		return ApprovalRefreshResult{}, fmt.Errorf("approval execution refresh returned HTTP %d", response.StatusCode)
	}
	var result ApprovalRefreshResult
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return ApprovalRefreshResult{}, fmt.Errorf("decode approval execution refresh response: %w", err)
	}
	return result, nil
}

// ReconcileApproval asks the Runtime to perform one read-only authority lookup
// for an unknown approval. It cannot submit a decision or replay a tool call.
func (c *Client) ReconcileApproval(
	ctx context.Context,
	id string,
	token string,
) (ApprovalReconciliationResult, error) {
	if c == nil {
		return ApprovalReconciliationResult{}, errors.New("control client is required")
	}
	if _, err := approval.TrustedReference(id, "", nil); err != nil {
		return ApprovalReconciliationResult{}, err
	}
	if token == "" || strings.TrimSpace(token) != token {
		return ApprovalReconciliationResult{}, ErrApprovalTokenRequired
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.endpoint+"/v1/approvals/"+url.PathEscape(id)+"/reconcile",
		bytes.NewReader([]byte(`{}`)),
	)
	if err != nil {
		return ApprovalReconciliationResult{}, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := c.decisionHTTP.Do(request)
	if err != nil {
		return ApprovalReconciliationResult{}, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return ApprovalReconciliationResult{}, err
	}
	if len(responseBody) > maxResponseBytes {
		return ApprovalReconciliationResult{}, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		return ApprovalReconciliationResult{}, fmt.Errorf("approval reconciliation returned HTTP %d", response.StatusCode)
	}
	var result ApprovalReconciliationResult
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return ApprovalReconciliationResult{}, fmt.Errorf("decode approval reconciliation response: %w", err)
	}
	return result, nil
}

func (c *Client) CreateAgentSession(
	ctx context.Context,
	id string,
	coreSessionID string,
	token string,
) (AgentSessionEnvelope, error) {
	if c == nil {
		return AgentSessionEnvelope{}, errors.New("control client is required")
	}
	if err := validateMutationToken(token); err != nil {
		return AgentSessionEnvelope{}, err
	}
	body, err := json.Marshal(map[string]any{
		"id":              id,
		"core_session_id": coreSessionID,
	})
	if err != nil {
		return AgentSessionEnvelope{}, err
	}
	var result AgentSessionEnvelope
	if _, err := c.postAgentJSON(
		ctx,
		"/v1/agent/sessions",
		body,
		token,
		[]int{http.StatusCreated},
		&result,
	); err != nil {
		return AgentSessionEnvelope{}, err
	}
	return result, nil
}

func (c *Client) RunAgentSession(
	ctx context.Context,
	id string,
	steps []toolpipeline.Request,
	token string,
) (AgentSessionRunResult, error) {
	if c == nil {
		return AgentSessionRunResult{}, errors.New("control client is required")
	}
	if err := validateMutationToken(token); err != nil {
		return AgentSessionRunResult{}, err
	}
	body, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		return AgentSessionRunResult{}, err
	}
	var result AgentSessionRunResult
	if _, err := c.postAgentJSON(
		ctx,
		"/v1/agent/sessions/"+url.PathEscape(id)+"/run",
		body,
		token,
		[]int{http.StatusOK, http.StatusAccepted},
		&result,
	); err != nil {
		return AgentSessionRunResult{}, err
	}
	return result, nil
}

func (c *Client) RunAgentLoop(
	ctx context.Context,
	id string,
	input string,
	maxIterations int,
	maxToolCalls int,
	token string,
) (AgentLoopResult, error) {
	if c == nil {
		return AgentLoopResult{}, errors.New("control client is required")
	}
	if err := validateMutationToken(token); err != nil {
		return AgentLoopResult{}, err
	}
	body, err := json.Marshal(map[string]any{
		"input":          input,
		"max_iterations": maxIterations,
		"max_tool_calls": maxToolCalls,
	})
	if err != nil {
		return AgentLoopResult{}, err
	}
	var result AgentLoopResult
	if _, err := c.postAgentJSON(
		ctx,
		"/v1/agent/sessions/"+url.PathEscape(id)+"/loop",
		body,
		token,
		[]int{http.StatusOK, http.StatusAccepted},
		&result,
	); err != nil {
		return AgentLoopResult{}, err
	}
	return result, nil
}

// ResumeAgentLoop asks the Runtime to claim one safe, Runtime-restarted model
// checkpoint. Model endpoint, credentials, tools, and invoker remain Runtime
// configuration and are never supplied by the client.
func (c *Client) ResumeAgentLoop(
	ctx context.Context,
	id string,
	token string,
) (AgentLoopResult, error) {
	if c == nil {
		return AgentLoopResult{}, errors.New("control client is required")
	}
	if err := validateMutationToken(token); err != nil {
		return AgentLoopResult{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 {
		return AgentLoopResult{}, errors.New("Agent Session ID is required")
	}
	var result AgentLoopResult
	if _, err := c.postAgentJSON(
		ctx,
		"/v1/agent/sessions/"+url.PathEscape(id)+"/loop/resume",
		[]byte(`{}`),
		token,
		[]int{http.StatusOK, http.StatusAccepted},
		&result,
	); err != nil {
		return AgentLoopResult{}, err
	}
	return result, nil
}

func (c *Client) postAgentJSON(
	ctx context.Context,
	path string,
	body []byte,
	token string,
	acceptedStatuses []int,
	target any,
) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.agentHTTP.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return response.StatusCode, err
	}
	if len(responseBody) > maxResponseBytes {
		return response.StatusCode, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	accepted := false
	for _, status := range acceptedStatuses {
		if response.StatusCode == status {
			accepted = true
			break
		}
	}
	if !accepted {
		return response.StatusCode, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err := json.Unmarshal(responseBody, target); err != nil {
		return response.StatusCode, fmt.Errorf("decode agent response: %w", err)
	}
	return response.StatusCode, nil
}

func validateMutationToken(token string) error {
	if token == "" || strings.TrimSpace(token) != token {
		return ErrApprovalTokenRequired
	}
	return nil
}

func (c *Client) getJSON(ctx context.Context, path string, accepted []int, target any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	acceptedStatus := false
	for _, status := range accepted {
		if response.StatusCode == status {
			acceptedStatus = true
			break
		}
	}
	if !acceptedStatus {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return response.StatusCode, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return response.StatusCode, err
	}
	if len(body) > maxResponseBytes {
		return response.StatusCode, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return response.StatusCode, fmt.Errorf("decode JSON: %w", err)
	}
	return response.StatusCode, nil
}

func validateLoopbackBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse control endpoint: %w", err)
	}
	if parsed.Scheme != "http" {
		return "", errors.New("control endpoint must use http")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("control endpoint must not contain credentials, query, or fragment")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("control endpoint host is required")
	}
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", errors.New("control endpoint must be loopback")
		}
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("control endpoint must not contain a path")
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
