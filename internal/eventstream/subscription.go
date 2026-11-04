package eventstream

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

const (
	defaultReconnectMin = 500 * time.Millisecond
	defaultReconnectMax = 5 * time.Second
	defaultQueueLimit   = 2048
	defaultHeaderLimit  = 256 << 10
)

type State string

const (
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateReconnecting State = "reconnecting"
	StateLagged       State = "lagged"
	StateStopped      State = "stopped"
)

type Snapshot struct {
	State                    State             `json:"state"`
	Cursor                   eventlog.Sequence `json:"cursor"`
	LatestSequence           eventlog.Sequence `json:"latest_sequence"`
	DroppedDiagnostics       uint64            `json:"dropped_diagnostics"`
	DiagnosticDroppedThrough eventlog.Sequence `json:"diagnostic_dropped_through,omitempty"`
	Truncated                bool              `json:"truncated"`
	ControlOverflow          bool              `json:"control_overflow"`
	Lagged                   bool              `json:"lagged"`
	LastError                string            `json:"last_error,omitempty"`
	Revision                 uint64            `json:"revision"`
}

type PollResult struct {
	Snapshot
	Events []eventlog.Event `json:"events"`
}

type Subscription struct {
	ctx    context.Context
	cancel context.CancelFunc
	http   *http.Client
	url    string

	mu                       sync.Mutex
	state                    State
	cursor                   eventlog.Sequence
	latestSequence           eventlog.Sequence
	droppedDiagnostics       uint64
	diagnosticDroppedThrough eventlog.Sequence
	truncated                bool
	controlOverflow          bool
	lagged                   bool
	lastError                string
	revision                 uint64
	events                   []eventlog.Event
}

func Open(ctx context.Context, endpoint string, after eventlog.Sequence) (*Subscription, error) {
	origin, err := coreinstance.ValidateEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	streamURL, err := eventStreamURL(origin, after)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	subscriptionContext, cancel := context.WithCancel(ctx)
	subscription := &Subscription{
		ctx:    subscriptionContext,
		cancel: cancel,
		http: &http.Client{
			Transport: transport,
		},
		url:    streamURL,
		state:  StateConnecting,
		cursor: after,
	}
	go subscription.run()
	return subscription, nil
}

func (s *Subscription) Poll() PollResult {
	if s == nil {
		return PollResult{Snapshot: Snapshot{State: StateStopped}}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := make([]eventlog.Event, len(s.events))
	copy(events, s.events)
	s.events = s.events[:0]
	return PollResult{
		Snapshot: s.snapshotLocked(),
		Events:   events,
	}
}

func (s *Subscription) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{State: StateStopped}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Subscription) Close() {
	if s == nil {
		return
	}
	s.cancel()
	s.mu.Lock()
	if s.state != StateStopped {
		s.state = StateStopped
		s.revision++
	}
	s.mu.Unlock()
}

func (s *Subscription) run() {
	backoff := defaultReconnectMin
	for {
		if s.ctx.Err() != nil {
			s.stop()
			return
		}
		s.setConnectionState(StateConnecting, "")
		response, err := s.connect()
		if err != nil {
			s.setConnectionState(StateReconnecting, err.Error())
			if !wait(s.ctx, backoff) {
				s.stop()
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}
		backoff = defaultReconnectMin
		s.setConnectionState(StateConnected, "")
		err = s.read(response.Body)
		response.Body.Close()
		if s.ctx.Err() != nil {
			s.stop()
			return
		}
		s.setConnectionState(StateReconnecting, err.Error())
		if !wait(s.ctx, backoff) {
			s.stop()
			return
		}
	}
}

func (s *Subscription) connect() (*http.Response, error) {
	s.mu.Lock()
	cursor := s.cursor
	s.mu.Unlock()
	streamURL, err := eventStreamURL(s.urlOrigin(), cursor)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(s.ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", strconv.FormatUint(uint64(cursor), 10))
	response, err := s.http.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, fmt.Errorf("event stream returned HTTP %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		response.Body.Close()
		return nil, fmt.Errorf("event stream content type is %q", contentType)
	}
	return response, nil
}

func (s *Subscription) read(body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), defaultHeaderLimit)
	eventName := ""
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if eventName == "events" && data.Len() > 0 {
				var window eventlog.Window
				if err := json.Unmarshal([]byte(data.String()), &window); err != nil {
					return fmt.Errorf("decode event stream window: %w", err)
				}
				s.applyWindow(window)
			}
			eventName = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			eventName = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

func (s *Subscription) applyWindow(window eventlog.Window) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latestSequence = window.LatestSequence
	s.droppedDiagnostics = window.DroppedDiagnostics
	s.diagnosticDroppedThrough = window.DiagnosticDroppedThrough
	s.truncated = s.truncated || window.Truncated
	s.controlOverflow = s.controlOverflow || window.ControlOverflow
	for _, event := range window.Events {
		if event.Sequence <= s.cursor {
			continue
		}
		if len(s.events) >= defaultQueueLimit {
			s.lagged = true
			s.state = StateLagged
			s.lastError = "local event queue exceeded"
			s.revision++
			continue
		}
		s.events = append(s.events, event)
		s.cursor = event.Sequence
	}
	s.revision++
}

func (s *Subscription) setConnectionState(state State, lastError string) {
	s.mu.Lock()
	if s.lagged {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.lastError = lastError
	s.revision++
	s.mu.Unlock()
}

func (s *Subscription) stop() {
	s.mu.Lock()
	if s.state != StateStopped {
		s.state = StateStopped
		s.revision++
	}
	s.mu.Unlock()
}

func (s *Subscription) snapshotLocked() Snapshot {
	return Snapshot{
		State:                    s.state,
		Cursor:                   s.cursor,
		LatestSequence:           s.latestSequence,
		DroppedDiagnostics:       s.droppedDiagnostics,
		DiagnosticDroppedThrough: s.diagnosticDroppedThrough,
		Truncated:                s.truncated,
		ControlOverflow:          s.controlOverflow,
		Lagged:                   s.lagged,
		LastError:                s.lastError,
		Revision:                 s.revision,
	}
}

func (s *Subscription) urlOrigin() string {
	parsed, _ := url.Parse(s.url)
	parsed.Path = ""
	parsed.RawQuery = ""
	return parsed.String()
}

func eventStreamURL(origin string, after eventlog.Sequence) (string, error) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	parsed.Path = "/v1/events/stream"
	parsed.RawPath = ""
	query := parsed.Query()
	query.Set("after", strconv.FormatUint(uint64(after), 10))
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func nextBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > defaultReconnectMax {
		return defaultReconnectMax
	}
	return next
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
