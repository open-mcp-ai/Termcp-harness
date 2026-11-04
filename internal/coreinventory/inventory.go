// Package coreinventory reads bounded, read-only Core session metadata through
// the public HTTP contract. It never exposes Core credentials or private store
// paths to callers.
package coreinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
)

const (
	maxResponseBytes = 1 << 20
	maxSessions      = 1000
)

type Session struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status"`
	Rows        int    `json:"rows,omitempty"`
	Cols        int    `json:"cols,omitempty"`
	SSHEndpoint string `json:"ssh_endpoint,omitempty"`
	SSHConfig   string `json:"ssh_config,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
}

type Snapshot struct {
	Configured bool      `json:"configured"`
	Total      int       `json:"total"`
	Sessions   []Session `json:"sessions"`
}

type Client struct {
	endpoint string
	token    string
	http     *http.Client
}

func NewClient(endpoint, token string) (*Client, error) {
	normalized, err := coreinstance.ValidateEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	return &Client{
		endpoint: normalized,
		token:    strings.TrimSpace(token),
		http:     &http.Client{Timeout: 10 * time.Second},
	}, nil
}

func (c *Client) List(ctx context.Context) (Snapshot, error) {
	if c == nil {
		return Snapshot{}, errors.New("Core inventory client is required")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/api/sessions", nil)
	if err != nil {
		return Snapshot{}, err
	}
	request.Header.Set("Accept", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read Core sessions: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("Core sessions returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("read Core sessions body: %w", err)
	}
	if len(body) > maxResponseBytes {
		return Snapshot{}, fmt.Errorf("Core sessions response exceeds %d bytes", maxResponseBytes)
	}
	var document struct {
		Sessions []Session `json:"sessions"`
	}
	if err := json.Unmarshal(body, &document); err != nil {
		return Snapshot{}, fmt.Errorf("decode Core sessions: %w", err)
	}
	if len(document.Sessions) > maxSessions {
		return Snapshot{}, fmt.Errorf("Core session count exceeds %d", maxSessions)
	}
	sessions := make([]Session, len(document.Sessions))
	seen := make(map[string]struct{}, len(document.Sessions))
	for index, session := range document.Sessions {
		session.ID = strings.TrimSpace(session.ID)
		session.Name = strings.TrimSpace(session.Name)
		session.Status = strings.TrimSpace(session.Status)
		session.SSHEndpoint = strings.TrimSpace(session.SSHEndpoint)
		session.SSHConfig = strings.TrimSpace(session.SSHConfig)
		session.Namespace = strings.TrimSpace(session.Namespace)
		if session.ID == "" || len(session.ID) > 256 {
			return Snapshot{}, errors.New("Core session has an invalid ID")
		}
		if session.Status == "" {
			return Snapshot{}, fmt.Errorf("Core session %q has no status", session.ID)
		}
		if _, duplicate := seen[session.ID]; duplicate {
			return Snapshot{}, fmt.Errorf("Core session %q is duplicated", session.ID)
		}
		seen[session.ID] = struct{}{}
		sessions[index] = session
	}
	return Snapshot{
		Configured: true,
		Total:      len(sessions),
		Sessions:   sessions,
	}, nil
}
