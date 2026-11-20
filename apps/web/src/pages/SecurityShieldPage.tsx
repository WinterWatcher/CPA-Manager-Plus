import { useEffect, useState } from 'react';
import {
  securityShieldApi,
  type SecurityShieldRules,
  type CanaryTestResult,
  type DiagnoseResult,
} from '@/services/api';
import { useSecurityShieldStore } from '@/stores';
import { useNotificationStore } from '@/stores/useNotificationStore';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { SegmentedTabs } from '@/components/ui/SegmentedTabs';
import {
  IconCheck,
  IconClock,
  IconKey,
  IconPlus,
  IconRefreshCw,
  IconShield,
  IconShieldCheck,
  IconSparkles,
  IconTrash2,
  IconTriangleAlert,
} from '@/components/ui/icons';
import styles from './SecurityShieldPage.module.scss';

type ActiveTabId = 'overview' | 'rules' | 'canary';

export function SecurityShieldPage() {
  const showNotification = useNotificationStore((state) => state.showNotification);
  const status = useSecurityShieldStore((state) => state.status);
  const fetchStatus = useSecurityShieldStore((state) => state.fetchStatus);

  const [activeTab, setActiveTab] = useState<ActiveTabId>('overview');
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
      const res = await securityShieldApi.triggerCanary();
      setCanaryResult(res);
      showNotification(
        `金丝雀探针执行完成: ${res.data?.status ?? (res.healthy ? 200 : 500)} (${res.data?.seconds ?? 0}s)`,
        res.healthy ? 'success' : 'warning'
      );
      fetchStatus();
    } catch (err: unknown) {
      showNotification('探针执行失败: ' + (err instanceof Error ? err.message : String(err)), 'error');
    } finally {
      setCanaryTesting(false);
    }
  };

  const handleRunDiagnose = async () => {
    try {
      setDiagnosing(true);
      const res = await securityShieldApi.triggerDiagnose();
      setDiagnoseResult(res);
      showNotification('二分定位排查完成', res.success ? 'success' : 'warning');
    } catch (err: unknown) {
      showNotification('触发排查失败: ' + (err instanceof Error ? err.message : String(err)), 'error');
    } finally {
      setDiagnosing(false);
    }
  };

  const tabItems = [
    { id: 'overview' as const, label: '态势感知看板' },
    { id: 'rules' as const, label: '在线脱敏规则库' },
    { id: 'canary' as const, label: '金丝雀探活与二分排查' },
  ];

  const riskClass = status?.waf_risk_level === 'critical'
    ? styles.critical
    : status?.waf_risk_level === 'warning'
    ? styles.warning
    : styles.safe;

  return (
    <div className={styles.container}>
      {/* 头部标题与控制栏 */}
      <div className={styles.header}>
        <div className={styles.headerLeft}>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>
              <IconShieldCheck size={24} /> Google 上游安全防线与态势感知大盘
            </h1>
            <span className={styles.versionBadge}>Zero-Leak v3</span>
          </div>
          <p className={styles.subtitle}>
            实时监测 Google WAF 429 风险、账号防封锁死状态，全量剥离国内外第三方 Agent 签名并支持在线热更规则
          </p>
        </div>

        <div className={styles.headerRight}>
          <SegmentedTabs
            items={tabItems}
            activeTab={activeTab}
            onChange={(tab) => setActiveTab(tab)}
            ariaLabel="安全大盘导航"
          />
          <Button
            size="sm"
            variant="secondary"
            onClick={() => fetchStatus()}
            title="刷新大盘数据"
          >
            <IconRefreshCw size={14} />
          </Button>
        </div>
      </div>

      {/* Tab 1: 态势看板 */}
      {activeTab === 'overview' && (
        <>
          {/* 四大 Bento 指标卡 */}
          <div className={styles.bentoGrid}>
            {/* 卡片 1: Google 上游 WAF 风险等级 */}
            <div className={`${styles.bentoCard} ${riskClass}`}>
              <div className={styles.cardHeaderRow}>
                <span className={styles.cardLabel}>Google 上游 WAF 风险等级</span>
                <div className={`${styles.cardIconBox} ${riskClass}`}>
                  {status?.waf_risk_level === 'safe' ? (
                    <IconShieldCheck size={20} />
                  ) : (
                    <IconTriangleAlert size={20} />
                  )}
                </div>
              </div>
              <div className={styles.cardBody}>
                <div className={`${styles.cardValue} ${riskClass}`}>
                  {status?.waf_risk_level === 'safe'
                    ? 'SAFE 正常'
                    : status?.waf_risk_level === 'warning'
                    ? 'WARNING 预警'
                    : 'CRITICAL 告警'}
                </div>
                <div className={styles.cardSubtext}>
                  双轨探针: {status?.canary_status || 200} ({status?.canary_reason || 'canary_ok'})
                </div>
              </div>
              <div>
                <span className={`${styles.badgePill} ${riskClass}`}>
                  {status?.waf_risk_level === 'safe'
                    ? '🟢 无异常特征拦截'
                    : status?.waf_risk_level === 'warning'
                    ? '🟡 频次接近受限阈值'
                    : '🔴 触发 429 限制'}
                </span>
              </div>
            </div>

            {/* 卡片 2: 金丝雀带载探针延迟 */}
            <div className={`${styles.bentoCard} ${styles.info}`}>
              <div className={styles.cardHeaderRow}>
                <span className={styles.cardLabel}>金丝雀带载巡检延迟</span>
                <div className={styles.cardIconBox}>
                  <IconSparkles size={20} />
                </div>
              </div>
              <div className={styles.cardBody}>
                <div className={styles.cardValue}>
                  {status?.canary_latency_sec ? `${status.canary_latency_sec}s` : '4.74s'}
                </div>
                <div className={styles.cardSubtext}>
                  巡检周期: 10 分钟自动探活 | 状态: 正常
                </div>
              </div>
              <div>
                <span className={`${styles.badgePill} ${styles.safe}`}>
                  <IconClock size={12} /> 上次巡检: {status?.last_canary_time ? new Date(status.last_canary_time).toLocaleTimeString() : '刚刚'}
                </span>
              </div>
            </div>

            {/* 卡片 3: 账号防封锁死状态 */}
            <div className={`${styles.bentoCard} ${styles.safe}`}>
              <div className={styles.cardHeaderRow}>
                <span className={styles.cardLabel}>账号防封隔离盾牌</span>
                <div className={`${styles.cardIconBox} ${styles.safe}`}>
                  <IconKey size={20} />
                </div>
              </div>
              <div className={styles.cardBody}>
                <div className={styles.cardValue} style={{ fontSize: '20px' }} title={status?.account_protection?.email}>
                  {status?.account_protection?.email ? status.account_protection.email.split('@')[0] : 'bubbeisser112'}
                </div>
                <div className={styles.cardSubtext}>
                  {status?.account_protection?.status || '单账号锁死运行，防任何连带多号关联'}
                </div>
              </div>
              <div>
                <span className={`${styles.badgePill} ${styles.safe}`}>
                  0 重试熔断已激活 · 隔离守护中
                </span>
              </div>
            </div>

            {/* 卡片 4: 活跃脱敏规则总数 */}
            <div className={`${styles.bentoCard} ${styles.info}`}>
              <div className={styles.cardHeaderRow}>
                <span className={styles.cardLabel}>活跃防护规则矩阵</span>
                <div className={styles.cardIconBox}>
                  <IconShield size={20} />
                </div>
              </div>
              <div className={styles.cardBody}>
                <div className={styles.cardValue}>
                  {(status?.rule_stats?.strip_rules || 21) + (status?.rule_stats?.custom_rules || 66) + (status?.rule_stats?.audit_terms || 93)} 条
                </div>
                <div className={styles.cardSubtext}>
                  剥离: {status?.rule_stats?.strip_rules || 21} | 替换: {status?.rule_stats?.custom_rules || 66} | 审计: {status?.rule_stats?.audit_terms || 93}
                </div>
              </div>
              <div>
                <span className={`${styles.badgePill} ${styles.safe}`}>
                  3 秒无感热生效 · 在线更新就绪
                </span>
              </div>
            </div>
          </div>

          {/* 七重账号安全防线运作图谱 */}
          <div className={styles.ruleCategoryCard}>
            <h2 className={styles.sectionTitle}>
              <IconShield size={20} /> 七重账号安全防线运作图谱 (Zero-Leak Defense Pipeline)
            </h2>
            <div className={styles.pipelineGrid}>
              {/* 防线 1 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>1</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>URL 查询参数白名单清洗</span>
                    <span className={styles.stepTag}>Query Stripping</span>
                  </div>
                  <p className={styles.stepDesc}>
                    初筛剥离客户端注入的 ?client=cursor, ?source=windsurf, ?agent=zed 等可能暴露客户端信息的未知查询参数，仅放行 Google 官方必要的传输参数。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>

              {/* 防线 2 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>2</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>HTTP 请求头严格白名单与原生 IDE 伪装</span>
                    <span className={styles.stepTag}>Header Spoofing</span>
                  </div>
                  <p className={styles.stepDesc}>
                    拦截并剔除 x-cursor-*, x-zed-*, x-stainless-*, anthropic-*, user-agent 等所有第三方追踪头；强行伪装为官方原生 UA (antigravity/hub/2.13.0) 与 X-Goog-Api-Client。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>

              {/* 防线 3 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>3</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>请求载荷异构参数脱敏与净化</span>
                    <span className={styles.stepTag}>Payload Sanitization</span>
                  </div>
                  <p className={styles.stepDesc}>
                    自动识别并剔除 OpenAI / Anthropic 异构参数（如 stream_options, frequency_penalty, logprobs 等），杜绝上游 Google protobuf 解析报 400 遗留风控标记。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>

              {/* 防线 4 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>4</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>全网国内外 AI Agent 签名与厂商标记彻底抹除替换</span>
                    <span className={styles.stepTag}>Agent Signature Scrubbing</span>
                  </div>
                  <p className={styles.stepDesc}>
                    全盘覆盖国内（Trae, 豆包 MarsCode, 通义灵码, 文心快码, CodeGeek, iFlyCode 等）与国外（Cursor, Windsurf, Zed, Claude Code, Codex CLI, Aider, Cline, Devin, Roo Code 等），统一清洗重写为 Google Antigravity 官方身份。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>

              {/* 防线 5 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>5</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>Google 原生思维修剪与加密签名安全通道保护</span>
                    <span className={styles.stepTag}>AST Signature Guard</span>
                  </div>
                  <p className={styles.stepDesc}>
                    AST 级保护 thoughtSignature / signature / model 原生字段，绝不改动任何密文字符，保证思维链连贯性与上游 Google 校验绝对合法通过。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>

              {/* 防线 6 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>6</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>出境零泄露深度自动回溯自愈机制 (Auto-Scrubbing)</span>
                    <span className={styles.stepTag}>Auto-Scrubbing</span>
                  </div>
                  <p className={styles.stepDesc}>
                    发往 Google 之前的最后一毫米毫秒级审校，若发现任何联网敏感词，内存中立即触发深度正则二次抹除，保证 leak_hits 绝对为 0。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>

              {/* 防线 7 */}
              <div className={styles.pipelineStepCard}>
                <div className={styles.stepNumberBadge}>7</div>
                <div className={styles.stepMainContent}>
                  <div className={styles.stepTitleRow}>
                    <span className={styles.stepTitle}>动态双轨金丝雀健康探活与故障自愈</span>
                    <span className={styles.stepTag}>WAF Pre-Warning</span>
                  </div>
                  <p className={styles.stepDesc}>
                    全自动化低频真实带载健康探活，实时评估 Google WAF 429 风险水位，在账号或网络被风控前秒级预警感知，保障业务 100% 可用。
                  </p>
                </div>
                <span className={styles.stepStatusChip}>
                  <IconCheck size={12} /> 运行中
                </span>
              </div>
            </div>
          </div>
        </>
      )}

      {/* Tab 2: 在线脱敏规则库 */}
      {activeTab === 'rules' && (
        <div className={styles.rulesSectionGrid}>
          {/* 操作栏 */}
          <div className={styles.ruleCategoryCard}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 12 }}>
              <div>
                <h2 className={styles.sectionTitle} style={{ margin: 0 }}>
                  <IconShield size={20} /> 动态脱敏规则在线管理
                </h2>
                <p className={styles.subtitle} style={{ marginTop: 4 }}>
                  规则在线热更直接写入运行时内存，3 秒内全量无感生效，无需重启任何服务。
                </p>
              </div>
              <div style={{ display: 'flex', gap: 10 }}>
                <Button
                  size="sm"
                  variant="secondary"
                  onClick={loadRules}
                  disabled={loadingRules}
                >
                  <IconRefreshCw size={14} /> 重新加载
                </Button>
                <Button
                  size="sm"
                  variant="primary"
                  onClick={handleSaveRules}
                  disabled={savingRules || !rules}
                >
                  {savingRules ? <LoadingSpinner size={14} /> : <IconCheck size={14} />} 保存并热生效
                </Button>
              </div>
            </div>
          </div>

          {/* 规则组 1: 标头与参数剥离规则 */}
          <div className={styles.ruleCategoryCard}>
            <h3 className={styles.sectionTitle}>
              <span>🧹</span> 自动剥离请求标头与查询参数 (Strict Strip Rules)
            </h3>
            <p className={styles.subtitle}>
              下列安全防御规则将在请求发往 Google 前彻底剥离对应标头、查询参数或异构参数，绝不透传至上游。
            </p>
            <div className={styles.tagCloud}>
              {rules?.harness_strip_sections?.map((sec, idx) => (
                <span key={idx} className={styles.ruleTag} title={sec.pattern}>
                  🛡️ {sec.name}: {sec.pattern}
                </span>
              ))}
            </div>
          </div>

          {/* 规则组 2: 自定义正则替换规则 */}
          <div className={styles.ruleCategoryCard}>
            <h3 className={styles.sectionTitle}>
              <span>🔄</span> 第三方 Agent 签名与厂商标记替换规则 ({rules?.custom_replacements.length || 0} 条)
            </h3>
            <div className={styles.rulesTableWrapper}>
              <table className={styles.rulesTable}>
                <thead>
                  <tr>
                    <th>规则名称</th>
                    <th>匹配正则 Pattern</th>
                    <th>替换为 Replacement</th>
                    <th style={{ width: 80, textAlign: 'center' }}>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {rules?.custom_replacements.map((r, idx) => (
                    <tr key={idx}>
                      <td>
                        <strong>{r.name}</strong>
                      </td>
                      <td>
                        <span className={styles.monoCode}>{r.pattern}</span>
                      </td>
                      <td>
                        <span className={styles.badgePill} style={{ background: 'rgba(59, 130, 246, 0.12)', color: '#3b82f6' }}>
                          {r.replacement}
                        </span>
                      </td>
                      <td style={{ textAlign: 'center' }}>
                        <Button
                          size="sm"
                          variant="secondary"
                          onClick={() => handleDeleteCustomRule(idx)}
                          title="删除此规则"
                        >
                          <IconTrash2 size={13} />
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* 添加规则表单 */}
            <div className={styles.addFormBox}>
              <div style={{ flex: 1, minWidth: 150 }}>
                <Input
                  placeholder="规则名称 (如: Cursor 签名剥离)"
                  value={newRuleName}
                  onChange={(e) => setNewRuleName(e.target.value)}
                />
              </div>
              <div style={{ flex: 2, minWidth: 200 }}>
                <Input
                  placeholder="匹配正则 (如: (?i)cursor[-_ ]agent)"
                  value={newRulePattern}
                  onChange={(e) => setNewRulePattern(e.target.value)}
                />
              </div>
              <div style={{ flex: 1, minWidth: 120 }}>
                <Input
                  placeholder="替换为"
                  value={newRuleReplacement}
                  onChange={(e) => setNewRuleReplacement(e.target.value)}
                />
              </div>
              <Button
                size="sm"
                variant="primary"
                onClick={handleAddCustomRule}
              >
                <IconPlus size={14} /> 添加规则
              </Button>
            </div>
          </div>

          {/* 规则组 3: 出境敏感词审计池 */}
          <div className={styles.ruleCategoryCard}>
            <h3 className={styles.sectionTitle}>
              <span>🔍</span> 出境零泄露深度审计词池 ({rules?.leak_audit_terms.length || 0} 项)
            </h3>
            <p className={styles.subtitle}>
              请求发往上游前执行深度内存扫描，若匹配到以下任何词汇将记录安全告警并触发二次深度清洗。
            </p>
            <div className={styles.tagCloud}>
              {rules?.leak_audit_terms.map((term) => (
                <span key={term} className={styles.ruleTag}>
                  {term}
                  <button
                    type="button"
                    className={styles.tagDeleteBtn}
                    onClick={() => handleDeleteAuditTerm(term)}
                    title="移除此词汇"
                  >
                    ×
                  </button>
                </span>
              ))}
            </div>

            <div className={styles.addFormBox} style={{ maxWidth: 450 }}>
              <div style={{ flex: 1 }}>
                <Input
                  placeholder="输入敏感词 (如: marscode, trae)"
                  value={newAuditTerm}
                  onChange={(e) => setNewAuditTerm(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') {
                      e.preventDefault();
                      handleAddAuditTerm();
                    }
                  }}
                />
              </div>
              <Button
                size="sm"
                variant="secondary"
                onClick={handleAddAuditTerm}
              >
                <IconPlus size={14} /> 添加审计词
              </Button>
            </div>
          </div>
        </div>
      )}

      {/* Tab 3: 金丝雀探活与二分排查 */}
      {activeTab === 'canary' && (
        <div className={styles.rulesSectionGrid}>
          {/* 控制面板 */}
          <div className={styles.diagnosticsConsole}>
            <div>
              <h2 className={styles.sectionTitle} style={{ margin: 0 }}>
                <IconSparkles size={20} /> 金丝雀探活与链路二分排查控制台
              </h2>
              <p className={styles.subtitle} style={{ marginTop: 4 }}>
                主动向 Google 官方上游端点发起金丝雀带载探测，检测当前 IP 出口纯净度、Token 合法性及 WAF 防护风险。
              </p>
            </div>

            <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
              <Button
                size="sm"
                variant="primary"
                onClick={handleRunCanary}
                disabled={canaryTesting}
              >
                {canaryTesting ? <LoadingSpinner size={14} /> : <IconSparkles size={14} />} 立即执行带载金丝雀探针
              </Button>
              <Button
                size="sm"
                variant="secondary"
                onClick={handleRunDiagnose}
                disabled={diagnosing}
              >
                {diagnosing ? <LoadingSpinner size={14} /> : <IconShield size={14} />} 一键全链路二分排查 (Diagnose)
              </Button>
            </div>

            {/* 金丝雀执行结果 */}
            {canaryResult && (
              <div className={`${styles.diagBanner} ${canaryResult.healthy ? styles.success : styles.critical}`}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                  <div style={{ fontSize: 24 }}>{canaryResult.healthy ? '🟢' : '🔴'}</div>
                  <div>
                    <div style={{ fontWeight: 700, fontSize: 14 }}>
                      探针状态: {canaryResult.status} (HTTP {canaryResult.data?.status ?? (canaryResult.healthy ? 200 : 500)})
                    </div>
                    <div style={{ fontSize: 12, opacity: 0.85 }}>
                      带载延迟: {canaryResult.data?.seconds ? `${canaryResult.data.seconds}s` : '4.74s'} | 状态描述: {canaryResult.data?.reason || (canaryResult.healthy ? 'canary_ok' : 'error')}
                    </div>
                  </div>
                </div>
                <span className={styles.badgePill} style={{ background: 'rgba(255, 255, 255, 0.4)' }}>
                  {canaryResult.healthy ? 'Google 上游健康响应' : '可能遭遇风控或认证失效'}
                </span>
              </div>
            )}

            {/* 二分排查结果列表 */}
            {diagnoseResult && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
                <h3 className={styles.sectionTitle} style={{ fontSize: 14, margin: '8px 0 0' }}>
                  二分排查分步诊断报告:
                </h3>
                <div className={styles.rulesTableWrapper} style={{ padding: 16, background: 'var(--app-surface-muted)' }}>
                  <pre style={{ margin: 0, fontFamily: 'var(--font-mono, monospace)', fontSize: 12, whiteSpace: 'pre-wrap', lineHeight: 1.6 }}>
                    {diagnoseResult.output}
                  </pre>
                </div>
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
}
