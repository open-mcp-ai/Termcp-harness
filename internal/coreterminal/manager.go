package coreterminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
)

const (
	defaultHTTPTimeout  = 5 * time.Second
	defaultWriteTimeout = 5 * time.Second
	maxResponseBytes    = 1 << 20
	maxHistoryBytes     = 512 << 10
	maxOutputBytes      = 1 << 20
	maxInputBytes       = 64 << 10
	maxTerminals        = 16
)

var (
	ErrTerminalNotFound = errors.New("terminal bridge session not found")
	ErrTerminalClosed   = errors.New("terminal bridge session is closed")
	ErrInvalidTarget    = errors.New("invalid terminal target")
)

type State string

const (
	StateConnecting State = "connecting"
	StateConnected  State = "connected"
	StateEnded      State = "ended"
	StateUnknown    State = "unknown"
)

type Shell struct {
	ID     string `json:"id"`
	Index  int    `json:"index"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
	Mode   string `json:"mode,omitempty"`
}

type OpenOptions struct {
	AgentSessionID string
	CoreSessionID  string
	ShellID        string
	Rows           int
	Cols           int
}

type OpenResult struct {
	ID               string `json:"id"`
	AgentSessionID   string `json:"agent_session_id"`
	CoreSessionID    string `json:"core_session_id"`
	ShellID          string `json:"shell_id"`
	State            State  `json:"state"`
	History          string `json:"history,omitempty"`
	HistoryBytes     int    `json:"history_bytes"`
	HistoryTruncated bool   `json:"history_truncated"`
}

type Snapshot struct {
	ID                 string `json:"id"`
	AgentSessionID     string `json:"agent_session_id"`
	CoreSessionID      string `json:"core_session_id"`
	ShellID            string `json:"shell_id"`
	State              State  `json:"state"`
	ActivitySource     string `json:"activity_source,omitempty"`
	LastError          string `json:"last_error,omitempty"`
	DroppedOutputBytes uint64 `json:"dropped_output_bytes"`
	Revision           uint64 `json:"revision"`
}

type PollResult struct {
	Snapshot
	Output string `json:"output,omitempty"`
}

type Manager struct {
	mu       sync.RWMutex
	sessions map[string]*bridgeSession
	nextID   atomic.Uint64
	http     *http.Client
	dialer   *websocket.Dialer
}

type bridgeSession struct {
	id             string
	agentSessionID string
	coreSessionID  string
	shellID        string
	conn           *websocket.Conn

	mu                 sync.Mutex
	writeMu            sync.Mutex
	output             strings.Builder
	droppedOutputBytes uint64
	state              State
	lastError          string
	activitySource     string
	revision           uint64
	closed             bool
}

func NewManager() *Manager {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Manager{
		sessions: make(map[string]*bridgeSession),
		http:     &http.Client{Timeout: defaultHTTPTimeout, Transport: transport},
		dialer: &websocket.Dialer{
			HandshakeTimeout: defaultHTTPTimeout,
		},
	}
}

func (m *Manager) Shells(ctx context.Context, endpoint, coreSessionID string) ([]Shell, error) {
	if m == nil {
		return nil, errors.New("terminal manager is required")
	}
	origin, err := coreinstance.ValidateEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	coreSessionID = strings.TrimSpace(coreSessionID)
	if coreSessionID == "" {
		return nil, fmt.Errorf("%w: Core Session ID is required", ErrInvalidTarget)
	}

	var response struct {
		Shells []Shell `json:"shells"`
	}
	requestURL := origin + "/api/sessions/" + url.PathEscape(coreSessionID) + "/shells"
	if err := m.getJSON(ctx, requestURL, &response); err != nil {
		return nil, err
	}
	for index := range response.Shells {
		response.Shells[index].ID = strings.TrimSpace(response.Shells[index].ID)
		response.Shells[index].Status = strings.TrimSpace(response.Shells[index].Status)
	}
	return response.Shells, nil
}

func (m *Manager) Open(ctx context.Context, endpoint string, options OpenOptions) (OpenResult, error) {
	if m == nil {
		return OpenResult{}, errors.New("terminal manager is required")
	}
	origin, err := coreinstance.ValidateEndpoint(endpoint)
	if err != nil {
		return OpenResult{}, err
	}
	options.AgentSessionID = strings.TrimSpace(options.AgentSessionID)
	options.CoreSessionID = strings.TrimSpace(options.CoreSessionID)
	options.ShellID = strings.TrimSpace(options.ShellID)
	if options.AgentSessionID == "" || options.CoreSessionID == "" || options.ShellID == "" {
		return OpenResult{}, fmt.Errorf("%w: Agent Session, Core Session, and Shell IDs are required", ErrInvalidTarget)
	}
	if options.Rows < 1 || options.Rows > 1000 || options.Cols < 1 || options.Cols > 2000 {
		return OpenResult{}, errors.New("terminal dimensions are out of range")
	}

	m.mu.RLock()
	count := len(m.sessions)
	m.mu.RUnlock()
	if count >= maxTerminals {
		return OpenResult{}, errors.New("terminal bridge session limit reached")
	}

	history, err := m.history(ctx, origin, options.ShellID)
	if err != nil {
		return OpenResult{}, err
	}

	wsURL, err := websocketURL(origin)
	if err != nil {
		return OpenResult{}, err
	}
	conn, response, err := m.dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return OpenResult{}, fmt.Errorf("connect Core terminal WebSocket: %w", err)
	}
	conn.SetReadLimit(2 << 20)

	id := fmt.Sprintf("terminal-%d", m.nextID.Add(1))
	session := &bridgeSession{
		id:             id,
		agentSessionID: options.AgentSessionID,
		coreSessionID:  options.CoreSessionID,
		shellID:        options.ShellID,
		conn:           conn,
		state:          StateConnecting,
	}

	if err := session.writeJSON(map[string]any{"type": "watch_add", "id": options.ShellID}); err != nil {
		conn.Close()
		return OpenResult{}, fmt.Errorf("subscribe Core terminal output: %w", err)
	}
	if err := session.writeJSON(map[string]any{
		"type": "resize",
		"id":   options.ShellID,
		"rows": options.Rows,
		"cols": options.Cols,
	}); err != nil {
		conn.Close()
		return OpenResult{}, fmt.Errorf("initialise Core terminal size: %w", err)
	}

	session.mu.Lock()
	session.state = StateConnected
	session.revision++
	session.mu.Unlock()

	m.mu.Lock()
	if len(m.sessions) >= maxTerminals {
		m.mu.Unlock()
		conn.Close()
		return OpenResult{}, errors.New("terminal bridge session limit reached")
	}
	m.sessions[id] = session
	m.mu.Unlock()

	go m.readPump(session)

	return OpenResult{
		ID:               id,
		AgentSessionID:   options.AgentSessionID,
		CoreSessionID:    options.CoreSessionID,
		ShellID:          options.ShellID,
		State:            StateConnected,
		History:          history.Data,
		HistoryBytes:     len(history.Data),
		HistoryTruncated: history.Start > 0,
	}, nil
}

func (m *Manager) Poll(id string) (PollResult, error) {
	session, err := m.get(id)
	if err != nil {
		return PollResult{}, err
	}

	session.mu.Lock()
	output := session.output.String()
	session.output.Reset()
	result := PollResult{
		Snapshot: session.snapshotLocked(),
		Output:   output,
	}
	session.mu.Unlock()
	return result, nil
}

func (m *Manager) Input(id, data string) error {
	if data == "" {
		return nil
	}
	if len(data) > maxInputBytes {
		return fmt.Errorf("terminal input exceeds %d bytes", maxInputBytes)
	}
	session, err := m.get(id)
	if err != nil {
		return err
	}
	session.mu.Lock()
	state := session.state
	shellID := session.shellID
	session.mu.Unlock()
	if state != StateConnected {
		return ErrTerminalClosed
	}
	return session.writeJSON(map[string]any{"type": "input", "id": shellID, "d": data, "nl": false})
}

func (m *Manager) Resize(id string, rows, cols int) error {
	if rows < 1 || rows > 1000 || cols < 1 || cols > 2000 {
		return errors.New("terminal dimensions are out of range")
	}
	session, err := m.get(id)
	if err != nil {
		return err
	}
	session.mu.Lock()
	state := session.state
	shellID := session.shellID
	session.mu.Unlock()
	if state != StateConnected {
		return ErrTerminalClosed
	}
	return session.writeJSON(map[string]any{
		"type": "resize",
		"id":   shellID,
		"rows": rows,
		"cols": cols,
	})
}

func (m *Manager) Close(id string) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if session == nil {
		return ErrTerminalNotFound
	}
	session.mu.Lock()
	session.closed = true
	session.state = StateEnded
	session.revision++
	session.mu.Unlock()
	session.writeJSON(map[string]any{"type": "watch_remove", "id": session.shellID})
	return session.conn.Close()
}

func (m *Manager) Shutdown() {
	if m == nil {
		return
	}
	m.mu.Lock()
	sessions := make([]*bridgeSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	m.sessions = make(map[string]*bridgeSession)
	m.mu.Unlock()
	for _, session := range sessions {
		session.mu.Lock()
		session.closed = true
		session.state = StateEnded
		session.revision++
		session.mu.Unlock()
		session.conn.Close()
	}
}

func (m *Manager) get(id string) (*bridgeSession, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrTerminalNotFound
	}
	m.mu.RLock()
	session := m.sessions[id]
	m.mu.RUnlock()
	if session == nil {
		return nil, ErrTerminalNotFound
	}
	return session, nil
}

func (m *Manager) readPump(session *bridgeSession) {
	defer session.conn.Close()
	for {
		_, payload, err := session.conn.ReadMessage()
		if err != nil {
			session.mu.Lock()
			if !session.closed && session.state != StateEnded {
				session.state = StateUnknown
				session.lastError = "Core terminal WebSocket disconnected; remote state is unknown"
				session.revision++
			}
			session.mu.Unlock()
			return
		}
		var message struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Data    string `json:"d"`
			ShellID string `json:"shell_id"`
			Source  string `json:"src"`
			Submit  bool   `json:"submit"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		switch message.Type {
		case "terminal":
			if message.ID == session.shellID {
				session.appendOutput(message.Data)
			}
		case "terminal_done":
			if message.ID == session.shellID {
				session.markEnded("")
				return
			}
		case "shell_activity":
			if message.ShellID == session.shellID {
				session.recordActivity(message.Source, message.Submit)
			}
		}
	}
}

func (s *bridgeSession) appendOutput(data string) {
	if data == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state == StateEnded {
		return
	}
	remaining := maxOutputBytes - s.output.Len()
	if remaining <= 0 {
		s.droppedOutputBytes += uint64(len(data))
		s.revision++
		return
	}
	if len(data) > remaining {
		s.droppedOutputBytes += uint64(len(data) - remaining)
		cut := remaining
		for cut > 0 && !utf8.RuneStart(data[cut]) {
			cut--
		}
		s.droppedOutputBytes += uint64(remaining - cut)
		data = data[:cut]
	}
	s.output.WriteString(data)
	s.revision++
}

func (s *bridgeSession) recordActivity(source string, submit bool) {
	source = strings.TrimSpace(source)
	if source != "api" && source != "ai" {
		source = ""
	}
	s.mu.Lock()
	s.activitySource = source
	if submit {
		s.activitySource = ""
	}
	s.revision++
	s.mu.Unlock()
}

func (s *bridgeSession) markEnded(message string) {
	s.mu.Lock()
	if !s.closed {
		s.state = StateEnded
		s.lastError = message
		s.activitySource = ""
		s.revision++
	}
	s.mu.Unlock()
}

func (s *bridgeSession) writeJSON(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout)); err != nil {
		return err
	}
	return s.conn.WriteJSON(value)
}

func (s *bridgeSession) snapshotLocked() Snapshot {
	return Snapshot{
		ID:                 s.id,
		AgentSessionID:     s.agentSessionID,
		CoreSessionID:      s.coreSessionID,
		ShellID:            s.shellID,
		State:              s.state,
		ActivitySource:     s.activitySource,
		LastError:          s.lastError,
		DroppedOutputBytes: s.droppedOutputBytes,
		Revision:           s.revision,
	}
}

type historyResult struct {
	Data  string
	Start int64
	End   int64
	Total int64
}

func (m *Manager) history(ctx context.Context, origin, shellID string) (historyResult, error) {
	requestURL := origin + "/api/shells/" + url.PathEscape(shellID) +
		"/output-range?tail=1&max=" + strconv.Itoa(maxHistoryBytes)
	var response struct {
		Start int64  `json:"start"`
		End   int64  `json:"end"`
		Total int64  `json:"total"`
		Data  string `json:"d"`
	}
	if err := m.getJSON(ctx, requestURL, &response); err != nil {
		return historyResult{}, fmt.Errorf("read Core terminal history: %w", err)
	}
	return historyResult{
		Data:  response.Data,
		Start: response.Start,
		End:   response.End,
		Total: response.Total,
	}, nil
}

func (m *Manager) getJSON(ctx context.Context, requestURL string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := m.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("Core response exceeds %d bytes", maxResponseBytes)
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Core returned HTTP %d", response.StatusCode)
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decode Core response: %w", err)
	}
	return nil
}

func websocketURL(origin string) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	default:
		return "", errors.New("Core endpoint must use http or https")
	}
	parsed.Path = "/api/ui/ws"
	parsed.RawPath = ""
	return parsed.String(), nil
}
