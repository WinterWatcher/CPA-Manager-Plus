import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card } from '@/components/ui/Card';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Select } from '@/components/ui/Select';
import { ToggleSwitch } from '@/components/ui/ToggleSwitch';
import { Modal } from '@/components/ui/Modal';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { IconRefreshCw, IconRotateCcw, IconShieldCheck, IconTriangleAlert } from '@/components/ui/icons';
import { useNotificationStore } from '@/stores';
import { proxyControlApi } from '@/services/api/proxyControl';
import type {
  HealthCheckResult,
  ProxyHistoryItem,
  ProxyNode,
  ProxyPolicy,
  ProxyStatus,
  SelectResult,
} from '@/types/proxyControl';
import styles from './ProxyManagementPage.module.scss';

export function ProxyManagementPage() {
  const { t } = useTranslation();
  const showNotification = useNotificationStore((state) => state.showNotification);

  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [checking, setChecking] = useState(false);

  const [status, setStatus] = useState<ProxyStatus | null>(null);
  const [nodes, setNodes] = useState<ProxyNode[]>([]);
  const [nodesTotal, setNodesTotal] = useState(0);
  const [policy, setPolicy] = useState<ProxyPolicy | null>(null);
  const [policyDraft, setPolicyDraft] = useState<ProxyPolicy | null>(null);
  const [policySaving, setPolicySaving] = useState(false);
  const [history, setHistory] = useState<ProxyHistoryItem[]>([]);

  // Filter & Search states
  const [searchQuery, setSearchQuery] = useState('');
  const [filterCountry, setFilterCountry] = useState('ALL');
  const [filterHealth, setFilterHealth] = useState('ALL');
  const [filterPreferredOnly, setFilterPreferredOnly] = useState(false);
  const [sortBy, setSortBy] = useState<'priority' | 'health' | 'latency' | 'last_checked'>('priority');

  // Modals
  const [selectedNodeForSwitch, setSelectedNodeForSwitch] = useState<ProxyNode | null>(null);
  const [switchConfirmed, setSwitchConfirmed] = useState(false);
  const [switching, setSwitching] = useState(false);

  const [dryRunResult, setDryRunResult] = useState<SelectResult | null>(null);
  const [dryRunModalOpen, setDryRunModalOpen] = useState(false);

  const [rollbackTargetId, setRollbackTargetId] = useState<string | null>(null);
  const [rollbackModalOpen, setRollbackModalOpen] = useState(false);
  const [rollingBack, setRollingBack] = useState(false);

  const loadData = useCallback(async () => {
    try {
      const [statusRes, nodesRes, policyRes, historyRes] = await Promise.all([
        proxyControlApi.getStatus().catch(() => null),
        proxyControlApi.getNodes().catch(() => null),
        proxyControlApi.getPolicy().catch(() => null),
        proxyControlApi.getHistory().catch(() => null),
      ]);

      if (statusRes) {
        setStatus(statusRes);
      } else {
        // Fallback status for unavailable controller
        setStatus({
          enabled: true,
          read_only: true,
          controller_available: false,
          check_stages: [],
        });
      }

      if (nodesRes) {
        setNodes(nodesRes.items);
        setNodesTotal(nodesRes.total);
      }
      if (policyRes) {
        setPolicy(policyRes);
        setPolicyDraft(policyRes);
      }
      if (historyRes) {
        setHistory(historyRes.items);
      }
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showNotification(msg, 'error');
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [showNotification]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleRefresh = useCallback(() => {
    setRefreshing(true);
    loadData();
  }, [loadData]);

  // Health check
  const handleHealthCheck = useCallback(async () => {
    setChecking(true);
    try {
      const res: HealthCheckResult = await proxyControlApi.healthCheck({ dry_run: true });
      if (status) {
        setStatus({
          ...status,
          check_stages: res.stages,
        });
      }
      showNotification(
        res.healthy
          ? t('proxy_management.switch_success', { defaultValue: '健康检查通过' })
          : t('proxy_management.stage_status_failed', { defaultValue: '健康检查未通过' }),
        res.healthy ? 'success' : 'warning'
      );
      // Reload history to capture recorded check
      proxyControlApi.getHistory().then((h) => setHistory(h.items)).catch(() => {});
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showNotification(msg, 'error');
    } finally {
      setChecking(false);
    }
  }, [showNotification, status, t]);

  // Dry run select
  const handleDryRunSelect = useCallback(async () => {
    try {
      const res = await proxyControlApi.dryRunSelect({ reason: 'manual_dry_run_analysis' });
      setDryRunResult(res);
      setDryRunModalOpen(true);
      proxyControlApi.getHistory().then((h) => setHistory(h.items)).catch(() => {});
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showNotification(msg, 'error');
    }
  }, [showNotification]);

  // Policy save
  const handleSavePolicy = useCallback(async () => {
    if (!policyDraft) return;
    setPolicySaving(true);
    try {
      const updated = await proxyControlApi.updatePolicy(policyDraft);
      setPolicy(updated);
      setPolicyDraft(updated);
      showNotification(t('proxy_management.policy_saved'), 'success');
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showNotification(msg, 'error');
    } finally {
      setPolicySaving(false);
    }
  }, [policyDraft, showNotification, t]);

  // Execute manual switch
  const handleExecuteSwitch = useCallback(async () => {
    if (!selectedNodeForSwitch || !switchConfirmed) return;
    setSwitching(true);
    try {
      const res = await proxyControlApi.select({
        node_id: selectedNodeForSwitch.id,
        reason: 'manual_admin_switch',
        confirm: true,
        dry_run: false,
      });

      if (res.success) {
        showNotification(t('proxy_management.switch_success'), 'success');
      } else if (res.rolled_back) {
        showNotification(t('proxy_management.switch_failed_rollback'), 'warning');
      }

      setSelectedNodeForSwitch(null);
      setSwitchConfirmed(false);
      loadData();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showNotification(msg, 'error');
    } finally {
      setSwitching(false);
    }
  }, [loadData, selectedNodeForSwitch, showNotification, switchConfirmed, t]);

  // Execute rollback
  const handleExecuteRollback = useCallback(async () => {
    if (!rollbackTargetId) return;
    setRollingBack(true);
    try {
      await proxyControlApi.rollback({
        rollback_id: rollbackTargetId,
        confirm: true,
      });
      showNotification(t('proxy_management.rollback_success'), 'success');
      setRollbackModalOpen(false);
      setRollbackTargetId(null);
      loadData();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : String(err);
      showNotification(msg, 'error');
    } finally {
      setRollingBack(false);
    }
  }, [loadData, rollbackTargetId, showNotification, t]);

  // Filtered & sorted nodes
  const filteredNodes = useMemo(() => {
    let result = [...nodes];
    if (filterCountry !== 'ALL') {
      result = result.filter((n) => n.country.toUpperCase() === filterCountry.toUpperCase());
    }
    if (filterHealth !== 'ALL') {
      result = result.filter((n) => n.health.toLowerCase() === filterHealth.toLowerCase());
    }
    if (filterPreferredOnly) {
      result = result.filter((n) => (n.preferred_level ?? 0) > 0);
    }
    if (searchQuery.trim()) {
      const q = searchQuery.toLowerCase().trim();
      result = result.filter(
        (n) => n.name.toLowerCase().includes(q) || n.id.toLowerCase().includes(q) || n.region.toLowerCase().includes(q)
      );
    }

    result.sort((a, b) => {
      if (a.selected !== b.selected) {
        return a.selected ? -1 : 1;
      }
      if (sortBy === 'priority') {
        return (b.preferred_level ?? 0) - (a.preferred_level ?? 0);
      }
      if (sortBy === 'latency') {
        const la = typeof a.latency_ms === 'number' && a.latency_ms > 0 ? a.latency_ms : 99999;
        const lb = typeof b.latency_ms === 'number' && b.latency_ms > 0 ? b.latency_ms : 99999;
        return la - lb;
      }
      if (sortBy === 'health') {
        return a.health.localeCompare(b.health);
      }
      if (sortBy === 'last_checked') {
        return (b.last_checked_at ?? '').localeCompare(a.last_checked_at ?? '');
      }
      return 0;
    });

    return result;
  }, [filterCountry, filterHealth, filterPreferredOnly, nodes, searchQuery, sortBy]);

  const uniqueCountries = useMemo(() => {
    const set = new Set<string>();
    nodes.forEach((n) => {
      if (n.country) set.add(n.country.toUpperCase());
    });
    return Array.from(set).sort();
  }, [nodes]);

  if (loading) {
    return (
      <div className={styles.container}>
        <LoadingSpinner />
      </div>
    );
  }

  const isReadOnly = Boolean(status?.read_only || !status?.controller_available);

  return (
    <div className={styles.container}>
      {/* Header */}
      <div className={styles.header}>
        <div className={styles.headerTitleGroup}>
          <div className={styles.titleRow}>
            <h1 className={styles.title}>{t('proxy_management.title')}</h1>
            {isReadOnly && (
              <span className={styles.readOnlyBadge}>
                {t('proxy_management.read_only_badge')}
              </span>
            )}
          </div>
          <p className={styles.subtitle}>{t('proxy_management.subtitle')}</p>
        </div>
        <div className={styles.headerActions}>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleHealthCheck}
            disabled={checking || isReadOnly}
          >
            {checking ? <LoadingSpinner size={16} /> : <IconShieldCheck size={16} />}
            <span>{t('proxy_management.btn_health_check')}</span>
          </Button>
          <Button
            variant="secondary"
            size="sm"
            onClick={handleDryRunSelect}
            disabled={isReadOnly}
          >
            <span>{t('proxy_management.btn_dry_run')}</span>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={handleRefresh}
            disabled={refreshing}
            aria-label="Refresh"
          >
            <IconRefreshCw size={16} className={refreshing ? 'animate-spin' : ''} />
          </Button>
        </div>
      </div>

      {/* Controller unavailable banner */}
      {!status?.controller_available && (
        <div className={styles.noticeBanner} role="alert">
          <IconTriangleAlert size={18} />
          <span>{t('proxy_management.controller_unavailable_notice')}</span>
        </div>
      )}

      {/* Status Cards */}
      <div className={styles.statusGrid}>
        {/* Current Node Card */}
        <Card>
          <div className={styles.cardHeader}>
            <h3 className={styles.cardTitle}>{t('proxy_management.current_node_title')}</h3>
            {status?.current_node && (
              <span className={`${styles.healthPill} ${styles[status.current_node.health] || styles.unknown}`}>
                {t(`proxy_management.health_${status.current_node.health}`, {
                  defaultValue: status.current_node.health,
                })}
              </span>
            )}
          </div>
          <div className={styles.statusPropsList}>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.current_node')}:</span>
              <span className={styles.propValue}>
                {status?.current_node ? status.current_node.name : '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.node_id')}:</span>
              <span className={styles.propValue}>
                {status?.current_node ? status.current_node.id : '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.country_region')}:</span>
              <span className={styles.propValue}>
                {status?.current_node ? `${status.current_node.country} / ${status.current_node.region}` : '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.latency')}:</span>
              <span className={styles.propValue}>
                {typeof status?.current_node?.latency_ms === 'number' && status.current_node.latency_ms > 0
                  ? `${status.current_node.latency_ms} ms`
                  : '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.egress_status')}:</span>
              <span className={styles.propValue}>
                {status?.egress?.ip_display || t('proxy_management.health_healthy')}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.status_reason')}:</span>
              <span className={styles.propValue}>
                {status?.selection?.last_reason || '—'}
              </span>
            </div>
          </div>
        </Card>

        {/* Selection Strategy Summary Card */}
        <Card>
          <div className={styles.cardHeader}>
            <h3 className={styles.cardTitle}>{t('proxy_management.selection_mode')}</h3>
            <span className={styles.propValue}>
              {status?.selection?.mode === 'auto'
                ? t('proxy_management.auto_mode')
                : t('proxy_management.manual_mode')}
            </span>
          </div>
          <div className={styles.statusPropsList}>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.auto_switch_label')}:</span>
              <span className={styles.propValue}>
                {policy?.auto_switch ? t('common.yes') : t('common.no')}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.dry_run_label')}:</span>
              <span className={styles.propValue}>
                {policy?.dry_run ? t('common.yes') : t('common.no')}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.allowed_countries')}:</span>
              <span className={styles.propValue}>
                {policy?.allowed_countries?.join(', ') || '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.failure_threshold')}:</span>
              <span className={styles.propValue}>
                {policy ? `${policy.failure_threshold} 次` : '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.health_check_rounds')}:</span>
              <span className={styles.propValue}>
                {policy ? `${policy.health_check_rounds} 轮` : '—'}
              </span>
            </div>
            <div className={styles.propRow}>
              <span className={styles.propLabel}>{t('proxy_management.cooldown_seconds')}:</span>
              <span className={styles.propValue}>
                {policy ? `${policy.cooldown_seconds} s` : '—'}
              </span>
            </div>
          </div>
        </Card>
      </div>

      {/* 6-step Health Check Stages */}
      {status?.check_stages && status.check_stages.length > 0 && (
        <Card>
          <h3 className={styles.cardTitle}>{t('proxy_management.stages_title')}</h3>
          <div className={styles.stagesContainer}>
            {status.check_stages.map((stg, idx) => (
              <div
                key={stg.stage}
                className={`${styles.stageCard} ${styles[stg.status] || ''}`}
              >
                <div className={styles.stageTop}>
                  <span className={styles.stageIndex}>0{idx + 1}</span>
                  <span className={`${styles.healthPill} ${styles[stg.status === 'passed' ? 'healthy' : stg.status === 'failed' ? 'failed' : 'unknown']}`}>
                    {t(`proxy_management.stage_status_${stg.status}`, { defaultValue: stg.status })}
                  </span>
                </div>
                <span className={styles.stageName}>
                  {t(`proxy_management.stage_${stg.stage}`, { defaultValue: stg.name })}
                </span>
                <div className={styles.stageMeta}>
                  {stg.http_status ? <span>HTTP {stg.http_status}</span> : null}
                  {stg.latency_ms ? <span>{stg.latency_ms}ms</span> : null}
                </div>
                {stg.error_code && (
                  <span className={styles.stageError}>
                    {stg.error_code}
                  </span>
                )}
              </div>
            ))}
          </div>
        </Card>
      )}

      {/* Candidate Nodes Table Card */}
      <Card>
        <div className={styles.cardHeader}>
          <h3 className={styles.cardTitle}>
            {t('proxy_management.candidate_nodes_title')} ({filteredNodes.length}/{nodesTotal})
          </h3>
        </div>

        {/* Toolbar */}
        <div className={styles.toolbar}>
          <div className={styles.filterGroup}>
            <Input
              placeholder={t('proxy_management.filter_search_placeholder')}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
            />
            <Select
              value={filterCountry}
              onChange={(val) => setFilterCountry(val)}
              options={[
                { value: 'ALL', label: t('proxy_management.filter_all_countries') },
                ...uniqueCountries.map((c) => ({ value: c, label: c })),
              ]}
              ariaLabel={t('proxy_management.filter_all_countries')}
            />
            <Select
              value={filterHealth}
              onChange={(val) => setFilterHealth(val)}
              options={[
                { value: 'ALL', label: t('proxy_management.filter_all_health') },
                { value: 'healthy', label: t('proxy_management.health_healthy') },
                { value: 'degraded', label: t('proxy_management.health_degraded') },
                { value: 'failed', label: t('proxy_management.health_failed') },
              ]}
              ariaLabel={t('proxy_management.filter_all_health')}
            />
            <Select
              value={sortBy}
              onChange={(val) => setSortBy(val as any)}
              options={[
                { value: 'priority', label: '按首选排序' },
                { value: 'latency', label: '按延迟排序' },
                { value: 'health', label: '按健康排序' },
                { value: 'last_checked', label: '按检测时间排序' },
              ]}
              ariaLabel="排序方式"
            />
          </div>
          <div>
            <label style={{ display: 'inline-flex', alignItems: 'center', gap: 6, fontSize: 13, cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={filterPreferredOnly}
                onChange={(e) => setFilterPreferredOnly(e.target.checked)}
              />
              {t('proxy_management.filter_preferred_only')}
            </label>
          </div>
        </div>

        {/* Table */}
        <div className={styles.tableWrapper}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>{t('proxy_management.col_name')}</th>
                <th>{t('proxy_management.col_country')}</th>
                <th>{t('proxy_management.col_region')}</th>
                <th>{t('proxy_management.col_health')}</th>
                <th>{t('proxy_management.col_latency')}</th>
                <th>{t('proxy_management.col_preferred')}</th>
                <th>{t('proxy_management.col_failures')}</th>
                <th>{t('proxy_management.col_actions')}</th>
              </tr>
            </thead>
            <tbody>
              {filteredNodes.map((n) => (
                <tr key={n.id} className={n.selected ? styles.current : ''}>
                  <td>
                    <div className={styles.nodeNameCol}>
                      <span>{n.name}</span>
                      {n.selected && (
                        <span className={styles.currentBadge}>
                          {t('proxy_management.current_badge')}
                        </span>
                      )}
                      {(n.preferred_level ?? 0) > 0 && (
                        <span className={styles.preferredBadge}>
                          {t('proxy_management.preferred_badge')} L{n.preferred_level}
                        </span>
                      )}
                    </div>
                  </td>
                  <td>{n.country}</td>
                  <td>{n.region}</td>
                  <td>
                    <span className={`${styles.healthPill} ${styles[n.health] || styles.unknown}`}>
                      {t(`proxy_management.health_${n.health}`, { defaultValue: n.health })}
                    </span>
                  </td>
                  <td>{typeof n.latency_ms === 'number' && n.latency_ms > 0 ? `${n.latency_ms} ms` : '—'}</td>
                  <td>{n.preferred_level ?? 0}</td>
                  <td>{n.consecutive_failures}</td>
                  <td>
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={n.selected || isReadOnly || !n.switchable}
                      onClick={() => {
                        setSelectedNodeForSwitch(n);
                        setSwitchConfirmed(false);
                      }}
                    >
                      {t('proxy_management.btn_switch_node')}
                    </Button>
                  </td>
                </tr>
              ))}
              {filteredNodes.length === 0 && (
                <tr>
                  <td colSpan={8} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-tertiary)' }}>
                    {t('proxy_management.no_nodes_found', { defaultValue: '暂无符合条件的候选节点' })}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </Card>

      {/* Auto-selection Policy Form Card */}
      {policyDraft && (
        <Card>
          <div className={styles.cardHeader}>
            <h3 className={styles.cardTitle}>{t('proxy_management.policy_title')}</h3>
            <Button
              variant="primary"
              size="sm"
              onClick={handleSavePolicy}
              disabled={policySaving || isReadOnly}
            >
              {policySaving ? <LoadingSpinner size={16} /> : t('proxy_management.save_policy')}
            </Button>
          </div>

          <div className={styles.policyGrid}>
            <div className={styles.policyRow}>
              <div className={styles.toggleRow}>
                <div className={styles.toggleInfo}>
                  <span className={styles.toggleTitle}>{t('proxy_management.auto_switch')}</span>
                  <span className={styles.toggleDesc}>{t('proxy_management.auto_switch_hint')}</span>
                </div>
                <ToggleSwitch
                  checked={policyDraft.auto_switch}
                  onChange={(val) => setPolicyDraft({ ...policyDraft, auto_switch: val })}
                  disabled={isReadOnly}
                  ariaLabel={t('proxy_management.auto_switch')}
                />
              </div>
            </div>

            <div className={styles.policyRow}>
              <div className={styles.toggleRow}>
                <div className={styles.toggleInfo}>
                  <span className={styles.toggleTitle}>{t('proxy_management.dry_run_mode')}</span>
                  <span className={styles.toggleDesc}>{t('proxy_management.dry_run_hint')}</span>
                </div>
                <ToggleSwitch
                  checked={policyDraft.dry_run}
                  onChange={(val) => setPolicyDraft({ ...policyDraft, dry_run: val })}
                  disabled={isReadOnly}
                  ariaLabel={t('proxy_management.dry_run_mode')}
                />
              </div>
            </div>

            <div className={styles.policyRow}>
              <Input
                label={t('proxy_management.allowed_countries')}
                hint={t('proxy_management.allowed_countries_hint')}
                value={policyDraft.allowed_countries.join(', ')}
                onChange={(e) =>
                  setPolicyDraft({
                    ...policyDraft,
                    allowed_countries: e.target.value
                      .split(/[\s,]+/)
                      .map((s) => s.trim().toUpperCase())
                      .filter(Boolean),
                  })
                }
                disabled={isReadOnly}
              />
            </div>

            <div className={styles.policyRow}>
              <Input
                label={t('proxy_management.preferred_nodes')}
                hint={t('proxy_management.preferred_nodes_hint')}
                value={policyDraft.preferred_nodes.join(', ')}
                onChange={(e) =>
                  setPolicyDraft({
                    ...policyDraft,
                    preferred_nodes: e.target.value
                      .split(/[\s,]+/)
                      .map((s) => s.trim())
                      .filter(Boolean),
                  })
                }
                disabled={isReadOnly}
              />
            </div>

            <div className={styles.policyRow}>
              <Input
                type="number"
                label={t('proxy_management.probe_concurrency')}
                value={policyDraft.probe_concurrency}
                onChange={(e) =>
                  setPolicyDraft({ ...policyDraft, probe_concurrency: parseInt(e.target.value, 10) || 1 })
                }
                disabled={isReadOnly}
              />
            </div>

            <div className={styles.policyRow}>
              <Input
                type="number"
                label={t('proxy_management.probe_timeout')}
                value={policyDraft.probe_timeout_seconds}
                onChange={(e) =>
                  setPolicyDraft({
                    ...policyDraft,
                    probe_timeout_seconds: parseInt(e.target.value, 10) || 10,
                  })
                }
                disabled={isReadOnly}
              />
            </div>

            <div className={styles.policyRow}>
              <Input
                type="number"
                label={t('proxy_management.failure_threshold')}
                value={policyDraft.failure_threshold}
                onChange={(e) =>
                  setPolicyDraft({
                    ...policyDraft,
                    failure_threshold: parseInt(e.target.value, 10) || 3,
                  })
                }
                disabled={isReadOnly}
              />
            </div>

            <div className={styles.policyRow}>
              <Input
                type="number"
                label={t('proxy_management.health_check_rounds')}
                value={policyDraft.health_check_rounds}
                onChange={(e) =>
                  setPolicyDraft({
                    ...policyDraft,
                    health_check_rounds: parseInt(e.target.value, 10) || 3,
                  })
                }
                disabled={isReadOnly}
              />
            </div>

            <div className={styles.policyRow}>
              <Input
                type="number"
                label={t('proxy_management.cooldown_seconds')}
                value={policyDraft.cooldown_seconds}
                onChange={(e) =>
                  setPolicyDraft({
                    ...policyDraft,
                    cooldown_seconds: parseInt(e.target.value, 10) || 300,
                  })
                }
                disabled={isReadOnly}
              />
            </div>
          </div>

          <div className={styles.safetyAlert}>
            {t('proxy_management.safety_warning')}
          </div>
        </Card>
      )}

      {/* History Card */}
      <Card>
        <div className={styles.cardHeader}>
          <h3 className={styles.cardTitle}>{t('proxy_management.history_title')}</h3>
        </div>
        <div className={styles.tableWrapper}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>{t('proxy_management.col_time')}</th>
                <th>{t('proxy_management.col_op')}</th>
                <th>{t('proxy_management.col_source')}</th>
                <th>{t('proxy_management.col_target')}</th>
                <th>{t('proxy_management.col_reason')}</th>
                <th>{t('proxy_management.col_dry_run')}</th>
                <th>{t('proxy_management.col_result')}</th>
                <th>{t('proxy_management.col_operator')}</th>
                <th>{t('proxy_management.col_actions')}</th>
              </tr>
            </thead>
            <tbody>
              {history.map((h) => (
                <tr key={h.id}>
                  <td>{h.timestamp}</td>
                  <td><strong>{h.operation}</strong></td>
                  <td>{h.source_node_name ? `${h.source_node_name} (${h.source_node_id})` : '—'}</td>
                  <td>{h.target_node_name ? `${h.target_node_name} (${h.target_node_id})` : '—'}</td>
                  <td>{h.trigger_reason || '—'}</td>
                  <td>{h.dry_run ? t('common.yes') : t('common.no')}</td>
                  <td>
                    <span
                      className={`${styles.healthPill} ${
                        h.final_result === 'success'
                          ? styles.healthy
                          : h.final_result === 'rolled_back'
                          ? styles.rollback
                          : styles.failed
                      }`}
                    >
                      {h.final_result}
                    </span>
                  </td>
                  <td>{h.operator}</td>
                  <td>
                    {(h.operation === 'manual_select' || h.operation === 'automatic_select') && !h.rolled_back && !isReadOnly && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          setRollbackTargetId(h.id);
                          setRollbackModalOpen(true);
                        }}
                        title={t('proxy_management.btn_rollback')}
                      >
                        <IconRotateCcw size={14} />
                        <span>{t('proxy_management.btn_rollback')}</span>
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
              {history.length === 0 && (
                <tr>
                  <td colSpan={9} style={{ textAlign: 'center', padding: '24px', color: 'var(--text-tertiary)' }}>
                    暂无操作记录
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </Card>

      {/* Switch Confirmation Modal */}
      <Modal
        open={Boolean(selectedNodeForSwitch)}
        onClose={() => {
          setSelectedNodeForSwitch(null);
          setSwitchConfirmed(false);
        }}
        title={t('proxy_management.modal_switch_title')}
        footer={
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Button
              variant="secondary"
              onClick={() => {
                setSelectedNodeForSwitch(null);
                setSwitchConfirmed(false);
              }}
            >
              {t('proxy_management.modal_cancel')}
            </Button>
            <Button
              variant="danger"
              disabled={!switchConfirmed || switching}
              onClick={handleExecuteSwitch}
            >
              {switching ? <LoadingSpinner size={16} /> : t('proxy_management.modal_confirm_btn')}
            </Button>
          </div>
        }
      >
        <div className={styles.modalContent}>
          <p>
            {t('proxy_management.modal_switch_desc', {
              name: selectedNodeForSwitch?.name,
              id: selectedNodeForSwitch?.id,
            })}
          </p>
          <div className={styles.modalSection}>
            <div><strong>国家/地区:</strong> {selectedNodeForSwitch?.country} / {selectedNodeForSwitch?.region}</div>
            <div><strong>当前健康:</strong> {selectedNodeForSwitch?.health}</div>
            <div><strong>延迟:</strong> {typeof selectedNodeForSwitch?.latency_ms === 'number' && selectedNodeForSwitch.latency_ms > 0 ? `${selectedNodeForSwitch.latency_ms} ms` : '—'}</div>
          </div>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13 }}>
            <input
              type="checkbox"
              checked={switchConfirmed}
              onChange={(e) => setSwitchConfirmed(e.target.checked)}
            />
            <span>{t('proxy_management.modal_confirm_check')}</span>
          </label>
        </div>
      </Modal>

      {/* Dry Run Result Modal */}
      <Modal
        open={dryRunModalOpen}
        onClose={() => setDryRunModalOpen(false)}
        title={t('proxy_management.modal_dry_run_title')}
        footer={
          <Button variant="primary" onClick={() => setDryRunModalOpen(false)}>
            {t('common.close')}
          </Button>
        }
      >
        <div className={styles.modalContent}>
          <p>{t('proxy_management.modal_dry_run_desc')}</p>
          {dryRunResult?.recommended_node ? (
            <div className={styles.modalSection}>
              <div>
                <strong>{t('proxy_management.recommended_node_label')}:</strong>{' '}
                {dryRunResult.recommended_node.name} ({dryRunResult.recommended_node.id})
              </div>
              <div>
                <strong>{t('proxy_management.latency')}:</strong>{' '}
                {typeof dryRunResult.recommended_node.latency_ms === 'number' && dryRunResult.recommended_node.latency_ms > 0
                  ? `${dryRunResult.recommended_node.latency_ms} ms`
                  : '—'}
              </div>
              <div>
                <strong>{t('proxy_management.would_switch_label')}:</strong>{' '}
                {dryRunResult.would_switch ? t('common.yes') : t('common.no')}
              </div>
              <div>
                <strong>{t('proxy_management.switch_reason_label')}:</strong> {dryRunResult.reason}
              </div>
            </div>
          ) : (
            <p>未找到推荐节点</p>
          )}
        </div>
      </Modal>

      {/* Rollback Confirmation Modal */}
      <Modal
        open={rollbackModalOpen}
        onClose={() => {
          setRollbackModalOpen(false);
          setRollbackTargetId(null);
        }}
        title={t('proxy_management.modal_rollback_title')}
        footer={
          <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
            <Button
              variant="secondary"
              onClick={() => {
                setRollbackModalOpen(false);
                setRollbackTargetId(null);
              }}
            >
              {t('proxy_management.modal_cancel')}
            </Button>
            <Button
              variant="danger"
              disabled={rollingBack}
              onClick={handleExecuteRollback}
            >
              {rollingBack ? <LoadingSpinner size={16} /> : t('proxy_management.modal_rollback_btn')}
            </Button>
          </div>
        }
      >
        <div className={styles.modalContent}>
          <p>{t('proxy_management.modal_rollback_desc')}</p>
        </div>
      </Modal>
    </div>
  );
}
