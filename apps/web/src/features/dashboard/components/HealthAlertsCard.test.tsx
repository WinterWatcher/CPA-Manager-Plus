import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it } from 'vitest';
import { HealthAlertsCard } from './HealthAlertsCard';
import type { DashboardRecentFailure } from '@/services/api/usageService';

describe('HealthAlertsCard', () => {
  const emptyProps = {
    authMetaMap: new Map(),
    authFileMap: new Map(),
    sourceInfoMap: {
      byAuthIndex: new Map(),
      bySource: new Map(),
      byIdentityKey: new Map(),
    },
    channelByAuthIndex: new Map(),
    apiKeyAliasMap: new Map(),
  };

  const baseFailure: DashboardRecentFailure = {
    timestamp_ms: 1729000000000,
    model: 'gpt-4o',
    api_key_hash: 'hash-1',
    source_hash: 'src-1',
    auth_index: 'auth-1',
    endpoint: '/v1/chat/completions',
    duration_ms: 250,
    fail_status_code: 500,
    fail_summary: 'Internal Server Error',
  };

  it('renders formatted duration when duration_ms > 0', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <HealthAlertsCard
          {...emptyProps}
          channelHealth={[]}
          recentFailures={[{ ...baseFailure, duration_ms: 350 }]}
          loading={false}
        />
      );
    });

    const json = JSON.stringify(renderer.toJSON());
    expect(json).toContain('350');
  });

  it('renders -- when duration_ms is 0 or null and avoids displaying 0 ms', async () => {
    let rendererZero!: ReactTestRenderer;
    await act(async () => {
      rendererZero = create(
        <HealthAlertsCard
          {...emptyProps}
          channelHealth={[]}
          recentFailures={[{ ...baseFailure, duration_ms: 0 }]}
          loading={false}
        />
      );
    });

    const jsonZero = JSON.stringify(rendererZero.toJSON());
    expect(jsonZero).toContain('--');
    expect(jsonZero).not.toContain('0 ms');
    expect(jsonZero).not.toContain('0ms');

    let rendererNull!: ReactTestRenderer;
    await act(async () => {
      rendererNull = create(
        <HealthAlertsCard
          {...emptyProps}
          channelHealth={[]}
          recentFailures={[{ ...baseFailure, duration_ms: null }]}
          loading={false}
        />
      );
    });

    const jsonNull = JSON.stringify(rendererNull.toJSON());
    expect(jsonNull).toContain('--');
    expect(jsonNull).not.toContain('0 ms');
  });

  it('displays capability when failure is non-chat', async () => {
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <HealthAlertsCard
          {...emptyProps}
          channelHealth={[]}
          recentFailures={[
            {
              ...baseFailure,
              model: 'dall-e-3',
              capability: 'image',
              billing_unit: 'image',
              duration_ms: null,
            },
          ]}
          loading={false}
        />
      );
    });

    const json = JSON.stringify(renderer.toJSON());
    expect(json).toContain('dall-e-3');
    expect(json).toContain(' · image');
  });
});
