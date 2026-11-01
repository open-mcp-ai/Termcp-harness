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
	maxResponseBytes    = 4 << 20
	maxHistoryBytes     = 512 << 10
	maxReconnectGap     = 4 << 20
	recoveryChunkBytes  = 256 << 10
	maxOutputBytes      = 1 << 20
	maxInputBytes       = 64 << 10
	maxTerminals        = 16
	reconnectMinDelay   = 200 * time.Millisecond
	reconnectMaxDelay   = 5 * time.Second
)

var (
	ErrTerminalNotFound      = errors.New("terminal bridge session not found")
	ErrTerminalClosed        = errors.New("terminal bridge session is closed")
	ErrTerminalReconnecting  = errors.New("terminal bridge session is reconnecting")
	ErrInvalidTarget         = errors.New("invalid terminal target")
	errTerminalWatchRejected = errors.New("Core terminal subscription rejected")
)

type State string

const (
	StateConnecting   State = "connecting"
	StateReconnecting State = "reconnecting"
	StateConnected    State = "connected"
	StateEnded        State = "ended"
	StateUnknown      State = "unknown"
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
	HistoryStart     int64  `json:"history_start"`
	HistoryEnd       int64  `json:"history_end"`
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
	OutputCursor       int64  `json:"output_cursor"`
	ReconnectAttempts  int    `json:"reconnect_attempts,omitempty"`
	Revision           uint64 `json:"revision"`
}

type PollResult struct {
	Snapshot
	Output string `json:"output,omitempty"`
}

type Manager struct {
	mu           sync.RWMutex
	sessions     map[string]*bridgeSession
	nextID       atomic.Uint64
	http         *http.Client
	dialer       *websocket.Dialer
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	reconnectMin time.Duration
	reconnectMax time.Duration
}

type bridgeSession struct {
	id             string
	agentSessionID string
	coreSessionID  string
	shellID        string
	origin         string
	conn           *websocket.Conn

	mu                 sync.Mutex
	writeMu            sync.Mutex
	output             strings.Builder
	droppedOutputBytes uint64
	state              State
	lastError          string
	activitySource     string
	outputCursor       int64
	reconnectAttempts  int
	revision           uint64
	closed             bool
}

func NewManager() *Manager {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		sessions:     make(map[string]*bridgeSession),
		http:         &http.Client{Timeout: defaultHTTPTimeout, Transport: transport},
		ctx:          ctx,
		cancel:       cancel,
		reconnectMin: reconnectMinDelay,
		reconnectMax: reconnectMaxDelay,
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
		origin:         origin,
		conn:           conn,
		state:          StateConnecting,
	}

	if err := session.writeJSON(map[string]any{"type": "watch_add", "id": options.ShellID}); err != nil {
		conn.Close()
		return OpenResult{}, fmt.Errorf("subscribe Core terminal output: %w", err)
	}
	cursor, err := awaitWatchReady(ctx, conn, options.ShellID)
	if err != nil {
		conn.Close()
		return OpenResult{}, fmt.Errorf("confirm Core terminal subscription: %w", err)
	}
	history, err := m.history(ctx, origin, options.ShellID, cursor)
	if err != nil {
		conn.Close()
		return OpenResult{}, err
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
	session.outputCursor = cursor
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

	m.startReadPump(session, conn)

	return OpenResult{
		ID:               id,
		AgentSessionID:   options.AgentSessionID,
		CoreSessionID:    options.CoreSessionID,
		ShellID:          options.ShellID,
		State:            StateConnected,
		History:          history.Data,
		HistoryBytes:     len(history.Data),
		HistoryStart:     history.Start,
		HistoryEnd:       history.End,
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
	if state == StateReconnecting {
		return ErrTerminalReconnecting
	}
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
	if state == StateReconnecting {
		return ErrTerminalReconnecting
	}
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
	return session.closeConnection()
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
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	for _, session := range sessions {
		session.mu.Lock()
		session.closed = true
		session.state = StateEnded
		session.revision++
		session.mu.Unlock()
		_ = session.closeConnection()
	}
	m.wg.Wait()
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

func (m *Manager) startReadPump(session *bridgeSession, conn *websocket.Conn) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.readPump(session, conn)
	}()
}

func (m *Manager) startReconnect(session *bridgeSession) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.reconnect(session)
	}()
}

func (m *Manager) readPump(session *bridgeSession, conn *websocket.Conn) {
	defer conn.Close()
	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if session.beginReconnect("Core terminal WebSocket disconnected; reconnecting") {
				m.startReconnect(session)
			}
			return
		}
		var message struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Data    string `json:"d"`
			Start   *int64 `json:"start"`
			End     *int64 `json:"end"`
			Cursor  *int64 `json:"cursor"`
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
				if err := session.appendOutputRange(message.Data, message.Start, message.End); err != nil {
					session.markUnknown(err.Error())
					return
				}
			}
		case "terminal_done":
			if message.ID == session.shellID {
				if err := session.finishAt(message.Cursor); err != nil {
					session.markUnknown(err.Error())
				}
				return
			}
		case "shell_activity":
			if message.ShellID == session.shellID {
				session.recordActivity(message.Source, message.Submit)
			}
		}
	}
}

func (m *Manager) reconnect(session *bridgeSession) {
	delay := m.reconnectMin
	if delay <= 0 {
		delay = reconnectMinDelay
	}
	maxDelay := m.reconnectMax
	if maxDelay < delay {
		maxDelay = reconnectMaxDelay
	}

	for attempt := 1; ; attempt++ {
		if m.ctx.Err() != nil || session.isClosed() {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-m.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		session.setReconnectAttempt(attempt)
		attemptContext, cancel := context.WithTimeout(m.ctx, 2*defaultHTTPTimeout)
		conn, cursor, err := m.connectWatch(attemptContext, session.origin, session.shellID)
		cancel()
		if err == nil {
			from := session.currentCursor()
			if cursor < from {
				_ = conn.Close()
				session.markUnknown(fmt.Sprintf(
					"terminal reconnect cursor %d moved behind consumed byte %d",
					cursor,
					from,
				))
				return
			}
			var recovered string
			recovered, err = m.recoverRange(m.ctx, session.origin, session.shellID, from, cursor)
			if err != nil {
				_ = conn.Close()
				session.markUnknown(fmt.Sprintf("terminal reconnect gap recovery failed: %v", err))
				return
			}
			if !session.installReconnected(conn, cursor, recovered) {
				_ = conn.Close()
				return
			}
			m.startReadPump(session, conn)
			return
		}
		if errors.Is(err, errTerminalWatchRejected) {
			session.markUnknown(err.Error())
			return
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
}

func (m *Manager) connectWatch(ctx context.Context, origin, shellID string) (*websocket.Conn, int64, error) {
	wsURL, err := websocketURL(origin)
	if err != nil {
		return nil, 0, err
	}
	conn, response, err := m.dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, 0, fmt.Errorf("connect Core terminal WebSocket: %w", err)
	}
	conn.SetReadLimit(2 << 20)
	if err := writeConnJSON(conn, map[string]any{"type": "watch_add", "id": shellID}); err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("subscribe Core terminal output: %w", err)
	}
	cursor, err := awaitWatchReady(ctx, conn, shellID)
	if err != nil {
		_ = conn.Close()
		return nil, 0, fmt.Errorf("confirm Core terminal subscription: %w", err)
	}
	return conn, cursor, nil
}

func (m *Manager) recoverRange(
	ctx context.Context,
	origin string,
	shellID string,
	from int64,
	to int64,
) (string, error) {
	if from < 0 || to < from {
		return "", fmt.Errorf("invalid reconnect range %d..%d", from, to)
	}
	if to-from > maxReconnectGap {
		return "", fmt.Errorf(
			"reconnect gap %d..%d exceeds recovery limit %d",
			from,
			to,
			maxReconnectGap,
		)
	}
	var recovered strings.Builder
	offset := from
	for offset < to {
		maxBytes := int64(recoveryChunkBytes)
		if remaining := to - offset; remaining < maxBytes {
			maxBytes = remaining
		}
		response, err := m.outputRange(ctx, origin, shellID, offset, int(maxBytes))
		if err != nil {
			return "", err
		}
		dataBytes := int64(len([]byte(response.Data)))
		if response.Start != offset || response.End != offset+dataBytes ||
			dataBytes == 0 || response.End > to {
			return "", fmt.Errorf(
				"output range %d..%d did not cover expected reconnect window %d..%d",
				response.Start,
				response.End,
				offset,
				to,
			)
		}
		recovered.WriteString(response.Data)
		offset = response.End
	}
	if offset != to || int64(recovered.Len()) != to-from {
		return "", fmt.Errorf("reconnect recovery stopped at byte %d, want %d", offset, to)
	}
	return recovered.String(), nil
}

func (s *bridgeSession) beginReconnect(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state == StateEnded || s.state == StateUnknown || s.state == StateReconnecting {
		return false
	}
	s.state = StateReconnecting
	s.lastError = boundedMessage(reason)
	s.activitySource = ""
	s.reconnectAttempts = 0
	s.revision++
	return true
}

func (s *bridgeSession) setReconnectAttempt(attempt int) {
	s.mu.Lock()
	if !s.closed && s.state == StateReconnecting {
		s.reconnectAttempts = attempt
		s.lastError = fmt.Sprintf("terminal reconnect attempt %d; remote state is unknown", attempt)
		s.revision++
	}
	s.mu.Unlock()
}

func (s *bridgeSession) currentCursor() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.outputCursor
}

func (s *bridgeSession) installReconnected(conn *websocket.Conn, cursor int64, recovered string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state != StateReconnecting || s.outputCursor > cursor {
		return false
	}
	if s.outputCursor < cursor {
		s.appendOutputLocked(recovered)
	}
	s.conn = conn
	s.outputCursor = cursor
	s.state = StateConnected
	s.lastError = ""
	s.activitySource = ""
	s.reconnectAttempts = 0
	s.revision++
	return true
}

func (s *bridgeSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.state == StateEnded || s.state == StateUnknown
}

func (s *bridgeSession) appendOutputRange(data string, start, end *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.state == StateEnded {
		return nil
	}
	if s.state == StateUnknown {
		return errors.New("Core terminal bridge is already unknown")
	}
	if start == nil || end == nil {
		return errors.New("Core terminal frame omitted its byte range")
	}
	if *start != s.outputCursor || *end < *start || *end-*start != int64(len(data)) {
		return fmt.Errorf(
			"Core terminal frame range %d..%d is not contiguous with byte %d for %d bytes",
			*start,
			*end,
			s.outputCursor,
			len(data),
		)
	}
	s.appendOutputLocked(data)
	s.outputCursor = *end
	if data != "" {
		s.revision++
	}
	return nil
}

func (s *bridgeSession) appendOutputLocked(data string) {
	remaining := maxOutputBytes - s.output.Len()
	if remaining <= 0 {
		s.droppedOutputBytes += uint64(len(data))
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
}

func (s *bridgeSession) finishAt(cursor *int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if cursor == nil || *cursor != s.outputCursor {
		got := int64(-1)
		if cursor != nil {
			got = *cursor
		}
		return fmt.Errorf(
			"Core terminal finished at byte %d after bridge consumed byte %d",
			got,
			s.outputCursor,
		)
	}
	s.state = StateEnded
	s.lastError = ""
	s.activitySource = ""
	s.reconnectAttempts = 0
	s.revision++
	return nil
}

func (s *bridgeSession) markUnknown(message string) {
	s.mu.Lock()
	if !s.closed {
		s.state = StateUnknown
		s.lastError = boundedMessage(message)
		s.activitySource = ""
		s.revision++
	}
	s.mu.Unlock()
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

func (s *bridgeSession) writeJSON(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	conn := s.conn
	closed := s.closed
	s.mu.Unlock()
	if closed || conn == nil {
		return ErrTerminalClosed
	}
	if err := conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(value)
}

func (s *bridgeSession) closeConnection() error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return nil
	}
	return conn.Close()
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
		OutputCursor:       s.outputCursor,
		ReconnectAttempts:  s.reconnectAttempts,
		Revision:           s.revision,
	}
}

type historyResult struct {
	Data  string
	Start int64
	End   int64
	Total int64
}

func awaitWatchReady(ctx context.Context, conn *websocket.Conn, shellID string) (int64, error) {
	deadline := time.Now().Add(defaultHTTPTimeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return 0, err
	}
	defer conn.SetReadDeadline(time.Time{})

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		var message struct {
			Type   string `json:"type"`
			ID     string `json:"id"`
			Cursor *int64 `json:"cursor"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		if message.ID != shellID {
			continue
		}
		switch message.Type {
		case "watch_ready":
			if message.Cursor == nil || *message.Cursor < 0 {
				return 0, errors.New("Core watch_ready omitted a valid cursor")
			}
			return *message.Cursor, nil
		case "watch_error":
			if strings.TrimSpace(message.Error) == "" {
				return 0, errTerminalWatchRejected
			}
			return 0, fmt.Errorf("%w: %s", errTerminalWatchRejected, message.Error)
		}
	}
}

type outputRangeResponse struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"`
	Total int64  `json:"total"`
	Data  string `json:"d"`
}

func (m *Manager) outputRange(
	ctx context.Context,
	origin string,
	shellID string,
	start int64,
	maxBytes int,
) (outputRangeResponse, error) {
	requestURL := origin + "/api/shells/" + url.PathEscape(shellID) +
		"/output-range?start=" + strconv.FormatInt(start, 10) +
		"&max=" + strconv.Itoa(maxBytes)
	var response outputRangeResponse
	if err := m.getJSON(ctx, requestURL, &response); err != nil {
		return outputRangeResponse{}, err
	}
	return response, nil
}

func (m *Manager) history(ctx context.Context, origin, shellID string, cursor int64) (historyResult, error) {
	if cursor < 0 {
		return historyResult{}, errors.New("Core terminal cursor must not be negative")
	}
	start := cursor - maxHistoryBytes
	if start < 0 {
		start = 0
	}
	response, err := m.outputRange(ctx, origin, shellID, start, maxHistoryBytes)
	if err != nil {
		return historyResult{}, fmt.Errorf("read Core terminal history: %w", err)
	}
	if response.End != cursor {
		return historyResult{}, fmt.Errorf(
			"Core terminal history ended at byte %d, subscription started at %d",
			response.End,
			cursor,
		)
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

func writeConnJSON(conn *websocket.Conn, value any) error {
	if err := conn.SetWriteDeadline(time.Now().Add(defaultWriteTimeout)); err != nil {
		return err
	}
	return conn.WriteJSON(value)
}

func boundedMessage(value string) string {
	const maxBytes = 512
	if len(value) <= maxBytes {
		return value
	}
	return value[:maxBytes] + "…"
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
