import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useSecurityShieldStore } from '@/stores';

export function SecurityShieldPill() {
  const navigate = useNavigate();
  const status = useSecurityShieldStore((state) => state.status);
  const startPolling = useSecurityShieldStore((state) => state.startPolling);
  const stopPolling = useSecurityShieldStore((state) => state.stopPolling);

  useEffect(() => {
    startPolling(30000); // 30秒静默轮询
    return () => {
      stopPolling();
    };
  }, [startPolling, stopPolling]);

  if (!status) {
    return null;
  }

  const risk = status.waf_risk_level;
  let bgClass = 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20';
  let dotClass = 'bg-emerald-500';
  let label = '防线正常';

  if (risk === 'warning') {
    bgClass = 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/30 animate-pulse';
    dotClass = 'bg-amber-500';
    label = `WAF 预警 (${status.canary_status})`;
  } else if (risk === 'critical') {
    bgClass = 'bg-rose-500/20 text-rose-600 dark:text-rose-400 border-rose-500/40 animate-pulse font-semibold';
    dotClass = 'bg-rose-500';
    label = `风控告警 (${status.canary_status})`;
  }

  return (
    <button
      type="button"
      onClick={() => navigate('/security-shield')}
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-full border transition-all cursor-pointer hover:opacity-85 shadow-sm ${bgClass}`}
      title={`Google 上游安全防线: ${status.canary_reason} | 延迟: ${status.canary_latency_sec}s | 点击查看态势感知大盘`}
    >
      <span className={`w-2 h-2 rounded-full ${dotClass}`} />
      <span className="font-mono">🛡️ {label}</span>
    </button>
  );
}
