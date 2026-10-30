package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	maxHeaderBytes         = 1 << 20
)

// ValidateListenAddress prevents exposing the unauthenticated v0 control API
// beyond the local machine.
func ValidateListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse listen address: %w", err)
	}
	if host == "localhost" {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q is not loopback", address)
	}
	return nil
}

type Server struct {
	listenAddress string
	handler       http.Handler
	runtime       *runtime.Runtime
	shutdown      time.Duration
	listen        func(network, address string) (net.Listener, error)
}

func NewServer(listenAddress string, handler http.Handler, rt *runtime.Runtime) (*Server, error) {
	if err := ValidateListenAddress(listenAddress); err != nil {
		return nil, err
	}
	if handler == nil {
		return nil, errors.New("handler is required")
	}
	if rt == nil {
		return nil, errors.New("runtime is required")
	}

	return &Server{
		listenAddress: listenAddress,
		handler:       handler,
		runtime:       rt,
		shutdown:      defaultShutdownTimeout,
		listen:        net.Listen,
	}, nil
}

// Run serves until ctx is cancelled, then drains HTTP and stops Runtime state.
func (s *Server) Run(ctx context.Context) error {
	listener, err := s.listen("tcp", s.listenAddress)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()

	if err := s.runtime.Start(); err != nil {
		return fmt.Errorf("start runtime: %w", err)
	}

	httpServer := &http.Server{
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	serverErr := make(chan error, 1)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serverErr <- err
	}()

	select {
	case err := <-serverErr:
		s.stopRuntime()
		if err != nil {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		if err := s.runtime.MarkStopping(); err != nil {
			return fmt.Errorf("mark runtime stopping: %w", err)
		}

		shutdownContext, cancel := context.WithTimeout(context.Background(), s.shutdown)
		defer cancel()
		shutdownErr := httpServer.Shutdown(shutdownContext)
		if shutdownErr != nil {
			_ = httpServer.Close()
		}
		serveErr := <-serverErr
		stopErr := s.runtime.Stop()

		if shutdownErr != nil {
			return fmt.Errorf("shutdown HTTP: %w", shutdownErr)
		}
		if serveErr != nil {
			return fmt.Errorf("serve HTTP: %w", serveErr)
		}
		if stopErr != nil {
			return fmt.Errorf("stop runtime: %w", stopErr)
		}
		return nil
	}
}

func (s *Server) stopRuntime() {
	if s.runtime.MarkStopping() == nil {
		_ = s.runtime.Stop()
	}
}
