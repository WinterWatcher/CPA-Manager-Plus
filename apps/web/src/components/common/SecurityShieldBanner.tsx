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

  return (
    <div
      className={`w-full px-4 py-2.5 flex items-center justify-between border-b text-xs transition-colors shadow-sm ${
        isCritical
          ? 'bg-rose-50 border-rose-200 text-rose-800 dark:bg-rose-950/40 dark:border-rose-900/60 dark:text-rose-200'
          : 'bg-amber-50 border-amber-200 text-amber-800 dark:bg-amber-950/40 dark:border-amber-900/60 dark:text-amber-200'
      }`}
    >
      <div className="flex items-center gap-2">
        <span className="text-base">{isCritical ? '🚨' : '⚠️'}</span>
        <div>
          <span className="font-semibold">
            {isCritical
              ? '【严重安全警报】Google 上游账号封控/未授权预警'
              : '【上游 WAF 风险预警】金丝雀探针捕获提示词指纹拦截 (429 RESOURCE_EXHAUSTED)'}
          </span>
          <span className="ml-2 opacity-90">
            {isCritical
              ? `上游状态码: ${status.canary_status} (${status.canary_reason})。网关已激活单账号熔断防线，杜绝高频并发封号。`
              : `上游状态码: ${status.canary_status}。系统已自动阻断并保护当前账号，建议前往安全防线页面启动二分定位。`}
          </span>
        </div>
      </div>

      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={() => navigate('/security-shield')}
          className={`px-2.5 py-1 rounded text-xs font-medium cursor-pointer transition-colors ${
            isCritical
              ? 'bg-rose-600 text-white hover:bg-rose-700'
              : 'bg-amber-600 text-white hover:bg-amber-700'
          }`}
        >
          查看防线大盘
        </button>
        <button
          type="button"
          onClick={dismissBanner}
          className="p-1 rounded opacity-60 hover:opacity-100 transition-opacity cursor-pointer text-sm"
          title="关闭本条提醒"
        >
          ✕
        </button>
      </div>
    </div>
  );
}
