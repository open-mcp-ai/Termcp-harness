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

export interface MCPServer {
  id: string;
  name: string;
  endpoint: string;
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
  };
  outcome: "confirmed" | "unknown";
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
  started_at?: string;
  finished_at?: string;
}

export interface AgentSessionsState {
  configured: boolean;
  total: number;
  sessions: AgentSessionSnapshot[];
}

export interface Bootstrap {
  endpoint: string;
  health: Health;
  runtime: RuntimeSnapshot;
  events: EventWindow;
  mcp: MCPState;
  core: CoreState;
  approvals: ApprovalsState;
  agent_sessions: AgentSessionsState;
}

export interface DesktopBridge {
  Bootstrap(): Promise<Bootstrap>;
  Events(after: number, limit: number): Promise<EventWindow>;
  RuntimeEndpoint(): Promise<string>;
  ApprovalControlAvailable(): Promise<boolean>;
  DecideApproval(id: string, revision: number, decision: "approve" | "reject"): Promise<ApprovalDecisionResult>;
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
