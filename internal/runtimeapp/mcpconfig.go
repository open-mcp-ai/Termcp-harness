package runtimeapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
)

const maxMCPConfigBytes = 1 << 20

type mcpConfigDocument struct {
	Servers []mcpServerConfig `json:"servers"`
}

type mcpServerConfig struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Transport     mcpcontrol.TransportKind `json:"transport"`
	Endpoint      string                   `json:"endpoint,omitempty"`
	Enabled       *bool                    `json:"enabled,omitempty"`
	AuthTokenFile string                   `json:"auth_token_file,omitempty"`
	Command       string                   `json:"command,omitempty"`
	Args          []string                 `json:"args,omitempty"`
	Env           []string                 `json:"env,omitempty"`
	WorkDir       string                   `json:"workdir,omitempty"`
}

type LoadedMCPServer struct {
	Spec    mcpcontrol.Spec
	Enabled bool
}

func LoadMCPConfigFile(path string) ([]LoadedMCPServer, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open MCP config file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxMCPConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read MCP config file: %w", err)
	}
	if len(data) > maxMCPConfigBytes {
		return nil, errors.New("MCP config file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	var document mcpConfigDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode MCP config file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("MCP config file contains multiple JSON values")
	}
	if len(document.Servers) == 0 {
		return nil, errors.New("MCP config file requires at least one server")
	}
	if len(document.Servers) > 31 {
		return nil, errors.New("MCP config file supports at most 31 additional servers")
	}

	baseDir := filepath.Dir(path)
	seen := make(map[string]struct{}, len(document.Servers))
	loaded := make([]LoadedMCPServer, 0, len(document.Servers))
	for index, server := range document.Servers {
		server.ID = strings.TrimSpace(server.ID)
		server.Name = strings.TrimSpace(server.Name)
		if server.ID == "" || server.Name == "" {
			return nil, fmt.Errorf("MCP config server %d requires id and name", index)
		}
		if _, exists := seen[server.ID]; exists {
			return nil, fmt.Errorf("MCP config contains duplicate server ID %q", server.ID)
		}
		seen[server.ID] = struct{}{}

		authToken := ""
		if strings.TrimSpace(server.AuthTokenFile) != "" {
			tokenPath := strings.TrimSpace(server.AuthTokenFile)
			if !filepath.IsAbs(tokenPath) {
				tokenPath = filepath.Join(baseDir, tokenPath)
			}
			authToken, err = tokenfile.Read(tokenPath)
			if err != nil {
				return nil, fmt.Errorf("read auth token for MCP server %q: %w", server.ID, err)
			}
		}
		workDir := strings.TrimSpace(server.WorkDir)
		if workDir != "" && !filepath.IsAbs(workDir) {
			workDir = filepath.Join(baseDir, workDir)
		}
		spec := mcpcontrol.Spec{
			ID:        server.ID,
			Name:      server.Name,
			Transport: server.Transport,
			Endpoint:  strings.TrimSpace(server.Endpoint),
			Command:   strings.TrimSpace(server.Command),
			Args:      append([]string(nil), server.Args...),
			Env:       append([]string(nil), server.Env...),
			WorkDir:   workDir,
			AuthToken: authToken,
		}
		if _, err := mcpcontrol.New(spec); err != nil {
			return nil, fmt.Errorf("MCP config server %q: %w", server.ID, err)
		}
		enabled := true
		if server.Enabled != nil {
			enabled = *server.Enabled
		}
		loaded = append(loaded, LoadedMCPServer{Spec: spec, Enabled: enabled})
	}
	return loaded, nil
}
