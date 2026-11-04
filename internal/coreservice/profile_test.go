package coreservice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileRoundTripDoesNotContainTokenValue(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "termcp-core.exe")
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	tokenPath := filepath.Join(root, "core.token")
	if err := os.WriteFile(tokenPath, []byte("secret-token-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := FileSHA256(binary)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "core-service.json")
	saved, err := SaveProfile(path, Profile{
		Schema:          ProfileSchema,
		BinaryPath:      binary,
		ExpectedVersion: "v0.2.8",
		BinarySHA256:    digest,
		Endpoint:        "http://127.0.0.1:18765",
		DataDir:         filepath.Join(root, "data"),
		AuthTokenFile:   tokenPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Endpoint != "http://127.0.0.1:18765" {
		t.Fatalf("endpoint = %q", saved.Endpoint)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "secret-token-value") {
		t.Fatal("profile stored the token value")
	}
	loaded, present, err := LoadProfile(path)
	if err != nil || !present {
		t.Fatalf("LoadProfile() = %+v, %t, %v", loaded, present, err)
	}
	if loaded.BinarySHA256 != digest || loaded.AuthTokenFile != tokenPath {
		t.Fatalf("loaded profile = %+v", loaded)
	}
}

func TestEndpointHostPort(t *testing.T) {
	host, port, err := endpointHostPort("http://127.0.0.1:19000")
	if err != nil || host != "127.0.0.1" || port != 19000 {
		t.Fatalf("endpointHostPort() = %q, %d, %v", host, port, err)
	}
	if _, _, err := endpointHostPort("http://127.0.0.1:19000/stream"); err == nil {
		t.Fatal("endpoint with path was accepted")
	}
}

func TestVerifyDataDirectory(t *testing.T) {
	root := t.TempDir()
	if err := verifyDataDirectory(root, filepath.Join(root, "termcp.log")); err != nil {
		t.Fatal(err)
	}
	if err := verifyDataDirectory(root, filepath.Join(filepath.Dir(root), "outside.log")); err == nil {
		t.Fatal("outside log path was accepted")
	}
}

func TestInspectBinaryRejectsMalformedOutput(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "fake-core.exe")
	if err := os.WriteFile(binary, []byte("not executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := InspectBinary(context.Background(), binary); err == nil {
		t.Fatal("malformed executable was accepted")
	}
}
