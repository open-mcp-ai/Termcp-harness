package controlapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
)

func TestEventStreamSendsInitialWindowAndLiveEvents(t *testing.T) {
	journal := eventlog.New()
	if _, err := journal.Append(eventlog.Entry{
		Type:  eventlog.TypeRuntimeStarted,
		Class: eventlog.ClassControl,
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(runningRuntime(t), WithEventLog(journal)))
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/events/stream?after=0")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/event-stream") {
		t.Fatalf("Content-Type = %q", contentType)
	}

	scanner := bufio.NewScanner(response.Body)
	first := readEventWindow(t, scanner)
	if len(first.Events) != 1 || first.Events[0].Sequence != 1 {
		t.Fatalf("initial window = %+v", first)
	}

	if _, err := journal.Append(eventlog.Entry{
		Type:  eventlog.TypeRuntimeStopping,
		Class: eventlog.ClassControl,
	}); err != nil {
		t.Fatal(err)
	}
	second := readEventWindow(t, scanner)
	if len(second.Events) != 1 || second.Events[0].Sequence != 2 {
		t.Fatalf("live window = %+v", second)
	}
}

func TestEventStreamUsesLastEventIDWhenAfterIsMissing(t *testing.T) {
	journal := eventlog.New()
	for range 2 {
		if _, err := journal.Append(eventlog.Entry{
			Type:  eventlog.TypeRuntimeStarted,
			Class: eventlog.ClassControl,
		}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(NewHandler(runningRuntime(t), WithEventLog(journal)))
	defer server.Close()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	window := readEventWindow(t, bufio.NewScanner(response.Body))
	if len(window.Events) != 1 || window.Events[0].Sequence != 2 {
		t.Fatalf("window = %+v", window)
	}
}

func readEventWindow(t *testing.T, scanner *bufio.Scanner) eventlog.Window {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for event stream")
		}
		eventName := ""
		data := ""
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				break
			}
			switch {
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if eventName != "events" || data == "" {
			continue
		}
		var window eventlog.Window
		if err := json.Unmarshal([]byte(data), &window); err != nil {
			t.Fatalf("decode event window: %v", err)
		}
		return window
	}
}
