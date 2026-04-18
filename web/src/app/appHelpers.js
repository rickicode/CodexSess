const defaultCodexModels = [
  'gpt-5.1-codex-max',
  'gpt-5.2',
  'gpt-5.2-codex',
  'gpt-5.3-codex',
  'gpt-5.4-mini',
  'gpt-5.4'
];

const jsonHeaders = { 'Content-Type': 'application/json' };
const uiPrefsKey = 'codexsess.ui.preferences';

function statusClass(kind) {
  if (kind === 'error') return 'error';
  if (kind === 'success') return 'success';
  return 'info';
}

function statusIcon(kind) {
  if (kind === 'error') return '!';
  if (kind === 'success') return 'OK';
  return 'i';
}

function usageLabel(value) {
  if (value === null || value === undefined) return '-';
  return `${value}%`;
}

function clampPercent(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 0;
  if (n < 0) return 0;
  if (n > 100) return 100;
  return Math.round(n);
}

function normalizeAccountTypeLabel(value) {
  const raw = String(value || '').trim();
  if (!raw) return 'Unknown';
  return raw
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
    .split(' ')
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1).toLowerCase())
    .join(' ');
}

function isLoopbackHost(hostname) {
  const host = String(hostname || '').trim().toLowerCase();
  return host === '127.0.0.1' || host === 'localhost' || host === '::1' || host === '[::1]';
}

function resolvedAPIBase(apiBase, windowObj = typeof window !== 'undefined' ? window : undefined) {
  if (!apiBase) return '';
  if (!windowObj) return apiBase;
  try {
    const parsed = new URL(apiBase, windowObj.location.origin);
    if (isLoopbackHost(parsed.hostname) && !isLoopbackHost(windowObj.location.hostname)) {
      return '';
    }
    return `${parsed.origin}`.replace(/\/+$/, '');
  } catch {
    return apiBase;
  }
}

function toAPIURL(url, apiBase, windowObj = typeof window !== 'undefined' ? window : undefined) {
  const raw = String(url || '').trim();
  if (/^https?:\/\//i.test(raw)) return raw;
  const base = resolvedAPIBase(apiBase, windowObj);
  if (!base) return raw;
  if (raw.startsWith('/')) return `${base}${raw}`;
  return `${base}/${raw}`;
}

function formatDurationLabelFromMinutes(totalMinutes) {
  const mins = Math.max(0, Math.round(Number(totalMinutes) || 0));
  if (mins >= 60 * 24) {
    const d = Math.floor(mins / (60 * 24));
    const h = Math.floor((mins % (60 * 24)) / 60);
    return h > 0 ? `${d}d ${h}h` : `${d}d`;
  }
  if (mins >= 60) {
    const h = Math.floor(mins / 60);
    const m = mins % 60;
    return m > 0 ? `${h}h ${m}m` : `${h}h`;
  }
  return `${mins}m`;
}

function normalizeWindowLabel(rawLabel) {
  const v = String(rawLabel || '').trim();
  if (!v) return '';
  const minutesMatch = v.match(/^(\d+)\s*m$/i);
  if (minutesMatch) {
    return formatDurationLabelFromMinutes(Number(minutesMatch[1]));
  }
  return v;
}

function extractWindowPercent(window, fallbackPercent) {
  if (window && typeof window === 'object') {
    const remaining = Number(window.remaining_percent);
    if (Number.isFinite(remaining)) return clampPercent(remaining);
    const used = Number(window.used_percent);
    if (Number.isFinite(used)) return clampPercent(100 - used);
  }
  return clampPercent(fallbackPercent);
}

function extractWindowResetISO(window, fallbackResetAt) {
  if (window && typeof window === 'object') {
    const resetAt = Number(window.reset_at);
    if (Number.isFinite(resetAt) && resetAt > 0) return new Date(resetAt * 1000).toISOString();
    const resetAfter = Number(window.reset_after_seconds);
    if (Number.isFinite(resetAfter) && resetAfter > 0) return new Date(Date.now() + resetAfter * 1000).toISOString();
  }
  return fallbackResetAt || null;
}

function extractWindowDurationLabel(window, fallbackLabel) {
  if (window && typeof window === 'object') {
    const limitSeconds = Number(window.limit_window_seconds);
    if (Number.isFinite(limitSeconds) && limitSeconds > 0) {
      return formatDurationLabelFromMinutes(Math.ceil(limitSeconds / 60));
    }
  }
  return normalizeWindowLabel(fallbackLabel);
}

function parseUsageWindows(usage) {
  if (!usage) return [];

  const getUsage = (snakeKey, pascalKey) => {
    if (usage?.[snakeKey] !== undefined && usage?.[snakeKey] !== null) return usage[snakeKey];
    if (usage?.[pascalKey] !== undefined && usage?.[pascalKey] !== null) return usage[pascalKey];
    return null;
  };

  let raw = null;
  try {
    const rawJSON = getUsage('raw_json', 'RawJSON');
    raw = rawJSON ? JSON.parse(rawJSON) : null;
  } catch {
    raw = null;
  }
  const rateLimit = raw?.rate_limit && typeof raw.rate_limit === 'object' ? raw.rate_limit : {};
  const codeReviewRate = raw?.code_review_rate_limit && typeof raw.code_review_rate_limit === 'object'
    ? raw.code_review_rate_limit
    : {};
  const windows = [];

  function pushWindow(name, key, window, fallbackPercent, fallbackResetAt, fallbackLabel) {
    windows.push({
      key,
      name,
      percent: extractWindowPercent(window, fallbackPercent),
      resetAt: extractWindowResetISO(window, fallbackResetAt),
      durationLabel: extractWindowDurationLabel(window, fallbackLabel)
    });
  }

  function isFiveHourWindow(window, fallbackLabel) {
    const secs = Number(window?.limit_window_seconds);
    if (Number.isFinite(secs) && secs > 0) {
      return Math.abs(secs - 18000) <= 120;
    }
    const label = normalizeWindowLabel(fallbackLabel).toLowerCase();
    return label.startsWith('5h');
  }

  const primaryWindow = rateLimit?.primary_window;
  const secondaryWindow = rateLimit?.secondary_window;
  const primaryLabel = normalizeWindowLabel(getUsage('window_primary', 'WindowPrimary'));
  const hasFiveHour = isFiveHourWindow(primaryWindow, primaryLabel);

  if (hasFiveHour) {
    pushWindow(
      primaryLabel || '5h',
      'primary_window',
      primaryWindow,
      getUsage('hourly_pct', 'HourlyPct'),
      getUsage('hourly_reset_at', 'HourlyResetAt'),
      getUsage('window_primary', 'WindowPrimary')
    );

    const weeklyFallback = getUsage('weekly_pct', 'WeeklyPct');
    const weeklyReset = getUsage('weekly_reset_at', 'WeeklyResetAt');
    if (secondaryWindow || weeklyFallback !== null || weeklyReset) {
      pushWindow('Weekly', 'secondary_window', secondaryWindow, weeklyFallback, weeklyReset, 'Weekly');
    }
  } else {
    pushWindow(
      'Weekly',
      'weekly_window',
      primaryWindow,
      getUsage('hourly_pct', 'HourlyPct'),
      getUsage('hourly_reset_at', 'HourlyResetAt'),
      'Weekly'
    );
  }

  const codeReviewPrimary = codeReviewRate?.primary_window;
  if (codeReviewPrimary) {
    pushWindow('Code Review', 'code_review_window', codeReviewPrimary, 0, null, 'Code Review');
  }

  return windows.filter((w) => w.name && Number.isFinite(w.percent));
}


function pageSizeOptions() {
  return [10, 20, 50, 100];
}

function menuFromPath(path) {
  const p = String(path || '').trim();
  if (p === '/settings') return 'settings';
  if (p === '/api') return 'api';
  if (p === '/logs') return 'logs';
  if (p === '/system-logs') return 'system-logs';
  if (p === '/about') return 'about';
  if (p === '/chat' || p.startsWith('/chat/')) return 'coding';
  return 'dashboard';
}

function pathForMenu(menu) {
  switch (menu) {
    case 'settings': return '/settings';
    case 'api': return '/api';
    case 'logs': return '/logs';
    case 'system-logs': return '/system-logs';
    case 'about': return '/about';
    case 'coding': return '/chat';
    default: return '/';
  }
}

function documentTitleByMenu(menu) {
  switch (menu) {
    case 'settings': return 'Settings';
    case 'api': return 'API';
    case 'logs': return 'API Logs';
    case 'system-logs': return 'System Logs';
    case 'about': return 'About';
    case 'coding': return 'Chat';
    default: return 'Dashboard';
  }
}

function formatResetTimestamp(dateObj) {
  if (!(dateObj instanceof Date) || Number.isNaN(dateObj.getTime())) return '-';
  return dateObj.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  });
}

function formatRemaining(diffMs) {
  const total = Math.max(0, Math.floor(diffMs / 1000));
  const hours = Math.floor(total / 3600);
  const mins = Math.floor((total % 3600) / 60);
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}

function prettyJSONText(raw) {
  if (!raw) return '';
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return String(raw);
  }
}

function openAIExample() {
  return `POST /v1/chat/completions
{
  "model": "gpt-5.2-codex",
  "messages": [{"role":"user","content":"Hello"}]
}`;
}

function claudeExample() {
  return `POST /claude/v1/messages
{
  "model": "gpt-5.2-codex",
  "max_tokens": 1024,
  "messages": [{"role":"user","content":"Hello"}]
}`;
}

function authJSONExample() {
  return 'GET /api/auth.json';
}

function usageStatusExample() {
  return 'GET /v1/usage';
}

function logStatusClass(statusCode) {
  if (statusCode >= 500) return 'error';
  if (statusCode >= 400) return 'warn';
  if (statusCode >= 300) return 'info';
  return 'success';
}

function parsePercentInput(value, fallbackValue) {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallbackValue;
  return Math.max(0, Math.min(100, Math.round(n)));
}

function parseSchedulerIntervalInput(value, fallbackValue) {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallbackValue;
  return Math.max(10, Math.min(300, Math.round(n)));
}

export {
  defaultCodexModels,
  jsonHeaders,
  uiPrefsKey,
  statusClass,
  statusIcon,
  usageLabel,
  clampPercent,
  normalizeAccountTypeLabel,
  isLoopbackHost,
  resolvedAPIBase,
  toAPIURL,
  formatDurationLabelFromMinutes,
  normalizeWindowLabel,
  extractWindowPercent,
  extractWindowResetISO,
  extractWindowDurationLabel,
  parseUsageWindows,
  pageSizeOptions,
  menuFromPath,
  pathForMenu,
  documentTitleByMenu,
  formatResetTimestamp,
  formatRemaining,
  prettyJSONText,
  openAIExample,
  claudeExample,
  authJSONExample,
  usageStatusExample,
  logStatusClass,
  parsePercentInput,
  parseSchedulerIntervalInput
};
