export function statusClass(kind) {
  if (kind === 'error') return 'error';
  if (kind === 'success') return 'success';
  return 'info';
}

export function statusIcon(kind) {
  if (kind === 'error') return '!';
  if (kind === 'success') return 'OK';
  return 'i';
}

export function usageLabel(value) {
  if (value === null || value === undefined) return '-';
  return `${value}%`;
}

export function clampPercent(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 0;
  if (n < 0) return 0;
  if (n > 100) return 100;
  return Math.round(n);
}

export function normalizeAccountTypeLabel(value) {
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

export function parseUsageWindows(usage) {
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

export function parsePercentInput(value, fallbackValue) {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallbackValue;
  return Math.max(0, Math.min(100, Math.round(n)));
}

export function parseSchedulerIntervalInput(value, fallbackValue) {
  const n = Number(value);
  if (!Number.isFinite(n)) return fallbackValue;
  return Math.max(10, Math.min(300, Math.round(n)));
}
