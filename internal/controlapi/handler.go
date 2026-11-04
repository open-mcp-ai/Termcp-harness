package controlapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/open-mcp-ai/termcp-harness/internal/agentsession"
	"github.com/open-mcp-ai/termcp-harness/internal/approval"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinstance"
	"github.com/open-mcp-ai/termcp-harness/internal/coreinventory"
	"github.com/open-mcp-ai/termcp-harness/internal/eventlog"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpcontrol"
	"github.com/open-mcp-ai/termcp-harness/internal/mcpdiscovery"
	"github.com/open-mcp-ai/termcp-harness/internal/model"
	"github.com/open-mcp-ai/termcp-harness/internal/recovery"
	"github.com/open-mcp-ai/termcp-harness/internal/runtime"
	"github.com/open-mcp-ai/termcp-harness/internal/toolcall"
	"github.com/open-mcp-ai/termcp-harness/internal/toolpipeline"
)

type Snapshotter interface {
	Snapshot() runtime.Snapshot
}

type healthResponse struct {
	Status  string        `json:"status"`
	State   runtime.State `json:"state"`
	Version string        `json:"version"`
}

type EventReader interface {
	Query(after eventlog.Sequence, limit int) (eventlog.Window, error)
}

type handlerOptions struct {
	events        EventReader
	recovery      RecoveryReader
	mcp           MCPLister
	mcpDiscovery  MCPDiscovery
	core          CoreReader
	coreSessions  CoreSessionLister
	approvals     ApprovalLister
	decider       ApprovalDecider
	refresher     ApprovalExecutionRefresher
	reconciler    ApprovalReconciler
	auth          *ApprovalAuthorizer
	sessions      AgentSessionLister
	sessionRunner AgentSessionRunner
	canceller     AgentSessionCanceller
	transcripts   AgentSessionTranscriptReader
	invoker       agentsession.Invoker
	modelRunner   AgentModelRunner
	modelAdapter  model.Adapter
	modelTools    ModelToolProvider
}

type Option func(*handlerOptions)

func WithEventLog(events EventReader) Option {
	return func(options *handlerOptions) {
		options.events = events
	}
}

type RecoveryReader interface {
	RecoverySnapshot() recovery.Snapshot
}

func WithRecovery(reader RecoveryReader) Option {
	return func(options *handlerOptions) {
		options.recovery = reader
	}
}

type MCPLister interface {
	List() []mcpcontrol.Snapshot
}

func WithMCPRegistry(registry MCPLister) Option {
	return func(options *handlerOptions) {
		options.mcp = registry
	}
}

type MCPDiscovery interface {
	Discover(context.Context) (mcpdiscovery.Snapshot, error)
	Register(context.Context, string) (mcpcontrol.Snapshot, error)
}

func WithMCPDiscovery(discovery MCPDiscovery) Option {
	return func(options *handlerOptions) {
		options.mcpDiscovery = discovery
	}
}

type CoreReader interface {
	Configured() bool
	Snapshot() (coreinstance.Snapshot, bool)
}

func WithCore(manager CoreReader) Option {
	return func(options *handlerOptions) {
		options.core = manager
	}
}

type CoreSessionLister interface {
	List(context.Context) (coreinventory.Snapshot, error)
}

func WithCoreSessions(lister CoreSessionLister) Option {
	return func(options *handlerOptions) {
		options.coreSessions = lister
	}
}

type ApprovalLister interface {
	List() ([]approval.Tracked, error)
}

type ApprovalCapabilityReader interface {
	TrustedCorrelationObserved() bool
}

func WithApprovals(lister ApprovalLister) Option {
	return func(options *handlerOptions) {
		options.approvals = lister
	}
}

type ApprovalDecider interface {
	Decide(context.Context, string, approval.Revision, approval.Decision) (approval.Snapshot, error)
}

func WithApprovalDecider(decider ApprovalDecider) Option {
	return func(options *handlerOptions) {
		options.decider = decider
	}
}

type ApprovalExecutionRefresher interface {
	RefreshExecution(context.Context, string) (approval.ExecutionRefresh, error)
}

func WithApprovalExecutionRefresher(refresher ApprovalExecutionRefresher) Option {
	return func(options *handlerOptions) {
		options.refresher = refresher
	}
}

type ApprovalReconciler interface {
	ReconcileUnknown(context.Context, string) (approval.Reconciliation, error)
}

func WithApprovalReconciler(reconciler ApprovalReconciler) Option {
	return func(options *handlerOptions) {
		options.reconciler = reconciler
	}
}

func WithApprovalAuthorizer(authorizer *ApprovalAuthorizer) Option {
	return func(options *handlerOptions) {
		options.auth = authorizer
	}
}

type AgentSessionLister interface {
	List() []agentsession.Snapshot
}

func WithAgentSessions(lister AgentSessionLister) Option {
	return func(options *handlerOptions) {
		options.sessions = lister
	}
}

type AgentSessionTranscriptReader interface {
	Transcript(string) (agentsession.Transcript, error)
}

func WithAgentSessionTranscripts(reader AgentSessionTranscriptReader) Option {
	return func(options *handlerOptions) {
		options.transcripts = reader
	}
}

type AgentSessionRunner interface {
	AgentSessionLister
	Create(string, string) (*agentsession.Session, error)
	Get(string) (*agentsession.Session, error)
	Run(context.Context, string, agentsession.Invoker, []agentsession.Step) (agentsession.Result, error)
}

type AgentSessionCanceller interface {
	Cancel(context.Context, string, uint64) (agentsession.Snapshot, error)
}

func WithAgentSessionRunner(runner AgentSessionRunner, invoker agentsession.Invoker) Option {
	return func(options *handlerOptions) {
		options.sessionRunner = runner
		options.sessions = runner
		options.invoker = invoker
		if canceller, ok := runner.(AgentSessionCanceller); ok {
			options.canceller = canceller
		}
	}
}

type AgentModelRunner interface {
	RunModel(
		context.Context,
		string,
		model.Adapter,
		agentsession.Invoker,
		[]agentsession.ModelTool,
		string,
		agentsession.ModelRunOptions,
	) (agentsession.ModelResult, error)
	ResumeModel(
		context.Context,
		string,
		model.Adapter,
		agentsession.Invoker,
		[]agentsession.ModelTool,
	) (agentsession.ModelResult, error)
}

type ModelToolProvider interface {
	ModelTools() []agentsession.ModelTool
}

func WithAgentModelLoop(
	runner AgentModelRunner,
	adapter model.Adapter,
	tools ModelToolProvider,
) Option {
	return func(options *handlerOptions) {
		options.modelRunner = runner
		options.modelAdapter = adapter
		options.modelTools = tools
	}
}

type errorResponse struct {
	Error string `json:"error"`
}

type mcpServersResponse struct {
	Servers        []mcpcontrol.Snapshot `json:"servers"`
	Total          int                   `json:"total"`
	Connected      int                   `json:"connected"`
	Ready          int                   `json:"ready"`
	ControlPlaneOn bool                  `json:"control_plane_configured"`
}

type coreResponse struct {
	Configured bool                   `json:"configured"`
	Core       *coreinstance.Snapshot `json:"core,omitempty"`
}

type approvalsResponse struct {
	Configured            bool                           `json:"configured"`
	CorrelationCapability approval.CorrelationCapability `json:"correlation_capability"`
	Total                 int                            `json:"total"`
	Approvals             []approval.Tracked             `json:"approvals"`
}

type agentSessionsResponse struct {
	Configured bool                    `json:"configured"`
	Total      int                     `json:"total"`
	Sessions   []agentsession.Snapshot `json:"sessions"`
}

type approvalDecisionRequest struct {
	Decision         approval.Decision `json:"decision"`
	ExpectedRevision approval.Revision `json:"expected_revision"`
}

type approvalDecisionResponse struct {
	Approval approval.Snapshot `json:"approval"`
	Outcome  string            `json:"outcome"`
	Warning  string            `json:"warning,omitempty"`
}

type approvalRefreshResponse struct {
	Approval approval.Tracked `json:"approval"`
	Changed  bool             `json:"changed"`
	Complete bool             `json:"complete"`
	Warning  string           `json:"warning,omitempty"`
}

type approvalReconcileResponse struct {
	Approval approval.Tracked `json:"approval"`
	Changed  bool             `json:"changed"`
	Resolved bool             `json:"resolved"`
	Warning  string           `json:"warning,omitempty"`
}

type agentSessionCreateRequest struct {
	ID            string `json:"id"`
	CoreSessionID string `json:"core_session_id,omitempty"`
}

type agentSessionRunRequest struct {
	Steps []toolpipeline.Request `json:"steps"`
}

type agentSessionCancelRequest struct {
	ExpectedRevision uint64 `json:"expected_revision"`
}

type agentCallSummary struct {
	CallID      string          `json:"call_id"`
	Status      toolcall.Status `json:"status"`
	ResultBytes int             `json:"result_bytes"`
	IsError     bool            `json:"is_error"`
}

type agentSessionRunResponse struct {
	Session agentsession.Snapshot `json:"session"`
	Calls   []agentCallSummary    `json:"calls"`
}

type agentSessionTranscriptResponse struct {
	Transcript agentsession.Transcript `json:"transcript"`
}

type agentLoopRequest struct {
	Input         string `json:"input"`
	MaxIterations int    `json:"max_iterations,omitempty"`
	MaxToolCalls  int    `json:"max_tool_calls,omitempty"`
}

type agentLoopResponse struct {
	Session agentsession.Snapshot `json:"session"`
	Output  string                `json:"output,omitempty"`
	Calls   []agentCallSummary    `json:"calls"`
}

func NewHandler(source Snapshotter, options ...Option) http.Handler {
	config := handlerOptions{}
	for _, option := range options {
		option(&config)
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		snapshot := source.Snapshot()
		statusCode := http.StatusOK
		status := "ok"
		if snapshot.State != runtime.StateRunning {
			statusCode = http.StatusServiceUnavailable
			status = "unavailable"
		}

		writeJSON(w, statusCode, healthResponse{
			Status:  status,
			State:   snapshot.State,
			Version: snapshot.Version,
		})
	})

	mux.HandleFunc("GET /v1/runtime", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, source.Snapshot())
	})
	mux.HandleFunc("GET /v1/recovery", func(w http.ResponseWriter, _ *http.Request) {
		response := recovery.Snapshot{State: recovery.StateNotConfigured, Consistent: true}
		if config.recovery != nil {
			response = config.recovery.RecoverySnapshot()
		}
		if response.State == "" {
			response.State = recovery.StateNotConfigured
		}
		if response.Stores == nil {
			response.Stores = []recovery.StoreSnapshot{}
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /v1/core", func(w http.ResponseWriter, _ *http.Request) {
		response := coreResponse{}
		if config.core != nil {
			response.Configured = config.core.Configured()
			if snapshot, ok := config.core.Snapshot(); ok {
				response.Core = &snapshot
			}
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /v1/core/sessions", func(w http.ResponseWriter, request *http.Request) {
		if config.coreSessions == nil {
			writeJSON(w, http.StatusOK, coreinventory.Snapshot{Sessions: []coreinventory.Session{}})
			return
		}
		listContext, cancel := context.WithTimeout(request.Context(), 10*time.Second)
		defer cancel()
		snapshot, err := config.coreSessions.List(listContext)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Core session inventory is unavailable"})
			return
		}
		if snapshot.Sessions == nil {
			snapshot.Sessions = []coreinventory.Session{}
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("GET /v1/approvals", func(w http.ResponseWriter, _ *http.Request) {
		response := approvalsResponse{
			CorrelationCapability: approval.CorrelationCapabilityUnavailable,
			Approvals:             []approval.Tracked{},
		}
		if config.approvals != nil {
			response.Configured = true
			response.CorrelationCapability = resolveApprovalCorrelationCapability(config.approvals)
			approvals, err := config.approvals.List()
			if err != nil {
				writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "approval query failed"})
				return
			}
			if approvals != nil {
				response.Approvals = approvals
			}
		}
		response.Total = len(response.Approvals)
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("POST /v1/approvals/{id}/decision", func(w http.ResponseWriter, request *http.Request) {
		if config.decider == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "approval decisions are disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) {
			return
		}
		contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if err != nil || contentType != "application/json" {
			writeJSON(w, http.StatusUnsupportedMediaType, errorResponse{Error: "Content-Type must be application/json"})
			return
		}

		var body approvalDecisionRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid approval decision body"})
			return
		}
		if err := ensureRequestEOF(decoder); err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid approval decision body"})
			return
		}
		if body.ExpectedRevision == 0 || (body.Decision != approval.DecisionApprove && body.Decision != approval.DecisionReject) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "decision and expected_revision are required"})
			return
		}

		decisionContext, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		snapshot, err := config.decider.Decide(
			decisionContext,
			strings.TrimSpace(request.PathValue("id")),
			body.ExpectedRevision,
			body.Decision,
		)
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "approval request not found"})
		case errors.Is(err, approval.ErrApprovalNotPending),
			errors.Is(err, approval.ErrStaleRevision),
			errors.Is(err, approval.ErrDecisionMismatch):
			writeJSON(w, http.StatusConflict, errorResponse{Error: "approval decision conflict"})
		case snapshot.Status == approval.StatusUnknown:
			response := approvalDecisionResponse{Approval: snapshot, Outcome: "unknown"}
			if errors.Is(err, approval.ErrApprovalPersistence) {
				response.Warning = "approval outcome is unknown and local approval history persistence failed"
			}
			writeJSON(w, http.StatusAccepted, response)
		case snapshot.Status.IsTerminal():
			response := approvalDecisionResponse{Approval: snapshot, Outcome: "confirmed"}
			if errors.Is(err, approval.ErrApprovalPersistence) {
				response.Warning = "approval state was confirmed but local approval history persistence failed"
			} else if err != nil {
				response.Warning = "approval state was confirmed but event recording failed"
			}
			writeJSON(w, http.StatusOK, response)
		case err == nil:
			writeJSON(w, http.StatusOK, approvalDecisionResponse{Approval: snapshot, Outcome: "confirmed"})
		default:
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "approval decision failed"})
		}
	})
	mux.HandleFunc("POST /v1/approvals/{id}/execution/refresh", func(w http.ResponseWriter, request *http.Request) {
		if config.refresher == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "approval execution refresh is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body struct{}
		if !decodeJSONBody(w, request, 64<<10, &body) {
			return
		}

		refreshContext, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		refresh, err := config.refresher.RefreshExecution(
			refreshContext,
			strings.TrimSpace(request.PathValue("id")),
		)
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "approval request not found"})
		case errors.Is(err, approval.ErrApprovalNotApproved):
			writeJSON(w, http.StatusConflict, errorResponse{Error: "approval request is not approved"})
		case errors.Is(err, approval.ErrApprovalLookupUnavailable):
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "approval execution lookup is unavailable"})
		case err != nil && refresh.Changed:
			writeJSON(w, http.StatusOK, approvalRefreshResponse{
				Approval: refresh.Approval,
				Changed:  refresh.Changed,
				Complete: refresh.Complete,
				Warning:  "execution state was refreshed but local history recording failed",
			})
		case err != nil:
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "approval execution refresh failed"})
		default:
			writeJSON(w, http.StatusOK, approvalRefreshResponse{
				Approval: refresh.Approval,
				Changed:  refresh.Changed,
				Complete: refresh.Complete,
			})
		}
	})
	mux.HandleFunc("POST /v1/approvals/{id}/reconcile", func(w http.ResponseWriter, request *http.Request) {
		if config.reconciler == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "approval reconciliation is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body struct{}
		if !decodeJSONBody(w, request, 64<<10, &body) {
			return
		}

		reconcileContext, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		reconciliation, err := config.reconciler.ReconcileUnknown(
			reconcileContext,
			strings.TrimSpace(request.PathValue("id")),
		)
		switch {
		case errors.Is(err, approval.ErrApprovalNotFound):
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "approval request not found"})
		case errors.Is(err, approval.ErrApprovalNotUnknown):
			writeJSON(w, http.StatusConflict, errorResponse{Error: "approval request is not unknown"})
		case errors.Is(err, approval.ErrApprovalLookupUnavailable):
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "approval lookup is unavailable"})
		case errors.Is(err, approval.ErrDecisionMismatch):
			writeJSON(w, http.StatusConflict, errorResponse{Error: "approval reconciliation conflict"})
		case err != nil && (reconciliation.Resolved || errors.Is(err, approval.ErrApprovalPersistence)):
			writeJSON(w, http.StatusOK, approvalReconcileResponse{
				Approval: reconciliation.Approval,
				Changed:  reconciliation.Changed,
				Resolved: reconciliation.Resolved,
				Warning:  "approval reconciliation completed but local recording failed",
			})
		case err != nil:
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "approval reconciliation failed"})
		default:
			writeJSON(w, http.StatusOK, approvalReconcileResponse{
				Approval: reconciliation.Approval,
				Changed:  reconciliation.Changed,
				Resolved: reconciliation.Resolved,
			})
		}
	})
	mux.HandleFunc("GET /v1/agent/sessions", func(w http.ResponseWriter, _ *http.Request) {
		response := agentSessionsResponse{Configured: config.sessions != nil, Sessions: []agentsession.Snapshot{}}
		if config.sessions != nil {
			if sessions := config.sessions.List(); sessions != nil {
				response.Sessions = sessions
			}
		}
		response.Total = len(response.Sessions)
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /v1/agent/sessions/{id}", func(w http.ResponseWriter, request *http.Request) {
		if config.sessionRunner == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Agent Session control is unavailable"})
			return
		}
		session, err := config.sessionRunner.Get(strings.TrimSpace(request.PathValue("id")))
		if err != nil {
			if errors.Is(err, agentsession.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "Agent Session not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Agent Session query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"session": session.Snapshot()})
	})
	mux.HandleFunc("GET /v1/agent/sessions/{id}/transcript", func(w http.ResponseWriter, request *http.Request) {
		if config.transcripts == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Agent Session transcript is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) {
			return
		}
		transcript, err := config.transcripts.Transcript(strings.TrimSpace(request.PathValue("id")))
		if err != nil {
			if errors.Is(err, agentsession.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "Agent Session not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "Agent Session transcript query failed"})
			return
		}
		writeJSON(w, http.StatusOK, agentSessionTranscriptResponse{Transcript: transcript})
	})
	mux.HandleFunc("POST /v1/agent/sessions", func(w http.ResponseWriter, request *http.Request) {
		if config.sessionRunner == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Agent Session creation is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body agentSessionCreateRequest
		if !decodeJSONBody(w, request, 64<<10, &body) {
			return
		}
		session, err := config.sessionRunner.Create(body.ID, body.CoreSessionID)
		if err != nil {
			if errors.Is(err, agentsession.ErrSessionExists) {
				writeJSON(w, http.StatusConflict, errorResponse{Error: "Agent Session already exists"})
				return
			}
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid Agent Session"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"session": session.Snapshot()})
	})
	mux.HandleFunc("POST /v1/agent/sessions/{id}/cancel", func(w http.ResponseWriter, request *http.Request) {
		if config.canceller == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Agent Session cancellation is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body agentSessionCancelRequest
		if !decodeJSONBody(w, request, 64<<10, &body) {
			return
		}
		if body.ExpectedRevision == 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "expected_revision is required"})
			return
		}

		cancelContext, cancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer cancel()
		snapshot, err := config.canceller.Cancel(
			cancelContext,
			strings.TrimSpace(request.PathValue("id")),
			body.ExpectedRevision,
		)
		switch {
		case errors.Is(err, agentsession.ErrSessionNotFound):
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "Agent Session not found"})
		case errors.Is(err, agentsession.ErrCancellationConflict):
			writeJSON(w, http.StatusConflict, errorResponse{Error: "Agent Session cancellation conflict"})
		case err != nil:
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "Agent Session cancellation failed"})
		default:
			status := http.StatusOK
			if snapshot.State == agentsession.StateCancelling {
				status = http.StatusAccepted
			}
			writeJSON(w, status, map[string]any{"session": snapshot})
		}
	})
	mux.HandleFunc("POST /v1/agent/sessions/{id}/run", func(w http.ResponseWriter, request *http.Request) {
		if config.sessionRunner == nil || config.invoker == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "Agent Session execution is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body agentSessionRunRequest
		if !decodeJSONBody(w, request, 1<<20, &body) {
			return
		}
		if len(body.Steps) == 0 || len(body.Steps) > 64 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "steps must contain between 1 and 64 entries"})
			return
		}

		runContext, cancel := context.WithTimeout(request.Context(), 2*time.Minute)
		defer cancel()
		result, err := config.sessionRunner.Run(
			runContext,
			strings.TrimSpace(request.PathValue("id")),
			config.invoker,
			body.Steps,
		)
		if result.Snapshot.ID == "" {
			status := http.StatusBadGateway
			if errors.Is(err, agentsession.ErrSessionNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, agentsession.ErrInvokerRequired) ||
				errors.Is(err, agentsession.ErrNoSteps) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, errorResponse{Error: "Agent Session execution failed"})
			return
		}

		calls := summarizeAgentCalls(result.Outcomes)
		status := http.StatusOK
		if result.Snapshot.State == agentsession.StateWaitingApproval ||
			result.Snapshot.State == agentsession.StateWaitingExecution {
			status = http.StatusAccepted
		}
		writeJSON(w, status, agentSessionRunResponse{Session: result.Snapshot, Calls: calls})
	})
	mux.HandleFunc("POST /v1/agent/sessions/{id}/loop", func(w http.ResponseWriter, request *http.Request) {
		if config.modelRunner == nil || config.modelAdapter == nil || config.modelTools == nil ||
			config.invoker == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "model Agent Loop is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body agentLoopRequest
		if !decodeJSONBody(w, request, 256<<10, &body) {
			return
		}
		body.Input = strings.TrimSpace(body.Input)
		if body.Input == "" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "input is required"})
			return
		}
		tools := config.modelTools.ModelTools()
		if len(tools) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "model tools are not available"})
			return
		}

		loopContext, cancel := context.WithTimeout(request.Context(), 5*time.Minute)
		defer cancel()
		result, err := config.modelRunner.RunModel(
			loopContext,
			strings.TrimSpace(request.PathValue("id")),
			config.modelAdapter,
			config.invoker,
			tools,
			body.Input,
			agentsession.ModelRunOptions{
				MaxIterations: body.MaxIterations,
				MaxToolCalls:  body.MaxToolCalls,
			},
		)
		if result.Snapshot.ID == "" {
			status := http.StatusBadGateway
			if errors.Is(err, agentsession.ErrSessionNotFound) {
				status = http.StatusNotFound
			} else if errors.Is(err, agentsession.ErrModelRequired) ||
				errors.Is(err, agentsession.ErrModelTools) ||
				errors.Is(err, agentsession.ErrModelInput) ||
				errors.Is(err, agentsession.ErrInvokerRequired) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, errorResponse{Error: "model Agent Loop failed"})
			return
		}

		status := http.StatusOK
		if result.Snapshot.State == agentsession.StateWaitingApproval ||
			result.Snapshot.State == agentsession.StateWaitingExecution {
			status = http.StatusAccepted
		}
		response := agentLoopResponse{
			Session: result.Snapshot,
			Output:  result.Output,
			Calls:   summarizeAgentCalls(result.Outcomes),
		}
		if err != nil && result.Snapshot.State == agentsession.StateRunning {
			status = http.StatusBadGateway
		}
		writeJSON(w, status, response)
	})
	mux.HandleFunc("POST /v1/agent/sessions/{id}/loop/resume", func(w http.ResponseWriter, request *http.Request) {
		if config.modelRunner == nil || config.modelAdapter == nil || config.modelTools == nil ||
			config.invoker == nil || config.auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "model Agent Loop recovery is disabled"})
			return
		}
		if !authorizeMutation(w, request, config.auth) || !requireJSONContentType(w, request) {
			return
		}
		var body struct{}
		if !decodeJSONBody(w, request, 64<<10, &body) {
			return
		}
		tools := config.modelTools.ModelTools()
		if len(tools) == 0 {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "model tools are not available"})
			return
		}

		resumeContext, cancel := context.WithTimeout(request.Context(), 5*time.Minute)
		defer cancel()
		result, err := config.modelRunner.ResumeModel(
			resumeContext,
			strings.TrimSpace(request.PathValue("id")),
			config.modelAdapter,
			config.invoker,
			tools,
		)
		if result.Snapshot.ID == "" {
			status := http.StatusBadGateway
			switch {
			case errors.Is(err, agentsession.ErrSessionNotFound):
				status = http.StatusNotFound
			case errors.Is(err, agentsession.ErrModelRecoveryUnavailable),
				errors.Is(err, agentsession.ErrModelRecoveryBlocked),
				errors.Is(err, agentsession.ErrModelRecoveryMismatch):
				status = http.StatusConflict
			case errors.Is(err, agentsession.ErrModelRequired),
				errors.Is(err, agentsession.ErrModelTools),
				errors.Is(err, agentsession.ErrInvokerRequired):
				status = http.StatusBadRequest
			}
			writeJSON(w, status, errorResponse{Error: "model Agent Loop recovery failed"})
			return
		}

		status := http.StatusOK
		if result.Snapshot.State == agentsession.StateWaitingApproval ||
			result.Snapshot.State == agentsession.StateWaitingExecution {
			status = http.StatusAccepted
		}
		response := agentLoopResponse{
			Session: result.Snapshot,
			Output:  result.Output,
			Calls:   summarizeAgentCalls(result.Outcomes),
		}
		if err != nil && result.Snapshot.State == agentsession.StateRunning {
			status = http.StatusBadGateway
		}
		if errors.Is(err, agentsession.ErrModelRecoveryBlocked) ||
			errors.Is(err, agentsession.ErrModelRecoveryMismatch) ||
			errors.Is(err, agentsession.ErrModelRecoveryUnavailable) {
			status = http.StatusConflict
			writeJSON(w, status, errorResponse{Error: "model Agent Loop recovery failed"})
			return
		}
		writeJSON(w, status, response)
	})
	mux.HandleFunc("GET /v1/events", func(w http.ResponseWriter, request *http.Request) {
		if config.events == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "event log is not configured"})
			return
		}

		after, err := parseAfter(request.URL.Query().Get("after"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid after cursor"})
			return
		}
		limit, err := parseLimit(request.URL.Query().Get("limit"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid limit"})
			return
		}
		window, err := config.events.Query(after, limit)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "event query failed"})
			return
		}
		writeJSON(w, http.StatusOK, window)
	})
	mux.HandleFunc("GET /v1/events/stream", eventStreamHandler(config.events))
	mux.HandleFunc("GET /v1/mcp/servers", func(w http.ResponseWriter, _ *http.Request) {
		response := mcpServersResponse{Servers: []mcpcontrol.Snapshot{}, ControlPlaneOn: config.mcp != nil}
		if config.mcp != nil {
			response.Servers = config.mcp.List()
			if response.Servers == nil {
				response.Servers = []mcpcontrol.Snapshot{}
			}
		}
		for _, server := range response.Servers {
			response.Total++
			if server.Connection == mcpcontrol.ConnectionConnected {
				response.Connected++
			}
			if server.Ready {
				response.Ready++
			}
		}
		writeJSON(w, http.StatusOK, response)
	})
	mux.HandleFunc("GET /v1/mcp/discovery", func(w http.ResponseWriter, request *http.Request) {
		if config.mcpDiscovery == nil {
			writeJSON(w, http.StatusOK, mcpdiscovery.Snapshot{
				Sources: []mcpdiscovery.Source{},
				Servers: []mcpdiscovery.Server{},
			})
			return
		}
		snapshot, err := config.mcpDiscovery.Discover(request.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "MCP discovery failed"})
			return
		}
		if snapshot.Sources == nil {
			snapshot.Sources = []mcpdiscovery.Source{}
		}
		if snapshot.Servers == nil {
			snapshot.Servers = []mcpdiscovery.Server{}
		}
		writeJSON(w, http.StatusOK, snapshot)
	})
	mux.HandleFunc("POST /v1/mcp/discovery/{id}/register", func(w http.ResponseWriter, request *http.Request) {
		if !authorizeMutation(w, request, config.auth) {
			return
		}
		if config.mcpDiscovery == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "MCP discovery is not configured"})
			return
		}
		server, err := config.mcpDiscovery.Register(request.Context(), request.PathValue("id"))
		switch {
		case errors.Is(err, mcpdiscovery.ErrUnknownServer):
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "discovered MCP server not found"})
		case errors.Is(err, mcpdiscovery.ErrNotLoadable):
			writeJSON(w, http.StatusConflict, errorResponse{Error: "discovered MCP server cannot be loaded"})
		case err != nil:
			writeJSON(w, http.StatusBadGateway, errorResponse{Error: "MCP registration failed"})
		default:
			writeJSON(w, http.StatusCreated, server)
		}
	})

	return mux
}

func summarizeAgentCalls(outcomes []toolpipeline.Outcome) []agentCallSummary {
	calls := make([]agentCallSummary, 0, len(outcomes))
	for _, outcome := range outcomes {
		resultBytes := 0
		isError := false
		if outcome.Result != nil {
			if encoded, encodeErr := json.Marshal(outcome.Result); encodeErr == nil {
				resultBytes = len(encoded)
			}
			isError = outcome.Result.IsError
		}
		calls = append(calls, agentCallSummary{
			CallID:      outcome.Call.ID,
			Status:      outcome.Call.Status,
			ResultBytes: resultBytes,
			IsError:     isError,
		})
	}
	return calls
}

func resolveApprovalCorrelationCapability(
	lister ApprovalLister,
) approval.CorrelationCapability {
	if reader, ok := lister.(ApprovalCapabilityReader); ok && reader.TrustedCorrelationObserved() {
		return approval.CorrelationCapabilityTrusted
	}
	return approval.CorrelationCapabilityUnverified
}

func parseAfter(raw string) (eventlog.Sequence, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, err
	}
	return eventlog.Sequence(value), nil
}

func ensureRequestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func authorizeMutation(w http.ResponseWriter, request *http.Request, authorizer *ApprovalAuthorizer) bool {
	if authorizer == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "mutation control is disabled"})
		return false
	}
	if err := authorizer.Authorize(request); err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrApprovalOriginDenied) {
			status = http.StatusForbidden
		}
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		writeJSON(w, status, errorResponse{Error: err.Error()})
		return false
	}
	return true
}

func requireJSONContentType(w http.ResponseWriter, request *http.Request) bool {
	contentType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, errorResponse{Error: "Content-Type must be application/json"})
		return false
	}
	return true
}

func decodeJSONBody(w http.ResponseWriter, request *http.Request, maxBytes int64, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, request.Body, maxBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return false
	}
	if err := ensureRequestEOF(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return false
	}
	return true
}

func parseLimit(raw string) (int, error) {
	if raw == "" {
		return 100, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 1000 {
		return 0, errors.New("limit must be between 1 and 1000")
	}
	return value, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(value)
}
