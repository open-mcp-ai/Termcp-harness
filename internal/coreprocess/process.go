// Package coreprocess supervises a prebuilt Core executable as a Harness-owned
// child process. It never imports Core internals and communicates only through
// Core's loopback HTTP contract.
package coreprocess

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/corelock"
)

const (
	defaultHost         = "127.0.0.1"
	defaultStartTimeout = 20 * time.Second
	defaultStopTimeout  = 5 * time.Second
	maxIdentityResponse = 1 << 20
)

type Config struct {
	BinaryPath   string
	Version      string
	DataDir      string
	LogPath      string
	Host         string
	NoInternal   bool
	StartTimeout time.Duration
	AuthToken    string
}

type Process struct {
	cmd       *exec.Cmd
	lock      *corelock.Lock
	logFile   *os.File
	endpoint  string
	port      int
	authToken string
	version   string
	logPath   string
	startedAt time.Time

	stopping atomic.Bool
	done     chan struct{}
	mu       sync.RWMutex
	waitErr  error
}

func Start(ctx context.Context, config Config) (*Process, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}

	lock, err := corelock.Acquire(normalized.DataDir)
	if err != nil {
		return nil, fmt.Errorf("lock bundled Core data directory: %w", err)
	}
	logFile, err := openLog(normalized.LogPath)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	port, err := allocateLoopbackPort(normalized.Host)
	if err != nil {
		_ = lock.Close()
		_ = logFile.Close()
		return nil, err
	}

	token, err := authToken(normalized.AuthToken)
	if err != nil {
		_ = lock.Close()
		_ = logFile.Close()
		return nil, err
	}
	endpoint := "http://" + net.JoinHostPort(normalized.Host, strconv.Itoa(port))
	cmd := exec.Command(normalized.BinaryPath, coreArgs(normalized, port)...)
	cmd.Env = coreEnvironment(token)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureCommand(cmd)
	if err := cmd.Start(); err != nil {
		_ = lock.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("start bundled Core: %w", err)
	}

	process := &Process{
		cmd:       cmd,
		lock:      lock,
		logFile:   logFile,
		endpoint:  endpoint,
		port:      port,
		authToken: token,
		version:   normalized.Version,
		logPath:   normalized.LogPath,
		startedAt: time.Now().UTC(),
		done:      make(chan struct{}),
	}
	go process.wait()

	readyContext, cancel := context.WithTimeout(ctx, normalized.StartTimeout)
	defer cancel()
	if err := process.waitReady(readyContext); err != nil {
		process.stopping.Store(true)
		process.kill()
		<-process.done
		return nil, err
	}
	return process, nil
}

func (p *Process) Endpoint() string      { return p.endpoint }
func (p *Process) MCPEndpoint() string   { return p.endpoint + "/stream" }
func (p *Process) AuthToken() string     { return p.authToken }
func (p *Process) Version() string       { return p.version }
func (p *Process) LogPath() string       { return p.logPath }
func (p *Process) PID() int              { return p.cmd.Process.Pid }
func (p *Process) StartedAt() time.Time  { return p.startedAt }
func (p *Process) Done() <-chan struct{} { return p.done }
func (p *Process) Stopping() bool         { return p.stopping.Load() }

func (p *Process) WaitErr() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.waitErr
}

func (p *Process) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	p.stopping.Store(true)
	select {
	case <-p.done:
		return nil
	default:
	}

	stopContext, cancel := context.WithTimeout(ctx, defaultStopTimeout)
	stopErr := p.requestStop(stopContext)
	cancel()
	if stopErr != nil {
		// The Core may not have reached its managed daemon mode. The caller still
		// needs bounded cleanup; the process will be killed below if it does not exit.
	}
	select {
	case <-p.done:
		return nil
	case <-time.After(defaultStopTimeout):
		p.kill()
		<-p.done
		return stopErr
	}
}

func (p *Process) wait() {
	err := p.cmd.Wait()
	if p.stopping.Load() {
		err = nil
	}
	p.mu.Lock()
	p.waitErr = err
	p.mu.Unlock()
	_ = p.logFile.Close()
	_ = p.lock.Close()
	close(p.done)
}

func (p *Process) kill() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

func (p *Process) requestStop(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/daemon/stop", nil)
	if err != nil {
		return err
	}
	if p.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+p.authToken)
	}
	response, err := (&http.Client{Timeout: defaultStopTimeout}).Do(request)
	if err != nil {
		return fmt.Errorf("request bundled Core stop: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		return errors.New("bundled Core is not running in managed daemon mode")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("bundled Core stop returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *Process) waitReady(ctx context.Context) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := p.ready(); err == nil {
			return nil
		} else {
			lastErr := err
			select {
			case <-ctx.Done():
				return fmt.Errorf("bundled Core did not become ready: %w (last error: %v)", ctx.Err(), lastErr)
			case <-p.done:
				return fmt.Errorf("bundled Core exited during startup: %w", p.WaitErr())
			case <-ticker.C:
			}
		}
	}
}

func (p *Process) ready() error {
	request, err := http.NewRequest(http.MethodGet, p.endpoint+"/api/version", nil)
	if err != nil {
		return err
	}
	if p.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+p.authToken)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Core version returned HTTP %d", response.StatusCode)
	}
	var version struct {
		Version string `json:"version"`
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxIdentityResponse))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return fmt.Errorf("decode Core version: %w", err)
	}
	if version.Version == "" {
		return errors.New("Core version is empty")
	}
	if p.version != "" && version.Version != p.version {
		return fmt.Errorf("bundled Core version mismatch: bundle=%s runtime=%s", p.version, version.Version)
	}
	return nil
}

func normalizeConfig(config Config) (Config, error) {
	config.BinaryPath = strings.TrimSpace(config.BinaryPath)
	config.Version = strings.TrimSpace(config.Version)
	config.DataDir = strings.TrimSpace(config.DataDir)
	config.LogPath = strings.TrimSpace(config.LogPath)
	config.Host = strings.TrimSpace(config.Host)
	if config.BinaryPath == "" {
		return Config{}, errors.New("Core binary path is required")
	}
	if config.DataDir == "" {
		return Config{}, errors.New("Core data directory is required")
	}
	if config.Host == "" {
		config.Host = defaultHost
	}
	if err := validateLoopbackHost(config.Host); err != nil {
		return Config{}, err
	}
	if config.LogPath == "" {
		config.LogPath = filepath.Join(config.DataDir, "logs", "core.log")
	}
	if config.StartTimeout <= 0 {
		config.StartTimeout = defaultStartTimeout
	}
	var err error
	config.BinaryPath, err = filepath.Abs(config.BinaryPath)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Core binary: %w", err)
	}
	config.DataDir, err = filepath.Abs(config.DataDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Core data directory: %w", err)
	}
	config.LogPath, err = filepath.Abs(config.LogPath)
	if err != nil {
		return Config{}, fmt.Errorf("resolve Core log path: %w", err)
	}
	return config, nil
}

func validateLoopbackHost(host string) error {
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("Core host %q is not loopback", host)
	}
	return nil
}

func coreArgs(config Config, port int) []string {
	args := []string{
		"--host", config.Host,
		"--port", strconv.Itoa(port),
		"--data-dir", config.DataDir,
		"--idle-timeout", "0",
	}
	if config.NoInternal {
		args = append(args, "--no-internal")
	}
	return args
}

func coreEnvironment(token string) []string {
	filtered := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		name := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			name = entry[:index]
		}
		switch {
		case strings.EqualFold(name, "TERMCP_DAEMON_CHILD"):
		case strings.EqualFold(name, "TERMCP_AUTH_TOKEN"):
		case strings.EqualFold(name, "TERMCP_AUTH_HASH"):
		case strings.EqualFold(name, "TERMCP_DISABLE_AUTH_TOKEN"):
		case strings.EqualFold(name, "TERMCP_DATA_DIR"):
		default:
			filtered = append(filtered, entry)
		}
	}
	filtered = append(filtered,
		"TERMCP_DAEMON_CHILD=1",
		"TERMCP_AUTH_TOKEN="+token,
	)
	return filtered
}

func authToken(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate Core authentication token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func allocateLoopbackPort(host string) (int, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return 0, fmt.Errorf("allocate bundled Core port: %w", err)
	}
	defer listener.Close()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("allocate bundled Core port: unexpected address type")
	}
	return address.Port, nil
}

func openLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Core log directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Core log: %w", err)
	}
	return file, nil
}

func DefaultDataDir() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(root, "termcp-harness", "core"), nil
}
