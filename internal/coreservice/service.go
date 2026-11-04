package coreservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/tokenfile"
)

const (
	startTimeout = 25 * time.Second
	stopTimeout  = 10 * time.Second
)

var versionPattern = regexp.MustCompile(`(?m)\btermcp\s+(\S+)`)

func InspectBinary(ctx context.Context, path string) (string, string, error) {
	absolute, err := absoluteRegularFile(path, "Core binary")
	if err != nil {
		return "", "", err
	}
	digest, err := FileSHA256(absolute)
	if err != nil {
		return "", "", err
	}
	commandContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(commandContext, absolute, "-version")
	configureHidden(command)
	output := &boundedBuffer{limit: 8192}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		return "", "", fmt.Errorf("read Core version: %w", err)
	}
	version := ""
	if match := versionPattern.FindStringSubmatch(output.String()); len(match) == 2 {
		version = strings.TrimSpace(match[1])
	}
	if version == "" {
		return "", "", errors.New("Core version output did not contain a version")
	}
	return version, digest, nil
}

func Status(ctx context.Context, profile Profile) (coreinstance.Snapshot, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return coreinstance.Snapshot{}, err
	}
	token, err := readAuthToken(normalized)
	if err != nil {
		return coreinstance.Snapshot{}, err
	}
	manager := coreinstance.NewManager(nil)
	snapshot, err := manager.Bind(ctx, coreinstance.Binding{
		Endpoint:  normalized.Endpoint,
		Ownership: coreinstance.OwnershipExternal,
		AuthToken: token,
	})
	if err != nil {
		return coreinstance.Snapshot{}, err
	}
	if snapshot.Version != normalized.ExpectedVersion {
		return coreinstance.Snapshot{}, fmt.Errorf(
			"Core service version mismatch: profile=%s runtime=%s",
			normalized.ExpectedVersion,
			snapshot.Version,
		)
	}
	if err := verifyDataDirectory(normalized.DataDir, snapshot.LogPath); err != nil {
		return coreinstance.Snapshot{}, err
	}
	return snapshot, nil
}

func Start(ctx context.Context, profile Profile) (coreinstance.Snapshot, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return coreinstance.Snapshot{}, err
	}
	if snapshot, statusErr := Status(ctx, normalized); statusErr == nil {
		return snapshot, nil
	}
	token, err := readAuthToken(normalized)
	if err != nil {
		return coreinstance.Snapshot{}, err
	}
	host, port, err := endpointHostPort(normalized.Endpoint)
	if err != nil {
		return coreinstance.Snapshot{}, err
	}
	startContext, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	command := exec.CommandContext(
		startContext,
		normalized.BinaryPath,
		"daemon", "start",
		"--host", host,
		"--port", strconv.Itoa(port),
		"--data-dir", normalized.DataDir,
		"--idle-timeout", "0",
	)
	command.Env = daemonEnvironment(token)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	configureHidden(command)
	if err := command.Run(); err != nil {
		return coreinstance.Snapshot{}, fmt.Errorf("start standalone Core daemon: %w", err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		snapshot, statusErr := Status(startContext, normalized)
		if statusErr == nil {
			return snapshot, nil
		}
		lastErr = statusErr
		select {
		case <-startContext.Done():
			return coreinstance.Snapshot{}, fmt.Errorf("standalone Core did not become ready: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func Stop(ctx context.Context, profile Profile) error {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return err
	}
	token, err := readAuthToken(normalized)
	if err != nil {
		return err
	}
	stopContext, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(
		stopContext,
		http.MethodPost,
		normalized.Endpoint+"/api/daemon/stop",
		nil,
	)
	if err != nil {
		return err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := (&http.Client{Timeout: stopTimeout}).Do(request)
	if err != nil {
		return fmt.Errorf("stop standalone Core: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("standalone Core stop returned HTTP %d", response.StatusCode)
	}
	return nil
}

func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open Core binary: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash Core binary: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func readAuthToken(profile Profile) (string, error) {
	if profile.AuthTokenFile == "" {
		return "", nil
	}
	return tokenfile.Read(profile.AuthTokenFile)
}

func endpointHostPort(endpoint string) (string, int, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", 0, err
	}
	host := parsed.Hostname()
	port := 18765
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", 0, errors.New("Core service endpoint has an invalid port")
		}
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", 0, errors.New("Core service endpoint must contain only scheme, host, and port")
	}
	return host, port, nil
}

func verifyDataDirectory(dataDir, logPath string) error {
	logPath = strings.TrimSpace(logPath)
	if logPath == "" {
		return errors.New("Core daemon response omitted its log path; data directory cannot be verified")
	}
	absoluteLog, err := filepath.Abs(logPath)
	if err != nil {
		return fmt.Errorf("resolve Core daemon log path: %w", err)
	}
	relative, err := filepath.Rel(filepath.Clean(dataDir), absoluteLog)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("Core daemon log path is outside the configured data directory")
	}
	return nil
}

func daemonEnvironment(token string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
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
			environment = append(environment, entry)
		}
	}
	if token != "" {
		environment = append(environment, "TERMCP_AUTH_TOKEN="+token)
	}
	return environment
}

type boundedBuffer struct {
	data  []byte
	limit int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	original := len(data)
	remaining := b.limit - len(b.data)
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		b.data = append(b.data, data[:remaining]...)
	}
	return original, nil
}

func (b *boundedBuffer) String() string {
	return string(b.data)
}

func (p Profile) StablePath() string {
	return filepath.Clean(p.BinaryPath)
}
