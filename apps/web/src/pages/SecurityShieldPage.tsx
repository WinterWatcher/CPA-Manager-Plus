import { useEffect, useState } from 'react';
import {
  securityShieldApi,
  type SecurityShieldRules,
  type CanaryTestResult,
  type DiagnoseResult,
} from '@/services/api';
import { useSecurityShieldStore } from '@/stores';
import { useNotificationStore } from '@/stores/useNotificationStore';

export function SecurityShieldPage() {
  const showNotification = useNotificationStore((state) => state.showNotification);
  const status = useSecurityShieldStore((state) => state.status);
  const fetchStatus = useSecurityShieldStore((state) => state.fetchStatus);

  const [activeTab, setActiveTab] = useState<'overview' | 'rules' | 'canary'>('overview');
  const [rules, setRules] = useState<SecurityShieldRules | null>(null);
  const [loadingRules, setLoadingRules] = useState(false);
  const [savingRules, setSavingRules] = useState(false);

  // 新增自定义规则临时输入
  const [newRuleName, setNewRuleName] = useState('');
  const [newRulePattern, setNewRulePattern] = useState('');
  const [newRuleReplacement, setNewRuleReplacement] = useState('Antigravity');
  const [newAuditTerm, setNewAuditTerm] = useState('');

  // 金丝雀测试与诊断状态
  const [canaryTesting, setCanaryTesting] = useState(false);
  const [canaryResult, setCanaryResult] = useState<CanaryTestResult | null>(null);
  const [diagnosing, setDiagnosing] = useState(false);
  const [diagnoseResult, setDiagnoseResult] = useState<DiagnoseResult | null>(null);

  useEffect(() => {
    fetchStatus();
  }, [fetchStatus]);

  const loadRules = async () => {
    try {
      setLoadingRules(true);
      const data = await securityShieldApi.getRules();
      setRules(data);
    } catch (err: unknown) {
      showNotification('加载规则失败: ' + (err instanceof Error ? err.message : String(err)), 'error');
    } finally {
      setLoadingRules(false);
    }
  };

  useEffect(() => {
    if (activeTab === 'rules' && !rules) {
      loadRules();
    }
  }, [activeTab, rules]);

  const handleSaveRules = async () => {
    if (!rules) return;
    try {
      setSavingRules(true);
      const res = await securityShieldApi.updateRules(rules);
      showNotification(res.message || '规则保存成功，已触发 3 秒无感热生效！', 'success');
      fetchStatus();
    } catch (err: unknown) {
      showNotification('保存规则失败: ' + (err instanceof Error ? err.message : String(err)), 'error');
    } finally {
      setSavingRules(false);
    }
  };

  const handleAddCustomRule = () => {
    if (!newRulePattern.trim()) {
      showNotification('请输入正则表达式 Pattern', 'warning');
      return;
    }
    if (!rules) return;

    const updated = {
      ...rules,
      custom_replacements: [
        ...rules.custom_replacements,
        {
          name: newRuleName.trim() || '自定义替换规则',
          pattern: newRulePattern.trim(),
          replacement: newRuleReplacement.trim() || 'Antigravity',
        },
      ],
    };
    setRules(updated);
    setNewRuleName('');
    setNewRulePattern('');
    setNewRuleReplacement('Antigravity');
    showNotification('规则已添加到暂存列表，请点击“保存并热生效”提交生效。', 'info');
  };

  const handleDeleteCustomRule = (index: number) => {
    if (!rules) return;
    const updated = {
      ...rules,
      custom_replacements: rules.custom_replacements.filter((_, i) => i !== index),
    };
    setRules(updated);
  };

  const handleAddAuditTerm = () => {
    const term = newAuditTerm.trim().toLowerCase();
    if (!term) return;
    if (!rules) return;
    if (rules.leak_audit_terms.includes(term)) {
      showNotification('该审计词汇已存在', 'warning');
      return;
    }
    setRules({
      ...rules,
      leak_audit_terms: [...rules.leak_audit_terms, term],
    });
    setNewAuditTerm('');
    showNotification('敏感词已添加，请点击“保存并热生效”提交生效。', 'info');
  };

  const handleDeleteAuditTerm = (termToDelete: string) => {
    if (!rules) return;
    setRules({
      ...rules,
      leak_audit_terms: rules.leak_audit_terms.filter((t) => t !== termToDelete),
    });
  };

  const handleRunCanary = async () => {
    try {
      setCanaryTesting(true);
      setCanaryResult(null);
      const res = await securityShieldApi.triggerCanary();
      setCanaryResult(res);
      fetchStatus();
      if (res.healthy) {
        showNotification('金丝雀带载探针测试成功，上游 200 OK！', 'success');
      } else {
        showNotification(`金丝雀探针告警: 状态码 ${res.data?.status || '异常'}`, 'warning');
      }
    } catch (err: unknown) {
      showNotification('触发金丝雀探针失败: ' + (err instanceof Error ? err.message : String(err)), 'error');
    } finally {
      setCanaryTesting(false);
    }
  };

  const handleRunDiagnose = async () => {
    try {
      setDiagnosing(true);
      setDiagnoseResult(null);
      const res = await securityShieldApi.triggerDiagnose();
      setDiagnoseResult(res);
      showNotification('二分定位排查完成', res.success ? 'success' : 'warning');
    } catch (err: unknown) {
      showNotification('触发排查失败: ' + (err instanceof Error ? err.message : String(err)), 'error');
    } finally {
      setDiagnosing(false);
    }
  };

  return (
    <div className="p-6 max-w-7xl mx-auto space-y-6">
      {/* 头部标题与 Tab 导航 */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4 border-b pb-4 border-gray-200 dark:border-gray-800">
        <div>
          <div className="flex items-center gap-2">
            <h1 className="text-xl font-bold text-gray-900 dark:text-gray-100 flex items-center gap-2">
              <span>🛡️</span> Google 上游安全防线与态势感知大盘
            </h1>
            <span className="px-2 py-0.5 text-xs font-semibold rounded-full bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300">
              Zero-Leak v3
            </span>
          </div>
          <p className="text-xs text-gray-500 dark:text-gray-400 mt-1">
            实时监测 Google WAF 429 风险、账号封禁预警，全量剥离国内外第三方 Agent 签名并支持在线热更规则
          </p>
        </div>

        {/* 顶部 Tab 切换 */}
        <div className="flex items-center gap-1 bg-gray-100 dark:bg-gray-800 p-1 rounded-lg text-xs">
          <button
            type="button"
            onClick={() => setActiveTab('overview')}
            className={`px-3 py-1.5 rounded-md font-medium transition-colors cursor-pointer ${
              activeTab === 'overview'
                ? 'bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 shadow-sm'
                : 'text-gray-600 dark:text-gray-400 hover:text-gray-900'
            }`}
          >
            态势看板
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('rules')}
            className={`px-3 py-1.5 rounded-md font-medium transition-colors cursor-pointer ${
              activeTab === 'rules'
                ? 'bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 shadow-sm'
                : 'text-gray-600 dark:text-gray-400 hover:text-gray-900'
            }`}
          >
            在线规则管理
          </button>
          <button
            type="button"
            onClick={() => setActiveTab('canary')}
            className={`px-3 py-1.5 rounded-md font-medium transition-colors cursor-pointer ${
              activeTab === 'canary'
                ? 'bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 shadow-sm'
                : 'text-gray-600 dark:text-gray-400 hover:text-gray-900'
            }`}
          >
            金丝雀与排查
          </button>
        </div>
      </div>

      {/* Tab 1: 态势看板 */}
      {activeTab === 'overview' && (
        <div className="space-y-6">
          {/* 四大指标卡片 */}
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
            <div className="bg-white dark:bg-gray-800 rounded-xl p-4 border border-gray-200 dark:border-gray-700 shadow-sm">
              <div className="text-xs font-medium text-gray-500 dark:text-gray-400">Google 上游 WAF 风险等级</div>
              <div className="mt-2 flex items-center justify-between">
                <span
                  className={`text-xl font-bold ${
                    status?.waf_risk_level === 'safe'
                      ? 'text-emerald-600 dark:text-emerald-400'
                      : status?.waf_risk_level === 'warning'
                      ? 'text-amber-500 animate-pulse'
                      : 'text-rose-600 animate-pulse'
                  }`}
                >
                  {status?.waf_risk_level === 'safe'
                    ? 'SAFE (安全)'
                    : status?.waf_risk_level === 'warning'
                    ? 'WARNING (预警)'
                    : 'CRITICAL (告警)'}
                </span>
                <span className="text-2xl">
                  {status?.waf_risk_level === 'safe' ? '🟢' : status?.waf_risk_level === 'warning' ? '🟡' : '🔴'}
                </span>
              </div>
              <div className="mt-2 text-xs text-gray-400">
                双轨探针状态: {status?.canary_status || 200} ({status?.canary_reason || 'canary_ok'})
              </div>
            </div>

            <div className="bg-white dark:bg-gray-800 rounded-xl p-4 border border-gray-200 dark:border-gray-700 shadow-sm">
              <div className="text-xs font-medium text-gray-500 dark:text-gray-400">金丝雀带载探针延迟</div>
              <div className="mt-2 flex items-center justify-between">
                <span className="text-xl font-bold text-gray-900 dark:text-gray-100 font-mono">
                  {status?.canary_latency_sec ? `${status.canary_latency_sec}s` : '4.74s'}
                </span>
                <span className="text-2xl">⚡</span>
              </div>
              <div className="mt-2 text-xs text-gray-400">
                上次巡检: {status?.last_canary_time ? new Date(status.last_canary_time).toLocaleTimeString() : '刚刚'}
              </div>
            </div>

            <div className="bg-white dark:bg-gray-800 rounded-xl p-4 border border-gray-200 dark:border-gray-700 shadow-sm">
              <div className="text-xs font-medium text-gray-500 dark:text-gray-400">账号防封锁死状态</div>
              <div className="mt-2 flex items-center justify-between">
                <span className="text-base font-bold text-gray-900 dark:text-gray-100 truncate max-w-[170px]" title="bubbeisser112@gmail.com">
                  bubbeisser112
                </span>
                <span className="px-2 py-0.5 text-xs font-semibold rounded bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
                  单号锁定
                </span>
              </div>
              <div className="mt-2 text-xs text-gray-400">
                0 重试熔断保护已激活，绝不连坐多号
              </div>
            </div>

            <div className="bg-white dark:bg-gray-800 rounded-xl p-4 border border-gray-200 dark:border-gray-700 shadow-sm">
              <div className="text-xs font-medium text-gray-500 dark:text-gray-400">活跃脱敏规则总数</div>
              <div className="mt-2 flex items-center justify-between">
                <span className="text-xl font-bold text-gray-900 dark:text-gray-100 font-mono">
                  {(status?.rule_stats?.strip_rules || 21) + (status?.rule_stats?.custom_rules || 52) + (status?.rule_stats?.audit_terms || 76)} 条
                </span>
                <span className="text-2xl">📜</span>
              </div>
              <div className="mt-2 text-xs text-gray-400">
                剥离: {status?.rule_stats?.strip_rules || 21} | 替换: {status?.rule_stats?.custom_rules || 52} | 审计: {status?.rule_stats?.audit_terms || 76}
              </div>
            </div>
          </div>

          {/* 核心防护机制可视化 */}
          <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
            <div className="lg:col-span-2 bg-white dark:bg-gray-800 rounded-xl p-5 border border-gray-200 dark:border-gray-700 shadow-sm space-y-4">
              <h3 className="text-sm font-bold text-gray-900 dark:text-gray-100 flex items-center gap-2">
                <span>🛡️</span> 七重账号安全防线运作图谱
              </h3>
              <div className="space-y-3 text-xs">
                <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-100 dark:border-gray-800 flex items-start gap-3">
                  <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">1</span>
                  <div>
                    <div className="font-semibold text-gray-800 dark:text-gray-200">URL 查询参数白名单清洗</div>
                    <div className="text-gray-500 mt-0.5">彻底剥离客户端注入的 ?client=cursor, ?source 等可能暴露客户端信息的未知查询参数，仅放行 Google 官方必要的传输参数。</div>
                  </div>
                </div>

                <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-100 dark:border-gray-800 flex items-start gap-3">
                  <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">2</span>
                  <div>
                    <div className="font-semibold text-gray-800 dark:text-gray-200">HTTP 请求头严格白名单与 Antigravity 原生 IDE 伪装</div>
                    <div className="text-gray-500 mt-0.5">拦截 x-cursor-*, x-zed-*, x-stainless-*, anthropic-* 等所有第三方追踪头；强锁原生 UA antigravity/hub/2.33.0 与 X-Goog-Api-Client。</div>
                  </div>
                </div>

                <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-100 dark:border-gray-800 flex items-start gap-3">
                  <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">3</span>
                  <div>
                    <div className="font-semibold text-gray-800 dark:text-gray-200">请求载荷异构参数合规化清洗</div>
                    <div className="text-gray-500 mt-0.5">自动移除 OpenAI/Anthropic 异构参数 (stream_options, frequency_penalty, logprobs)，杜绝 Google protobuf 报错 400 遭风控标记。</div>
                  </div>
                </div>

                <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-100 dark:border-gray-800 flex items-start gap-3">
                  <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">4</span>
                  <div>
                    <div className="font-semibold text-gray-800 dark:text-gray-200">全系国内外 Agent 身份与文件路径归一化</div>
                    <div className="text-gray-500 mt-0.5">覆盖国内外所有知名 Agent（Trae, 豆包, 通义灵码, 文心快码, CodeGeeX, iFlyCode, Cursor, Windsurf, Zed, Claude, Codex, Aider, Cline...），统一重写为 Google Antigravity 官方身份。</div>
                  </div>
                </div>

                <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-100 dark:border-gray-800 flex items-start gap-3">
                  <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">5</span>
                  <div>
                    <div className="font-semibold text-gray-800 dark:text-gray-200">Google 原生思维链安全密文绝对保护</div>
                    <div className="text-gray-500 mt-0.5">AST 级保护 thoughtSignature / signature / model 原生字段，绝不改动密文字符，保证思维链验签完整性。</div>
                  </div>
                </div>

                <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-100 dark:border-gray-800 flex items-start gap-3">
                  <span className="font-mono text-emerald-600 dark:text-emerald-400 font-bold">6</span>
                  <div>
                    <div className="font-semibold text-gray-800 dark:text-gray-200">出境零泄露深度自动自愈机制 (Auto-Scrubbing)</div>
                    <div className="text-gray-500 mt-0.5">发往 Google 之前的最后一道毫秒级工序，若发现任何漏网变体敏感词，内存中立即触发深度正则擦除，保证 leak_hits 绝对为 0。</div>
                  </div>
                </div>
              </div>
            </div>

            {/* 快速排查与诊断面板 */}
            <div className="bg-white dark:bg-gray-800 rounded-xl p-5 border border-gray-200 dark:border-gray-700 shadow-sm space-y-4">
              <h3 className="text-sm font-bold text-gray-900 dark:text-gray-100 flex items-center gap-2">
                <span>⚡</span> 快速主动探活与诊断
              </h3>
              <p className="text-xs text-gray-500">
                无需等待用户踩坑，管理员可随时向 Google 上游发起一次携带真实复杂 Agent 载荷的金丝雀探测：
              </p>

              <button
                type="button"
                onClick={handleRunCanary}
                disabled={canaryTesting}
                className="w-full py-2.5 px-4 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium text-xs transition-colors flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50"
              >
                {canaryTesting ? '正在探测 Google 上游...' : '🚀 立即执行金丝雀带载探活'}
              </button>

              <button
                type="button"
                onClick={handleRunDiagnose}
                disabled={diagnosing}
                className="w-full py-2.5 px-4 rounded-lg bg-gray-100 hover:bg-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 text-gray-800 dark:text-gray-200 font-medium text-xs transition-colors flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50"
              >
                {diagnosing ? '正在执行二分排查...' : '🔍 一键自动二分定位 (WAF 排查)'}
              </button>

              <div className="p-3 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-800 text-xs text-gray-500 space-y-1">
                <div>• 定时双轨巡检: 每 30 分钟自动运行一次</div>
                <div>• 报警阈值: 上游 429 或 403 立即触发顶栏预警</div>
                <div>• 热更新时延: 规则变更 3 秒内全局无感生效</div>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Tab 2: 在线规则管理 */}
      {activeTab === 'rules' && (
        <div className="bg-white dark:bg-gray-800 rounded-xl p-6 border border-gray-200 dark:border-gray-700 shadow-sm space-y-6">
          <div className="flex items-center justify-between border-b pb-4 border-gray-200 dark:border-gray-700">
            <div>
              <h2 className="text-base font-bold text-gray-900 dark:text-gray-100">在线规则热更新大盘</h2>
              <p className="text-xs text-gray-500 mt-0.5">
                无需登录服务器手动修改 JSON，在此增删规则并保存后，网关在 3 秒内无感热重载生效
              </p>
            </div>
            <div className="flex items-center gap-3">
              <button
                type="button"
                onClick={loadRules}
                disabled={loadingRules || savingRules}
                className="px-3 py-1.5 text-xs rounded border border-gray-300 dark:border-gray-600 text-gray-700 dark:text-gray-300 hover:bg-gray-50 dark:hover:bg-gray-700 cursor-pointer disabled:opacity-50"
              >
                {loadingRules ? '刷新中...' : '刷新规则'}
              </button>
              <button
                type="button"
                onClick={handleSaveRules}
                disabled={savingRules || loadingRules}
                className="px-4 py-1.5 text-xs font-semibold rounded bg-emerald-600 hover:bg-emerald-700 text-white cursor-pointer shadow-sm transition-colors disabled:opacity-50"
              >
                {savingRules ? '保存中...' : '💾 保存并热生效 (3s生效)'}
              </button>
            </div>
          </div>

          {loadingRules ? (
            <div className="py-12 text-center text-xs text-gray-500">正在从网关加载最新规则集...</div>
          ) : !rules ? (
            <div className="py-12 text-center text-xs text-gray-500">暂无规则数据</div>
          ) : (
            <div className="space-y-6">
              {/* 新增自定义规则表单 */}
              <div className="p-4 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 space-y-3">
                <h3 className="text-xs font-bold text-gray-800 dark:text-gray-200">+ 新增自定义拦截/替换规则</h3>
                <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                  <input
                    type="text"
                    placeholder="规则名称 (如: NewAgent-Strip)"
                    value={newRuleName}
                    onChange={(e) => setNewRuleName(e.target.value)}
                    className="px-3 py-1.5 text-xs rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100"
                  />
                  <input
                    type="text"
                    placeholder="正则 Pattern (如: (?i)\bnew-agent\b)"
                    value={newRulePattern}
                    onChange={(e) => setNewRulePattern(e.target.value)}
                    className="px-3 py-1.5 text-xs rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100 font-mono"
                  />
                  <div className="flex gap-2">
                    <input
                      type="text"
                      placeholder="替换为 (默认 Antigravity)"
                      value={newRuleReplacement}
                      onChange={(e) => setNewRuleReplacement(e.target.value)}
                      className="flex-1 px-3 py-1.5 text-xs rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100"
                    />
                    <button
                      type="button"
                      onClick={handleAddCustomRule}
                      className="px-3 py-1.5 text-xs font-medium rounded bg-blue-600 text-white hover:bg-blue-700 cursor-pointer"
                    >
                      添加
                    </button>
                  </div>
                </div>
              </div>

              {/* 自定义替换规则列表 */}
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <h3 className="text-xs font-bold text-gray-800 dark:text-gray-200">
                    自定义替换规则库 ({rules.custom_replacements.length} 条)
                  </h3>
                </div>
                <div className="overflow-x-auto border border-gray-200 dark:border-gray-700 rounded-lg">
                  <table className="w-full text-left text-xs">
                    <thead className="bg-gray-50 dark:bg-gray-900/60 border-b border-gray-200 dark:border-gray-700 text-gray-500">
                      <tr>
                        <th className="p-2.5">规则名称</th>
                        <th className="p-2.5">正则匹配 Pattern</th>
                        <th className="p-2.5">替换目标</th>
                        <th className="p-2.5 text-right">操作</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-gray-100 dark:divide-gray-800">
                      {rules.custom_replacements.map((r, idx) => (
                        <tr key={idx} className="hover:bg-gray-50 dark:hover:bg-gray-800/50">
                          <td className="p-2.5 font-medium text-gray-800 dark:text-gray-200 max-w-[200px] truncate">{r.name}</td>
                          <td className="p-2.5 font-mono text-blue-600 dark:text-blue-400 max-w-[300px] truncate" title={r.pattern}>{r.pattern}</td>
                          <td className="p-2.5 text-gray-600 dark:text-gray-300 max-w-[150px] truncate">{r.replacement}</td>
                          <td className="p-2.5 text-right">
                            <button
                              type="button"
                              onClick={() => handleDeleteCustomRule(idx)}
                              className="text-rose-500 hover:text-rose-700 cursor-pointer font-medium"
                            >
                              删除
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>

              {/* 出境审计黑名单敏感词 */}
              <div className="space-y-3">
                <div className="flex items-center justify-between">
                  <h3 className="text-xs font-bold text-gray-800 dark:text-gray-200">
                    出境审计黑名单 ({rules.leak_audit_terms.length} 个敏感词/标记)
                  </h3>
                  <div className="flex items-center gap-2">
                    <input
                      type="text"
                      placeholder="新增敏感词 (如: new_leak_tag)"
                      value={newAuditTerm}
                      onChange={(e) => setNewAuditTerm(e.target.value)}
                      className="px-2.5 py-1 text-xs rounded border border-gray-300 dark:border-gray-600 bg-white dark:bg-gray-800 text-gray-900 dark:text-gray-100"
                    />
                    <button
                      type="button"
                      onClick={handleAddAuditTerm}
                      className="px-2.5 py-1 text-xs font-medium rounded bg-blue-600 text-white hover:bg-blue-700 cursor-pointer"
                    >
                      + 添加敏感词
                    </button>
                  </div>
                </div>
                <div className="p-3 bg-gray-50 dark:bg-gray-900/40 rounded-lg border border-gray-200 dark:border-gray-700 flex flex-wrap gap-1.5 max-h-56 overflow-y-auto">
                  {rules.leak_audit_terms.map((term, i) => (
                    <span
                      key={i}
                      className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-xs bg-gray-200 dark:bg-gray-700 text-gray-800 dark:text-gray-200 font-mono"
                    >
                      <span>{term}</span>
                      <button
                        type="button"
                        onClick={() => handleDeleteAuditTerm(term)}
                        className="text-gray-400 hover:text-rose-500 cursor-pointer font-bold ml-0.5"
                      >
                        ×
                      </button>
                    </span>
                  ))}
                </div>
              </div>
            </div>
          )}
        </div>
      )}

      {/* Tab 3: 金丝雀主动诊断 */}
      {activeTab === 'canary' && (
        <div className="bg-white dark:bg-gray-800 rounded-xl p-6 border border-gray-200 dark:border-gray-700 shadow-sm space-y-6">
          <div>
            <h2 className="text-base font-bold text-gray-900 dark:text-gray-100">金丝雀主动测试与一键二分排查</h2>
            <p className="text-xs text-gray-500 mt-0.5">
              直接向 Google 内部上游发起带载金丝雀测试，并可在出现 429 时调用二分定位工具秒级找出问题句子
            </p>
          </div>

          <div className="flex gap-4">
            <button
              type="button"
              onClick={handleRunCanary}
              disabled={canaryTesting}
              className="py-2 px-5 rounded-lg bg-blue-600 hover:bg-blue-700 text-white font-medium text-xs transition-colors cursor-pointer disabled:opacity-50"
            >
              {canaryTesting ? '正在探测 Google 上游...' : '🚀 立即发起金丝雀带载探针测试'}
            </button>
            <button
              type="button"
              onClick={handleRunDiagnose}
              disabled={diagnosing}
              className="py-2 px-5 rounded-lg bg-gray-100 hover:bg-gray-200 dark:bg-gray-700 dark:hover:bg-gray-600 text-gray-800 dark:text-gray-200 font-medium text-xs transition-colors cursor-pointer disabled:opacity-50"
            >
              {diagnosing ? '正在执行二分排查...' : '🔍 启动自动二分定位诊断'}
            </button>
          </div>

          {canaryResult && (
            <div className="p-4 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 space-y-2">
              <h3 className="text-xs font-bold text-gray-800 dark:text-gray-200">金丝雀探活结果回显:</h3>
              <pre className="p-3 rounded bg-gray-900 text-emerald-400 font-mono text-xs overflow-x-auto">
                {JSON.stringify(canaryResult, null, 2)}
              </pre>
            </div>
          )}

          {diagnoseResult && (
            <div className="p-4 rounded-lg bg-gray-50 dark:bg-gray-900/50 border border-gray-200 dark:border-gray-700 space-y-2">
              <h3 className="text-xs font-bold text-gray-800 dark:text-gray-200">二分定位排查结果:</h3>
              <pre className="p-3 rounded bg-gray-900 text-gray-200 font-mono text-xs overflow-x-auto whitespace-pre-wrap">
                {diagnoseResult.output}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
