package coreservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/open-mcp-ai/termcp-harness/internal/atomicfile"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
)

const (
	ProfileSchema    = 1
	maxProfileBytes  = 64 << 10
	maxCoreBinaryBytes = 1 << 30
	defaultEndpoint  = "http://127.0.0.1:18765"
	profileFileName  = "core-service.json"
	stateDirectory   = "termcp-harness"
	defaultCoreState = ".termcp"
)

type Profile struct {
	Schema          int    `json:"schema"`
	BinaryPath      string `json:"binary_path"`
	ExpectedVersion string `json:"expected_version"`
	BinarySHA256    string `json:"binary_sha256"`
	Endpoint        string `json:"endpoint"`
	DataDir         string `json:"data_dir"`
	AuthTokenFile   string `json:"auth_token_file,omitempty"`
}

func DefaultProfilePath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user config directory: %w", err)
	}
	return filepath.Join(root, stateDirectory, profileFileName), nil
}

func DefaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	return filepath.Join(home, defaultCoreState), nil
}

func DefaultEndpoint() string {
	return defaultEndpoint
}

func SaveProfile(path string, profile Profile) (Profile, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return Profile{}, err
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return Profile{}, errors.New("Core service profile path is required")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return Profile{}, fmt.Errorf("resolve Core service profile path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o700); err != nil {
		return Profile{}, fmt.Errorf("create Core service profile directory: %w", err)
	}
	body, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return Profile{}, fmt.Errorf("encode Core service profile: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(absolutePath), ".termcp-core-service-*.tmp")
	if err != nil {
		return Profile{}, fmt.Errorf("create temporary Core service profile: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = temp.Close()
		return Profile{}, fmt.Errorf("protect temporary Core service profile: %w", err)
	}
	if _, err := temp.Write(append(body, '\n')); err != nil {
		_ = temp.Close()
		return Profile{}, fmt.Errorf("write Core service profile: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return Profile{}, fmt.Errorf("sync Core service profile: %w", err)
	}
	if err := temp.Close(); err != nil {
		return Profile{}, fmt.Errorf("close Core service profile: %w", err)
	}
	if err := atomicfile.Replace(tempPath, absolutePath); err != nil {
		return Profile{}, fmt.Errorf("publish Core service profile: %w", err)
	}
	return normalized, nil
}

func LoadProfile(path string) (Profile, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		var err error
		path, err = DefaultProfilePath()
		if err != nil {
			return Profile{}, false, err
		}
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Profile{}, false, nil
	}
	if err != nil {
		return Profile{}, false, fmt.Errorf("open Core service profile: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Profile{}, false, fmt.Errorf("stat Core service profile: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxProfileBytes {
		return Profile{}, false, errors.New("Core service profile is invalid or too large")
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxProfileBytes))
	decoder.DisallowUnknownFields()
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, false, fmt.Errorf("decode Core service profile: %w", err)
	}
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return Profile{}, false, err
	}
	return normalized, true, nil
}

func NormalizeProfile(profile Profile) (Profile, error) {
	if profile.Schema != ProfileSchema {
		return Profile{}, fmt.Errorf("unsupported Core service profile schema %d", profile.Schema)
	}
	profile.BinaryPath = strings.TrimSpace(profile.BinaryPath)
	profile.ExpectedVersion = strings.TrimSpace(profile.ExpectedVersion)
	profile.BinarySHA256 = strings.ToLower(strings.TrimSpace(profile.BinarySHA256))
	profile.Endpoint = strings.TrimSpace(profile.Endpoint)
	profile.DataDir = strings.TrimSpace(profile.DataDir)
	profile.AuthTokenFile = strings.TrimSpace(profile.AuthTokenFile)
	if profile.BinaryPath == "" || profile.ExpectedVersion == "" || profile.BinarySHA256 == "" {
		return Profile{}, errors.New("Core binary path, expected version, and SHA-256 are required")
	}
	if profile.Endpoint == "" {
		profile.Endpoint = defaultEndpoint
	}
	if profile.DataDir == "" {
		var err error
		profile.DataDir, err = DefaultDataDir()
		if err != nil {
			return Profile{}, err
		}
	}
	normalizedEndpoint, err := coreinstance.ValidateEndpoint(profile.Endpoint)
	if err != nil {
		return Profile{}, fmt.Errorf("validate Core service endpoint: %w", err)
	}
	profile.Endpoint = normalizedEndpoint
	profile.BinaryPath, err = absoluteRegularFile(profile.BinaryPath, "Core binary")
	if err != nil {
		return Profile{}, err
	}
	actualHash, err := FileSHA256(profile.BinaryPath)
	if err != nil {
		return Profile{}, err
	}
	if actualHash != profile.BinarySHA256 {
		return Profile{}, errors.New("Core binary SHA-256 changed; reinstall the Core service profile")
	}
	profile.DataDir, err = filepath.Abs(profile.DataDir)
	if err != nil {
		return Profile{}, fmt.Errorf("resolve Core data directory: %w", err)
	}
	if profile.AuthTokenFile != "" {
		profile.AuthTokenFile, err = absoluteRegularFile(profile.AuthTokenFile, "Core auth token file")
		if err != nil {
			return Profile{}, err
		}
	}
	return profile, nil
}

func absoluteRegularFile(path, label string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file", label)
	}
	if label == "Core binary" && info.Size() > maxCoreBinaryBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", label, maxCoreBinaryBytes)
	}
	return absolute, nil
}
