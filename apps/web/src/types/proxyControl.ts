export type NodeHealth =
  | 'healthy'
  | 'degraded'
  | 'checking'
  | 'failed'
  | 'unknown'
  | 'switching'
  | 'rollback';

export type StageStatus = 'passed' | 'failed' | 'running' | 'pending' | 'skipped';

export interface HealthCheckStage {
  stage: string;
  name: string;
  status: StageStatus;
  http_status?: number;
  latency_ms?: number;
  started_at?: string;
  completed_at?: string;
  error_code?: string;
  passed: boolean;
}

export interface ProxyNode {
  id: string;
  name: string;
  country: string;
  region: string;
  health: NodeHealth;
  latency_ms: number;
  selected: boolean;
  preferred_level?: number;
  consecutive_failures: number;
  consecutive_successes: number;
  switchable: boolean;
  last_checked_at?: string;
  selected_at?: string;
  reason?: string;
}

export interface EgressStatus {
  health: NodeHealth;
  ip: string | null;
  ip_display: string;
  country: string;
  checked_at: string;
}

export interface SelectionStatus {
  mode: 'manual' | 'auto';
  dry_run: boolean;
  auto_switch: boolean;
  last_reason?: string;
  last_result?: string;
}

export interface ProxyStatus {
  enabled: boolean;
  read_only: boolean;
  controller_available: boolean;
  current_node?: ProxyNode | null;
  egress?: EgressStatus;
  selection?: SelectionStatus;
  check_stages: HealthCheckStage[];
}

export interface NodeListResponse {
  items: ProxyNode[];
  total: number;
  healthy: number;
  degraded: number;
  failed: number;
  last_refresh_at: string | null;
}

export interface NodeFilterParams {
  country?: string;
  health?: string;
  selected?: boolean;
  preferred?: boolean;
  sort?: 'priority' | 'health' | 'latency' | 'last_checked';
  page?: number;
  page_size?: number;
}

export interface ProxyPolicy {
  auto_switch: boolean;
  dry_run: boolean;
  allowed_countries: string[];
  preferred_nodes: string[];
  probe_concurrency: number;
  probe_timeout_seconds: number;
  health_check_rounds: number;
  failure_threshold: number;
  success_threshold: number;
  cooldown_seconds: number;
  rollback_on_failure: boolean;
  allow_auto_rollback: boolean;
}

export interface HealthCheckRequest {
  dry_run: boolean;
  node_id?: string;
}

export interface HealthCheckResult {
  node_id: string;
  healthy: boolean;
  latency_ms: number;
  stages: HealthCheckStage[];
  checked_at: string;
  error_code?: string;
}

export interface SelectRequest {
  node_id: string;
  reason?: string;
  confirm: boolean;
  dry_run?: boolean;
}

export interface SelectResult {
  success: boolean;
  would_switch: boolean;
  rolled_back: boolean;
  current_node?: ProxyNode;
  recommended_node?: ProxyNode;
  candidates?: ProxyNode[];
  reason?: string;
  checks?: HealthCheckStage[];
  rollback_id?: string;
}

export interface RollbackRequest {
  rollback_id: string;
  confirm: boolean;
}

export interface ProxyHistoryItem {
  id: string;
  timestamp: string;
  operation:
    | 'health_check'
    | 'dry_run_select'
    | 'manual_select'
    | 'automatic_select'
    | 'rollback'
    | 'refresh_nodes';
  source_node_id?: string;
  source_node_name?: string;
  target_node_id?: string;
  target_node_name?: string;
  trigger_reason?: string;
  dry_run: boolean;
  health_check_passed: boolean;
  final_result: 'success' | 'failed' | 'rolled_back' | 'kept_current';
  rolled_back: boolean;
  operator: string;
  error_code?: string;
}

export interface ProxyHistoryPage {
  items: ProxyHistoryItem[];
  total: number;
}
