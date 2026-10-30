// Package corebundle locates and validates a prebuilt Core binary that is
// shipped beside the Harness executable. It deliberately does not import or
// execute Core internals; the bundle remains an independent process artifact.
package corebundle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	ManifestName      = "manifest.json"
	ManifestSchema    = 1
	EnvBundleDir      = "TERMCP_HARNESS_CORE_BUNDLE"
	maxManifestBytes  = 64 << 10
	defaultBinaryName = "termcp-core"
)

type Manifest struct {
	Schema  int    `json:"schema"`
	Version string `json:"version"`
	Binary  string `json:"binary"`
	SHA256  string `json:"sha256"`
}

type Bundle struct {
	Dir        string
	Manifest   Manifest
	BinaryPath string
}

// Prepare copies a prebuilt Core binary into a versioned bundle directory and
// writes a manifest containing its exact digest. It never overwrites an
// existing bundle file.
func Prepare(binaryPath, version, outDir string) (*Bundle, error) {
	binaryPath = strings.TrimSpace(binaryPath)
	version = strings.TrimSpace(version)
	outDir = strings.TrimSpace(outDir)
	if binaryPath == "" {
		return nil, errors.New("Core binary path is required")
	}
	if version == "" {
		return nil, errors.New("Core version is required")
	}
	if outDir == "" {
		return nil, errors.New("Core bundle output directory is required")
	}

	source, err := filepath.Abs(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("resolve Core binary: %w", err)
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, fmt.Errorf("stat Core binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("Core binary must be a regular file")
	}

	dir, err := filepath.Abs(outDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Core bundle directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create Core bundle directory: %w", err)
	}

	name := BinaryFilename()
	destination := filepath.Join(dir, name)
	if _, err := os.Stat(destination); err == nil {
		return nil, fmt.Errorf("Core bundle binary already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect Core bundle destination: %w", err)
	}
	if filepath.Clean(source) == filepath.Clean(destination) {
		return nil, errors.New("Core binary source and destination must differ")
	}

	if err := copyExecutable(source, destination); err != nil {
		return nil, err
	}
	digest, err := hashFile(destination)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{Schema: ManifestSchema, Version: version, Binary: name, SHA256: digest}
	if err := writeManifest(dir, manifest); err != nil {
		return nil, err
	}
	return Load(dir)
}

func Load(dir string) (*Bundle, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, errors.New("Core bundle directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve Core bundle directory: %w", err)
	}
	manifestPath := filepath.Join(absolute, ManifestName)
	file, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open Core bundle manifest: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Core bundle manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("Core bundle manifest exceeds %d bytes", maxManifestBytes)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("decode Core bundle manifest: %w", err)
	}
	if err := validateManifest(manifest); err != nil {
		return nil, err
	}
	binaryPath, err := binaryPathWithin(absolute, manifest.Binary)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("stat bundled Core binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("bundled Core binary must be a regular file")
	}
	digest, err := hashFile(binaryPath)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(digest, manifest.SHA256) {
		return nil, fmt.Errorf("bundled Core SHA-256 mismatch: manifest=%s actual=%s", manifest.SHA256, digest)
	}
	return &Bundle{Dir: absolute, Manifest: manifest, BinaryPath: binaryPath}, nil
}

// Discover resolves the bundle passed by the caller, TERMCP_HARNESS_CORE_BUNDLE,
// or the conventional <executable-dir>/core directory. An absent optional
// bundle is not an error; the caller can continue with an external Core.
func Discover(requested string) (string, bool, error) {
	if value := strings.TrimSpace(requested); value != "" {
		return value, true, nil
	}
	if value := strings.TrimSpace(os.Getenv(EnvBundleDir)); value != "" {
		return value, true, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", false, fmt.Errorf("locate Harness executable: %w", err)
	}
	candidate := filepath.Join(filepath.Dir(executable), "core")
	if _, err := os.Stat(filepath.Join(candidate, ManifestName)); err == nil {
		return candidate, true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect conventional Core bundle: %w", err)
	}
	return "", false, nil
}

func BinaryFilename() string {
	if runtime.GOOS == "windows" {
		return defaultBinaryName + ".exe"
	}
	return defaultBinaryName
}

func validateManifest(manifest Manifest) error {
	if manifest.Schema != ManifestSchema {
		return fmt.Errorf("unsupported Core bundle manifest schema %d", manifest.Schema)
	}
	if strings.TrimSpace(manifest.Version) == "" || len(manifest.Version) > 128 {
		return errors.New("Core bundle version must be 1-128 characters")
	}
	if strings.TrimSpace(manifest.Binary) == "" {
		return errors.New("Core bundle binary path is required")
	}
	if decoded, err := hex.DecodeString(manifest.SHA256); err != nil || len(decoded) != sha256.Size {
		return errors.New("Core bundle SHA-256 must be 64 hexadecimal characters")
	}
	return nil
}

func binaryPathWithin(dir, name string) (string, error) {
	if filepath.IsAbs(name) {
		return "", errors.New("Core bundle binary path must be relative")
	}
	clean := filepath.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("Core bundle binary path escapes the bundle directory")
	}
	binaryPath := filepath.Join(dir, clean)
	relative, err := filepath.Rel(dir, binaryPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("Core bundle binary path escapes the bundle directory")
	}
	return binaryPath, nil
}

func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open Core binary: %w", err)
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return fmt.Errorf("create bundled Core binary: %w", err)
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		return errors.Join(
			wrapError("copy Core binary", copyErr),
			wrapError("sync bundled Core binary", syncErr),
			wrapError("close bundled Core binary", closeErr),
		)
	}
	return nil
}

func writeManifest(dir string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Core bundle manifest: %w", err)
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(dir, ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("create Core bundle manifest: %w", err)
	}
	temp := file.Name()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return fmt.Errorf("write Core bundle manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(temp)
		return fmt.Errorf("sync Core bundle manifest: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("close Core bundle manifest: %w", err)
	}
	if err := os.Rename(temp, filepath.Join(dir, ManifestName)); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("publish Core bundle manifest: %w", err)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file for hashing: %w", err)
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func wrapError(label string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", label, err)
}
