import { useNavigate } from 'react-router-dom';
import { useSecurityShieldStore } from '@/stores';

export function SecurityShieldBanner() {
  const navigate = useNavigate();
  const status = useSecurityShieldStore((state) => state.status);
  const bannerDismissed = useSecurityShieldStore((state) => state.bannerDismissed);
  const dismissBanner = useSecurityShieldStore((state) => state.dismissBanner);

  if (!status || bannerDismissed || status.waf_risk_level === 'safe') {
    return null;
  }

  const isCritical = status.waf_risk_level === 'critical';
  const bgColor = isCritical ? 'rgba(239, 68, 68, 0.12)' : 'rgba(245, 158, 11, 0.12)';
  const borderColor = isCritical ? 'rgba(239, 68, 68, 0.3)' : 'rgba(245, 158, 11, 0.3)';
  const textColor = isCritical ? '#b91c1c' : '#b45309';
  const btnBg = isCritical ? '#ef4444' : '#f59e0b';

  return (
    <div
      style={{
        width: '100%',
        padding: '10px 18px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        borderBottom: `1px solid ${borderColor}`,
        backgroundColor: bgColor,
        color: textColor,
        fontSize: '12px',
        boxShadow: '0 2px 8px rgba(0,0,0,0.04)',
        transition: 'all 0.2s ease',
      }}
    >
      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <span style={{ fontSize: '16px' }}>{isCritical ? '🚨' : '⚠️'}</span>
        <div>
          <span style={{ fontWeight: 700 }}>
            {isCritical
              ? '【严重安全警报】Google 上游账号封控/未授权预警'
              : '【上游 WAF 风险预警】金丝雀探针捕获提示词指纹拦截 (429 RESOURCE_EXHAUSTED)'}
          </span>
          <span style={{ marginLeft: '8px', opacity: 0.9 }}>
            {isCritical
              ? `上游状态码: ${status.canary_status} (${status.canary_reason})。网关已激活单账号熔断防线，杜绝高频并发封号。`
              : `上游状态码: ${status.canary_status}。系统已自动阻断并保护当前账号，建议前往安全防线页面启动二分定位。`}
          </span>
        </div>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
        <button
          type="button"
          onClick={() => navigate('/security-shield')}
          style={{
            padding: '4px 12px',
            borderRadius: '6px',
            fontSize: '12px',
            fontWeight: 600,
            cursor: 'pointer',
            backgroundColor: btnBg,
            color: '#fff',
            border: 'none',
          }}
        >
          查看防线大盘
        </button>
        <button
          type="button"
          onClick={dismissBanner}
          style={{
            padding: '4px',
            background: 'none',
            border: 'none',
            color: textColor,
            cursor: 'pointer',
            fontSize: '14px',
            opacity: 0.7,
          }}
          title="关闭本条提醒"
        >
          ✕
        </button>
      </div>
    </div>
  );
}
