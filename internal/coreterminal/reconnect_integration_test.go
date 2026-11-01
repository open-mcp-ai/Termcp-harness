package coreterminal

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManagerCoreReconnectIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_RECONNECT_TEST") != "1" {
		t.Skip("set TERMCP_CORE_RECONNECT_TEST=1 and provide a dedicated Core shell")
	}
	if runtime.GOOS != "windows" {
		t.Skip("this integration expects the Windows internal Core shell")
	}
	endpoint := strings.TrimSpace(os.Getenv("TERMCP_CORE_ENDPOINT"))
	agentSessionID := strings.TrimSpace(os.Getenv("TERMCP_HARNESS_TEST_AGENT_ID"))
	coreSessionID := strings.TrimSpace(os.Getenv("TERMCP_CORE_TEST_CORE_SESSION_ID"))
	shellID := strings.TrimSpace(os.Getenv("TERMCP_CORE_TEST_SHELL_ID"))
	if endpoint == "" || agentSessionID == "" || coreSessionID == "" || shellID == "" {
		t.Fatal("TERMCP_CORE_ENDPOINT and the Agent/Core/Shell IDs are required")
	}

	proxy := startReconnectProxy(t, endpoint)
	manager := NewManager()
	manager.reconnectMin = 25 * time.Millisecond
	manager.reconnectMax = 100 * time.Millisecond
	defer manager.Shutdown()

	opened, err := manager.Open(context.Background(), proxy.URL(), OpenOptions{
		AgentSessionID: agentSessionID,
		CoreSessionID:  coreSessionID,
		ShellID:        shellID,
		Rows:           31,
		Cols:           117,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close(opened.ID)

	command := `$p='RECONNECT'; 1..40 | ForEach-Object { "$p-$_" ; Start-Sleep -Milliseconds 50 }` + "\r"
	if err := manager.Input(opened.ID, command); err != nil {
		t.Fatal(err)
	}

	var output strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	sawReconnecting := false
	dropped := false
	for time.Now().Before(deadline) {
		polled, err := manager.Poll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(polled.Output)
		if polled.State == StateReconnecting {
			sawReconnecting = true
		}
		if !dropped && strings.Contains(output.String(), "RECONNECT-1") {
			proxy.DropConnections()
			dropped = true
		}
		if dropped && sawReconnecting && polled.State == StateConnected &&
			strings.Contains(output.String(), "RECONNECT-40") {
			if polled.ReconnectAttempts != 0 || polled.LastError != "" {
				t.Fatalf("reconnected snapshot = %+v", polled.Snapshot)
			}
			t.Logf(
				"Core reconnect recovered output through cursor %d after reconnect attempts",
				polled.OutputCursor,
			)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf(
		"Core reconnect did not recover; dropped=%t saw_reconnecting=%t output=%q",
		dropped,
		sawReconnecting,
		output.String(),
	)
}

type reconnectProxy struct {
	listener net.Listener
	target   string
	mu       sync.Mutex
	conns    map[net.Conn]struct{}
	closed   chan struct{}
	acceptWG sync.WaitGroup
	connWG   sync.WaitGroup
}

func startReconnectProxy(t *testing.T, endpoint string) *reconnectProxy {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &reconnectProxy{
		listener: listener,
		target:   parsed.Host,
		conns:    make(map[net.Conn]struct{}),
		closed:   make(chan struct{}),
	}
	proxy.acceptWG.Add(1)
	go proxy.accept()
	t.Cleanup(proxy.Close)
	return proxy
}

func (p *reconnectProxy) URL() string {
	return "http://" + p.listener.Addr().String()
}

func (p *reconnectProxy) accept() {
	defer p.acceptWG.Done()
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}
		server, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = client.Close()
			continue
		}
		p.track(client)
		p.track(server)
		p.connWG.Add(2)
		go p.pipe(client, server)
		go p.pipe(server, client)
	}
}

func (p *reconnectProxy) pipe(source, target net.Conn) {
	defer p.connWG.Done()
	_, _ = io.Copy(target, source)
	_ = source.Close()
	_ = target.Close()
	p.untrack(source)
	p.untrack(target)
}

func (p *reconnectProxy) track(conn net.Conn) {
	p.mu.Lock()
	p.conns[conn] = struct{}{}
	p.mu.Unlock()
}

func (p *reconnectProxy) untrack(conn net.Conn) {
	p.mu.Lock()
	delete(p.conns, conn)
	p.mu.Unlock()
}

func (p *reconnectProxy) DropConnections() {
	p.mu.Lock()
	conns := make([]net.Conn, 0, len(p.conns))
	for conn := range p.conns {
		conns = append(conns, conn)
	}
	p.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}

func (p *reconnectProxy) Close() {
	select {
	case <-p.closed:
		return
	default:
		close(p.closed)
	}
	_ = p.listener.Close()
	p.acceptWG.Wait()
	p.DropConnections()
	p.connWG.Wait()
}
