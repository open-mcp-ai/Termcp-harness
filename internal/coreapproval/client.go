// Package coreapproval adapts the public Termcp Core approval HTTP contract to
// the Harness approval coordinator.
package coreapproval

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

	"github.com/open-mcp-ai/termcp-harness/internal/approval"
)

const (
	defaultHTTPTimeout = 15 * time.Second
	maxResponseBytes   = 1 << 20
)

var (
	ErrNotFound        = errors.New("Core approval request not found")
	ErrConflict        = errors.New("Core approval request conflict")
	ErrInvalidResponse = errors.New("invalid Core approval response")
)

type Client struct {
	baseURL   string
	authToken string
	http      *http.Client
}

func NewClient(baseURL, authToken string, httpClient *http.Client) (*Client, error) {
	normalized, err := validateBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{
		baseURL:   normalized,
		authToken: strings.TrimSpace(authToken),
		http:      httpClient,
	}, nil
}

// Decide submits exactly one approval decision. Transport ambiguity is returned
// as an error and is interpreted by the coordinator as an unknown outcome.
func (c *Client) Decide(
	ctx context.Context,
	id string,
	decision approval.Decision,
) (approval.DecisionOutcome, error) {
	if c == nil {
		return approval.DecisionOutcome{}, errors.New("Core approval client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := approval.TrustedReference(id, "", nil); err != nil {
		return approval.DecisionOutcome{}, err
	}
	action, err := actionFor(decision)
	if err != nil {
		return approval.DecisionOutcome{}, err
	}

	endpoint := c.baseURL + "/api/approvals/" + url.PathEscape(id) + "/" + action
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("build Core approval request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("submit Core approval decision: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("read Core approval response: %w", err)
	}
	if len(body) > maxResponseBytes {
		return approval.DecisionOutcome{}, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maxResponseBytes)
	}
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return approval.DecisionOutcome{}, ErrNotFound
	case http.StatusConflict:
		outcome, decodeErr := decodeDecisionOutcome(body)
		if decodeErr == nil && outcome.Status == approval.StatusApproved &&
			outcome.Execution != nil && outcome.Execution.State == approval.ExecutionFailed {
			return outcome, nil
		}
		return approval.DecisionOutcome{}, ErrConflict
	default:
		return approval.DecisionOutcome{}, fmt.Errorf("Core approval decision returned HTTP %d", response.StatusCode)
	}

	return decodeDecisionOutcome(body)
}

// Lookup reads one approval request without replaying its decision or side
// effects. It is the recovery path after a decision response becomes unknown.
func (c *Client) Lookup(ctx context.Context, id string) (approval.DecisionOutcome, error) {
	if c == nil {
		return approval.DecisionOutcome{}, errors.New("Core approval client is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := approval.TrustedReference(id, "", nil); err != nil {
		return approval.DecisionOutcome{}, err
	}
	endpoint := c.baseURL + "/api/approvals/" + url.PathEscape(id)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("build Core approval lookup: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("lookup Core approval: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("read Core approval lookup: %w", err)
	}
	if len(body) > maxResponseBytes {
		return approval.DecisionOutcome{}, fmt.Errorf("%w: response exceeds %d bytes", ErrInvalidResponse, maxResponseBytes)
	}
	switch response.StatusCode {
	case http.StatusOK:
		return decodeDecisionOutcome(body)
	case http.StatusNotFound:
		return approval.DecisionOutcome{}, ErrNotFound
	default:
		return approval.DecisionOutcome{}, fmt.Errorf("Core approval lookup returned HTTP %d", response.StatusCode)
	}
}

func validateBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("parse Core approval base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("Core approval base URL must use http or https")
	}
	if parsed.User != nil {
		return "", errors.New("Core approval base URL must not contain user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Core approval base URL must not contain query or fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", errors.New("Core approval base URL must be an origin without a path")
	}
	host := parsed.Hostname()
	if host == "" {
		return "", errors.New("Core approval base URL host is required")
	}
	if !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return "", fmt.Errorf("Core approval host %q is not loopback", host)
		}
	}
	return parsed.Scheme + "://" + parsed.Host, nil
}

func actionFor(decision approval.Decision) (string, error) {
	switch decision {
	case approval.DecisionApprove:
		return "approve", nil
	case approval.DecisionReject:
		return "reject", nil
	default:
		return "", fmt.Errorf("unsupported approval decision %q", decision)
	}
}

type coreExecution struct {
	State      string `json:"state"`
	StartedAt  int64  `json:"started_at"`
	FinishedAt int64  `json:"finished_at"`
	Error      string `json:"error"`
}

type coreApprovalRequest struct {
	State     string         `json:"state"`
	Execution *coreExecution `json:"execution"`
}

func decodeDecisionOutcome(body []byte) (approval.DecisionOutcome, error) {
	var response struct {
		Request   coreApprovalRequest `json:"request"`
		State     string              `json:"state"`
		Execution *coreExecution      `json:"execution"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return approval.DecisionOutcome{}, fmt.Errorf("%w: decode response: %v", ErrInvalidResponse, err)
	}
	state := response.Request.State
	if state == "" {
		state = response.State
	}
	if state == "" {
		return approval.DecisionOutcome{}, fmt.Errorf("%w: approval state is missing", ErrInvalidResponse)
	}
	status, err := approvalStatus(state)
	if err != nil {
		return approval.DecisionOutcome{}, err
	}
	wireExecution := response.Request.Execution
	if wireExecution == nil {
		wireExecution = response.Execution
	}
	execution, err := decodeExecution(wireExecution)
	if err != nil {
		return approval.DecisionOutcome{}, err
	}
	if execution != nil && status != approval.StatusApproved {
		return approval.DecisionOutcome{}, fmt.Errorf(
			"%w: execution result requires approved state, got %s",
			ErrInvalidResponse,
			status,
		)
	}
	return approval.DecisionOutcome{Status: status, Execution: execution}, nil
}

func decodeExecution(wire *coreExecution) (*approval.Execution, error) {
	if wire == nil {
		return nil, nil
	}
	if wire.State == "" {
		return nil, fmt.Errorf("%w: execution state is missing", ErrInvalidResponse)
	}
	if wire.StartedAt <= 0 {
		return nil, fmt.Errorf("%w: execution started_at is invalid", ErrInvalidResponse)
	}
	state := approval.ExecutionState(wire.State)
	switch state {
	case approval.ExecutionRunning,
		approval.ExecutionSubmitted,
		approval.ExecutionSucceeded,
		approval.ExecutionFailed,
		approval.ExecutionUnknown:
	default:
		return nil, fmt.Errorf("%w: unknown execution state %q", ErrInvalidResponse, wire.State)
	}
	var finishedAt *time.Time
	if wire.FinishedAt > 0 {
		value := time.UnixMilli(wire.FinishedAt).UTC()
		finishedAt = &value
	}
	if state != approval.ExecutionRunning && finishedAt == nil {
		return nil, fmt.Errorf("%w: terminal execution is missing finished_at", ErrInvalidResponse)
	}
	if len(wire.Error) > 4096 {
		return nil, fmt.Errorf("%w: execution error exceeds 4096 bytes", ErrInvalidResponse)
	}
	return &approval.Execution{
		State:      state,
		StartedAt:  time.UnixMilli(wire.StartedAt).UTC(),
		FinishedAt: finishedAt,
		Error:      strings.TrimSpace(wire.Error),
	}, nil
}

func approvalStatus(state string) (approval.Status, error) {
	switch state {
	case "pending":
		return approval.StatusPending, nil
	case "approved":
		return approval.StatusApproved, nil
	case "rejected":
		return approval.StatusRejected, nil
	case "expired":
		return approval.StatusExpired, nil
	case "cancelled":
		return approval.StatusCancelled, nil
	default:
		return approval.StatusUnknown, fmt.Errorf("%w: unknown approval state %q", ErrInvalidResponse, state)
	}
}
