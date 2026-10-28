import { parseTimestamp } from './timestamp';

/**
 * 格式化工具函数
 * 从原项目 src/utils/string.js 迁移
 */

const resolveDefaultLocale = (): string | undefined => {
  const fromDocument =
    typeof document !== 'undefined' ? document.documentElement?.lang?.trim() : '';
  if (fromDocument) return fromDocument;
  const fromNavigator = typeof navigator !== 'undefined' ? navigator.language?.trim() : '';
  return fromNavigator || undefined;
};

/**
 * 隐藏 API Key 中间部分，仅保留前后两位
 */
export function maskApiKey(key: string): string {
  const trimmed = String(key || '').trim();
  if (!trimmed) {
    return '';
  }

  const MASKED_LENGTH = 10;
  const visibleChars = trimmed.length < 4 ? 1 : 2;
  const start = trimmed.slice(0, visibleChars);
  const end = trimmed.slice(-visibleChars);
  const maskedLength = Math.max(MASKED_LENGTH - visibleChars * 2, 1);
  const masked = '*'.repeat(maskedLength);

  return `${start}${masked}${end}`;
}

const API_KEY_MASK_REGEX =
  /(sk-proj-[A-Za-z0-9-_]{6,}|sk-ant-[A-Za-z0-9-_]{6,}|sk-[A-Za-z0-9-_]{6,}|sess-[A-Za-z0-9-_]{6,}|ghp_[A-Za-z0-9]{6,}|github_pat_[A-Za-z0-9_]{20,}|AIza[0-9A-Za-z-_]{8,}|hf_[A-Za-z0-9]{6,}|pk_[A-Za-z0-9]{6,}|rk_[A-Za-z0-9]{6,})/g;
const AUTHORIZATION_MASK_REGEX = /\b(authorization\s*[:=]\s*)(?:bearer\s+)?[^\s,"'{}]+/gi;
const BEARER_MASK_REGEX = /\bbearer\s+[A-Za-z0-9._~+/=-]{8,}/gi;
const TOKEN_FIELD_MASK_REGEX =
  /\b(access_token|refresh_token|id_token)\b(\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+/gi;
const API_KEY_FIELD_MASK_REGEX =
  /\b(api[-_ ]?key|x-api-key)\b(\s*["']?\s*[:=]\s*["']?)[^"',\s&}]+/gi;
const COOKIE_JSON_FIELD_MASK_REGEX = /("?(?:cookie|set-cookie)"?\s*:\s*")[^"]*(")/gi;
const COOKIE_MASK_REGEX = /\b(cookie|set-cookie)\s*:\s*[^,\r\n"}]+/gi;

/**
 * 将文本中的 API Key 片段替换为脱敏显示
 */
export function maskSensitiveText(value: string): string {
  const trimmed = String(value || '').trim();
  if (!trimmed) {
    return '';
  }

  return trimmed
    .replace(AUTHORIZATION_MASK_REGEX, '$1[redacted]')
    .replace(BEARER_MASK_REGEX, 'Bearer [redacted]')
    .replace(TOKEN_FIELD_MASK_REGEX, '$1$2[redacted]')
    .replace(API_KEY_FIELD_MASK_REGEX, '$1$2[redacted]')
    .replace(API_KEY_MASK_REGEX, (match) => maskApiKey(match))
    .replace(COOKIE_JSON_FIELD_MASK_REGEX, '$1[redacted]$2')
    .replace(COOKIE_MASK_REGEX, '$1: [redacted]');
}

/**
 * 格式化文件大小
 */
export function formatFileSize(bytes: number): string {
  if (bytes === 0) return '0 B';

  const units = ['B', 'KB', 'MB', 'GB'];
  const k = 1024;
  const i = Math.floor(Math.log(bytes) / Math.log(k));

  return `${(bytes / Math.pow(k, i)).toFixed(2)} ${units[i]}`;
}

/**
 * 格式化日期时间
 */
export function formatDateTime(date: string | Date, locale?: string): string {
  const d = typeof date === 'string' ? parseTimestamp(date) ?? new Date(date) : date;

  if (isNaN(d.getTime())) {
    return 'Invalid Date';
  }

  const resolvedLocale = locale?.trim() || resolveDefaultLocale();
  return d.toLocaleString(resolvedLocale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  });
}

/**
 * 将 Unix 时间戳（秒/毫秒/微秒/纳秒）格式化为本地时间字符串
 */
export function formatUnixTimestamp(value: unknown, locale?: string): string {
  if (value === null || value === undefined || value === '') return '';

  const asNumber = typeof value === 'number' ? value : Number(value);
  const date = (() => {
    if (!Number.isFinite(asNumber) || Number.isNaN(asNumber)) {
      return parseTimestamp(value) ?? new Date(String(value));
    }

    const abs = Math.abs(asNumber);

    // 秒：常见 10 位（~1e9）
    if (abs < 1e11) return new Date(asNumber * 1000);

    // 毫秒：常见 13 位（~1e12）
    if (abs < 1e14) return new Date(asNumber);

    // 微秒：常见 16 位（~1e15）
    if (abs < 1e17) return new Date(Math.round(asNumber / 1000));

    // 纳秒：常见 19 位（~1e18）
    return new Date(Math.round(asNumber / 1e6));
  })();

  if (Number.isNaN(date.getTime())) return '';
  return locale ? date.toLocaleString(locale) : date.toLocaleString();
}

/**
 * 格式化数字（添加千位分隔符）
 */
export function formatNumber(num: number, locale?: string): string {
  const resolvedLocale = locale?.trim() || resolveDefaultLocale();
  return num.toLocaleString(resolvedLocale);
}

/**
 * 截断长文本
 */
export function truncateText(text: string, maxLength: number): string {
  if (text.length <= maxLength) {
    return text;
  }
  return text.slice(0, maxLength) + '...';
}

/**
 * 脱敏 Provider 上游 URL，屏蔽私网 IP、内网域名和非标准内部端口
 */
export function sanitizeProviderUrl(rawUrl: string): string {
  const trimmed = String(rawUrl || '').trim();
  if (!trimmed) return '';

  try {
    const parsed = new URL(trimmed);
    const hostname = parsed.hostname.toLowerCase();
    const isLoopback =
      hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1' || hostname === '0.0.0.0';
    const isPrivateIP =
      /^10\./.test(hostname) ||
      /^192\.168\./.test(hostname) ||
      /^172\.(1[6-9]|2\d|3[01])\./.test(hostname) ||
      /^169\.254\./.test(hostname) ||
      /^fc00:/.test(hostname) ||
      /^fe80:/.test(hostname);
    const isInternalDomain =
      hostname.endsWith('.internal') ||
      hostname.endsWith('.local') ||
      hostname.endsWith('.lan') ||
      hostname.endsWith('.corp') ||
      hostname.endsWith('.cluster.local') ||
      hostname.includes('.svc.');

    if (isLoopback || isPrivateIP || isInternalDomain) {
      return '[internal route]';
    }

    // 不暴露非标准内部端口
    if (parsed.port && parsed.port !== '80' && parsed.port !== '443') {
      return `${parsed.protocol}//${parsed.hostname}${parsed.pathname === '/' ? '' : parsed.pathname}`;
    }

    return `${parsed.protocol}//${parsed.hostname}${parsed.pathname}`;
  } catch {
    if (
      /localhost|127\.0\.0\.1|0\.0\.0\.0|10\.\d+|192\.168\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+|\.internal|\.local|\.lan/i.test(
        trimmed
      )
    ) {
      return '[internal route]';
    }
    return trimmed;
  }
}
