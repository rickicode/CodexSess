<script>
  import { onMount, onDestroy, tick } from 'svelte';

  let sessions = $state([]);
  let activeSessionID = $state('');
  let messages = $state([]);
  let draftMessage = $state('');
  let selectedModel = $state('gpt-5.2-codex');
  let selectedReasoningLevel = $state('medium');
  let selectedWorkDir = $state('~/');
  let selectedSandboxMode = $state('write');
  let loadingSessions = $state(false);
  let loadingMessages = $state(false);
  let loadingOlderMessages = $state(false);
  let hasMoreMessages = $state(false);
  let oldestLoadedMessageID = $state('');
  let newestLoadedMessageID = $state('');
  let sending = $state(false);
  let deleting = $state(false);
  let viewStatus = $state('Ready.');
  let composerError = $state('');
  let streamingPending = $state(false);
  let messagesViewport = $state(null);
  let autoFollowBottom = $state(true);
  let lastMessagesScrollTop = $state(0);
  let showScrollBottomButton = $state(false);
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
  let forceStopArmed = $state(false);
  let activeExecCommand = $state('');
  let wsStreamSocket = $state(null);
  let wsHealthSocket = $state(null);
  let wsHealthStatus = $state('disconnected');
  let wsHealthReconnectTimer = null;
  let wsHealthKeepAlive = false;
  let wsHealthReady = false;
  let showExecOutputModal = $state(false);
  let selectedExecEntry = $state(null);
  let showSubagentDetailModal = $state(false);
  let selectedSubagentEntry = $state(null);
  const initialMessagesPageSize = 50;
  const olderMessagesPageSize = 40;
  const nearBottomThresholdPx = 120;

  const apiBase = String(import.meta.env.VITE_API_BASE || '').trim().replace(/\/+$/, '');
  const draftStoragePrefix = 'codexsess.coding.draft.v1:';
  const viewModeStorageKey = 'codexsess.coding.view_mode.v1';
  const reasoningLevelStorageKey = 'codexsess.coding.reasoning_level.v1';
  const enableSkillHintWrap = ['1', 'true', 'yes', 'on'].includes(
    String(import.meta.env.VITE_CODEXSESS_SKILL_HINT_WRAP || '').trim().toLowerCase()
  );
  const jsonHeaders = { 'Content-Type': 'application/json' };
  const models = ['gpt-5.2-codex', 'gpt-5.3-codex', 'gpt-5.4-mini', 'gpt-5.4'];
  const reasoningLevels = [
    { value: 'low', label: 'Low' },
    { value: 'medium', label: 'Medium' },
    { value: 'high', label: 'High' }
  ];
  const slashCommands = ['/status', '/review [optional focus]'];

  function normalizeReasoningLevel(value) {
    const v = String(value || '').trim().toLowerCase();
    if (v === 'low' || v === 'high') return v;
    return 'medium';
  }

  function onReasoningLevelChange() {
    selectedReasoningLevel = normalizeReasoningLevel(selectedReasoningLevel);
    if (typeof window === 'undefined') return;
    try {
      localStorage.setItem(reasoningLevelStorageKey, selectedReasoningLevel);
    } catch {
    }
    queuePersistSessionPreferences();
  }

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
    try {
      return d.toLocaleString(undefined, {
        year: 'numeric',
        month: 'numeric',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        second: '2-digit',
        fractionalSecondDigits: 3
      });
    } catch {
      return d.toLocaleString();
    }
  }

  function timestampFromMessage(message) {
    const raw = String(message?.created_at || '');
    const ms = Date.parse(raw);
    if (!Number.isNaN(ms)) return ms;
    return 0;
  }

  function sequenceFromMessage(message) {
    const value = Number(message?.sequence ?? 0);
    if (Number.isFinite(value) && value > 0) return value;
    return 0;
  }

  function sortMessagesChronologically(input) {
    const src = Array.isArray(input) ? input : [];
    return src
      .map((item, idx) => ({ item, idx }))
      .sort((a, b) => {
        const sa = sequenceFromMessage(a.item);
        const sb = sequenceFromMessage(b.item);
        if (sa > 0 && sb > 0 && sa !== sb) return sa - sb;
        const ta = timestampFromMessage(a.item);
        const tb = timestampFromMessage(b.item);
        if (ta !== tb) return ta - tb;
        return a.idx - b.idx;
      })
      .map((entry) => entry.item);
  }

  function mergeMessagesChronologically(currentMessages, incomingMessages) {
    const existing = Array.isArray(currentMessages) ? currentMessages : [];
    const incoming = Array.isArray(incomingMessages) ? incomingMessages : [];
    if (incoming.length === 0) return existing;
    const merged = [...existing];
    const known = new Set(
      existing.map((item) => String(item?.id || '').trim()).filter(Boolean)
    );
    for (const item of incoming) {
      const id = String(item?.id || '').trim();
      if (id && known.has(id)) continue;
      if (id) known.add(id);
      merged.push(item);
    }
    return sortMessagesChronologically(merged);
  }

  function messageMatchKey(item) {
    const role = String(item?.role || '').trim().toLowerCase();
    const content = String(item?.content || '').trim();
    return `${role}|${content}`;
  }

  function reconcileLiveMessagesWithPersisted(currentMessages, persistedMessages, liveMessageIDs = []) {
    const current = Array.isArray(currentMessages) ? [...currentMessages] : [];
    const persisted = Array.isArray(persistedMessages) ? persistedMessages : [];
    if (persisted.length === 0) return current;
    const liveSet = new Set((Array.isArray(liveMessageIDs) ? liveMessageIDs : []).map((id) => String(id || '').trim()).filter(Boolean));
    if (liveSet.size === 0) {
      return mergeMessagesChronologically(current, persisted);
    }

    const liveIndexByKey = new Map();
    const matchWindowMs = 4000;
    for (let idx = 0; idx < current.length; idx += 1) {
      const row = current[idx];
      const id = String(row?.id || '').trim();
      if (!id || !liveSet.has(id)) continue;
      const key = messageMatchKey(row);
      if (!key) continue;
      const bucket = liveIndexByKey.get(key) || [];
      bucket.push({ idx, ts: timestampFromMessage(row) });
      liveIndexByKey.set(key, bucket);
    }

    const unmatchedPersisted = [];
    for (const row of persisted) {
      const key = messageMatchKey(row);
      const bucket = liveIndexByKey.get(key) || [];
      if (bucket.length > 0) {
        const persistedTS = timestampFromMessage(row);
        let pickAt = 0;
        if (persistedTS > 0) {
          // Prefer stable queue order; only pick later candidate when it is clearly within time window.
          for (let i = 0; i < bucket.length; i += 1) {
            const candidateTS = Number(bucket[i]?.ts || 0);
            if (candidateTS <= 0) continue;
            if (Math.abs(candidateTS - persistedTS) <= matchWindowMs) {
              pickAt = i;
              break;
            }
          }
          const firstTS = Number(bucket[0]?.ts || 0);
          if (firstTS > 0 && Math.abs(firstTS - persistedTS) > matchWindowMs && pickAt === 0) {
            // No safe live match in window; keep persisted row for chronological merge path.
            unmatchedPersisted.push(row);
            continue;
          }
        }
        const [picked] = bucket.splice(pickAt, 1);
        const idx = Number(picked?.idx ?? -1);
        if (idx >= 0 && idx < current.length) {
          current[idx] = row;
          continue;
        }
        unmatchedPersisted.push(row);
        continue;
      }
      unmatchedPersisted.push(row);
    }
    return mergeMessagesChronologically(current, unmatchedPersisted);
  }

  function normalizeActivityCommandKey(command) {
    return String(command || '').trim().replace(/\s+/g, ' ').toLowerCase();
  }

  function normalizeExecStatus(status) {
    const v = String(status || '').trim().toLowerCase();
    if (v === 'running' || v === 'done' || v === 'failed') return v;
    return 'running';
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

  function parseFileOperationText(rawText) {
    const text = String(rawText || '').trim();
    if (!text) return '';
    // Examples:
    // [Edited internal/service/coding.go (+34 -1)]
    // [Read web/src/views/CodingView.svelte]
    const m = text.match(/^\[(Edited|Read|Created|Deleted|Moved|Renamed)\s+(.+)\]$/i);
    if (!m) return '';
    const action = String(m[1] || '').trim();
    const target = String(m[2] || '').trim();
    if (!action || !target) return '';
    return `${action}: ${target}`;
  }

  function extractLikelyAgentIDs(raw) {
    const text = String(raw || '').trim();
    if (!text) return [];
    const uuidPattern = /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
    const out = [];
    for (const match of text.matchAll(uuidPattern)) {
      const v = String(match?.[0] || '').trim();
      if (v) out.push(v);
    }
    if (out.length > 0) return [...new Set(out)];
    const tokens = text.split(/[,\s]+/).map((item) => String(item || '').trim()).filter(Boolean);
    for (const token of tokens) {
      if (/^agent-[a-z0-9_-]{4,}$/i.test(token)) {
        out.push(token);
        continue;
      }
      if (token.length >= 20 && (token.match(/-/g) || []).length >= 2 && /^[a-z0-9_-]+$/i.test(token)) {
        out.push(token);
      }
    }
    return [...new Set(out)];
  }

  function extractSubagentIdentityFromText(raw) {
    const text = String(raw || '').trim();
    if (!text) return { nickname: '', agentType: '', ids: [] };
    const ids = extractLikelyAgentIDs(text);
    const spawnMatch = text.match(/spawned\s+(.+?)(?:\s+\[([^\]]+)\])?(?:$|\n)/i);
    let nickname = String(spawnMatch?.[1] || '').trim();
    let agentType = String(spawnMatch?.[2] || '').trim();
    if (!nickname) {
      nickname = String(text.match(/(?:^|\n)\s*(?:name|nickname)\s*:\s*([^\n]+)/i)?.[1] || '').trim();
    }
    if (!agentType) {
      agentType = String(text.match(/(?:^|\n)\s*(?:role|agent[_\s-]?type)\s*:\s*([^\n]+)/i)?.[1] || '').trim();
    }
    if (/^subagent$/i.test(nickname) || extractLikelyAgentIDs(nickname).length > 0) {
      nickname = '';
    }
    if (!nickname) {
      const statusName = text.match(/(?:^|\n)\s*([A-Za-z][A-Za-z0-9_-]{2,})\s+status\s*:/i);
      nickname = String(statusName?.[1] || '').trim();
    }
    return { nickname, agentType: normalizeSubagentRole(agentType), ids };
  }

  function normalizeSubagentRole(raw) {
    const text = String(raw || '').trim();
    if (!text) return '';
    if (extractLikelyAgentIDs(text).length > 0) return '';
    const low = text.toLowerCase();
    const blocked = new Set([
      'collab_tool_call',
      'tool_call',
      'command_execution',
      'exec_command',
      'spawn_agent',
      'wait_agent',
      'wait',
      'send_input',
      'resume_agent',
      'close_agent',
      'item.started',
      'item.updated',
      'item.completed',
      'tool.started',
      'tool.completed',
      'tool.call.started',
      'tool.call.completed'
    ]);
    if (blocked.has(low)) return '';
    return text;
  }

  function normalizeSubagentToolFamily(raw) {
    const tool = String(raw || '').trim().toLowerCase();
    if (tool === 'wait_agent' || tool === 'wait') return 'wait';
    if (tool === 'spawn_agent') return 'spawn';
    if (tool === 'send_input') return 'send_input';
    if (tool === 'resume_agent') return 'resume_agent';
    if (tool === 'close_agent') return 'close_agent';
    return tool;
  }

  function cleanSubagentDetailText(raw, knownIDs = []) {
    const text = String(raw || '').trim();
    if (!text) return '';
    const idSet = new Set((Array.isArray(knownIDs) ? knownIDs : []).map((item) => String(item || '').trim()).filter(Boolean));
    const lines = text
      .split('\n')
      .map((line) => String(line || '').trim())
      .filter(Boolean);
    const filtered = [];
    for (const line of lines) {
      if (idSet.has(line)) continue;
      if (extractLikelyAgentIDs(line).length === 1 && line.split(/\s+/).length === 1) continue;
      if (filtered.length > 0 && filtered[filtered.length - 1] === line) continue;
      filtered.push(line);
    }
    return filtered.join('\n').trim();
  }

  function formatSubagentFallbackName(ids = [], targetID = '') {
    const firstID = [...(Array.isArray(ids) ? ids : []), String(targetID || '').trim()].find((item) => String(item || '').trim());
    const id = String(firstID || '').trim();
    if (!id) return '';
    const fallbackNames = [
      'Huygens', 'Curie', 'Gauss', 'Noether', 'Lovelace', 'Turing', 'Faraday', 'Kepler',
      'Hypatia', 'Ramanujan', 'Euler', 'Bohr', 'Fermi', 'Mendel', 'Tesla', 'Darwin',
      'Sagan', 'Franklin', 'Pasteur', 'Planck', 'Dirac', 'Raman', 'Hopper', 'Shannon'
    ];
    const compact = id.includes('-') ? id.replace(/-/g, '') : id;
    let hash = 0;
    for (let i = 0; i < compact.length; i += 1) {
      hash = ((hash * 31) + compact.charCodeAt(i)) >>> 0;
    }
    const picked = fallbackNames[hash % fallbackNames.length];
    return String(picked || '').trim();
  }

  function isFallbackSubagentName(raw) {
    const value = String(raw || '').trim();
    if (!value) return false;
    if (/^agent-[a-z0-9_-]{4,}$/i.test(value)) return true;
    const fallbackNames = new Set([
      'huygens', 'curie', 'gauss', 'noether', 'lovelace', 'turing', 'faraday', 'kepler',
      'hypatia', 'ramanujan', 'euler', 'bohr', 'fermi', 'mendel', 'tesla', 'darwin',
      'sagan', 'franklin', 'pasteur', 'planck', 'dirac', 'raman', 'hopper', 'shannon'
    ]);
    return fallbackNames.has(value.toLowerCase());
  }

  function inferSubagentRoleFromText(...inputs) {
    const text = inputs
      .map((value) => String(value || '').trim().toLowerCase())
      .filter(Boolean)
      .join('\n');
    if (!text) return '';

    const explicitRole = text.match(/\b(frontend-developer|react-specialist|nextjs-developer|vue-expert|svelte-specialist|ui-fixer|backend-developer|api-designer|fullstack-developer|code-mapper|browser-debugger|javascript-pro|typescript-pro|golang-pro|sql-pro|reviewer|debugger|qa-expert|test-automator|security-auditor|performance-engineer|accessibility-tester|api-documenter|seo-specialist|build-engineer|dependency-manager|deployment-engineer|docker-expert|kubernetes-specialist|websocket-engineer)\b/i);
    if (explicitRole?.[1]) return String(explicitRole[1] || '').trim().toLowerCase();

    if (/\bsvelte|sveltekit\b/i.test(text)) return 'svelte-specialist';
    if (/\bnext\.?js|app router|pages router\b/i.test(text)) return 'nextjs-developer';
    if (/\breact|jsx|tsx\b/i.test(text)) return 'react-specialist';
    if (/\bvue|nuxt\b/i.test(text)) return 'vue-expert';
    if (/\btypescript|tsc|type\b/i.test(text)) return 'typescript-pro';
    if (/\bjavascript|node\b/i.test(text)) return 'javascript-pro';
    if (/\bsql|query|database|migration|postgres|mysql|sqlite\b/i.test(text)) return 'sql-pro';
    if (/\bgolang|\bgo\b|goroutine|go service\b/i.test(text)) return 'golang-pro';
    if (/\bsecurity|vuln|owasp|xss|csrf|auth\b/i.test(text)) return 'security-auditor';
    if (/\btest|qa|e2e|playwright|vitest|regression\b/i.test(text)) return 'qa-expert';
    if (/\breview|bug|risk|scan|audit|risky assumptions\b/i.test(text)) return 'reviewer';
    if (/\bapi|backend|service|httpapi\b/i.test(text)) return 'backend-developer';
    if (/\bui|layout|css|frontend\b/i.test(text)) return 'frontend-developer';
    return '';
  }

  function choosePreferredSubagentName(incoming = '', existing = '') {
    const next = String(incoming || '').trim();
    const prev = String(existing || '').trim();
    if (next && !isFallbackSubagentName(next)) return next;
    if (prev && !isFallbackSubagentName(prev)) return prev;
    return next || prev;
  }

  function choosePreferredSubagentRole(incoming = '', existing = '') {
    const next = String(incoming || '').trim();
    const prev = String(existing || '').trim();
    if (next && next.toLowerCase() !== 'subagent') return next;
    if (prev && prev.toLowerCase() !== 'subagent') return prev;
    return next || prev;
  }

  function buildSubagentMergeKey(toolName, details = {}) {
    const name = String(toolName || '').trim().toLowerCase();
    const ids = Array.isArray(details?.ids)
      ? [...new Set(details.ids.map((item) => String(item || '').trim()).filter(Boolean))].sort()
      : [];
    const targetID = String(details?.targetID || '').trim();
    const nickname = String(details?.nickname || '').trim();
    const prompt = String(details?.prompt || '').trim();
    const summary = String(details?.summary || '').trim();
    const callID = String(details?.callID || '').trim();
    const idSeed = ids[0] || targetID;
    if (name === 'spawn_agent') {
      return normalizeActivityCommandKey(`spawn|${idSeed || nickname || prompt || summary || callID}`);
    }
    if (name === 'wait_agent' || name === 'wait') {
      return normalizeActivityCommandKey(`wait|${ids.join(',') || targetID || nickname || prompt || summary || callID}`);
    }
    if (name === 'send_input') {
      return normalizeActivityCommandKey(`send_input|${targetID || ids[0] || prompt || callID}`);
    }
    if (name === 'resume_agent') {
      return normalizeActivityCommandKey(`resume_agent|${targetID || ids[0] || callID}`);
    }
    if (name === 'close_agent') {
      return normalizeActivityCommandKey(`close_agent|${targetID || ids[0] || callID}`);
    }
    return normalizeActivityCommandKey(`${name}|${callID || idSeed || prompt || summary}`);
  }

  function buildSubagentLifecycleKey(details = {}) {
    const ids = Array.isArray(details?.ids)
      ? [...new Set(details.ids.map((item) => String(item || '').trim()).filter(Boolean))].sort()
      : [];
    const targetID = String(details?.targetID || '').trim();
    const nickname = String(details?.nickname || '').trim();
    const callID = String(details?.callID || '').trim();
    const prompt = String(details?.prompt || '').trim();
    const summary = String(details?.summary || '').trim();
    const seed = callID || ids[0] || targetID || nickname || prompt || summary;
    return normalizeActivityCommandKey(`subagent|${seed}`);
  }

  function normalizeSubagentPromptKey(raw) {
    const text = String(raw || '')
      .trim()
      .toLowerCase()
      .replace(/\.{3,}/g, ' ')
      .replace(/[^\p{L}\p{N}\s_-]+/gu, ' ')
      .replace(/\s+/g, ' ');
    return normalizeActivityCommandKey(text);
  }

  function parseSubagentActivityText(rawText) {
    const text = String(rawText || '').trim();
    if (!text || !text.startsWith('•')) return null;
    const lines = text.split('\n').map((line) => String(line || '').trim()).filter(Boolean);
    if (lines.length === 0) return null;
    const head = lines[0];
    const detailLineRaw = lines.slice(1).join('\n').replace(/^└\s*/i, '').trim();
    const idMatches = [...new Set([...extractLikelyAgentIDs(detailLineRaw), ...extractLikelyAgentIDs(head)])];
    const detailLine = cleanSubagentDetailText(detailLineRaw, idMatches);

    if (/^•\s*spawned\s+/i.test(head)) {
      const m = head.match(/^•\s*spawned\s+(.+?)(?:\s+\[(.+)\])?$/i);
      const nickname = String(m?.[1] || '').trim().replace(/subagent$/i, '').trim();
      const role = normalizeSubagentRole(String(m?.[2] || '').trim());
      const targetID = idMatches[0] || '';
      return {
        key: buildSubagentMergeKey('spawn_agent', {
          ids: idMatches,
          targetID,
          nickname,
          prompt: detailLine,
          summary: detailLine
        }),
        toolName: 'spawn_agent',
        status: 'done',
        phase: 'completed',
        title: nickname ? `Spawned ${nickname}` : 'Spawned subagent',
        nickname,
        agentType: role,
        targetID,
        ids: idMatches,
        prompt: detailLine,
        summary: detailLine,
        raw: { text }
      };
    }
    if (/^•\s*waiting\s+for\s+\d+\s+agents?/i.test(head)) {
      const countMatch = head.match(/(\d+)/);
      const count = Number(countMatch?.[1] || 0) || idMatches.length;
      const title = idMatches.length === 1 ? `Waiting ${idMatches[0]}` : `Waiting ${count || idMatches.length} agents`;
      const waitKey = buildSubagentMergeKey('wait_agent', {
        ids: idMatches,
        targetID: idMatches[0] || '',
        summary: detailLine
      });
      return {
        key: waitKey,
        toolName: 'wait_agent',
        status: 'running',
        phase: 'started',
        title,
        nickname: '',
        agentType: '',
        targetID: idMatches[0] || '',
        ids: idMatches,
        prompt: '',
        summary: detailLine,
        raw: { text }
      };
    }
    if (/^•\s*waiting\s+/i.test(head)) {
      const waitTarget = String(head.replace(/^•\s*waiting\s+/i, '') || '').trim();
      const waitIDs = [...new Set([...idMatches, ...extractLikelyAgentIDs(waitTarget)])];
      const waitKey = buildSubagentMergeKey('wait_agent', {
        ids: waitIDs,
        targetID: waitIDs[0] || waitTarget,
        summary: detailLine || waitTarget
      });
      const title = waitTarget ? `Waiting ${waitTarget}` : 'Waiting for agents';
      return {
        key: waitKey,
        toolName: 'wait_agent',
        status: 'running',
        phase: 'started',
        title,
        nickname: '',
        agentType: '',
        targetID: waitIDs[0] || waitTarget,
        ids: waitIDs,
        prompt: '',
        summary: detailLine || waitTarget,
        raw: { text }
      };
    }
    if (/^•\s*subagent\s+wait\s+completed/i.test(head)) {
      const waitKey = buildSubagentMergeKey('wait_agent', {
        ids: idMatches,
        targetID: idMatches[0] || '',
        summary: detailLine
      });
      return {
        key: waitKey,
        toolName: 'wait_agent',
        status: 'done',
        phase: 'completed',
        title: 'Subagent wait completed',
        nickname: '',
        agentType: '',
        targetID: idMatches[0] || '',
        ids: idMatches,
        prompt: '',
        summary: detailLine,
        raw: { text }
      };
    }
    if (/^•\s*sent\s+input\s+to\s+subagent/i.test(head)) {
      return {
        key: buildSubagentMergeKey('send_input', {
          ids: idMatches,
          targetID: detailLine,
          summary: detailLine
        }),
        toolName: 'send_input',
        status: 'done',
        phase: 'completed',
        title: 'Sent input to subagent',
        nickname: '',
        agentType: '',
        targetID: detailLine,
        ids: idMatches,
        prompt: '',
        summary: detailLine,
        raw: { text }
      };
    }
    if (/^•\s*resumed\s+subagent/i.test(head)) {
      return {
        key: buildSubagentMergeKey('resume_agent', {
          ids: idMatches,
          targetID: detailLine
        }),
        toolName: 'resume_agent',
        status: 'done',
        phase: 'completed',
        title: 'Resumed subagent',
        nickname: '',
        agentType: '',
        targetID: detailLine,
        ids: idMatches,
        prompt: '',
        summary: detailLine,
        raw: { text }
      };
    }
    if (/^•\s*closed\s+subagent/i.test(head)) {
      return {
        key: buildSubagentMergeKey('close_agent', {
          ids: idMatches,
          targetID: detailLine
        }),
        toolName: 'close_agent',
        status: 'done',
        phase: 'completed',
        title: 'Closed subagent',
        nickname: '',
        agentType: '',
        targetID: detailLine,
        ids: idMatches,
        prompt: '',
        summary: detailLine,
        raw: { text }
      };
    }
    return null;
  }

  function parseRawEventPayload(rawText) {
    const text = String(rawText || '').trim();
    if (!text || !(text.startsWith('{') || text.startsWith('['))) return null;
    try {
      return JSON.parse(text);
    } catch {
      return null;
    }
  }

  function isRedundantAssistantRawEvent(payload) {
    if (!payload || typeof payload !== 'object') return false;
    const eventType = String(payload?.type || '').trim().toLowerCase();
    if (eventType !== 'item.completed' && eventType !== 'item.updated') return false;
    const item = payload?.item;
    const itemType = String(item?.type || '').trim().toLowerCase();
    if (itemType !== 'agent_message') return false;
    const text = String(item?.text || '').trim();
    return text.length > 0;
  }

  function extractExecOutputText(value) {
    if (value == null) return '';
    if (typeof value === 'string') return String(value);
    if (Array.isArray(value)) {
      return value.map((item) => extractExecOutputText(item)).filter(Boolean).join('\n');
    }
    if (typeof value === 'object') {
      const direct = ['aggregated_output', 'stdout', 'stderr', 'output_text', 'text', 'message', 'result', 'output'];
      for (const key of direct) {
        const v = extractExecOutputText(value?.[key]);
        if (v) return v;
      }
      try {
        return JSON.stringify(value, null, 2);
      } catch {
        return '';
      }
    }
    return String(value);
  }

  function parseToolArguments(raw) {
    if (raw == null) return {};
    if (typeof raw === 'object' && !Array.isArray(raw)) return raw;
    const text = String(raw || '').trim();
    if (!text || (!text.startsWith('{') && !text.startsWith('['))) return {};
    try {
      const parsed = JSON.parse(text);
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) return parsed;
    } catch {
    }
    return {};
  }

  function extractStringList(value) {
    if (!Array.isArray(value)) return [];
    return value.map((item) => String(item || '').trim()).filter(Boolean);
  }

  function extractSummaryFromAny(value) {
    if (value == null) return '';
    if (typeof value === 'string') {
      const text = String(value || '').trim();
      if (!text) return '';
      try {
        return extractSummaryFromAny(JSON.parse(text));
      } catch {
        return text;
      }
    }
    if (Array.isArray(value)) {
      for (const item of value) {
        const text = extractSummaryFromAny(item);
        if (text) return text;
      }
      return '';
    }
    if (typeof value === 'object') {
      for (const key of ['final_message', 'message', 'summary', 'text', 'status']) {
        const text = extractSummaryFromAny(value?.[key]);
        if (text) return text;
      }
      for (const key of ['result', 'output', 'data', 'response', 'agents_states']) {
        const text = extractSummaryFromAny(value?.[key]);
        if (text) return text;
      }
    }
    return '';
  }

  function findFirstTextByKeys(value, keys) {
    const wanted = new Set((Array.isArray(keys) ? keys : []).map((k) => String(k || '').trim().toLowerCase()).filter(Boolean));
    if (wanted.size === 0) return '';
    const seen = new Set();
    const walk = (node) => {
      if (node == null) return '';
      if (typeof node === 'string') {
        const text = String(node || '').trim();
        if (!text) return '';
        if ((text.startsWith('{') || text.startsWith('[')) && !seen.has(text)) {
          seen.add(text);
          try {
            return walk(JSON.parse(text));
          } catch {
          }
        }
        return '';
      }
      if (Array.isArray(node)) {
        for (const item of node) {
          const found = walk(item);
          if (found) return found;
        }
        return '';
      }
      if (typeof node === 'object') {
        for (const [k, v] of Object.entries(node)) {
          if (wanted.has(String(k || '').trim().toLowerCase())) {
            const text = String(v || '').trim();
            if (text) return text;
          }
        }
        for (const v of Object.values(node)) {
          const found = walk(v);
          if (found) return found;
        }
      }
      return '';
    };
    return walk(value);
  }

  function collectAgentIDsDeep(value) {
    const out = new Set();
    const seen = new Set();
    const walk = (node) => {
      if (node == null) return;
      if (typeof node === 'string') {
        const text = String(node || '').trim();
        if (!text) return;
        for (const id of extractLikelyAgentIDs(text)) out.add(id);
        if ((text.startsWith('{') || text.startsWith('[')) && !seen.has(text)) {
          seen.add(text);
          try {
            walk(JSON.parse(text));
          } catch {
          }
        }
        return;
      }
      if (Array.isArray(node)) {
        for (const item of node) walk(item);
        return;
      }
      if (typeof node === 'object') {
        for (const v of Object.values(node)) walk(v);
      }
    };
    walk(value);
    return [...out];
  }

  function isGenericSubagentTitle(text) {
    const value = String(text || '').trim().toLowerCase();
    if (!value) return true;
    if (value === 'spawned subagent') return true;
    if (value === 'subagent activity') return true;
    if (value === 'waiting for agents') return true;
    if (value === 'subagent wait completed') return true;
    if (/^waiting\s+[0-9a-f-]{20,}$/i.test(value)) return true;
    return false;
  }

  function parseExecEventFromPayload(payload) {
    const evt = payload && typeof payload === 'object' ? payload : null;
    if (!evt) return null;
    const type = String(evt?.type || '').trim().toLowerCase();
    const item = evt?.item && typeof evt.item === 'object' ? evt.item : {};
    const itemType = String(item?.type || '').trim().toLowerCase();
    if (itemType && !['command_execution', 'tool_call', 'exec_command'].includes(itemType)) {
      return null;
    }
    if (itemType === 'collab_tool_call') return null;
    if (!type.startsWith('item.') && !type.startsWith('tool.')) {
      return null;
    }

    const fn = item?.function && typeof item.function === 'object' ? item.function : {};
    const toolName = String(item?.tool || item?.tool_name || item?.name || fn?.name || '').trim().toLowerCase();
    const toolArgs = parseToolArguments(
      item?.arguments ||
      item?.input ||
      item?.params ||
      item?.payload ||
      fn?.arguments ||
      fn?.input ||
      evt?.arguments ||
      evt?.input ||
      evt?.params ||
      evt?.payload
    );
    const command = String(
      item?.command ||
      item?.cmd ||
      evt?.command ||
      evt?.cmd ||
      toolArgs?.command ||
      toolArgs?.cmd ||
      toolArgs?.shell_command ||
      ''
    ).trim();
    const isExplicitExec =
      itemType === 'command_execution' ||
      itemType === 'exec_command' ||
      toolName === 'exec_command' ||
      toolName === 'exec';
    if (!isExplicitExec) return null;
    if (!command) return null;
    let status = '';
    let exitCode = 0;
    if (type === 'item.started' || type === 'item.updated' || type === 'tool.started' || type === 'tool.call.started') {
      status = 'running';
    } else if (type === 'item.completed' || type === 'tool.completed' || type === 'tool.call.completed') {
      exitCode = Number(item?.exit_code ?? evt?.exit_code ?? 0) || 0;
      status = exitCode !== 0 ? 'failed' : 'done';
    }
    const output = extractExecOutputText(
      item?.aggregated_output ||
      item?.output ||
      item?.result ||
      evt?.aggregated_output ||
      evt?.output ||
      item?.text ||
      item?.output_text ||
      ''
    );
    return {
      command,
      status: normalizeExecStatus(status || 'running'),
      exitCode,
      output: String(output || '').trim()
    };
  }

  function parseSubagentEventFromPayload(payload) {
    const evt = payload && typeof payload === 'object' ? payload : null;
    if (!evt) return null;
    const type = String(evt?.type || '').trim().toLowerCase();
    const item = evt?.item && typeof evt.item === 'object' ? evt.item : {};
    const itemType = String(item?.type || '').trim().toLowerCase();
    if (!itemType || (itemType !== 'tool_call' && itemType !== 'collab_tool_call')) {
      return null;
    }
    if (!type.startsWith('item.') && !type.startsWith('tool.')) return null;
    const fn = item?.function && typeof item.function === 'object' ? item.function : {};
    const toolName = String(item?.tool || item?.tool_name || item?.name || fn?.name || '').trim().toLowerCase();
    if (!['spawn_agent', 'wait_agent', 'wait', 'send_input', 'resume_agent', 'close_agent'].includes(toolName)) {
      return null;
    }
    if (toolName === 'wait' && itemType !== 'collab_tool_call') {
      return null;
    }
    const args = parseToolArguments(item?.arguments || item?.input || item?.params || item?.payload || fn?.arguments || fn?.input);
    const argAgent = args?.agent && typeof args.agent === 'object' && !Array.isArray(args.agent) ? args.agent : {};
    const itemAgent = item?.agent && typeof item.agent === 'object' && !Array.isArray(item.agent) ? item.agent : {};
    const firstText = (...values) => {
      for (const value of values) {
        if (value == null) continue;
        const text = String(value || '').trim();
        if (text) return text;
      }
      return '';
    };
    const callID = String(item?.call_id || item?.tool_call_id || item?.id || '').trim();
    const ids = [
      ...extractStringList(args?.ids),
      ...extractStringList(args?.agent_ids),
      ...extractStringList(args?.subagent_ids),
      ...extractStringList(args?.receiver_thread_ids),
      ...extractStringList(item?.receiver_thread_ids)
    ];
    const promptRaw = String(args?.message || args?.prompt || item?.prompt || '').trim();
    const summary = String(
      extractSummaryFromAny(item?.output_text || item?.text || item?.output || item?.result || item?.response || item?.agents_states)
    ).trim();
    const hints = extractSubagentIdentityFromText([summary, promptRaw, String(item?.output_text || ''), String(item?.text || '')].join('\n'));
    const deepNickname = findFirstTextByKeys(
      [item?.output, item?.result, item?.response, item?.agents_states, item?.output_text, item?.text],
      ['nickname', 'name', 'agent_name', 'display_name']
    );
    let nickname = firstText(
      args?.nickname,
      args?.name,
      args?.agent_name,
      args?.agentName,
      args?.display_name,
      args?.displayName,
      argAgent?.nickname,
      argAgent?.name,
      argAgent?.display_name,
      argAgent?.displayName,
      item?.nickname,
      item?.name,
      item?.agent_name,
      item?.agentName,
      itemAgent?.nickname,
      itemAgent?.name,
      itemAgent?.display_name,
      itemAgent?.displayName,
      hints?.nickname,
      deepNickname
    );
    const deepRole = findFirstTextByKeys(
      [item?.output, item?.result, item?.response, item?.agents_states, item?.output_text, item?.text],
      ['agent_type', 'agenttype', 'role', 'type']
    );
    let agentType = normalizeSubagentRole(firstText(
      args?.agent_type,
      args?.agentType,
      args?.role,
      args?.type,
      typeof args?.agent === 'string' ? args.agent : '',
      argAgent?.agent_type,
      argAgent?.agentType,
      argAgent?.role,
      argAgent?.type,
      item?.agent_type,
      item?.agentType,
      item?.role,
      item?.type,
      typeof item?.agent === 'string' ? item.agent : '',
      itemAgent?.agent_type,
      itemAgent?.agentType,
      itemAgent?.role,
      itemAgent?.type,
      hints?.agentType,
      deepRole
    ));
    if (!agentType && toolName === 'spawn_agent') {
      agentType = normalizeSubagentRole(inferSubagentRoleFromText(promptRaw, item?.prompt, args?.message, args?.prompt));
    }
    const idsWithHints = [...new Set([
      ...ids,
      ...extractLikelyAgentIDs(firstText(args?.id, args?.agent_id, args?.subagent_id, args?.thread_id, args?.receiver_thread_id)),
      ...extractLikelyAgentIDs(firstText(item?.receiver_thread_id, item?.target_id)),
      ...(Array.isArray(hints?.ids) ? hints.ids : []),
      ...collectAgentIDsDeep([item?.output, item?.result, item?.response, item?.agents_states])
    ])];
    const targetID = firstText(
      args?.id,
      args?.agent_id,
      args?.subagent_id,
      args?.thread_id,
      args?.receiver_thread_id,
      item?.receiver_thread_id,
      item?.target_id,
      idsWithHints[0]
    );
    if (!nickname) {
      nickname = formatSubagentFallbackName(idsWithHints, targetID);
    }
    if (!agentType && nickname) {
      agentType = 'subagent';
    }
    const prompt = cleanSubagentDetailText(promptRaw, [targetID, ...idsWithHints]);
    const summaryClean = cleanSubagentDetailText(summary, [targetID, ...idsWithHints]);
    const isCompleted = type === 'item.completed' || type === 'tool.completed' || type === 'tool.call.completed';
    const isStarted = type === 'item.started' || type === 'item.updated' || type === 'tool.started' || type === 'tool.call.started';
    const hasError = Boolean(item?.error || evt?.error);
    const status = hasError ? 'failed' : (isCompleted ? 'done' : (isStarted ? 'running' : 'running'));
    const phase = isCompleted ? 'completed' : (isStarted ? 'started' : 'updated');
    const key = buildSubagentMergeKey(toolName, {
      callID,
      ids: idsWithHints,
      targetID,
      nickname,
      prompt,
      summary: summaryClean
    });
    let title = 'Subagent Activity';
    if (toolName === 'spawn_agent') {
      title = nickname ? `Spawned ${nickname}${agentType ? ` [${agentType}]` : ''}` : 'Spawned subagent';
    } else if (toolName === 'wait_agent' || toolName === 'wait') {
      title = nickname
        ? `Waiting ${nickname}${agentType ? ` [${agentType}]` : ''}`
        : (idsWithHints.length === 1 ? `Waiting ${idsWithHints[0]}` : (idsWithHints.length > 1 ? `Waiting ${idsWithHints.length} agents` : 'Waiting for agents'));
    } else if (toolName === 'send_input') {
      title = 'Sent input to subagent';
    } else if (toolName === 'resume_agent') {
      title = 'Resumed subagent';
    } else if (toolName === 'close_agent') {
      title = 'Closed subagent';
    }
    return {
      key,
      callID,
      toolName,
      status,
      phase,
      title,
      nickname,
      agentType,
      targetID,
      ids: idsWithHints,
      prompt,
      summary: summaryClean,
      raw: evt
    };
  }

  function mergeExecOutput(startText, endText) {
    const normalize = (value) => {
      const text = String(value || '').trim();
      return text === 'No output captured.' ? '' : text;
    };
    const first = normalize(startText);
    const second = normalize(endText);
    if (!first) return second;
    if (!second) return first;
    if (first.includes(second)) return first;
    if (second.includes(first)) return second;
    return `${first}\n${second}`;
  }

  function buildExecAwareMessages(inputMessages, includeRawEvents) {
    const src = Array.isArray(inputMessages) ? inputMessages : [];
    const out = [];
    const activeExecByKey = new Map();
    const activeSubagentByKey = new Map();
    const subagentIdentityByID = new Map();
    let lastExecKey = '';
    const normalizeExecOutputSource = (value) => {
      const v = String(value || '').trim().toLowerCase();
      if (v === 'live' || v === 'persisted' || v === 'persisted-merge') return v;
      return 'live';
    };
    const mergeExecOutputSource = (existingSource, incomingSource) => {
      const cur = normalizeExecOutputSource(existingSource);
      const next = normalizeExecOutputSource(incomingSource);
      if (cur === next) return cur;
      return 'persisted-merge';
    };
    const detectMetaSource = (id) => {
      const raw = String(id || '').trim().toLowerCase();
      if (raw.startsWith('event-') || raw.startsWith('stderr-') || raw.startsWith('activity-')) {
        return 'live';
      }
      return 'persisted';
    };

    const upsertExecEntry = (entry) => {
      const command = String(entry?.command || '').trim();
      if (!command) return null;
      const key = normalizeActivityCommandKey(command);
      if (!key) return null;
      const nowISO = new Date().toISOString();
      const nextStatus = normalizeExecStatus(entry?.status || 'running');
      const nextExitCode = Number(entry?.exitCode || 0) || 0;
      const nextOutput = String(entry?.output || '').trim();
      const nextCreatedAt = String(entry?.createdAt || nowISO);
      const nextCreatedAtMs = Date.parse(nextCreatedAt);
      for (let i = out.length - 1; i >= 0 && i >= out.length - 8; i -= 1) {
        const prev = out[i];
        if (String(prev?.role || '').trim().toLowerCase() !== 'exec') continue;
        if (String(prev?.exec_command || '').trim() !== command) continue;
        if (normalizeExecStatus(prev?.exec_status) !== nextStatus) continue;
        if ((Number(prev?.exec_exit_code || 0) || 0) !== nextExitCode) continue;
        const prevOutput = String(prev?.exec_output || '').trim();
        if (prevOutput !== nextOutput) continue;
        const prevMs = Date.parse(String(prev?.updated_at || prev?.created_at || ''));
        if (!Number.isNaN(nextCreatedAtMs) && !Number.isNaN(prevMs) && Math.abs(nextCreatedAtMs - prevMs) > 1800) continue;
        return prev;
      }
      const next = {
        id: `exec-${String(entry?.sourceID || key)}`,
        role: 'exec',
        content: command,
        created_at: nextCreatedAt,
        updated_at: nextCreatedAt,
        pending: false,
        exec_command: command,
        exec_status: nextStatus,
        exec_exit_code: nextExitCode,
        exec_output: nextOutput,
        exec_output_source: normalizeExecOutputSource(entry?.source || 'live')
      };
      out.push(next);
      if (next.exec_status === 'running') {
        activeExecByKey.set(key, out.length - 1);
        lastExecKey = command;
      } else {
        activeExecByKey.delete(key);
        if (normalizeActivityCommandKey(lastExecKey) === key) {
          lastExecKey = '';
        }
      }
      return next;
    };

    const upsertSubagentEntry = (entry) => {
      const key = normalizeActivityCommandKey(entry?.key || '');
      const incomingToolName = String(entry?.toolName || '').trim().toLowerCase();
      const incomingToolFamily = normalizeSubagentToolFamily(incomingToolName);
      const incomingStatus = String(entry?.status || 'running').trim().toLowerCase();
      const isTransientWaitEvent = (incomingToolName === 'wait_agent' || incomingToolName === 'wait') && incomingStatus !== 'running';
      const lifecycleKey = buildSubagentLifecycleKey({
        ids: Array.isArray(entry?.ids) ? entry.ids : [],
        targetID: entry?.targetID,
        nickname: entry?.nickname,
        callID: entry?.callID,
        prompt: entry?.prompt,
        summary: entry?.summary
      });
      const nowISO = new Date().toISOString();
      const idsFromEntry = Array.isArray(entry?.ids) ? entry.ids.filter(Boolean) : [];
      const targetFromEntry = String(entry?.targetID || '').trim();
      const knownNames = [];
      for (const id of idsFromEntry) {
        const known = String(subagentIdentityByID.get(id) || '').trim();
        if (known) knownNames.push(known);
      }
      if (targetFromEntry) {
        const known = String(subagentIdentityByID.get(targetFromEntry) || '').trim();
        if (known) knownNames.push(known);
      }
      const resolvedNickname = String(entry?.nickname || knownNames[0] || '').trim();
      const fallbackNickname = resolvedNickname || formatSubagentFallbackName(idsFromEntry, targetFromEntry);
      const resolvedRoleRaw = String(entry?.agentType || '').trim();
      const inferredRole = String(entry?.toolName || '').trim().toLowerCase() === 'spawn_agent'
        ? inferSubagentRoleFromText(entry?.prompt, entry?.title)
        : '';
      const resolvedRole = resolvedRoleRaw || (fallbackNickname ? 'subagent' : '');
      const effectiveRole = normalizeSubagentRole(resolvedRoleRaw || inferredRole || resolvedRole);
      const resolvedTitle = (() => {
        const base = String(entry?.title || 'Subagent Activity').trim();
        const roleTag = effectiveRole ? ` [${effectiveRole}]` : '';
        if (fallbackNickname && /^spawned\b/i.test(base)) {
          return `Spawned ${fallbackNickname}${roleTag}`;
        }
        if (fallbackNickname && /^waiting\b/i.test(base)) {
          const hasName = base.toLowerCase().includes(fallbackNickname.toLowerCase());
          const hasRole = resolvedRole ? base.toLowerCase().includes(resolvedRole.toLowerCase()) : false;
          if (hasName || hasRole) return base;
          return `${base} (${fallbackNickname}${effectiveRole ? ` · ${effectiveRole}` : ''})`;
        }
        if (effectiveRole && !base.includes('[') && !base.includes('(')) return `${base}${roleTag}`;
        return base;
      })();
      const indexedActive = key ? activeSubagentByKey.get(key) : undefined;
      const indexedByKey = (() => {
        if (!key && !lifecycleKey) return undefined;
        for (let idx = out.length - 1; idx >= 0; idx -= 1) {
          const row = out[idx];
          if (!row || String(row?.role || '').trim().toLowerCase() !== 'subagent') continue;
          const rowKey = normalizeActivityCommandKey(row?.subagent_key || '');
          const rowLifecycle = normalizeActivityCommandKey(row?.subagent_lifecycle_key || '');
          if (lifecycleKey && rowLifecycle && rowLifecycle === lifecycleKey) return idx;
          if (key && rowKey && rowKey === key) return idx;
        }
        return undefined;
      })();
      const indexedByPrompt = (() => {
        const toolName = String(entry?.toolName || '').trim().toLowerCase();
        const status = String(entry?.status || '').trim().toLowerCase();
        if (toolName !== 'spawn_agent' || status === 'running') return undefined;
        const incomingPrompt = normalizeSubagentPromptKey(entry?.prompt || entry?.summary || '');
        if (!incomingPrompt) return undefined;
        for (let idx = out.length - 1; idx >= 0; idx -= 1) {
          const row = out[idx];
          if (!row || String(row?.role || '').trim().toLowerCase() !== 'subagent') continue;
          if (String(row?.subagent_status || '').trim().toLowerCase() !== 'running') continue;
          if (String(row?.subagent_tool || '').trim().toLowerCase() !== 'spawn_agent') continue;
          const rowPrompt = normalizeSubagentPromptKey(row?.subagent_prompt || row?.subagent_summary || '');
          if (!rowPrompt) continue;
          if (rowPrompt === incomingPrompt || rowPrompt.includes(incomingPrompt) || incomingPrompt.includes(rowPrompt)) {
            return idx;
          }
        }
        return undefined;
      })();
      const existingIdx = typeof indexedActive === 'number'
        ? indexedActive
        : (typeof indexedByKey === 'number' ? indexedByKey : indexedByPrompt);
      const resolvedExistingIdx = (() => {
        if (typeof existingIdx !== 'number' || !out[existingIdx]) return undefined;
        const currentToolFamily = normalizeSubagentToolFamily(String(out[existingIdx]?.subagent_tool || '').trim().toLowerCase());
        if (!incomingToolFamily || !currentToolFamily || incomingToolFamily === currentToolFamily) {
          return existingIdx;
        }
        return undefined;
      })();
      if (typeof resolvedExistingIdx === 'number' && out[resolvedExistingIdx]) {
        const current = out[resolvedExistingIdx];
        const nextStatus = String(entry?.status || current?.subagent_status || 'running').trim().toLowerCase();
        const mergedIDs = [...new Set([...(Array.isArray(current?.subagent_ids) ? current.subagent_ids : []), ...idsFromEntry])];
        const resolvedTarget = targetFromEntry || String(current?.subagent_target_id || '').trim();
        const fallbackCurrentTitle = String(current?.subagent_title || 'Subagent Activity').trim();
        const chosenTitle = (() => {
          const incoming = String(resolvedTitle || '').trim();
          if (!incoming) return fallbackCurrentTitle;
          if (!isGenericSubagentTitle(incoming)) return incoming;
          if (!isGenericSubagentTitle(fallbackCurrentTitle)) return fallbackCurrentTitle;
          if (fallbackNickname && /^spawned\b/i.test(incoming)) {
            return `Spawned ${fallbackNickname}${resolvedRole ? ` [${resolvedRole}]` : ''}`;
          }
          if (fallbackNickname && /^waiting\b/i.test(incoming)) {
            return `Waiting ${fallbackNickname}${effectiveRole ? ` [${effectiveRole}]` : ''}`;
          }
          return incoming;
        })();
        const preferredNickname = choosePreferredSubagentName(
          resolvedNickname || fallbackNickname,
          String(current?.subagent_nickname || '').trim()
        );
        const preferredRole = choosePreferredSubagentRole(
          effectiveRole,
          String(current?.subagent_role || '').trim()
        );
        out[resolvedExistingIdx] = {
          ...current,
          updated_at: String(entry?.createdAt || nowISO),
          subagent_status: nextStatus,
          subagent_phase: String(entry?.phase || current?.subagent_phase || '').trim().toLowerCase(),
          subagent_key: key || String(current?.subagent_key || '').trim(),
          subagent_lifecycle_key: lifecycleKey || String(current?.subagent_lifecycle_key || '').trim(),
          subagent_title: chosenTitle,
          subagent_tool: String(entry?.toolName || current?.subagent_tool || '').trim(),
          subagent_ids: mergedIDs,
          subagent_target_id: resolvedTarget,
          subagent_nickname: preferredNickname,
          subagent_role: preferredRole,
          subagent_prompt: String(entry?.prompt || current?.subagent_prompt || '').trim(),
          subagent_summary: String(entry?.summary || current?.subagent_summary || '').trim(),
          subagent_raw: entry?.raw || current?.subagent_raw || {}
        };
        if (preferredNickname) {
          for (const id of mergedIDs) {
            subagentIdentityByID.set(id, preferredNickname);
          }
          if (resolvedTarget) {
            subagentIdentityByID.set(resolvedTarget, preferredNickname);
          }
        }
        if (nextStatus !== 'running') {
          activeSubagentByKey.delete(key);
        }
        if (isTransientWaitEvent) {
          return out[resolvedExistingIdx];
        }
        return out[resolvedExistingIdx];
      }
      if (isTransientWaitEvent) {
        if (key) activeSubagentByKey.delete(key);
        return null;
      }
      const finalTitle = (() => {
        const current = String(resolvedTitle || '').trim();
        if (!isGenericSubagentTitle(current)) return current || 'Subagent Activity';
        if (fallbackNickname && /^spawned\b/i.test(current || String(entry?.title || '').trim())) {
          return `Spawned ${fallbackNickname}${effectiveRole ? ` [${effectiveRole}]` : ''}`;
        }
        if (fallbackNickname && /^waiting\b/i.test(current || String(entry?.title || '').trim())) {
          return `Waiting ${fallbackNickname}${effectiveRole ? ` [${effectiveRole}]` : ''}`;
        }
        return current || 'Subagent Activity';
      })();
      const next = {
        id: `subagent-${String(entry?.sourceID || key)}`,
        role: 'subagent',
        content: finalTitle,
        created_at: String(entry?.createdAt || nowISO),
        updated_at: String(entry?.createdAt || nowISO),
        pending: false,
        subagent_status: String(entry?.status || 'running').trim().toLowerCase(),
        subagent_phase: String(entry?.phase || '').trim().toLowerCase(),
        subagent_key: key,
        subagent_lifecycle_key: lifecycleKey,
        subagent_title: finalTitle,
        subagent_tool: String(entry?.toolName || '').trim(),
        subagent_ids: idsFromEntry,
        subagent_target_id: targetFromEntry,
        subagent_nickname: choosePreferredSubagentName(resolvedNickname || fallbackNickname, ''),
        subagent_role: choosePreferredSubagentRole(effectiveRole, ''),
        subagent_prompt: String(entry?.prompt || '').trim(),
        subagent_summary: String(entry?.summary || '').trim(),
        subagent_raw: entry?.raw || {}
      };
      out.push(next);
      if (next.subagent_nickname) {
        for (const id of idsFromEntry) {
          subagentIdentityByID.set(id, next.subagent_nickname);
        }
        if (targetFromEntry) {
          subagentIdentityByID.set(targetFromEntry, next.subagent_nickname);
        }
      }
      if (key && next.subagent_status === 'running') {
        activeSubagentByKey.set(key, out.length - 1);
      }
      return next;
    };

    for (const item of src) {
      const role = String(item?.role || '').trim().toLowerCase();
      if (role === 'activity') {
        const subagentActivity = parseSubagentActivityText(item?.content || '');
        if (subagentActivity?.toolName) {
          upsertSubagentEntry({
            ...subagentActivity,
            sourceID: item?.id,
            createdAt: item?.created_at
          });
          continue;
        }
        const parsed = parseActivityText(item?.content || '');
        if (parsed.kind === 'running' && parsed.command) {
          upsertExecEntry({
            command: parsed.command,
            status: 'running',
            exitCode: 0,
            sourceID: item?.id,
            createdAt: item?.created_at,
            source: detectMetaSource(item?.id)
          });
          continue;
        }
        if ((parsed.kind === 'done' || parsed.kind === 'failed') && parsed.command) {
          upsertExecEntry({
            command: parsed.command,
            status: parsed.kind === 'failed' ? 'failed' : 'done',
            exitCode: Number(parsed.exitCode || 0) || 0,
            sourceID: item?.id,
            createdAt: item?.created_at,
            source: detectMetaSource(item?.id)
          });
          continue;
        }
        out.push(item);
        continue;
      }
      if (role === 'event') {
        const payload = parseRawEventPayload(item?.content || '');
        if (isRedundantAssistantRawEvent(payload)) {
          // Assistant text is rendered as assistant bubble; hide duplicate raw event row.
          continue;
        }
        const parsedExec = parseExecEventFromPayload(payload);
        const parsedSubagent = parseSubagentEventFromPayload(payload);
        const rawText = String(item?.content || '').trim();
        const fileOpText = parseFileOperationText(rawText);
        let handled = false;
        if (parsedExec?.command) {
          const recordExec = upsertExecEntry({
            ...parsedExec,
            sourceID: item?.id,
            createdAt: item?.created_at,
            source: detectMetaSource(item?.id)
          });
          void recordExec;
          handled = true;
        }
        if (parsedSubagent?.toolName) {
          const recordSubagent = upsertSubagentEntry({
            ...parsedSubagent,
            sourceID: item?.id,
            createdAt: item?.created_at
          });
          void recordSubagent;
          handled = true;
        }
        if (!handled && rawText) {
          const activeKey = normalizeActivityCommandKey(lastExecKey || activeExecCommand);
          if (activeKey && activeExecByKey.has(activeKey)) {
            const idx = activeExecByKey.get(activeKey);
            if (typeof idx === 'number' && out[idx]) {
              out[idx] = {
                ...out[idx],
                exec_output: mergeExecOutput(out[idx]?.exec_output || '', rawText),
                updated_at: String(item?.created_at || out[idx]?.updated_at || new Date().toISOString()),
                exec_output_source: mergeExecOutputSource(out[idx]?.exec_output_source, detectMetaSource(item?.id))
              };
              handled = true;
            }
          }
        }
        if (fileOpText) {
          out.push({
            id: `fileop-${String(item?.id || Date.now())}`,
            role: 'activity',
            content: fileOpText,
            created_at: String(item?.created_at || new Date().toISOString()),
            updated_at: String(item?.created_at || new Date().toISOString()),
            pending: false
          });
          if (!includeRawEvents) {
            continue;
          }
        }
        if (handled && !includeRawEvents) {
          continue;
        }
        out.push(item);
        continue;
      }
      if (role === 'stderr') {
        const text = String(item?.content || '').trim();
        if (text) {
          const activeKey = normalizeActivityCommandKey(lastExecKey || activeExecCommand);
          if (activeKey && activeExecByKey.has(activeKey)) {
            const idx = activeExecByKey.get(activeKey);
            if (typeof idx === 'number' && out[idx]) {
              out[idx] = {
                ...out[idx],
                exec_output: mergeExecOutput(out[idx]?.exec_output || '', text),
                updated_at: String(item?.created_at || out[idx]?.updated_at || new Date().toISOString()),
                exec_output_source: mergeExecOutputSource(out[idx]?.exec_output_source, detectMetaSource(item?.id))
              };
              if (!includeRawEvents) continue;
            }
          }
        }
        out.push(item);
        continue;
      }
      out.push(item);
    }
    return out
      .filter((row) => {
        if (!row) return false;
        if (String(row?.role || '').trim().toLowerCase() !== 'subagent') return true;
        const tool = String(row?.subagent_tool || '').trim().toLowerCase();
        const status = String(row?.subagent_status || '').trim().toLowerCase();
        const isRunning = status === 'running';
        const title = String(row?.subagent_title || row?.content || '').trim().toLowerCase();
        if ((tool === 'wait_agent' || tool === 'wait') && !isRunning) {
          return false;
        }
        if (title.startsWith('waiting') && !isRunning) {
          return false;
        }
        return true;
      })
      .map((row) => {
      if (String(row?.role || '').trim().toLowerCase() !== 'subagent') return row;
      const tool = String(row?.subagent_tool || '').trim().toLowerCase();
      const ids = Array.isArray(row?.subagent_ids) ? row.subagent_ids.filter(Boolean) : [];
      const target = String(row?.subagent_target_id || '').trim();
      const knownName = [...ids, target]
        .map((id) => String(subagentIdentityByID.get(id) || '').trim())
        .find((value) => value && !isFallbackSubagentName(value)) || '';
      let nextTitle = String(row?.subagent_title || row?.content || 'Subagent Activity').trim();
      if (tool && tool !== 'spawn_agent') {
        nextTitle = nextTitle.replace(/\s+\[[^\]]+\]\s*$/u, '').trim();
      }
      if (!knownName) {
        return {
          ...row,
          subagent_title: nextTitle,
          content: nextTitle
        };
      }

      const currentName = String(row?.subagent_nickname || '').trim();
      const currentRole = String(row?.subagent_role || '').trim();
      const nextName = choosePreferredSubagentName(knownName, currentName);
      const nextRole = tool === 'spawn_agent' ? choosePreferredSubagentRole('', currentRole) : '';
      if (/^spawned\b/i.test(nextTitle) || /^waiting\b/i.test(nextTitle)) {
        const verb = /^spawned\b/i.test(nextTitle) ? 'Spawned' : 'Waiting';
        const displayName = nextName || subagentDisplayName({ ...row, subagent_nickname: nextName });
        const displayRole = tool === 'spawn_agent'
          ? (nextRole || subagentDisplayRole({ ...row, subagent_nickname: nextName, subagent_role: nextRole }))
          : '';
        if (displayName) {
          nextTitle = `${verb} ${displayName}${displayRole ? ` [${displayRole}]` : ''}`;
        }
      }
      return {
        ...row,
        subagent_nickname: nextName,
        subagent_role: nextRole,
        subagent_title: nextTitle,
        content: nextTitle
      };
    });
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
    messages = [...messages, next];
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
    if (role === 'exec') return 'exec';
    if (role === 'subagent') return 'subagent';
    if (role === 'activity') return 'activity';
    if (role === 'event') return 'event';
    if (role === 'stderr') return 'stderr';
    return 'user';
  }

  function renderedMessagesForView() {
    const rawMode = String(logViewMode || '').trim().toLowerCase() === 'raw';
    let rendered = buildExecAwareMessages(messages, rawMode);
    rendered = dedupeAdjacentSpawnSubagentMessages(rendered);
    rendered = rendered.filter((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      if (role !== 'activity') return true;
      const parsed = parseActivityText(item?.content || '');
      if (!parsed?.command) return true;
      return parsed.kind === 'other';
    });
    if (!rawMode) {
      rendered = rendered.filter((item) => String(item?.role || '').trim().toLowerCase() !== 'event');
    }
    // Keep strict bubble-by-bubble chronology in both modes.
    // Grouping can hide boundaries when upstream event ordering is dense.
    return rendered;
  }

  function execStatusLabel(status, exitCode) {
    const normalized = normalizeExecStatus(status);
    if (normalized === 'running') return 'Running';
    if (normalized === 'failed') return `Failed${Number(exitCode || 0) ? ` (exit ${Number(exitCode || 0)})` : ''}`;
    return 'Done';
  }

  function subagentStatusLabel(status) {
    const normalized = String(status || '').trim().toLowerCase();
    if (normalized === 'running') return 'Running';
    if (normalized === 'failed') return 'Failed';
    return 'Done';
  }

  function subagentPreview(message) {
    const parts = [];
    const nickname = subagentDisplayName(message);
    const role = subagentDisplayRole(message);
    const title = String(subagentDisplayTitle(message) || '').trim().toLowerCase();
    const isWaiting = title.startsWith('waiting');
    if (isWaiting) return '';
    const target = String(message?.subagent_target_id || '').trim();
    const ids = Array.isArray(message?.subagent_ids) ? message.subagent_ids.filter(Boolean) : [];
    const prompt = String(message?.subagent_prompt || '').trim();
    const summary = String(message?.subagent_summary || '').trim();
    if (nickname) parts.push(`Name: ${nickname}`);
    if (role) parts.push(`Role: ${role}`);
    if (!isWaiting) {
      if (target) parts.push(`Target: ${target}`);
      if (ids.length > 0) parts.push(`IDs: ${ids.slice(0, 3).join(', ')}${ids.length > 3 ? ', ...' : ''}`);
    }
    if (prompt) parts.push(`Prompt: ${prompt.slice(0, 180)}${prompt.length > 180 ? '...' : ''}`);
    if (summary) parts.push(`Summary: ${summary.slice(0, 220)}${summary.length > 220 ? '...' : ''}`);
    if (parts.length === 0) return 'No subagent details captured.';
    return parts.join('\n');
  }

  function subagentDisplayName(message) {
    const rawNickname = String(message?.subagent_nickname || '').trim();
    const nickname = (
      !rawNickname ||
      rawNickname === '-' ||
      /^subagent$/i.test(rawNickname) ||
      extractLikelyAgentIDs(rawNickname).length > 0
    ) ? '' : rawNickname;
    const ids = [
      ...(Array.isArray(message?.subagent_ids) ? message.subagent_ids : []),
      ...extractLikelyAgentIDs(String(message?.subagent_target_id || '')),
      ...extractLikelyAgentIDs(String(message?.subagent_prompt || '')),
      ...extractLikelyAgentIDs(String(message?.subagent_summary || '')),
      ...extractLikelyAgentIDs(JSON.stringify(message?.subagent_raw || {}))
    ];
    return String(
      nickname ||
      formatSubagentFallbackName(ids, message?.subagent_target_id || '')
    ).trim();
  }

  function subagentDisplayRole(message) {
    const tool = String(message?.subagent_tool || '').trim().toLowerCase();
    if (tool && tool !== 'spawn_agent') {
      return '';
    }
    const role = String(message?.subagent_role || '').trim();
    if (role && role !== '-') return role;
    return subagentDisplayName(message) ? 'subagent' : '';
  }

  function subagentDisplayTitle(message) {
    const base = String(message?.subagent_title || message?.content || 'Subagent Activity').trim();
    const name = subagentDisplayName(message);
    const role = subagentDisplayRole(message);
    if (/^waiting\b/i.test(base)) {
      const safeName = name || 'subagent';
      return `Waiting ${safeName}${role ? ` [${role}]` : ''}`;
    }
    if (!isGenericSubagentTitle(base)) return base;
    if (name && /^spawned\b/i.test(base)) return `Spawned ${name}${role ? ` [${role}]` : ''}`;
    if (name && /^waiting\b/i.test(base)) return `Waiting ${name}${role ? ` [${role}]` : ''}`;
    return base;
  }

  function shouldHideRenderedMessage(message) {
    if (String(message?.role || '').trim().toLowerCase() !== 'subagent') return false;
    const title = String(subagentDisplayTitle(message) || '').trim().toLowerCase();
    const status = String(message?.subagent_status || '').trim().toLowerCase();
    return title.startsWith('waiting') && status !== 'running';
  }

  function dedupeAdjacentSpawnSubagentMessages(input) {
    const src = Array.isArray(input) ? input : [];
    const out = [];
    for (const row of src) {
      const current = row || {};
      const role = String(current?.role || '').trim().toLowerCase();
      if (role !== 'subagent') {
        out.push(current);
        continue;
      }
      const prev = out.length > 0 ? out[out.length - 1] : null;
      const currentTool = String(current?.subagent_tool || '').trim().toLowerCase();
      const prevTool = String(prev?.subagent_tool || '').trim().toLowerCase();
      const currentTitle = String(current?.subagent_title || current?.content || '').trim().toLowerCase();
      const prevTitle = String(prev?.subagent_title || prev?.content || '').trim().toLowerCase();
      const currentIsSpawn = currentTool === 'spawn_agent' || /^spawned\b/i.test(currentTitle);
      const prevIsSpawn = prevTool === 'spawn_agent' || /^spawned\b/i.test(prevTitle);
      const currentStatus = String(current?.subagent_status || '').trim().toLowerCase();
      const prevStatus = String(prev?.subagent_status || '').trim().toLowerCase();
      const currentPrompt = normalizeSubagentPromptKey(current?.subagent_prompt || current?.subagent_summary || '');
      const prevPrompt = normalizeSubagentPromptKey(prev?.subagent_prompt || prev?.subagent_summary || '');
      const promptMatches = Boolean(
        currentPrompt &&
        prevPrompt &&
        (currentPrompt === prevPrompt || currentPrompt.includes(prevPrompt) || prevPrompt.includes(currentPrompt))
      );
      const shouldMerge = Boolean(
        prev &&
        String(prev?.role || '').trim().toLowerCase() === 'subagent' &&
        prevIsSpawn &&
        currentIsSpawn &&
        prevStatus === 'running' &&
        (currentStatus === 'done' || currentStatus === 'failed') &&
        (promptMatches || !currentPrompt || !prevPrompt)
      );
      if (!shouldMerge) {
        out.push(current);
        continue;
      }
      out[out.length - 1] = {
        ...prev,
        updated_at: String(current?.updated_at || prev?.updated_at || ''),
        subagent_status: currentStatus,
        subagent_phase: String(current?.subagent_phase || prev?.subagent_phase || '').trim().toLowerCase(),
        subagent_title: subagentDisplayTitle(current) || subagentDisplayTitle(prev),
        subagent_tool: String(current?.subagent_tool || prev?.subagent_tool || 'spawn_agent').trim(),
        subagent_ids: [...new Set([...(Array.isArray(prev?.subagent_ids) ? prev.subagent_ids : []), ...(Array.isArray(current?.subagent_ids) ? current.subagent_ids : [])])],
        subagent_target_id: String(current?.subagent_target_id || prev?.subagent_target_id || '').trim(),
        subagent_nickname: choosePreferredSubagentName(
          String(current?.subagent_nickname || '').trim() || subagentDisplayName(current),
          String(prev?.subagent_nickname || '').trim() || subagentDisplayName(prev)
        ),
        subagent_role: choosePreferredSubagentRole(
          String(current?.subagent_role || '').trim() || subagentDisplayRole(current),
          String(prev?.subagent_role || '').trim() || subagentDisplayRole(prev)
        ),
        subagent_prompt: String(current?.subagent_prompt || prev?.subagent_prompt || '').trim(),
        subagent_summary: String(current?.subagent_summary || prev?.subagent_summary || '').trim(),
        subagent_raw: current?.subagent_raw || prev?.subagent_raw || {}
      };
    }
    return out;
  }

  function openExecOutputModal(message) {
    if (!message) return;
    selectedExecEntry = {
      command: String(message?.exec_command || message?.content || '-').trim() || '-',
      status: normalizeExecStatus(message?.exec_status || 'running'),
      exitCode: Number(message?.exec_exit_code || 0) || 0,
      output: sanitizeSensitiveLogText(String(message?.exec_output || '').trim() || 'No output captured.'),
      source: String(message?.exec_output_source || 'live').trim().toLowerCase(),
      when: String(message?.updated_at || message?.created_at || '')
    };
    showExecOutputModal = true;
  }

  function closeExecOutputModal() {
    showExecOutputModal = false;
    selectedExecEntry = null;
  }

  function openSubagentDetailModal(message) {
    if (!message) return;
    const raw = message?.subagent_raw || {};
    let rawText = '{}';
    try {
      rawText = JSON.stringify(raw, null, 2);
    } catch {
      rawText = '{}';
    }
    selectedSubagentEntry = {
      title: String(message?.subagent_title || message?.content || 'Subagent Activity').trim(),
      status: String(message?.subagent_status || 'done').trim().toLowerCase(),
      tool: String(message?.subagent_tool || '').trim(),
      nickname: String(message?.subagent_nickname || '').trim(),
      role: String(message?.subagent_role || '').trim(),
      targetID: String(message?.subagent_target_id || '').trim(),
      ids: Array.isArray(message?.subagent_ids) ? message.subagent_ids.filter(Boolean) : [],
      prompt: String(message?.subagent_prompt || '').trim(),
      summary: String(message?.subagent_summary || '').trim(),
      raw: sanitizeSensitiveLogText(rawText),
      when: String(message?.updated_at || message?.created_at || '')
    };
    showSubagentDetailModal = true;
  }

  function closeSubagentDetailModal() {
    showSubagentDetailModal = false;
    selectedSubagentEntry = null;
  }

  function refreshActiveExecCommandFromMessages() {
    const enriched = buildExecAwareMessages(messages, false);
    const running = enriched
      .filter((item) => String(item?.role || '').trim().toLowerCase() === 'exec' && normalizeExecStatus(item?.exec_status) === 'running');
    const last = running.length > 0 ? running[running.length - 1] : null;
    activeExecCommand = String(last?.exec_command || '').trim();
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

  function toggleLogViewMode() {
    setLogViewMode(logViewMode === 'raw' ? 'compact' : 'raw');
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
        selectedReasoningLevel = normalizeReasoningLevel(active.reasoning_level || selectedReasoningLevel);
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
          reasoning_level: normalizeReasoningLevel(selectedReasoningLevel),
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

  function isMessagesViewportNearBottom() {
    if (!messagesViewport) return true;
    const remaining = messagesViewport.scrollHeight - (messagesViewport.scrollTop + messagesViewport.clientHeight);
    return remaining <= nearBottomThresholdPx;
  }

  function refreshScrollAffordance() {
    showScrollBottomButton = Boolean(messagesViewport && !isMessagesViewportNearBottom());
  }

  async function loadMessages(sessionID, { silent = false, preserveViewport = false } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) {
      messages = [];
      return;
    }
    const showLoading = !silent;
    if (showLoading) {
      loadingMessages = true;
    }
    let previousDistanceFromBottom = 0;
    if (preserveViewport && messagesViewport) {
      previousDistanceFromBottom = Math.max(
        0,
        messagesViewport.scrollHeight - (messagesViewport.scrollTop + messagesViewport.clientHeight)
      );
    }
    try {
      const data = await req(`/api/coding/messages?session_id=${encodeURIComponent(sid)}&limit=${initialMessagesPageSize}`);
      if (String(activeSessionID || '').trim() !== sid) {
        return;
      }
      const incomingMessages = Array.isArray(data?.messages) ? data.messages : [];
      const preserveLoadedHistory = preserveViewport && !autoFollowBottom && messages.length > 0;
      if (preserveLoadedHistory) {
        const existingIDs = new Set(messages.map((item) => String(item?.id || '').trim()).filter(Boolean));
        const missingFromTail = incomingMessages.filter((item) => !existingIDs.has(String(item?.id || '').trim()));
        if (missingFromTail.length > 0) {
          messages = mergeMessagesChronologically(messages, missingFromTail);
        }
        hasMoreMessages = hasMoreMessages || Boolean(data?.has_more);
        oldestLoadedMessageID = String(messages?.[0]?.id || oldestLoadedMessageID || '').trim();
        newestLoadedMessageID = String(data?.newest_id || messages?.[messages.length - 1]?.id || newestLoadedMessageID || '').trim();
      } else {
        messages = incomingMessages;
        hasMoreMessages = Boolean(data?.has_more);
        oldestLoadedMessageID = String(data?.oldest_id || messages?.[0]?.id || '').trim();
        newestLoadedMessageID = String(data?.newest_id || messages?.[messages.length - 1]?.id || '').trim();
      }
      refreshActiveExecCommandFromMessages();
      await tick();
      if (preserveViewport && messagesViewport && !autoFollowBottom) {
        messagesViewport.scrollTop = Math.max(
          0,
          messagesViewport.scrollHeight - messagesViewport.clientHeight - previousDistanceFromBottom
        );
      } else {
        autoFollowBottom = true;
        scrollMessagesToBottom(true);
      }
      if (messagesViewport) {
        lastMessagesScrollTop = messagesViewport.scrollTop;
      }
      refreshScrollAffordance();
    } finally {
      if (showLoading) {
        loadingMessages = false;
      }
    }
  }

  async function loadOlderMessages() {
    const sid = String(activeSessionID || '').trim();
    const beforeID = String(oldestLoadedMessageID || '').trim();
    if (!sid || !beforeID || loadingOlderMessages || loadingMessages || sending || !hasMoreMessages) return;
    if (!messagesViewport) return;
    loadingOlderMessages = true;
    const prevScrollHeight = messagesViewport.scrollHeight;
    const prevScrollTop = messagesViewport.scrollTop;
    try {
      const data = await req(`/api/coding/messages?session_id=${encodeURIComponent(sid)}&limit=${olderMessagesPageSize}&before_id=${encodeURIComponent(beforeID)}`);
      if (String(activeSessionID || '').trim() !== sid) {
        return;
      }
      const older = Array.isArray(data?.messages) ? data.messages : [];
      if (older.length > 0) {
        const existingIDs = new Set(messages.map((item) => String(item?.id || '').trim()).filter(Boolean));
        const dedupedOlder = older.filter((item) => !existingIDs.has(String(item?.id || '').trim()));
        if (dedupedOlder.length > 0) {
          messages = [...dedupedOlder, ...messages];
        }
      }
      hasMoreMessages = Boolean(data?.has_more);
      oldestLoadedMessageID = String(data?.oldest_id || messages?.[0]?.id || '').trim();
      newestLoadedMessageID = String(data?.newest_id || messages?.[messages.length - 1]?.id || '').trim();
      await tick();
      if (messagesViewport) {
        const nextScrollHeight = messagesViewport.scrollHeight;
        messagesViewport.scrollTop = Math.max(0, nextScrollHeight - prevScrollHeight + prevScrollTop);
        lastMessagesScrollTop = messagesViewport.scrollTop;
      }
      refreshScrollAffordance();
    } finally {
      loadingOlderMessages = false;
    }
  }

  function onMessagesScroll() {
    if (!messagesViewport) return;
    const currentTop = messagesViewport.scrollTop;
    const nearBottom = isMessagesViewportNearBottom();
    const scrolledUp = currentTop < (lastMessagesScrollTop - 2);
    if (scrolledUp) {
      autoFollowBottom = false;
    } else if (nearBottom) {
      autoFollowBottom = true;
    }
    lastMessagesScrollTop = currentTop;
    refreshScrollAffordance();
    if (messagesViewport.scrollTop <= 80 && hasMoreMessages && !loadingOlderMessages && !loadingMessages) {
      loadOlderMessages().catch(() => {});
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
      return true;
    }
    if (becameDone) {
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

  function scrollMessagesToBottom(force = false) {
    if (!messagesViewport) return;
    if (!force && !autoFollowBottom) return;
    messagesViewport.scrollTop = messagesViewport.scrollHeight;
    lastMessagesScrollTop = messagesViewport.scrollTop;
    if (typeof window !== 'undefined') {
      window.requestAnimationFrame(() => {
        if (!messagesViewport) return;
        if (!force && !autoFollowBottom) return;
        messagesViewport.scrollTop = messagesViewport.scrollHeight;
        lastMessagesScrollTop = messagesViewport.scrollTop;
        refreshScrollAffordance();
      });
      setTimeout(() => {
        if (!messagesViewport) return;
        if (!force && !autoFollowBottom) return;
        messagesViewport.scrollTop = messagesViewport.scrollHeight;
        lastMessagesScrollTop = messagesViewport.scrollTop;
        refreshScrollAffordance();
      }, 60);
    }
    refreshScrollAffordance();
  }

  function jumpToLatestMessages() {
    autoFollowBottom = true;
    scrollMessagesToBottom(true);
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
        reasoning_level: normalizeReasoningLevel(selectedReasoningLevel),
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
      selectedReasoningLevel = normalizeReasoningLevel(created.reasoning_level || selectedReasoningLevel);
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
      selectedReasoningLevel = normalizeReasoningLevel(active.reasoning_level || selectedReasoningLevel);
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
    closeExecOutputModal();
    closeSubagentDetailModal();
    activeSessionID = sid;
    syncSessionIDToURL(sid);
    const selected = sessions.find((item) => item.id === sid);
    if (selected?.model) selectedModel = selected.model;
    selectedReasoningLevel = normalizeReasoningLevel(selected?.reasoning_level || selectedReasoningLevel);
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

  async function deleteSessionByID(sessionID, { fromDrawer = false } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid || deleting) return;
    const target = sessions.find((item) => item?.id === sid) || null;
    if (typeof window !== 'undefined') {
      const ok = window.confirm(`Delete session "${target?.title || sid}"?`);
      if (!ok) return;
    }
    const deletingActive = sid === String(activeSessionID || '').trim();
    deleting = true;
    try {
      if (deletingActive) {
        closeExecOutputModal();
        closeSubagentDetailModal();
      }
      await req(`/api/coding/sessions?id=${encodeURIComponent(sid)}`, { method: 'DELETE' });
      clearDraftForSession(sid);
      viewStatus = 'Session deleted.';
      await loadSessions({ autoSelect: deletingActive });
      if (sessions.length === 0) {
        await createSession({ autoOpen: true, workDir: '~/' });
        if (fromDrawer) {
          showSessionDrawer = false;
        }
      } else {
        if (!sessions.find((item) => item?.id === activeSessionID)) {
          activeSessionID = String(sessions?.[0]?.id || '').trim();
        }
        syncSessionIDToURL(activeSessionID);
        if (deletingActive) {
          await loadMessages(activeSessionID);
          draftMessage = loadDraftForSession(activeSessionID);
          startBackgroundMonitor(activeSessionID);
        }
      }
    } finally {
      deleting = false;
    }
  }

  async function deleteActiveSession() {
    const session = activeSession();
    if (!session?.id) return;
    await deleteSessionByID(session.id);
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
    autoFollowBottom = true;
    sending = true;
    stopRequested = false;
    forceStopArmed = false;
    backgroundProcessing = false;
    await tick();
    scrollMessagesToBottom(true);

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
        reasoning_level: normalizeReasoningLevel(selectedReasoningLevel),
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
          const parsedExec = parseExecEventFromPayload(parseRawEventPayload(text));
          if (parsedExec?.command) {
            if (parsedExec.status === 'running') {
              activeExecCommand = parsedExec.command;
            } else if (normalizeActivityCommandKey(parsedExec.command) === normalizeActivityCommandKey(activeExecCommand)) {
              activeExecCommand = '';
            }
          }
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
      const hasLiveMeta = streamedMetaIDs.length > 0;
      const hasLiveAssistant = streamedAssistantIDs.length > 0;
      streamingPending = false;
      if (userMessage) {
        messages = messages.map((item) => (item?.id === pendingID ? userMessage : item));
      } else {
        messages = messages.filter((item) => item?.id !== pendingID);
      }
      if (!hasLiveMeta && eventMessages.length > 0) {
        const normalizedIncoming = eventMessages.map((item) => ({
          ...item,
          content: sanitizeSensitiveLogText(item?.content || '')
        }));
        messages = mergeMessagesChronologically(messages, normalizedIncoming);
      }
      const persistedAssistant = assistantMessages.length > 0
        ? assistantMessages
        : (assistantMessage ? [assistantMessage] : []);
      if (!hasLiveAssistant && persistedAssistant.length > 0) {
        const assistantDedupSet = new Set(
          messages
            .filter((item) => String(item?.role || '').trim().toLowerCase() === 'assistant')
            .map((item) => String(item?.content || '').trim())
            .filter(Boolean)
        );
        const dedupedPersistedAssistant = persistedAssistant.filter((item) => {
          const key = String(item?.content || '').trim();
          if (!key) return true;
          if (assistantDedupSet.has(key)) return false;
          assistantDedupSet.add(key);
          return true;
        });
        if (hasLiveMeta) {
          // Preserve live terminal chronology; avoid global re-sort on assistant finalize.
          messages = [...messages, ...dedupedPersistedAssistant];
        } else {
          messages = mergeMessagesChronologically(messages, dedupedPersistedAssistant);
        }
      }
      if (!(hasLiveMeta || hasLiveAssistant)) {
        messages = sortMessagesChronologically(messages);
      }
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
      forceStopArmed = false;
    }
  }

  async function cancelStreaming() {
    if (!sending && !backgroundProcessing) return;
    const force = forceStopArmed;
    stopRequested = true;
    if (force) {
      viewStatus = 'Force stopping...';
    } else {
      forceStopArmed = true;
      viewStatus = 'Stopping... press Force Stop to kill immediately.';
    }
    const session = activeSession();
    if (session?.id) {
      try {
        await req('/api/coding/stop', {
          method: 'POST',
          body: JSON.stringify({ session_id: session.id, force })
        });
      } catch {
      }
    }
    try {
      if (force && wsStreamSocket) {
        wsStreamSocket.close();
      }
    } catch {
    }
  }

  function onComposerKeydown(event) {
    if (event.key !== 'Enter') return;
    if (event.isComposing) return;
    if (event.shiftKey) return;
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    sendMessage();
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
              reasoning_level: payload.reasoning_level,
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

  function wsStatusLabel(status) {
    const v = String(status || '').trim().toLowerCase();
    if (v === 'connected') return 'WS Connected';
    if (v === 'connecting') return 'WS Connecting';
    return 'WS Disconnected';
  }

  function closeWSHealthSocket() {
    wsHealthKeepAlive = false;
    if (wsHealthReconnectTimer) {
      clearTimeout(wsHealthReconnectTimer);
      wsHealthReconnectTimer = null;
    }
    if (wsHealthSocket) {
      try {
        wsHealthSocket.close();
      } catch {
      }
      wsHealthSocket = null;
    }
    wsHealthStatus = 'disconnected';
  }

  function scheduleWSHealthReconnect(delayMs = 1200) {
    if (!wsHealthKeepAlive || !wsHealthReady) return;
    if (wsHealthReconnectTimer) return;
    wsHealthReconnectTimer = setTimeout(() => {
      wsHealthReconnectTimer = null;
      connectWSHealthSocket();
    }, delayMs);
  }

  function connectWSHealthSocket() {
    if (!wsHealthKeepAlive || !wsHealthReady) return;
    if (wsHealthSocket && (wsHealthSocket.readyState === WebSocket.OPEN || wsHealthSocket.readyState === WebSocket.CONNECTING)) {
      return;
    }
    const candidates = [
      ...new Set(buildWSURLCandidates('/api/coding/ws').map((candidate) => String(candidate || '').trim()).filter(Boolean))
    ];
    if (candidates.length === 0) {
      wsHealthStatus = 'disconnected';
      scheduleWSHealthReconnect(2500);
      return;
    }
    let attemptIndex = 0;

    const connectAttempt = () => {
      if (!wsHealthKeepAlive || !wsHealthReady) return;
      if (attemptIndex >= candidates.length) {
        wsHealthStatus = 'disconnected';
        scheduleWSHealthReconnect(2800);
        return;
      }

      const wsURL = candidates[attemptIndex];
      attemptIndex += 1;
      wsHealthStatus = 'connecting';

      const socket = new WebSocket(wsURL);
      wsHealthSocket = socket;
      let opened = false;
      const connectTimeout = setTimeout(() => {
        if (opened) return;
        try {
          socket.close();
        } catch {
        }
      }, 3500);

      socket.onopen = () => {
        if (wsHealthSocket !== socket) return;
        opened = true;
        clearTimeout(connectTimeout);
        wsHealthStatus = 'connected';
      };

      socket.onmessage = () => {};

      socket.onerror = () => {
        try {
          socket.close();
        } catch {
        }
      };

      socket.onclose = () => {
        clearTimeout(connectTimeout);
        const isActiveSocket = wsHealthSocket === socket;
        if (isActiveSocket) {
          wsHealthSocket = null;
        }
        if (!isActiveSocket) return;
        if (!wsHealthKeepAlive) {
          wsHealthStatus = 'disconnected';
          return;
        }
        if (opened) {
          wsHealthStatus = 'disconnected';
          scheduleWSHealthReconnect(1800);
          return;
        }
        if (attemptIndex < candidates.length) {
          connectAttempt();
          return;
        }
        wsHealthStatus = 'disconnected';
        scheduleWSHealthReconnect(2800);
      };
    };

    connectAttempt();
  }

  onMount(() => {
    if (typeof window !== 'undefined') {
      try {
        const persisted = String(localStorage.getItem(viewModeStorageKey) || '').trim().toLowerCase();
        if (persisted === 'raw' || persisted === 'compact') {
          logViewMode = persisted;
        }
        selectedReasoningLevel = normalizeReasoningLevel(localStorage.getItem(reasoningLevelStorageKey));
      } catch {
      }
    }
    ensureSessionOnFirstOpen()
      .catch((error) => {
        viewStatus = String(error?.message || 'Failed to initialize coding sessions.');
      })
      .finally(() => {
        wsHealthReady = true;
        wsHealthKeepAlive = true;
        connectWSHealthSocket();
      });
    refreshCodexIdentity().catch(() => {});
  });

  $effect(() => {
    const sid = String(activeSessionID || '').trim();
    if (!sid) return;
    saveDraftForSession(sid, draftMessage);
  });

  $effect(() => {
    const sid = String(activeSessionID || '').trim();
    if (!sid) return;
    if (!wsHealthReady) return;
    wsHealthKeepAlive = true;
    connectWSHealthSocket();
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
    closeWSHealthSocket();
  });
</script>

<section class="panel coding-panel">
  <header class="coding-topbar">
    <div class="coding-topbar-left">
      <button class="btn btn-secondary topbar-icon-btn" type="button" onclick={backToDashboard} aria-label="Dashboard">
        <span class="topbar-btn-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M3 11.5 12 4l9 7.5v8.5a1 1 0 0 1-1 1h-5.5v-6h-5v6H4a1 1 0 0 1-1-1z"></path></svg>
        </span>
        <span class="topbar-btn-label">Dashboard</span>
      </button>
      <button class="btn btn-secondary topbar-icon-btn" type="button" onclick={() => (showSessionDrawer = true)} aria-label="Sessions">
        <span class="topbar-btn-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M4 5h16v4H4zm0 5.5h16v4H4zM4 16h16v3H4z"></path></svg>
        </span>
        <span class="topbar-btn-label">Sessions</span>
      </button>
      <div class="coding-topbar-title">
        <strong>CodexSess Chat</strong>
        <span title={selectedWorkDir || '~/'}>
          {selectedWorkDir || '~/'}
        </span>
      </div>
    </div>
    <div class="coding-topbar-right">
      <div class="coding-topbar-selects">
        <select class="coding-model-select" bind:value={selectedModel} onchange={queuePersistSessionPreferences} aria-label="Model for coding session">
          {#each models as model}
            <option value={model}>{model}</option>
          {/each}
        </select>
        <select class="coding-reasoning-select" bind:value={selectedReasoningLevel} onchange={onReasoningLevelChange} aria-label="Reasoning level for coding session">
          {#each reasoningLevels as level}
            <option value={level.value}>Reasoning: {level.label}</option>
          {/each}
        </select>
      </div>
      <div class="coding-view-mode-toggle" role="group" aria-label="coding output mode">
        <button
          class="btn topbar-icon-btn coding-view-toggle-btn {logViewMode === 'raw' ? 'mode-raw' : 'mode-compact'}"
          type="button"
          onclick={toggleLogViewMode}
          aria-label={logViewMode === 'raw' ? 'Switch to Compact View' : 'Switch to Raw CLI View'}
        >
          <span class="topbar-btn-icon" aria-hidden="true">
            {#if logViewMode === 'raw'}
              <svg viewBox="0 0 24 24"><path d="M4 7h10v3H4zm0 7h10v3H4zm12-3.5 2.5-2.5L20 9.5 17.5 12 20 14.5 18.5 16 16 13.5 13.5 16 12 14.5 14.5 12 12 9.5 13.5 8z"></path></svg>
            {:else}
              <svg viewBox="0 0 24 24"><path d="M4 6h16v3H4zm0 5h12v3H4zm0 5h9v3H4z"></path></svg>
            {/if}
          </span>
          <span class="topbar-btn-label">{logViewMode === 'raw' ? 'Raw CLI' : 'Compact'}</span>
        </button>
      </div>
      <button class="btn btn-secondary topbar-icon-btn" onclick={openNewSessionModal} disabled={loadingSessions || sending} aria-label="New Session">
        <span class="topbar-btn-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M12 5v14m-7-7h14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round"></path></svg>
        </span>
        <span class="topbar-btn-label">New Session</span>
      </button>
      <button class="btn btn-danger topbar-icon-btn" onclick={deleteActiveSession} disabled={!activeSessionID || deleting || sending} aria-label="Delete Session">
        <span class="topbar-btn-icon" aria-hidden="true">
          <svg viewBox="0 0 24 24"><path d="M6 7h12l-1 13H7zm3-3h6l1 2H8z"></path></svg>
        </span>
        <span class="topbar-btn-label">Delete</span>
      </button>
    </div>
  </header>

  <div class="coding-layout">
    <section class="coding-chat-area full" aria-label="Coding chat">
      {#if !activeSessionID}
        <div class="empty-state">Session not selected.</div>
      {:else}
        <div class="coding-messages-wrap">
        <div class="coding-messages" bind:this={messagesViewport} onscroll={onMessagesScroll}>
          {#if loadingOlderMessages}
            <p class="empty-note coding-loading-older">Loading older messages...</p>
          {/if}
          {#if loadingMessages && messages.length === 0}
            <p class="empty-note">Loading messages...</p>
          {:else}
            {@const renderedMessages = renderedMessagesForView()}
            {#if renderedMessages.length === 0}
              <p class="empty-note">Start by sending a coding instruction.</p>
            {:else}
              {#each renderedMessages as message (message.id)}
                {#if !shouldHideRenderedMessage(message)}
                <article class="coding-message {messageRoleClass(message)} {message.pending ? 'pending' : ''} {message.failed ? 'failed' : ''}">
                  <div class="coding-message-head">
                    <strong>
                      {#if message.role === 'assistant'}
                        {assistantDisplayName()}
                      {:else if message.role === 'exec'}
                        Terminal
                      {:else if message.role === 'subagent'}
                        Subagent
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
                      {#if message.role === 'exec'}
                        {execStatusLabel(message.exec_status, message.exec_exit_code)}
                      {:else if message.role === 'subagent'}
                        {subagentStatusLabel(message.subagent_status)}
                      {:else if message.failed}
                        Failed to send
                      {:else if !message.pending}
                        {formatWhen(message.created_at)}
                      {/if}
                    </span>
                  </div>
                  {#if message.role === 'exec'}
                    <div class="coding-exec-summary">
                      <code class="mono" title={message.exec_command || message.content || '-'}>
                        {message.exec_command || message.content || '-'}
                      </code>
                    </div>
                    <button class="btn btn-secondary btn-small coding-exec-open" type="button" onclick={() => openExecOutputModal(message)}>
                      Output
                    </button>
                  {:else if message.role === 'subagent'}
                    <div class="coding-subagent-summary">
                      <code class="mono" title={subagentDisplayTitle(message)}>
                        {subagentDisplayTitle(message)}
                      </code>
                      {#if subagentPreview(message)}
                        <pre>{subagentPreview(message)}</pre>
                      {/if}
                    </div>
                    <button class="btn btn-secondary btn-small coding-subagent-open" type="button" onclick={() => openSubagentDetailModal(message)}>
                      View Detail
                    </button>
                  {:else}
                    <pre>{isMessageExpanded(message.id) ? (messageDisplayContent(message) || '-') : messagePreviewContent(messageDisplayContent(message) || '-')}</pre>
                    {#if shouldCollapseContent(messageDisplayContent(message) || '')}
                      <button class="btn btn-secondary btn-small coding-show-more" type="button" onclick={() => toggleMessageExpanded(message.id)}>
                        {isMessageExpanded(message.id) ? 'Show less' : 'Show more'}
                      </button>
                    {/if}
                    {#if message.pending && message.role === 'assistant' && !String(message.content || '').trim()}
                      <p class="coding-message-status">Coding...</p>
                    {/if}
                  {/if}
                </article>
                {/if}
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
        {#if showScrollBottomButton}
          <button class="btn btn-secondary btn-small coding-scroll-bottom-btn" type="button" onclick={jumpToLatestMessages}>
            Jump to latest
          </button>
        {/if}
        </div>

        <div class="coding-composer">
          <textarea
            placeholder="Write coding task here... (Enter to send, Shift+Enter for newline). Supports /status, /review, and $skill"
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
              <span>{(sending || backgroundProcessing) ? (forceStopArmed ? 'Force Stop' : 'Stop') : 'Send'}</span>
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12l18-9-6 9 6 9-18-9z"></path></svg>
            </button>
          </div>
        </div>
      {/if}
    </section>
  </div>
  <div class="coding-status-line" aria-live="polite">
    <div class="coding-status-main">
      <span class="coding-status-ws {wsHealthStatus}">
        [{wsStatusLabel(wsHealthStatus)}]
      </span>
      <span class="coding-status-text {String(viewStatus || '').toLowerCase().startsWith('streaming') ? 'is-streaming' : ''}">
        {#if String(viewStatus || '').toLowerCase().startsWith('streaming')}
          <span class="status-streaming-pulse" aria-hidden="true"></span>
          <span class="status-streaming-label">Streaming</span>
          <span class="status-streaming-dots" aria-hidden="true"></span>
        {:else}
          {viewStatus}
        {/if}
      </span>
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

{#if showExecOutputModal && selectedExecEntry}
  <div class="modal-backdrop modal-backdrop-coding" role="presentation">
    <div class="modal-card modal-card-coding modal-card-exec-output" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && closeExecOutputModal()}>
      <div class="modal-head">
        <div>
          <h3>Terminal Output</h3>
          <p class="modal-subtitle">{execStatusLabel(selectedExecEntry.status, selectedExecEntry.exitCode)}</p>
          <p class="setting-title">Source: {selectedExecEntry.source || 'live'}</p>
        </div>
        <button class="btn btn-secondary btn-small" onclick={closeExecOutputModal}>Close</button>
      </div>
      <div class="modal-body">
        <p class="setting-title">Command</p>
        <pre class="coding-exec-modal-command mono">{selectedExecEntry.command}</pre>
        <p class="setting-title">Output</p>
        <pre class="coding-exec-modal-output mono">{selectedExecEntry.output}</pre>
        {#if selectedExecEntry.when}
          <p class="setting-title">{formatWhen(selectedExecEntry.when)}</p>
        {/if}
      </div>
    </div>
  </div>
{/if}

{#if showSubagentDetailModal && selectedSubagentEntry}
  <div class="modal-backdrop modal-backdrop-coding" role="presentation">
    <div class="modal-card modal-card-coding modal-card-subagent-detail" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && closeSubagentDetailModal()}>
      <div class="modal-head">
        <div>
          <h3>Subagent Detail</h3>
          <p class="modal-subtitle">{subagentStatusLabel(selectedSubagentEntry.status)}</p>
        </div>
        <button class="btn btn-secondary btn-small" onclick={closeSubagentDetailModal}>Close</button>
      </div>
      <div class="modal-body">
        <p class="setting-title">Activity</p>
        <pre class="coding-subagent-modal-block mono">{selectedSubagentEntry.title}</pre>
        <p class="setting-title">Tool</p>
        <pre class="coding-subagent-modal-block mono">{selectedSubagentEntry.tool || '-'}</pre>
        <p class="setting-title">Role / Target</p>
        <pre class="coding-subagent-modal-block mono">{subagentDisplayName(selectedSubagentEntry) || '-'}{subagentDisplayRole(selectedSubagentEntry) ? ` [${subagentDisplayRole(selectedSubagentEntry)}]` : ''}{selectedSubagentEntry.targetID ? `\n${selectedSubagentEntry.targetID}` : ''}</pre>
        <p class="setting-title">IDs</p>
        <pre class="coding-subagent-modal-block mono">{selectedSubagentEntry.ids.length > 0 ? selectedSubagentEntry.ids.join('\n') : '-'}</pre>
        <p class="setting-title">Prompt</p>
        <pre class="coding-subagent-modal-block mono">{selectedSubagentEntry.prompt || '-'}</pre>
        <p class="setting-title">Summary</p>
        <pre class="coding-subagent-modal-block mono">{selectedSubagentEntry.summary || '-'}</pre>
        <p class="setting-title">Raw Event</p>
        <pre class="coding-subagent-modal-raw mono">{selectedSubagentEntry.raw}</pre>
        {#if selectedSubagentEntry.when}
          <p class="setting-title">{formatWhen(selectedSubagentEntry.when)}</p>
        {/if}
      </div>
    </div>
  </div>
{/if}

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
            <div class="coding-session-item-row">
              <button
                class="coding-session-item {activeSessionID === session.id ? 'is-active' : ''}"
                onclick={() => selectSession(session.id)}
              >
                <strong>{session.title || 'New Session'}</strong>
                <span class="mono">{sessionDisplayID(session)}</span>
                <span>{formatWhen(session.last_message_at)}</span>
                <span class="mono">{session.work_dir || '~/'}</span>
              </button>
              <button
                class="btn btn-danger btn-small coding-session-delete"
                type="button"
                onclick={(event) => {
                  event.stopPropagation();
                  deleteSessionByID(session.id, { fromDrawer: true }).catch(() => {});
                }}
                disabled={deleting || sending}
                aria-label={`Delete session ${session.title || session.id}`}
                title="Delete session"
              >
                Delete
              </button>
            </div>
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
