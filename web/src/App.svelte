<script>
  let accounts = $state([]);
  let busy = $state(false);
  let status = $state({ text: 'Initializing...', kind: 'info' });

  let apiKey = $state('');
  let openAIEndpoint = $state('');
  let claudeEndpoint = $state('');
  let activeMenu = $state('dashboard');
  let apiLogs = $state([]);
  let showLogDetailModal = $state(false);
  let logDetailEntry = $state(null);
  let availableModels = $state([]);
  let modelMappings = $state({});
  let mappingAlias = $state('');
  let mappingTargetModel = $state('gpt-5.2-codex');
  let editingMappingAlias = $state('');
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

  let deviceLogin = $state(null);
  let deviceCodeCopied = $state(false);
  let nowTick = $state(Date.now());

  let showRemoveModal = $state(false);
  let removeCandidate = $state(null);

  let pollTimer = null;
  let browserWaitTimer = null;

  const jsonHeaders = { 'Content-Type': 'application/json' };

  function setStatus(text, kind = 'info') {
    status = { text, kind };
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

  function titleCase(input) {
    return String(input || '')
      .replace(/_/g, ' ')
      .replace(/\s+/g, ' ')
      .trim()
      .replace(/\b\w/g, (c) => c.toUpperCase());
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
    return `curl ${openAIEndpoint || 'http://127.0.0.1:3061/v1/chat/completions'} \\
  -H "Authorization: Bearer ${apiKey || 'sk-codexsess-...'}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.2-codex",
    "messages": [{"role":"user","content":"Reply exactly with: OK"}],
    "stream": false
  }'`;
  }

  function claudeExample() {
    return `curl ${claudeEndpoint || 'http://127.0.0.1:3061/v1/messages'} \\
  -H "x-api-key: ${apiKey || 'sk-codexsess-...'}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-5.2-codex",
    "max_tokens": 512,
    "messages": [{"role":"user","content":"Reply exactly with: OK"}],
    "stream": false
  }'`;
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

  function logStatusClass(status) {
    const n = Number(status) || 0;
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
    const response = await fetch(url, {
      headers: jsonHeaders,
      ...options
    });
    const bodyText = await response.text();
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
    openAIEndpoint = data.openai_endpoint || '';
    claudeEndpoint = data.claude_endpoint || '';
    const fromAPI = Array.isArray(data.available_models) ? data.available_models : [];
    availableModels = fromAPI.length > 0 ? fromAPI : defaultCodexModels;
    modelMappings = (data.model_mappings && typeof data.model_mappings === 'object') ? data.model_mappings : {};
    if (!availableModels.includes(mappingTargetModel) && availableModels.length > 0) {
      mappingTargetModel = availableModels[0];
    }
  }

  async function loadAPILogs() {
    const data = await req('/api/logs?limit=400');
    const lines = Array.isArray(data.lines) ? data.lines : [];
    apiLogs = [...lines].reverse().map((line, idx) => parseAPILogLine(line, idx));
  }

  async function refreshAllData() {
    await Promise.all([loadAccounts(), loadSettings()]);
    setStatus(`Loaded ${accounts.length} account(s).`, 'success');
  }

  async function useAccount(selector) {
    busy = true;
    try {
      await req('/api/account/use', {
        method: 'POST',
        body: JSON.stringify({ selector })
      });
      await refreshAllData();
      setStatus(`Active account switched to ${selector}.`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function refreshUsage(selector) {
    busy = true;
    try {
      await req('/api/usage/refresh', {
        method: 'POST',
        body: JSON.stringify({ selector })
      });
      await refreshAllData();
      setStatus(`Usage refreshed for ${selector}.`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function refreshAllUsage() {
    busy = true;
    try {
      await req('/api/usage/refresh', {
        method: 'POST',
        body: JSON.stringify({ all: true })
      });
      await refreshAllData();
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
    busy = true;
    try {
      await req('/api/account/remove', {
        method: 'POST',
        body: JSON.stringify({ selector: removeCandidate.id })
      });
      await refreshAllData();
      setStatus(`Removed account ${removeCandidate.email || removeCandidate.id}.`, 'success');
      closeRemoveModal();
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function regenerateAPIKey() {
    busy = true;
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
      busy = false;
    }
  }

  async function copyText(value, label) {
    const text = String(value || '').trim();
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setStatus(`${label} copied.`, 'success');
    } catch {
      setStatus(`Failed to copy ${label}.`, 'error');
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
    busy = true;
    try {
      // Save target first to avoid losing existing mapping when rename fails midway.
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
      busy = false;
    }
  }

  async function deleteModelMapping(alias) {
    const key = String(alias || '').trim();
    if (!key) return;
    busy = true;
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
      busy = false;
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
    deviceLogin = null;
    deviceCodeCopied = false;
    clearPollTimer();
    clearBrowserWaitTimer();
  }

  function onModalBackdropClick(event) {
    if (event.target === event.currentTarget) {
      closeAddAccountModal();
    }
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
      const hasNew = nowIDs.some((id) => !browserKnownIDs.includes(id));
      if (hasNew) {
        browserWaiting = false;
        browserLoginURL = '';
        browserLoginID = '';
        clearBrowserWaitTimer();
        await refreshAllData();
        setStatus('Browser callback login success. Account added.', 'success');
        closeAddAccountModal();
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
        deviceLogin = null;
        await refreshAllData();
        setStatus('Device login success. Account added and active.', 'success');
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
    try {
      await navigator.clipboard.writeText(code);
      deviceCodeCopied = true;
      setTimeout(() => {
        deviceCodeCopied = false;
      }, 1300);
    } catch {
      setStatus('Failed to copy device code.', 'error');
    }
  }

  $effect(() => {
    const onBeforeUnload = () => {
      cancelBrowserLoginSession();
    };
    window.addEventListener('beforeunload', onBeforeUnload);

    refreshAllData().catch((error) => {
      setStatus(error.message, 'error');
    });

    return () => {
      window.removeEventListener('beforeunload', onBeforeUnload);
      cancelBrowserLoginSession();
      clearPollTimer();
      clearBrowserWaitTimer();
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
</script>

<svelte:head>
  <title>codexsess web console</title>
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />
  <link
    href="https://fonts.googleapis.com/css2?family=Fira+Code:wght@400;500;600;700&family=Fira+Sans:wght@400;500;600;700&display=swap"
    rel="stylesheet"
  />
</svelte:head>

<main class="app-shell">
  <div class="backdrop"></div>

  <section class="hero card">
    <div class="hero-head">
      <div>
        <p class="kicker">codexSess</p>
        <h1>Account Control Plane</h1>
        <p>
          All account and proxy settings are managed from web UI.
        </p>
      </div>
      <nav class="menu-nav" aria-label="Main Menu">
        <button class="menu-btn {activeMenu === 'dashboard' ? 'is-active' : ''}" onclick={() => (activeMenu = 'dashboard')}>
          Dashboard
        </button>
        <button class="menu-btn {activeMenu === 'settings' ? 'is-active' : ''}" onclick={() => (activeMenu = 'settings')}>
          Settings
        </button>
        <button class="menu-btn {activeMenu === 'logs' ? 'is-active' : ''}" onclick={() => (activeMenu = 'logs')}>
          API Logs
        </button>
      </nav>
    </div>
  </section>

  <section class="status-banner status-{statusClass(status.kind)}" aria-live="polite">
    <span class="status-badge">{statusIcon(status.kind)}</span>
    <p>{status.text}</p>
  </section>

  {#if activeMenu === 'dashboard'}
    <section class="card">
      <header class="section-header">
        <h2>Add Account</h2>
      </header>
      <div class="actions">
        <button class="btn btn-primary" onclick={openAddAccountModal} disabled={busy}>Add Account</button>
        <button class="btn btn-secondary" onclick={refreshAllUsage} disabled={busy}>Refresh All Usage</button>
      </div>
    </section>
  {/if}

  {#if showAddAccountModal}
    <div
      class="modal-backdrop"
      role="button"
      tabindex="0"
      onclick={onModalBackdropClick}
      onkeydown={(e) => (e.key === 'Escape' || e.key === 'Enter') && closeAddAccountModal()}
    >
      <div class="modal-card" role="dialog" aria-modal="true" tabindex="0">
        <div class="modal-head">
          <div>
            <h3>Add Account</h3>
            <p class="modal-subtitle">Connect ChatGPT account with secure browser or device flow.</p>
          </div>
          <button class="btn btn-secondary btn-mini" onclick={closeAddAccountModal} disabled={busy}>Close</button>
        </div>

        {#if addAccountMode === 'menu'}
          <p class="muted modal-helper">Choose preferred login flow.</p>
          <div class="method-grid">
            <button class="method-card method-card-primary" onclick={startBrowserLogin} disabled={busy}>
              <span class="method-title">Browser Callback</span>
              <span class="method-desc">Open ChatGPT login page in new tab and wait callback automatically.</span>
            </button>
            <button class="method-card" onclick={startDeviceLogin} disabled={busy}>
              <span class="method-title">Device Code</span>
              <span class="method-desc">Copy one-time code, sign in on OpenAI device page, then confirm.</span>
            </button>
          </div>
        {/if}

        {#if addAccountMode === 'browser'}
          <div class="modal-body">
            <div class="mode-badge mode-browser">Browser Login</div>
            <label for="browserLoginUrl">Browser Login URL</label>
            <input id="browserLoginUrl" value={browserLoginURL} readonly disabled />
            <div class="actions modal-actions">
              <button class="btn btn-primary" onclick={openBrowserLoginTab} disabled={busy}>Open New Tab</button>
              <button class="btn btn-secondary" onclick={() => (addAccountMode = 'menu')} disabled={busy}>Back</button>
            </div>
            {#if browserWaiting}
              <p class="muted modal-helper">Waiting callback from browser login...</p>
            {/if}
          </div>
        {/if}

        {#if addAccountMode === 'device'}
          <div class="modal-body">
            <div class="mode-badge mode-device">Device Login</div>
            <p class="muted modal-helper">Open device login URL and enter the code below.</p>
            <label for="deviceVerifyUrl">Device Login URL</label>
            <input
              id="deviceVerifyUrl"
              value={deviceLogin?.verification_uri_complete || deviceLogin?.verification_uri || ''}
              readonly
              disabled
            />
            <label for="deviceUserCode">Device Code (One-Time)</label>
            <div class="device-code-row">
              <input id="deviceUserCode" value={deviceLogin?.user_code || ''} readonly disabled class="mono code-input" />
              <button class="btn btn-secondary device-copy-btn" onclick={copyDeviceCode} disabled={busy}>
                {#if deviceCodeCopied}Copied{:else}Copy{/if}
              </button>
            </div>
            <div class="actions modal-actions">
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
      onclick={(e) => e.target === e.currentTarget && closeRemoveModal()}
      onkeydown={(e) => (e.key === 'Escape' || e.key === 'Enter') && closeRemoveModal()}
    >
      <div class="modal-card" role="dialog" aria-modal="true" tabindex="0">
        <div class="modal-head">
          <div>
            <h3>Confirm Remove Account</h3>
            <p class="modal-subtitle">This removes stored tokens and account record from codexsess.</p>
          </div>
        </div>
        <div class="modal-body">
          <p class="muted modal-helper">
            Remove account <span class="mono">{removeCandidate.email || '-'}</span>?
          </p>
          <p class="muted modal-helper">ID: <span class="mono">{removeCandidate.id}</span></p>
          <div class="actions modal-actions">
            <button class="btn btn-secondary" onclick={closeRemoveModal} disabled={busy}>Cancel</button>
            <button class="btn btn-danger" onclick={removeAccount} disabled={busy}>Remove</button>
          </div>
        </div>
      </div>
    </div>
  {/if}

  {#if activeMenu === 'dashboard'}
    <section class="card accounts-wrap" aria-label="Managed accounts">
      <header>
        <h2>Managed Accounts</h2>
        <p>{accounts.length} account(s)</p>
      </header>

      <div class="accounts-grid">
        {#if accounts.length === 0}
          <div class="account-empty">No accounts yet. Add via Browser Callback or Device Login.</div>
        {:else}
          {#each accounts as account (account.id)}
            {@const usageWindows = parseUsageWindows(account.usage)}
            <article class="account-card {account.active ? 'is-active' : ''}">
              <div class="account-head">
                <div>
                  <p class="account-email">{account.email || '-'}</p>
                </div>
                {#if account.active}
                  <span class="badge">ACTIVE</span>
                {:else}
                  <span class="muted">idle</span>
                {/if}
              </div>

              <div class="usage-grid">
                {#if usageWindows.length === 0}
                  <div class="usage-empty">Usage unavailable. Click refresh.</div>
                {:else}
                  {#each usageWindows as window (window.key)}
                    <div class="usage-card">
                      <div class="usage-card-head">
                        <p class="usage-title">{window.name}</p>
                        <p class="usage-percent">{usageLabel(window.percent)}</p>
                      </div>
                      <div class="usage-progress" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={window.percent}>
                        <span style={`width: ${clampPercent(window.percent)}%`}></span>
                      </div>
                      <p class="usage-reset">{formatResetLabel(window.resetAt)}</p>
                    </div>
                  {/each}
                {/if}
              </div>

              <div class="account-foot">
                <span class="plan-badge">{(account.plan_type || 'unknown').toUpperCase()}</span>
                <div class="row-actions icon-actions">
                  <button
                    class="btn btn-mini btn-primary icon-btn"
                    onclick={() => useAccount(account.id)}
                    disabled={busy || account.active}
                    title="Use account"
                    aria-label="Use account"
                  >
                    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5v14l11-7z"></path></svg>
                  </button>
                  <button
                    class="btn btn-mini btn-secondary icon-btn"
                    onclick={() => refreshUsage(account.id)}
                    disabled={busy}
                    title="Refresh usage"
                    aria-label="Refresh usage"
                  >
                    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M17.65 6.35A7.95 7.95 0 0012 4V1L7 6l5 5V7a5 5 0 11-5 5H5a7 7 0 107.75-6.95c1.67.17 3.16.93 4.24 2.1l.66-.8z"></path></svg>
                  </button>
                  <button
                    class="btn btn-mini btn-danger icon-btn"
                    onclick={() => openRemoveModal(account)}
                    disabled={busy}
                    title="Remove account"
                    aria-label="Remove account"
                  >
                    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M9 3h6l1 2h4v2H4V5h4l1-2zm1 6h2v9h-2V9zm4 0h2v9h-2V9zM7 9h2v9H7V9z"></path></svg>
                  </button>
                </div>
              </div>
            </article>
          {/each}
        {/if}
      </div>
    </section>
  {/if}

  {#if activeMenu === 'settings'}
    <section class="card settings-wrap">
      <header class="section-header">
        <h2>Settings</h2>
      </header>
      <div class="settings-list">
        <div class="setting-row">
          <p class="field-label">API Key</p>
          <div class="setting-value-row">
            <input value={apiKey} readonly disabled />
            <button class="btn btn-secondary" onclick={() => copyText(apiKey, 'API key')} disabled={busy}>Copy</button>
            <button class="btn btn-primary" onclick={regenerateAPIKey} disabled={busy}>Regenerate</button>
          </div>
        </div>
        <div class="setting-row">
          <p class="field-label">OpenAI Compatible Endpoint</p>
          <div class="setting-value-row">
            <input value={openAIEndpoint} readonly disabled />
            <button class="btn btn-secondary" onclick={() => copyText(openAIEndpoint, 'OpenAI endpoint')} disabled={busy}>Copy</button>
          </div>
        </div>
        <div class="setting-row">
          <p class="field-label">Claude Endpoint</p>
          <div class="setting-value-row">
            <input value={claudeEndpoint} readonly disabled />
            <button class="btn btn-secondary" onclick={() => copyText(claudeEndpoint, 'Claude endpoint')} disabled={busy}>Copy</button>
          </div>
        </div>
        <div class="setting-row">
          <p class="field-label">Available Codex Models</p>
          <div class="models-box">
            {#if availableModels.length === 0}
              <p class="muted">No model list loaded.</p>
            {:else}
              {#each availableModels as model}
                <span class="model-chip">{model}</span>
              {/each}
            {/if}
          </div>
        </div>
        <div class="setting-row">
          <p class="field-label">Model Mapping</p>
          <div class="mapping-form">
            <input placeholder="Alias (e.g. default)" bind:value={mappingAlias} />
            <select bind:value={mappingTargetModel}>
              {#each availableModels as model}
                <option value={model}>{model}</option>
              {/each}
            </select>
            <button class="btn btn-primary" onclick={saveModelMapping} disabled={busy}>
              {editingMappingAlias ? 'Update Mapping' : 'Save Mapping'}
            </button>
            {#if editingMappingAlias}
              <button class="btn btn-secondary" onclick={cancelEditMapping} disabled={busy}>Cancel</button>
            {/if}
          </div>
          <div class="mapping-list">
            {#if Object.keys(modelMappings).length === 0}
              <p class="muted">No mappings yet.</p>
            {:else}
              {#each Object.entries(modelMappings) as [alias, model]}
                <div class="mapping-row">
                  <code>{alias}</code>
                  <span>→</span>
                  <code>{model}</code>
                  <button class="btn btn-secondary btn-mini" onclick={() => startEditMapping(alias)} disabled={busy}>Edit</button>
                  <button class="btn btn-danger btn-mini" onclick={() => deleteModelMapping(alias)} disabled={busy}>Delete</button>
                </div>
              {/each}
            {/if}
          </div>
        </div>
        <div class="setting-row">
          <p class="field-label">OpenAI Compatible Request Example</p>
          <div class="example-box">
            <pre>{openAIExample()}</pre>
            <button class="btn btn-secondary" onclick={() => copyText(openAIExample(), 'OpenAI example')}>Copy Example</button>
          </div>
        </div>
        <div class="setting-row">
          <p class="field-label">Claude Request Example</p>
          <div class="example-box">
            <pre>{claudeExample()}</pre>
            <button class="btn btn-secondary" onclick={() => copyText(claudeExample(), 'Claude example')}>Copy Example</button>
          </div>
        </div>
      </div>
    </section>
  {/if}

  {#if activeMenu === 'logs'}
    <section class="card logs-wrap">
      <header class="section-header">
        <h2>API Logs</h2>
        <button class="btn btn-secondary" onclick={loadAPILogs} disabled={busy}>Refresh</button>
      </header>
      <p class="muted logs-note">Only proxy API traffic is logged (OpenAI/Claude). Dashboard requests are excluded.</p>
      <div class="logs-box">
        {#if apiLogs.length === 0}
          <p class="muted">No traffic logs yet.</p>
        {:else}
          {#each apiLogs as entry (entry.id)}
            <article class="log-row">
              <div class="log-main">
                <div class="log-topline">
                  <code class="log-path">{entry.path}</code>
                  <span class="log-method">{entry.method}</span>
                  <span class="log-proto">{entry.protocol}</span>
                </div>
                <p class="log-subline">
                  <span>{formatLogTimestamp(entry.timestamp)}</span>
                  <span>{entry.latencyMS} ms</span>
                  {#if entry.model}
                    <span>{entry.model}</span>
                  {/if}
                </p>
              </div>
              <div class="log-actions">
                <span class="log-status {logStatusClass(entry.status)}">{entry.status || '-'}</span>
                <button class="btn btn-secondary btn-mini" onclick={() => openLogDetail(entry)}>Detail</button>
              </div>
            </article>
          {/each}
        {/if}
      </div>
    </section>
  {/if}

  {#if showLogDetailModal && logDetailEntry}
    <div
      class="modal-backdrop"
      role="button"
      tabindex="0"
      onclick={(e) => e.target === e.currentTarget && closeLogDetailModal()}
      onkeydown={(e) => (e.key === 'Escape' || e.key === 'Enter') && closeLogDetailModal()}
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
            </p>
          </div>
          <button class="btn btn-secondary btn-mini" onclick={closeLogDetailModal}>Close</button>
        </div>
        <div class="log-detail-grid">
          <div class="log-payload">
            <p class="field-label">Request Body</p>
            <pre>{logDetailEntry.requestBody || '-'}</pre>
          </div>
          <div class="log-payload">
            <p class="field-label">Response Body</p>
            <pre>{logDetailEntry.responseBody || '-'}</pre>
          </div>
        </div>
      </div>
    </div>
  {/if}

</main>

<style>
  :global(:root) {
    --bg: #0f172a;
    --bg-soft: #111f36;
    --card: rgba(15, 23, 42, 0.86);
    --border: rgba(148, 163, 184, 0.28);
    --text: #f8fafc;
    --muted: #94a3b8;
    --primary: #22c55e;
    --danger: #ef4444;
  }

  :global(*) { box-sizing: border-box; }

  :global(body) {
    margin: 0;
    min-height: 100vh;
    background:
      radial-gradient(circle at 10% 20%, rgba(34, 197, 94, 0.2), transparent 28%),
      radial-gradient(circle at 82% -5%, rgba(59, 130, 246, 0.22), transparent 35%),
      linear-gradient(160deg, var(--bg), #060b16 70%);
    color: var(--text);
    font-family: 'Fira Sans', 'Segoe UI', sans-serif;
  }

  .app-shell {
    width: min(1220px, calc(100% - 2rem));
    margin: 0 auto;
    padding: 2rem 0 2.5rem;
    display: grid;
    gap: 1rem;
    position: relative;
  }

  .backdrop {
    position: fixed;
    inset: 0;
    pointer-events: none;
    background-image:
      linear-gradient(rgba(148,163,184,.05) 1px, transparent 1px),
      linear-gradient(90deg, rgba(148,163,184,.05) 1px, transparent 1px);
    background-size: 24px 24px;
    mask-image: radial-gradient(circle at center, black 40%, transparent 90%);
    z-index: -1;
  }

  .card {
    border: 1px solid var(--border);
    background: var(--card);
    backdrop-filter: blur(10px);
    border-radius: 1rem;
    padding: 1rem;
    box-shadow: 0 20px 40px rgba(2,6,23,.34);
  }

  .hero h1 {
    font-family: 'Fira Code', monospace;
    letter-spacing: .02em;
    margin: .4rem 0;
    font-size: clamp(1.4rem, 3vw, 2.2rem);
  }

  .hero p { margin: 0; color: var(--muted); max-width: 72ch; }

  .hero-head {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    gap: 1rem;
    flex-wrap: wrap;
  }

  .menu-nav {
    display: flex;
    gap: .45rem;
    flex-wrap: wrap;
    justify-content: flex-end;
  }

  .menu-btn {
    border: 1px solid rgba(148,163,184,.35);
    background: rgba(15,23,42,.6);
    color: #e2e8f0;
    border-radius: .65rem;
    padding: .48rem .72rem;
    font-size: .82rem;
    font-weight: 700;
    cursor: pointer;
  }

  .menu-btn.is-active {
    border-color: rgba(34,197,94,.58);
    background: rgba(34,197,94,.18);
    color: #bbf7d0;
  }

  .kicker {
    margin: 0;
    text-transform: uppercase;
    letter-spacing: .11em;
    color: #86efac;
    font-size: .78rem;
    font-weight: 700;
  }

  .section-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: .8rem;
    margin-bottom: .9rem;
  }

  .section-header h2 { margin: 0; font-size: 1rem; font-family: 'Fira Code', monospace; }

  .row-grid { display: grid; grid-template-columns: 1fr auto; gap: .75rem; }
  .actions { display: flex; gap: .6rem; flex-wrap: wrap; }

  label { color: var(--muted); font-size: .84rem; }

  input {
    width: 100%;
    border-radius: .7rem;
    border: 1px solid rgba(148,163,184,.35);
    padding: .72rem .8rem;
    background: var(--bg-soft);
    color: var(--text);
    outline: none;
  }

  input:focus-visible {
    border-color: var(--primary);
    box-shadow: 0 0 0 3px rgba(34,197,94,.2);
  }

  .btn {
    border: 0;
    border-radius: .68rem;
    padding: .68rem .9rem;
    color: #03120a;
    font-weight: 700;
    transition: filter 200ms ease;
    cursor: pointer;
  }

  .btn:disabled { opacity: .45; cursor: not-allowed; }
  .btn:hover:not(:disabled) { filter: brightness(1.08); }

  .btn-primary { background: var(--primary); }
  .btn-secondary { background: #334155; color: #e2e8f0; }
  .btn-danger { background: var(--danger); color: #fff; }
  .btn-mini { padding: .42rem .58rem; font-size: .8rem; min-width: 66px; }

  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(2, 6, 23, 0.72);
    display: grid;
    place-items: center;
    z-index: 20;
    padding: 1rem;
  }

  .modal-card {
    width: min(620px, 100%);
    border: 1px solid rgba(148,163,184,.35);
    border-radius: 1rem;
    background:
      radial-gradient(circle at 100% -20%, rgba(34, 197, 94, 0.14), transparent 40%),
      #0f172a;
    padding: 1rem 1rem 1.15rem;
    box-shadow: 0 20px 50px rgba(0,0,0,.5);
    display: grid;
    gap: 1rem;
  }

  .modal-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: .8rem;
  }

  .modal-head h3 { margin: 0; font-family: 'Fira Code', monospace; }
  .modal-subtitle {
    margin: .3rem 0 0;
    color: var(--muted);
    font-size: .82rem;
  }

  .modal-body {
    display: grid;
    gap: .65rem;
    padding: .2rem .15rem 0;
  }

  .modal-actions { margin-top: .3rem; }
  .modal-helper { margin: 0; font-size: .85rem; }

  .method-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: .7rem;
  }

  .method-card {
    appearance: none;
    border: 1px solid rgba(148, 163, 184, 0.35);
    background: linear-gradient(180deg, rgba(30, 41, 59, 0.65), rgba(15, 23, 42, 0.9));
    color: var(--text);
    border-radius: .8rem;
    padding: .85rem;
    text-align: left;
    cursor: pointer;
    display: grid;
    gap: .4rem;
    transition: border-color .2s ease, transform .2s ease, background .2s ease;
  }

  .method-card:hover:not(:disabled) {
    border-color: rgba(148, 163, 184, 0.65);
    transform: translateY(-1px);
  }

  .method-card-primary {
    border-color: rgba(34, 197, 94, 0.5);
    background: linear-gradient(180deg, rgba(34, 197, 94, 0.23), rgba(15, 23, 42, 0.95));
  }

  .method-title {
    font-size: .9rem;
    font-weight: 700;
    letter-spacing: .01em;
  }

  .method-desc {
    font-size: .78rem;
    color: var(--muted);
    line-height: 1.45;
  }

  .mode-badge {
    justify-self: start;
    padding: .2rem .55rem;
    border-radius: 999px;
    font-size: .72rem;
    font-weight: 700;
    letter-spacing: .03em;
    border: 1px solid transparent;
  }

  .mode-browser {
    background: rgba(59, 130, 246, 0.16);
    border-color: rgba(59, 130, 246, 0.45);
    color: #bfdbfe;
  }

  .mode-device {
    background: rgba(16, 185, 129, 0.16);
    border-color: rgba(16, 185, 129, 0.45);
    color: #bbf7d0;
  }

  .code-input {
    font-size: 1rem;
    letter-spacing: .09em;
    font-weight: 700;
    color: #bbf7d0;
  }

  .device-code-row {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: .55rem;
    align-items: center;
  }

  .device-copy-btn {
    min-width: 84px;
  }

  .accounts-wrap header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    margin-bottom: .7rem;
  }

  .accounts-wrap header h2 { margin: 0; font-size: 1rem; font-family: 'Fira Code', monospace; }
  .accounts-wrap header p { margin: 0; color: var(--muted); font-size: .84rem; }

  .settings-list {
    display: grid;
    gap: .8rem;
  }

  .setting-row {
    display: grid;
    gap: .4rem;
  }

  .field-label {
    margin: 0;
    color: var(--muted);
    font-size: .84rem;
  }

  .setting-value-row {
    display: grid;
    grid-template-columns: 1fr auto auto;
    gap: .55rem;
    align-items: center;
  }

  .mapping-form {
    display: grid;
    grid-template-columns: 1fr 1fr auto auto;
    gap: .55rem;
    align-items: center;
  }

  .mapping-form select {
    width: 100%;
    border-radius: .7rem;
    border: 1px solid rgba(148,163,184,.35);
    padding: .72rem .8rem;
    background: var(--bg-soft);
    color: var(--text);
    outline: none;
  }

  .mapping-list {
    border: 1px solid rgba(148,163,184,.22);
    border-radius: .75rem;
    background: rgba(2,6,23,.45);
    padding: .6rem;
    display: grid;
    gap: .45rem;
  }

  .mapping-row {
    display: grid;
    grid-template-columns: 1fr auto 2fr auto auto;
    align-items: center;
    gap: .5rem;
    font-size: .82rem;
  }

  .logs-note {
    margin: 0 0 .65rem;
    font-size: .82rem;
  }

  .example-box {
    border: 1px solid rgba(148,163,184,.22);
    border-radius: .75rem;
    background: rgba(2,6,23,.45);
    padding: .7rem;
    display: grid;
    gap: .65rem;
  }

  .models-box {
    border: 1px solid rgba(148,163,184,.22);
    border-radius: .75rem;
    background: rgba(2,6,23,.45);
    padding: .65rem;
    display: flex;
    flex-wrap: wrap;
    gap: .45rem;
  }

  .model-chip {
    display: inline-flex;
    align-items: center;
    border-radius: 999px;
    border: 1px solid rgba(59,130,246,.45);
    background: rgba(30,58,138,.24);
    color: #dbeafe;
    font-size: .74rem;
    padding: .22rem .55rem;
    font-family: 'Fira Code', monospace;
  }

  .example-box pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: .73rem;
    line-height: 1.45;
    color: #dbeafe;
    font-family: 'Fira Code', monospace;
  }

  .logs-box {
    border: 1px solid rgba(148,163,184,.22);
    border-radius: .8rem;
    background: rgba(2,6,23,.45);
    padding: .7rem;
    max-height: 58vh;
    overflow: auto;
    display: grid;
    gap: .55rem;
  }

  .log-row {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: .75rem;
    align-items: center;
    border: 1px solid rgba(148,163,184,.24);
    border-radius: .72rem;
    background: rgba(15,23,42,.62);
    padding: .62rem .68rem;
  }

  .log-main {
    min-width: 0;
    display: grid;
    gap: .32rem;
  }

  .log-topline {
    display: flex;
    gap: .45rem;
    align-items: center;
    flex-wrap: wrap;
  }

  .log-path {
    font-size: .78rem;
    color: #dbeafe;
    background: rgba(30,58,138,.2);
    border: 1px solid rgba(59,130,246,.35);
    border-radius: .45rem;
    padding: .14rem .4rem;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .log-method,
  .log-proto {
    font-size: .68rem;
    font-weight: 700;
    letter-spacing: .05em;
    text-transform: uppercase;
    color: #cbd5e1;
    border: 1px solid rgba(148,163,184,.35);
    border-radius: 999px;
    padding: .14rem .45rem;
  }

  .log-subline {
    margin: 0;
    color: var(--muted);
    font-size: .72rem;
    display: flex;
    gap: .58rem;
    flex-wrap: wrap;
  }

  .log-actions {
    display: inline-flex;
    align-items: center;
    gap: .45rem;
  }

  .log-status {
    min-width: 56px;
    text-align: center;
    border-radius: 999px;
    font-size: .72rem;
    font-weight: 700;
    letter-spacing: .03em;
    padding: .2rem .48rem;
    border: 1px solid rgba(148,163,184,.35);
    color: #e2e8f0;
    background: rgba(30,41,59,.65);
  }

  .log-status.status-2xx {
    border-color: rgba(34,197,94,.45);
    color: #bbf7d0;
    background: rgba(22,101,52,.24);
  }

  .log-status.status-3xx {
    border-color: rgba(56,189,248,.45);
    color: #bae6fd;
    background: rgba(12,74,110,.24);
  }

  .log-status.status-4xx {
    border-color: rgba(251,191,36,.5);
    color: #fde68a;
    background: rgba(120,53,15,.26);
  }

  .log-status.status-5xx {
    border-color: rgba(248,113,113,.52);
    color: #fecaca;
    background: rgba(127,29,29,.28);
  }

  .log-detail-card {
    width: min(980px, 100%);
  }

  .log-detail-grid {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: .7rem;
  }

  .log-payload {
    border: 1px solid rgba(148,163,184,.22);
    border-radius: .72rem;
    background: rgba(2,6,23,.5);
    padding: .62rem;
    display: grid;
    gap: .42rem;
  }

  .log-payload pre {
    margin: 0;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: .73rem;
    line-height: 1.45;
    color: #dbeafe;
    font-family: 'Fira Code', monospace;
    max-height: 44vh;
    overflow: auto;
  }

  .status-banner {
    display: flex;
    align-items: center;
    gap: .55rem;
    border: 1px solid rgba(148,163,184,.28);
    border-radius: .82rem;
    padding: .62rem .74rem;
    background: rgba(15,23,42,.72);
  }

  .status-banner p {
    margin: 0;
    font-size: .86rem;
    line-height: 1.35;
  }

  .status-badge {
    min-width: 26px;
    height: 26px;
    border-radius: 999px;
    display: grid;
    place-items: center;
    font-size: .68rem;
    font-weight: 700;
    letter-spacing: .02em;
    border: 1px solid rgba(148,163,184,.4);
    background: rgba(30,41,59,.85);
  }

  .mono { font-family: 'Fira Code', monospace; }

  .accounts-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(320px, 380px));
    justify-content: start;
    gap: .75rem;
  }

  .account-card {
    border: 1px solid rgba(148,163,184,.22);
    border-radius: .9rem;
    background: rgba(15,23,42,.55);
    padding: .85rem;
    display: grid;
    gap: .75rem;
  }

  .account-card.is-active {
    border-color: rgba(34,197,94,.5);
    box-shadow: inset 0 0 0 1px rgba(34,197,94,.22);
  }

  .account-head {
    display: flex;
    justify-content: space-between;
    gap: .6rem;
    align-items: flex-start;
  }

  .account-email {
    margin: 0;
    font-weight: 700;
    font-size: .95rem;
    word-break: break-word;
  }

  .usage-grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: .55rem;
  }

  .usage-card {
    border: 1px solid rgba(148,163,184,.2);
    border-radius: .72rem;
    padding: .52rem .6rem;
    background: rgba(2,6,23,.4);
    display: grid;
    gap: .34rem;
  }

  .usage-card-head {
    display: flex;
    justify-content: space-between;
    gap: .5rem;
    align-items: baseline;
  }

  .usage-title {
    margin: 0;
    font-size: .76rem;
    color: #cbd5e1;
    font-weight: 700;
  }

  .usage-percent {
    margin: 0;
    font-size: .95rem;
    font-weight: 700;
  }

  .usage-progress {
    width: 100%;
    height: .48rem;
    border-radius: 999px;
    overflow: hidden;
    background: rgba(148,163,184,.2);
  }

  .usage-progress > span {
    display: block;
    height: 100%;
    border-radius: inherit;
    background: linear-gradient(90deg, #22c55e, #14b8a6);
  }

  .usage-reset {
    margin: 0;
    font-size: .74rem;
    color: var(--muted);
  }

  .usage-empty,
  .account-empty {
    text-align: center;
    color: var(--muted);
    border: 1px dashed rgba(148,163,184,.35);
    border-radius: .8rem;
    padding: .9rem;
  }

  .badge {
    display: inline-block;
    border-radius: 999px;
    background: rgba(34,197,94,.22);
    border: 1px solid rgba(34,197,94,.5);
    color: #bbf7d0;
    padding: .16rem .5rem;
    font-size: .72rem;
    font-weight: 700;
  }

  .muted { color: var(--muted); }
  .row-actions { display: flex; gap: .42rem; }

  .account-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: .6rem;
  }

  .plan-badge {
    display: inline-flex;
    align-items: center;
    border-radius: 999px;
    border: 1px solid rgba(148,163,184,.45);
    color: #dbeafe;
    background: rgba(30,41,59,.7);
    font-size: .7rem;
    font-weight: 700;
    letter-spacing: .06em;
    padding: .2rem .55rem;
    text-transform: uppercase;
  }

  .icon-actions {
    justify-content: flex-end;
    gap: .35rem;
  }

  .icon-btn {
    min-width: 36px;
    width: 36px;
    height: 32px;
    display: grid;
    place-items: center;
    padding: 0;
  }

  .icon-btn svg {
    width: 16px;
    height: 16px;
    fill: currentColor;
  }

  .status-success {
    border-color: rgba(34,197,94,.45);
    background: rgba(6, 78, 59, .25);
    color: #bbf7d0;
  }

  .status-success .status-badge {
    border-color: rgba(34,197,94,.55);
    background: rgba(34,197,94,.18);
    color: #86efac;
  }

  .status-error {
    border-color: rgba(244,63,94,.5);
    background: rgba(127, 29, 29, .24);
    color: #fecdd3;
  }

  .status-error .status-badge {
    border-color: rgba(244,63,94,.65);
    background: rgba(244,63,94,.18);
    color: #fda4af;
  }

  .status-info {
    border-color: rgba(59,130,246,.45);
    background: rgba(30,58,138,.22);
    color: #dbeafe;
  }

  .status-info .status-badge {
    border-color: rgba(59,130,246,.6);
    background: rgba(59,130,246,.2);
    color: #bfdbfe;
  }

  @media (max-width: 980px) {
    .row-grid { grid-template-columns: 1fr; }
    .accounts-grid { grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); }
    .setting-value-row { grid-template-columns: 1fr auto; }
    .setting-value-row .btn-primary { grid-column: 1 / -1; }
    .mapping-form { grid-template-columns: 1fr; }
    .mapping-row { grid-template-columns: 1fr auto 1fr; }
    .mapping-row .btn { width: 100%; }
  }

  @media (max-width: 640px) {
    .app-shell { width: calc(100% - 1rem); padding: .8rem 0 1.2rem; }
    .card { padding: .8rem; border-radius: .8rem; }
    .actions, .row-actions { width: 100%; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: .5rem; }
    .btn { width: 100%; }
    .modal-card { padding: .85rem; }
    .method-grid { grid-template-columns: 1fr; }
    .accounts-grid { grid-template-columns: 1fr; }
    .usage-grid { grid-template-columns: 1fr; }
    .account-foot { align-items: stretch; flex-direction: column; }
    .icon-actions { grid-template-columns: repeat(3, minmax(0, 48px)); justify-content: start; }
    .icon-btn { width: 100%; min-width: 0; }
    .menu-nav { width: 100%; justify-content: flex-start; }
    .setting-value-row { grid-template-columns: 1fr; }
    .log-row { grid-template-columns: 1fr; }
    .log-actions { justify-content: space-between; }
    .log-detail-grid { grid-template-columns: 1fr; }
  }

  @media (prefers-reduced-motion: reduce) {
    .btn { transition: none; }
  }
</style>
