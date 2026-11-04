package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

func TestRunWithReadyReportsDynamicLoopbackEndpoint(t *testing.T) {
	rt, err := runtime.New("test")
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}), rt)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.RunWithReady(ctx, func(endpoint string) error {
			ready <- endpoint
			return nil
		})
	}()

	select {
	case endpoint := <-ready:
		if endpoint == "http://127.0.0.1:0" {
			t.Fatal("ready endpoint retained the wildcard port")
		}
		response, err := http.Get(endpoint + "/healthz")
		if err != nil {
			t.Fatalf("GET ready endpoint: %v", err)
		}
		_ = response.Body.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("server did not report readiness")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWithReady() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
}
