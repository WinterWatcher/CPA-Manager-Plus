import { useState, useEffect, useCallback } from 'react';
import type React from 'react';
import { usageServiceApi } from '@/services/api/usageService';
import type {
  ModelPriceCandidate,
  ModelPriceFreezeStatus,
  ModelPriceSnapshot,
  ModelPriceAuditEvent,
} from '@/utils/usage';
import styles from './PriceControlPlaneSection.module.scss';

interface PriceControlPlaneSectionProps {
  serviceBase: string;
  managementKey?: string;
  onPriceChanged?: () => void;
}

export function PriceControlPlaneSection({
  serviceBase,
  managementKey,
  onPriceChanged,
}: PriceControlPlaneSectionProps) {
  const [candidates, setCandidates] = useState<ModelPriceCandidate[]>([]);
  const [frozenList, setFrozenList] = useState<ModelPriceFreezeStatus[]>([]);
  const [snapshots, setSnapshots] = useState<ModelPriceSnapshot[]>([]);
  const [auditEvents, setAuditEvents] = useState<ModelPriceAuditEvent[]>([]);
  const [activeTab, setActiveTab] = useState<'candidates' | 'frozen' | 'history' | 'audit'>('candidates');
  const [loading, setLoading] = useState(false);
  const [actionLoading, setActionLoading] = useState<number | string | null>(null);

  const [freezeProvider, setFreezeProvider] = useState('');
  const [freezeModel, setFreezeModel] = useState('');
  const [freezeReason, setFreezeReason] = useState('');

  const loadData = useCallback(async () => {
    if (!serviceBase) return;
    setLoading(true);
    try {
      const [candRes, frozenRes] = await Promise.all([
        usageServiceApi.getModelPriceCandidates(serviceBase, { limit: 100 }, managementKey),
        usageServiceApi.getFrozenModelPrices(serviceBase, managementKey),
      ]);
      setCandidates(candRes?.candidates || []);
      setFrozenList(frozenRes?.frozen || []);

      if (activeTab === 'history') {
        const histRes = await usageServiceApi.getModelPriceHistory(serviceBase, undefined, undefined, 50, 0, managementKey);
        setSnapshots(histRes?.snapshots || []);
      } else if (activeTab === 'audit') {
        const auditRes = await usageServiceApi.getModelPriceAudit(serviceBase, undefined, undefined, 50, 0, managementKey);
        setAuditEvents(auditRes?.events || []);
      }
    } catch {
      // ignore in UI, error shown via toast
    } finally {
      setLoading(false);
    }
  }, [serviceBase, managementKey, activeTab]);

  useEffect(() => {
    loadData();
  }, [loadData]);

  const handleApprove = async (id: number) => {
    setActionLoading(id);
    try {
      await usageServiceApi.approveModelPriceCandidate(serviceBase, id, 'approved by admin', managementKey);
      await loadData();
      onPriceChanged?.();
    } catch {
      // handled
    } finally {
      setActionLoading(null);
    }
  };

  const handleReject = async (id: number) => {
    setActionLoading(id);
    try {
      await usageServiceApi.rejectModelPriceCandidate(serviceBase, id, 'rejected by admin', managementKey);
      await loadData();
      onPriceChanged?.();
    } catch {
      // handled
    } finally {
      setActionLoading(null);
    }
  };

  const handleUnfreeze = async (provider: string, model: string) => {
    const key = `${provider}:${model}`;
    setActionLoading(key);
    try {
      await usageServiceApi.unfreezeModelPrice(serviceBase, provider, model, 'unfrozen by admin', managementKey);
      await loadData();
      onPriceChanged?.();
    } catch {
      // handled
    } finally {
      setActionLoading(null);
    }
  };

  const handleFreeze = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!freezeModel.trim()) return;
    setActionLoading('manual-freeze');
    try {
      await usageServiceApi.freezeModelPrice(serviceBase, freezeProvider.trim(), freezeModel.trim(), freezeReason.trim(), managementKey);
      setFreezeProvider('');
      setFreezeModel('');
      setFreezeReason('');
      await loadData();
      onPriceChanged?.();
    } catch {
      // handled
    } finally {
      setActionLoading(null);
    }
  };

  const formatDate = (ts?: number) => {
    if (!ts || ts <= 0) return '-';
    const d = new Date(ts);
    if (isNaN(d.getTime())) return '-';
    return d.toLocaleString();
  };

  const renderChangeKindBadge = (kind: string) => {
    switch (kind) {
      case 'free_to_paid':
        return <span className={`${styles.badge} ${styles.badgeCritical}`}>⚠️ 免费转付费 Free→Paid</span>;
      case 'unit_changed':
        return <span className={`${styles.badge} ${styles.badgeCritical}`}>⚠️ 计费单位变更 Unit Changed</span>;
      case 'model_remapped':
        return <span className={`${styles.badge} ${styles.badgeCritical}`}>⚠️ 映射变更 Model Remapped</span>;
      case 'price_increase':
        return <span className={`${styles.badge} ${styles.badgeWarning}`}>📈 价格上涨 Price Increase</span>;
      case 'price_decrease':
        return <span className={`${styles.badge} ${styles.badgeSuccess}`}>📉 价格下降 Price Decrease</span>;
      case 'paid_to_free':
        return <span className={`${styles.badge} ${styles.badgeSuccess}`}>🎁 转为免费 Paid→Free</span>;
      default:
        return <span className={`${styles.badge} ${styles.badgeNeutral}`}>{kind || 'Unchanged'}</span>;
    }
  };

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <div>
          <h2 className={styles.title}>价格安全控制面 (Price Control Plane)</h2>
          <p className={styles.subtitle}>
            实时监控价格变更，防止未经审批的涨价或计费变更直接生效。
          </p>
        </div>
        <button className={styles.refreshBtn} onClick={loadData} disabled={loading}>
          {loading ? '刷新中...' : '刷新状态'}
        </button>
      </div>

      <div className={styles.tabs}>
        <button
          className={`${styles.tab} ${activeTab === 'candidates' ? styles.tabActive : ''}`}
          onClick={() => setActiveTab('candidates')}
        >
          待审批候选 ({candidates.filter((c) => c.status === 'pending' || c.status === 'blocked').length})
        </button>
        <button
          className={`${styles.tab} ${activeTab === 'frozen' ? styles.tabActive : ''}`}
          onClick={() => setActiveTab('frozen')}
        >
          冻结状态 ({frozenList.length})
        </button>
        <button
          className={`${styles.tab} ${activeTab === 'history' ? styles.tabActive : ''}`}
          onClick={() => setActiveTab('history')}
        >
          价格快照与历史
        </button>
        <button
          className={`${styles.tab} ${activeTab === 'audit' ? styles.tabActive : ''}`}
          onClick={() => setActiveTab('audit')}
        >
          审批与变更审计
        </button>
      </div>

      {activeTab === 'candidates' && (
        <div className={styles.content}>
          {candidates.length === 0 ? (
            <div className={styles.empty}>暂无待审批的价格变更候选</div>
          ) : (
            <div className={styles.tableWrapper}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>供应商 (Provider)</th>
                    <th>模型 (Model)</th>
                    <th>变更类型 (Change Kind)</th>
                    <th>候选单价 (Candidate Price)</th>
                    <th>状态 (Status)</th>
                    <th>来源 (Source)</th>
                    <th>发现时间 (Detected)</th>
                    <th>操作 (Actions)</th>
                  </tr>
                </thead>
                <tbody>
                  {candidates.map((c) => (
                    <tr key={c.id}>
                      <td><strong>{c.provider || 'default'}</strong></td>
                      <td>{c.model}</td>
                      <td>{renderChangeKindBadge(c.changeKind)}</td>
                      <td>
                        Prompt: ${c.prompt}/1M<br />
                        Comp: ${c.completion}/1M
                        {c.unitPrice ? <div>Unit: ${c.unitPrice}</div> : null}
                      </td>
                      <td>
                        <span className={`${styles.statusBadge} ${c.status === 'blocked' ? styles.statusBlocked : styles.statusPending}`}>
                          {c.status}
                        </span>
                      </td>
                      <td>{c.source}</td>
                      <td>{formatDate(c.detectedAtMs)}</td>
                      <td>
                        {c.status === 'pending' || c.status === 'blocked' ? (
                          <div className={styles.btnGroup}>
                            <button
                              className={styles.approveBtn}
                              onClick={() => handleApprove(c.id)}
                              disabled={actionLoading === c.id}
                            >
                              批准生效
                            </button>
                            <button
                              className={styles.rejectBtn}
                              onClick={() => handleReject(c.id)}
                              disabled={actionLoading === c.id}
                            >
                              拒绝
                            </button>
                          </div>
                        ) : (
                          <span>已处理 ({c.status})</span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {activeTab === 'frozen' && (
        <div className={styles.content}>
          <form className={styles.freezeForm} onSubmit={handleFreeze}>
            <h3>手动冻结模型价格 (Manual Freeze)</h3>
            <div className={styles.formRow}>
              <input
                placeholder="供应商 (Provider)"
                value={freezeProvider}
                onChange={(e) => setFreezeProvider(e.target.value)}
              />
              <input
                placeholder="模型名称 (Model) *"
                value={freezeModel}
                onChange={(e) => setFreezeModel(e.target.value)}
                required
              />
              <input
                placeholder="冻结原因 (Reason)"
                value={freezeReason}
                onChange={(e) => setFreezeReason(e.target.value)}
              />
              <button type="submit" disabled={actionLoading === 'manual-freeze'}>
                冻结模型
              </button>
            </div>
          </form>

          {frozenList.length === 0 ? (
            <div className={styles.empty}>当前没有处于冻结状态的模型</div>
          ) : (
            <div className={styles.tableWrapper}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>供应商</th>
                    <th>模型</th>
                    <th>冻结原因</th>
                    <th>操作人</th>
                    <th>冻结时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {frozenList.map((f) => (
                    <tr key={`${f.provider}:${f.model}`}>
                      <td>{f.provider || 'default'}</td>
                      <td>{f.model}</td>
                      <td>{f.frozenReason || '系统安全阻断'}</td>
                      <td>{f.frozenBy || 'system'}</td>
                      <td>{formatDate(f.frozenAtMs)}</td>
                      <td>
                        <button
                          className={styles.unfreezeBtn}
                          onClick={() => handleUnfreeze(f.provider, f.model)}
                          disabled={actionLoading === `${f.provider}:${f.model}`}
                        >
                          解除冻结
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {activeTab === 'history' && (
        <div className={styles.content}>
          {snapshots.length === 0 ? (
            <div className={styles.empty}>暂无价格快照历史</div>
          ) : (
            <div className={styles.tableWrapper}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>快照 ID</th>
                    <th>供应商</th>
                    <th>模型</th>
                    <th>计费单位</th>
                    <th>价格</th>
                    <th>快照哈希</th>
                    <th>创建人</th>
                    <th>创建时间</th>
                  </tr>
                </thead>
                <tbody>
                  {snapshots.map((s) => (
                    <tr key={s.id}>
                      <td>#{s.id}</td>
                      <td>{s.provider || 'default'}</td>
                      <td>{s.model}</td>
                      <td>{s.billingUnit || 'tokens'}</td>
                      <td>${s.prompt} / ${s.completion}</td>
                      <td><code>{s.priceHash.slice(0, 12)}...</code></td>
                      <td>{s.createdBy || 'system'}</td>
                      <td>{formatDate(s.createdAtMs)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {activeTab === 'audit' && (
        <div className={styles.content}>
          {auditEvents.length === 0 ? (
            <div className={styles.empty}>暂无审计事件</div>
          ) : (
            <div className={styles.tableWrapper}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>动作 (Action)</th>
                    <th>供应商</th>
                    <th>模型</th>
                    <th>操作人</th>
                    <th>原因 / 备注</th>
                    <th>时间</th>
                  </tr>
                </thead>
                <tbody>
                  {auditEvents.map((a) => (
                    <tr key={a.id}>
                      <td><span className={styles.badge}>{a.action}</span></td>
                      <td>{a.provider || 'default'}</td>
                      <td>{a.model}</td>
                      <td>{a.operator}</td>
                      <td>{a.reason || '-'}</td>
                      <td>{formatDate(a.createdAtMs)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
