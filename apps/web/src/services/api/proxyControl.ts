import { apiClient } from './client';
import type {
  HealthCheckRequest,
  HealthCheckResult,
  NodeFilterParams,
  NodeListResponse,
  ProxyHistoryPage,
  ProxyPolicy,
  ProxyStatus,
  RollbackRequest,
  SelectRequest,
  SelectResult,
} from '@/types/proxyControl';

export const proxyControlApi = {
  getStatus: async (): Promise<ProxyStatus> => {
    return apiClient.get('/v0/management/proxy/status');
  },

  getNodes: async (filter?: NodeFilterParams): Promise<NodeListResponse> => {
    return apiClient.get('/v0/management/proxy/nodes', { params: filter });
  },

  getPolicy: async (): Promise<ProxyPolicy> => {
    return apiClient.get('/v0/management/proxy/policy');
  },

  updatePolicy: async (policy: ProxyPolicy): Promise<ProxyPolicy> => {
    return apiClient.put('/v0/management/proxy/policy', policy);
  },

  healthCheck: async (req?: HealthCheckRequest): Promise<HealthCheckResult> => {
    return apiClient.post('/v0/management/proxy/health-check', req ?? { dry_run: true });
  },

  dryRunSelect: async (req?: Partial<SelectRequest>): Promise<SelectResult> => {
    return apiClient.post('/v0/management/proxy/select/dry-run', req ?? {});
  },

  select: async (req: SelectRequest): Promise<SelectResult> => {
    return apiClient.post('/v0/management/proxy/select', req);
  },

  rollback: async (req: RollbackRequest): Promise<{ ok: boolean; rolled_back: boolean }> => {
    return apiClient.post('/v0/management/proxy/rollback', req);
  },

  getHistory: async (operation?: string, page = 1, pageSize = 20): Promise<ProxyHistoryPage> => {
    return apiClient.get('/v0/management/proxy/history', {
      params: { operation, page, page_size: pageSize },
    });
  },
};
