import { createElement, type ReactNode } from 'react';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { OpenAIProviderConfig } from '@/types';
import { buildProviderRows, type ProviderRow } from '@/components/providers/ProviderTable/rowData';
import { ProviderTable } from '@/components/providers/ProviderTable/ProviderTable';
import { ProviderDetailDrawer } from '@/components/providers/ProviderDetailDrawer/ProviderDetailDrawer';
import { OpenAIEditDrawer } from '@/components/providers/ProviderEditDrawer/OpenAIEditDrawer';

const authState = vi.hoisted(() => ({
  serverVersion: 'v7.2.93' as string | null,
  serverCommit: null as string | null,
}));

vi.mock('@/stores/useAuthStore', () => ({
  useAuthStore: (selector: (state: typeof authState) => unknown) => selector(authState),
}));

const mocks = vi.hoisted(() => ({
  getOpenAIProviders: vi.fn(),
  updateOpenAIProvider: vi.fn(),
  createOpenAIProvider: vi.fn(),
  showNotification: vi.fn(),
  updateConfigValue: vi.fn(),
  apiCallRequest: vi.fn(),
}));

vi.mock('@/stores', () => ({
  useConfigStore: (selector: (state: unknown) => unknown) =>
    selector({
      config: { openaiCompatibility: [] },
      updateConfigValue: mocks.updateConfigValue,
    }),
  useNotificationStore: () => ({ showNotification: mocks.showNotification }),
}));

vi.mock('@/components/ui/Drawer', () => ({
  Drawer: ({
    open,
    title,
    children,
    footer,
  }: {
    open: boolean;
    title?: ReactNode;
    children: ReactNode;
    footer?: ReactNode;
  }) => (open ? createElement('div', { 'data-testid': 'drawer' }, title, children, footer) : null),
}));

vi.mock('@/components/ui/Modal', () => ({
  Modal: ({ open, children }: { open: boolean; children: ReactNode }) =>
    open ? createElement('div', null, children) : null,
}));

vi.mock('@/services/api', () => ({
  apiCallApi: { request: mocks.apiCallRequest },
  getApiCallErrorDetails: vi.fn(() => ''),
  modelsApi: { fetchModelsViaApiCall: vi.fn() },
  providersApi: {
    createOpenAIProvider: mocks.createOpenAIProvider,
    getOpenAIProviders: mocks.getOpenAIProviders,
    updateOpenAIProvider: mocks.updateOpenAIProvider,
  },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const getText = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : getText(child))).join('');

const buildOpenAIRow = (provider: OpenAIProviderConfig) =>
  buildProviderRows({
    gemini: [],
    codex: [],
    claude: [],
    vertex: [],
    openai: [provider],
    usageByProvider: new Map(),
  })[0];

const renderDetailText = (row: ProviderRow): string => {
  let renderer!: ReactTestRenderer;
  act(() => {
    renderer = create(
      <ProviderDetailDrawer
        row={row}
        open
        usageByProvider={new Map()}
        resolvedTheme="light"
        actionsDisabled={false}
        toggleDisabled={false}
        onClose={() => {}}
        onEdit={() => {}}
        onDelete={() => {}}
        onToggle={() => {}}
        onToggleWebsockets={() => {}}
        onToggleCloak={() => {}}
        onToggleDisableCooling={() => {}}
      />
    );
  });
  const text = getText(renderer.root);
  act(() => renderer.unmount());
  return text;
};

const findSaveButton = (root: ReactTestInstance) =>
  root
    .findAllByType('button')
    .filter((button) => String(button.props.className ?? '').includes('btn-primary'))
    .slice(-1)[0];

describe('Provider capability management requirements', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    authState.serverVersion = 'v7.2.93';
    mocks.updateOpenAIProvider.mockResolvedValue(undefined);
    mocks.createOpenAIProvider.mockResolvedValue(undefined);
    mocks.apiCallRequest.mockResolvedValue({ statusCode: 200, body: '{}' });
  });

  // 1. 旧Provider无能力字段
  it('handles legacy provider without capability fields normally', () => {
    const legacyProvider: OpenAIProviderConfig = {
      name: 'legacy-openai',
      baseUrl: 'https://api.openai.com/v1',
      apiKeyEntries: [{ apiKey: 'sk-legacy1234567890' }],
      models: [{ name: 'gpt-4o' }],
    };
    const row = buildOpenAIRow(legacyProvider);

    expect(row.capability).toBe('chat');
    expect(row.billingUnit).toBe('tokens');
    expect(row.isAsync).toBe(false);

    let tableRenderer!: ReactTestRenderer;
    act(() => {
      tableRenderer = create(
        <ProviderTable
          rows={[row]}
          loading={false}
          actionsDisabled={false}
          toggleDisabled={false}
          resolvedTheme="light"
          emptyState={<div>empty</div>}
          onShowDetail={() => {}}
          onEdit={() => {}}
          onDelete={() => {}}
          onToggle={() => {}}
          onPriorityChange={() => {}}
        />
      );
    });
    const tableText = getText(tableRenderer.root);
    // Legacy chat has no capability badge
    expect(tableRenderer.root.findAllByProps({ 'data-testid': 'provider-capability-badge' })).toHaveLength(0);
    expect(tableText).toContain('legacy-openai');
    act(() => tableRenderer.unmount());

    const detailText = renderDetailText(row);
    expect(detailText).toContain('legacy-openai');
    expect(detailText).toContain(i18n.t('ai_providers.capability_chat'));
  });

  // 2. Provider显示能力
  it('displays non-chat capability on table badge and detail drawer', () => {
    const asrProvider: OpenAIProviderConfig = {
      name: 'whisper-service',
      baseUrl: 'https://whisper.example.com/v1',
      apiKeyEntries: [{ apiKey: 'sk-whisper12345678' }],
      capability: 'asr',
      billingUnit: 'audio_seconds',
      unitPrice: 0.006,
    };
    const row = buildOpenAIRow(asrProvider);

    let tableRenderer!: ReactTestRenderer;
    act(() => {
      tableRenderer = create(
        <ProviderTable
          rows={[row]}
          loading={false}
          actionsDisabled={false}
          toggleDisabled={false}
          resolvedTheme="light"
          emptyState={<div>empty</div>}
          onShowDetail={() => {}}
          onEdit={() => {}}
          onDelete={() => {}}
          onToggle={() => {}}
          onPriorityChange={() => {}}
        />
      );
    });
    const badges = tableRenderer.root.findAllByProps({ 'data-testid': 'provider-capability-badge' });
    expect(badges).toHaveLength(1);
    expect(getText(badges[0])).toBe(i18n.t('ai_providers.capability_asr'));
    act(() => tableRenderer.unmount());

    const detailText = renderDetailText(row);
    expect(detailText).toContain(i18n.t('ai_providers.capability_asr'));
  });

  // 3. Provider显示多个支持的接口路径
  it('displays supported endpoints list in detail drawer', () => {
    const videoProvider: OpenAIProviderConfig = {
      name: 'video-cluster',
      baseUrl: 'https://video.example.com/v1',
      apiKeyEntries: [{ apiKey: 'sk-video12345678' }],
      capability: 'video',
      supportedEndpoints: ['/v1/videos/generations', '/v1/videos/tasks/{id}'],
      billingUnit: 'video_tasks',
      unitPrice: 0.5,
      isAsync: true,
    };
    const row = buildOpenAIRow(videoProvider);
    const detailText = renderDetailText(row);

    expect(detailText).toContain('/v1/videos/generations');
    expect(detailText).toContain('/v1/videos/tasks/{id}');
    expect(detailText).toContain(i18n.t('ai_providers.supported_endpoints_label'));
  });

  // 4. Provider显示billing unit
  it('displays billing unit in detail drawer', () => {
    const imgProvider: OpenAIProviderConfig = {
      name: 'flux-image-cluster',
      baseUrl: 'https://image.example.com/v1',
      apiKeyEntries: [{ apiKey: 'sk-img12345678' }],
      capability: 'image',
      billingUnit: 'images',
      unitPrice: 0.04,
    };
    const row = buildOpenAIRow(imgProvider);
    const detailText = renderDetailText(row);

    expect(detailText).toContain(i18n.t('ai_providers.billing_unit_images'));
  });

  // 5. Provider编辑image
  it('edits and saves an image provider configuration', async () => {
    mocks.getOpenAIProviders.mockResolvedValue([
      {
        name: 'dall-e-provider',
        baseUrl: 'https://api.openai.com/v1',
        apiKeyEntries: [{ apiKey: 'sk-img-test' }],
        models: [{ name: 'dall-e-3' }],
        capability: 'image',
        supportedEndpoints: ['/v1/images/generations'],
        billingUnit: 'images',
        unitPrice: 0.04,
        isAsync: false,
      },
    ]);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OpenAIEditDrawer open editIndex={0} disabled={false} onClose={vi.fn()} onSaved={vi.fn()} />
      );
    });

    await act(async () => {
      await findSaveButton(renderer.root)?.props.onClick();
    });

    expect(mocks.updateOpenAIProvider).toHaveBeenCalledWith(
      'dall-e-provider',
      0,
      expect.objectContaining({
        capability: 'image',
        billingUnit: 'images',
        unitPrice: 0.04,
        supportedEndpoints: ['/v1/images/generations'],
        isAsync: false,
      })
    );
    act(() => renderer.unmount());
  });

  // 6. Provider编辑video
  it('edits and saves a video provider with async execution', async () => {
    mocks.getOpenAIProviders.mockResolvedValue([
      {
        name: 'sora-provider',
        baseUrl: 'https://api.openai.com/v1',
        apiKeyEntries: [{ apiKey: 'sk-sora-test' }],
        models: [{ name: 'sora' }],
        capability: 'video',
        supportedEndpoints: ['/v1/videos/generations', '/v1/videos/tasks/{id}'],
        billingUnit: 'video_tasks',
        unitPrice: 1.5,
        isAsync: true,
      },
    ]);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OpenAIEditDrawer open editIndex={0} disabled={false} onClose={vi.fn()} onSaved={vi.fn()} />
      );
    });

    await act(async () => {
      await findSaveButton(renderer.root)?.props.onClick();
    });

    expect(mocks.updateOpenAIProvider).toHaveBeenCalledWith(
      'sora-provider',
      0,
      expect.objectContaining({
        capability: 'video',
        billingUnit: 'video_tasks',
        unitPrice: 1.5,
        isAsync: true,
      })
    );
    act(() => renderer.unmount());
  });

  // 7. Provider编辑asr
  it('edits and saves an asr provider configuration', async () => {
    mocks.getOpenAIProviders.mockResolvedValue([
      {
        name: 'sensevoice-provider',
        baseUrl: 'https://asr.example.com/v1',
        apiKeyEntries: [{ apiKey: 'sk-asr-test' }],
        models: [{ name: 'SenseVoiceSmall' }],
        capability: 'asr',
        supportedEndpoints: ['/v1/audio/transcriptions'],
        billingUnit: 'audio_seconds',
        unitPrice: 0.00022,
        isAsync: false,
      },
    ]);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OpenAIEditDrawer open editIndex={0} disabled={false} onClose={vi.fn()} onSaved={vi.fn()} />
      );
    });

    await act(async () => {
      await findSaveButton(renderer.root)?.props.onClick();
    });

    expect(mocks.updateOpenAIProvider).toHaveBeenCalledWith(
      'sensevoice-provider',
      0,
      expect.objectContaining({
        capability: 'asr',
        billingUnit: 'audio_seconds',
        unitPrice: 0.00022,
      })
    );
    act(() => renderer.unmount());
  });

  // 8. API Key保持遮罩
  it('ensures API key remains masked in detail drawer and table', () => {
    const provider: OpenAIProviderConfig = {
      name: 'masked-key-provider',
      baseUrl: 'https://secure.example.com/v1',
      apiKeyEntries: [{ apiKey: 'sk-super-secret-key-123456789' }],
      models: [{ name: 'gpt-4o' }],
    };
    const row = buildOpenAIRow(provider);
    const detailText = renderDetailText(row);

    expect(detailText).not.toContain('sk-super-secret-key-123456789');
    expect(detailText).toContain('sk******89');
  });

  // 9. 内部URL不显示
  it('masks loopback, private LAN IP addresses and non-standard ports', () => {
    const loopbackRow = buildOpenAIRow({
      name: 'loopback-provider',
      baseUrl: 'http://127.0.0.1:11434/v1',
      apiKeyEntries: [{ apiKey: 'sk-key', proxyUrl: 'http://10.0.1.5:8080' }],
    });
    const loopbackDetail = renderDetailText(loopbackRow);
    expect(loopbackDetail).toContain('[internal route]');
    expect(loopbackDetail).not.toContain('127.0.0.1');
    expect(loopbackDetail).not.toContain('10.0.1.5');
    expect(loopbackDetail).not.toContain('11434');
    expect(loopbackDetail).not.toContain('8080');

    const lanRow = buildOpenAIRow({
      name: 'lan-provider',
      baseUrl: 'http://192.168.1.100/v1',
      apiKeyEntries: [{ apiKey: 'sk-key' }],
    });
    const lanDetail = renderDetailText(lanRow);
    expect(lanDetail).toContain('[internal route]');
    expect(lanDetail).not.toContain('192.168.1.100');
  });

  // 10. 保存后保留聊天字段
  it('preserves existing chat models, headers, prefix, testModel on save', async () => {
    mocks.getOpenAIProviders.mockResolvedValue([
      {
        name: 'full-chat-provider',
        baseUrl: 'https://api.openai.com/v1',
        prefix: 'chat-',
        testModel: 'gpt-4o',
        headers: { 'X-Custom-Route': 'fast' },
        apiKeyEntries: [{ apiKey: 'sk-full' }],
        models: [{ name: 'gpt-4o', alias: 'gpt-4' }],
        disableCooling: false,
        capability: 'chat',
        billingUnit: 'tokens',
      },
    ]);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OpenAIEditDrawer open editIndex={0} disabled={false} onClose={vi.fn()} onSaved={vi.fn()} />
      );
    });

    await act(async () => {
      await findSaveButton(renderer.root)?.props.onClick();
    });

    expect(mocks.updateOpenAIProvider).toHaveBeenCalledWith(
      'full-chat-provider',
      0,
      expect.objectContaining({
        name: 'full-chat-provider',
        prefix: 'chat-',
        testModel: 'gpt-4o',
        headers: { 'X-Custom-Route': 'fast' },
        disableCooling: false,
        models: [{ name: 'gpt-4o', alias: 'gpt-4' }],
      })
    );
    act(() => renderer.unmount());
  });

  // 11. 非法billing unit被拒绝
  it('rejects invalid billing units during save validation', async () => {
    mocks.getOpenAIProviders.mockResolvedValue([
      {
        name: 'invalid-unit-provider',
        baseUrl: 'https://api.openai.com/v1',
        apiKeyEntries: [{ apiKey: 'sk-test' }],
        models: [{ name: 'model-a' }],
        capability: 'chat',
        billingUnit: 'invalid_unsupported_unit',
      },
    ]);

    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <OpenAIEditDrawer open editIndex={0} disabled={false} onClose={vi.fn()} onSaved={vi.fn()} />
      );
    });

    await act(async () => {
      await findSaveButton(renderer.root)?.props.onClick();
    });

    expect(mocks.showNotification).toHaveBeenCalledWith(
      i18n.t('model_prices.rule_error_invalid_billing_unit'),
      'warning'
    );
    expect(mocks.updateOpenAIProvider).not.toHaveBeenCalled();
    act(() => renderer.unmount());
  });

  // 12. unitPrice=0显示免费
  it('displays unitPrice=0 as explicit free without treating it as missing', () => {
    const freeRow = buildOpenAIRow({
      name: 'free-tts-provider',
      baseUrl: 'https://tts.example.com/v1',
      apiKeyEntries: [{ apiKey: 'sk-free-tts' }],
      capability: 'tts',
      billingUnit: 'characters',
      unitPrice: 0,
    });
    const detailText = renderDetailText(freeRow);

    expect(detailText).toContain(i18n.t('common.free', { defaultValue: '免费' }));
    expect(detailText).toContain('(0)');
    expect(detailText).not.toContain('ai_providers.price_unconfigured');
  });
});
