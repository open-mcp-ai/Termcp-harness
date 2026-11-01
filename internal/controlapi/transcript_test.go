package controlapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
)

func TestAgentSessionTranscriptRequiresAuthentication(t *testing.T) {
	reader := &fakeTranscriptReader{transcript: agentsession.Transcript{
		Session:  agentsession.Snapshot{ID: "session-1", State: agentsession.StateCompleted},
		Messages: []model.Message{{Role: model.RoleUser, Content: "hello"}},
		Output:   "answer",
	}}
	handler := NewHandler(
		runningRuntime(t),
		WithAgentSessionTranscripts(reader),
		WithApprovalAuthorizer(newTestApprovalAuthorizer(t)),
	)
	request := httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/session-1/transcript", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/session-1/transcript", nil)
	request.Header.Set("Authorization", "Bearer "+testApprovalToken)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body agentSessionTranscriptResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Transcript.Output != "answer" || len(body.Transcript.Messages) != 1 ||
		body.Transcript.Session.ID != "session-1" {
		t.Fatalf("transcript = %+v", body.Transcript)
	}
}

func TestAgentSessionTranscriptReportsDisabled(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(runningRuntime(t)).ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/v1/agent/sessions/session-1/transcript", nil),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}

type fakeTranscriptReader struct {
	transcript agentsession.Transcript
}

func (f *fakeTranscriptReader) Transcript(string) (agentsession.Transcript, error) {
	return f.transcript, nil
}
