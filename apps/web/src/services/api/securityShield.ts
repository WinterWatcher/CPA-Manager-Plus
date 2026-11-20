import { apiClient, createScopedApiRequestConfig, type ApiClientRequestScope } from './client';

export interface SecurityShieldStatus {
  healthy: boolean;
  gateway_running: boolean;
  waf_risk_level: 'safe' | 'warning' | 'critical';
  canary_status: number;
  canary_healthy: boolean;
  canary_reason: string;
  last_canary_time: string;
  canary_latency_sec: number;
  account_protection: {
    email: string;
    status: string;
    policy: string;
    sanitization_engine: string;
  };
  rule_stats: {
    strip_rules: number;
    custom_rules: number;
    audit_terms: number;
  };
  active_version: string;
}

export interface RuleSection {
  name: string;
  pattern: string;
  replacement: string;
}

export interface SecurityShieldRules {
  version: string;
  description: string;
  harness_strip_sections: RuleSection[];
  custom_replacements: RuleSection[];
  leak_audit_terms: string[];
}

export interface CanaryTestResult {
  status: string;
  healthy: boolean;
  data?: {
    time: string;
    healthy: boolean;
    reason: string;
    status: number;
    seconds: number;
    canary_healthy: boolean;
    canary_reason: string;
    canary_status: number;
  };
  error?: string;
  raw?: string;
}

export interface DiagnoseResult {
  status: string;
  success: boolean;
  output: string;
}

export const securityShieldApi = {
  async getStatus(requestScope?: ApiClientRequestScope): Promise<SecurityShieldStatus> {
    const data = await apiClient.get<SecurityShieldStatus>(
      '/security-shield/status',
      requestScope ? createScopedApiRequestConfig(requestScope) : undefined
    );
    return data as unknown as SecurityShieldStatus;
  },

  async getRules(requestScope?: ApiClientRequestScope): Promise<SecurityShieldRules> {
    const data = await apiClient.get<SecurityShieldRules>(
      '/security-shield/rules',
      requestScope ? createScopedApiRequestConfig(requestScope) : undefined
    );
    return data as unknown as SecurityShieldRules;
  },

  async updateRules(rules: SecurityShieldRules, requestScope?: ApiClientRequestScope): Promise<{ status: string; message: string }> {
    const data = await apiClient.post<{ status: string; message: string }>(
      '/security-shield/rules',
      rules,
      requestScope ? createScopedApiRequestConfig(requestScope) : undefined
    );
    return data as unknown as { status: string; message: string };
  },

  async triggerCanary(requestScope?: ApiClientRequestScope): Promise<CanaryTestResult> {
    const data = await apiClient.post<CanaryTestResult>(
      '/security-shield/canary',
      {},
      requestScope ? createScopedApiRequestConfig(requestScope) : undefined
    );
    return data as unknown as CanaryTestResult;
  },

  async triggerDiagnose(requestScope?: ApiClientRequestScope): Promise<DiagnoseResult> {
    const data = await apiClient.post<DiagnoseResult>(
      '/security-shield/diagnose',
      {},
      requestScope ? createScopedApiRequestConfig(requestScope) : undefined
    );
    return data as unknown as DiagnoseResult;
  },
};
