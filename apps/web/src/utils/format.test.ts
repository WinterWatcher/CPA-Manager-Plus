import { describe, expect, it } from 'vitest';
import { maskApiKey, sanitizeProviderUrl } from './format';

describe('maskApiKey', () => {
  it('masks intermediate characters', () => {
    expect(maskApiKey('sk-1234567890abcdef')).toBe('sk******ef');
  });

  it('handles empty input', () => {
    expect(maskApiKey('')).toBe('');
  });
});

describe('sanitizeProviderUrl', () => {
  it('allows safe public URLs without ports', () => {
    expect(sanitizeProviderUrl('https://api.openai.com/v1')).toBe('https://api.openai.com/v1');
    expect(sanitizeProviderUrl('https://api.deepseek.com')).toBe('https://api.deepseek.com/');
  });

  it('masks loopback and localhost addresses', () => {
    expect(sanitizeProviderUrl('http://localhost:8000/v1')).toBe('[internal route]');
    expect(sanitizeProviderUrl('http://127.0.0.1:11434/v1')).toBe('[internal route]');
  });

  it('masks private LAN IP addresses', () => {
    expect(sanitizeProviderUrl('http://10.0.1.5:8080/v1')).toBe('[internal route]');
    expect(sanitizeProviderUrl('http://192.168.1.100/v1')).toBe('[internal route]');
    expect(sanitizeProviderUrl('http://172.20.0.2:9000/v1')).toBe('[internal route]');
  });

  it('masks internal hostnames and cluster addresses', () => {
    expect(sanitizeProviderUrl('http://llm-gateway.internal/v1')).toBe('[internal route]');
    expect(sanitizeProviderUrl('http://proxy.local:3000')).toBe('[internal route]');
    expect(sanitizeProviderUrl('http://vllm.svc.cluster.local:8000')).toBe('[internal route]');
  });

  it('does not display internal non-standard ports on public hostnames', () => {
    const sanitized = sanitizeProviderUrl('https://custom.upstream.com:8443/v1');
    expect(sanitized).not.toContain(':8443');
    expect(sanitized).toContain('https://custom.upstream.com');
  });

  it('handles empty or blank input', () => {
    expect(sanitizeProviderUrl('')).toBe('');
    expect(sanitizeProviderUrl('   ')).toBe('');
  });
});
