<script>
  import { onMount } from 'svelte';
  import DashboardView from './views/DashboardView.svelte';
  import CodingView from './views/CodingView.svelte';
  import SettingsView from './views/SettingsView.svelte';
  import ApiEndpointView from './views/ApiEndpointView.svelte';
  import ApiLogsView from './views/ApiLogsView.svelte';
  import AboutView from './views/AboutView.svelte';

  let accounts = $state([]);
  let busy = $state(false);
  let status = $state({ text: 'Initializing...', kind: 'info' });

  let apiKey = $state('');
  let apiMode = $state('codex_cli');
  let openAIEndpoint = $state('');
  let claudeEndpoint = $state('');
  let authJSONEndpoint = $state('');
  let isChatRoute = $state(typeof window !== 'undefined' && (window.location.pathname === '/chat' || window.location.pathname.startsWith('/chat/')));
  let activeMenu = $state('dashboard');
  let apiLogs = $state([]);
  let showLogDetailModal = $state(false);
  let logDetailEntry = $state(null);
  let availableModels = $state([]);
  let modelMappings = $state({});
  let mappingAlias = $state('');
  let mappingTargetModel = $state('gpt-5.2-codex');
  let editingMappingAlias = $state('');
  let settingsBusy = $state(false);
  let copiedAction = $state('');
  let showAccountEmail = $state(true);
  let autoRefreshEnabled = $state(false);
  let autoRefreshMinutes = $state(30);
  let autoRefreshMinutesInput = $state('30');
  let usageAlertThreshold = $state(5);
  let usageAlertThresholdInput = $state('5');
  let usageAutoSwitchThreshold = $state(2);
  let usageAutoSwitchThresholdInput = $state('2');
  let usageSoundEnabled = $state(true);
  let appVersion = $state('dev');
  let codexVersion = $state('unknown');
  let latestVersion = $state('');
  let releaseURL = $state('');
  let latestChangelog = $state('');
  let updateAvailable = $state(false);
  let updateCheckedAt = $state('');
  let updateCheckError = $state('');
  let updateCheckBusy = $state(false);
  let autoRefreshBusy = $state(false);
  let usageRefreshBusy = $state(false);
  let usageAutomationBusy = $state(false);
  let autoSwitchInProgress = $state(false);
  let lastAutoSwitchAt = $state(0);
  let lastUsageAlertSignature = $state('');
  let backgroundRefreshError = $state('');
  let backgroundRefreshLastAt = $state(0);
  let activeUsageAlert = $state(null);
  let uiPrefsLoaded = $state(false);
  let accountSearchQuery = $state('');
  let accountTypeFilter = $state('all');
  let mobileSidebarOpen = $state(false);

  const appMode = (import.meta.env.VITE_APP_MODE || 'web').toLowerCase();

  const defaultCodexModels = [
    'gpt-5.1-codex-max',
    'gpt-5.2',
    'gpt-5.2-codex',
    'gpt-5.3-codex',
    'gpt-5.4-mini',
    'gpt-5.4'
  ];

  let showAddAccountModal = $state(false);
  let addAccountMode = $state('menu');

  let browserLoginURL = $state('');
  let browserLoginID = $state('');
  let browserWaiting = $state(false);
  let browserKnownIDs = $state([]);
  let browserCallbackURL = $state('');

  let deviceLogin = $state(null);
  let deviceCodeCopied = $state(false);
  let nowTick = $state(Date.now());

  let showRemoveModal = $state(false);
  let removeCandidate = $state(null);

  let pollTimer = null;
  let browserWaitTimer = null;
  let copiedResetTimer = null;
  let usageThresholdPersistTimer = null;
  let usageThresholdPersistInFlight = false;
  let usageThresholdPersistQueued = null;
  let usageThresholdLastSavedAlert = null;
  let usageThresholdLastSavedSwitch = null;
  let soundCache = {};
  let refreshAllInFlight = null;
  let lastRefreshAllAt = 0;

  const jsonHeaders = { 'Content-Type': 'application/json' };
  const uiPrefsKey = 'codexsess.ui.preferences.v1';
  const apiBase = String(import.meta.env.VITE_API_BASE || '').trim().replace(/\/+$/, '');

  function setStatus(text, kind = 'info') {
    status = { text, kind };
  }

  function toAPIURL(url) {
    const raw = String(url || '').trim();
    if (!apiBase || /^https?:\/\//i.test(raw)) return raw;
    if (raw.startsWith('/')) return `${apiBase}${raw}`;
    return `${apiBase}/${raw}`;
  }

  function sendClientEvent(type, message, data = {}, level = 'info') {
    const payload = {
      type: String(type || 'event'),
      source: 'web-console',
      level: String(level || 'info'),
      message: String(message || ''),
      data: (data && typeof data === 'object') ? data : {}
    };
    fetch(toAPIURL('/api/events/log'), {
      method: 'POST',
      headers: jsonHeaders,
      credentials: 'same-origin',
      body: JSON.stringify(payload)
    }).catch(() => {});
  }

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

  function accountTypeOptions() {
    const values = new Set();
    for (const account of accounts) {
      const v = String(account?.plan_type || '').trim().toLowerCase();
      if (v) values.add(v);
    }
    const opts = [...values].sort((a, b) => a.localeCompare(b)).map((value) => ({
      value,
      label: normalizeAccountTypeLabel(value)
    }));
    return [{ value: 'all', label: 'All Account Types' }, ...opts];
  }

  function filteredAccounts() {
    const q = String(accountSearchQuery || '').trim().toLowerCase();
    const type = String(accountTypeFilter || 'all').trim().toLowerCase();
    return accounts.filter((account) => {
      const accountType = String(account?.plan_type || '').trim().toLowerCase();
      if (type !== 'all' && accountType !== type) return false;
      if (!q) return true;
      const haystack = [
        account?.email,
        account?.alias,
        account?.id,
        account?.plan_type
      ]
        .map((item) => String(item || '').toLowerCase())
        .join(' ');
      return haystack.includes(q);
    });
  }

  function activeAPIAccount() {
    return accounts.find((account) => account?.active_api) || null;
  }

  function activeCLIAccount() {
    return accounts.find((account) => account?.active_cli) || null;
  }

  function accountDisplayLabel(account) {
    if (!account) return 'Not selected';
    return showAccountEmail ? (account.email || account.id || '-') : (account.id || '-');
  }

  function setAccountSearchQuery(value) {
    accountSearchQuery = value;
  }

  function setAccountTypeFilter(value) {
    accountTypeFilter = value;
  }

  function toggleMobileSidebar() {
    mobileSidebarOpen = !mobileSidebarOpen;
  }

  function closeMobileSidebar() {
    mobileSidebarOpen = false;
  }

  function switchMenu(menu) {
    if (menu === 'coding' && typeof window !== 'undefined') {
      window.location.href = '/chat';
      return;
    }
    activeMenu = menu;
    closeMobileSidebar();
  }

  function detectChatRoute() {
    if (typeof window === 'undefined') return false;
    const path = String(window.location.pathname || '').trim().toLowerCase();
    return path === '/chat' || path.startsWith('/chat/');
  }

  function syncRouteMode() {
    isChatRoute = detectChatRoute();
    if (isChatRoute) {
      activeMenu = 'coding';
      closeMobileSidebar();
    }
  }


  function documentTitleByMenu(menu) {
    const base = 'CodexSess Console';
    const key = String(menu || '').trim().toLowerCase();
    switch (key) {
      case 'coding':
        return `Chat - ${base}`;
      case 'settings':
        return `Settings - ${base}`;
      case 'api-endpoints':
        return `API Workspace - ${base}`;
      case 'logs':
        return `API Logs - ${base}`;
      case 'about':
        return `About - ${base}`;
      default:
        return base;
    }
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

  function parsePercentInput(value, fallbackValue) {
    const n = Number(value);
    if (!Number.isFinite(n)) return fallbackValue;
    return Math.max(0, Math.min(100, Math.round(n)));
  }

  function playNotificationTone(kind = 'info', { wait = false } = {}) {
    if (!usageSoundEnabled || typeof window === 'undefined') return Promise.resolve();
    const fileByKind = {
      info: '/sounds/use.ogg',
      alert: '/sounds/alert.ogg',
      switch: '/sounds/switch.ogg'
    };
    const src = fileByKind[kind] || fileByKind.info;
    try {
      if (!soundCache[src]) {
        const baseAudio = new Audio(src);
        baseAudio.preload = 'auto';
        soundCache[src] = baseAudio;
      }
      const sound = soundCache[src].cloneNode(true);
      sound.volume = kind === 'alert' ? 0.9 : 0.8;
      const playPromise = sound.play().catch(() => {});
      if (!wait) return playPromise;
      return new Promise((resolve) => {
        let done = false;
        const finish = () => {
          if (done) return;
          done = true;
          resolve();
        };
        sound.addEventListener('ended', finish, { once: true });
        sound.addEventListener('error', finish, { once: true });
        setTimeout(finish, 2200);
        Promise.resolve(playPromise).finally(() => {
          if (sound.paused) finish();
        });
      });
    } catch {
      return Promise.resolve();
    }
    return Promise.resolve();
  }

  function getPrimaryUsageWindow(account) {
    const windows = parseUsageWindows(account?.usage);
    const fiveHour = windows.find((w) => String(w.name || '').trim().toLowerCase().startsWith('5h'));
    const weekly = windows.find((w) => String(w.name || '').trim().toLowerCase().includes('weekly'));
    if (fiveHour) return { type: '5h', window: fiveHour };
    if (weekly) return { type: 'weekly', window: weekly };
    if (windows.length > 0) return { type: 'weekly', window: windows[0] };
    return null;
  }

  function getActiveAccount() {
    return accounts.find((account) => account?.active_api || account?.active) || null;
  }

  function backupUsageCandidates(currentActiveID) {
    return accounts
      .filter((account) => account?.id && account.id !== currentActiveID)
      .map((account) => {
        const metric = getPrimaryUsageWindow(account);
        return {
          account,
          percent: metric ? clampPercent(metric.window?.percent) : -1
        };
      })
      .filter((item) => item.percent >= 0)
      .sort((a, b) => b.percent - a.percent);
  }

  async function evaluateActiveAccountUsage({ allowAutoSwitch = true, source = 'unknown' } = {}) {
    if (usageAutomationBusy) return;
    const active = getActiveAccount();
    if (!active) {
      activeUsageAlert = null;
      lastUsageAlertSignature = '';
      return;
    }
    const metric = getPrimaryUsageWindow(active);
    if (!metric) {
      activeUsageAlert = null;
      lastUsageAlertSignature = '';
      return;
    }
    const percent = clampPercent(metric.window?.percent);
    const windowLabel = metric.type === '5h' ? '5h' : 'weekly';
    const alertThreshold = parsePercentInput(usageAlertThreshold, 5);
    const switchThreshold = parsePercentInput(usageAutoSwitchThreshold, 2);

    if (percent > alertThreshold) {
      activeUsageAlert = null;
      lastUsageAlertSignature = '';
      return;
    }

    const alertMessage = `Active account ${windowLabel} remaining is ${percent}% (threshold ${alertThreshold}%).`;
    const signature = `${active.id}:${windowLabel}:${percent}`;
    activeUsageAlert = {
      level: percent <= switchThreshold ? 'critical' : 'warning',
      message: alertMessage,
      percent,
      windowLabel
    };
    if (lastUsageAlertSignature !== signature) {
      playNotificationTone('alert');
      setStatus(alertMessage, percent <= switchThreshold ? 'error' : 'info');
      sendClientEvent('usage-alert', alertMessage, {
        account_id: active.id,
        window: windowLabel,
        remaining_percent: percent,
        alert_threshold: alertThreshold,
        auto_switch_threshold: switchThreshold
      }, percent <= switchThreshold ? 'error' : 'warning');
      lastUsageAlertSignature = signature;
    }

    if (activeMenu === 'settings') return;
    if (!allowAutoSwitch || percent > switchThreshold) return;
    if (autoSwitchInProgress) return;
    if (Date.now() - lastAutoSwitchAt < 10000) return;

    const candidates = backupUsageCandidates(active.id);
    if (candidates.length === 0) {
      const noCandidateMessage = 'Auto-switch skipped: no backup account with usage data.';
      setStatus(noCandidateMessage, 'error');
      sendClientEvent('auto-switch-skipped', noCandidateMessage, {
        account_id: active.id,
        reason: 'no_backup_usage'
      }, 'error');
      return;
    }
    const best = candidates[0];
    if (best.percent <= 0) {
      const exhaustedMessage = 'Auto-switch failed: semua API habis (remaining 0%).';
      setStatus(exhaustedMessage, 'error');
      sendClientEvent('auto-switch-failed', exhaustedMessage, {
        account_id: active.id,
        reason: 'all_accounts_exhausted'
      }, 'error');
      activeUsageAlert = {
        level: 'critical',
        message: exhaustedMessage,
        percent: 0,
        windowLabel
      };
      return;
    }
    if (best.percent <= percent) {
      const noBetterMessage = `Auto-switch skipped: no backup account better than current (${percent}%).`;
      setStatus(noBetterMessage, 'error');
      sendClientEvent('auto-switch-skipped', noBetterMessage, {
        account_id: active.id,
        current_percent: percent,
        best_percent: best.percent,
        reason: 'no_better_candidate'
      }, 'warning');
      return;
    }
    const target = best.account;

    autoSwitchInProgress = true;
    usageAutomationBusy = true;
    lastAutoSwitchAt = Date.now();
    sendClientEvent('auto-switch-start', `Auto-switch starting to ${target.email || target.id}.`, {
      from_account_id: active.id,
      to_account_id: target.id,
      from_percent: percent,
      to_percent: best.percent
    }, 'warning');
    try {
      await useAccount(target.id, { source: 'auto', suppressTone: false, suppressStatus: true, refreshStatusMessage: false });
      setStatus(`Auto-switched account to ${target.email || target.id} because active ${windowLabel} reached ${percent}%.`, 'success');
      sendClientEvent('auto-switch-success', `Auto-switch success to ${target.email || target.id}.`, {
        from_account_id: active.id,
        to_account_id: target.id,
        from_percent: percent,
        to_percent: best.percent
      }, 'success');
    } catch {
      sendClientEvent('auto-switch-failed', `Auto-switch failed to ${target.email || target.id}.`, {
        from_account_id: active.id,
        to_account_id: target.id
      }, 'error');
    } finally {
      usageAutomationBusy = false;
      autoSwitchInProgress = false;
    }
  }

  function formatResetLabel(resetAtISO) {
    nowTick;
    if (!resetAtISO) return '-';
    const target = new Date(resetAtISO);
    if (Number.isNaN(target.getTime())) return '-';
    const diffMs = target.getTime() - Date.now();
    if (diffMs <= 0) return `reset (${formatResetTimestamp(target)})`;
    return `${formatRemaining(diffMs)} (${formatResetTimestamp(target)})`;
  }

  function formatResetTimestamp(dateObj) {
    const mm = String(dateObj.getMonth() + 1).padStart(2, '0');
    const dd = String(dateObj.getDate()).padStart(2, '0');
    const hh = String(dateObj.getHours()).padStart(2, '0');
    const mi = String(dateObj.getMinutes()).padStart(2, '0');
    return `${mm}/${dd} ${hh}:${mi}`;
  }

  function formatRemaining(diffMs) {
    const totalMinutes = Math.max(0, Math.floor(diffMs / 60000));
    const days = Math.floor(totalMinutes / (60 * 24));
    const hours = Math.floor((totalMinutes % (60 * 24)) / 60);
    const mins = totalMinutes % 60;
    if (days > 0) return mins > 0 ? `${days}d ${hours}h ${mins}m` : `${days}d ${hours}h`;
    if (hours > 0) return `${hours}h ${mins}m`;
    return `${mins}m`;
  }

  function openAIExample() {
    return `curl ${openAIEndpoint || 'http://127.0.0.1:3061/v1/chat/completions'} \\\n  -H "Authorization: Bearer ${apiKey || 'sk-...'}" \\\n  -H "Content-Type: application/json" \\\n  -d '{
    "model": "gpt-5.2-codex",
    "messages": [{"role":"user","content":"Reply exactly with: OK"}],
    "stream": false
  }'`;
  }

  function claudeExample() {
    return `curl ${claudeEndpoint || 'http://127.0.0.1:3061/v1/messages'} \\\n  -H "x-api-key: ${apiKey || 'sk-...'}" \\\n  -H "Content-Type: application/json" \\\n  -d '{
    "model": "gpt-5.2-codex",
    "max_tokens": 512,
    "messages": [{"role":"user","content":"Reply exactly with: OK"}],
    "stream": false
  }'`;
  }

  function authJSONExample() {
    return `curl ${authJSONEndpoint || 'http://127.0.0.1:3061/v1/auth.json'} \\\n  -H "Authorization: Bearer ${apiKey || 'sk-...'}" \\\n  -o auth.json`;
  }

  function prettyJSONText(raw) {
    const text = String(raw ?? '').trim();
    if (!text) return '';
    try {
      return JSON.stringify(JSON.parse(text), null, 2);
    } catch {
      return text;
    }
  }

  function parseAPILogLine(line, index) {
    const rawLine = String(line ?? '');
    const fallback = {
      id: `raw-${index}`,
      rawLine,
      timestamp: null,
      protocol: 'unknown',
      method: '-',
      path: '(raw log line)',
      status: 0,
      latencyMS: 0,
      model: '',
      accountHint: '',
      accountID: '',
      accountEmail: '',
      requestBody: '',
      responseBody: '',
      invalid: true
    };
    if (!rawLine.trim()) return fallback;
    try {
      const obj = JSON.parse(rawLine);
      const timestamp = typeof obj.timestamp === 'string' ? obj.timestamp : null;
      const protocol = String(obj.protocol || 'unknown').toLowerCase();
      const method = String(obj.method || '-').toUpperCase();
      const path = String(obj.path || '/');
      const status = Number(obj.status) || 0;
      const latencyMS = Number(obj.latency_ms) || 0;
      const model = String(obj.model || '').trim();
      const accountHint = String(obj.account_hint || '').trim();
      const accountID = String(obj.account_id || '').trim();
      const accountEmail = String(obj.account_email || '').trim();
      const requestBody = prettyJSONText(obj.request_body);
      const responseBody = prettyJSONText(obj.response_body);
      return {
        id: `${timestamp || 'ts'}-${index}-${path}-${status}`,
        rawLine,
        timestamp,
        protocol,
        method,
        path,
        status,
        latencyMS,
        model,
        accountHint,
        accountID,
        accountEmail,
        requestBody,
        responseBody,
        invalid: false
      };
    } catch {
      return fallback;
    }
  }

  function formatLogTimestamp(ts) {
    if (!ts) return '-';
    const d = new Date(ts);
    if (Number.isNaN(d.getTime())) return '-';
    return d.toLocaleString();
  }

  function logStatusClass(statusCode) {
    const n = Number(statusCode) || 0;
    if (n >= 500) return 'status-5xx';
    if (n >= 400) return 'status-4xx';
    if (n >= 300) return 'status-3xx';
    if (n >= 200) return 'status-2xx';
    return 'status-unknown';
  }

  function openLogDetail(entry) {
    logDetailEntry = entry;
    showLogDetailModal = true;
  }

  function closeLogDetailModal() {
    showLogDetailModal = false;
    logDetailEntry = null;
  }

  async function req(url, options = {}) {
    const targetURL = toAPIURL(url);
    const response = await fetch(targetURL, {
      headers: jsonHeaders,
      ...options
    });
    if (response.redirected && String(response.url || '').includes('/auth/login')) {
      if (typeof window !== 'undefined') {
        window.location.href = '/auth/login';
      }
      throw new Error('Authentication required');
    }
    const bodyText = await response.text();
    if (response.ok) {
      const contentType = String(response.headers.get('content-type') || '').toLowerCase();
      if (contentType.includes('text/html') && /<html/i.test(bodyText) && /login/i.test(bodyText)) {
        if (typeof window !== 'undefined') {
          window.location.href = '/auth/login';
        }
        throw new Error('Authentication required');
      }
    }
    let body = {};
    try {
      body = JSON.parse(bodyText || '{}');
    } catch {
      body = {};
    }
    if (!response.ok) {
      const msg = typeof body?.error === 'string'
        ? body.error
        : (body?.error?.message || body?.message || bodyText || `HTTP ${response.status}`);
      throw new Error(msg);
    }
    return body;
  }

  async function loadAccounts() {
    const data = await req('/api/accounts');
    accounts = data.accounts || [];
  }

  async function loadSettings() {
    const data = await req('/api/settings');
    apiKey = data.api_key || '';
    apiMode = String(data.api_mode || 'codex_cli').trim().toLowerCase() === 'direct_api' ? 'direct_api' : 'codex_cli';
    openAIEndpoint = data.openai_endpoint || '';
    claudeEndpoint = data.claude_endpoint || '';
    authJSONEndpoint = data.auth_json_endpoint || '';
    const fromAPI = Array.isArray(data.available_models) ? data.available_models : [];
    availableModels = fromAPI.length > 0 ? fromAPI : defaultCodexModels;
    modelMappings = (data.model_mappings && typeof data.model_mappings === 'object') ? data.model_mappings : {};
    const alertThreshold = Number(data.usage_alert_threshold);
    if (Number.isFinite(alertThreshold)) {
      usageAlertThreshold = parsePercentInput(alertThreshold, usageAlertThreshold);
      usageAlertThresholdInput = String(usageAlertThreshold);
      usageThresholdLastSavedAlert = usageAlertThreshold;
    }
    const autoSwitchThreshold = Number(data.usage_auto_switch_threshold);
    if (Number.isFinite(autoSwitchThreshold)) {
      usageAutoSwitchThreshold = parsePercentInput(autoSwitchThreshold, usageAutoSwitchThreshold);
      usageAutoSwitchThresholdInput = String(usageAutoSwitchThreshold);
      usageThresholdLastSavedSwitch = usageAutoSwitchThreshold;
    }
    if (!availableModels.includes(mappingTargetModel) && availableModels.length > 0) {
      mappingTargetModel = availableModels[0];
    }
    appVersion = String(data.app_version || 'dev').trim() || 'dev';
    codexVersion = String(data.codex_version || 'unknown').trim() || 'unknown';
    latestVersion = String(data.latest_version || '').trim();
    releaseURL = String(data.release_url || '').trim();
    latestChangelog = String(data.latest_changelog || '').trim();
    updateAvailable = Boolean(data.update_available);
    updateCheckedAt = String(data.update_checked_at || '').trim();
    updateCheckError = String(data.update_check_error || '').trim();
  }

  async function checkForUpdates() {
    updateCheckBusy = true;
    try {
      const data = await req('/api/version/check', { method: 'POST', body: JSON.stringify({}) });
      appVersion = String(data.app_version || appVersion).trim() || appVersion;
      latestVersion = String(data.latest_version || '').trim();
      releaseURL = String(data.release_url || '').trim();
      latestChangelog = String(data.latest_changelog || '').trim();
      updateAvailable = Boolean(data.update_available);
      updateCheckedAt = String(data.update_checked_at || '').trim();
      updateCheckError = String(data.update_check_error || '').trim();
      if (updateAvailable) {
        setStatus(`Update available: v${latestVersion}`, 'info');
      } else {
        setStatus(`You are using the latest version (${appVersion}).`, 'success');
      }
    } catch (error) {
      updateCheckError = String(error?.message || 'failed to check updates');
      setStatus(updateCheckError, 'error');
    } finally {
      updateCheckBusy = false;
    }
  }

  async function loadAPILogs() {
    const data = await req('/api/logs?limit=400');
    const lines = Array.isArray(data.lines) ? data.lines : [];
    apiLogs = [...lines].reverse().map((line, idx) => parseAPILogLine(line, idx));
  }

  async function refreshUsageForSelectors(selectors) {
    const uniqueSelectors = [...new Set((selectors || []).map((v) => String(v || '').trim()).filter(Boolean))];
    let refreshed = 0;
    const ran = await withUsageRefreshLock(async () => {
      for (const selector of uniqueSelectors) {
        try {
          await req('/api/usage/refresh', {
            method: 'POST',
            body: JSON.stringify({ selector })
          });
          refreshed++;
        } catch {
          // continue refreshing remaining selectors
        }
      }
      await refreshAllData();
    });
    if (!ran) {
      return { refreshed: 0, total: uniqueSelectors.length };
    }
    backgroundRefreshError = '';
    backgroundRefreshLastAt = Date.now();
    return { refreshed, total: uniqueSelectors.length };
  }

  async function refreshAllData({ statusMessage = true, force = false } = {}) {
    const now = Date.now();
    if (!force && lastRefreshAllAt > 0 && now - lastRefreshAllAt < 1200) {
      return;
    }
    if (refreshAllInFlight) {
      await refreshAllInFlight;
      return;
    }
    refreshAllInFlight = Promise.all([loadAccounts(), loadSettings()]);
    try {
      await refreshAllInFlight;
      lastRefreshAllAt = Date.now();
    } finally {
      refreshAllInFlight = null;
    }
    if (statusMessage) {
      setStatus(`Loaded ${accounts.length} account(s).`, 'success');
    }
    await evaluateActiveAccountUsage({ allowAutoSwitch: true, source: 'refresh-all-data' });
  }

  async function useAccount(
    selector,
    {
      source = 'manual',
      suppressTone = false,
      suppressStatus = false,
      refreshStatusMessage = true,
      mode = 'api'
    } = {}
  ) {
    busy = true;
    try {
      const endpoint = mode === 'cli' ? '/api/account/use-cli' : '/api/account/use-api';
      const toneKind = source === 'auto' ? 'switch' : 'info';
      if (!suppressTone && source !== 'auto') {
        await playNotificationTone(toneKind, { wait: true });
      }
      await req(endpoint, {
        method: 'POST',
        body: JSON.stringify({ selector })
      });
      await refreshAllData({ statusMessage: refreshStatusMessage });
      if (!suppressTone && source === 'auto') {
        playNotificationTone(toneKind);
      }
      if (!suppressStatus) {
        setStatus(`${mode === 'cli' ? 'Codex CLI' : 'API'} account switched to ${selector}.`, 'success');
      }
    } catch (error) {
      setStatus(error.message, 'error');
      throw error;
    } finally {
      busy = false;
    }
  }

  async function useCLIAccount(selector) {
    return useAccount(selector, { mode: 'cli', source: 'manual' });
  }

  async function refreshUsage(selector) {
    if (usageRefreshBusy) {
      setStatus('Usage refresh is still running.', 'info');
      return;
    }
    busy = true;
    try {
      await withUsageRefreshLock(async () => {
        await req('/api/usage/refresh', {
          method: 'POST',
          body: JSON.stringify({ selector })
        });
        await refreshAllData();
      });
      backgroundRefreshError = '';
      backgroundRefreshLastAt = Date.now();
      setStatus(`Usage refreshed for ${selector}.`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function refreshAllUsage() {
    if (usageRefreshBusy) {
      setStatus('Usage refresh is still running.', 'info');
      return;
    }
    busy = true;
    try {
      await withUsageRefreshLock(async () => {
        await req('/api/usage/refresh', {
          method: 'POST',
          body: JSON.stringify({ all: true })
        });
        await refreshAllData();
      });
      backgroundRefreshError = '';
      backgroundRefreshLastAt = Date.now();
      setStatus('Usage refreshed for all accounts.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  function openRemoveModal(account) {
    removeCandidate = account;
    showRemoveModal = true;
  }

  function closeRemoveModal() {
    if (busy) return;
    showRemoveModal = false;
    removeCandidate = null;
  }

  async function removeAccount() {
    if (!removeCandidate?.id) return;
    let removed = false;
    busy = true;
    try {
      await req('/api/account/remove', {
        method: 'POST',
        body: JSON.stringify({ selector: removeCandidate.id })
      });
      await refreshAllData();
      setStatus(`Removed account ${removeCandidate.email || removeCandidate.id}.`, 'success');
      removed = true;
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
      if (removed) {
        closeRemoveModal();
      }
    }
  }

  async function regenerateAPIKey() {
    settingsBusy = true;
    try {
      const data = await req('/api/settings/api-key', {
        method: 'POST',
        body: JSON.stringify({ regenerate: true })
      });
      apiKey = data.api_key || '';
      setStatus('API key regenerated.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      settingsBusy = false;
    }
  }

  function markCopied(key) {
    if (!key) return;
    copiedAction = key;
    if (copiedResetTimer) {
      clearTimeout(copiedResetTimer);
      copiedResetTimer = null;
    }
    copiedResetTimer = setTimeout(() => {
      if (copiedAction === key) copiedAction = '';
      copiedResetTimer = null;
    }, 1300);
  }

  function isCopied(key) {
    return copiedAction === key;
  }

  async function writeClipboardText(text) {
    const value = String(text || '');
    if (!value) return false;

    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
        return true;
      }
    } catch {
      // Fallback below for non-secure context / denied clipboard permissions.
    }

    try {
      const textarea = document.createElement('textarea');
      textarea.value = value;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      textarea.style.pointerEvents = 'none';
      textarea.style.top = '-1000px';
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      textarea.setSelectionRange(0, textarea.value.length);
      const ok = Boolean(document.execCommand && document.execCommand('copy'));
      document.body.removeChild(textarea);
      return ok;
    } catch {
      return false;
    }
  }

  async function copyText(value, label, key = '') {
    const text = String(value || '').trim();
    if (!text) return;
    const copied = await writeClipboardText(text);
    if (copied) {
      markCopied(key);
      setStatus(`${label} copied.`, 'success');
    } else {
      setStatus(`Failed to copy ${label}.`, 'error');
    }
  }

  function setMappingAlias(value) {
    mappingAlias = value;
  }

  function setMappingTargetModel(value) {
    mappingTargetModel = value;
  }

  function toggleShowAccountEmail() {
    showAccountEmail = !showAccountEmail;
  }

  function toggleAutoRefreshEnabled() {
    autoRefreshEnabled = !autoRefreshEnabled;
    if (autoRefreshEnabled) {
      runBackgroundUsageRefresh({ silent: true, force: false });
    }
  }

  function toggleUsageSoundEnabled() {
    usageSoundEnabled = !usageSoundEnabled;
    if (usageSoundEnabled) {
      playNotificationTone('info');
      setTimeout(() => {
        playNotificationTone('switch');
      }, 140);
    }
  }

  async function setAPIMode(nextMode) {
    const normalized = String(nextMode || '').trim().toLowerCase() === 'direct_api' ? 'direct_api' : 'codex_cli';
    if (apiMode === normalized) return;
    settingsBusy = true;
    try {
      const data = await req('/api/settings', {
        method: 'POST',
        body: JSON.stringify({ api_mode: normalized })
      });
      apiMode = String(data.api_mode || normalized).trim().toLowerCase() === 'direct_api' ? 'direct_api' : 'codex_cli';
      setStatus(`API mode changed to ${apiMode === 'direct_api' ? 'Direct API' : 'Codex CLI'}.`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      settingsBusy = false;
    }
  }

  function setAutoRefreshMinutesInput(value) {
    autoRefreshMinutesInput = String(value ?? '');
    const trimmed = autoRefreshMinutesInput.trim();
    if (!trimmed) return;
    const n = Number(trimmed);
    if (!Number.isFinite(n)) return;
    autoRefreshMinutes = Math.max(1, Math.round(n));
  }

  function commitAutoRefreshMinutesInput() {
    const trimmed = String(autoRefreshMinutesInput || '').trim();
    const n = Number(trimmed);
    if (!trimmed || !Number.isFinite(n)) {
      autoRefreshMinutesInput = String(autoRefreshMinutes);
      return;
    }
    const normalized = Math.max(1, Math.round(n));
    autoRefreshMinutes = normalized;
    autoRefreshMinutesInput = String(normalized);
  }

  function setUsageAlertThresholdInput(value) {
    const normalized = parsePercentInput(value, usageAlertThreshold);
    usageAlertThreshold = normalized;
    usageAlertThresholdInput = String(normalized);
  }

  function commitUsageAlertThresholdInput(nextValue = null) {
    const sourceValue = nextValue === null || nextValue === undefined ? usageAlertThresholdInput : nextValue;
    const normalized = parsePercentInput(sourceValue, usageAlertThreshold);
    usageAlertThreshold = normalized;
    usageAlertThresholdInput = String(normalized);
    queuePersistUsageThresholdSettings('alert');
  }

  function nudgeUsageAlertThreshold(delta) {
    const next = parsePercentInput(Number(usageAlertThreshold) + Number(delta), usageAlertThreshold);
    usageAlertThreshold = next;
    usageAlertThresholdInput = String(next);
    queuePersistUsageThresholdSettings('alert');
  }

  function setUsageAutoSwitchThresholdInput(value) {
    const normalized = parsePercentInput(value, usageAutoSwitchThreshold);
    usageAutoSwitchThreshold = normalized;
    usageAutoSwitchThresholdInput = String(normalized);
  }

  function commitUsageAutoSwitchThresholdInput(nextValue = null) {
    const sourceValue = nextValue === null || nextValue === undefined ? usageAutoSwitchThresholdInput : nextValue;
    const normalized = parsePercentInput(sourceValue, usageAutoSwitchThreshold);
    usageAutoSwitchThreshold = normalized;
    usageAutoSwitchThresholdInput = String(normalized);
    queuePersistUsageThresholdSettings('switch');
  }

  function nudgeUsageAutoSwitchThreshold(delta) {
    const next = parsePercentInput(Number(usageAutoSwitchThreshold) + Number(delta), usageAutoSwitchThreshold);
    usageAutoSwitchThreshold = next;
    usageAutoSwitchThresholdInput = String(next);
    queuePersistUsageThresholdSettings('switch');
  }

  function queuePersistUsageThresholdSettings(changedType) {
    usageThresholdPersistQueued = {
      alertThreshold: parsePercentInput(usageAlertThreshold, 5),
      autoSwitchThreshold: parsePercentInput(usageAutoSwitchThreshold, 2),
      changedType
    };
    if (usageThresholdPersistTimer) {
      clearTimeout(usageThresholdPersistTimer);
      usageThresholdPersistTimer = null;
    }
    usageThresholdPersistTimer = setTimeout(() => {
      usageThresholdPersistTimer = null;
      void flushUsageThresholdSettingsPersist();
    }, 220);
  }

  async function flushUsageThresholdSettingsPersist() {
    if (usageThresholdPersistInFlight) return;
    const next = usageThresholdPersistQueued;
    if (!next) return;
    usageThresholdPersistQueued = null;

    const alertThreshold = parsePercentInput(next.alertThreshold, 5);
    const autoSwitchThreshold = parsePercentInput(next.autoSwitchThreshold, 2);
    const changedType = next.changedType;
    if (
      usageThresholdLastSavedAlert === alertThreshold &&
      usageThresholdLastSavedSwitch === autoSwitchThreshold
    ) {
      return;
    }

    usageThresholdPersistInFlight = true;
    try {
      const data = await req('/api/settings', {
        method: 'POST',
        body: JSON.stringify({
          usage_alert_threshold: alertThreshold,
          usage_auto_switch_threshold: autoSwitchThreshold
        })
      });
      const savedAlert = parsePercentInput(Number(data.usage_alert_threshold), usageAlertThreshold);
      const savedSwitch = parsePercentInput(Number(data.usage_auto_switch_threshold), usageAutoSwitchThreshold);
      usageAlertThreshold = savedAlert;
      usageAlertThresholdInput = String(savedAlert);
      usageAutoSwitchThreshold = savedSwitch;
      usageAutoSwitchThresholdInput = String(savedSwitch);
      usageThresholdLastSavedAlert = savedAlert;
      usageThresholdLastSavedSwitch = savedSwitch;
      if (changedType === 'alert') {
        setStatus(`Usage alert threshold set to ${savedAlert}%.`, 'success');
      } else {
        setStatus(`Usage auto-switch threshold set to ${savedSwitch}%.`, 'success');
      }
    } catch (error) {
      setStatus(`Failed to save usage thresholds: ${error.message}`, 'error');
    } finally {
      usageThresholdPersistInFlight = false;
      if (usageThresholdPersistQueued) {
        if (usageThresholdPersistTimer) {
          clearTimeout(usageThresholdPersistTimer);
          usageThresholdPersistTimer = null;
        }
        usageThresholdPersistTimer = setTimeout(() => {
          usageThresholdPersistTimer = null;
          void flushUsageThresholdSettingsPersist();
        }, 120);
      }
    }
  }

  async function withUsageRefreshLock(task) {
    if (usageRefreshBusy) return false;
    usageRefreshBusy = true;
    try {
      await task();
      return true;
    } finally {
      usageRefreshBusy = false;
    }
  }

  async function runBackgroundUsageRefresh({ silent = true, force = false } = {}) {
    if (autoRefreshBusy) return;
    const intervalMS = Math.max(1, Number(autoRefreshMinutes) || 30) * 60 * 1000;
    if (!force && backgroundRefreshLastAt > 0 && Date.now() - backgroundRefreshLastAt < intervalMS) {
      return;
    }
    autoRefreshBusy = true;
    sendClientEvent('background-refresh-start', 'Background usage refresh started.', {
      force,
      minutes: Math.max(1, Number(autoRefreshMinutes) || 30)
    });
    try {
      const ran = await withUsageRefreshLock(async () => {
        await req('/api/usage/refresh', {
          method: 'POST',
          body: JSON.stringify({ all: true })
        });
        await Promise.all([loadAccounts(), loadSettings()]);
      });
      if (!ran) return;
      backgroundRefreshLastAt = Date.now();
      sendClientEvent('background-refresh-success', 'Background usage refresh completed.', {
        accounts: Array.isArray(accounts) ? accounts.length : 0
      }, 'success');
      await evaluateActiveAccountUsage({ allowAutoSwitch: true, source: 'background-refresh' });
      if (backgroundRefreshError && !silent) {
        setStatus('Background usage refresh recovered.', 'success');
      }
      backgroundRefreshError = '';
    } catch (error) {
      const message = String(error?.message || 'unknown error');
      const isNewError = backgroundRefreshError !== message;
      backgroundRefreshError = message;
      sendClientEvent('background-refresh-failed', `Background refresh failed: ${message}`, {
        force
      }, 'error');
      if (isNewError && !silent) {
        setStatus(`Background auto refresh failed: ${message}`, 'error');
      }
    } finally {
      autoRefreshBusy = false;
    }
  }

  function startEditMapping(alias) {
    const key = String(alias || '').trim();
    if (!key) return;
    editingMappingAlias = key;
    mappingAlias = key;
    const current = String(modelMappings[key] || '').trim();
    if (current && availableModels.includes(current)) {
      mappingTargetModel = current;
      return;
    }
    if (availableModels.length > 0) {
      mappingTargetModel = availableModels[0];
      return;
    }
    mappingTargetModel = 'gpt-5.2-codex';
  }

  function cancelEditMapping() {
    editingMappingAlias = '';
    mappingAlias = '';
    mappingTargetModel = availableModels[0] || 'gpt-5.2-codex';
  }

  async function saveModelMapping() {
    const alias = String(mappingAlias || '').trim();
    const model = String(mappingTargetModel || '').trim();
    const previousAlias = String(editingMappingAlias || '').trim();
    const isRename = previousAlias && previousAlias !== alias;
    if (!alias) {
      setStatus('Mapping alias is required.', 'error');
      return;
    }
    if (!model) {
      setStatus('Target model is required.', 'error');
      return;
    }
    settingsBusy = true;
    try {
      const data = await req('/api/model-mappings', {
        method: 'POST',
        body: JSON.stringify({ alias, model })
      });
      modelMappings = (data.mappings && typeof data.mappings === 'object') ? data.mappings : modelMappings;
      if (isRename) {
        try {
          const deleted = await req(`/api/model-mappings?alias=${encodeURIComponent(previousAlias)}`, {
            method: 'DELETE'
          });
          modelMappings = (deleted.mappings && typeof deleted.mappings === 'object') ? deleted.mappings : modelMappings;
        } catch (error) {
          setStatus(`Mapping saved as ${alias}, but old alias ${previousAlias} was not removed: ${error.message}`, 'error');
          return;
        }
      }
      editingMappingAlias = '';
      mappingAlias = '';
      setStatus(`Model mapping saved: ${alias} -> ${model}`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      settingsBusy = false;
    }
  }

  async function deleteModelMapping(alias) {
    const key = String(alias || '').trim();
    if (!key) return;
    settingsBusy = true;
    try {
      const data = await req(`/api/model-mappings?alias=${encodeURIComponent(key)}`, {
        method: 'DELETE'
      });
      modelMappings = (data.mappings && typeof data.mappings === 'object') ? data.mappings : modelMappings;
      if (mappingAlias === key) {
        mappingAlias = '';
      }
      if (editingMappingAlias === key) {
        editingMappingAlias = '';
      }
      setStatus(`Model mapping removed: ${key}`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      settingsBusy = false;
    }
  }

  function openAddAccountModal() {
    showAddAccountModal = true;
    addAccountMode = 'menu';
  }

  function clearPollTimer() {
    if (pollTimer) {
      clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function clearBrowserWaitTimer() {
    if (browserWaitTimer) {
      clearInterval(browserWaitTimer);
      browserWaitTimer = null;
    }
  }

  function cancelBrowserLoginSession() {
    if (!browserLoginURL && !browserLoginID) return;
    const payload = JSON.stringify({ login_id: browserLoginID || null });
    try {
      if (typeof navigator !== 'undefined' && typeof navigator.sendBeacon === 'function') {
        const blob = new Blob([payload], { type: 'application/json' });
        navigator.sendBeacon('/api/auth/browser/cancel', blob);
        return;
      }
    } catch {
      // fallback to fetch
    }
    fetch('/api/auth/browser/cancel', {
      method: 'POST',
      headers: jsonHeaders,
      body: payload,
      keepalive: true
    }).catch(() => {});
  }

  function closeAddAccountModal() {
    cancelBrowserLoginSession();
    showAddAccountModal = false;
    addAccountMode = 'menu';
    browserWaiting = false;
    browserLoginURL = '';
    browserLoginID = '';
    browserCallbackURL = '';
    deviceLogin = null;
    deviceCodeCopied = false;
    clearPollTimer();
    clearBrowserWaitTimer();
  }

  async function startBrowserLogin() {
    busy = true;
    try {
      const data = await req('/api/auth/browser/start', {
        method: 'POST',
        body: JSON.stringify({})
      });
      const authURL = data.auth_url || '';
      const loginID = data.login_id || '';
      if (!authURL) {
        throw new Error('missing auth_url');
      }
      addAccountMode = 'browser';
      browserLoginURL = authURL;
      browserLoginID = loginID;
      browserCallbackURL = '';
      browserWaiting = false;
      browserKnownIDs = accounts.map((a) => a.id);
      setStatus('Browser login URL ready. Click Open New Tab.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  function scheduleBrowserWait() {
    clearBrowserWaitTimer();
    browserWaitTimer = setInterval(() => {
      checkBrowserLoginResult();
    }, 2500);
  }

  async function checkBrowserLoginResult() {
    if (!browserWaiting) return;
    try {
      await loadAccounts();
      const nowIDs = accounts.map((a) => a.id);
      const newIDs = nowIDs.filter((id) => !browserKnownIDs.includes(id));
      if (newIDs.length > 0) {
        browserWaiting = false;
        browserLoginURL = '';
        browserLoginID = '';
        clearBrowserWaitTimer();
        closeAddAccountModal();
        setStatus('Browser callback login success. Account added.', 'success');
        const usageRefresh = await refreshUsageForSelectors(newIDs);
        if (usageRefresh.refreshed > 0) {
          setStatus(`Browser callback login success. Usage refreshed for ${usageRefresh.refreshed}/${usageRefresh.total} account(s).`, 'success');
        }
      }
    } catch {
      // keep waiting silently
    }
  }

  function openBrowserLoginTab() {
    if (!browserLoginURL) return;
    window.open(browserLoginURL, '_blank', 'noopener,noreferrer');
    browserWaiting = true;
    setStatus('Waiting callback from browser login...', 'info');
    scheduleBrowserWait();
  }

  async function submitManualBrowserCallback() {
    const callbackURL = String(browserCallbackURL || '').trim();
    const loginID = String(browserLoginID || '').trim();
    if (!loginID) {
      setStatus('Browser login session not ready.', 'error');
      return;
    }
    if (!callbackURL) {
      setStatus('Callback URL is required.', 'error');
      return;
    }
    busy = true;
    try {
      const data = await req('/api/auth/browser/complete', {
        method: 'POST',
        body: JSON.stringify({ login_id: loginID, callback_url: callbackURL })
      });
      const accountID = String(data?.account?.id || '').trim();
      closeAddAccountModal();
      setStatus('Browser callback login success. Account added.', 'success');
      if (accountID) {
        const usageRefresh = await refreshUsageForSelectors([accountID]);
        if (usageRefresh.refreshed > 0) {
          setStatus(`Browser callback login success. Usage refreshed for ${usageRefresh.refreshed}/${usageRefresh.total} account(s).`, 'success');
        }
      }
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function startDeviceLogin() {
    busy = true;
    try {
      const data = await req('/api/auth/device/start', {
        method: 'POST',
        body: JSON.stringify({})
      });
      addAccountMode = 'device';
      deviceLogin = data.login;
      deviceCodeCopied = false;
      setStatus('Device code generated. Login on OpenAI page, then wait for verification.', 'success');
      scheduleDevicePoll();
      pollDeviceLogin();
    } catch (error) {
      const msg = String(error.message || '');
      if (msg.toLowerCase().includes('cloudflare challenge') || msg.toLowerCase().includes('use browser login')) {
        addAccountMode = 'browser';
        setStatus('Device login blocked by Cloudflare. Switched to Browser Login flow.', 'error');
      } else {
        setStatus(msg, 'error');
      }
    } finally {
      busy = false;
    }
  }

  function scheduleDevicePoll() {
    clearPollTimer();
    if (!deviceLogin?.login_id) return;
    const sec = Math.max(3, Number(deviceLogin.interval_seconds || 5));
    pollTimer = setInterval(() => {
      pollDeviceLogin();
    }, sec * 1000);
  }

  async function pollDeviceLogin() {
    if (!deviceLogin?.login_id) return;
    try {
      const data = await req('/api/auth/device/poll', {
        method: 'POST',
        body: JSON.stringify({ login_id: deviceLogin.login_id })
      });
      const result = data.result || {};
      if (result.status === 'success') {
        clearPollTimer();
        const accountID = String(result?.account?.id || '').trim();
        deviceLogin = null;
        if (accountID) {
          const usageRefresh = await refreshUsageForSelectors([accountID]);
          if (usageRefresh.refreshed > 0) {
            setStatus('Device login success. Account added, active, and usage refreshed.', 'success');
          } else {
            setStatus('Device login success. Account added and active.', 'success');
          }
        } else {
          await refreshAllData();
          setStatus('Device login success. Account added and active.', 'success');
        }
        closeAddAccountModal();
        return;
      }
      if (result.status === 'pending') {
        setStatus('Waiting for device login verification...', 'info');
        return;
      }
      clearPollTimer();
      deviceLogin = null;
      setStatus(result.error || `Device login status: ${result.status}`, 'error');
    } catch (error) {
      setStatus(error.message, 'error');
    }
  }

  async function copyDeviceCode() {
    const code = deviceLogin?.user_code || '';
    if (!code) return;
    await copyText(code, 'Device code', 'device_code');
    deviceCodeCopied = isCopied('device_code');
  }

  function loadUIPreferences() {
    try {
      const raw = localStorage.getItem(uiPrefsKey);
      if (raw) {
        const parsed = JSON.parse(raw);
        showAccountEmail = parsed?.showAccountEmail !== false;
        autoRefreshEnabled = parsed?.autoRefreshEnabled === true;
        const mins = Number(parsed?.autoRefreshMinutes);
        autoRefreshMinutes = Number.isFinite(mins) ? Math.max(1, Math.round(mins)) : 30;
        autoRefreshMinutesInput = String(autoRefreshMinutes);
        const alertThreshold = Number(parsed?.usageAlertThreshold);
        usageAlertThreshold = Number.isFinite(alertThreshold) ? parsePercentInput(alertThreshold, 5) : 5;
        usageAlertThresholdInput = String(usageAlertThreshold);
        const autoSwitchThreshold = Number(parsed?.usageAutoSwitchThreshold);
        usageAutoSwitchThreshold = Number.isFinite(autoSwitchThreshold) ? parsePercentInput(autoSwitchThreshold, 2) : 2;
        usageAutoSwitchThresholdInput = String(usageAutoSwitchThreshold);
        usageSoundEnabled = parsed?.usageSoundEnabled !== false;
        const lastAt = Number(parsed?.backgroundRefreshLastAt || 0);
        backgroundRefreshLastAt = Number.isFinite(lastAt) ? Math.max(0, Math.round(lastAt)) : 0;
      }
    } catch {
      showAccountEmail = true;
      autoRefreshEnabled = false;
      autoRefreshMinutes = 30;
      autoRefreshMinutesInput = '30';
      usageAlertThreshold = 5;
      usageAlertThresholdInput = '5';
      usageAutoSwitchThreshold = 2;
      usageAutoSwitchThresholdInput = '2';
      usageSoundEnabled = true;
      backgroundRefreshLastAt = 0;
    }
    uiPrefsLoaded = true;
  }

  function saveUIPreferences() {
    try {
      const payload = {
        showAccountEmail,
        autoRefreshEnabled,
        autoRefreshMinutes: Math.max(1, Number(autoRefreshMinutes) || 30),
        usageAlertThreshold: parsePercentInput(usageAlertThreshold, 5),
        usageAutoSwitchThreshold: parsePercentInput(usageAutoSwitchThreshold, 2),
        usageSoundEnabled,
        backgroundRefreshLastAt
      };
      localStorage.setItem(uiPrefsKey, JSON.stringify(payload));
    } catch {
    }
  }

  onMount(() => {
    syncRouteMode();
    const onBeforeUnload = () => {
      cancelBrowserLoginSession();
    };
    const onPopstate = () => {
      syncRouteMode();
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    window.addEventListener('popstate', onPopstate);
    loadUIPreferences();

    if (!isChatRoute) {
      refreshAllData().catch((error) => {
        setStatus(error.message, 'error');
      });
    }

    const onResize = () => {
      if (window.innerWidth > 760) closeMobileSidebar();
    };
    const onEsc = (event) => {
      if (event.key === 'Escape') closeMobileSidebar();
    };
    window.addEventListener('resize', onResize);
    window.addEventListener('keydown', onEsc);

    return () => {
      window.removeEventListener('beforeunload', onBeforeUnload);
      window.removeEventListener('popstate', onPopstate);
      window.removeEventListener('resize', onResize);
      window.removeEventListener('keydown', onEsc);
      cancelBrowserLoginSession();
      clearPollTimer();
      clearBrowserWaitTimer();
      if (copiedResetTimer) {
        clearTimeout(copiedResetTimer);
        copiedResetTimer = null;
      }
      if (usageThresholdPersistTimer) {
        clearTimeout(usageThresholdPersistTimer);
        usageThresholdPersistTimer = null;
      }
    };
  });

  $effect(() => {
    const timer = setInterval(() => {
      nowTick = Date.now();
    }, 30000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (activeMenu !== 'logs') return;
    loadAPILogs().catch((error) => setStatus(error.message, 'error'));
    const timer = setInterval(() => {
      loadAPILogs().catch(() => {});
    }, 5000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (!autoRefreshEnabled) return;
    const minutes = Math.max(1, Number(autoRefreshMinutes) || 30);
    const timer = setInterval(() => {
      runBackgroundUsageRefresh({ silent: true, force: true });
    }, minutes * 60 * 1000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    const onVisible = () => {
      if (!autoRefreshEnabled) return;
      if (document.visibilityState !== 'visible') return;
      runBackgroundUsageRefresh({ silent: true, force: false });
    };
    const onFocus = () => {
      if (!autoRefreshEnabled) return;
      runBackgroundUsageRefresh({ silent: true, force: false });
    };
    document.addEventListener('visibilitychange', onVisible);
    window.addEventListener('focus', onFocus);
    return () => {
      document.removeEventListener('visibilitychange', onVisible);
      window.removeEventListener('focus', onFocus);
    };
  });

  $effect(() => {
    usageAlertThreshold;
    usageAutoSwitchThreshold;
    if (!uiPrefsLoaded) return;
    evaluateActiveAccountUsage({ allowAutoSwitch: false, source: 'threshold-change' });
  });

  $effect(() => {
    if (!uiPrefsLoaded) return;
    saveUIPreferences();
  });
</script>

<svelte:head>
  <title>{isChatRoute ? `Chat - CodexSess Console` : documentTitleByMenu(activeMenu)}</title>
  <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />
  <link
    href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500;600&family=IBM+Plex+Sans:wght@400;500;600;700&display=swap"
    rel="stylesheet"
  />
</svelte:head>

<div class="app-root {mobileSidebarOpen ? 'mobile-sidebar-open' : ''} {isChatRoute ? 'chat-route' : ''}" data-app-mode={appMode}>
  {#if !isChatRoute}
  <aside class="sidebar {mobileSidebarOpen ? 'is-open' : ''}">
    <div class="brand">
      <strong>CodexSess</strong>
      <span>Codex Account Management</span>
      <small class="brand-meta">Codex CLI: {codexVersion}</small>
    </div>

    <nav class="nav" aria-label="Main menu">
      <button class={activeMenu === 'dashboard' ? 'is-active' : ''} onclick={() => switchMenu('dashboard')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M3 3h8v8H3V3zm10 0h8v5h-8V3zM3 13h5v8H3v-8zm7 0h11v8H10v-8z"></path></svg>
        </span>
        <span>Dashboard</span>
      </button>
      <button class={activeMenu === 'coding' ? 'is-active' : ''} onclick={() => switchMenu('coding')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M4 5h16v14H4V5zm2 2v10h12V7H6zm2 2h8v2H8V9zm0 4h6v2H8v-2z"></path></svg>
        </span>
        <span>Chat</span>
      </button>
      <button class={activeMenu === 'api-endpoints' ? 'is-active' : ''} onclick={() => switchMenu('api-endpoints')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M3 5h18v4H3V5zm0 5.5h18v4H3v-4zM3 16h18v3H3v-3z"></path></svg>
        </span>
        <span>API Workspace</span>
      </button>
      <button class={activeMenu === 'logs' ? 'is-active' : ''} onclick={() => switchMenu('logs')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M4 4h16v4H4V4zm0 6h16v10H4V10zm3 3v4h2v-4H7zm4 0v4h2v-4h-2zm4 0v4h2v-4h-2z"></path></svg>
        </span>
        <span>API Logs</span>
      </button>
      <button class={activeMenu === 'settings' ? 'is-active' : ''} onclick={() => switchMenu('settings')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M19.14 12.94a7.96 7.96 0 000-1.88l2.03-1.58-1.92-3.32-2.39.96a8.1 8.1 0 00-1.62-.94L14.9 3h-3.8l-.34 2.18c-.56.22-1.1.52-1.6.9l-2.42-.98-1.9 3.32 2.02 1.6a8.2 8.2 0 000 1.86l-2.03 1.58 1.92 3.34 2.41-.98c.5.38 1.03.7 1.6.92L11.1 21h3.8l.34-2.2c.58-.22 1.12-.52 1.62-.9l2.4.96 1.9-3.32-2.02-1.6zM13 15a3 3 0 110-6 3 3 0 010 6z"></path></svg>
        </span>
        <span>Settings</span>
      </button>
      <button class={activeMenu === 'about' ? 'is-active' : ''} onclick={() => switchMenu('about')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M12 2a10 10 0 100 20 10 10 0 000-20zm0 4a1.4 1.4 0 110 2.8A1.4 1.4 0 0112 6zm-1.5 5h3v7h-3v-7z"></path></svg>
        </span>
        <span>About</span>
      </button>
    </nav>
    <div class="sidebar-footer">
      <p class="sidebar-version">Version: v{appVersion || 'dev'}</p>
      <form method="post" action="/auth/logout">
        <button class="btn btn-secondary sidebar-logout" type="submit">Logout</button>
      </form>
    </div>
  </aside>
  {/if}

  <main class="content {activeMenu === 'logs' ? 'content-logs' : ''}">
    {#if !isChatRoute}
      <div class="mobile-topbar">
        <button class="mobile-burger" type="button" aria-label="Toggle menu" onclick={toggleMobileSidebar}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 6h16v2H4V6zm0 5h16v2H4v-2zm0 5h16v2H4v-2z"></path>
          </svg>
        </button>
        <strong>{documentTitleByMenu(activeMenu)}</strong>
      </div>
      <section class="status-banner status-{statusClass(status.kind)}" aria-live="polite">
        <span class="status-icon">{statusIcon(status.kind)}</span>
        <p>{status.text}</p>
      </section>
      <section class="active-summary-strip" aria-label="Active account summary">
        <div class="active-summary-item">
          <span class="active-summary-label">API</span>
          <strong>{accountDisplayLabel(activeAPIAccount())}</strong>
        </div>
        <div class="active-summary-item">
          <span class="active-summary-label">CLI</span>
          <strong>{accountDisplayLabel(activeCLIAccount())}</strong>
        </div>
      </section>
    {/if}

    {#if activeMenu === 'coding'}
      <CodingView />
    {/if}

    {#if !isChatRoute && activeMenu === 'dashboard'}
      <DashboardView
        accounts={filteredAccounts()}
        totalAccounts={accounts.length}
        {showAccountEmail}
        {busy}
        {accountSearchQuery}
        {accountTypeFilter}
        accountTypeOptions={accountTypeOptions()}
        onSetAccountSearchQuery={setAccountSearchQuery}
        onSetAccountTypeFilter={setAccountTypeFilter}
        onOpenAddAccountModal={openAddAccountModal}
        onRefreshAllUsage={refreshAllUsage}
        onUseApiAccount={useAccount}
        onUseCliAccount={useCLIAccount}
        onRefreshUsage={refreshUsage}
        onOpenRemoveModal={openRemoveModal}
        {usageLabel}
        {clampPercent}
        {parseUsageWindows}
        {formatResetLabel}
        {activeUsageAlert}
      />
    {/if}

    {#if !isChatRoute && activeMenu === 'settings'}
      <SettingsView
        busy={settingsBusy}
        {apiMode}
        onSetAPIMode={setAPIMode}
        {showAccountEmail}
        onToggleShowAccountEmail={toggleShowAccountEmail}
        {autoRefreshEnabled}
        {autoRefreshMinutes}
        {autoRefreshMinutesInput}
        {usageAlertThreshold}
        {usageAlertThresholdInput}
        {usageAutoSwitchThreshold}
        {usageAutoSwitchThresholdInput}
        {usageSoundEnabled}
        onToggleAutoRefreshEnabled={toggleAutoRefreshEnabled}
        onSetAutoRefreshMinutesInput={setAutoRefreshMinutesInput}
        onCommitAutoRefreshMinutesInput={commitAutoRefreshMinutesInput}
        onSetUsageAlertThresholdInput={setUsageAlertThresholdInput}
        onCommitUsageAlertThresholdInput={commitUsageAlertThresholdInput}
        onSetUsageAutoSwitchThresholdInput={setUsageAutoSwitchThresholdInput}
        onCommitUsageAutoSwitchThresholdInput={commitUsageAutoSwitchThresholdInput}
        onNudgeUsageAlertThreshold={nudgeUsageAlertThreshold}
        onNudgeUsageAutoSwitchThreshold={nudgeUsageAutoSwitchThreshold}
        onToggleUsageSoundEnabled={toggleUsageSoundEnabled}
        {autoRefreshBusy}
        {backgroundRefreshError}
        {backgroundRefreshLastAt}
      />
    {/if}

    {#if !isChatRoute && activeMenu === 'api-endpoints'}
      <ApiEndpointView
        busy={settingsBusy}
        {apiKey}
        {openAIEndpoint}
        {claudeEndpoint}
        authJSONEndpoint={authJSONEndpoint}
        {availableModels}
        {modelMappings}
        {mappingAlias}
        {mappingTargetModel}
        {editingMappingAlias}
        onSetMappingAlias={setMappingAlias}
        onSetMappingTargetModel={setMappingTargetModel}
        onSaveModelMapping={saveModelMapping}
        onCancelEditMapping={cancelEditMapping}
        onStartEditMapping={startEditMapping}
        onDeleteModelMapping={deleteModelMapping}
        onRegenerateAPIKey={regenerateAPIKey}
        onCopyText={copyText}
        {isCopied}
        {openAIExample}
        {claudeExample}
        {authJSONExample}
      />
    {/if}

    {#if !isChatRoute && activeMenu === 'logs'}
      <ApiLogsView
        {busy}
        {apiLogs}
        onLoadAPILogs={loadAPILogs}
        onOpenLogDetail={openLogDetail}
        {formatLogTimestamp}
        {logStatusClass}
      />
    {/if}

    {#if !isChatRoute && activeMenu === 'about'}
      <AboutView
        {busy}
        {appVersion}
        {latestVersion}
        {updateAvailable}
        {updateCheckedAt}
        {updateCheckError}
        {updateCheckBusy}
        {latestChangelog}
        {releaseURL}
        onCheckForUpdates={checkForUpdates}
      />
    {/if}
  </main>

  {#if !isChatRoute && mobileSidebarOpen}
    <button class="sidebar-overlay" type="button" aria-label="Close menu" onclick={closeMobileSidebar}></button>
  {/if}

  {#if showAddAccountModal}
    <div
      class="modal-backdrop"
      role="presentation"
    >
      <div
        class="modal-card"
        role="dialog"
        aria-modal="true"
        tabindex="0"
        onkeydown={(event) => event.key === 'Escape' && closeAddAccountModal()}
      >
        <div class="modal-head">
          <div>
            <h3>Add Account</h3>
            <p class="modal-subtitle">Connect ChatGPT account with browser callback or device login.</p>
          </div>
          <button class="btn btn-secondary btn-small" onclick={closeAddAccountModal} disabled={busy}>Close</button>
        </div>

        {#if addAccountMode === 'menu'}
          <p class="modal-helper">Choose preferred login flow.</p>
          <div class="method-grid">
            <button class="method-card" onclick={startBrowserLogin} disabled={busy}>
              <span class="method-title">Browser Callback</span>
              <span class="method-desc">Open ChatGPT login page in a new tab and wait for automatic callback.</span>
            </button>
            <button class="method-card" onclick={startDeviceLogin} disabled={busy}>
              <span class="method-title">Device Code</span>
              <span class="method-desc">Copy one-time code, sign in on OpenAI device page, then verify.</span>
            </button>
          </div>
        {/if}

        {#if addAccountMode === 'browser'}
          <div class="modal-body">
            <label for="browserLoginUrl">Browser Login URL</label>
            <div class="device-code-row">
              <input id="browserLoginUrl" value={browserLoginURL} readonly disabled />
              <button
                class="btn btn-secondary"
                onclick={() => copyText(browserLoginURL, 'Browser login URL', 'browser_login_url')}
                disabled={busy || !browserLoginURL}
              >
                {#if isCopied('browser_login_url')}Copied{:else}Copy{/if}
              </button>
              <button class="btn btn-primary" onclick={openBrowserLoginTab} disabled={busy || !browserLoginURL}>
                Open New Tab
              </button>
            </div>
            <label for="browserCallbackUrl">Callback URL (Manual Paste)</label>
            <div class="device-code-row">
              <input
                id="browserCallbackUrl"
                value={browserCallbackURL}
                placeholder="http://127.0.0.1:3061/auth/callback?code=...&state=..."
                oninput={(event) => (browserCallbackURL = event.currentTarget.value)}
                disabled={busy}
              />
              <button
                class="btn btn-secondary"
                onclick={submitManualBrowserCallback}
                disabled={busy || !browserCallbackURL.trim()}
              >
                Submit
              </button>
            </div>
            <div class="panel-actions">
              <button class="btn btn-secondary" onclick={() => (addAccountMode = 'menu')} disabled={busy}>Back</button>
            </div>
            {#if browserWaiting}
              <p class="modal-helper">Waiting callback from browser login...</p>
            {/if}
          </div>
        {/if}

        {#if addAccountMode === 'device'}
          <div class="modal-body">
            <p class="modal-helper">Open device login URL and enter the code below.</p>
            <label for="deviceVerifyUrl">Device Login URL</label>
            <div class="device-code-row">
              <input
                id="deviceVerifyUrl"
                value={deviceLogin?.verification_uri_complete || deviceLogin?.verification_uri || ''}
                readonly
                disabled
              />
              <button
                class="btn btn-secondary"
                onclick={() => copyText(deviceLogin?.verification_uri_complete || deviceLogin?.verification_uri || '', 'Device login URL', 'device_login_url')}
                disabled={busy || !(deviceLogin?.verification_uri_complete || deviceLogin?.verification_uri)}
              >
                {#if isCopied('device_login_url')}Copied{:else}Copy{/if}
              </button>
            </div>
            <label for="deviceUserCode">Device Code (One-Time)</label>
            <div class="device-code-row">
              <input id="deviceUserCode" value={deviceLogin?.user_code || ''} readonly disabled class="mono" />
              <button class="btn btn-secondary" onclick={copyDeviceCode} disabled={busy}>
                {#if isCopied('device_code') || deviceCodeCopied}Copied{:else}Copy{/if}
              </button>
            </div>
            <div class="panel-actions">
              <button class="btn btn-secondary" onclick={() => (addAccountMode = 'menu')} disabled={busy}>Back</button>
            </div>
          </div>
        {/if}
      </div>
    </div>
  {/if}

  {#if showRemoveModal && removeCandidate}
    <div
      class="modal-backdrop"
      role="button"
      tabindex="0"
      onclick={(event) => event.target === event.currentTarget && closeRemoveModal()}
      onkeydown={(event) => (event.key === 'Escape' || event.key === 'Enter') && closeRemoveModal()}
    >
      <div class="modal-card" role="dialog" aria-modal="true" tabindex="0">
        <div class="modal-head">
          <div>
            <h3>Confirm Remove Account</h3>
            <p class="modal-subtitle">This removes stored tokens and account record from codexsess.</p>
          </div>
        </div>
        <div class="modal-body">
          <p class="modal-helper">Remove account <span class="mono">{removeCandidate.email || '-'}</span>?</p>
          <p class="modal-helper">ID: <span class="mono">{removeCandidate.id}</span></p>
          <div class="panel-actions">
            <button class="btn btn-secondary" onclick={closeRemoveModal} disabled={busy}>Cancel</button>
            <button class="btn btn-danger" onclick={removeAccount} disabled={busy}>Remove</button>
          </div>
        </div>
      </div>
    </div>
  {/if}

  {#if showLogDetailModal && logDetailEntry}
    <div
      class="modal-backdrop"
      role="button"
      tabindex="0"
      onclick={(event) => event.target === event.currentTarget && closeLogDetailModal()}
      onkeydown={(event) => (event.key === 'Escape' || event.key === 'Enter') && closeLogDetailModal()}
    >
      <div class="modal-card log-detail-card" role="dialog" aria-modal="true" tabindex="0">
        <div class="modal-head">
          <div>
            <h3>API Log Detail</h3>
            <p class="modal-subtitle">
              <span class="mono">{logDetailEntry.method}</span>
              <span> </span>
              <span class="mono">{logDetailEntry.path}</span>
              <span> · </span>
              <span class="mono">{logDetailEntry.status || '-'}</span>
              {#if logDetailEntry.accountEmail || logDetailEntry.accountID || logDetailEntry.accountHint}
                <span> · </span>
                <span class="mono">{logDetailEntry.accountEmail || logDetailEntry.accountID || logDetailEntry.accountHint}</span>
              {/if}
            </p>
          </div>
          <button class="btn btn-secondary btn-small" onclick={closeLogDetailModal}>Close</button>
        </div>
        <div class="log-detail-grid">
          <div class="log-payload">
            <p>Request Body</p>
            <pre>{logDetailEntry.requestBody || '-'}</pre>
          </div>
          <div class="log-payload">
            <p>Response Body</p>
            <pre>{logDetailEntry.responseBody || '-'}</pre>
          </div>
        </div>
      </div>
    </div>
  {/if}
</div>
