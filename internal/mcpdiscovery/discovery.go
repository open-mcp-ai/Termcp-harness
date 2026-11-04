// Package mcpdiscovery discovers MCP server definitions from supported local
// clients. Discovery never connects to a server or exposes credentials.
package mcpdiscovery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
)

const (
	maxDiscoveryOutput = 1 << 20
	defaultScanTimeout = 10 * time.Second
)

type SourceState string
type ServerState string

const (
	SourceReady       SourceState = "ready"
	SourceNotFound    SourceState = "not_found"
	SourceUnavailable SourceState = "unavailable"
	SourceInvalid     SourceState = "invalid"

	ServerReady              ServerState = "ready"
	ServerDisabled           ServerState = "disabled"
	ServerUnsupported        ServerState = "unsupported"
	ServerUnavailable        ServerState = "unavailable"
	ServerNeedsConfiguration ServerState = "needs_configuration"
)

type Source struct {
	Client      string      `json:"client"`
	Scope       string      `json:"scope"`
	Tags        []string    `json:"tags"`
	State       SourceState `json:"state"`
	ServerCount int         `json:"server_count"`
	Warning     string      `json:"warning,omitempty"`
}

type Server struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Client     string      `json:"client"`
	Scope      string      `json:"scope"`
	Tags       []string    `json:"tags"`
	Transport  string      `json:"transport"`
	Enabled    bool        `json:"enabled"`
	Loadable   bool        `json:"loadable"`
	State      ServerState `json:"state"`
	Reason     string      `json:"reason,omitempty"`
	AuthStatus string      `json:"auth_status,omitempty"`
}

type Snapshot struct {
	ScannedAt time.Time `json:"scanned_at"`
	Sources   []Source  `json:"sources"`
	Servers   []Server  `json:"servers"`
}

type scanResult struct {
	snapshot Snapshot
	specs    map[string]mcpcontrol.Spec
}

type Scanner struct {
	WorkspaceDir string
	Timeout      time.Duration
	lookPath     func(string) (string, error)
	userHome     func() (string, error)
}

func NewScanner(workspaceDir string) *Scanner {
	return &Scanner{
		WorkspaceDir: strings.TrimSpace(workspaceDir),
		Timeout:      defaultScanTimeout,
		lookPath:     exec.LookPath,
		userHome:     os.UserHomeDir,
	}
}

func (s *Scanner) Scan(ctx context.Context) scanResult {
	result := scanResult{
		snapshot: Snapshot{
			ScannedAt: time.Now().UTC(),
			Sources:   []Source{},
			Servers:   []Server{},
		},
		specs: make(map[string]mcpcontrol.Spec),
	}
	home, err := s.userHome()
	if err != nil {
		result.snapshot.Sources = append(result.snapshot.Sources, Source{
			Client:  "codex",
			Scope:   "global",
			Tags:    []string{"global", "codex"},
			State:   SourceUnavailable,
			Warning: "无法确定用户目录",
		})
		return result
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	configPath := filepath.Join(codexHome, "config.toml")
	if info, statErr := os.Stat(configPath); statErr != nil || !info.Mode().IsRegular() {
		result.snapshot.Sources = append(result.snapshot.Sources, Source{
			Client: "codex",
			Scope:  "global",
			Tags:   []string{"global", "codex"},
			State:  SourceNotFound,
		})
		return result
	}
	codexPath, lookErr := s.lookPath("codex")
	if lookErr != nil {
		result.snapshot.Sources = append(result.snapshot.Sources, Source{
			Client:  "codex",
			Scope:   "global",
			Tags:    []string{"global", "codex"},
			State:   SourceUnavailable,
			Warning: "Codex CLI 未安装或不可执行",
		})
		return result
	}
	timeout := s.Timeout
	if timeout <= 0 {
		timeout = defaultScanTimeout
	}
	scanContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, err := runLimited(
		scanContext,
		codexPath,
		[]string{"mcp", "list", "--json"},
		home,
		append(os.Environ(), "CODEX_HOME="+codexHome),
		maxDiscoveryOutput,
	)
	if err != nil {
		result.snapshot.Sources = append(result.snapshot.Sources, Source{
			Client:  "codex",
			Scope:   "global",
			Tags:    []string{"global", "codex"},
			State:   SourceUnavailable,
			Warning: "Codex MCP 列表读取失败",
		})
		return result
	}
	var entries []codexServer
	if err := json.Unmarshal(stdout, &entries); err != nil {
		result.snapshot.Sources = append(result.snapshot.Sources, Source{
			Client:  "codex",
			Scope:   "global",
			Tags:    []string{"global", "codex"},
			State:   SourceInvalid,
			Warning: "Codex MCP 列表格式不受支持",
		})
		return result
	}
	for _, entry := range entries {
		server, spec, ok := classifyCodexServer(entry, s.lookPath)
		if !ok {
			continue
		}
		result.snapshot.Servers = append(result.snapshot.Servers, server)
		if server.Loadable {
			result.specs[server.ID] = spec
		}
	}
	sort.Slice(result.snapshot.Servers, func(i, j int) bool {
		return strings.ToLower(result.snapshot.Servers[i].Name) <
			strings.ToLower(result.snapshot.Servers[j].Name)
	})
	result.snapshot.Sources = append(result.snapshot.Sources, Source{
		Client:      "codex",
		Scope:       "global",
		Tags:        []string{"global", "codex"},
		State:       SourceReady,
		ServerCount: len(result.snapshot.Servers),
	})
	return result
}

type codexServer struct {
	Name           string         `json:"name"`
	Enabled        bool           `json:"enabled"`
	DisabledReason string         `json:"disabled_reason"`
	Transport      codexTransport `json:"transport"`
	AuthStatus     string         `json:"auth_status"`
}

type codexTransport struct {
	Type              string            `json:"type"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env"`
	EnvVars           []string          `json:"env_vars"`
	CWD               string            `json:"cwd"`
	URL               string            `json:"url"`
	BearerTokenEnvVar string            `json:"bearer_token_env_var"`
	HTTPHeaders       map[string]string `json:"http_headers"`
	EnvHTTPHeaders    map[string]string `json:"env_http_headers"`
	HTTPHeadersHelper string            `json:"http_headers_helper"`
}

func classifyCodexServer(
	entry codexServer,
	lookPath func(string) (string, error),
) (Server, mcpcontrol.Spec, bool) {
	name := strings.TrimSpace(entry.Name)
	if name == "" || len(name) > 128 {
		return Server{}, mcpcontrol.Spec{}, false
	}
	tags := []string{"global", "codex"}
	server := Server{
		ID:         "codex:global:" + name,
		Name:       name,
		Client:     "codex",
		Scope:      "global",
		Tags:       tags,
		Transport:  entry.Transport.Type,
		Enabled:    entry.Enabled,
		AuthStatus: strings.TrimSpace(entry.AuthStatus),
	}
	if !entry.Enabled {
		server.State = ServerDisabled
		server.Reason = "Codex 配置已停用"
		if reason := strings.TrimSpace(entry.DisabledReason); reason != "" {
			server.Reason = reason
		}
		return server, mcpcontrol.Spec{}, true
	}
	switch entry.Transport.Type {
	case "stdio":
		if strings.TrimSpace(entry.Transport.Command) == "" {
			server.State = ServerUnavailable
			server.Reason = "缺少 stdio command"
			return server, mcpcontrol.Spec{}, true
		}
		if len(entry.Transport.Env) > 0 {
			server.State = ServerNeedsConfiguration
			server.Reason = "配置包含环境字面值；需要人工确认后再加载"
			return server, mcpcontrol.Spec{}, true
		}
		command, args, err := prepareStdioCommand(entry.Transport.Command, entry.Transport.Args, lookPath)
		if err != nil {
			server.State = ServerUnavailable
			server.Reason = err.Error()
			return server, mcpcontrol.Spec{}, true
		}
		env := make([]string, 0, len(entry.Transport.EnvVars))
		for _, name := range entry.Transport.EnvVars {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			value, ok := os.LookupEnv(name)
			if !ok {
				server.State = ServerUnavailable
				server.Reason = "缺少所需环境变量 " + name
				return server, mcpcontrol.Spec{}, true
			}
			env = append(env, name+"="+value)
		}
		workDir := strings.TrimSpace(entry.Transport.CWD)
		if workDir != "" {
			info, err := os.Stat(workDir)
			if err != nil || !info.IsDir() {
				server.State = ServerUnavailable
				server.Reason = "工作目录不存在"
				return server, mcpcontrol.Spec{}, true
			}
		}
		spec := mcpcontrol.Spec{
			ID:        server.ID,
			Name:      server.Name,
			Transport: mcpcontrol.TransportStdio,
			Command:   command,
			Args:      args,
			Env:       env,
			WorkDir:   workDir,
		}
		if _, err := mcpcontrol.New(spec); err != nil {
			server.State = ServerUnavailable
			server.Reason = "MCP 定义不受支持"
			return server, mcpcontrol.Spec{}, true
		}
		server.Transport = string(mcpcontrol.TransportStdio)
		server.Loadable = true
		server.State = ServerReady
		return server, spec, true
	case "streamable_http":
		endpoint := strings.TrimSpace(entry.Transport.URL)
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Hostname() == "" {
			server.State = ServerUnavailable
			server.Reason = "HTTP endpoint 无效"
			return server, mcpcontrol.Spec{}, true
		}
		if !loopbackHost(parsed.Hostname()) {
			server.State = ServerUnsupported
			server.Reason = "Harness v1 仅支持回环 HTTP MCP"
			return server, mcpcontrol.Spec{}, true
		}
		if len(entry.Transport.HTTPHeaders) > 0 ||
			len(entry.Transport.EnvHTTPHeaders) > 0 ||
			strings.TrimSpace(entry.Transport.HTTPHeadersHelper) != "" {
			server.State = ServerNeedsConfiguration
			server.Reason = "HTTP headers 需要人工确认后再加载"
			return server, mcpcontrol.Spec{}, true
		}
		authToken := ""
		if name := strings.TrimSpace(entry.Transport.BearerTokenEnvVar); name != "" {
			value, ok := os.LookupEnv(name)
			if !ok || value == "" {
				server.State = ServerUnavailable
				server.Reason = "缺少 bearer token 环境变量 " + name
				return server, mcpcontrol.Spec{}, true
			}
			authToken = value
		}
		spec := mcpcontrol.Spec{
			ID:        server.ID,
			Name:      server.Name,
			Transport: mcpcontrol.TransportStreamableHTTP,
			Endpoint:  endpoint,
			AuthToken: authToken,
		}
		if _, err := mcpcontrol.New(spec); err != nil {
			server.State = ServerUnavailable
			server.Reason = "HTTP MCP 定义不受支持"
			return server, mcpcontrol.Spec{}, true
		}
		server.Transport = string(mcpcontrol.TransportStreamableHTTP)
		server.Loadable = true
		server.State = ServerReady
		return server, spec, true
	default:
		server.State = ServerUnsupported
		server.Reason = "未知 MCP transport"
		return server, mcpcontrol.Spec{}, true
	}
}

func prepareStdioCommand(
	command string,
	args []string,
	lookPath func(string) (string, error),
) (string, []string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil, errors.New("缺少 stdio command")
	}
	resolved := command
	if !filepath.IsAbs(resolved) {
		var err error
		resolved, err = lookPath(command)
		if err != nil {
			return "", nil, errors.New("stdio command 不在当前 PATH 中")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, errors.New("stdio command 不在当前 PATH 中")
	}
	if runtime.GOOS != "windows" {
		return resolved, append([]string(nil), args...), nil
	}
	extension := strings.ToLower(filepath.Ext(resolved))
	switch extension {
	case ".exe", ".com":
		return resolved, append([]string(nil), args...), nil
	case ".cmd", ".bat":
		return prepareNPMCommandShim(resolved, args, lookPath)
	default:
		return "", nil, errors.New("Windows MCP command 不是可直接执行的 exe/com 或 npm shim")
	}
}

func prepareNPMCommandShim(
	commandPath string,
	args []string,
	lookPath func(string) (string, error),
) (string, []string, error) {
	file, err := os.Open(commandPath)
	if err != nil {
		return "", nil, errors.New("无法读取 npm command shim")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10+1))
	if err != nil || len(data) > 64<<10 {
		return "", nil, errors.New("npm command shim 无法安全读取")
	}
	const marker = `"%dp0%\node_modules\`
	shim := string(data)
	index := strings.Index(shim, marker)
	if index < 0 {
		return "", nil, errors.New("command shim 不是受支持的 npm launcher")
	}
	start := index + 1
	endOffset := strings.IndexByte(shim[start:], '"')
	if endOffset <= 0 {
		return "", nil, errors.New("npm command shim 缺少脚本路径")
	}
	scriptTemplate := shim[start : start+endOffset]
	baseDir := filepath.Dir(commandPath)
	scriptPath := strings.ReplaceAll(scriptTemplate, `%dp0%`, baseDir)
	scriptPath, err = filepath.Abs(scriptPath)
	if err != nil {
		return "", nil, errors.New("npm command shim 脚本路径无效")
	}
	relative, err := filepath.Rel(baseDir, scriptPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("npm command shim 脚本路径越界")
	}
	info, err := os.Stat(scriptPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, errors.New("npm command shim 脚本不存在")
	}
	nodePath := filepath.Join(baseDir, "node.exe")
	if info, err := os.Stat(nodePath); err != nil || !info.Mode().IsRegular() {
		nodePath, err = lookPath("node")
		if err != nil {
			return "", nil, errors.New("无法定位 npm command shim 所需的 Node.js")
		}
	}
	resolvedArgs := make([]string, 0, len(args)+1)
	resolvedArgs = append(resolvedArgs, scriptPath)
	resolvedArgs = append(resolvedArgs, args...)
	return nodePath, resolvedArgs, nil
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runLimited(
	ctx context.Context,
	binary string,
	args []string,
	dir string,
	env []string,
	limit int64,
) ([]byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	command.Env = env
	stdout := &limitedBuffer{limit: limit}
	command.Stdout = stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

type limitedBuffer struct {
	bytes.Buffer
	limit int64
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	if int64(b.Len()+len(value)) > b.limit {
		return 0, errors.New("MCP discovery output exceeds limit")
	}
	return b.Buffer.Write(value)
}

type Manager struct {
	scanner *Scanner
	host    *mcpcontrol.Host

	mu    sync.Mutex
	last  Snapshot
	specs map[string]mcpcontrol.Spec
}

var (
	ErrUnknownServer = errors.New("discovered MCP server not found")
	ErrNotLoadable   = errors.New("discovered MCP server cannot be loaded")
)

func NewManager(scanner *Scanner, host *mcpcontrol.Host) *Manager {
	return &Manager{scanner: scanner, host: host}
}

func (m *Manager) Discover(ctx context.Context) (Snapshot, error) {
	if m == nil || m.scanner == nil {
		return Snapshot{ScannedAt: time.Now().UTC(), Sources: []Source{}, Servers: []Server{}}, nil
	}
	result := m.scanner.Scan(ctx)
	m.mu.Lock()
	m.last = cloneSnapshot(result.snapshot)
	m.specs = result.specs
	m.mu.Unlock()
	return cloneSnapshot(result.snapshot), nil
}

func (m *Manager) Register(ctx context.Context, id string) (mcpcontrol.Snapshot, error) {
	if m == nil || m.host == nil {
		return mcpcontrol.Snapshot{}, errors.New("MCP host is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" || len(id) > 256 {
		return mcpcontrol.Snapshot{}, ErrUnknownServer
	}
	m.mu.Lock()
	spec, ok := m.specs[id]
	m.mu.Unlock()
	if !ok {
		if _, err := m.Discover(ctx); err != nil {
			return mcpcontrol.Snapshot{}, err
		}
		m.mu.Lock()
		spec, ok = m.specs[id]
		m.mu.Unlock()
	}
	if !ok {
		return mcpcontrol.Snapshot{}, ErrUnknownServer
	}
	snapshot, err := m.host.Register(spec)
	if err != nil {
		return mcpcontrol.Snapshot{}, fmt.Errorf("%w: %v", ErrNotLoadable, err)
	}
	return snapshot, nil
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := snapshot
	cloned.Sources = append([]Source(nil), snapshot.Sources...)
	cloned.Servers = append([]Server(nil), snapshot.Servers...)
	for index := range cloned.Sources {
		cloned.Sources[index].Tags = append([]string(nil), snapshot.Sources[index].Tags...)
	}
	for index := range cloned.Servers {
		cloned.Servers[index].Tags = append([]string(nil), snapshot.Servers[index].Tags...)
	}
	return cloned
}
