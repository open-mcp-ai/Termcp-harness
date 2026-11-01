package agentsession

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
)

func TestFileStoreRestoresCompletedModelTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Create("persisted-session", "core-1"); err != nil {
		t.Fatal(err)
	}
	adapter := &scriptedModelAdapter{responses: []model.Response{
		{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			Name: "termcp-core__term", Arguments: json.RawMessage(`{"cmd":""}`),
		}}}},
		{Message: model.Message{Role: model.RoleAssistant, Content: "persisted answer"}},
	}}
	invoker := &modelToolInvoker{statuses: []toolcall.Status{toolcall.StatusSucceeded}}
	result, err := manager.RunModel(
		context.Background(), "persisted-session", adapter, invoker, testModelTools(t), "persist this task", ModelRunOptions{},
	)
	if err != nil {
		t.Fatalf("RunModel() error = %v", err)
	}
	if result.Snapshot.State != StateCompleted || result.Snapshot.OutputBytes == 0 {
		t.Fatalf("result = %+v", result)
	}

	restored, err := NewManagerWithPersistence(eventlog.New(), store)
	if err != nil {
		t.Fatalf("restore manager: %v", err)
	}
	sessions := restored.List()
	if len(sessions) != 1 || sessions[0].State != StateCompleted || sessions[0].TranscriptMessages < 4 {
		t.Fatalf("restored sessions = %+v", sessions)
	}
	transcript, err := restored.Transcript("persisted-session")
	if err != nil {
		t.Fatal(err)
	}
	if transcript.Output != "persisted answer" || len(transcript.Messages) < 4 ||
		transcript.Session.OutputBytes != len("persisted answer") {
		t.Fatalf("transcript = %+v", transcript)
	}
}

func TestFileStoreLoadsVersionOneSessionWithoutCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
		"version": 1,
		"sessions": [{
			"snapshot": {"id":"legacy-session","state":"completed","revision":2},
			"messages": [{"role":"user","content":"legacy"}],
			"output": "legacy output"
		}]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].Snapshot.ID != "legacy-session" ||
		sessions[0].ModelLoopCheckpoint != nil {
		t.Fatalf("legacy sessions = %+v", sessions)
	}
}

func TestPersistentManagerRecoversInflightSessionAsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	referenceState := Snapshot{
		ID: "restart-session", CoreSessionID: "core-1", State: StateWaitingApproval,
		Revision: 5, CurrentCallID: "call-1", CurrentApprovalID: "approval-1",
		CurrentApprovalStatus: "approved", CurrentToolStatus: toolcall.StatusApprovalPending,
		CurrentCompletion: "completed",
	}
	if err := store.Save([]PersistedSession{{
		Snapshot: referenceState,
		Messages: []model.Message{{Role: model.RoleUser, Content: "unfinished"}},
	}}); err != nil {
		t.Fatal(err)
	}
	journal := eventlog.New()
	manager, err := NewManagerWithPersistence(journal, store)
	if err != nil {
		t.Fatalf("restore manager: %v", err)
	}
	snapshot, err := manager.Get("restart-session")
	if err != nil {
		t.Fatal(err)
	}
	restored := snapshot.Snapshot()
	if restored.State != StateUnknown || !restored.Recovered ||
		restored.RecoveryReason != persistenceRecoveryReason ||
		restored.CurrentToolStatus != toolcall.StatusUnknown ||
		restored.FinishedAt == nil || restored.CurrentApprovalID != "approval-1" {
		t.Fatalf("restored snapshot = %+v", restored)
	}
	window, err := journal.Query(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != 1 || window.Events[0].Type != eventlog.TypeAgentSessionUnknown {
		t.Fatalf("recovery events = %+v", window.Events)
	}
	var data map[string]any
	if err := json.Unmarshal(window.Events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["reason"] != persistenceRecoveryReason {
		t.Fatalf("recovery event = %+v", data)
	}
}

func TestFileStoreRejectsOversizedState(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	store.maxBytes = 64
	if err := store.Save([]PersistedSession{{Snapshot: Snapshot{ID: strings.Repeat("x", 100), State: StateNew}}}); err == nil {
		t.Fatal("oversized Agent Session store was accepted")
	}
}

func TestFileStoreConcurrentSavesRemainReadable(t *testing.T) {
	store, err := NewFileStore(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	const writes = 64
	errorsByWrite := make(chan error, writes)
	for index := range writes {
		go func() {
			errorsByWrite <- store.Save([]PersistedSession{{
				Snapshot: Snapshot{ID: "concurrent-session", State: StateCompleted, Revision: uint64(index + 1)},
				Output:   fmt.Sprintf("output-%d", index),
			}})
		}()
	}
	for range writes {
		if err := <-errorsByWrite; err != nil {
			t.Fatalf("Save() error = %v", err)
		}
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded) != 1 || loaded[0].Snapshot.ID != "concurrent-session" ||
		!strings.HasPrefix(loaded[0].Output, "output-") {
		t.Fatalf("loaded sessions = %+v", loaded)
	}
}

func TestManagerSerializesPersistenceSnapshots(t *testing.T) {
	persistence := &blockingPersistence{
		firstSaveStarted:  make(chan struct{}),
		secondSaveStarted: make(chan struct{}),
		allowFirstSave:    make(chan struct{}),
	}
	manager, err := NewManagerWithPersistence(eventlog.New(), persistence)
	if err != nil {
		t.Fatal(err)
	}

	createErrors := make(chan error, 2)
	go func() {
		_, err := manager.Create("session-a", "core-a")
		createErrors <- err
	}()
	waitForSignal(t, persistence.firstSaveStarted, "first persistence save")

	go func() {
		_, err := manager.Create("session-b", "core-b")
		createErrors <- err
	}()

	overlapped := false
	select {
	case <-persistence.secondSaveStarted:
		overlapped = true
	case <-time.After(200 * time.Millisecond):
	}
	close(persistence.allowFirstSave)
	for range 2 {
		if err := waitForError(t, createErrors, "session creation"); err != nil {
			t.Fatal(err)
		}
	}

	if overlapped {
		t.Fatal("persistence saves overlapped; an older snapshot can overwrite a newer one")
	}
	persistence.mu.Lock()
	defer persistence.mu.Unlock()
	if persistence.maxActive != 1 {
		t.Fatalf("maximum concurrent saves = %d, want 1", persistence.maxActive)
	}
	if len(persistence.savedIDs) != 2 {
		t.Fatalf("save calls = %d, want 2", len(persistence.savedIDs))
	}
	last := persistence.savedIDs[len(persistence.savedIDs)-1]
	if len(last) != 2 || last[0] != "session-a" || last[1] != "session-b" {
		t.Fatalf("last persisted sessions = %v, want [session-a session-b]", last)
	}
}

func TestFileStoreCreatesOwnerOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not expose POSIX owner/group/other mode bits")
	}
	path := filepath.Join(t.TempDir(), "sessions.json")
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(nil); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("store permissions = %o, want group/other disabled", info.Mode().Perm())
	}
}

type blockingPersistence struct {
	mu                sync.Mutex
	firstSaveStarted  chan struct{}
	secondSaveStarted chan struct{}
	allowFirstSave    chan struct{}
	firstStarted      sync.Once
	secondStarted     sync.Once
	calls             int
	active            int
	maxActive         int
	savedIDs          [][]string
}

func (p *blockingPersistence) Load() ([]PersistedSession, error) {
	return nil, nil
}

func (p *blockingPersistence) Save(sessions []PersistedSession) error {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.active++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
	ids := make([]string, len(sessions))
	for i, session := range sessions {
		ids[i] = session.Snapshot.ID
	}
	p.mu.Unlock()

	switch call {
	case 1:
		p.firstStarted.Do(func() { close(p.firstSaveStarted) })
		<-p.allowFirstSave
	case 2:
		p.secondStarted.Do(func() { close(p.secondSaveStarted) })
	}

	p.mu.Lock()
	p.savedIDs = append(p.savedIDs, ids)
	p.active--
	p.mu.Unlock()
	return nil
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForError(t *testing.T, errors <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-errors:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}
