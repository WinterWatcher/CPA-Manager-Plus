import { create } from 'zustand';
import { securityShieldApi, type SecurityShieldStatus } from '@/services/api';

interface SecurityShieldState {
  status: SecurityShieldStatus | null;
  isLoading: boolean;
  error: string | null;
  lastFetched: number;
  bannerDismissed: boolean;
  pollIntervalId: number | null;

  fetchStatus: () => Promise<void>;
  startPolling: (intervalMs?: number) => void;
  stopPolling: () => void;
  dismissBanner: () => void;
  resetDismissed: () => void;
}

export const useSecurityShieldStore = create<SecurityShieldState>((set, get) => ({
  status: null,
  isLoading: false,
  error: null,
  lastFetched: 0,
  bannerDismissed: false,
  pollIntervalId: null,

  fetchStatus: async () => {
    try {
      set({ isLoading: true, error: null });
      const status = await securityShieldApi.getStatus();
      const prevStatus = get().status;

      // 如果风险级别从 safe 变成 warning/critical，重新重置 dismissed 状态，确保及时告警
      if (
        prevStatus &&
        prevStatus.waf_risk_level === 'safe' &&
        status.waf_risk_level !== 'safe'
      ) {
        set({ bannerDismissed: false });
      }

      set({
        status,
        isLoading: false,
        lastFetched: Date.now(),
      });
    } catch (err: unknown) {
      set({
        isLoading: false,
        error: err instanceof Error ? err.message : String(err),
      });
    }
  },

  startPolling: (intervalMs = 30000) => {
    const existing = get().pollIntervalId;
    if (existing) {
      window.clearInterval(existing);
    }
    // 立即执行一次
    get().fetchStatus();
    const timer = window.setInterval(() => {
      get().fetchStatus();
    }, intervalMs);
    set({ pollIntervalId: timer });
  },

  stopPolling: () => {
    const timer = get().pollIntervalId;
    if (timer) {
      window.clearInterval(timer);
      set({ pollIntervalId: null });
    }
  },

  dismissBanner: () => {
    set({ bannerDismissed: true });
  },

  resetDismissed: () => {
    set({ bannerDismissed: false });
  },
}));
