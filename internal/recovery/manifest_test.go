package recovery

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotConfiguredRecoveryIsNeutral(t *testing.T) {
	snapshot := Inspect(Config{})

	if snapshot.State != StateNotConfigured || !snapshot.Consistent {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if len(snapshot.Stores) != 3 {
		t.Fatalf("stores = %+v", snapshot.Stores)
	}
	for _, store := range snapshot.Stores {
		if store.Configured || store.State != StoreNotConfigured {
			t.Fatalf("store = %+v", store)
		}
	}
}

func TestConfiguredStoresWithoutManifestAreUnverified(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	writeTestFile(t, config.Stores[StoreEvents], "events")

	snapshot := Inspect(config)

	if snapshot.State != StateUnverified || snapshot.Consistent {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if !strings.Contains(snapshot.Warning, "无法证明") {
		t.Fatalf("warning = %q", snapshot.Warning)
	}
}

func TestManifestCertifiesMatchingStoreGeneration(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	writeTestFile(t, config.Stores[StoreEvents], "events")
	writeTestFile(t, config.Stores[StoreSessions], "sessions")

	written, err := WriteManifest(config)
	if err != nil {
		t.Fatal(err)
	}
	restored := Inspect(config)

	if written.State != StateComplete || !written.Consistent {
		t.Fatalf("written snapshot = %+v", written)
	}
	if restored.State != StateComplete || !restored.Consistent ||
		!restored.ManifestPresent || restored.ManifestVersion != ManifestVersion {
		t.Fatalf("restored snapshot = %+v", restored)
	}
	for _, store := range restored.Stores {
		if store.Name == StoreApprovals {
			if store.Configured || store.State != StoreNotConfigured {
				t.Fatalf("approval store = %+v", store)
			}
			continue
		}
		if !store.Configured || !store.Exists || store.Fingerprint == "" {
			t.Fatalf("store = %+v", store)
		}
	}
}

func TestChangedOrMissingStoreMakesRecoveryPartial(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	writeTestFile(t, config.Stores[StoreEvents], "events-v1")
	writeTestFile(t, config.Stores[StoreSessions], "sessions-v1")
	if _, err := WriteManifest(config); err != nil {
		t.Fatal(err)
	}

	writeTestFile(t, config.Stores[StoreSessions], "sessions-v2")
	if snapshot := Inspect(config); snapshot.State != StatePartial || snapshot.Consistent {
		t.Fatalf("changed snapshot = %+v", snapshot)
	}

	if err := os.Remove(config.Stores[StoreEvents]); err != nil {
		t.Fatal(err)
	}
	if snapshot := Inspect(config); snapshot.State != StatePartial || snapshot.Consistent {
		t.Fatalf("missing snapshot = %+v", snapshot)
	}
}

func TestCorruptManifestFailsPartialWithoutLeakingPath(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	writeTestFile(t, config.Stores[StoreEvents], "events")
	if err := os.WriteFile(config.ManifestPath, []byte("{invalid"), 0o600); err != nil {
		t.Fatal(err)
	}

	snapshot := Inspect(config)
	if snapshot.State != StatePartial || snapshot.Consistent {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), root) || strings.Contains(string(encoded), "events.json") {
		t.Fatalf("snapshot leaked store path: %s", encoded)
	}
}

func TestManifestRejectsUnreadableStoreAndIncompleteManifest(t *testing.T) {
	root := t.TempDir()
	config := testConfig(root)
	if err := os.Mkdir(config.Stores[StoreEvents], 0o700); err != nil {
		t.Fatal(err)
	}
	if snapshot := Inspect(config); snapshot.State != StatePartial {
		t.Fatalf("unreadable snapshot = %+v", snapshot)
	}
	if _, err := WriteManifest(config); err == nil {
		t.Fatal("WriteManifest() accepted a directory as a store")
	}

	config.Stores[StoreEvents] = filepath.Join(root, "events.json")
	if err := os.WriteFile(config.ManifestPath, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if snapshot := Inspect(config); snapshot.State != StatePartial {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func testConfig(root string) Config {
	return Config{
		ManifestPath: filepath.Join(root, "recovery.json"),
		Stores: map[StoreName]string{
			StoreEvents:   filepath.Join(root, "events.json"),
			StoreSessions: filepath.Join(root, "sessions.json"),
		},
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
