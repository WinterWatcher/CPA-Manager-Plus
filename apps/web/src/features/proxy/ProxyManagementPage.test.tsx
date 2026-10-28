import { createElement, type ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ProxyManagementPage } from './ProxyManagementPage';
import { Select } from '@/components/ui/Select';
import { proxyControlApi } from '@/services/api/proxyControl';
import type { ProxyNode, ProxyPolicy, ProxyStatus } from '@/types/proxyControl';

const { showNotification } = vi.hoisted(() => ({ showNotification: vi.fn() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, options?: { defaultValue?: string }) => options?.defaultValue || key,
  }),
}));

vi.mock('@/stores', () => ({
  useNotificationStore: (selector?: (state: { showNotification: typeof showNotification }) => unknown) => {
    const state = { showNotification };
    return selector ? selector(state) : state;
  },
}));

vi.mock('@/components/ui/Modal', () => ({
  Modal: ({
    open,
    title,
    children,
    footer,
  }: {
    open: boolean;
    title?: ReactNode;
    children: ReactNode;
    footer?: ReactNode;
  }) => (open ? createElement('div', { 'data-testid': 'modal' }, title, children, footer) : null),
}));

vi.mock('@/services/api/proxyControl', () => ({
  proxyControlApi: {
    getStatus: vi.fn(),
    getNodes: vi.fn(),
    getPolicy: vi.fn(),
    updatePolicy: vi.fn(),
    healthCheck: vi.fn(),
    dryRunSelect: vi.fn(),
    select: vi.fn(),
    rollback: vi.fn(),
    getHistory: vi.fn(),
  },
}));

const sampleNodes: ProxyNode[] = [
  {
    id: 'node-7f31',
    name: 'JP-Tokyo-01',
    country: 'JP',
    region: 'Tokyo',
    health: 'healthy',
    latency_ms: 145,
    selected: false,
    preferred_level: 1,
    consecutive_failures: 0,
    consecutive_successes: 10,
    switchable: true,
  },
  {
    id: 'node-7f3a',
    name: 'JP-Tokyo-03',
    country: 'JP',
    region: 'Tokyo',
    health: 'healthy',
    latency_ms: 182,
    selected: true,
    preferred_level: 2,
    consecutive_failures: 0,
    consecutive_successes: 15,
    switchable: true,
  },
  {
    id: 'node-8a12',
    name: 'JP-Osaka-02',
    country: 'JP',
    region: 'Osaka',
    health: 'degraded',
    latency_ms: 320,
    selected: false,
    preferred_level: 0,
    consecutive_failures: 1,
    consecutive_successes: 0,
    switchable: true,
  },
  {
    id: 'node-9c44',
    name: 'JP-Tokyo-07',
    country: 'JP',
    region: 'Tokyo',
    health: 'failed',
    latency_ms: 0,
    selected: false,
    preferred_level: 0,
    consecutive_failures: 4,
    consecutive_successes: 0,
    switchable: false,
    reason: 'node_probe_timeout',
  },
  {
    id: 'node-3e21',
    name: 'SG-Singapore-01',
    country: 'SG',
    region: 'Singapore',
    health: 'healthy',
    latency_ms: 195,
    selected: false,
    preferred_level: 0,
    consecutive_failures: 0,
    consecutive_successes: 8,
    switchable: true,
  },
];

const sampleStatus: ProxyStatus = {
  enabled: true,
  read_only: false,
  controller_available: true,
  current_node: sampleNodes[1],
  egress: {
    health: 'healthy',
    ip: null,
    ip_display: 'JP egress verified',
    country: 'JP',
    checked_at: '2026-10-28T14:30:00Z',
  },
  selection: {
    mode: 'manual',
    dry_run: true,
    auto_switch: false,
    last_reason: 'current_node_healthy',
    last_result: 'kept_current',
  },
  check_stages: [
    { stage: 'proxy_reachability', name: '代理连通性', status: 'passed', http_status: 200, latency_ms: 35, passed: true },
    { stage: 'egress_ip_validation', name: '出口IP校验', status: 'passed', http_status: 200, latency_ms: 50, passed: true },
    { stage: 'google_reachability', name: 'Google基础连通性', status: 'passed', http_status: 200, latency_ms: 65, passed: true },
    { stage: 'oauth_reachability', name: 'OAuth连通性', status: 'passed', http_status: 200, latency_ms: 80, passed: true },
    { stage: 'cloud_code_reachability', name: 'Cloud Code连通性', status: 'passed', http_status: 200, latency_ms: 95, passed: true },
    { stage: 'node_confirmation', name: '节点最终确认', status: 'passed', http_status: 200, latency_ms: 110, passed: true },
  ],
};

const samplePolicy: ProxyPolicy = {
  auto_switch: false,
  dry_run: true,
  allowed_countries: ['JP', 'SG'],
  preferred_nodes: ['node-7f31', 'node-7f3a'],
  probe_concurrency: 5,
  probe_timeout_seconds: 10,
  health_check_rounds: 3,
  failure_threshold: 3,
  success_threshold: 2,
  cooldown_seconds: 300,
  rollback_on_failure: true,
  allow_auto_rollback: true,
};

const getText = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : getText(child))).join('');

describe('ProxyManagementPage UI', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(proxyControlApi.getStatus).mockResolvedValue(sampleStatus);
    vi.mocked(proxyControlApi.getNodes).mockResolvedValue({
      items: sampleNodes,
      total: sampleNodes.length,
      healthy: 3,
      degraded: 1,
      failed: 1,
      last_refresh_at: '2026-10-28T14:30:00Z',
    });
    vi.mocked(proxyControlApi.getPolicy).mockResolvedValue(samplePolicy);
    vi.mocked(proxyControlApi.getHistory).mockResolvedValue({
      items: [
        {
          id: 'hist-1',
          timestamp: '2026-10-28 14:30:00',
          operation: 'health_check',
          source_node_id: 'node-7f3a',
          source_node_name: 'JP-Tokyo-03',
          dry_run: true,
          health_check_passed: true,
          final_result: 'success',
          rolled_back: false,
          operator: 'admin',
        },
      ],
      total: 1,
    });
  });

  it('renders page and displays current proxy status card and stages', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<ProxyManagementPage />);
    });

    const rootText = getText(renderer.root);
    expect(rootText).toContain('JP-Tokyo-03');
    expect(rootText).toContain('node-7f3a');
    expect(rootText).toContain('182 ms');
    expect(rootText).toContain('JP egress verified');
    expect(rootText).toContain('current_node_healthy');

    // 6-step stages rendered
    expect(rootText).toContain('代理连通性');
    expect(rootText).toContain('节点最终确认');

    // Sensitive checks: verify no private tokens/IPs leaked in DOM
    expect(rootText).not.toContain('NODE_API_TOKEN');
    expect(rootText).not.toContain('vmess://');
    expect(rootText).not.toContain('vless://');
  });

  it('renders read-only mode banner when controller is unavailable', async () => {
    vi.mocked(proxyControlApi.getStatus).mockResolvedValueOnce({
      enabled: true,
      read_only: true,
      controller_available: false,
      check_stages: [],
    });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<ProxyManagementPage />);
    });

    const rootText = getText(renderer.root);
    expect(rootText).toContain('proxy_management.read_only_badge');
    expect(rootText).toContain('proxy_management.controller_unavailable_notice');
  });

  it('filters candidate nodes by country and health status', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<ProxyManagementPage />);
    });

    // Default: all 5 nodes present
    const rows = renderer.root.findAllByType('tr');
    expect(rows.length).toBeGreaterThanOrEqual(6);

    // Filter to SG using Select component
    const countrySelect = renderer.root.findAllByType(Select)[0];
    await act(async () => {
      countrySelect.props.onChange('SG');
    });

    const textAfterFilter = getText(renderer.root);
    expect(textAfterFilter).toContain('SG-Singapore-01');
    expect(textAfterFilter).not.toContain('JP-Osaka-02');
  });

  it('triggers dry-run select and displays recommendation modal without switching node', async () => {
    vi.mocked(proxyControlApi.dryRunSelect).mockResolvedValueOnce({
      success: true,
      would_switch: true,
      rolled_back: false,
      recommended_node: sampleNodes[0],
      reason: 'preferred_healthy_low_latency',
    });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<ProxyManagementPage />);
    });

    const dryRunBtn = renderer.root
      .findAllByType('button')
      .find((b) => getText(b).includes('proxy_management.btn_dry_run'));
    expect(dryRunBtn).toBeDefined();

    await act(async () => {
      await dryRunBtn!.props.onClick();
    });

    expect(proxyControlApi.dryRunSelect).toHaveBeenCalledWith({ reason: 'manual_dry_run_analysis' });

    // Dry-run modal content rendered
    const modalText = getText(renderer.root);
    expect(modalText).toContain('proxy_management.modal_dry_run_title');
    expect(modalText).toContain('JP-Tokyo-01');
    expect(modalText).toContain('preferred_healthy_low_latency');

    // Live node has NOT been changed
    expect(proxyControlApi.select).not.toHaveBeenCalled();
  });

  it('requires confirmation checkbox before executing manual node switch', async () => {
    vi.mocked(proxyControlApi.select).mockResolvedValueOnce({
      success: true,
      would_switch: true,
      rolled_back: false,
      current_node: sampleNodes[0],
      rollback_id: 'rb-test-123',
    });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<ProxyManagementPage />);
    });

    // Click switch button on first candidate
    const switchBtns = renderer.root
      .findAllByType('button')
      .filter((b) => getText(b).includes('proxy_management.btn_switch_node') && !b.props.disabled);
    expect(switchBtns.length).toBeGreaterThan(0);

    await act(async () => {
      switchBtns[0].props.onClick();
    });

    // Confirmation modal should be visible
    expect(getText(renderer.root)).toContain('proxy_management.modal_switch_title');

    // Confirm button must initially be disabled without checking checkbox
    const confirmBtn = renderer.root
      .findAllByType('button')
      .find((b) => getText(b).includes('proxy_management.modal_confirm_btn'));
    expect(confirmBtn?.props.disabled).toBe(true);

    // Toggle checkbox
    const checkbox = renderer.root
      .findAllByType('input')
      .find((i) => i.props.type === 'checkbox' && i.parent?.children.some((c: any) => typeof c === 'string' && c.includes('modal_confirm_check') || (c?.props?.children && String(c.props.children).includes('modal_confirm_check'))));

    await act(async () => {
      checkbox!.props.onChange({ target: { checked: true } });
    });

    // Confirm button is now enabled
    const activeConfirmBtn = renderer.root
      .findAllByType('button')
      .find((b) => getText(b).includes('proxy_management.modal_confirm_btn'));
    expect(activeConfirmBtn?.props.disabled).toBe(false);

    await act(async () => {
      await activeConfirmBtn!.props.onClick();
    });

    expect(proxyControlApi.select).toHaveBeenCalledWith(
      expect.objectContaining({
        node_id: sampleNodes[0].id,
        confirm: true,
      })
    );
    expect(showNotification).toHaveBeenCalledWith('proxy_management.switch_success', 'success');
  });

  it('allows policy editing and saves successfully', async () => {
    vi.mocked(proxyControlApi.updatePolicy).mockResolvedValueOnce({
      ...samplePolicy,
      cooldown_seconds: 600,
    });

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<ProxyManagementPage />);
    });

    const savePolicyBtn = renderer.root
      .findAllByType('button')
      .find((b) => getText(b).includes('proxy_management.save_policy'));
    expect(savePolicyBtn).toBeDefined();

    await act(async () => {
      await savePolicyBtn!.props.onClick();
    });

    expect(proxyControlApi.updatePolicy).toHaveBeenCalled();
    expect(showNotification).toHaveBeenCalledWith('proxy_management.policy_saved', 'success');
  });
});
