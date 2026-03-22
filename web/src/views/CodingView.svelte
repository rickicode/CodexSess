<script>
  import { onMount, onDestroy, tick } from 'svelte';

  let sessions = $state([]);
  let activeSessionID = $state('');
  let messages = $state([]);
  let draftMessage = $state('');
  let selectedModel = $state('gpt-5.2-codex');
  let selectedWorkDir = $state('~/');
  let selectedSandboxMode = $state('write');
  let loadingSessions = $state(false);
  let loadingMessages = $state(false);
  let sending = $state(false);
  let deleting = $state(false);
  let viewStatus = $state('Ready.');
  let composerError = $state('');
  let streamingPending = $state(false);
  let messagesViewport = $state(null);
  let showNewSessionModal = $state(false);
  let newSessionPath = $state('~/');
  let pathSuggestions = $state(['~/']);
  let loadingPathSuggestions = $state(false);
  let pathSuggestTimer = null;
  let sessionPrefsTimer = null;
  let persistingSessionPrefs = $state(false);
  let showSkillModal = $state(false);
  let availableSkills = $state([]);
  let skillSearchQuery = $state('');
  let loadingSkills = $state(false);
  let showSessionDrawer = $state(false);
  let codexCLIEmail = $state('');
  let backgroundMonitorTimer = null;
  let expandedMessageMap = $state({});
  let logViewMode = $state('compact');
  let backgroundProcessing = $state(false);
  let stopRequested = $state(false);
  let activeExecCommand = $state('');
  let wsStreamSocket = $state(null);

  const apiBase = String(import.meta.env.VITE_API_BASE || '').trim().replace(/\/+$/, '');
  const draftStoragePrefix = 'codexsess.coding.draft.v1:';
  const viewModeStorageKey = 'codexsess.coding.view_mode.v1';
  const enableSkillHintWrap = ['1', 'true', 'yes', 'on'].includes(
    String(import.meta.env.VITE_CODEXSESS_SKILL_HINT_WRAP || '').trim().toLowerCase()
  );
  const jsonHeaders = { 'Content-Type': 'application/json' };
  const models = ['gpt-5.2-codex', 'gpt-5.3-codex', 'gpt-5.4-mini', 'gpt-5.4'];
  const slashCommands = ['/status', '/review [optional focus]'];

  function isLoopbackHost(hostname) {
    const host = String(hostname || '').trim().toLowerCase();
    return host === '127.0.0.1' || host === 'localhost' || host === '::1' || host === '[::1]';
  }

  function resolvedAPIBase() {
    if (!apiBase) return '';
    if (typeof window === 'undefined') return apiBase;
    try {
      const parsed = new URL(apiBase, window.location.origin);
      if (isLoopbackHost(parsed.hostname) && !isLoopbackHost(window.location.hostname)) {
        return '';
      }
      return `${parsed.origin}`.replace(/\/+$/, '');
    } catch {
      return apiBase;
    }
  }

  function toAPIURL(url) {
    const raw = String(url || '').trim();
    if (/^https?:\/\//i.test(raw)) return raw;
    const base = resolvedAPIBase();
    if (!base) return raw;
    if (raw.startsWith('/')) return `${base}${raw}`;
    return `${base}/${raw}`;
  }

  function toWSURL(path) {
    const raw = String(path || '').trim();
    const base = toAPIURL(raw);
    try {
      const u = new URL(base, (typeof window !== 'undefined' ? window.location.origin : undefined));
      if (u.protocol === 'https:') u.protocol = 'wss:';
      else if (u.protocol === 'http:') u.protocol = 'ws:';
      return u.toString();
    } catch {
      if (typeof window !== 'undefined') {
        const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        return `${proto}//${window.location.host}${raw.startsWith('/') ? raw : `/${raw}`}`;
      }
      return raw;
    }
  }

  function buildWSURLCandidates(path) {
    const raw = String(path || '').trim();
    const out = [];
    if (typeof window !== 'undefined') {
      const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const sameOrigin = `${proto}//${window.location.host}${raw.startsWith('/') ? raw : `/${raw}`}`;
      out.push(sameOrigin);
    }
    const fromBase = toWSURL(raw);
    if (fromBase && !out.includes(fromBase)) {
      out.push(fromBase);
    }
    return out.filter(Boolean);
  }

  async function req(url, options = {}) {
    const response = await fetch(toAPIURL(url), {
      headers: jsonHeaders,
      credentials: 'same-origin',
      ...options
    });
    if (response.redirected && String(response.url || '').includes('/auth/login')) {
      if (typeof window !== 'undefined') window.location.href = '/auth/login';
      throw new Error('Authentication required');
    }
    const text = await response.text();
    let body = {};
    try {
      body = JSON.parse(text || '{}');
    } catch {
      body = {};
    }
    if (!response.ok) {
      const message = body?.error?.message || body?.message || text || `HTTP ${response.status}`;
      throw new Error(message);
    }
    return body;
  }

  function formatWhen(value) {
    const d = new Date(String(value || ''));
    if (Number.isNaN(d.getTime())) return '-';
    return d.toLocaleString();
  }

  function normalizeActivityCommandKey(command) {
    return String(command || '').trim().replace(/\s+/g, ' ').toLowerCase();
  }

  function parseActivityText(rawText) {
    const text = String(rawText || '').trim();
    if (!text) return { kind: 'other', command: '', exitCode: 0 };
    let m = text.match(/^Running:\s+(.+)$/i);
    if (m) {
      return { kind: 'running', command: String(m[1] || '').trim(), exitCode: 0 };
    }
    m = text.match(/^Command done:\s+(.+)$/i);
    if (m) {
      return { kind: 'done', command: String(m[1] || '').trim(), exitCode: 0 };
    }
    m = text.match(/^Command failed\s+\(exit\s+(\d+)\):\s+(.+)$/i);
    if (m) {
      return {
        kind: 'failed',
        command: String(m[2] || '').trim(),
        exitCode: Number(m[1]) || 0
      };
    }
    return { kind: 'other', command: '', exitCode: 0 };
  }

  function mergeActivityContent(startText, endText) {
    const first = String(startText || '').trim();
    const second = String(endText || '').trim();
    if (!first) return second || '-';
    if (!second) return first || '-';
    if (first === second) return first;
    return `${first}\n${second}`;
  }

  function compactActivityMessages(inputMessages) {
    const src = Array.isArray(inputMessages) ? inputMessages : [];
    const out = [];
    const runningIndexByKey = new Map();

    for (const item of src) {
      if (String(item?.role || '').trim().toLowerCase() !== 'activity') {
        out.push(item);
        continue;
      }
      const parsed = parseActivityText(item?.content || '');
      const key = normalizeActivityCommandKey(parsed.command);
      if (parsed.kind === 'running' && key) {
        if (runningIndexByKey.has(key)) {
          const idx = runningIndexByKey.get(key);
          if (typeof idx === 'number' && out[idx]) {
            out[idx] = {
              ...out[idx],
              content: item?.content || out[idx].content || '-'
            };
            continue;
          }
        }
        out.push(item);
        runningIndexByKey.set(key, out.length - 1);
        continue;
      }
      if ((parsed.kind === 'done' || parsed.kind === 'failed') && key) {
        if (runningIndexByKey.has(key)) {
          const idx = runningIndexByKey.get(key);
          if (typeof idx === 'number' && out[idx]) {
            out[idx] = {
              ...out[idx],
              content: mergeActivityContent(out[idx].content || '', item?.content || '')
            };
            runningIndexByKey.delete(key);
            continue;
          }
        }
      }
      out.push(item);
    }

    return out;
  }

  function appendActivityMessage(rawText) {
    const text = String(rawText || '').trim();
    if (!text) return '';
    const parsed = parseActivityText(text);
    if (parsed.kind === 'running' && parsed.command) {
      activeExecCommand = parsed.command;
    } else if ((parsed.kind === 'done' || parsed.kind === 'failed') && parsed.command) {
      if (normalizeActivityCommandKey(parsed.command) === normalizeActivityCommandKey(activeExecCommand)) {
        activeExecCommand = '';
      }
    }
    const next = {
      id: `activity-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      role: 'activity',
      content: text,
      created_at: new Date().toISOString(),
      pending: false
    };
    messages = compactActivityMessages([...messages, next]);
    return String(next.id || '');
  }

  function appendStreamMetaMessage(role, rawText) {
    const messageRole = String(role || '').trim().toLowerCase();
    const text = String(rawText || '').trim();
    if (!messageRole || !text) return '';
    const next = {
      id: `${messageRole}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      role: messageRole,
      content: text,
      created_at: new Date().toISOString(),
      pending: false
    };
    messages = [...messages, next];
    return String(next.id || '');
  }

  function groupedMessagesForView() {
    const src = Array.isArray(messages) ? messages : [];
    const out = [];
    let openActivityIndex = -1;
    for (const item of src) {
      const role = String(item?.role || '').trim().toLowerCase();
      if (role !== 'activity') {
        openActivityIndex = -1;
        out.push(item);
        continue;
      }
      if (openActivityIndex < 0) {
        out.push({
          ...item,
          id: `activity-group-${String(item?.id || Date.now())}`
        });
        openActivityIndex = out.length - 1;
        continue;
      }
      const existing = out[openActivityIndex];
      if (!existing) {
        out.push({
          ...item,
          id: `activity-group-${String(item?.id || Date.now())}`
        });
        openActivityIndex = out.length - 1;
        continue;
      }
      out[openActivityIndex] = {
        ...existing,
        content: mergeActivityContent(existing?.content || '', item?.content || ''),
        created_at: item?.created_at || existing?.created_at
      };
    }
    return out;
  }

  function activeSession() {
    return sessions.find((item) => item?.id === activeSessionID) || null;
  }

  function sessionDisplayID(session) {
    return String(session?.display_id || session?.codex_thread_id || session?.id || '-').trim() || '-';
  }

  function readSessionIDFromURL() {
    if (typeof window === 'undefined') return '';
    try {
      const url = new URL(window.location.href);
      return String(url.searchParams.get('id') || '').trim();
    } catch {
      return '';
    }
  }

  function syncSessionIDToURL(sessionID) {
    if (typeof window === 'undefined') return;
    const sid = String(sessionID || '').trim();
    try {
      const url = new URL(window.location.href);
      if (sid) {
        url.searchParams.set('id', sid);
      } else {
        url.searchParams.delete('id');
      }
      window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`);
    } catch {
    }
  }

  function assistantDisplayName() {
    const email = String(codexCLIEmail || '').trim();
    if (!email) return 'Codex';
    return `Codex - ${email}`;
  }

  function messageRoleClass(message) {
    const role = String(message?.role || '').trim().toLowerCase();
    if (role === 'assistant') return 'assistant';
    if (role === 'activity') return 'activity';
    if (role === 'event') return 'event';
    if (role === 'stderr') return 'stderr';
    return 'user';
  }

  function renderedMessagesForView() {
    let rendered = groupedMessagesForView();
    if (String(logViewMode || '').trim().toLowerCase() !== 'raw') {
      rendered = rendered.filter((item) => String(item?.role || '').trim().toLowerCase() !== 'event');
    }
    return rendered;
  }

  function setLogViewMode(mode) {
    const normalized = String(mode || '').trim().toLowerCase() === 'raw' ? 'raw' : 'compact';
    logViewMode = normalized;
    if (typeof window !== 'undefined') {
      try {
        localStorage.setItem(viewModeStorageKey, normalized);
      } catch {
      }
    }
  }

  function shouldCollapseContent(content) {
    const text = String(content || '');
    if (!text) return false;
    if (text.length > 1600) return true;
    return text.split('\n').length > 20;
  }

  function messagePreviewContent(content) {
    const text = String(content || '');
    if (!shouldCollapseContent(text)) return text;
    const lines = text.split('\n');
    if (lines.length > 20) {
      return `${lines.slice(0, 20).join('\n')}\n...`;
    }
    return `${text.slice(0, 1600)}\n...`;
  }

  function sanitizeSensitiveLogText(input) {
    let text = String(input || '');
    if (!text) return '';
    text = text.replace(/("(?:access_token|refresh_token|id_token|api[_-]?key|authorization|anthropic_auth_token)"\s*:\s*")([^"]+)(")/gi, '$1[REDACTED]$3');
    text = text.replace(/\b((?:access_token|refresh_token|id_token|api[_-]?key|authorization|anthropic_auth_token)\s*=\s*)(\S+)/gi, '$1[REDACTED]');
    text = text.replace(/\bBearer\s+[A-Za-z0-9._-]+/gi, 'Bearer [REDACTED]');
    text = text.replace(/\bsk-[A-Za-z0-9]{12,}\b/g, 'sk-[REDACTED]');
    text = text.replace(/\/home\/[^/\s]+/g, '/home/[user]');
    return text;
  }

  function messageDisplayContent(message) {
    const role = String(message?.role || '').trim().toLowerCase();
    const content = String(message?.content || '-');
    if (role === 'event' || role === 'stderr' || role === 'activity') {
      return sanitizeSensitiveLogText(content);
    }
    return content;
  }

  function isMessageExpanded(id) {
    return Boolean(expandedMessageMap?.[String(id || '')]);
  }

  function toggleMessageExpanded(id) {
    const key = String(id || '').trim();
    if (!key) return;
    expandedMessageMap = {
      ...expandedMessageMap,
      [key]: !expandedMessageMap?.[key]
    };
  }

  async function refreshCodexIdentity() {
    try {
      const data = await req('/api/accounts');
      const items = Array.isArray(data?.accounts) ? data.accounts : [];
      const activeCLI = items.find((item) => Boolean(item?.active_cli));
      codexCLIEmail = String(activeCLI?.email || '').trim();
    } catch {
      codexCLIEmail = '';
    }
  }

  function draftStorageKey(sessionID) {
    const sid = String(sessionID || '').trim();
    if (!sid) return '';
    return `${draftStoragePrefix}${sid}`;
  }

  function saveDraftForSession(sessionID, text) {
    if (typeof window === 'undefined') return;
    const key = draftStorageKey(sessionID);
    if (!key) return;
    const value = String(text || '');
    try {
      if (!value.trim()) {
        localStorage.removeItem(key);
        return;
      }
      localStorage.setItem(key, value);
    } catch {
    }
  }

  function loadDraftForSession(sessionID) {
    if (typeof window === 'undefined') return '';
    const key = draftStorageKey(sessionID);
    if (!key) return '';
    try {
      return String(localStorage.getItem(key) || '');
    } catch {
      return '';
    }
  }

  function clearDraftForSession(sessionID) {
    if (typeof window === 'undefined') return;
    const key = draftStorageKey(sessionID);
    if (!key) return;
    try {
      localStorage.removeItem(key);
    } catch {
    }
  }

  async function loadSessions({ autoSelect = true } = {}) {
    loadingSessions = true;
    try {
      const data = await req('/api/coding/sessions');
      sessions = Array.isArray(data.sessions) ? data.sessions : [];
      if (autoSelect && sessions.length > 0 && !sessions.find((item) => item.id === activeSessionID)) {
        activeSessionID = sessions[0].id;
      }
      const active = activeSession();
      if (active) {
        selectedModel = String(active.model || selectedModel).trim() || selectedModel;
        selectedWorkDir = String(active.work_dir || '~/').trim() || '~/';
        selectedSandboxMode = String(active.sandbox_mode || 'write').trim() || 'write';
      }
    } finally {
      loadingSessions = false;
    }
  }

  async function persistSessionPreferences() {
    const session = activeSession();
    if (!session?.id) return;
    persistingSessionPrefs = true;
    try {
      const data = await req('/api/coding/sessions', {
        method: 'PUT',
        body: JSON.stringify({
          session_id: session.id,
          model: selectedModel,
          work_dir: selectedWorkDir || '~/',
          sandbox_mode: selectedSandboxMode || 'write'
        })
      });
      const updated = data?.session;
      if (updated?.id) {
        sessions = sessions.map((item) => (item.id === updated.id ? updated : item));
      }
    } finally {
      persistingSessionPrefs = false;
    }
  }

  function queuePersistSessionPreferences() {
    if (sessionPrefsTimer) clearTimeout(sessionPrefsTimer);
    sessionPrefsTimer = setTimeout(() => {
      sessionPrefsTimer = null;
      persistSessionPreferences().catch(() => {});
    }, 220);
  }

  async function loadMessages(sessionID, { silent = false } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) {
      messages = [];
      return;
    }
    const showLoading = !silent;
    if (showLoading) {
      loadingMessages = true;
    }
    try {
      const data = await req(`/api/coding/messages?session_id=${encodeURIComponent(sid)}`);
      messages = compactActivityMessages(Array.isArray(data.messages) ? data.messages : []);
      await tick();
      scrollMessagesToBottom();
    } finally {
      if (showLoading) {
        loadingMessages = false;
      }
    }
  }

  async function refreshBackgroundStatus(sessionID, { syncMessages = false } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) {
      backgroundProcessing = false;
      return false;
    }
    const data = await req(`/api/coding/status?session_id=${encodeURIComponent(sid)}`);
    const inFlight = Boolean(data?.in_flight);
    const becameDone = backgroundProcessing && !inFlight;
    backgroundProcessing = inFlight;
    if (inFlight) {
      if (!sending) {
        viewStatus = 'Streaming...';
      }
      // Avoid replacing live WS-rendered messages while actively streaming in this tab.
      if (syncMessages && !loadingMessages && !sending) {
        await loadMessages(sid, { silent: true });
      }
      return true;
    }
    if (syncMessages && !loadingMessages) {
      await loadMessages(sid, { silent: true });
    }
    if (becameDone) {
      await loadMessages(sid, { silent: true });
      viewStatus = 'Background processing finished.';
    }
    return false;
  }

  function stopBackgroundMonitor() {
    if (backgroundMonitorTimer) {
      clearInterval(backgroundMonitorTimer);
      backgroundMonitorTimer = null;
    }
  }

  function startBackgroundMonitor(sessionID) {
    const sid = String(sessionID || '').trim();
    stopBackgroundMonitor();
    if (!sid) {
      backgroundProcessing = false;
      return;
    }
    refreshBackgroundStatus(sid, { syncMessages: !sending })
      .then((inFlight) => {
        if (!inFlight) return;
        backgroundMonitorTimer = setInterval(() => {
          const activeID = String(activeSessionID || '').trim();
          if (!activeID || activeID !== sid) {
            stopBackgroundMonitor();
            return;
          }
          refreshBackgroundStatus(activeID, { syncMessages: !sending })
            .then((stillInFlight) => {
              if (!stillInFlight) {
                stopBackgroundMonitor();
              }
            })
            .catch(() => {});
        }, 3000);
      })
      .catch(() => {});
  }

  function scrollMessagesToBottom() {
    if (!messagesViewport) return;
    messagesViewport.scrollTop = messagesViewport.scrollHeight;
    if (typeof window !== 'undefined') {
      window.requestAnimationFrame(() => {
        if (!messagesViewport) return;
        messagesViewport.scrollTop = messagesViewport.scrollHeight;
      });
      setTimeout(() => {
        if (!messagesViewport) return;
        messagesViewport.scrollTop = messagesViewport.scrollHeight;
      }, 60);
    }
  }

  async function loadPathSuggestions(prefix = '~/') {
    loadingPathSuggestions = true;
    try {
      const data = await req(`/api/coding/path-suggestions?prefix=${encodeURIComponent(prefix)}`);
      const values = Array.isArray(data.suggestions) ? data.suggestions : [];
      pathSuggestions = values.length > 0 ? values : [prefix || '~/'];
    } finally {
      loadingPathSuggestions = false;
    }
  }

  function schedulePathSuggestions(prefix) {
    if (pathSuggestTimer) clearTimeout(pathSuggestTimer);
    pathSuggestTimer = setTimeout(() => {
      pathSuggestTimer = null;
      loadPathSuggestions(prefix).catch(() => {});
    }, 180);
  }

  async function loadSkills() {
    loadingSkills = true;
    try {
      const data = await req('/api/coding/skills');
      availableSkills = Array.isArray(data.skills) ? data.skills : [];
    } finally {
      loadingSkills = false;
    }
  }

  function openSkillModal() {
    showSkillModal = true;
    skillSearchQuery = '';
    loadSkills().catch(() => {});
  }

  function closeSkillModal() {
    showSkillModal = false;
  }

  function filteredSkills() {
    const q = String(skillSearchQuery || '').trim().toLowerCase();
    if (!q) return availableSkills;
    return availableSkills.filter((item) => String(item || '').toLowerCase().includes(q));
  }

  function insertSkillToken(skillName) {
    const name = String(skillName || '').trim();
    if (!name) return;
    const token = `$${name}`;
    const base = String(draftMessage || '').trim();
    draftMessage = base ? `${base} ${token}` : token;
    closeSkillModal();
    viewStatus = `Skill inserted: ${token}`;
  }

  async function createSession({ autoOpen = true, workDir = '~/' } = {}) {
    const data = await req('/api/coding/sessions', {
      method: 'POST',
      body: JSON.stringify({
        title: '',
        model: selectedModel,
        work_dir: String(workDir || '~/').trim() || '~/',
        sandbox_mode: selectedSandboxMode || 'write'
      })
    });
    const created = data?.session;
    if (!created?.id) throw new Error('Failed to create session');
    await loadSessions({ autoSelect: false });
    if (autoOpen) {
      activeSessionID = created.id;
      syncSessionIDToURL(created.id);
      selectedModel = String(created.model || selectedModel).trim() || selectedModel;
      selectedWorkDir = String(created.work_dir || '~/').trim() || '~/';
      selectedSandboxMode = String(created.sandbox_mode || 'write').trim() || 'write';
      await loadMessages(created.id);
      draftMessage = loadDraftForSession(created.id);
      startBackgroundMonitor(created.id);
    }
    viewStatus = 'New session created.';
  }

  function openNewSessionModal() {
    newSessionPath = selectedWorkDir || '~/';
    showNewSessionModal = true;
    loadPathSuggestions(newSessionPath).catch(() => {});
  }

  function closeNewSessionModal() {
    if (sending) return;
    showNewSessionModal = false;
  }

  async function createSessionFromModal() {
    const path = String(newSessionPath || '').trim() || '~/';
    await createSession({ autoOpen: true, workDir: path });
    closeNewSessionModal();
  }

  async function ensureSessionOnFirstOpen() {
    await loadSessions({ autoSelect: true });
    if (sessions.length === 0) {
      await createSession({ autoOpen: true, workDir: '~/' });
      return;
    }
    const requestedID = readSessionIDFromURL();
    if (requestedID && sessions.find((item) => item.id === requestedID)) {
      activeSessionID = requestedID;
    } else if (!activeSessionID) {
      activeSessionID = sessions[0].id;
    }
    syncSessionIDToURL(activeSessionID);
    const active = activeSession();
    if (active) {
      selectedModel = String(active.model || selectedModel).trim() || selectedModel;
      selectedWorkDir = String(active.work_dir || '~/').trim() || '~/';
      selectedSandboxMode = String(active.sandbox_mode || 'write').trim() || 'write';
    }
    await loadMessages(activeSessionID);
    draftMessage = loadDraftForSession(activeSessionID);
    startBackgroundMonitor(activeSessionID);
  }

  async function selectSession(sessionID) {
    const sid = String(sessionID || '').trim();
    if (!sid || sid === activeSessionID) return;
    activeSessionID = sid;
    syncSessionIDToURL(sid);
    const selected = sessions.find((item) => item.id === sid);
    if (selected?.model) selectedModel = selected.model;
    selectedWorkDir = String(selected?.work_dir || '~/').trim() || '~/';
    selectedSandboxMode = String(selected?.sandbox_mode || 'write').trim() || 'write';
    await loadMessages(sid);
    draftMessage = loadDraftForSession(sid);
    startBackgroundMonitor(sid);
    showSessionDrawer = false;
  }

  function backToDashboard() {
    if (typeof window !== 'undefined') {
      window.location.href = '/';
    }
  }

  async function deleteActiveSession() {
    const session = activeSession();
    if (!session?.id || deleting) return;
    if (typeof window !== 'undefined') {
      const ok = window.confirm(`Delete session "${session.title || session.id}"?`);
      if (!ok) return;
    }
    deleting = true;
    try {
      await req(`/api/coding/sessions?id=${encodeURIComponent(session.id)}`, { method: 'DELETE' });
      viewStatus = 'Session deleted.';
      await loadSessions({ autoSelect: true });
      if (sessions.length === 0) {
        await createSession({ autoOpen: true, workDir: '~/' });
      } else {
        syncSessionIDToURL(activeSessionID);
        await loadMessages(activeSessionID);
        draftMessage = loadDraftForSession(activeSessionID);
        startBackgroundMonitor(activeSessionID);
      }
    } finally {
      deleting = false;
    }
  }

  async function sendMessage() {
    if (sending) return;
    const content = String(draftMessage || '').trim();
    if (!content) return;
    const session = activeSession();
    if (!session?.id) return;

    const slashMeta = parseSupportedSlashCommand(content);
    if (slashMeta.error) {
      viewStatus = slashMeta.error;
      return;
    }

    composerError = '';
    const prepared = prepareMessageContent(content);
    const pendingID = `pending-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const pendingMessage = {
      id: pendingID,
      role: 'user',
      content,
      created_at: new Date().toISOString(),
      pending: true
    };
    messages = [...messages, pendingMessage];
    sending = true;
    stopRequested = false;
    backgroundProcessing = false;
    await tick();
    scrollMessagesToBottom();

    const workingDraft = content;
    let liveAssistantID = '';
    let streamedAssistantIDs = [];
    let streamedMetaIDs = [];
    streamingPending = true;
    draftMessage = '';
    viewStatus = 'Streaming...';
    try {
      let donePayload = null;
      donePayload = await streamChatViaWebSocket({
        session_id: session.id,
        content: slashMeta.contentOverride ?? prepared,
        model: selectedModel,
        work_dir: selectedWorkDir || '~/',
        sandbox_mode: selectedSandboxMode || 'write',
        command: slashMeta.commandMode || 'chat'
      }, (evt) => {
        const eventType = String(evt?.event || '').trim().toLowerCase();
        if (eventType === 'assistant_message') {
          const text = String(evt?.text || '').trim();
          if (!text) return;
          streamingPending = false;
          const liveID = `live-assistant-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
          streamedAssistantIDs = [...streamedAssistantIDs, liveID];
          messages = messages
            .concat([{
              id: liveID,
              role: 'assistant',
              content: text,
              created_at: new Date().toISOString(),
              pending: false
            }]);
          scrollMessagesToBottom();
          return;
        }
        if (eventType === 'activity') {
          const text = sanitizeSensitiveLogText(String(evt?.text || '').trim());
          if (!text) return;
          const id = appendActivityMessage(text);
          if (id) streamedMetaIDs = [...streamedMetaIDs, id];
          scrollMessagesToBottom();
          return;
        }
        if (eventType === 'raw_event') {
          const text = sanitizeSensitiveLogText(String(evt?.text || '').trim());
          if (!text) return;
          const id = appendStreamMetaMessage('event', text);
          if (id) streamedMetaIDs = [...streamedMetaIDs, id];
          scrollMessagesToBottom();
          return;
        }
        if (eventType === 'stderr') {
          const text = sanitizeSensitiveLogText(String(evt?.text || '').trim());
          if (!text) return;
          const id = appendStreamMetaMessage('stderr', text);
          if (id) streamedMetaIDs = [...streamedMetaIDs, id];
          scrollMessagesToBottom();
          return;
        }
        if (eventType === 'delta') {
          const deltaText = String(evt?.text || '');
          if (!deltaText) return;
          streamingPending = false;
          if (!liveAssistantID) {
            liveAssistantID = `live-assistant-delta-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
            streamedAssistantIDs = [...streamedAssistantIDs, liveAssistantID];
            messages = messages.concat([{
              id: liveAssistantID,
              role: 'assistant',
              content: '',
              created_at: new Date().toISOString(),
              pending: false
            }]);
          }
          messages = messages.map((item) =>
            item?.id === liveAssistantID
              ? { ...item, content: `${item.content || ''}${deltaText}` }
              : item
          );
          scrollMessagesToBottom();
        }
      });

      if (!donePayload) {
        throw new Error('Streaming ended before completion.');
      }

      const userMessage = donePayload?.user;
      const assistantMessage = donePayload?.assistant;
      const eventMessages = Array.isArray(donePayload?.event_messages) ? donePayload.event_messages : [];
      const assistantMessages = Array.isArray(donePayload?.assistant_messages) ? donePayload.assistant_messages : [];
      streamingPending = false;
      messages = messages.filter(
        (item) =>
          item?.id !== pendingID &&
          !streamedAssistantIDs.includes(item?.id) &&
          !streamedMetaIDs.includes(item?.id)
      );
      if (userMessage) messages = [...messages, userMessage];
      if (eventMessages.length > 0) {
        messages = [...messages, ...eventMessages.map((item) => ({
          ...item,
          content: sanitizeSensitiveLogText(item?.content || '')
        }))];
      }
      if (assistantMessages.length > 0) {
        messages = [...messages, ...assistantMessages];
      } else if (assistantMessage) {
        messages = [...messages, assistantMessage];
      }
      messages = compactActivityMessages(messages);
      clearDraftForSession(session.id);

      if (donePayload?.session?.id) {
        const updated = donePayload.session;
        sessions = sessions.map((item) => (item.id === updated.id ? updated : item));
        sessions = [...sessions].sort((a, b) => String(b.last_message_at || '').localeCompare(String(a.last_message_at || '')));
        selectedWorkDir = String(updated.work_dir || selectedWorkDir).trim() || '~/';
        selectedSandboxMode = String(updated.sandbox_mode || selectedSandboxMode).trim() || 'write';
      }
      await tick();
      scrollMessagesToBottom();
      viewStatus = 'Response received.';
      activeExecCommand = '';
      composerError = '';
    } catch (error) {
      const busy = String(error?.message || '').toLowerCase().includes('already processing');
      const failReason = String(error?.message || 'Failed to send message.');
      const detachedBackground = failReason.toLowerCase().includes('websocket_detached_background');
      if (!busy && !detachedBackground) {
        messages = messages.filter(
          (item) =>
            item?.id !== pendingID &&
            !streamedAssistantIDs.includes(item?.id) &&
            !streamedMetaIDs.includes(item?.id)
        );
        draftMessage = workingDraft;
        saveDraftForSession(session.id, workingDraft);
      }
      const aborted =
        String(error?.name || '').trim() === 'AbortError' ||
        String(error?.message || '').toLowerCase().includes('aborted');
      if (busy || detachedBackground) {
        composerError = '';
        const inFlight = await refreshBackgroundStatus(session.id, { syncMessages: true }).catch(() => false);
        if (inFlight) {
          viewStatus = 'Streaming...';
          startBackgroundMonitor(session.id);
        } else {
          composerError = aborted ? '' : failReason;
          viewStatus = aborted ? (stopRequested ? 'Stopped.' : 'Streaming canceled.') : failReason;
        }
      } else if (stopRequested) {
        composerError = '';
        viewStatus = 'Stopped.';
      } else {
        composerError = aborted ? '' : failReason;
        viewStatus = aborted ? (stopRequested ? 'Stopped.' : 'Streaming canceled.') : failReason;
      }
      streamingPending = false;
      if (!sending) {
        activeExecCommand = '';
      }
    } finally {
      sending = false;
      if (streamingPending) streamingPending = false;
      if (!stopRequested) {
        startBackgroundMonitor(session.id);
      } else {
        backgroundProcessing = false;
      }
      stopRequested = false;
    }
  }

  async function cancelStreaming() {
    if (!sending && !backgroundProcessing) return;
    stopRequested = true;
    viewStatus = 'Stopping...';
    const session = activeSession();
    if (session?.id) {
      try {
        await req('/api/coding/stop', {
          method: 'POST',
          body: JSON.stringify({ session_id: session.id })
        });
      } catch {
      }
    }
    try {
      if (wsStreamSocket) {
        wsStreamSocket.close();
      }
    } catch {
    }
  }

  function onComposerKeydown(event) {
    if (event.shiftKey && event.key === 'Enter') {
      event.preventDefault();
      sendMessage();
      return;
    }
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') {
      event.preventDefault();
      sendMessage();
    }
  }

  function normalizePathInput(raw) {
    const clean = String(raw || '').trim();
    if (!clean) return '~/';
    return clean;
  }

  function setSandboxMode(mode) {
    const normalized = String(mode || '').trim().toLowerCase();
    const next = normalized === 'write' ? 'write' : 'full-access';
    if (selectedSandboxMode === next) return;
    selectedSandboxMode = next;
    queuePersistSessionPreferences();
    viewStatus = `Sandbox set to ${next}.`;
  }

  function toggleSandboxMode() {
    setSandboxMode(selectedSandboxMode === 'full-access' ? 'write' : 'full-access');
  }

  function parseSupportedSlashCommand(input) {
    const raw = String(input || '').trim();
    if (!raw.startsWith('/')) return { commandMode: 'chat', contentOverride: null, error: '' };
    const parts = raw.split(/\s+/);
    const cmd = String(parts[0] || '').toLowerCase();
    const arg = raw.slice(parts[0].length).trim();
    if (cmd === '/status') {
      return { commandMode: 'chat', contentOverride: '/status', error: '' };
    }
    if (cmd === '/review') {
      const reviewPrompt = String(arg || '').trim();
      return { commandMode: 'review', contentOverride: reviewPrompt ? `/review ${reviewPrompt}` : '/review', error: '' };
    }
    return { commandMode: 'chat', contentOverride: raw, error: '' };
  }

  function prepareMessageContent(raw) {
    const original = String(raw || '').trim();
    if (!original) return '';
    if (!enableSkillHintWrap) return original;
    const skillMatches = [...original.matchAll(/\$([a-zA-Z0-9._-]+)/g)];
    if (skillMatches.length === 0) return original;
    const skills = [...new Set(skillMatches.map((match) => String(match[1] || '').trim()).filter(Boolean))];
    if (skills.length === 0) return original;
    return `Skill hints requested: ${skills.join(', ')}.\n\nUser request (unaltered):\n${original}`;
  }

  function streamChatViaWebSocket(payload, onEvent) {
    return new Promise((resolve, reject) => {
      const candidates = buildWSURLCandidates('/api/coding/ws');
      if (candidates.length === 0) {
        reject(new Error('WebSocket endpoint unavailable.'));
        return;
      }

      let settled = false;
      let attemptIndex = 0;
      let ws = null;
      let startedAck = false;

      const finish = (fn, value) => {
        if (settled) return;
        settled = true;
        if (wsStreamSocket === ws) {
          wsStreamSocket = null;
        }
        try {
          if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
            ws.close();
          }
        } catch {
        }
        fn(value);
      };

      const connectAttempt = () => {
        if (settled) return;
        if (attemptIndex >= candidates.length) {
          finish(reject, new Error('WebSocket stream error.'));
          return;
        }
        const wsURL = candidates[attemptIndex];
        attemptIndex += 1;
        ws = new WebSocket(wsURL);
        wsStreamSocket = ws;
        let didOpen = false;

        ws.onopen = () => {
          didOpen = true;
          try {
            ws.send(JSON.stringify({
              type: 'send',
              session_id: payload.session_id,
              content: payload.content,
              model: payload.model,
              work_dir: payload.work_dir,
              sandbox_mode: payload.sandbox_mode,
              command: payload.command
            }));
          } catch (err) {
            finish(reject, err instanceof Error ? err : new Error('Failed to send websocket payload.'));
          }
        };

        ws.onmessage = (event) => {
          let evt = {};
          try {
            evt = JSON.parse(String(event?.data || '{}'));
          } catch {
            return;
          }
          if (onEvent) onEvent(evt);
          const eventType = String(evt?.event || '').trim().toLowerCase();
          if (eventType === 'started') {
            startedAck = true;
            return;
          }
          if (eventType === 'done') {
            finish(resolve, evt);
            return;
          }
          if (eventType === 'error') {
            finish(reject, new Error(String(evt?.message || 'Streaming failed.')));
          }
        };

        ws.onerror = () => {
          try {
            ws.close();
          } catch {
          }
        };

        ws.onclose = (event) => {
          if (settled) return;
          if (!didOpen) {
            connectAttempt();
            return;
          }
          if (startedAck) {
            finish(reject, new Error('websocket_detached_background'));
            return;
          }
          finish(reject, new Error('WebSocket connection failed before run start.'));
        };
      };

      connectAttempt();
    });
  }

  onMount(() => {
    if (typeof window !== 'undefined') {
      try {
        const persisted = String(localStorage.getItem(viewModeStorageKey) || '').trim().toLowerCase();
        if (persisted === 'raw' || persisted === 'compact') {
          logViewMode = persisted;
        }
      } catch {
      }
    }
    ensureSessionOnFirstOpen().catch((error) => {
      viewStatus = String(error?.message || 'Failed to initialize coding sessions.');
    });
    refreshCodexIdentity().catch(() => {});
  });

  $effect(() => {
    const sid = String(activeSessionID || '').trim();
    if (!sid) return;
    saveDraftForSession(sid, draftMessage);
  });

  onDestroy(() => {
    if (pathSuggestTimer) {
      clearTimeout(pathSuggestTimer);
      pathSuggestTimer = null;
    }
    if (sessionPrefsTimer) {
      clearTimeout(sessionPrefsTimer);
      sessionPrefsTimer = null;
    }
    stopBackgroundMonitor();
    if (wsStreamSocket) {
      try {
        wsStreamSocket.close();
      } catch {
      }
      wsStreamSocket = null;
    }
  });
</script>

<section class="panel coding-panel">
  <header class="coding-topbar">
    <div class="coding-topbar-left">
      <button class="btn btn-secondary" type="button" onclick={backToDashboard}>Dashboard</button>
      <button class="btn btn-secondary" type="button" onclick={() => (showSessionDrawer = true)}>Sessions</button>
      <div class="coding-topbar-title">
        <strong>CodexSess Chat</strong>
        <span title={selectedWorkDir || '~/'}>
          {selectedWorkDir || '~/'}
        </span>
      </div>
    </div>
    <div class="coding-topbar-right">
      <select bind:value={selectedModel} onchange={queuePersistSessionPreferences} aria-label="Model for coding session">
        {#each models as model}
          <option value={model}>{model}</option>
        {/each}
      </select>
      <div class="coding-view-mode-toggle" role="group" aria-label="coding output mode">
        <button class="btn btn-secondary {logViewMode === 'compact' ? 'is-active' : ''}" type="button" onclick={() => setLogViewMode('compact')}>
          Compact
        </button>
        <button class="btn btn-secondary {logViewMode === 'raw' ? 'is-active' : ''}" type="button" onclick={() => setLogViewMode('raw')}>
          Raw CLI
        </button>
      </div>
      <button class="btn btn-secondary" onclick={openNewSessionModal} disabled={loadingSessions || sending}>
        New Session
      </button>
      <button class="btn btn-danger" onclick={deleteActiveSession} disabled={!activeSessionID || deleting || sending}>
        Delete
      </button>
    </div>
  </header>

  <div class="coding-layout">
    <section class="coding-chat-area full" aria-label="Coding chat">
      {#if !activeSessionID}
        <div class="empty-state">Session not selected.</div>
      {:else}
        <div class="coding-messages" bind:this={messagesViewport}>
          {#if loadingMessages && messages.length === 0}
            <p class="empty-note">Loading messages...</p>
          {:else}
            {@const renderedMessages = renderedMessagesForView()}
            {#if renderedMessages.length === 0}
              <p class="empty-note">Start by sending a coding instruction.</p>
            {:else}
              {#each renderedMessages as message (message.id)}
                <article class="coding-message {messageRoleClass(message)} {message.pending ? 'pending' : ''} {message.failed ? 'failed' : ''}">
                  <div class="coding-message-head">
                    <strong>
                      {#if message.role === 'assistant'}
                        {assistantDisplayName()}
                      {:else if message.role === 'activity'}
                        Activity
                      {:else if message.role === 'event'}
                        CLI Event
                      {:else if message.role === 'stderr'}
                        CLI stderr
                      {:else}
                        You
                      {/if}
                    </strong>
                    <span>
                      {#if message.failed}
                        Failed to send
                      {:else if !message.pending}
                        {formatWhen(message.created_at)}
                      {/if}
                    </span>
                  </div>
                  <pre>{isMessageExpanded(message.id) ? (messageDisplayContent(message) || '-') : messagePreviewContent(messageDisplayContent(message) || '-')}</pre>
                  {#if shouldCollapseContent(messageDisplayContent(message) || '')}
                    <button class="btn btn-secondary btn-small coding-show-more" type="button" onclick={() => toggleMessageExpanded(message.id)}>
                      {isMessageExpanded(message.id) ? 'Show less' : 'Show more'}
                    </button>
                  {/if}
                  {#if message.pending && message.role === 'assistant' && !String(message.content || '').trim()}
                    <p class="coding-message-status">Coding...</p>
                  {/if}
                </article>
              {/each}
            {/if}
          {/if}
          {#if sending && streamingPending}
            <div class="coding-streaming-note" role="status" aria-live="polite">
              <span class="streaming-pulse" aria-hidden="true"></span>
              <span class="streaming-label">Streaming</span>
              <span class="streaming-dots" aria-hidden="true"></span>
              <span class="streaming-bar" aria-hidden="true"><i></i></span>
            </div>
          {/if}
          {#if backgroundProcessing && !sending}
            <div class="coding-streaming-note" role="status" aria-live="polite">
              <span class="streaming-pulse" aria-hidden="true"></span>
              <span class="streaming-label">Streaming...</span>
            </div>
          {/if}
        </div>

        <div class="coding-composer">
          <textarea
            placeholder="Write coding task here... (Shift+Enter to send). Supports /status, /review, and $skill"
            bind:value={draftMessage}
            rows="4"
            onkeydown={onComposerKeydown}
            oninput={() => {
              if (composerError) composerError = '';
            }}
            disabled={sending || backgroundProcessing}
          ></textarea>
          {#if composerError}
            <p class="coding-composer-error">Failed to send: {composerError}</p>
          {/if}
          <div class="inline-actions coding-composer-actions">
            <button class="btn btn-secondary" onclick={openSkillModal} disabled={sending || backgroundProcessing}>Insert Skill</button>
            <button
              class="btn btn-secondary sandbox-mode-btn {selectedSandboxMode === 'full-access' ? 'mode-full' : 'mode-write'}"
              type="button"
              onclick={toggleSandboxMode}
              disabled={sending || backgroundProcessing}
            >
              {selectedSandboxMode === 'full-access' ? 'Full Access' : 'Write'}
            </button>
            <button
              class="btn {(sending || backgroundProcessing) ? 'btn-danger' : 'btn-primary'} btn-send"
              onclick={(sending || backgroundProcessing) ? cancelStreaming : sendMessage}
              disabled={(!(sending || backgroundProcessing) && !draftMessage.trim())}
            >
              <span>{(sending || backgroundProcessing) ? 'Stop' : 'Send'}</span>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12l18-9-6 9 6 9-18-9z"></path></svg>
            </button>
          </div>
        </div>
      {/if}
    </section>
  </div>
  <div class="coding-status-line" aria-live="polite">
    <div class="coding-status-main">
      <span>{viewStatus}</span>
      {#if persistingSessionPrefs}
        <span class="coding-status-saving">Saving session settings...</span>
      {/if}
    </div>
    {#if activeExecCommand}
      <div class="coding-status-command mono" title={activeExecCommand}>
        Running: {activeExecCommand}
      </div>
    {/if}
  </div>
</section>

{#if showNewSessionModal}
  <div class="modal-backdrop modal-backdrop-coding" role="presentation">
    <div class="modal-card modal-card-coding" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && closeNewSessionModal()}>
      <div class="modal-head">
        <div>
          <h3>New Session</h3>
          <p class="modal-subtitle">Choose workspace/folder before starting coding session.</p>
        </div>
        <button class="btn btn-secondary btn-small" onclick={closeNewSessionModal} disabled={sending}>Close</button>
      </div>
      <div class="modal-body">
        <label for="sessionWorkDir">Workspace Path</label>
        <input
          id="sessionWorkDir"
          list="sessionWorkDirSuggestions"
          bind:value={newSessionPath}
          placeholder="~/"
          oninput={(event) => schedulePathSuggestions(event.currentTarget.value)}
          onfocus={() => schedulePathSuggestions(newSessionPath)}
          disabled={sending}
        />
        <datalist id="sessionWorkDirSuggestions">
          {#each pathSuggestions as option}
            <option value={option}></option>
          {/each}
        </datalist>
        <p class="setting-title">
          {#if loadingPathSuggestions}
            Loading path suggestions...
          {:else}
            Default path is `~/`. Suggestions are loaded from current folder listing.
          {/if}
        </p>
        <div class="panel-actions">
          <button class="btn btn-secondary" onclick={() => loadPathSuggestions(newSessionPath)} disabled={sending || loadingPathSuggestions}>
            Refresh Suggestions
          </button>
          <button class="btn btn-primary" onclick={createSessionFromModal} disabled={sending || !newSessionPath.trim()}>
            Create Session
          </button>
        </div>
      </div>
    </div>
  </div>
{/if}

{#if showSessionDrawer}
  <div class="modal-backdrop modal-backdrop-coding" role="presentation">
    <div class="modal-card modal-card-coding drawer-card" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && (showSessionDrawer = false)}>
      <div class="modal-head">
        <div>
          <h3>Sessions</h3>
          <p class="modal-subtitle">Pick a session from the list.</p>
        </div>
        <button class="btn btn-secondary btn-small" onclick={() => (showSessionDrawer = false)}>Close</button>
      </div>
      <div class="coding-sessions-list drawer-list" aria-label="Session list">
        {#if loadingSessions}
          <p class="empty-note">Loading sessions...</p>
        {:else if sessions.length === 0}
          <p class="empty-note">No session yet.</p>
        {:else}
          {#each sessions as session (session.id)}
            <button
              class="coding-session-item {activeSessionID === session.id ? 'is-active' : ''}"
              onclick={() => selectSession(session.id)}
            >
              <strong>{session.title || 'New Session'}</strong>
              <span class="mono">{sessionDisplayID(session)}</span>
              <span>{formatWhen(session.last_message_at)}</span>
              <span class="mono">{session.work_dir || '~/'}</span>
            </button>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

{#if showSkillModal}
  <div class="modal-backdrop modal-backdrop-coding" role="presentation">
    <div class="modal-card modal-card-coding" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && closeSkillModal()}>
      <div class="modal-head">
        <div>
          <h3>Insert Skill</h3>
          <p class="modal-subtitle">Select available skill and insert `$skill_name` into composer.</p>
        </div>
        <button class="btn btn-secondary btn-small" onclick={closeSkillModal}>Close</button>
      </div>
      <div class="modal-body">
        <label for="skillSearchInput">Search Skill</label>
        <input
          id="skillSearchInput"
          value={skillSearchQuery}
          placeholder="Search skill name..."
          oninput={(event) => (skillSearchQuery = event.currentTarget.value)}
        />
        {#if loadingSkills}
          <p class="setting-title">Loading skills...</p>
        {:else if filteredSkills().length === 0}
          <p class="setting-title">No skills found.</p>
        {:else}
          <div class="skill-list">
            {#each filteredSkills() as skill}
              <button class="skill-item" onclick={() => insertSkillToken(skill)}>
                <span>{skill}</span>
                <code>${skill}</code>
              </button>
            {/each}
          </div>
        {/if}
      </div>
    </div>
  </div>
{/if}
