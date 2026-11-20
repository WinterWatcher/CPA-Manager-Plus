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
  let label = '防线正常';
  let color = '#10b981';
  let bg = 'rgba(16, 185, 129, 0.12)';
  let border = 'rgba(16, 185, 129, 0.3)';

  if (risk === 'warning') {
    label = `WAF 预警 (${status.canary_status})`;
    color = '#d97706';
    bg = 'rgba(245, 158, 11, 0.15)';
    border = 'rgba(245, 158, 11, 0.4)';
  } else if (risk === 'critical') {
    label = `风控告警 (${status.canary_status})`;
    color = '#ef4444';
    bg = 'rgba(239, 68, 68, 0.15)';
    border = 'rgba(239, 68, 68, 0.4)';
  }

  return (
    <button
      type="button"
      onClick={() => navigate('/security-shield')}
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: '6px',
        padding: '3px 10px',
        fontSize: '12px',
        fontWeight: 600,
        borderRadius: '9999px',
        border: `1px solid ${border}`,
        backgroundColor: bg,
        color: color,
        cursor: 'pointer',
        transition: 'all 0.2s ease',
      }}
      title={`Google 上游安全防线: ${status.canary_reason} | 延迟: ${status.canary_latency_sec}s | 点击查看态势感知大盘`}
    >
      <span
        style={{
          width: '8px',
          height: '8px',
          borderRadius: '50%',
          backgroundColor: color,
          boxShadow: `0 0 6px ${color}`,
        }}
      />
      <span style={{ fontFamily: 'var(--font-mono, monospace)' }}>🛡️ {label}</span>
    </button>
  );
}
