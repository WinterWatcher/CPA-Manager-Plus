import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { PriceControlPlaneSection } from './PriceControlPlaneSection';
import { usageServiceApi } from '@/services/api/usageService';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k: string) => k,
  }),
}));

vi.mock('@/services/api/usageService', () => ({
  usageServiceApi: {
    getModelPriceCandidates: vi.fn(),
    getFrozenModelPrices: vi.fn(),
    getModelPriceHistory: vi.fn(),
    getModelPriceAudit: vi.fn(),
    getModelPricePolicies: vi.fn(),
    exportModelPricePolicy: vi.fn(),
    revokeModelPricePolicy: vi.fn(),
    rollbackModelPricePolicy: vi.fn(),
    approveModelPriceCandidate: vi.fn(),
    rejectModelPriceCandidate: vi.fn(),
    freezeModelPrice: vi.fn(),
    unfreezeModelPrice: vi.fn(),
  },
}));

describe('PriceControlPlaneSection', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(usageServiceApi.getModelPriceCandidates).mockResolvedValue({
      candidates: [
        {
          id: 1,
          provider: 'openai',
          model: 'gpt-4o',
          prompt: 5.0,
          completion: 15.0,
          cache: 2.5,
          source: 'litellm',
          candidatePriceHash: 'hash-1',
          changeKind: 'free_to_paid',
          status: 'blocked',
          detectedAtMs: 1700000000000,
        },
        {
          id: 2,
          provider: 'anthropic',
          model: 'claude-3-5',
          prompt: 3.0,
          completion: 15.0,
          cache: 0.3,
          source: 'models.dev',
          candidatePriceHash: 'hash-2',
          changeKind: 'price_decrease',
          status: 'pending',
          detectedAtMs: 1700000000000,
        },
      ],
      total: 2,
    });
    vi.mocked(usageServiceApi.getFrozenModelPrices).mockResolvedValue({
      frozen: [
        {
          provider: 'openai',
          model: 'gpt-4o',
          isFrozen: true,
          frozenReason: 'critical free_to_paid drift',
          frozenAtMs: 1700000000000,
        },
      ],
    });
  });

  it('renders pending candidates with appropriate danger badges', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<PriceControlPlaneSection serviceBase="http://mock" />);
    });

    const text = JSON.stringify(renderer.toJSON());
    expect(text).toContain('gpt-4o');
    expect(text).toContain('claude-3-5');
    expect(text).toContain('Free→Paid');
    expect(text).toContain('Price Decrease');
  });

  it('approves a candidate on button click', async () => {
    vi.mocked(usageServiceApi.approveModelPriceCandidate).mockResolvedValue({
      id: 2,
      provider: 'anthropic',
      model: 'claude-3-5',
      prompt: 3.0,
      completion: 15.0,
      cache: 0.3,
      source: 'models.dev',
      candidatePriceHash: 'hash-2',
      changeKind: 'price_decrease',
      status: 'approved',
      detectedAtMs: 1700000000000,
    });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<PriceControlPlaneSection serviceBase="http://mock" />);
    });

    const buttons = renderer.root.findAll((node) => node.type === 'button' && node.children.includes('批准生效'));
    expect(buttons.length).toBe(2);

    await act(async () => {
      buttons[1].props.onClick();
    });

    expect(usageServiceApi.approveModelPriceCandidate).toHaveBeenCalledWith(
      'http://mock',
      2,
      expect.any(String),
      undefined
    );
  });

  it('switches to frozen tab and displays frozen models', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<PriceControlPlaneSection serviceBase="http://mock" />);
    });

    const frozenTabBtn = renderer.root.findAll(
      (node) => node.type === 'button' && typeof node.children[0] === 'string' && (node.children[0] as string).includes('冻结状态')
    )[0];
    expect(frozenTabBtn).toBeDefined();

    await act(async () => {
      frozenTabBtn.props.onClick();
    });

    const text = JSON.stringify(renderer.toJSON());
    expect(text).toContain('critical free_to_paid drift');
    expect(text).toContain('解除冻结');
  });

  it('switches to policies tab and displays policies with rollback and revoke buttons', async () => {
    vi.mocked(usageServiceApi.getModelPricePolicies).mockResolvedValue({
      policies: [
        {
          id: 10,
          snapshotId: 100,
          policyVersion: 'cpamp-openai-gpt-4o-r1',
          provider: 'openai',
          model: 'gpt-4o',
          upstreamModelHash: 'hash-abc',
          priceStatus: 'paid_confirmed',
          billingUnit: 'tokens',
          prompt: 2.5,
          completion: 10.0,
          cache: 1.25,
          unitPrice: 0,
          maxRequestCostUsd: 1.0,
          providerDailyBudgetUsd: 100,
          providerMonthlyBudgetUsd: 2000,
          globalDailyBudgetUsd: 500,
          globalMonthlyBudgetUsd: 10000,
          effectiveAtMs: 1700000000000,
          source: 'cpamp-approved',
          contentHash: 'sha256:11223344556677889900aabbccddeeff',
          status: 'active',
          createdAtMs: 1700000000000,
          createdBy: 'admin',
        },
      ],
      total: 1,
    });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<PriceControlPlaneSection serviceBase="http://mock" />);
    });

    const policyTabBtn = renderer.root.findAll(
      (node) => node.type === 'button' && typeof node.children[0] === 'string' && (node.children[0] as string).includes('策略快照与同步')
    )[0];
    expect(policyTabBtn).toBeDefined();

    await act(async () => {
      policyTabBtn.props.onClick();
    });

    const text = JSON.stringify(renderer.toJSON());
    expect(text).toContain('cpamp-openai-gpt-4o-r1');
    expect(text).toContain('sha256:11223344');
    expect(text).toContain('回滚');
    expect(text).toContain('撤销');

    // Test rollback action
    vi.mocked(usageServiceApi.rollbackModelPricePolicy).mockResolvedValue({ success: true, snapshot: {} });
    const rollbackBtn = renderer.root.findAll((node) => node.type === 'button' && node.children.includes('回滚'))[0];
    expect(rollbackBtn).toBeDefined();

    await act(async () => {
      rollbackBtn.props.onClick();
    });
    expect(usageServiceApi.rollbackModelPricePolicy).toHaveBeenCalledWith('http://mock', 10, 'rollback by admin', undefined);

    // Test revoke action
    vi.mocked(usageServiceApi.revokeModelPricePolicy).mockResolvedValue({ success: true });
    const revokeBtn = renderer.root.findAll((node) => node.type === 'button' && node.children.includes('撤销'))[0];
    expect(revokeBtn).toBeDefined();

    await act(async () => {
      revokeBtn.props.onClick();
    });
    expect(usageServiceApi.revokeModelPricePolicy).toHaveBeenCalledWith('http://mock', 10, 'revoked by admin', undefined);
  });
});
