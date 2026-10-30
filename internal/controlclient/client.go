package controlclient

import (
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

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

const (
	defaultTimeout    = 5 * time.Second
	maxResponseBytes  = 4 << 20
	defaultEventLimit = 200
	maxEventLimit     = 1000
)

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

type Bootstrap struct {
	Endpoint string           `json:"endpoint"`
	Health   Health           `json:"health"`
	Runtime  runtime.Snapshot `json:"runtime"`
	Events   eventlog.Window  `json:"events"`
	MCP      MCPServers       `json:"mcp"`
}

type Client struct {
	endpoint string
	http     *http.Client
}

func New(endpoint string) (*Client, error) {
	normalized, err := validateLoopbackBaseURL(endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{
		endpoint: normalized,
		http: &http.Client{
			Timeout: defaultTimeout,
		},
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
