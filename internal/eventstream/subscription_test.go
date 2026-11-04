package eventstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestSubscriptionReconnectsFromCursorWithoutDuplicates(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection := connections.Add(1)
		after, err := strconv.ParseUint(request.URL.Query().Get("after"), 10, 64)
		if err != nil {
			t.Errorf("after cursor: %v", err)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("response does not support flush")
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		if connection == 1 {
			if after != 0 {
				t.Errorf("first after = %d", after)
			}
			writeWindow(w, flusher, eventlog.Window{
				Events:         []eventlog.Event{testEvent(1)},
				LatestSequence: 1,
			})
			return
		}
		if after != 1 {
			t.Errorf("second after = %d", after)
		}
		writeWindow(w, flusher, eventlog.Window{
			Events:         []eventlog.Event{testEvent(2)},
			LatestSequence: 2,
		})
		<-request.Context().Done()
	}))
	defer server.Close()

	subscription, err := Open(context.Background(), server.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	seen := []eventlog.Sequence{}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && len(seen) < 2 {
		polled := subscription.Poll()
		for _, event := range polled.Events {
			seen = append(seen, event.Sequence)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fmt.Sprint(seen) != "[1 2]" {
		t.Fatalf("seen sequences = %v", seen)
	}
}

func TestSubscriptionSurfacesRetentionMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeWindow(w, flusher, eventlog.Window{
			Events:                   []eventlog.Event{testEvent(3)},
			LatestSequence:           5,
			DroppedDiagnostics:       2,
			DiagnosticDroppedThrough: 2,
			Truncated:                true,
			ControlOverflow:          true,
		})
		<-request.Context().Done()
	}))
	defer server.Close()

	subscription, err := Open(context.Background(), server.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		polled := subscription.Poll()
		if len(polled.Events) == 1 {
			if !polled.Truncated || !polled.ControlOverflow || polled.DroppedDiagnostics != 2 ||
				polled.DiagnosticDroppedThrough != 2 || polled.Cursor != 3 {
				t.Fatalf("polled = %+v", polled)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("subscription did not deliver event")
}

func TestClosedSubscriptionStops(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-request.Context().Done()
	}))
	defer server.Close()

	subscription, err := Open(context.Background(), server.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	subscription.Close()
	polled := subscription.Poll()
	if polled.State != StateStopped {
		t.Fatalf("state = %q", polled.State)
	}
}

func writeWindow(w http.ResponseWriter, flusher http.Flusher, window eventlog.Window) {
	payload, _ := json.Marshal(window)
	_, _ = fmt.Fprintf(w, "event: events\ndata: %s\n\n", payload)
	flusher.Flush()
}

func testEvent(sequence eventlog.Sequence) eventlog.Event {
	return eventlog.Event{
		EventID:    fmt.Sprintf("event-%d", sequence),
		Sequence:   sequence,
		OccurredAt: time.Now().UTC(),
		Type:       eventlog.TypeRuntimeStarted,
		Class:      eventlog.ClassControl,
	}
}
