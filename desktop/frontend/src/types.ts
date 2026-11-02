export type RuntimeState = "new" | "running" | "stopping" | "stopped" | "failed";
export type EventClass = "control" | "diagnostic";
export type CoreIdentityState = "identified" | "unavailable" | "conflict";
export type CoreOwnership = "external" | "managed" | "unverified";
export type ApprovalCorrelation = "unknown" | "trusted" | "conflict";
export type ApprovalCorrelationCapability =
  | "unavailable"
  | "unverified"
  | "trusted";
export type ApprovalStatus =
  | "pending"
  | "submitting"
  | "approved"
  | "rejected"
  | "expired"
  | "cancelled"
  | "unknown";
export type ApprovalDecision = "none" | "approve" | "reject";
export type ApprovalExecutionState =
  | "running"
  | "submitted"
  | "succeeded"
  | "failed"
  | "unknown";
export type ToolCallStatus =
  | "proposed"
  | "blocked"
  | "approval_pending"
  | "ready"
  | "dispatched"
  | "accepted"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "expired"
  | "unknown";
export type ToolCompletionRequirement = "accepted" | "completed";
export type AgentSessionState =
  | "new"
  | "running"
  | "cancelling"
  | "waiting_approval"
  | "waiting_execution"
  | "completed"
  | "blocked"
  | "failed"
  | "cancelled"
  | "expired"
  | "unknown";

export interface Health {
  status: string;
  state: RuntimeState;
  version: string;
}

export interface RuntimeSnapshot {
  instance_id: string;
  version: string;
  state: RuntimeState;
  started_at?: string;
  stopped_at?: string;
}

export interface HarnessEvent {
  event_id: string;
  sequence: number;
  occurred_at: string;
  type: string;
  class: EventClass;
  subject_id?: string;
  correlation_id?: string;
  data?: unknown;
}

export interface EventWindow {
  events: HarnessEvent[];
  latest_sequence: number;
  dropped_diagnostics: number;
  diagnostic_dropped_through?: number;
  truncated: boolean;
  has_more: boolean;
  control_overflow: boolean;
}

export type EventStreamState =
  | "connecting"
  | "connected"
  | "reconnecting"
  | "lagged"
  | "stopped";

export interface EventStreamSnapshot {
  state: EventStreamState;
  cursor: number;
  latest_sequence: number;
  dropped_diagnostics: number;
  diagnostic_dropped_through?: number;
  truncated: boolean;
  control_overflow: boolean;
  lagged: boolean;
  last_error?: string;
  revision: number;
}

export interface EventStreamPollResult extends EventStreamSnapshot {
  events: HarnessEvent[];
}

export interface MCPServer {
  id: string;
  name: string;
  endpoint: string;
  transport: "streamable-http" | "sse" | "stdio";
  management: string;
  registration: string;
  connection: string;
  capability_state: string;
  enablement: string;
  ready: boolean;
  failure_reason?: string;
  capabilities?: {
    protocol_version?: string;
    server_name?: string;
    server_version?: string;
    tools_supported?: boolean;
    tool_count?: number;
  };
  revision: number;
}

export interface MCPState {
  servers: MCPServer[];
  total: number;
  connected: number;
  ready: number;
  control_plane_configured: boolean;
}

export interface CoreIdentity {
  endpoint: string;
  state: CoreIdentityState;
  ownership: CoreOwnership;
  ownership_verified: boolean;
  version?: string;
  daemon: boolean;
  pid?: number;
  started_at?: string;
  log_path?: string;
  idle_timeout_ms?: number;
  instance_key?: string;
  observed_at: string;
  revision: number;
  changed: boolean;
  previous_instance_key?: string;
  last_error?: string;
}

export interface CoreState {
  configured: boolean;
  core?: CoreIdentity;
}

export interface TrackedApproval {
  request: {
    id: string;
    call_id: string;
    status: ApprovalStatus;
    decision: ApprovalDecision;
    revision: number;
    execution?: ApprovalExecution;
    recovered?: boolean;
    recovery_reason?: string;
  };
  reference: {
    correlation: ApprovalCorrelation;
    approval_id?: string;
    session_id?: string;
    expires_at?: string;
    summary?: string;
  };
}

export interface ApprovalExecution {
  state: ApprovalExecutionState;
  started_at: string;
  finished_at?: string;
  error?: string;
}

export interface ApprovalsState {
  configured: boolean;
  correlation_capability: ApprovalCorrelationCapability;
  total: number;
  approvals: TrackedApproval[];
}

export interface ApprovalDecisionResult {
  approval: {
    id: string;
    call_id: string;
    status: ApprovalStatus;
    decision: ApprovalDecision;
    revision: number;
    execution?: ApprovalExecution;
    recovered?: boolean;
    recovery_reason?: string;
  };
  outcome: "confirmed" | "unknown";
  warning?: string;
}

export interface ApprovalRefreshResult {
  approval: TrackedApproval;
  changed: boolean;
  complete: boolean;
  warning?: string;
}

export interface ApprovalReconciliationResult {
  approval: TrackedApproval;
  changed: boolean;
  resolved: boolean;
  warning?: string;
}

export interface AgentSessionSnapshot {
  id: string;
  core_session_id?: string;
  state: AgentSessionState;
  revision: number;
  current_call_id?: string;
  current_approval_id?: string;
  current_approval_status?: ApprovalStatus;
  current_tool_status?: ToolCallStatus;
  current_execution?: ApprovalExecution;
  current_completion?: ToolCompletionRequirement;
  model_turns?: number;
  model_tool_calls?: number;
  transcript_messages?: number;
  output_bytes?: number;
  recovered?: boolean;
  recovery_reason?: string;
  model_loop_recovery?: ModelLoopRecoverySummary;
  cancellation?: AgentCancellation;
  started_at?: string;
  finished_at?: string;
}

export type CancellationPhase =
  | "before_dispatch"
  | "model_request"
  | "tool_dispatch"
  | "waiting_approval"
  | "waiting_execution";

export interface AgentCancellation {
  requested_at: string;
  phase: CancellationPhase;
  remote_outcome_unknown?: boolean;
  approval_pending?: boolean;
}

export type ModelLoopRecoveryStage =
  | "model_requesting"
  | "tools_ready"
  | "tool_dispatching"
  | "tool_observed"
  | "waiting_approval"
  | "waiting_execution"
  | "checkpoint_corrupted";

export interface ModelLoopRecoverySummary {
  stage: ModelLoopRecoveryStage;
  safe_boundary: boolean;
  blocked_reason?: string;
  iteration: number;
  tool_calls: number;
  pending_tool_calls: number;
  current_call_id?: string;
  checkpoint_revision: number;
  updated_at: string;
}

export interface AgentSessionsState {
  configured: boolean;
  total: number;
  sessions: AgentSessionSnapshot[];
}

export interface AgentCallSummary {
  call_id: string;
  status: ToolCallStatus;
  result_bytes: number;
  is_error: boolean;
}

export interface ModelLoopLaunchResult {
  agent_session_id: string;
  session: AgentSessionSnapshot;
  output?: string;
  calls: AgentCallSummary[];
}

export interface ModelToolCall {
  id: string;
  name: string;
  arguments: unknown;
}

export interface ModelMessage {
  role: "system" | "user" | "assistant" | "tool";
  content?: string;
  tool_calls?: ModelToolCall[];
  tool_call_id?: string;
  name?: string;
}

export interface AgentSessionTranscript {
  session: AgentSessionSnapshot;
  messages: ModelMessage[];
  output?: string;
}

export type TerminalState = "connecting" | "reconnecting" | "connected" | "ended" | "unknown";

export interface TerminalShell {
  id: string;
  index: number;
  name?: string;
  status: string;
  mode?: string;
}

export interface TerminalShellsResult {
  agent_session_id: string;
  core_session_id: string;
  shells: TerminalShell[];
}

export interface TerminalOpenResult {
  id: string;
  agent_session_id: string;
  core_session_id: string;
  shell_id: string;
  state: TerminalState;
  history?: string;
  history_bytes: number;
  history_truncated: boolean;
}

export interface TerminalSnapshot {
  id: string;
  agent_session_id: string;
  core_session_id: string;
  shell_id: string;
  state: TerminalState;
  activity_source?: "api" | "ai";
  last_error?: string;
  dropped_output_bytes: number;
  reconnect_attempts?: number;
  revision: number;
}

export interface TerminalPollResult extends TerminalSnapshot {
  output?: string;
}

export type RecoveryState = "not_configured" | "unverified" | "complete" | "partial";
export type RecoveryStoreState = "not_configured" | "missing" | "present" | "unreadable";

export interface RecoveryStoreSnapshot {
  name: string;
  configured: boolean;
  state: RecoveryStoreState;
  exists: boolean;
  size?: number;
  fingerprint?: string;
  error?: string;
}

export interface RecoverySnapshot {
  manifest_configured: boolean;
  manifest_present: boolean;
  manifest_version?: number;
  recorded_at?: string;
  state: RecoveryState;
  consistent: boolean;
  stores: RecoveryStoreSnapshot[];
  warning?: string;
}

export interface Bootstrap {
  endpoint: string;
  health: Health;
  runtime: RuntimeSnapshot;
  recovery: RecoverySnapshot;
  events: EventWindow;
  mcp: MCPState;
  core: CoreState;
  approvals: ApprovalsState;
  agent_sessions: AgentSessionsState;
}

export interface DesktopBridge {
  Bootstrap(): Promise<Bootstrap>;
  Events(after: number, limit: number): Promise<EventWindow>;
  OpenEventStream(after: number): Promise<EventStreamSnapshot>;
  PollEventStream(): Promise<EventStreamPollResult>;
  CloseEventStream(): Promise<void>;
  RuntimeEndpoint(): Promise<string>;
  ApprovalControlAvailable(): Promise<boolean>;
  DecideApproval(id: string, revision: number, decision: "approve" | "reject"): Promise<ApprovalDecisionResult>;
  RefreshApprovalExecution(id: string): Promise<ApprovalRefreshResult>;
  ReconcileApproval(id: string): Promise<ApprovalReconciliationResult>;
  StartModelLoop(coreSessionId: string, input: string, maxIterations: number, maxToolCalls: number): Promise<ModelLoopLaunchResult>;
  ResumeModelLoop(agentSessionId: string): Promise<ModelLoopLaunchResult>;
  CancelAgentSession(id: string, expectedRevision: number): Promise<{ session: AgentSessionSnapshot }>;
  AgentSessionTranscript(id: string): Promise<{ transcript: AgentSessionTranscript }>;
  TerminalShells(agentSessionId: string): Promise<TerminalShellsResult>;
  TerminalOpen(agentSessionId: string, shellId: string, rows: number, cols: number): Promise<TerminalOpenResult>;
  TerminalPoll(id: string): Promise<TerminalPollResult>;
  TerminalInput(id: string, data: string): Promise<void>;
  TerminalResize(id: string, rows: number, cols: number): Promise<void>;
  TerminalClose(id: string): Promise<void>;
  WindowMinimise(): Promise<void>;
  WindowToggleMaximise(): Promise<void>;
  WindowClose(): Promise<void>;
}

declare global {
  interface Window {
    go?: {
      main?: {
        App?: DesktopBridge;
      };
    };
  }
}
