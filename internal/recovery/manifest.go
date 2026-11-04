// Package recovery verifies that independently persisted Harness stores belong
// to the same last known-good recovery generation. It never claims cross-file
// atomicity.
package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/atomicfile"
)

const (
	ManifestVersion    = 1
	maxManifestBytes   = 1 << 20
	maxStoreBytes      = 256 << 20
	tempFilePrefix     = ".termcp-recovery-*.tmp"
	StoreEvents        = StoreName("events")
	StoreSessions      = StoreName("sessions")
	StoreApprovals     = StoreName("approvals")
	StateNotConfigured = State("not_configured")
	StateUnverified    = State("unverified")
	StateComplete      = State("complete")
	StatePartial       = State("partial")
	StoreNotConfigured = StoreState("not_configured")
	StoreMissing       = StoreState("missing")
	StorePresent       = StoreState("present")
	StoreUnreadable    = StoreState("unreadable")
)

type StoreName string
type State string
type StoreState string

type Config struct {
	ManifestPath string
	Stores       map[StoreName]string
}

type StoreSnapshot struct {
	Name        StoreName  `json:"name"`
	Configured  bool       `json:"configured"`
	State       StoreState `json:"state"`
	Exists      bool       `json:"exists"`
	Size        int64      `json:"size,omitempty"`
	Fingerprint string     `json:"fingerprint,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type Snapshot struct {
	ManifestConfigured bool            `json:"manifest_configured"`
	ManifestPresent    bool            `json:"manifest_present"`
	ManifestVersion    int             `json:"manifest_version,omitempty"`
	RecordedAt         *time.Time      `json:"recorded_at,omitempty"`
	State              State           `json:"state"`
	Consistent         bool            `json:"consistent"`
	Stores             []StoreSnapshot `json:"stores"`
	Warning            string          `json:"warning,omitempty"`
}

type Reader struct {
	snapshot Snapshot
}

func NewReader(snapshot Snapshot) *Reader {
	return &Reader{snapshot: cloneSnapshot(snapshot)}
}

func (r *Reader) RecoverySnapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	return cloneSnapshot(r.snapshot)
}

type manifestDocument struct {
	Version    int                      `json:"version"`
	RecordedAt time.Time                `json:"recorded_at"`
	Stores     map[string]manifestStore `json:"stores"`
}

type manifestStore struct {
	Exists      bool   `json:"exists"`
	Size        int64  `json:"size,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func Inspect(config Config) Snapshot {
	snapshot := Snapshot{
		ManifestConfigured: strings.TrimSpace(config.ManifestPath) != "",
		State:              StateNotConfigured,
		Stores:             inspectStores(config.Stores),
	}
	configured := configuredStoreCount(snapshot.Stores)
	if configured == 0 {
		snapshot.Consistent = true
		return snapshot
	}
	for _, store := range snapshot.Stores {
		if store.Configured && store.State == StoreUnreadable {
			snapshot.State = StatePartial
			snapshot.Warning = fmt.Sprintf("%s Store 无法读取：%s", store.Name, store.Error)
			return snapshot
		}
	}
	if !snapshot.ManifestConfigured {
		snapshot.State = StateUnverified
		snapshot.Warning = "恢复清单未配置；各 Store 可独立读取，但不能证明属于同一恢复代次。"
		return snapshot
	}

	manifest, err := readManifest(config.ManifestPath)
	if errors.Is(err, os.ErrNotExist) {
		snapshot.State = StateUnverified
		snapshot.Warning = "恢复清单尚不存在；本次恢复无法证明各 Store 属于同一代次。"
		return snapshot
	}
	if err != nil {
		snapshot.State = StatePartial
		snapshot.Warning = "恢复清单不可用：" + safeError(err)
		return snapshot
	}
	snapshot.ManifestPresent = true
	snapshot.ManifestVersion = manifest.Version
	recordedAt := manifest.RecordedAt.UTC()
	snapshot.RecordedAt = &recordedAt

	if manifest.Version != ManifestVersion {
		snapshot.State = StatePartial
		snapshot.Warning = fmt.Sprintf("恢复清单版本 %d 不受支持。", manifest.Version)
		return snapshot
	}
	if !storesMatchManifest(snapshot.Stores, manifest.Stores) {
		snapshot.State = StatePartial
		snapshot.Warning = "一个或多个 Store 与最后已知完整恢复清单不一致。"
		return snapshot
	}
	snapshot.State = StateComplete
	snapshot.Consistent = true
	return snapshot
}

func WriteManifest(config Config) (Snapshot, error) {
	if strings.TrimSpace(config.ManifestPath) == "" {
		return Inspect(config), nil
	}
	if configuredStoreCountFromPaths(config.Stores) == 0 {
		return Inspect(config), errors.New("recovery manifest requires at least one configured store")
	}
	stores := inspectStores(config.Stores)
	for _, store := range stores {
		if store.Configured && store.State == StoreUnreadable {
			return Inspect(config), fmt.Errorf("inspect %s store: %s", store.Name, store.Error)
		}
	}
	manifest := manifestDocument{
		Version:    ManifestVersion,
		RecordedAt: time.Now().UTC(),
		Stores:     make(map[string]manifestStore, len(stores)),
	}
	for _, store := range stores {
		if !store.Configured {
			continue
		}
		manifest.Stores[string(store.Name)] = manifestStore{
			Exists:      store.Exists,
			Size:        store.Size,
			Fingerprint: store.Fingerprint,
		}
	}
	if err := writeManifestAtomically(config.ManifestPath, manifest); err != nil {
		return Inspect(config), err
	}
	return Inspect(config), nil
}

func inspectStores(paths map[StoreName]string) []StoreSnapshot {
	names := []StoreName{StoreEvents, StoreSessions, StoreApprovals}
	for name := range paths {
		if name != StoreEvents && name != StoreSessions && name != StoreApprovals {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	result := make([]StoreSnapshot, 0, len(names))
	for _, name := range names {
		path := strings.TrimSpace(paths[name])
		store := StoreSnapshot{Name: name, Configured: path != "", State: StoreNotConfigured}
		if path == "" {
			result = append(result, store)
			continue
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			store.State = StoreUnreadable
			store.Error = "路径无法解析"
			result = append(result, store)
			continue
		}
		info, err := os.Stat(absolute)
		if errors.Is(err, os.ErrNotExist) {
			store.State = StoreMissing
			result = append(result, store)
			continue
		}
		if err != nil {
			store.State = StoreUnreadable
			store.Error = safeError(err)
			result = append(result, store)
			continue
		}
		if !info.Mode().IsRegular() {
			store.State = StoreUnreadable
			store.Error = "Store 不是普通文件"
			result = append(result, store)
			continue
		}
		if info.Size() > maxStoreBytes {
			store.State = StoreUnreadable
			store.Error = fmt.Sprintf("Store 超过 %d 字节", maxStoreBytes)
			result = append(result, store)
			continue
		}
		fingerprint, err := fingerprintFile(absolute)
		if err != nil {
			store.State = StoreUnreadable
			store.Error = safeError(err)
			result = append(result, store)
			continue
		}
		store.Exists = true
		store.State = StorePresent
		store.Size = info.Size()
		store.Fingerprint = fingerprint
		result = append(result, store)
	}
	return result
}

func readManifest(path string) (manifestDocument, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return manifestDocument{}, err
	}
	defer file.Close()
	limited := io.LimitReader(file, maxManifestBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return manifestDocument{}, err
	}
	if len(data) > maxManifestBytes {
		return manifestDocument{}, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	var document manifestDocument
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return manifestDocument{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return manifestDocument{}, errors.New("manifest contains multiple JSON values")
		}
		return manifestDocument{}, err
	}
	if document.Version == 0 || document.RecordedAt.IsZero() || document.Stores == nil {
		return manifestDocument{}, errors.New("manifest is incomplete")
	}
	return document, nil
}

func writeManifestAtomically(path string, document manifestDocument) error {
	absolute, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return fmt.Errorf("resolve recovery manifest path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return fmt.Errorf("create recovery manifest directory: %w", err)
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recovery manifest: %w", err)
	}
	if len(data) > maxManifestBytes {
		return fmt.Errorf("recovery manifest exceeds %d bytes", maxManifestBytes)
	}
	temp, err := os.CreateTemp(filepath.Dir(absolute), tempFilePrefix)
	if err != nil {
		return fmt.Errorf("create recovery manifest temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set recovery manifest permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write recovery manifest: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("sync recovery manifest: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close recovery manifest: %w", err)
	}
	if err := atomicfile.Replace(tempPath, absolute); err != nil {
		return fmt.Errorf("replace recovery manifest: %w", err)
	}
	_ = os.Chmod(absolute, 0o600)
	return nil
}

func fingerprintFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func storesMatchManifest(stores []StoreSnapshot, recorded map[string]manifestStore) bool {
	configured := 0
	for _, store := range stores {
		if store.Configured {
			configured++
			expected, ok := recorded[string(store.Name)]
			if !ok {
				return false
			}
			if expected.Exists != store.Exists ||
				expected.Size != store.Size ||
				expected.Fingerprint != store.Fingerprint {
				return false
			}
			continue
		}
		if _, ok := recorded[string(store.Name)]; ok {
			return false
		}
	}
	return configured == len(recorded)
}

func configuredStoreCount(stores []StoreSnapshot) int {
	count := 0
	for _, store := range stores {
		if store.Configured {
			count++
		}
	}
	return count
}

func configuredStoreCountFromPaths(paths map[StoreName]string) int {
	count := 0
	for _, path := range paths {
		if strings.TrimSpace(path) != "" {
			count++
		}
	}
	return count
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, os.ErrPermission):
		return "权限不足"
	case errors.Is(err, os.ErrNotExist):
		return "文件不存在"
	default:
		return "读取失败"
	}
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	cloned := snapshot
	cloned.Stores = append([]StoreSnapshot(nil), snapshot.Stores...)
	if snapshot.RecordedAt != nil {
		value := *snapshot.RecordedAt
		cloned.RecordedAt = &value
	}
	return cloned
}
