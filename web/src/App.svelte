<script>
  import { onMount } from 'svelte';
  import DashboardView from './views/DashboardView.svelte';
  import CodingView from './views/CodingView.svelte';
  import SettingsView from './views/SettingsView.svelte';
  import ApiEndpointView from './views/ApiEndpointView.svelte';
  import ApiLogsView from './views/ApiLogsView.svelte';
  import SystemLogsView from './views/SystemLogsView.svelte';
  import AboutView from './views/AboutView.svelte';
  import {
    formatLogTimestamp,
    logStatusClass,
    parseAPILogLine
  } from './app/apiLogHelpers.js';
  import {
    detectChatRoute,
    documentTitleByMenu,
    menuFromPath,
    pathForMenu
  } from './app/routeHelpers.js';
  import {
    buildAuthJSONExample,
    buildClaudeExample,
    buildOpenAIExample,
    buildUsageStatusExample
  } from './app/endpointExamples.js';
  import {
    accountStatusOptions,
    formatResetLabel,
    pageSizeOptions,
    usageAvailabilityOptions
  } from './app/dashboardHelpers.js';
  import {
    accountDisplayLabel as accountDisplayLabelForView,
    accountTypeOptions as buildAccountTypeOptions,
    accountUsageSortScore as accountUsageSortScoreForView,
    activeAPIAccount as selectActiveAPIAccount,
    activeCLIAccount as selectActiveCLIAccount,
    paginatedAccounts as buildPaginatedAccounts
  } from './app/accountViewHelpers.js';
  import {
    nextDashboardPage,
    nextDashboardPageSize,
    nextFilterValue
  } from './app/dashboardFilterHelpers.js';
  import {
    cancelBrowserLoginSessionRequest,
    clearIntervalTimer
  } from './app/browserLoginHelpers.js';
  import {
    loadUIPreferences as readUIPreferences,
    saveUIPreferences as persistUIPreferences,
    writeClipboardText
  } from './app/browserStateHelpers.js';
  import {
    copyTextWithFeedback,
    isCopiedState,
    markCopiedState
  } from './app/copyHelpers.js';
  import {
    closeLogDetailState,
    closeSystemLogDetailState,
    openLogDetailState,
    openSystemLogDetailState
  } from './app/logDetailHelpers.js';
  import {
    authJSONExample as buildAuthJSONExampleSelector,
    claudeExample as buildClaudeExampleSelector,
    openAIExample as buildOpenAIExampleSelector,
    usageStatusExample as buildUsageStatusExampleSelector
  } from './app/endpointExampleSelectors.js';
  import {
    postClientEvent,
    requestJSON
  } from './app/clientHelpers.js';
  import {
    cancelEditMappingState,
    startEditMappingState
  } from './app/mappingHelpers.js';
  import {
    buildClaudeCodeIntegration,
    normalizeDirectAPIStrategy
  } from './app/settingsHelpers.js';
  import {
    buildQueuedThresholdPayload,
    commitThresholdInput,
    normalizeThresholdInput,
    nudgeThresholdInput,
    shouldSkipThresholdPersist,
    thresholdPersistSuccessMessage
  } from './app/usageThresholdHelpers.js';
  import {
    backupUsageCandidates as selectBackupUsageCandidates,
    getActiveAccount as selectActiveUsageAccount,
    getPrimaryUsageWindow as selectPrimaryUsageWindow
  } from './app/usageSelectionHelpers.js';
  import {
    defaultCodexModels,
    jsonHeaders,
    resolvedAPIBase as helperResolvedAPIBase,
    toAPIURL as helperToAPIURL,
    uiPrefsKey
  } from './app/appHelpers.js';
  import {
    clampPercent,
    normalizeAccountTypeLabel,
    parsePercentInput,
    parseSchedulerIntervalInput,
    parseUsageWindows,
    statusClass,
    statusIcon,
    usageLabel
  } from './lib/usageHelpers.js';

  let accounts = $state([]);
  let busy = $state(false);
  let status = $state({ text: 'Initializing...', kind: 'info' });

  let apiKey = $state('');
  let directAPIStrategy = $state('round_robin');
  let openAIEndpoint = $state('');
  let openAIResponsesEndpoint = $state('');
  let claudeEndpoint = $state('');
  let authJSONEndpoint = $state('');
  let usageStatusEndpoint = $state('');
  let isChatRoute = $state(typeof window !== 'undefined' && (window.location.pathname === '/chat' || window.location.pathname.startsWith('/chat/')));
  let activeMenu = $state('dashboard');
  let apiLogs = $state([]);
  let systemLogs = $state([]);
  let systemLogsTotal = $state(0);
  let showSystemLogDetail = $state(false);
  let systemLogDetail = $state(null);
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
  let usageAlertThreshold = $state(5);
  let usageAlertThresholdInput = $state('5');
  let usageAutoSwitchThreshold = $state(2);
  let usageAutoSwitchThresholdInput = $state('2');
  let usageSchedulerIntervalMinutes = $state(60);
  let usageSchedulerIntervalMinutesInput = $state('60');
  let usageSoundEnabled = $state(true);
  let codingTemplateHome = $state(null);
  let codingTemplateHomeBusy = $state(false);
  let claudeCodeIntegration = $state({
    connected: false,
    base_url: '',
    env_file_path: '',
    profiles: [],
    model_preset: {},
    activate_command: '',
    provider: 'codex'
  });
  let showClaudeEnableModal = $state(false);
  let appVersion = $state('dev');
  let codexVersion = $state('unknown');
  let latestVersion = $state('');
  let releaseURL = $state('');
  let latestChangelog = $state('');
  let updateAvailable = $state(false);
  let updateCheckedAt = $state('');
  let updateCheckError = $state('');
  let updateCheckBusy = $state(false);
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
  let accountTypeOptionsSource = $state([]);
  let usageAvailabilityFilter = $state('all');
  let accountStatusFilter = $state('all');
  let totalAccountsFromServer = $state(0);
  let totalFilteredAccounts = $state(0);
  let dashboardPageSize = $state(20);
  let dashboardPage = $state(1);
  let mobileSidebarOpen = $state(false);
  const appMode = (import.meta.env.VITE_APP_MODE || 'web').toLowerCase();

  let showAddAccountModal = $state(false);
  let addAccountMode = $state('menu');

  let browserLoginURL = $state('');
  let browserLoginID = $state('');
  let browserWaiting = $state(false);
  let browserKnownIDs = $state([]);
  let browserCallbackURL = $state('');
  let browserProgressStage = $state('idle');

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
  let usageThresholdLastSavedInterval = null;
  let activeAPIAccountID = $state('');
  let activeAPIAccountEmail = $state('');
  let activeCLIAccountID = $state('');
  let activeCLIAccountEmail = $state('');
  let invalidAccountsTotal = $state(0);
  let accountEmailCache = $state({});
  let soundCache = {};
  let refreshAllInFlight = null;
  let lastRefreshAllAt = 0;
  let suppressActiveSwitchToneUntil = 0;

  const apiBase = String(import.meta.env.VITE_API_BASE || '').trim().replace(/\/+$/, '');

  function setStatus(text, kind = 'info') {
    status = { text, kind };
  }


  function toAPIURL(url) {
    return helperToAPIURL(url, apiBase);
  }

  function sendClientEvent(type, message, data = {}, level = 'info') {
    postClientEvent({ type, message, data, level, toAPIURL, jsonHeaders });
  }

  function accountTypeOptions() {
    if (Array.isArray(accountTypeOptionsSource) && accountTypeOptionsSource.length > 0) {
      const opts = accountTypeOptionsSource
        .map((value) => ({
          value,
          label: normalizeAccountTypeLabel(value)
        }));
      return [{ value: 'all', label: 'All Account Types' }, ...opts];
    }
    return buildAccountTypeOptions(accounts, normalizeAccountTypeLabel);
  }

  function filteredAccounts() {
    return accounts;
  }

  function accountUsageSortScore(account) {
    return accountUsageSortScoreForView(account, parseUsageWindows, clampPercent);
  }

  function setDashboardPageSize(value) {
    const result = nextDashboardPageSize(value, dashboardPageSize, pageSizeOptions());
    if (!result.changed) return;
    dashboardPageSize = result.next;
    dashboardPage = 1;
    loadAccounts();
  }

  function setDashboardPage(value) {
    const result = nextDashboardPage(value, dashboardPage);
    if (!result.changed) return;
    dashboardPage = result.next;
    loadAccounts();
  }

  function paginatedAccounts() {
    return buildPaginatedAccounts(accounts, totalFilteredAccounts, dashboardPageSize, dashboardPage);
  }

  const dashboardPagination = $derived(paginatedAccounts());

  function activeAPIAccount() {
    const globalID = String(activeAPIAccountID || '').trim();
    if (globalID) {
      const found = accounts.find((account) => String(account?.id || '').trim() === globalID);
      if (found) return found;
      return { id: globalID, email: String(activeAPIAccountEmail || '').trim() };
    }
    return selectActiveAPIAccount(accounts);
  }

  function activeCLIAccount() {
    const globalID = String(activeCLIAccountID || '').trim();
    if (globalID) {
      const found = accounts.find((account) => String(account?.id || '').trim() === globalID);
      if (found) return found;
      return { id: globalID, email: String(activeCLIAccountEmail || '').trim() };
    }
    return selectActiveCLIAccount(accounts);
  }

  function accountDisplayLabel(account) {
    return accountDisplayLabelForView(account, showAccountEmail);
  }

  function setAccountSearchQuery(value) {
    const result = nextFilterValue(value, accountSearchQuery);
    accountSearchQuery = result.next;
    dashboardPage = 1;
    loadAccounts();
  }

  function setAccountTypeFilter(value) {
    const result = nextFilterValue(value, accountTypeFilter);
    accountTypeFilter = result.next;
    dashboardPage = 1;
    loadAccounts();
  }

  function setUsageAvailabilityFilter(value) {
    const result = nextFilterValue(value, usageAvailabilityFilter);
    usageAvailabilityFilter = result.next;
    dashboardPage = 1;
    loadAccounts();
  }

  function setAccountStatusFilter(value) {
    const result = nextFilterValue(value, accountStatusFilter);
    accountStatusFilter = result.next;
    dashboardPage = 1;
    loadAccounts();
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
    if (typeof window !== 'undefined') {
      const nextPath = pathForMenu(menu);
      if (nextPath && window.location.pathname !== nextPath) {
        window.history.pushState({}, '', nextPath);
      }
    }
    activeMenu = menu;
    isChatRoute = menu === 'coding';
    closeMobileSidebar();
  }

  function syncRouteMode() {
    if (typeof window === 'undefined') return;
    const path = String(window.location.pathname || '').trim().toLowerCase();
    isChatRoute = detectChatRoute(path);
    if (isChatRoute) {
      activeMenu = 'coding';
      closeMobileSidebar();
      return;
    }
    activeMenu = menuFromPath(path);
    closeMobileSidebar();
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
    return selectPrimaryUsageWindow(account, parseUsageWindows, clampPercent);
  }

  function getActiveAccount() {
    return selectActiveUsageAccount(accounts);
  }

  function backupUsageCandidates(currentActiveID) {
    return selectBackupUsageCandidates(accounts, currentActiveID, { getPrimaryUsageWindow, clampPercent });
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

  function openAIExample() {
    return buildOpenAIExampleSelector({ openAIEndpoint, apiKey, buildOpenAIExample });
  }

  function claudeExample() {
    return buildClaudeExampleSelector({ claudeEndpoint, apiKey, buildClaudeExample });
  }

  function authJSONExample() {
    return buildAuthJSONExampleSelector({ authJSONEndpoint, apiKey, buildAuthJSONExample });
  }

  function usageStatusExample() {
    return buildUsageStatusExampleSelector({ usageStatusEndpoint, apiKey, buildUsageStatusExample });
  }

  function openSystemLogDetail(entry) {
    const next = openSystemLogDetailState(entry);
    showSystemLogDetail = next.showSystemLogDetail;
    systemLogDetail = next.systemLogDetail;
  }

  function closeSystemLogDetail() {
    const next = closeSystemLogDetailState();
    showSystemLogDetail = next.showSystemLogDetail;
    systemLogDetail = next.systemLogDetail;
  }

  function openLogDetail(entry) {
    const next = openLogDetailState(entry);
    showLogDetailModal = next.showLogDetailModal;
    logDetailEntry = next.logDetailEntry;
  }

  function closeLogDetailModal() {
    const next = closeLogDetailState();
    showLogDetailModal = next.showLogDetailModal;
    logDetailEntry = next.logDetailEntry;
  }

  async function req(url, options = {}) {
    return requestJSON(url, options, { toAPIURL, jsonHeaders });
  }

  async function loadAccountTypeOptions() {
    try {
      const data = await req('/api/accounts/types');
      const rawTypes = Array.isArray(data?.account_types) ? data.account_types : [];
      const normalized = [...new Set(rawTypes
        .map((value) => String(value || '').trim().toLowerCase())
        .filter(Boolean))]
        .sort((a, b) => a.localeCompare(b));
      accountTypeOptionsSource = normalized;
    } catch {
      accountTypeOptionsSource = [];
    }
  }

  async function loadAccounts() {
    const previousAccounts = Array.isArray(accounts) ? accounts : [];
    const previousAPIActiveID = String(activeAPIAccountID || activeAPIAccount()?.id || '').trim();
    const previousCLIActiveID = String(activeCLIAccountID || activeCLIAccount()?.id || '').trim();
    
    const params = new URLSearchParams({
      page: dashboardPage,
      limit: dashboardPageSize
    });
    if (accountSearchQuery) params.set('q', accountSearchQuery);
    if (accountTypeFilter && accountTypeFilter !== 'all') params.set('type', accountTypeFilter);
    if (accountStatusFilter && accountStatusFilter !== 'all') params.set('status', accountStatusFilter);
    if (usageAvailabilityFilter && usageAvailabilityFilter !== 'all') params.set('usage', usageAvailabilityFilter);

    const data = await req(`/api/accounts?${params.toString()}`);
    const nextAccounts = Array.isArray(data.accounts) ? data.accounts : [];
    accounts = nextAccounts;
    if (Array.isArray(nextAccounts) && nextAccounts.length > 0) {
      const nextCache = { ...(accountEmailCache || {}) };
      for (const account of nextAccounts) {
        const id = String(account?.id || '').trim();
        const email = String(account?.email || '').trim();
        if (id && email) nextCache[id] = email;
      }
      accountEmailCache = nextCache;
    }
    if (typeof data.total_filtered === 'number') {
      totalFilteredAccounts = data.total_filtered;
    }
    activeAPIAccountID = String(data.active_api_account_id || '').trim();
    activeAPIAccountEmail = String(data.active_api_account_email || '').trim();
    activeCLIAccountID = String(data.active_cli_account_id || '').trim();
    activeCLIAccountEmail = String(data.active_cli_account_email || '').trim();
    invalidAccountsTotal = Math.max(0, Number(data.invalid_accounts_total) || 0);
    if (!activeAPIAccountEmail && activeAPIAccountID) {
      activeAPIAccountEmail = String(accountEmailCache?.[activeAPIAccountID] || '').trim();
    }
    if (!activeCLIAccountEmail && activeCLIAccountID) {
      activeCLIAccountEmail = String(accountEmailCache?.[activeCLIAccountID] || '').trim();
    }

    const nextAPIActiveID = String(activeAPIAccountID || nextAccounts.find((a) => a?.active_api)?.id || '').trim();
    const nextCLIActiveID = String(activeCLIAccountID || nextAccounts.find((a) => a?.active_cli)?.id || '').trim();
    const apiSwitched = previousAPIActiveID !== '' && nextAPIActiveID !== '' && previousAPIActiveID !== nextAPIActiveID;
    const cliSwitched = previousCLIActiveID !== '' && nextCLIActiveID !== '' && previousCLIActiveID !== nextCLIActiveID;
    const toneSuppressed = Date.now() < suppressActiveSwitchToneUntil;
    if (!toneSuppressed && (apiSwitched || cliSwitched)) {
      playNotificationTone('switch');
      const labelFor = (id, source) => {
        const needle = String(id || '').trim();
        if (!needle) return '-';
        const list = source === 'previous' ? previousAccounts : nextAccounts;
        const found = list.find((account) => String(account?.id || '').trim() === needle);
        const email = String(found?.email || '').trim();
        return email || needle;
      };
      const parts = [];
      if (apiSwitched) {
        parts.push(`API active switched: ${labelFor(previousAPIActiveID, 'previous')} -> ${labelFor(nextAPIActiveID, 'next')}`);
      }
      if (cliSwitched) {
        parts.push(`CLI active switched: ${labelFor(previousCLIActiveID, 'previous')} -> ${labelFor(nextCLIActiveID, 'next')}`);
      }
      const detail = parts.join(' | ');
      const message = `Backend auto-switch detected. ${detail}`;
      setStatus(message, 'success');
      sendClientEvent('backend-active-switch', message, {
        previous_api_active: previousAPIActiveID || null,
        next_api_active: nextAPIActiveID || null,
        previous_cli_active: previousCLIActiveID || null,
        next_cli_active: nextCLIActiveID || null
      }, 'info');
    }
  }

  async function loadSettings() {
    const data = await req('/api/settings');
    apiKey = data.api_key || '';
    directAPIStrategy = normalizeDirectAPIStrategy(data.direct_api_strategy || 'round_robin');
    openAIEndpoint = data.openai_endpoint || '';
    openAIResponsesEndpoint = data.openai_responses_url || '';
    claudeEndpoint = data.claude_endpoint || '';
    authJSONEndpoint = data.auth_json_endpoint || '';
    usageStatusEndpoint = data.usage_status_endpoint || '';
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
    const schedulerIntervalMinutes = Number(data.usage_scheduler_interval_minutes);
    if (Number.isFinite(schedulerIntervalMinutes)) {
      usageSchedulerIntervalMinutes = parseSchedulerIntervalInput(schedulerIntervalMinutes, usageSchedulerIntervalMinutes);
      usageSchedulerIntervalMinutesInput = String(usageSchedulerIntervalMinutes);
      usageThresholdLastSavedInterval = usageSchedulerIntervalMinutes;
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
    claudeCodeIntegration = buildClaudeCodeIntegration(data);
    await loadCodingTemplateHomeStatus();
  }

  async function loadCodingTemplateHomeStatus() {
    const data = await req('/api/coding/template-home');
    codingTemplateHome = (data?.status && typeof data.status === 'object') ? data.status : null;
  }

  async function manageCodingTemplateHome(action) {
    const normalized = String(action || '').trim().toLowerCase() === 'resync' ? 'resync' : 'initialize';
    codingTemplateHomeBusy = true;
    try {
      const data = await req('/api/coding/template-home', {
        method: 'POST',
        body: JSON.stringify({ action: normalized })
      });
      codingTemplateHome = (data?.status && typeof data.status === 'object') ? data.status : null;
      setStatus(normalized === 'resync' ? 'Template home resynced.' : 'Template home initialized.', 'success');
    } catch (error) {
      setStatus(`Failed to update template home: ${error.message}`, 'error');
    } finally {
      codingTemplateHomeBusy = false;
    }
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

  async function loadSystemLogs() {
    const data = await req('/api/system/logs?limit=400');
    const entries = Array.isArray(data.logs) ? data.logs : [];
    systemLogs = entries.map((entry) => ({
      id: String(entry.id || ''),
      kind: String(entry.kind || ''),
      message: String(entry.message || ''),
      metaJSON: String(entry.meta_json || ''),
      createdAt: String(entry.created_at || '')
    }));
    systemLogsTotal = Number(data.total || 0);
  }

  async function clearSystemLogs() {
    busy = true;
    try {
      await req('/api/system/logs', { method: 'DELETE' });
      await loadSystemLogs();
      setStatus('System logs cleared.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function refreshUsageForSelectors(selectors) {
    const uniqueSelectors = [...new Set((selectors || []).map((v) => String(v || '').trim()).filter(Boolean))];
    let refreshed = 0;
    const ran = await withUsageRefreshLock(async () => {
      for (const selector of uniqueSelectors) {
        try {
          await req('/api/usage/refresh', {
            method: 'POST',
            body: JSON.stringify({ selector, source: 'auto' })
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

    const loadTotalTask = async () => {
      try {
        const totalData = await req('/api/accounts/total');
        totalAccountsFromServer = Number(totalData.total) || 0;
      } catch (err) {
        console.error('Failed to load total accounts', err);
      }
    };

    refreshAllInFlight = Promise.all([loadTotalTask(), loadAccounts(), loadAccountTypeOptions(), loadSettings(), loadCodingTemplateHomeStatus()]);
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
      suppressActiveSwitchToneUntil = Date.now() + 5000;
      if (!suppressTone && source !== 'auto') {
        await playNotificationTone(toneKind, { wait: true });
      }
      await req(endpoint, {
        method: 'POST',
        body: JSON.stringify({ selector })
      });
      await refreshAllData({ statusMessage: refreshStatusMessage, force: true });
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
          body: JSON.stringify({ selector, source: 'manual' })
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

  async function backupAllAccounts() {
    busy = true;
    try {
      const payload = await req('/api/accounts/backup');
      const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
      const href = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = href;
      anchor.download = `codexsess-accounts-backup-${new Date().toISOString().replace(/[:.]/g, '-')}.json`;
      document.body.appendChild(anchor);
      anchor.click();
      document.body.removeChild(anchor);
      URL.revokeObjectURL(href);
      setStatus('Backup accounts berhasil diunduh.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function exportAllAccountTokens() {
    busy = true;
    try {
      const payload = await req('/api/accounts/export-tokens');
      const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' });
      const href = URL.createObjectURL(blob);
      const anchor = document.createElement('a');
      anchor.href = href;
      anchor.download = `codexsess-accounts-export-${new Date().toISOString().replace(/[:.]/g, '-')}.json`;
      document.body.appendChild(anchor);
      anchor.click();
      document.body.removeChild(anchor);
      URL.revokeObjectURL(href);
      setStatus('Export tokens berhasil diunduh.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
    }
  }

  async function restoreAccounts(file) {
    if (!file) return;
    busy = true;
    try {
      const text = await file.text();
      const payload = JSON.parse(text || '{}');
      const data = await req('/api/accounts/restore', {
        method: 'POST',
        body: JSON.stringify(payload)
      });
      await refreshAllData({ statusMessage: false, force: true });
      setStatus(`Restore selesai: ${Number(data?.restored || 0)} akun dipulihkan, ${Number(data?.skipped || 0)} dilewati.`, 'success');
    } catch (error) {
      setStatus(`Restore backup gagal: ${error.message}`, 'error');
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

  async function deleteRevokedAccounts() {
    if (!confirm('Delete all revoked accounts permanently? This cannot be undone.')) return;
    busy = true;
    try {
      const data = await req('/api/accounts/revoked', { method: 'DELETE' });
      const n = data?.deleted ?? 0;
      await refreshAllData();
      setStatus(n > 0 ? `Deleted ${n} revoked account(s).` : 'No revoked accounts to delete.', 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      busy = false;
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
    markCopiedState(key, {
      getCopiedAction: () => copiedAction,
      setCopiedAction: (value) => { copiedAction = value; },
      getResetTimer: () => copiedResetTimer,
      setResetTimer: (value) => { copiedResetTimer = value; }
    });
  }

  function isCopied(key) {
    return isCopiedState(copiedAction, key);
  }

  async function copyText(value, label, key = '') {
    await copyTextWithFeedback(value, label, key, { writeClipboardText, markCopied, setStatus });
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

  function toggleUsageSoundEnabled() {
    usageSoundEnabled = !usageSoundEnabled;
    if (usageSoundEnabled) {
      playNotificationTone('info');
      setTimeout(() => {
        playNotificationTone('switch');
      }, 140);
    }
  }

  async function setDirectAPIStrategy(nextStrategy) {
    const normalized = normalizeDirectAPIStrategy(nextStrategy);
    if (directAPIStrategy === normalized) return;
    settingsBusy = true;
    try {
      const data = await req('/api/settings', {
        method: 'POST',
        body: JSON.stringify({ direct_api_strategy: normalized })
      });
      directAPIStrategy = normalizeDirectAPIStrategy(data.direct_api_strategy || normalized);
      setStatus(`Direct API strategy set to ${directAPIStrategy === 'load_balance' ? 'Load Balance' : 'Round Robin'}.`, 'success');
    } catch (error) {
      setStatus(error.message, 'error');
    } finally {
      settingsBusy = false;
    }
  }

  function openClaudeCodeIntegrationModal() {
    showClaudeEnableModal = true;
  }

  function closeClaudeCodeIntegrationModal() {
    if (settingsBusy) return;
    showClaudeEnableModal = false;
  }

  async function enableClaudeCodeIntegration() {
    settingsBusy = true;
    try {
      const data = await req('/api/settings/claude-code', {
        method: 'POST',
        body: JSON.stringify({})
      });
      claudeCodeIntegration = buildClaudeCodeIntegration(data);
      showClaudeEnableModal = false;
      setStatus('Claude Code integration enabled. Run the activation command shown below in your current terminal.', 'success');
    } catch (error) {
      setStatus(`Failed to enable Claude Code integration: ${error.message}`, 'error');
    } finally {
      settingsBusy = false;
    }
  }

  function setUsageAlertThresholdInput(value) {
    const next = normalizeThresholdInput(value, usageAlertThreshold, parsePercentInput);
    usageAlertThreshold = next.value;
    usageAlertThresholdInput = next.input;
  }

  function commitUsageAlertThresholdInput(nextValue = null) {
    const next = commitThresholdInput(nextValue, usageAlertThresholdInput, usageAlertThreshold, parsePercentInput);
    usageAlertThreshold = next.value;
    usageAlertThresholdInput = next.input;
    queuePersistUsageThresholdSettings('alert');
  }

  function nudgeUsageAlertThreshold(delta) {
    const next = nudgeThresholdInput(usageAlertThreshold, delta, parsePercentInput);
    usageAlertThreshold = next.value;
    usageAlertThresholdInput = next.input;
    queuePersistUsageThresholdSettings('alert');
  }

  function setUsageAutoSwitchThresholdInput(value) {
    const next = normalizeThresholdInput(value, usageAutoSwitchThreshold, parsePercentInput);
    usageAutoSwitchThreshold = next.value;
    usageAutoSwitchThresholdInput = next.input;
  }

  function setUsageSchedulerIntervalInput(value) {
    const next = normalizeThresholdInput(value, usageSchedulerIntervalMinutes, parseSchedulerIntervalInput);
    usageSchedulerIntervalMinutes = next.value;
    usageSchedulerIntervalMinutesInput = next.input;
  }

  function commitUsageAutoSwitchThresholdInput(nextValue = null) {
    const next = commitThresholdInput(nextValue, usageAutoSwitchThresholdInput, usageAutoSwitchThreshold, parsePercentInput);
    usageAutoSwitchThreshold = next.value;
    usageAutoSwitchThresholdInput = next.input;
    queuePersistUsageThresholdSettings('switch');
  }

  function nudgeUsageAutoSwitchThreshold(delta) {
    const next = nudgeThresholdInput(usageAutoSwitchThreshold, delta, parsePercentInput);
    usageAutoSwitchThreshold = next.value;
    usageAutoSwitchThresholdInput = next.input;
    queuePersistUsageThresholdSettings('switch');
  }

  function commitUsageSchedulerIntervalInput(nextValue = null) {
    const next = commitThresholdInput(nextValue, usageSchedulerIntervalMinutesInput, usageSchedulerIntervalMinutes, parseSchedulerIntervalInput);
    usageSchedulerIntervalMinutes = next.value;
    usageSchedulerIntervalMinutesInput = next.input;
    queuePersistUsageThresholdSettings('interval');
  }

  function nudgeUsageSchedulerInterval(delta) {
    const next = nudgeThresholdInput(usageSchedulerIntervalMinutes, delta, parseSchedulerIntervalInput);
    usageSchedulerIntervalMinutes = next.value;
    usageSchedulerIntervalMinutesInput = next.input;
    queuePersistUsageThresholdSettings('interval');
  }

  function queuePersistUsageThresholdSettings(changedType) {
    usageThresholdPersistQueued = buildQueuedThresholdPayload({
      usageAlertThreshold,
      usageAutoSwitchThreshold,
      usageSchedulerIntervalMinutes,
      changedType,
      parsePercentInput,
      parseSchedulerIntervalInput
    });
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
    const schedulerIntervalMinutes = parseSchedulerIntervalInput(next.schedulerIntervalMinutes, 60);
    const changedType = next.changedType;
    if (shouldSkipThresholdPersist(next, {
      alert: usageThresholdLastSavedAlert,
      switch: usageThresholdLastSavedSwitch,
      interval: usageThresholdLastSavedInterval
    })) {
      return;
    }

    usageThresholdPersistInFlight = true;
    try {
      const data = await req('/api/settings', {
        method: 'POST',
        body: JSON.stringify({
          usage_alert_threshold: alertThreshold,
          usage_auto_switch_threshold: autoSwitchThreshold,
          usage_scheduler_interval_minutes: schedulerIntervalMinutes
        })
      });
      const savedAlert = parsePercentInput(Number(data.usage_alert_threshold), usageAlertThreshold);
      const savedSwitch = parsePercentInput(Number(data.usage_auto_switch_threshold), usageAutoSwitchThreshold);
      const savedInterval = parseSchedulerIntervalInput(Number(data.usage_scheduler_interval_minutes), usageSchedulerIntervalMinutes);
      usageAlertThreshold = savedAlert;
      usageAlertThresholdInput = String(savedAlert);
      usageAutoSwitchThreshold = savedSwitch;
      usageAutoSwitchThresholdInput = String(savedSwitch);
      usageSchedulerIntervalMinutes = savedInterval;
      usageSchedulerIntervalMinutesInput = String(savedInterval);
      usageThresholdLastSavedAlert = savedAlert;
      usageThresholdLastSavedSwitch = savedSwitch;
      usageThresholdLastSavedInterval = savedInterval;
      setStatus(thresholdPersistSuccessMessage(changedType, { savedAlert, savedSwitch, savedInterval }), 'success');
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

  function startEditMapping(alias) {
    const next = startEditMappingState(alias, modelMappings, availableModels);
    if (!next) return;
    editingMappingAlias = next.editingMappingAlias;
    mappingAlias = next.mappingAlias;
    mappingTargetModel = next.mappingTargetModel;
  }

  function cancelEditMapping() {
    const next = cancelEditMappingState(availableModels);
    editingMappingAlias = next.editingMappingAlias;
    mappingAlias = next.mappingAlias;
    mappingTargetModel = next.mappingTargetModel;
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
    pollTimer = clearIntervalTimer(pollTimer);
  }

  function clearBrowserWaitTimer() {
    browserWaitTimer = clearIntervalTimer(browserWaitTimer);
  }

  function cancelBrowserLoginSession() {
    cancelBrowserLoginSessionRequest({ browserLoginURL, browserLoginID, jsonHeaders });
  }

  function closeAddAccountModal() {
    cancelBrowserLoginSession();
    showAddAccountModal = false;
    addAccountMode = 'menu';
    browserWaiting = false;
    browserLoginURL = '';
    browserLoginID = '';
    browserCallbackURL = '';
    browserProgressStage = 'idle';
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
      browserProgressStage = 'ready';
      browserKnownIDs = accounts.map((a) => a.id);
      setStatus('Browser login is ready. Open the link on this machine; OpenAI should return to localhost:1455 after sign-in.', 'success');
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
	    const statusData = await req(`/api/auth/browser/status?login_id=${encodeURIComponent(browserLoginID || '')}`);
      if (browserProgressStage === 'opened') {
        browserProgressStage = 'waiting';
      }
	    if (statusData?.status === 'pending') {
	      return;
	    }
	    if (statusData?.status === 'success') {
	      const accountID = String(statusData?.account?.id || '').trim();
	      browserProgressStage = 'success';
	      browserWaiting = false;
	      browserLoginURL = '';
	      browserLoginID = '';
	      clearBrowserWaitTimer();
	      await loadAccounts();
	      closeAddAccountModal();
	      setStatus('Browser callback login success. Account added.', 'success');
	      if (accountID) {
	        const usageRefresh = await refreshUsageForSelectors([accountID]);
	        if (usageRefresh.refreshed > 0) {
	          setStatus(`Browser callback login success. Usage refreshed for ${usageRefresh.refreshed}/${usageRefresh.total} account(s).`, 'success');
	        }
	      }
	      return;
	    }
	    if (statusData?.status === 'error' || statusData?.status === 'cancelled') {
	      browserWaiting = false;
	      clearBrowserWaitTimer();
	      browserProgressStage = 'recovery';
	      setStatus(String(statusData?.error || 'Browser login did not complete.'), 'error');
	      return;
	    }
	    await loadAccounts();
      const nowIDs = accounts.map((a) => a.id);
      const newIDs = nowIDs.filter((id) => !browserKnownIDs.includes(id));
      if (newIDs.length > 0) {
        browserProgressStage = 'success';
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
    browserProgressStage = 'opened';
    setStatus('Browser tab opened. Finish sign-in there; the real callback target is localhost:1455 on this machine.', 'info');
    scheduleBrowserWait();
  }

  function validateManualBrowserCallbackURL(raw) {
    const callbackURL = String(raw || '').trim();
    if (!callbackURL) {
      return 'Callback URL is required.';
    }
    let parsed;
    try {
      parsed = new URL(callbackURL);
    } catch {
      return 'Callback URL must be a valid URL.';
    }
    if (parsed.protocol !== 'http:' || parsed.hostname !== 'localhost' || parsed.port !== '1455') {
      return 'Callback URL must target http://localhost:1455.';
    }
    if (!parsed.searchParams.get('code')) {
      return 'Callback URL must include code.';
    }
    if (!parsed.searchParams.get('state')) {
      return 'Callback URL must include state.';
    }
    return '';
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
    const callbackValidationError = validateManualBrowserCallbackURL(callbackURL);
    if (callbackValidationError) {
      setStatus(callbackValidationError, 'error');
      return;
    }
    busy = true;
    browserProgressStage = 'recovery';
    try {
      const data = await req('/api/auth/browser/complete', {
        method: 'POST',
        body: JSON.stringify({ login_id: loginID, callback_url: callbackURL })
      });
      const accountID = String(data?.account?.id || '').trim();
      browserProgressStage = 'success';
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

  const browserCallbackValidationError = $derived(validateManualBrowserCallbackURL(browserCallbackURL));

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
    const prefs = readUIPreferences(uiPrefsKey);
    showAccountEmail = prefs.showAccountEmail;
    usageSoundEnabled = prefs.usageSoundEnabled;
    uiPrefsLoaded = true;
  }

  function saveUIPreferences() {
    persistUIPreferences(uiPrefsKey, {
      showAccountEmail,
      usageSoundEnabled
    });
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

    if (isChatRoute) {
      loadSettings().catch((error) => {
        setStatus(error.message, 'error');
      });
    } else {
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
    if (activeMenu !== 'system-logs') return;
    loadSystemLogs().catch((error) => setStatus(error.message, 'error'));
    const timer = setInterval(() => {
      loadSystemLogs().catch(() => {});
    }, 8000);
    return () => clearInterval(timer);
  });

  $effect(() => {
    if (isChatRoute) return;
    const timer = setInterval(() => {
      refreshAllData({ statusMessage: false, force: true }).catch(() => {});
    }, 60000);
    return () => clearInterval(timer);
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
  <link rel="icon" type="image/png" href="/favicon.png" />
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
      <span class="brand-meta brand-meta-count">[{totalAccountsFromServer} Accounts]</span>
      <span class="brand-title">Codex Account Management</span>
      <span class="brand-meta brand-meta-cli">
        {#if String(codexVersion || '').trim().toLowerCase().includes('codex-cli')}
          {codexVersion}
        {:else}
          codex-cli {codexVersion}
        {/if}
      </span>
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
          <svg viewBox="0 0 24 24"><path d="M4 5.5A2.5 2.5 0 0 1 6.5 3h11A2.5 2.5 0 0 1 20 5.5v8A2.5 2.5 0 0 1 17.5 16H10l-4.5 4v-4H6.5A2.5 2.5 0 0 1 4 13.5v-8zm4 2.5h8v1.8H8zm0 3.6h5.8v1.8H8z"></path></svg>
        </span>
        <span>Chat</span>
      </button>
      <button class={activeMenu === 'api-endpoints' ? 'is-active' : ''} onclick={() => switchMenu('api-endpoints')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M3 7.5A2.5 2.5 0 0 1 5.5 5h5A2.5 2.5 0 0 1 13 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-5A2.5 2.5 0 0 1 3 16.5v-9zm8 0A2.5 2.5 0 0 1 13.5 5h5A2.5 2.5 0 0 1 21 7.5v9a2.5 2.5 0 0 1-2.5 2.5h-5A2.5 2.5 0 0 1 11 16.5v-9zM6 8h4v1.6H6zm0 3h4v1.6H6zm7-3h4v1.6h-4zm0 3h4v1.6h-4z"></path></svg>
        </span>
        <span>Workspaces</span>
      </button>
      <button class={activeMenu === 'logs' ? 'is-active' : ''} onclick={() => switchMenu('logs')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M4 4h16v4H4V4zm0 6h16v10H4V10zm3 3v4h2v-4H7zm4 0v4h2v-4h-2zm4 0v4h2v-4h-2z"></path></svg>
        </span>
        <span>API Activity</span>
      </button>
      <button class={activeMenu === 'system-logs' ? 'is-active' : ''} onclick={() => switchMenu('system-logs')}>
        <span class="nav-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M4 5h16v4H4V5zm0 5h16v4H4v-4zm0 5h10v4H4v-4z"></path></svg>
        </span>
        <span>System Logs</span>
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

  <main class="content {activeMenu === 'logs' || activeMenu === 'system-logs' ? 'content-logs' : ''}">
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
        <div class="active-summary-item">
          <span class="active-summary-label">Invalid</span>
          <strong>{invalidAccountsTotal}</strong>
        </div>
      </section>
    {/if}

    {#if activeMenu === 'coding'}
      <CodingView activeCLIEmail={String(activeCLIAccount()?.email || '').trim()} />
    {/if}

    {#if !isChatRoute && activeMenu === 'dashboard'}
      <DashboardView
        accounts={dashboardPagination.items}
        totalAccounts={totalAccountsFromServer}
        filteredCount={dashboardPagination.totalFiltered}
        page={dashboardPagination.page}
        totalPages={dashboardPagination.totalPages}
        perPage={dashboardPagination.perPage}
        pageStart={dashboardPagination.startIndex}
        pageEnd={dashboardPagination.endIndex}
        pageSizeOptions={pageSizeOptions()}
        onSetPage={setDashboardPage}
        onSetPageSize={setDashboardPageSize}
        {showAccountEmail}
        {busy}
        {accountSearchQuery}
        {accountTypeFilter}
        {usageAvailabilityFilter}
        {accountStatusFilter}
        accountTypeOptions={accountTypeOptions()}
        accountStatusOptions={accountStatusOptions()}
        usageAvailabilityOptions={usageAvailabilityOptions()}
        onSetAccountSearchQuery={setAccountSearchQuery}
        onSetAccountTypeFilter={setAccountTypeFilter}
        onSetUsageAvailabilityFilter={setUsageAvailabilityFilter}
        onSetAccountStatusFilter={setAccountStatusFilter}
        onOpenAddAccountModal={openAddAccountModal}
        onBackupAccounts={backupAllAccounts}
        onExportAccountTokens={exportAllAccountTokens}
        onRestoreAccounts={restoreAccounts}
        onDeleteRevokedAccounts={deleteRevokedAccounts}
        onUseApiAccount={useAccount}
        onUseCliAccount={useCLIAccount}
        onRefreshUsage={refreshUsage}
        onOpenRemoveModal={openRemoveModal}
        {usageLabel}
        {clampPercent}
        {parseUsageWindows}
        {formatResetLabel}
        {nowTick}
        {activeUsageAlert}
      />
    {/if}

    {#if !isChatRoute && activeMenu === 'settings'}
      <SettingsView
        busy={settingsBusy}
        {directAPIStrategy}
        {codingTemplateHome}
        codingTemplateBusy={codingTemplateHomeBusy}
        onSetDirectAPIStrategy={setDirectAPIStrategy}
        onInitializeCodingTemplateHome={() => manageCodingTemplateHome('initialize')}
        onResyncCodingTemplateHome={() => manageCodingTemplateHome('resync')}
        onRefreshCodingTemplateHome={loadCodingTemplateHomeStatus}
        {showAccountEmail}
        onToggleShowAccountEmail={toggleShowAccountEmail}
        {usageAlertThreshold}
        {usageAlertThresholdInput}
        {usageAutoSwitchThreshold}
        {usageAutoSwitchThresholdInput}
        {usageSchedulerIntervalMinutes}
        {usageSchedulerIntervalMinutesInput}
        {usageSoundEnabled}
        onSetUsageAlertThresholdInput={setUsageAlertThresholdInput}
        onCommitUsageAlertThresholdInput={commitUsageAlertThresholdInput}
        onSetUsageAutoSwitchThresholdInput={setUsageAutoSwitchThresholdInput}
        onCommitUsageAutoSwitchThresholdInput={commitUsageAutoSwitchThresholdInput}
        onSetUsageSchedulerIntervalInput={setUsageSchedulerIntervalInput}
        onCommitUsageSchedulerIntervalInput={commitUsageSchedulerIntervalInput}
        onNudgeUsageAlertThreshold={nudgeUsageAlertThreshold}
        onNudgeUsageAutoSwitchThreshold={nudgeUsageAutoSwitchThreshold}
        onNudgeUsageSchedulerInterval={nudgeUsageSchedulerInterval}
        onToggleUsageSoundEnabled={toggleUsageSoundEnabled}
        {backgroundRefreshError}
        {backgroundRefreshLastAt}
      />
    {/if}

    {#if !isChatRoute && activeMenu === 'api-endpoints'}
      <ApiEndpointView
        busy={settingsBusy}
        {apiKey}
        {openAIEndpoint}
        openAIResponsesEndpoint={openAIResponsesEndpoint}
        {claudeEndpoint}
        authJSONEndpoint={authJSONEndpoint}
        usageStatusEndpoint={usageStatusEndpoint}
        {claudeCodeIntegration}
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
        onEnableClaudeCodeIntegration={openClaudeCodeIntegrationModal}
        onCopyText={copyText}
        {isCopied}
        {openAIExample}
        {claudeExample}
        {authJSONExample}
        {usageStatusExample}
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

    {#if !isChatRoute && activeMenu === 'system-logs'}
      <SystemLogsView
        {busy}
        {systemLogs}
        {systemLogsTotal}
        onLoadSystemLogs={loadSystemLogs}
        onClearSystemLogs={clearSystemLogs}
        onOpenSystemLogDetail={openSystemLogDetail}
        formatLogTimestamp={formatLogTimestamp}
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
            <p class="modal-subtitle">Connect ChatGPT account with local browser login or device login.</p>
          </div>
          <button class="btn btn-secondary btn-small" onclick={closeAddAccountModal} disabled={busy}>Close</button>
        </div>

        {#if addAccountMode === 'menu'}
          <p class="modal-helper">Choose preferred login flow.</p>
          <div class="method-grid">
            <button class="method-card" onclick={startBrowserLogin} disabled={busy}>
              <span class="method-title">Browser Login</span>
              <span class="method-desc">Open ChatGPT login in a new tab on this machine. OpenAI returns to localhost:1455 after sign-in.</span>
            </button>
            <button class="method-card" onclick={startDeviceLogin} disabled={busy}>
              <span class="method-title">Device Code</span>
              <span class="method-desc">Copy one-time code, sign in on OpenAI device page, then verify.</span>
            </button>
          </div>
        {/if}

        {#if addAccountMode === 'browser'}
          <div class="modal-body">
            <div class="browser-login-section" aria-label="Browser login">
              <div class="browser-login-section-head">
                <strong>Browser login</strong>
                <span class={`browser-login-status browser-login-status-${browserProgressStage}`}>
                  {#if browserProgressStage === 'ready'}Ready{/if}
                  {#if browserProgressStage === 'opened'}Browser opened{/if}
                  {#if browserProgressStage === 'waiting'}Waiting{/if}
                  {#if browserProgressStage === 'recovery'}Manual fallback{/if}
                  {#if browserProgressStage === 'success'}Connected{/if}
                  {#if browserProgressStage === 'idle'}Not started{/if}
                </span>
              </div>
              <p class="modal-helper">Open the login on this machine. After sign-in, OpenAI should return to <span class="mono">localhost:1455</span>.</p>
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
                  Open Browser
                </button>
              </div>
              {#if browserWaiting}
                <p class="modal-helper">Waiting for the localhost:1455 return and the new account to appear.</p>
              {/if}
            </div>

            <details class="browser-login-section browser-login-section-secondary" ontoggle={(event) => event.currentTarget.open && browserProgressStage !== 'success' && (browserProgressStage = 'recovery')}>
              <summary>Use manual callback if the browser already returned</summary>
              <div class="stack-small" style="margin-top: 12px;">
                <p class="modal-helper">Only use this if the browser already hit <span class="mono">localhost:1455</span> and the account still did not show up here.</p>
                <label for="browserCallbackUrl">Callback URL</label>
                <div class="device-code-row">
                  <input
                    id="browserCallbackUrl"
                    value={browserCallbackURL}
                    placeholder="http://localhost:1455/auth/callback?code=...&state=..."
                    oninput={(event) => {
                      browserCallbackURL = event.currentTarget.value;
                      if (browserCallbackURL.trim() && browserProgressStage !== 'success') {
                        browserProgressStage = 'recovery';
                      }
                    }}
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
                {#if browserCallbackURL.trim() && browserCallbackValidationError}
                  <p class="modal-helper" style="color: var(--danger);">{browserCallbackValidationError}</p>
                {/if}
              </div>
            </details>
            <div class="panel-actions">
              <button class="btn btn-secondary" onclick={() => (addAccountMode = 'menu')} disabled={busy}>Back</button>
            </div>
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
              <span> · </span>
              <span class="mono">
                tok req:{logDetailEntry.requestTokens || 0} resp:{logDetailEntry.responseTokens || 0} total:{logDetailEntry.totalTokens || 0}
              </span>
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

  {#if showClaudeEnableModal}
    <div class="modal-backdrop" role="presentation" onclick={(event) => event.target === event.currentTarget && closeClaudeCodeIntegrationModal()}>
      <div class="modal-card" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && closeClaudeCodeIntegrationModal()}>
        <div class="modal-head">
          <div>
            <h3>Enable Claude Code</h3>
            <p class="modal-subtitle">Choose provider for Claude Code integration.</p>
          </div>
          <button class="btn btn-secondary btn-small" onclick={closeClaudeCodeIntegrationModal} disabled={settingsBusy}>Close</button>
        </div>
        <div class="modal-body">
          <p class="setting-title">Claude Code integration now uses CodexSess endpoint only.</p>
          <div class="panel-actions">
            <button class="btn btn-primary" onclick={enableClaudeCodeIntegration} disabled={settingsBusy}>
              Enable Now
            </button>
          </div>
        </div>
      </div>
    </div>
  {/if}

  {#if showSystemLogDetail && systemLogDetail}
    <div class="modal-backdrop" role="presentation" onclick={(event) => event.target === event.currentTarget && closeSystemLogDetail()}>
      <div class="modal-card log-detail-card" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && closeSystemLogDetail()}>
        <div class="modal-head">
          <div>
            <h3>System Log Detail</h3>
            <p class="modal-subtitle">Entry metadata snapshot.</p>
          </div>
          <button class="btn btn-secondary btn-small" onclick={closeSystemLogDetail}>Close</button>
        </div>
        <div class="log-detail-grid">
          <div class="log-payload">
            <p>Message</p>
            <pre>{systemLogDetail.message || '-'}</pre>
          </div>
          <div class="log-payload">
            <p>Meta JSON</p>
            <pre>{systemLogDetail.metaJSON || '-'}</pre>
          </div>
        </div>
      </div>
    </div>
  {/if}
</div>
