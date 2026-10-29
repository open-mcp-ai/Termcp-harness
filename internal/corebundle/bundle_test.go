package corebundle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareAndLoad(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source-core")
	if err := os.WriteFile(source, []byte("fake-core-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "bundle")
	bundle, err := Prepare(source, "v0.2.8", out)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if bundle.Manifest.Version != "v0.2.8" || bundle.Manifest.SHA256 == "" {
		t.Fatalf("manifest = %+v", bundle.Manifest)
	}
	if _, err := os.Stat(bundle.BinaryPath); err != nil {
		t.Fatalf("bundled binary: %v", err)
	}
	loaded, err := Load(out)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Manifest != bundle.Manifest || loaded.BinaryPath != bundle.BinaryPath {
		t.Fatalf("loaded = %+v, want %+v", loaded, bundle)
	}
	if _, err := Prepare(source, "v0.2.8", out); err == nil {
		t.Fatal("Prepare() overwrote an existing bundle")
	}
}

func TestLoadRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	manifest := Manifest{Schema: ManifestSchema, Version: "v0.2.8", Binary: "../core", SHA256: strings.Repeat("a", 64)}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "termcp-core"), []byte("tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Schema: ManifestSchema, Version: "v0.2.8", Binary: "termcp-core", SHA256: strings.Repeat("0", 64)}
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(dir, ManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("Load() error = %v", err)
	}
}
