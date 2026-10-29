package toolpipeline_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

type approvalListResponse struct {
	Count     int            `json:"count"`
	Approvals []approvalItem `json:"approvals"`
}

type approvalItem struct {
	Need    int             `json:"need"`
	Request approvalRequest `json:"request"`
}

type approvalRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Summary   string `json:"summary"`
	State     string `json:"state"`
}

// TestTermcpCoreApprovalReviewIntegration is opt-in because it enables review
// on disposable sessions and submits real file operations.
func TestTermcpCoreApprovalReviewIntegration(t *testing.T) {
	if os.Getenv("TERMCP_CORE_APPROVAL_TEST") != "1" {
		t.Skip("set TERMCP_CORE_APPROVAL_TEST=1 and provide dedicated effect/observer sessions")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	effectSessionID := os.Getenv("TERMCP_CORE_TEST_EFFECT_SESSION_ID")
	observerSessionID := os.Getenv("TERMCP_CORE_TEST_OBSERVER_SESSION_ID")
	effectHandle := os.Getenv("TERMCP_CORE_TEST_EFFECT_HANDLE")
	tempDir := os.Getenv("TERMCP_CORE_TEST_TEMP_DIR")
	if endpoint == "" || effectSessionID == "" || observerSessionID == "" {
		t.Fatal("TERMCP_CORE_MCP_URL, TERMCP_CORE_TEST_EFFECT_SESSION_ID and TERMCP_CORE_TEST_OBSERVER_SESSION_ID are required")
	}
	if effectHandle == "" {
		effectHandle = effectSessionID + ":1"
	}
	if tempDir == "" {
		tempDir = "/tmp"
	}

	baseURL := strings.TrimSuffix(endpoint, "/stream")
	effectHost := connectCoreHost(t, endpoint)
	observerHost := connectCoreHost(t, endpoint)
	defer observerHost.Close()
	defer func() {
		_ = setReview(context.Background(), baseURL, effectSessionID, false, 0)
	}()

	stamp := fmt.Sprint(time.Now().UnixNano())
	approvedPath := path.Join(tempDir, "termcp-harness-approved-"+stamp+".txt")
	rejectedPath := path.Join(tempDir, "termcp-harness-rejected-"+stamp+".txt")
	concurrentPath := path.Join(tempDir, "termcp-harness-concurrent-"+stamp+".txt")
	cancelledPath := path.Join(tempDir, "termcp-harness-cancelled-"+stamp+".txt")
	defer func() {
		removePOSIXFiles(t, observerHost, observerSessionID, approvedPath, rejectedPath, concurrentPath, cancelledPath)
	}()

	if err := setReview(context.Background(), baseURL, effectSessionID, true, 60); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	approvedToken := "APPROVED_" + stamp
	approvedArgs := termArguments("write $1 file $2 --data $3", effectSessionID, approvedPath, approvedToken)
	approvedOutcome, err := invokeReviewWrite(t, effectHost, "approval-pending", approvedArgs)
	if err != nil || approvedOutcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("review pending outcome = %+v, error = %v, result = %s", approvedOutcome.Call, err, resultText(t, approvedOutcome.Result))
	}
	assertRemoteFileMissing(t, observerHost, observerSessionID, approvedPath)
	pending := waitForApprovalState(t, baseURL, effectSessionID, "", "pending", 5*time.Second)

	// The pending request must survive the submitting MCP connection closing.
	if err := effectHost.Close(); err != nil {
		t.Fatalf("close submitting MCP host: %v", err)
	}
	if got := waitForApprovalState(t, baseURL, effectSessionID, pending.Request.ID, "pending", 3*time.Second); got.Request.ID != pending.Request.ID {
		t.Fatalf("pending request changed after disconnect: %+v", got)
	}
	if status := decideApproval(t, baseURL, pending.Request.ID, "approve"); status != http.StatusOK {
		t.Fatalf("approve status = %d, want 200", status)
	}
	_ = waitForApprovalState(t, baseURL, effectSessionID, pending.Request.ID, "approved", 3*time.Second)
	approvedText := directRead(t, observerHost, observerSessionID, approvedPath)
	if strings.Count(approvedText, approvedToken) != 1 {
		t.Fatalf("approved file text = %q", approvedText)
	}
	if status := decideApproval(t, baseURL, pending.Request.ID, "reject"); status != http.StatusConflict {
		t.Fatalf("second decision status = %d, want 409", status)
	}
	removeRemoteFile(t, observerHost, effectSessionID, approvedPath)

	effectHost = connectCoreHost(t, endpoint)
	defer effectHost.Close()
	rejectedPathToken := "REJECTED_" + stamp
	rejectedArgs := termArguments("write $1 file $2 --data $3", effectSessionID, rejectedPath, rejectedPathToken)
	rejectedOutcome, err := invokeReviewWrite(t, effectHost, "approval-rejected", rejectedArgs)
	if err != nil || rejectedOutcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("rejected scenario pending = %+v, error = %v", rejectedOutcome.Call, err)
	}
	rejected := waitForApprovalState(t, baseURL, effectSessionID, "", "pending", 5*time.Second)
	if status := decideApproval(t, baseURL, rejected.Request.ID, "reject"); status != http.StatusOK {
		t.Fatalf("reject status = %d, want 200", status)
	}
	_ = waitForApprovalState(t, baseURL, effectSessionID, rejected.Request.ID, "rejected", 3*time.Second)
	assertRemoteFileMissing(t, observerHost, observerSessionID, rejectedPath)
	if status := decideApproval(t, baseURL, rejected.Request.ID, "approve"); status != http.StatusConflict {
		t.Fatalf("approve after reject status = %d, want 409", status)
	}

	if err := setReview(context.Background(), baseURL, effectSessionID, true, 60); err != nil {
		t.Fatalf("re-enable review for concurrency: %v", err)
	}
	concurrentToken := "CONCURRENT_" + stamp
	concurrentArgs := termArguments("write $1 file $2 --data $3", effectSessionID, concurrentPath, concurrentToken)
	concurrentOutcome, err := invokeReviewWrite(t, effectHost, "approval-concurrent", concurrentArgs)
	if err != nil || concurrentOutcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("concurrent scenario pending = %+v, error = %v", concurrentOutcome.Call, err)
	}
	concurrent := waitForApprovalState(t, baseURL, effectSessionID, "", "pending", 5*time.Second)
	statuses := concurrentDecisions(t, baseURL, concurrent.Request.ID)
	sort.Ints(statuses)
	if len(statuses) != 2 || statuses[0] != http.StatusOK || statuses[1] != http.StatusConflict {
		t.Fatalf("concurrent decision statuses = %v, want [200 409]", statuses)
	}
	finalConcurrent := findApproval(t, baseURL, effectSessionID, concurrent.Request.ID)
	switch finalConcurrent.Request.State {
	case "approved":
		text := directRead(t, observerHost, observerSessionID, concurrentPath)
		if strings.Count(text, concurrentToken) != 1 {
			t.Fatalf("approved concurrent file text = %q", text)
		}
		removeRemoteFile(t, observerHost, effectSessionID, concurrentPath)
	case "rejected":
		assertRemoteFileMissing(t, observerHost, observerSessionID, concurrentPath)
	default:
		t.Fatalf("concurrent final state = %q, want approved or rejected", finalConcurrent.Request.State)
	}

	if err := setReview(context.Background(), baseURL, effectSessionID, true, 60); err != nil {
		t.Fatalf("re-enable review for cancellation: %v", err)
	}
	cancelledToken := "CANCELLED_" + stamp
	cancelledArgs := termArguments("write $1 file $2 --data $3", effectSessionID, cancelledPath, cancelledToken)
	cancelledOutcome, err := invokeReviewWrite(t, effectHost, "approval-cancelled", cancelledArgs)
	if err != nil || cancelledOutcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("cancellation scenario pending = %+v, error = %v", cancelledOutcome.Call, err)
	}
	cancelled := waitForApprovalState(t, baseURL, effectSessionID, "", "pending", 5*time.Second)
	if result, err := effectHost.CallTool(context.Background(), "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "term", Arguments: termArguments("close $1", effectSessionID),
	}}); err != nil || result == nil || result.IsError {
		t.Fatalf("close effect session: result=%+v error=%v", result, err)
	}
	time.Sleep(500 * time.Millisecond)
	status := decideApproval(t, baseURL, cancelled.Request.ID, "approve")
	if status != http.StatusNotFound && status != http.StatusConflict {
		t.Fatalf("approve after session close status = %d, want 404 or 409", status)
	}
	assertRemoteFileMissing(t, observerHost, observerSessionID, cancelledPath)
}

// TestTermcpCoreApprovalExpiryContract is opt-in. It expects review expiry to
// be fail-closed even when no waiter is consuming the request. Core 0.2.8 is
// known to fail this contract test: after the deadline, approve executes.
func TestTermcpCoreApprovalExpiryContract(t *testing.T) {
	if os.Getenv("TERMCP_CORE_APPROVAL_EXPIRY_TEST") != "1" {
		t.Skip("set TERMCP_CORE_APPROVAL_EXPIRY_TEST=1 and provide dedicated effect/observer sessions")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	effectSessionID := os.Getenv("TERMCP_CORE_TEST_EFFECT_SESSION_ID")
	observerSessionID := os.Getenv("TERMCP_CORE_TEST_OBSERVER_SESSION_ID")
	tempDir := os.Getenv("TERMCP_CORE_TEST_TEMP_DIR")
	if endpoint == "" || effectSessionID == "" || observerSessionID == "" {
		t.Fatal("TERMCP_CORE_MCP_URL, TERMCP_CORE_TEST_EFFECT_SESSION_ID and TERMCP_CORE_TEST_OBSERVER_SESSION_ID are required")
	}
	if tempDir == "" {
		tempDir = "/tmp"
	}

	baseURL := strings.TrimSuffix(endpoint, "/stream")
	effectHost := connectCoreHost(t, endpoint)
	observerHost := connectCoreHost(t, endpoint)
	defer effectHost.Close()
	defer observerHost.Close()
	path := path.Join(tempDir, fmt.Sprintf("termcp-harness-expired-%d.txt", time.Now().UnixNano()))
	token := "EXPIRED_" + fmt.Sprint(time.Now().UnixNano())
	defer func() { removeRemoteFile(t, observerHost, effectSessionID, path) }()
	defer func() { _ = setReview(context.Background(), baseURL, effectSessionID, false, 0) }()

	if err := setReview(context.Background(), baseURL, effectSessionID, true, 1); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	outcome, err := invokeReviewWrite(t, effectHost, "approval-expired", termArguments(
		"write $1 file $2 --data $3", effectSessionID, path, token,
	))
	if err != nil || outcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("expiry scenario pending = %+v, error = %v", outcome.Call, err)
	}
	pending := waitForApprovalState(t, baseURL, effectSessionID, "", "pending", 5*time.Second)
	time.Sleep(2 * time.Second)
	status := decideApproval(t, baseURL, pending.Request.ID, "approve")
	if status == http.StatusOK {
		text := directRead(t, observerHost, observerSessionID, path)
		t.Fatalf("expired request was approved and executed instead of failing closed: status=%d file=%q", status, text)
	}
	if status != http.StatusConflict {
		t.Fatalf("expired approval status = %d, want 409", status)
	}
	_ = waitForApprovalState(t, baseURL, effectSessionID, pending.Request.ID, "expired", 2*time.Second)
	assertRemoteFileMissing(t, observerHost, observerSessionID, path)
}

// TestTermcpCoreApprovalSessionDeathStateContract is opt-in. It expects a
// pending request to remain visible as cancelled after its session closes.
// Core 0.2.8 is known to fail the visibility assertion because the approval
// queue is detached and the request disappears from /api/approvals.
func TestTermcpCoreApprovalSessionDeathStateContract(t *testing.T) {
	if os.Getenv("TERMCP_CORE_APPROVAL_SESSION_DEATH_TEST") != "1" {
		t.Skip("set TERMCP_CORE_APPROVAL_SESSION_DEATH_TEST=1 and provide dedicated effect/observer sessions")
	}
	endpoint := os.Getenv("TERMCP_CORE_MCP_URL")
	effectSessionID := os.Getenv("TERMCP_CORE_TEST_EFFECT_SESSION_ID")
	observerSessionID := os.Getenv("TERMCP_CORE_TEST_OBSERVER_SESSION_ID")
	tempDir := os.Getenv("TERMCP_CORE_TEST_TEMP_DIR")
	if endpoint == "" || effectSessionID == "" || observerSessionID == "" {
		t.Fatal("TERMCP_CORE_MCP_URL, TERMCP_CORE_TEST_EFFECT_SESSION_ID and TERMCP_CORE_TEST_OBSERVER_SESSION_ID are required")
	}
	if tempDir == "" {
		tempDir = "/tmp"
	}

	baseURL := strings.TrimSuffix(endpoint, "/stream")
	effectHost := connectCoreHost(t, endpoint)
	observerHost := connectCoreHost(t, endpoint)
	defer observerHost.Close()
	path := path.Join(tempDir, fmt.Sprintf("termcp-harness-cancelled-state-%d.txt", time.Now().UnixNano()))
	removePOSIXFiles(t, observerHost, observerSessionID, path)

	if err := setReview(context.Background(), baseURL, effectSessionID, true, 60); err != nil {
		t.Fatalf("enable review: %v", err)
	}
	outcome, err := invokeReviewWrite(t, effectHost, "approval-session-death", termArguments(
		"write $1 file $2 --data $3", effectSessionID, path, "CANCELLED_"+fmt.Sprint(time.Now().UnixNano()),
	))
	if err != nil || outcome.Call.Status != toolcall.StatusApprovalPending {
		t.Fatalf("session death scenario pending = %+v, error = %v", outcome.Call, err)
	}
	pending := waitForApprovalState(t, baseURL, effectSessionID, "", "pending", 5*time.Second)
	if result, err := effectHost.CallTool(context.Background(), "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "term", Arguments: termArguments("close $1", effectSessionID),
	}}); err != nil || result == nil || result.IsError {
		t.Fatalf("close effect session: result=%+v error=%v", result, err)
	}
	time.Sleep(500 * time.Millisecond)
	if status := decideApproval(t, baseURL, pending.Request.ID, "approve"); status != http.StatusNotFound && status != http.StatusConflict {
		t.Fatalf("approve after session close status = %d, want 404 or 409", status)
	}
	assertRemoteFileMissing(t, observerHost, observerSessionID, path)
	_ = waitForApprovalState(t, baseURL, effectSessionID, pending.Request.ID, "cancelled", 3*time.Second)
}

func invokeReviewWrite(t *testing.T, host *mcpcontrol.Host, callID string, arguments json.RawMessage) (toolpipeline.Outcome, error) {
	t.Helper()
	policy, err := toolpipeline.NewAllowlistPolicy(toolpipeline.Rule{
		ServerID:  "termcp-core",
		ToolName:  "term",
		Effect:    toolcall.EffectIdempotentWrite,
		Arguments: arguments,
		Reason:    "approval integration write",
	})
	if err != nil {
		t.Fatalf("NewAllowlistPolicy: %v", err)
	}
	pipeline, err := toolpipeline.New(policy, host, eventlog.New())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return pipeline.Invoke(context.Background(), toolpipeline.Request{
		CallID:    callID,
		ServerID:  "termcp-core",
		ToolName:  "term",
		Arguments: arguments,
	})
}

func setReview(ctx context.Context, baseURL, sessionID string, enabled bool, timeoutSeconds int) error {
	body, _ := json.Marshal(map[string]any{
		"enabled":         enabled,
		"timeout_seconds": timeoutSeconds,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPatch, baseURL+"/api/sessions/"+sessionID+"/approval", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("set review status %d", response.StatusCode)
	}
	return nil
}

func findApproval(t *testing.T, baseURL, sessionID, requestID string) approvalItem {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/approvals?session_id="+sessionID, nil)
	if err != nil {
		t.Fatalf("build approvals request: %v", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("list approvals: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list approvals status = %d", response.StatusCode)
	}
	var list approvalListResponse
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatalf("decode approvals: %v", err)
	}
	for _, item := range list.Approvals {
		if requestID == "" || item.Request.ID == requestID {
			return item
		}
	}
	t.Fatalf("approval %q not found in %+v", requestID, list)
	return approvalItem{}
}

func waitForApprovalState(t *testing.T, baseURL, sessionID, requestID, want string, timeout time.Duration) approvalItem {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(http.MethodGet, baseURL+"/api/approvals?session_id="+sessionID, nil)
		if err == nil {
			response, requestErr := http.DefaultClient.Do(request)
			if requestErr == nil {
				var list approvalListResponse
				decodeErr := json.NewDecoder(response.Body).Decode(&list)
				_ = response.Body.Close()
				if decodeErr == nil {
					for i := range list.Approvals {
						item := list.Approvals[i]
						if (requestID == "" || item.Request.ID == requestID) && item.Request.State == want {
							return item
						}
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("approval %q never reached state %q", requestID, want)
	return approvalItem{}
}

func decideApproval(t *testing.T, baseURL, requestID, decision string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/approvals/"+requestID+"/"+decision, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build decision request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("%s approval: %v", decision, err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func concurrentDecisions(t *testing.T, baseURL, requestID string) []int {
	t.Helper()
	start := make(chan struct{})
	statuses := make(chan int, 2)
	var group sync.WaitGroup
	for _, decision := range []string{"approve", "reject"} {
		decision := decision
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			statuses <- decideApproval(t, baseURL, requestID, decision)
		}()
	}
	close(start)
	group.Wait()
	close(statuses)
	result := make([]int, 0, 2)
	for status := range statuses {
		result = append(result, status)
	}
	return result
}

func assertRemoteFileMissing(t *testing.T, host *mcpcontrol.Host, sessionID, file string) {
	t.Helper()
	result, err := host.CallTool(context.Background(), "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "term", Arguments: termArguments("read $1 file $2", sessionID, file),
	}})
	if err == nil && result != nil && !result.IsError {
		t.Fatalf("file %q exists before approval", file)
	}
}

func removePOSIXFiles(t *testing.T, host *mcpcontrol.Host, sessionID string, files ...string) {
	t.Helper()
	if len(files) == 0 {
		return
	}
	quoted := make([]string, 0, len(files))
	for _, file := range files {
		quoted = append(quoted, "'"+strings.ReplaceAll(file, "'", `'\"'\"'`)+"'")
	}
	command := "rm -f -- " + strings.Join(quoted, " ")
	result, err := host.CallTool(context.Background(), "termcp-core", mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: "term", Arguments: termArguments("run $1 $2", sessionID+":1", command),
	}})
	if err != nil || result == nil || result.IsError {
		t.Logf("cleanup files failed: result=%+v error=%v", result, err)
	}
}
