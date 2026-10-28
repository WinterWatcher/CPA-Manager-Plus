import { describe, expect, it } from 'vitest';
import type { OpenAIProviderConfig } from '@/types';
import type { ProviderRecentUsageMap } from '../utils';
import { buildProviderRows } from './rowData';

const emptyInput = {
  gemini: [],
  codex: [],
  claude: [],
  vertex: [],
  openai: [],
  usageByProvider: new Map() as ProviderRecentUsageMap,
};

describe('buildProviderRows capability fields', () => {
  it('maps capability, supportedEndpoints, billingUnit, unitPrice, isAsync, and canTrackUsage from OpenAIProviderConfig', () => {
    const openai: OpenAIProviderConfig[] = [
      {
        name: 'Whisper-ASR-Provider',
        baseUrl: 'https://asr.example.com/v1',
        apiKeyEntries: [{ apiKey: 'sk-asr-key-9999' }],
        capability: 'asr',
        supportedEndpoints: ['/v1/audio/transcriptions'],
        billingUnit: 'audio_seconds',
        unitPrice: 0.006,
        isAsync: false,
        canTrackUsage: true,
        description: 'Dedicated ASR transcribing service',
      },
      {
        name: 'Sora-Video-Provider',
        baseUrl: 'https://video.example.com/v1',
        apiKeyEntries: [{ apiKey: 'sk-video-key-8888' }],
        capability: 'video',
        supportedEndpoints: ['/v1/videos/generations', '/v1/videos/tasks/{id}'],
        billingUnit: 'video_tasks',
        unitPrice: 0.50,
        isAsync: true,
        canTrackUsage: true,
        description: 'Async text to video service',
      },
    ];

    const rows = buildProviderRows({ ...emptyInput, openai });
    expect(rows).toHaveLength(2);

    const asrRow = rows[0];
    expect(asrRow.capability).toBe('asr');
    expect(asrRow.supportedEndpoints).toEqual(['/v1/audio/transcriptions']);
    expect(asrRow.billingUnit).toBe('audio_seconds');
    expect(asrRow.unitPrice).toBe(0.006);
    expect(asrRow.isAsync).toBe(false);
    expect(asrRow.canTrackUsage).toBe(true);
    expect(asrRow.description).toBe('Dedicated ASR transcribing service');

    const videoRow = rows[1];
    expect(videoRow.capability).toBe('video');
    expect(videoRow.supportedEndpoints).toEqual(['/v1/videos/generations', '/v1/videos/tasks/{id}']);
    expect(videoRow.billingUnit).toBe('video_tasks');
    expect(videoRow.unitPrice).toBe(0.50);
    expect(videoRow.isAsync).toBe(true);
    expect(videoRow.canTrackUsage).toBe(true);
  });
});
