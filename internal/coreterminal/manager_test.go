package coreterminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestManagerStreamsOrderedOutputAndActivity(t *testing.T) {
	var (
		mu       sync.Mutex
		received []map[string]any
	)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/shells/shell-1/output-range":
			if got := request.URL.Query().Get("tail"); got != "1" {
				t.Errorf("tail = %q", got)
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
			for range 2 {
				var message map[string]any
				if err := conn.ReadJSON(&message); err != nil {
					return
				}
				mu.Lock()
				received = append(received, message)
				mu.Unlock()
			}
			_ = conn.WriteJSON(map[string]any{"type": "sessions", "sessions": []any{}})
			_ = conn.WriteJSON(map[string]any{"type": "terminal", "id": "shell-1", "d": "one\r\n"})
			_ = conn.WriteJSON(map[string]any{"type": "terminal", "id": "other", "d": "ignored"})
			_ = conn.WriteJSON(map[string]any{"type": "terminal", "id": "shell-1", "d": "two\r\n"})
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
					_ = conn.WriteJSON(map[string]any{"type": "terminal_done", "id": "shell-1"})
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
	if opened.History != "history\r\n" || !opened.HistoryTruncated || opened.State != StateConnected {
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

func TestManagerBoundsOutputAndMarksUnknownOnDisconnect(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/shells/shell-1/output-range":
			writeTestJSON(t, w, map[string]any{"start": 0, "end": 0, "total": 0, "d": ""})
		case "/api/ui/ws":
			conn, err := upgrader.Upgrade(w, request, nil)
			if err != nil {
				return
			}
			for range 2 {
				var message map[string]any
				if err := conn.ReadJSON(&message); err != nil {
					_ = conn.Close()
					return
				}
			}
			_ = conn.Close()
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
		if polled.State == StateUnknown {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("terminal did not become unknown after Core WebSocket closed")
}

func TestBridgeOutputIsBoundedAndDropsAreVisible(t *testing.T) {
	session := &bridgeSession{state: StateConnected}
	session.appendOutput(strings.Repeat("a", maxOutputBytes))
	session.appendOutput("tail")
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

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
