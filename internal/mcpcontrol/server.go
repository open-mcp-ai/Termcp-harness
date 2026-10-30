package mcpcontrol

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
)

type RegistrationState string

const (
	RegistrationDiscovered RegistrationState = "discovered"
	RegistrationRegistered RegistrationState = "registered"
)

type ConnectionState string

const (
	ConnectionNotAttempted ConnectionState = "not_attempted"
	ConnectionConnecting   ConnectionState = "connecting"
	ConnectionConnected    ConnectionState = "connected"
	ConnectionDisconnected ConnectionState = "disconnected"
	ConnectionUnavailable  ConnectionState = "unavailable"
)

type CapabilityState string

const (
	CapabilitiesNotLoaded CapabilityState = "not_loaded"
	CapabilitiesLoading   CapabilityState = "loading"
	CapabilitiesLoaded    CapabilityState = "loaded"
	CapabilitiesStale     CapabilityState = "stale"
)

type EnablementState string

const (
	EnablementDisabled EnablementState = "disabled"
	EnablementEnabled  EnablementState = "enabled"
)

type ManagementState string

const (
	ManagementExternal ManagementState = "external"
	ManagementManaged  ManagementState = "managed"
)

type FailureReason string

const (
	FailureNone           FailureReason = ""
	FailureConfiguration  FailureReason = "configuration"
	FailureAuthentication FailureReason = "authentication"
	FailureNetwork        FailureReason = "network"
	FailureProtocol       FailureReason = "protocol"
	FailureCapability     FailureReason = "capability"
	FailureProcess        FailureReason = "process"
	FailurePermission     FailureReason = "permission"
	FailureUnknown        FailureReason = "unknown"
)

type Revision uint64

var (
	ErrInvalidTransition = errors.New("invalid MCP server transition")
	ErrStaleRevision     = errors.New("stale MCP server revision")
)

type Spec struct {
	ID         string
	Name       string
	Endpoint   string
	Management ManagementState
}

type Capabilities struct {
	ProtocolVersion string
	ServerName      string
	ServerVersion   string
	ToolsSupported  bool
	ToolCount       int
}

type Snapshot struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Endpoint        string            `json:"endpoint"`
	Management      ManagementState   `json:"management"`
	Registration    RegistrationState `json:"registration"`
	Connection      ConnectionState   `json:"connection"`
	CapabilityState CapabilityState   `json:"capability_state"`
	Enablement      EnablementState   `json:"enablement"`
	Ready           bool              `json:"ready"`
	FailureReason   FailureReason     `json:"failure_reason,omitempty"`
	Capabilities    Capabilities      `json:"capabilities,omitempty"`
	Revision        Revision          `json:"revision"`
}

type Server struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func New(spec Spec) (*Server, error) {
	if strings.TrimSpace(spec.ID) == "" {
		return nil, errors.New("MCP server ID is required")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return nil, errors.New("MCP server name is required")
	}
	endpoint, err := validateLoopbackEndpoint(spec.Endpoint)
	if err != nil {
		return nil, err
	}
	if spec.Management == "" {
		spec.Management = ManagementExternal
	}
	if spec.Management != ManagementExternal && spec.Management != ManagementManaged {
		return nil, fmt.Errorf("invalid MCP management state %q", spec.Management)
	}

	return &Server{snapshot: Snapshot{
		ID:              spec.ID,
		Name:            spec.Name,
		Endpoint:        endpoint,
		Management:      spec.Management,
		Registration:    RegistrationDiscovered,
		Connection:      ConnectionNotAttempted,
		CapabilityState: CapabilitiesNotLoaded,
		Enablement:      EnablementDisabled,
		Revision:        1,
	}}, nil
}

func (s *Server) Register(expected Revision) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkRevision(expected); err != nil {
		return s.snapshot, err
	}
	if s.snapshot.Registration != RegistrationDiscovered {
		return s.snapshot, s.invalid("register")
	}

	s.snapshot.Registration = RegistrationRegistered
	s.commit()
	return s.snapshot, nil
}

func (s *Server) BeginConnect(expected Revision) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkRevision(expected); err != nil {
		return s.snapshot, err
	}
	if s.snapshot.Registration != RegistrationRegistered {
		return s.snapshot, s.invalid("connect before registration")
	}
	switch s.snapshot.Connection {
	case ConnectionDisconnected, ConnectionUnavailable:
		if s.snapshot.CapabilityState == CapabilitiesLoaded {
			s.snapshot.CapabilityState = CapabilitiesStale
		}
	case ConnectionNotAttempted:
	default:
		return s.snapshot, s.invalid("connect")
	}

	s.snapshot.Connection = ConnectionConnecting
	s.snapshot.FailureReason = FailureNone
	s.commit()
	return s.snapshot, nil
}

func (s *Server) MarkConnected(expected Revision) (Snapshot, error) {
	action := func() error {
		if s.snapshot.Connection != ConnectionConnecting {
			return s.invalid("mark connected")
		}
		s.snapshot.Connection = ConnectionConnected
		s.snapshot.FailureReason = FailureNone
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) BeginCapabilityLoad(expected Revision) (Snapshot, error) {
	action := func() error {
		if s.snapshot.Connection != ConnectionConnected {
			return s.invalid("load capabilities while disconnected")
		}
		switch s.snapshot.CapabilityState {
		case CapabilitiesNotLoaded, CapabilitiesStale:
		default:
			return s.invalid("begin capability load")
		}
		s.snapshot.CapabilityState = CapabilitiesLoading
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) LoadCapabilities(expected Revision, capabilities Capabilities) (Snapshot, error) {
	action := func() error {
		if s.snapshot.Connection != ConnectionConnected || s.snapshot.CapabilityState != CapabilitiesLoading {
			return s.invalid("store capabilities")
		}
		if capabilities.ToolCount < 0 || capabilities.ProtocolVersion == "" {
			return errors.New("invalid MCP capability snapshot")
		}
		s.snapshot.CapabilityState = CapabilitiesLoaded
		s.snapshot.Capabilities = capabilities
		s.snapshot.FailureReason = FailureNone
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) Enable(expected Revision) (Snapshot, error) {
	action := func() error {
		if s.snapshot.Registration != RegistrationRegistered || s.snapshot.Connection != ConnectionConnected || s.snapshot.CapabilityState != CapabilitiesLoaded {
			return s.invalid("enable unready MCP server")
		}
		s.snapshot.Enablement = EnablementEnabled
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) Disable(expected Revision) (Snapshot, error) {
	action := func() error {
		if s.snapshot.Registration != RegistrationRegistered {
			return s.invalid("disable undiscovered MCP server")
		}
		s.snapshot.Enablement = EnablementDisabled
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) MarkDisconnected(expected Revision) (Snapshot, error) {
	action := func() error {
		switch s.snapshot.Connection {
		case ConnectionConnected, ConnectionUnavailable:
		default:
			return s.invalid("disconnect")
		}
		s.snapshot.Connection = ConnectionDisconnected
		s.invalidateCapabilities()
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) MarkUnavailable(expected Revision, reason FailureReason) (Snapshot, error) {
	action := func() error {
		if !validFailureReason(reason) || reason == FailureNone {
			return errors.New("MCP failure reason is required and must be recognized")
		}
		if s.snapshot.Connection != ConnectionConnecting && s.snapshot.Connection != ConnectionConnected {
			return s.invalid("mark unavailable")
		}
		s.snapshot.Connection = ConnectionUnavailable
		s.snapshot.FailureReason = reason
		s.invalidateCapabilities()
		return nil
	}
	return s.update(expected, action)
}

func (s *Server) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot
}

func (s *Server) update(expected Revision, action func() error) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkRevision(expected); err != nil {
		return s.snapshot, err
	}
	if err := action(); err != nil {
		return s.snapshot, err
	}
	s.commit()
	return s.snapshot, nil
}

func (s *Server) checkRevision(expected Revision) error {
	if expected != s.snapshot.Revision {
		return fmt.Errorf("%w: expected %d, current %d", ErrStaleRevision, expected, s.snapshot.Revision)
	}
	return nil
}

func (s *Server) invalid(action string) error {
	return fmt.Errorf("%w: %s in registration=%s connection=%s capabilities=%s", ErrInvalidTransition, action, s.snapshot.Registration, s.snapshot.Connection, s.snapshot.CapabilityState)
}

func (s *Server) commit() {
	s.snapshot.Revision++
	s.snapshot.Ready = s.snapshot.Enablement == EnablementEnabled && s.snapshot.Connection == ConnectionConnected && s.snapshot.CapabilityState == CapabilitiesLoaded
}

func (s *Server) invalidateCapabilities() {
	if s.snapshot.CapabilityState == CapabilitiesLoaded || s.snapshot.CapabilityState == CapabilitiesStale {
		s.snapshot.CapabilityState = CapabilitiesStale
		return
	}
	s.snapshot.CapabilityState = CapabilitiesNotLoaded
}

func validFailureReason(reason FailureReason) bool {
	switch reason {
	case FailureNone, FailureConfiguration, FailureAuthentication, FailureNetwork, FailureProtocol, FailureCapability, FailureProcess, FailurePermission, FailureUnknown:
		return true
	default:
		return false
	}
}

func validateLoopbackEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse MCP endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("MCP endpoint must use http or https")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("MCP endpoint must not contain credentials, query, or fragment in the v1 registry")
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return parsed.String(), nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("MCP endpoint must be loopback in the v1 registry")
	}
	return parsed.String(), nil
}
