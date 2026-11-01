package coreterminal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestManagerListsCoreSessionShells(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/sessions/core-1/shells" {
			http.NotFound(w, request)
			return
		}
		writeTestJSON(t, w, map[string]any{
			"shells": []any{
				map[string]any{"id": "shell-1", "index": 1, "name": "main", "status": "running", "mode": "pty"},
				map[string]any{"id": "shell-2", "index": 2, "name": "old", "status": "exited", "mode": "pty"},
			},
		})
	}))
	defer server.Close()

	shells, err := NewManager().Shells(context.Background(), server.URL, "core-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(shells) != 2 || shells[0].ID != "shell-1" || shells[1].Status != "exited" {
		t.Fatalf("shells = %+v", shells)
	}
}

func TestManagerRequiresWatchReadyCursor(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/ui/ws" {
			http.NotFound(w, request)
			return
		}
		conn, err := upgrader.Upgrade(w, request, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var watch map[string]any
		if err := conn.ReadJSON(&watch); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"type": "sessions", "sessions": []any{}})
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := NewManager().Open(ctx, server.URL, OpenOptions{
		AgentSessionID: "agent-1",
		CoreSessionID:  "core-1",
		ShellID:        "shell-1",
		Rows:           24,
		Cols:           80,
	})
	if err == nil || !strings.Contains(err.Error(), "confirm Core terminal subscription") {
		t.Fatalf("Open() error = %v", err)
	}
}

func TestManagerStreamsOrderedOutputAndActivity(t *testing.T) {
	var (
		mu       sync.Mutex
		received []map[string]any
	)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/shells/shell-1/output-range":
			if got := request.URL.Query().Get("start"); got != "0" {
				t.Errorf("history start = %q", got)
			}
			if got := request.URL.Query().Get("max"); got != "524288" {
				t.Errorf("history max = %q", got)
			}
			writeTestJSON(t, w, map[string]any{
				"start": 4, "end": 12, "total": 12, "d": "history\r\n",
			})
		case "/api/ui/ws":
			conn, err := upgrader.Upgrade(w, request, nil)
			if err != nil {
				t.Errorf("upgrade: %v", err)
				return
			}
			defer conn.Close()
			var watch map[string]any
			if err := conn.ReadJSON(&watch); err != nil {
				return
			}
			mu.Lock()
			received = append(received, watch)
			mu.Unlock()
			_ = conn.WriteJSON(map[string]any{"type": "sessions", "sessions": []any{}})
			_ = conn.WriteJSON(map[string]any{"type": "watch_ready", "id": "shell-1", "cursor": 12})
			var resize map[string]any
			if err := conn.ReadJSON(&resize); err != nil {
				return
			}
			mu.Lock()
			received = append(received, resize)
			mu.Unlock()
			_ = conn.WriteJSON(map[string]any{"type": "terminal", "id": "shell-1", "d": "one\r\n", "start": 12, "end": 17})
			_ = conn.WriteJSON(map[string]any{"type": "terminal", "id": "other", "d": "ignored", "start": 12, "end": 19})
			_ = conn.WriteJSON(map[string]any{"type": "terminal", "id": "shell-1", "d": "two\r\n", "start": 17, "end": 22})
			_ = conn.WriteJSON(map[string]any{"type": "shell_activity", "shell_id": "shell-1", "src": "ai", "submit": false})
			for {
				var message map[string]any
				if err := conn.ReadJSON(&message); err != nil {
					return
				}
				mu.Lock()
				received = append(received, message)
				mu.Unlock()
				if message["type"] == "input" {
					_ = conn.WriteJSON(map[string]any{"type": "shell_activity", "shell_id": "shell-1", "src": "api", "submit": true})
					_ = conn.WriteJSON(map[string]any{"type": "terminal_done", "id": "shell-1", "cursor": 22})
					return
				}
			}
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	manager := NewManager()
	defer manager.Shutdown()
	opened, err := manager.Open(context.Background(), server.URL, OpenOptions{
		AgentSessionID: "agent-1",
		CoreSessionID:  "core-1",
		ShellID:        "shell-1",
		Rows:           30,
		Cols:           120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened.History != "history\r\n" || opened.HistoryStart != 4 || opened.HistoryEnd != 12 ||
		!opened.HistoryTruncated || opened.State != StateConnected {
		t.Fatalf("opened = %+v", opened)
	}

	var output strings.Builder
	deadline := time.Now().Add(2 * time.Second)
	for output.String() != "one\r\ntwo\r\n" && time.Now().Before(deadline) {
		polled, err := manager.Poll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(polled.Output)
		if polled.ActivitySource == "ai" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := output.String(); got != "one\r\ntwo\r\n" {
		t.Fatalf("output = %q", got)
	}
	if err := manager.Input(opened.ID, "echo hi\r"); err != nil {
		t.Fatal(err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		polled, err := manager.Poll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		if polled.State == StateEnded && polled.ActivitySource == "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) < 3 {
		t.Fatalf("received frames = %+v", received)
	}
	watch := received[0]
	resize := received[1]
	input := received[2]
	if watch["type"] != "watch_add" || watch["id"] != "shell-1" {
		t.Fatalf("watch frame = %+v", watch)
	}
	if resize["type"] != "resize" || resize["rows"] != float64(30) || resize["cols"] != float64(120) {
		t.Fatalf("resize frame = %+v", resize)
	}
	if input["type"] != "input" || input["d"] != "echo hi\r" {
		t.Fatalf("input frame = %+v", input)
	}
}

func TestManagerMarksReconnectingOnDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	blockReconnect := make(chan struct{})
	var connectionCount int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/shells/shell-1/output-range":
			writeTestJSON(t, w, map[string]any{"start": 0, "end": 0, "total": 0, "d": ""})
		case "/api/ui/ws":
			conn, err := upgrader.Upgrade(w, request, nil)
			if err != nil {
				return
			}
			var watch map[string]any
			if err := conn.ReadJSON(&watch); err != nil {
				_ = conn.Close()
				return
			}
			mu.Lock()
			connectionCount++
			current := connectionCount
			mu.Unlock()
			if current > 1 {
				<-blockReconnect
				_ = conn.Close()
				return
			}
			_ = conn.WriteJSON(map[string]any{"type": "watch_ready", "id": "shell-1", "cursor": 0})
			var resize map[string]any
			if err := conn.ReadJSON(&resize); err != nil {
				_ = conn.Close()
				return
			}
			_ = conn.Close()
		default:
			http.NotFound(w, request)
		}
	}))
	manager := NewManager()
	defer func() {
		close(blockReconnect)
		manager.Shutdown()
		server.Close()
	}()
	opened, err := manager.Open(context.Background(), server.URL, OpenOptions{
		AgentSessionID: "agent-1",
		CoreSessionID:  "core-1",
		ShellID:        "shell-1",
		Rows:           24,
		Cols:           80,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		polled, err := manager.Poll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		if polled.State == StateReconnecting && polled.ReconnectAttempts > 0 {
			if err := manager.Input(opened.ID, "x"); !errors.Is(err, ErrTerminalReconnecting) {
				t.Fatalf("Input() during reconnect error = %v", err)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("terminal did not enter reconnecting after Core WebSocket closed")
}

func TestManagerReconnectsAndRecoversExactOutputGap(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	secondDone := make(chan struct{})
	var connectionCount int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/shells/shell-1/output-range":
			start := request.URL.Query().Get("start")
			maxBytes := request.URL.Query().Get("max")
			switch {
			case start == "0" && maxBytes == "524288":
				writeTestJSON(t, w, map[string]any{"start": 0, "end": 0, "total": 0, "d": ""})
			case start == "3" && maxBytes == "2":
				writeTestJSON(t, w, map[string]any{"start": 3, "end": 5, "total": 7, "d": "de"})
			default:
				http.Error(w, "unexpected output range", http.StatusBadRequest)
			}
		case "/api/ui/ws":
			conn, err := upgrader.Upgrade(w, request, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var watch map[string]any
			if err := conn.ReadJSON(&watch); err != nil {
				return
			}
			mu.Lock()
			connectionCount++
			current := connectionCount
			mu.Unlock()
			switch current {
			case 1:
				_ = conn.WriteJSON(map[string]any{"type": "watch_ready", "id": "shell-1", "cursor": 0})
				var resize map[string]any
				if err := conn.ReadJSON(&resize); err != nil {
					return
				}
				_ = conn.WriteJSON(map[string]any{
					"type": "terminal", "id": "shell-1", "d": "abc", "start": 0, "end": 3,
				})
				_ = conn.Close()
			case 2:
				_ = conn.WriteJSON(map[string]any{"type": "watch_ready", "id": "shell-1", "cursor": 5})
				_ = conn.WriteJSON(map[string]any{
					"type": "terminal", "id": "shell-1", "d": "fg", "start": 5, "end": 7,
				})
				<-secondDone
			}
		default:
			http.NotFound(w, request)
		}
	}))
	manager := NewManager()
	manager.reconnectMin = 5 * time.Millisecond
	manager.reconnectMax = 20 * time.Millisecond
	defer func() {
		close(secondDone)
		manager.Shutdown()
		server.Close()
	}()
	opened, err := manager.Open(context.Background(), server.URL, OpenOptions{
		AgentSessionID: "agent-1",
		CoreSessionID:  "core-1",
		ShellID:        "shell-1",
		Rows:           24,
		Cols:           80,
	})
	if err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		polled, err := manager.Poll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(polled.Output)
		if output.String() == "abcdefg" {
			if polled.State != StateConnected || polled.OutputCursor != 7 ||
				polled.ReconnectAttempts != 0 || polled.LastError != "" {
				t.Fatalf("reconnected snapshot = %+v", polled.Snapshot)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reconnected output = %q, want abcdefg", output.String())
}

func TestManagerReconnectGapFailsClosed(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	secondDone := make(chan struct{})
	var connectionCount int
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/shells/shell-1/output-range":
			switch request.URL.Query().Get("start") {
			case "0":
				writeTestJSON(t, w, map[string]any{"start": 0, "end": 0, "total": 5, "d": ""})
			case "3":
				writeTestJSON(t, w, map[string]any{"start": 4, "end": 5, "total": 5, "d": "e"})
			default:
				http.Error(w, "unexpected output range", http.StatusBadRequest)
			}
		case "/api/ui/ws":
			conn, err := upgrader.Upgrade(w, request, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			var watch map[string]any
			if err := conn.ReadJSON(&watch); err != nil {
				return
			}
			mu.Lock()
			connectionCount++
			current := connectionCount
			mu.Unlock()
			if current == 1 {
				_ = conn.WriteJSON(map[string]any{"type": "watch_ready", "id": "shell-1", "cursor": 0})
				var resize map[string]any
				if err := conn.ReadJSON(&resize); err != nil {
					return
				}
				_ = conn.WriteJSON(map[string]any{
					"type": "terminal", "id": "shell-1", "d": "abc", "start": 0, "end": 3,
				})
				_ = conn.Close()
			} else {
				_ = conn.WriteJSON(map[string]any{"type": "watch_ready", "id": "shell-1", "cursor": 5})
				<-secondDone
			}
		default:
			http.NotFound(w, request)
		}
	}))
	manager := NewManager()
	manager.reconnectMin = 5 * time.Millisecond
	manager.reconnectMax = 20 * time.Millisecond
	defer func() {
		close(secondDone)
		manager.Shutdown()
		server.Close()
	}()
	opened, err := manager.Open(context.Background(), server.URL, OpenOptions{
		AgentSessionID: "agent-1",
		CoreSessionID:  "core-1",
		ShellID:        "shell-1",
		Rows:           24,
		Cols:           80,
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		polled, err := manager.Poll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		if polled.State == StateUnknown {
			if !strings.Contains(polled.LastError, "gap recovery failed") ||
				polled.OutputCursor != 3 {
				t.Fatalf("failed reconnect snapshot = %+v", polled.Snapshot)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("unrecoverable reconnect gap did not fail closed")
}

func TestManagerRecoversGapInBoundedPages(t *testing.T) {
	payload := strings.Repeat("x", recoveryChunkBytes+17)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/shells/shell-1/output-range" {
			http.NotFound(w, request)
			return
		}
		requests++
		start, _ := strconv.Atoi(request.URL.Query().Get("start"))
		maxBytes, _ := strconv.Atoi(request.URL.Query().Get("max"))
		if start < 0 || start > len(payload) || maxBytes <= 0 {
			http.Error(w, "invalid range", http.StatusBadRequest)
			return
		}
		end := start + maxBytes
		if end > len(payload) {
			end = len(payload)
		}
		writeTestJSON(t, w, map[string]any{
			"start": start,
			"end":   end,
			"total": len(payload),
			"d":     payload[start:end],
		})
	}))
	defer server.Close()
	manager := NewManager()
	defer manager.Shutdown()
	recovered, err := manager.recoverRange(
		context.Background(),
		server.URL,
		"shell-1",
		0,
		int64(len(payload)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || recovered != payload {
		t.Fatalf("requests = %d, recovered bytes = %d", requests, len(recovered))
	}
	if _, err := manager.recoverRange(
		context.Background(),
		server.URL,
		"shell-1",
		0,
		maxReconnectGap+1,
	); err == nil || !strings.Contains(err.Error(), "exceeds recovery limit") {
		t.Fatalf("oversized gap error = %v", err)
	}
}

func TestBridgeOutputIsBoundedAndDropsAreVisible(t *testing.T) {
	session := &bridgeSession{state: StateConnected}
	start, end := int64(0), int64(maxOutputBytes)
	if err := session.appendOutputRange(strings.Repeat("a", maxOutputBytes), &start, &end); err != nil {
		t.Fatal(err)
	}
	start, end = end, end+4
	if err := session.appendOutputRange("tail", &start, &end); err != nil {
		t.Fatal(err)
	}
	polled, err := (&Manager{sessions: map[string]*bridgeSession{"t": session}}).Poll("t")
	if err != nil {
		t.Fatal(err)
	}
	if len(polled.Output) != maxOutputBytes {
		t.Fatalf("output bytes = %d", len(polled.Output))
	}
	if polled.DroppedOutputBytes != 4 {
		t.Fatalf("dropped = %d", polled.DroppedOutputBytes)
	}
}

func TestBridgeRejectsTerminalOutputCursorGap(t *testing.T) {
	session := &bridgeSession{state: StateConnected}
	start, end := int64(0), int64(2)
	if err := session.appendOutputRange("ab", &start, &end); err != nil {
		t.Fatal(err)
	}
	start, end = 3, 5
	err := session.appendOutputRange("cd", &start, &end)
	if err == nil || !strings.Contains(err.Error(), "not contiguous") {
		t.Fatalf("gap error = %v", err)
	}
	session.markUnknown(err.Error())
	polled, pollErr := (&Manager{sessions: map[string]*bridgeSession{"t": session}}).Poll("t")
	if pollErr != nil {
		t.Fatal(pollErr)
	}
	if polled.State != StateUnknown || polled.Output != "ab" || polled.OutputCursor != 2 {
		t.Fatalf("polled = %+v", polled)
	}
}

func TestBridgeRejectsEmptyTerminalRangeAwayFromCursor(t *testing.T) {
	session := &bridgeSession{state: StateConnected}
	start, end := int64(2), int64(2)
	err := session.appendOutputRange("", &start, &end)
	if err == nil || !strings.Contains(err.Error(), "not contiguous") {
		t.Fatalf("empty gap error = %v", err)
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
