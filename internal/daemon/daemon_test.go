package daemon

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

func TestValidateListenAddress(t *testing.T) {
	tests := []struct {
		address string
		wantErr bool
	}{
		{address: "127.0.0.1:8787"},
		{address: "[::1]:8787"},
		{address: "localhost:8787"},
		{address: "0.0.0.0:8787", wantErr: true},
		{address: "192.0.2.1:8787", wantErr: true},
		{address: "missing-port", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			err := ValidateListenAddress(tt.address)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateListenAddress() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestRunStopsRuntimeAfterContextCancellation(t *testing.T) {
	rt, err := runtime.New("test")
	if err != nil {
		t.Fatalf("runtime.New() error = %v", err)
	}
	server, err := NewServer("127.0.0.1:0", http.NotFoundHandler(), rt)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	listenerReady := make(chan net.Listener, 1)
	server.listen = func(network, address string) (net.Listener, error) {
		listener, listenErr := net.Listen(network, address)
		if listenErr == nil {
			listenerReady <- listener
		}
		return listener, listenErr
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- server.Run(ctx)
	}()

	select {
	case <-listenerReady:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not create a listener")
	}
	if got := rt.Snapshot().State; got != runtime.StateRunning {
		t.Fatalf("runtime state = %q, want %q", got, runtime.StateRunning)
	}

	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not return after cancellation")
	}
	if got := rt.Snapshot().State; got != runtime.StateStopped {
		t.Fatalf("runtime state = %q, want %q", got, runtime.StateStopped)
	}
}
