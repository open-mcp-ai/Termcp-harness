export type RuntimeState = "new" | "running" | "stopping" | "stopped" | "failed";
export type EventClass = "control" | "diagnostic";
export type CoreIdentityState = "identified" | "unavailable" | "conflict";
export type CoreOwnership = "external" | "managed" | "unverified";

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

export interface Bootstrap {
  endpoint: string;
  health: Health;
  runtime: RuntimeSnapshot;
  events: EventWindow;
  mcp: MCPState;
  core: CoreState;
}

export interface DesktopBridge {
  Bootstrap(): Promise<Bootstrap>;
  Events(after: number, limit: number): Promise<EventWindow>;
  RuntimeEndpoint(): Promise<string>;
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
