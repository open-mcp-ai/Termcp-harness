package runtimeapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

func CoreOriginFromMCP(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse MCP endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("MCP endpoint must use http or https")
	}
	if parsed.Host == "" {
		return "", errors.New("MCP endpoint host is required")
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func SameHTTPOrigin(left, right string) bool {
	leftURL, leftErr := url.Parse(strings.TrimSpace(left))
	rightURL, rightErr := url.Parse(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return false
	}
	return leftURL.Scheme != "" &&
		leftURL.Scheme == rightURL.Scheme &&
		leftURL.Host != "" &&
		leftURL.Host == rightURL.Host
}

func LoadToolPolicy(path string) (*toolpipeline.AllowlistPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open tool policy file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil {
		return nil, fmt.Errorf("read tool policy file: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, errors.New("tool policy file exceeds 1 MiB")
	}
	var document struct {
		Rules []toolpipeline.Rule `json:"rules"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode tool policy file: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("tool policy file contains multiple JSON values")
	}
	if len(document.Rules) == 0 {
		return nil, errors.New("tool policy file requires at least one rule")
	}
	policy, err := toolpipeline.NewAllowlistPolicy(document.Rules...)
	if err != nil {
		return nil, fmt.Errorf("compile tool policy: %w", err)
	}
	return policy, nil
}

func ReadApprovalToken(path string) (string, error) {
	return tokenfile.Read(path)
}
