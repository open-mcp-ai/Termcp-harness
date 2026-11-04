package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/controlclient"
	"github.com/open-mcp-ai/termcp-harness/internal/runtimeapp"
)

func TestEmbeddedRuntimeTerminalBridgeIntegration(t *testing.T) {
	bundleDir := strings.TrimSpace(os.Getenv("TERMCP_EMBEDDED_CORE_BUNDLE"))
	if strings.TrimSpace(os.Getenv("TERMCP_EMBEDDED_DESKTOP_E2E")) != "1" {
		t.Skip("set TERMCP_EMBEDDED_DESKTOP_E2E=1 and TERMCP_EMBEDDED_CORE_BUNDLE")
	}
	if bundleDir == "" {
		t.Fatal("TERMCP_EMBEDDED_CORE_BUNDLE is required")
	}

	root := t.TempDir()
	owned, approvalToken, err := startEmbeddedRuntime(context.Background(), embeddedRuntimeOptions{
		CoreBundleDir:     bundleDir,
		CoreDataDir:       filepath.Join(root, "core"),
		EventLogFile:      filepath.Join(root, "events.json"),
		SessionStoreFile:  filepath.Join(root, "sessions.json"),
		ApprovalStoreFile: filepath.Join(root, "approvals.json"),
		RecoveryManifest:  filepath.Join(root, "recovery.json"),
		DesktopLogFile:    filepath.Join(root, "desktop.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	access := owned.CoreAccess()
	if access.Endpoint == "" || access.AuthToken == "" || !access.Managed {
		_ = owned.Close(context.Background())
		t.Fatalf("CoreAccess() = %+v", access)
	}

	coreSessionID, shellID := createEmbeddedCoreSession(t, access)

	client, err := controlclient.New(owned.Endpoint())
	if err != nil {
		t.Fatal(err)
	}
	agentSessionID := fmt.Sprintf("agent-embedded-terminal-%d", time.Now().UnixNano())
	if _, err := client.CreateAgentSession(
		context.Background(),
		agentSessionID,
		coreSessionID,
		approvalToken,
	); err != nil {
		t.Fatal(err)
	}

	app := newAppWithRuntime(client, approvalToken, owned)
	app.ctx = context.Background()
	defer func() {
		deleteEmbeddedCoreSession(t, access, coreSessionID)
		app.shutdown(context.Background())
	}()

	shells, err := app.TerminalShells(agentSessionID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, shell := range shells.Shells {
		if shell.ID == shellID {
			found = true
		}
	}
	if !found {
		t.Fatalf("Shell %q not found in %+v", shellID, shells.Shells)
	}
	opened, err := app.TerminalOpen(agentSessionID, shellID, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer app.TerminalClose(opened.ID)

	marker := fmt.Sprintf("embedded-terminal-%d", time.Now().UnixNano())
	if err := app.TerminalInput(opened.ID, "echo "+marker+"\r"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var output strings.Builder
	for time.Now().Before(deadline) {
		polled, err := app.TerminalPoll(opened.ID)
		if err != nil {
			t.Fatal(err)
		}
		output.WriteString(polled.Output)
		if strings.Contains(output.String(), marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("terminal marker did not arrive: %q", output.String())
}

func createEmbeddedCoreSession(
	t *testing.T,
	access runtimeapp.CoreAccess,
) (string, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"ssh_config": "internal",
		"name":       fmt.Sprintf("embedded-terminal-%d", time.Now().UnixNano()),
		"mode":       "pty",
		"rows":       24,
		"cols":       80,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		strings.TrimRight(access.Endpoint, "/")+"/api/sessions",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+access.AuthToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("create Core Session status = %d", response.StatusCode)
	}
	var created struct {
		SessionID string `json:"session_id"`
		ShellID   string `json:"shell_id"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.SessionID == "" || created.ShellID == "" {
		t.Fatalf("created Core Session = %+v", created)
	}
	return created.SessionID, created.ShellID
}

func deleteEmbeddedCoreSession(
	t *testing.T,
	access runtimeapp.CoreAccess,
	sessionID string,
) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodDelete,
		strings.TrimRight(access.Endpoint, "/")+"/api/sessions/"+sessionID,
		nil,
	)
	if err != nil {
		t.Error(err)
		return
	}
	request.Header.Set("Authorization", "Bearer "+access.AuthToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Error(err)
		return
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Errorf("delete Core Session status = %d", response.StatusCode)
	}
}
