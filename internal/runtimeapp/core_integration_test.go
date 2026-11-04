package runtimeapp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
)

func TestEmbeddedRuntimeStartsAndOwnsBundledCore(t *testing.T) {
	bundleDir := strings.TrimSpace(os.Getenv("TERMCP_RUNTIMEAPP_CORE_BUNDLE"))
	if bundleDir == "" {
		t.Skip("set TERMCP_RUNTIMEAPP_CORE_BUNDLE to run the bundled Core integration test")
	}
	root := t.TempDir()
	app, err := Start(context.Background(), Config{
		Version:           "embedded-e2e",
		ListenAddr:        "127.0.0.1:0",
		CoreBundleDir:     bundleDir,
		CoreDataDir:       filepath.Join(root, "core"),
		CoreStartTimeout:  30 * time.Second,
		ApprovalToken:     "0123456789abcdef0123456789abcdef",
		EventLogFile:      filepath.Join(root, "events.json"),
		SessionStoreFile:  filepath.Join(root, "sessions.json"),
		ApprovalStoreFile: filepath.Join(root, "approvals.json"),
		RecoveryManifest:  filepath.Join(root, "recovery.json"),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	access := app.CoreAccess()
	if !access.Managed || access.Endpoint == "" || access.AuthToken == "" {
		t.Fatalf("CoreAccess() = %+v", access)
	}
	defer func() {
		closeContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if closeErr := app.Close(closeContext); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}()

	response, err := http.Get(app.Endpoint() + "/v1/core")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var core struct {
		Configured bool                  `json:"configured"`
		Core       coreinstance.Snapshot `json:"core"`
	}
	if err := json.NewDecoder(response.Body).Decode(&core); err != nil {
		t.Fatal(err)
	}
	if !core.Configured || core.Core.State != coreinstance.StateIdentified {
		t.Fatalf("Core snapshot = %+v", core.Core)
	}
	if core.Core.Ownership != coreinstance.OwnershipManaged {
		t.Fatalf("Core ownership = %q, want %q", core.Core.Ownership, coreinstance.OwnershipManaged)
	}
}
