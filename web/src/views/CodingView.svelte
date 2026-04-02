<script>
  import { onMount, onDestroy, tick } from 'svelte';
  import CodingComposer from './coding/CodingComposer.svelte';
  import CodingExecOutputModal from './coding/CodingExecOutputModal.svelte';
  import CodingMessagesPane from './coding/CodingMessagesPane.svelte';
  import CodingNewSessionModal from './coding/CodingNewSessionModal.svelte';
  import CodingSessionDrawer from './coding/CodingSessionDrawer.svelte';
  import CodingSkillModal from './coding/CodingSkillModal.svelte';
  import CodingStatusLine from './coding/CodingStatusLine.svelte';
  import CodingSubagentDetailModal from './coding/CodingSubagentDetailModal.svelte';
  import CodingTopbar from './coding/CodingTopbar.svelte';
  import {
    buildWSURLCandidates as buildWSURLCandidatesForTransport,
    requestJSON,
    resolvedAPIBase as resolveAPIBaseForTransport,
    toAPIURL as buildAPIURL,
    toWSURL as buildWSURL
  } from '../lib/coding/apiTransport.js';
  import {
    normalizeReasoningLevel
  } from '../lib/coding/reasoning.js';
  import {
    formatWhen
  } from '../lib/coding/timeFormat.js';
  import {
    buildSubagentLifecycleKey,
    buildSubagentMergeKey,
    cleanSubagentDetailText,
    choosePreferredSubagentName,
    choosePreferredSubagentRole,
    extractLikelyAgentIDs,
    extractStringList,
    extractSubagentIdentityFromText,
    extractSummaryFromAny,
    fileOperationDedupKey,
    fileOperationDisplayParts,
    formatSubagentFallbackName,
    inferSubagentRoleFromText,
    normalizeActivityCommandKey,
    normalizeExecIdentityCommandKey,
    normalizeExecOutputSource,
    normalizeExecStatus,
    normalizeFileOperationAction,
    parseRuntimeRecoveryActivity,
    normalizeSubagentRole,
    normalizeSubagentPromptKey,
    normalizeSubagentToolFamily,
    normalizedFileOperationLabel,
    parseMCPActivityPayload,
    parseToolArguments,
    parseActivityText,
    parseFileOperationPayload,
    parseFileOperationText
  } from '../lib/coding/activityParsing.js';
  import {
    mergeMessagesChronologically,
    reconcileLiveMessagesWithPersisted,
    sequenceFromMessage,
    sortMessagesChronologically,
    timestampFromMessage
  } from '../lib/coding/messageMerge.js';
  import {
    clearDraftForSession as clearSessionDraft,
    loadDraftForSession as loadSessionDraft,
    readSessionIDFromURL,
    saveDraftForSession as saveSessionDraft,
    sessionDisplayID,
    sortSessionsByRecency,
    syncSessionIDToURL
  } from './coding/sessionState.js';
  import {
    buildExecAwareMessages,
    parseExecEventFromPayload,
    parseMCPActivityText,
    parseRawEventPayload,
    parseSubagentActivityText,
    parseSubagentEventFromPayload
  } from './coding/liveMessagePipeline.js';
  import { findActiveLiveMessageID } from './coding/liveState.js';
  import {
    collectLiveMessageIDs,
    completedViewStatus,
    execStatusLabel,
    fileOpTone,
    isInternalRunnerActivity,
    parsePlanningFinalPlan,
    messageDisplayContent,
    messagePreviewContent,
    normalizeExecCommandForDisplay,
    projectMessagesForView,
    sanitizeSensitiveLogText,
    shouldCollapseContent,
    shouldHideRenderedMessage,
    subagentDetailFields,
    subagentDisplayName,
    subagentDisplayRole,
    subagentDisplayTitle,
    subagentPreview,
    subagentStatusLabel
  } from './coding/messageView.js';
  import {
    canLoadMoreChat,
    hiddenRenderedMessagesCount,
    isViewportNearBottom
  } from './coding/chatViewport.js';

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
  let initialSessionBootstrap = $state(true);
  let composerError = $state('');
  let composerLockedUntilAssistant = $state(false);
  let streamingPending = $state(false);
  let messagesViewport = $state(null);
  let autoFollowBottom = $state(true);
  let lastMessagesScrollTop = $state(0);
  let showScrollBottomButton = $state(false);
  let showNewSessionModal = $state(false);
  let newSessionPath = $state('~/');
  let creatingSessionFlow = $state(false);
  let pathSuggestions = $state(['~/']);
  let loadingPathSuggestions = $state(false);
  let pathSuggestTimer = null;
  let sessionPrefsTimer = null;
  let compactSnapshotPersistTimer = null;
  let queuedCompactSnapshot = null;
  let persistingSessionPrefs = $state(false);
  let showSkillModal = $state(false);
  let availableSkills = $state([]);
  let skillSearchQuery = $state('');
  let loadingSkills = $state(false);
  let showSessionDrawer = $state(false);
  let { activeCLIEmail = '' } = $props();
  let codexCLIEmail = $state('');
  let backgroundMonitorTimer = null;
  let expandedMessageMap = $state({});
  let messageLoadSource = $state('canonical');
  let backgroundProcessing = $state(false);
  let stopRequested = $state(false);
  let forceStopArmed = $state(false);
  let expectedWSDetach = $state(false);
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
  let renderedMessages = $state([]);
  let visibleRenderedMessageLimit = $state(30);
  let visibleRenderedMessages = $state([]);
  let historyExpandedManually = $state(false);
  let draftPersistTimer = null;
  const initialMessagesPageSize = 30;
  const olderMessagesPageSize = 40;
  const nearBottomThresholdPx = 120;

  const apiBase = String(import.meta.env.VITE_API_BASE || '').trim().replace(/\/+$/, '');
  const draftStoragePrefix = 'codexsess.coding.draft.v1:';
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
  function syncComposerControlsFromSession(session) {
    if (!session) return;
    selectedModel = String(session.model || selectedModel).trim() || selectedModel;
    selectedReasoningLevel = normalizeReasoningLevel(session.reasoning_level || selectedReasoningLevel);
    selectedWorkDir = String(session.work_dir || '~/').trim() || '~/';
    selectedSandboxMode = String(session.sandbox_mode || 'write').trim() || 'write';
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

  function resolvedAPIBase() {
    return resolveAPIBaseForTransport(apiBase);
  }

  function toAPIURL(url) {
    return buildAPIURL(url, apiBase);
  }

  function toWSURL(path) {
    return buildWSURL(path, apiBase);
  }

  function buildWSURLCandidates(path) {
    return buildWSURLCandidatesForTransport(path, apiBase);
  }

  async function req(url, options = {}) {
    return requestJSON(url, options, { apiBase, jsonHeaders });
  }

  function nextWSRequestID(prefix = 'req') {
    const p = String(prefix || 'req').trim().toLowerCase() || 'req';
    return `${p}-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
  }

  function appendActivityMessage(rawText, createdAt) {
    const text = String(rawText || '').trim();
    if (!text) return '';
    const fileOpLabel = parseFileOperationText(text);
    const next = {
      id: `activity-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      role: 'activity',
      content: text,
      file_op: fileOpLabel || '',
      created_at: String(createdAt || '').trim() || new Date().toISOString(),
      pending: false
    };
    messages = [...messages, next];
    return String(next.id || '');
  }

  function appendStreamMetaMessage(role, rawText, createdAt) {
    const messageRole = String(role || '').trim().toLowerCase();
    const text = String(rawText || '').trim();
    if (!messageRole || !text) return '';
    const next = {
      id: `${messageRole}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      role: messageRole,
      content: text,
      created_at: String(createdAt || '').trim() || new Date().toISOString(),
      pending: false
    };
    messages = [...messages, next];
    return String(next.id || '');
  }

  function appendKickoffFailureMessage(rawText, createdAt) {
    const text = String(rawText || '').trim();
    if (!text) return '';
    return appendStreamMetaMessage('stderr', text, createdAt);
  }

  function liveMessageIDs(rows = messages) {
    return collectLiveMessageIDs(Array.isArray(rows) ? rows : []);
  }

  function assistantRowsFromDonePayload(payload) {
    const rows = [];
    if (Array.isArray(payload?.assistant_messages)) {
      rows.push(...payload.assistant_messages.filter(Boolean));
    }
    if (payload?.assistant && typeof payload.assistant === 'object') {
      rows.push(payload.assistant);
    }
    const deduped = [];
    const seen = new Set();
    for (const row of rows) {
      const id = String(row?.id || '').trim();
      const key = id || `${String(row?.role || '').trim().toLowerCase()}|${String(row?.actor || '').trim().toLowerCase()}|${String(row?.content || '').trim()}`;
      if (!key || seen.has(key)) continue;
      seen.add(key);
      deduped.push(row);
    }
    return deduped;
  }

  function mergeDoneAssistantRows(payload) {
    const assistantRows = assistantRowsFromDonePayload(payload);
    if (assistantRows.length === 0) return;
    messages = messages.filter((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      return !(role === 'assistant' && item?.pending);
    });
    messages = reconcileLiveMessagesWithPersisted(messages, assistantRows, liveMessageIDs(messages));
  }

  function mergePendingUserMessage(pendingID, serverUserMessage) {
    const server = serverUserMessage && typeof serverUserMessage === 'object' ? serverUserMessage : null;
    if (!server) {
      messages = messages.filter((item) => item?.id !== pendingID);
      return;
    }
    messages = messages.map((item) => {
      if (item?.id !== pendingID) return item;
      const createdAt = String(item?.created_at || '').trim() || String(server?.created_at || '').trim();
      return {
        ...server,
        created_at: createdAt,
        updated_at: String(server?.updated_at || server?.created_at || createdAt).trim() || createdAt
      };
    });
  }

  function liveAssistantActorForSession() {
    return '';
  }

  function ensureStreamAssistantPlaceholder(actor = '', createdAt = new Date().toISOString()) {
    const normalizedActor = String(actor || '').trim().toLowerCase();
    const existing = messages.find((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      const itemActor = String(item?.actor || '').trim().toLowerCase();
      return role === 'assistant' && item?.pending && itemActor === normalizedActor;
    });
    if (existing?.id) return String(existing.id);
    const next = {
      id: buildStreamMessageID('assistant', normalizedActor, 0),
      role: 'assistant',
      content: '',
      created_at: createdAt,
      updated_at: createdAt,
      pending: true
    };
    if (normalizedActor) next.actor = normalizedActor;
    messages = mergeMessagesChronologically(messages, [next]);
    return String(next.id || '');
  }

  function dropPendingAssistantPlaceholders() {
    messages = messages.filter((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      return !(role === 'assistant' && item?.pending);
    });
  }

  function hasPendingAssistantPlaceholder(actor = '') {
    const normalizedActor = String(actor || '').trim().toLowerCase();
    return messages.some((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      if (role !== 'assistant' || !item?.pending) return false;
      if (!normalizedActor) return true;
      return String(item?.actor || '').trim().toLowerCase() === normalizedActor;
    });
  }

  function stripPendingAssistantPlaceholders(rows = []) {
    const src = Array.isArray(rows) ? rows : [];
    return src.filter((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      return !(role === 'assistant' && item?.pending);
    });
  }

  function stripTransientStreamRows(rows = []) {
    const src = Array.isArray(rows) ? rows : [];
    return src.filter((item) => {
      const id = String(item?.id || '').trim();
      if (!id.startsWith('stream-')) return true;
      const role = String(item?.role || '').trim().toLowerCase();
      if (role === 'assistant') return false;
      if (role === 'activity' || role === 'exec' || role === 'subagent' || role === 'stderr') return false;
      return true;
    });
  }

  function hasAssistantRows(rows = []) {
    const src = Array.isArray(rows) ? rows : [];
    return src.some((item) => String(item?.role || '').trim().toLowerCase() === 'assistant');
  }

  function hasSettledAssistantSince(startedAt, actor = '') {
    const startedMs = Date.parse(String(startedAt || '').trim());
    const normalizedActor = String(actor || '').trim().toLowerCase();
    return messages.some((item) => {
      const role = String(item?.role || '').trim().toLowerCase();
      if (role !== 'assistant' || item?.pending) return false;
      const itemActor = String(item?.actor || '').trim().toLowerCase();
      if (normalizedActor && itemActor !== normalizedActor) return false;
      if (Number.isNaN(startedMs)) return true;
      const ts = Date.parse(String(item?.updated_at || item?.created_at || '').trim());
      if (Number.isNaN(ts)) return true;
      return ts >= startedMs-1000;
    });
  }

  function activeSession() {
    return sessions.find((item) => item?.id === activeSessionID) || null;
  }

  function intentMode() {
    return 'chat';
  }

  function messageAccountEmail(message = null) {
    const messageEmail = String(message?.account_email || message?.codex_email || '').trim();
    if (messageEmail) return messageEmail;
    return '';
  }

  function messageCodexLabel(message = null) {
    const messageEmail = messageAccountEmail(message);
    if (messageEmail) return `Codex - ${messageEmail}`;
    const liveEmail = String(activeCLIEmail || '').trim();
    if (liveEmail) return `Codex - ${liveEmail}`;
    if (codexCLIEmail) return `Codex - ${codexCLIEmail}`;
    return 'Codex';
  }

  function actorCodexLabel(actorName, message = null) {
    const base = String(actorName || '').trim() || 'Codex';
    const messageEmail = messageAccountEmail(message);
    if (messageEmail) return `${base} - ${messageEmail}`;
    const liveEmail = String(activeCLIEmail || '').trim();
    if (liveEmail) return `${base} - ${liveEmail}`;
    if (codexCLIEmail) return `${base} - ${codexCLIEmail}`;
    return base;
  }

  function assistantDisplayName(message = null) {
    return messageCodexLabel(message);
  }

  function assistantUsageSummary(message) {
    const inputTokens = Number(message?.input_tokens || 0) || 0;
    const cachedInputTokens = Number(message?.cached_input_tokens || message?.cached_tokens || 0) || 0;
    const outputTokens = Number(message?.output_tokens || 0) || 0;
    const parts = [];
    if (inputTokens > 0) parts.push(`input ${inputTokens.toLocaleString()}`);
    if (cachedInputTokens > 0) parts.push(`cached ${cachedInputTokens.toLocaleString()}`);
    if (outputTokens > 0) parts.push(`output ${outputTokens.toLocaleString()}`);
    return parts.length > 0 ? `Usage: ${parts.join(' · ')}` : '';
  }

  function latestFailureStatusLabel(inputMessages) {
    const rows = Array.isArray(inputMessages) ? inputMessages : [];
    for (let idx = rows.length - 1; idx >= 0; idx -= 1) {
      const message = rows[idx] || {};
      if (String(message?.role || '').trim().toLowerCase() !== 'stderr') continue;
      const content = String(message?.content || '').trim();
      if (!content) continue;
      return content;
    }
    return '';
  }

  function messageActor(message) {
    const role = String(message?.role || '').trim().toLowerCase();
    if (role === 'executor') return role;
    if (role === 'exec' || role === 'subagent' || role === 'activity' || role === 'stderr') {
      const actor = String(message?.actor || '').trim().toLowerCase();
      if (actor === 'executor') return actor;
      return '';
    }
    if (role !== 'assistant') return '';
    const actor = String(message?.actor || '').trim().toLowerCase();
    if (actor === 'executor') return actor;
    return '';
  }

  function messageRoleClass(message) {
    const role = String(message?.role || '').trim().toLowerCase();
    const actor = messageActor(message);

    if (role === 'exec') return 'exec';
    if (role === 'subagent') return 'subagent';
    if (role === 'activity' && message?.mcp_activity) return 'mcp';
    if (role === 'stderr') {
      const rawActor = String(message?.actor || '').trim().toLowerCase();
      if (rawActor === 'executor') return 'stderr executor';
      return 'stderr';
    }
    if (role === 'activity') {
      const activityActor = String(message?.actor || '').trim().toLowerCase();
      const recovery = parseRuntimeRecoveryActivity(message?.content || '');
      if (recovery?.role === 'executor' || recovery?.role === 'chat') return 'activity internal-runner';
      if (activityActor === 'executor') return 'activity internal-runner';
      if (actor === 'executor') return 'activity internal-runner';
      return 'activity internal-runner';
    }

    if (role === 'assistant') {
      return 'assistant';
    }

    if (actor === 'executor') return 'assistant';

    if (role === 'event') return 'event';
    return 'user';
  }

  function messageRoleLabel(message) {
    const role = String(message?.role || '').trim().toLowerCase();

    if (role === 'exec') return 'Terminal';
    if (role === 'subagent') return 'Subagent';
    if (role === 'stderr') return 'CLI stderr';

    if (role === 'activity') {
      if (message?.mcp_activity || /^mcp\s+(done|failed):/i.test(String(message?.content || '').trim()) || /^running mcp:/i.test(String(message?.content || '').trim())) {
        return 'MCP';
      }
      return 'Activity';
    }

    if (role === 'assistant') return assistantDisplayName(message);

    if (role === 'event') return 'CLI Event';
    return 'You';
  }

  function messageShowsAssistantUsage(message) {
    const role = String(message?.role || '').trim().toLowerCase();
    return role === 'assistant';
  }

  function streamEventTimestamp(evt) {
    const raw = evt?.created_at || evt?.timestamp || evt?.payload?.created_at || evt?.payload?.timestamp || evt?.payload?.createdAt || evt?.createdAt;
    const text = String(raw || '').trim();
    if (text) return text;
    return new Date().toISOString();
  }

  function streamEventSequence(evt) {
    const value = Number(evt?.sequence ?? evt?.seq ?? evt?.event_seq ?? evt?.payload?.sequence ?? evt?.payload?.seq ?? evt?.payload?.event_seq ?? 0);
    if (Number.isFinite(value) && value > 0) return value;
    return 0;
  }

  function mergeExecOutput(baseOutput, nextOutput) {
    const base = String(baseOutput || '');
    const next = String(nextOutput || '');
    if (!base) return next;
    if (!next) return base;
    if (base.endsWith('\n') || next.startsWith('\n')) {
      return `${base}${next}`;
    }
    return `${base}\n${next}`;
  }

  function appendExecOutputToLatest(outputText, createdAt) {
    const text = String(outputText || '').trim();
    if (!text) return false;
    for (let idx = messages.length - 1; idx >= 0; idx -= 1) {
      const current = messages[idx];
      if (!current) continue;
      if (String(current?.role || '').trim().toLowerCase() !== 'exec') continue;
      const next = {
        ...current,
        exec_output: mergeExecOutput(current?.exec_output || '', text),
        exec_output_source: current?.exec_output_source || 'live',
        updated_at: createdAt
      };
      messages = messages.map((item, itemIdx) => (itemIdx === idx ? next : item));
      return true;
    }
    return false;
  }

  function streamEventInFlight(evt) {
    if (!evt || typeof evt !== 'object') return null;
    const direct = evt?.in_flight;
    if (typeof direct === 'boolean') return direct;
    const nested = evt?.payload?.in_flight;
    if (typeof nested === 'boolean') return nested;
    return null;
  }

  function streamEventActor(evt, streamType) {
    const raw = String(evt?.actor || evt?.payload?.actor || '').trim().toLowerCase();
    if (raw) return raw;
    return '';
  }

  function buildStreamMessageID(streamType, actor, sequence) {
    const suffix = Math.random().toString(36).slice(2, 8);
    const seqPart = sequence > 0 ? String(sequence) : String(Date.now());
    const actorPart = actor || 'chat';
    return `stream-${streamType}-${actorPart}-${seqPart}-${suffix}`;
  }

  function upsertStreamAssistantMessage({ actor, lane = '', text, isDelta, createdAt, sequence, streamType }) {
    if (!text) return;
    const normalizedActor = String(actor || '').trim().toLowerCase();
    let index = -1;
    for (let idx = messages.length - 1; idx >= 0; idx -= 1) {
      const candidate = messages[idx];
      if (!candidate) continue;
      if (String(candidate?.role || '').trim().toLowerCase() !== 'assistant') continue;
      const candidateActor = String(candidate?.actor || '').trim().toLowerCase();
      if (candidateActor !== normalizedActor) continue;
      if (candidate?.pending) {
        index = idx;
        break;
      }
    }
    if (index >= 0) {
      const current = messages[index];
      const base = String(current?.content || '');
      const nextContent = isDelta ? `${base}${text}` : text;
      const next = {
        ...current,
        content: nextContent,
        updated_at: createdAt,
        pending: isDelta
      };
      if (sequence > 0) next.sequence = sequence;
      messages = messages.map((item, idx) => (idx === index ? next : item));
      return;
    }
    const id = buildStreamMessageID(streamType || 'assistant', normalizedActor, sequence);
    const next = {
      id,
      role: 'assistant',
      content: text,
      created_at: createdAt,
      updated_at: createdAt,
      pending: isDelta
    };
    if (lane) next.lane = lane;
    if (normalizedActor) next.actor = normalizedActor;
    if (sequence > 0) next.sequence = sequence;
    messages = mergeMessagesChronologically(messages, [next]);
  }

  function autoFollowStreamIfNeeded(shouldAutoFollow, viewport = messagesViewport) {
    if (!shouldAutoFollow) return;
    if (viewport === messagesViewport && !autoFollowBottom) return;
    scrollViewportToBottom(viewport);
  }

  function appendStreamEventMessage(evt) {
    if (!evt) return;
    const streamType = String(evt?.stream_type || evt?.payload?.stream_type || '').trim().toLowerCase();
    if (!streamType) return;
    const rawText = String(evt?.text ?? evt?.payload?.text ?? evt?.message ?? evt?.payload?.message ?? '');
    const text = rawText.trim();
    let actor = streamEventActor(evt, streamType);
    let lane = String(evt?.lane || evt?.payload?.lane || '').trim().toLowerCase();
    if (!lane) {
      if (actor === 'executor') lane = 'executor';
      if (actor === 'chat' || actor === 'assistant' || actor === 'user') lane = 'chat';
    }
    const targetViewport = resolveViewportForStreamEvent(evt, streamType, actor);
    const shouldAutoFollow = isViewportNearBottom(targetViewport, nearBottomThresholdPx);
    const createdAt = streamEventTimestamp(evt);
    const sequence = streamEventSequence(evt);
    if (streamType === 'raw_event') {
      const rawPayloadText = String(evt?.raw_payload || evt?.payload?.raw_payload || text || '').trim();
      const payload = parseRawEventPayload(rawPayloadText);
      const parsedExec = parseExecEventFromPayload(payload);
      if (parsedExec?.command) {
        const next = {
          id: buildStreamMessageID('exec', actor, sequence),
          role: 'exec',
          content: parsedExec.command,
          created_at: createdAt,
          updated_at: createdAt,
          pending: false,
          exec_command: parsedExec.command,
          exec_status: parsedExec.status || 'running',
          exec_exit_code: Number(parsedExec.exitCode || 0) || 0,
          exec_output: parsedExec.output || '',
          exec_output_source: 'live'
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      const parsedSubagent = parseSubagentEventFromPayload(payload);
      if (parsedSubagent?.toolName) {
        const next = {
          id: buildStreamMessageID('subagent', actor, sequence),
          role: 'subagent',
          content: parsedSubagent.title || text,
          created_at: createdAt,
          updated_at: createdAt,
          pending: false,
          subagent_status: parsedSubagent.status || 'running',
          subagent_phase: parsedSubagent.phase || '',
          subagent_key: parsedSubagent.key || '',
          subagent_lifecycle_key: parsedSubagent.lifecycleKey || parsedSubagent.key || '',
          subagent_title: parsedSubagent.title || text,
          subagent_tool: parsedSubagent.toolName || '',
          subagent_ids: Array.isArray(parsedSubagent.ids) ? parsedSubagent.ids : [],
          subagent_target_id: parsedSubagent.targetID || '',
          subagent_nickname: parsedSubagent.nickname || '',
          subagent_name: parsedSubagent.name || '',
          subagent_role: parsedSubagent.agentType || '',
          subagent_model: parsedSubagent.model || '',
          subagent_reasoning: parsedSubagent.reasoning || '',
          subagent_prompt: parsedSubagent.prompt || '',
          subagent_summary: parsedSubagent.summary || '',
          subagent_raw: parsedSubagent.raw || {}
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      const parsedMCP = parseMCPActivityPayload(payload);
      if (parsedMCP?.text) {
        const next = {
          id: buildStreamMessageID('mcp', actor, sequence),
          role: 'activity',
          content: parsedMCP.text,
          mcp_activity: true,
          mcp_activity_generic: /^mcp server status:/i.test(parsedMCP.text),
          mcp_activity_target: String(parsedMCP.target || '').trim(),
          mcp_activity_server: String(parsedMCP.server || '').trim(),
          mcp_activity_tool: String(parsedMCP.tool || '').trim(),
          created_at: createdAt,
          updated_at: createdAt,
          pending: false
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      const fileOpLabel = parseFileOperationPayload(payload);
      if (fileOpLabel) {
        const next = {
          id: buildStreamMessageID('fileop', actor, sequence),
          role: 'activity',
          content: fileOpLabel,
          file_op: fileOpLabel,
          created_at: createdAt,
          updated_at: createdAt,
          pending: false
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      if (String(payload?.type || '').trim().toLowerCase().replaceAll('/', '.') === 'thread.started') {
        return;
      }
      return;
    }
    if (streamType === 'delta' || streamType === 'assistant_message') {
      upsertStreamAssistantMessage({
        actor,
        lane,
        text: rawText,
        isDelta: streamType === 'delta',
        createdAt,
        sequence,
        streamType
      });
      autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
      return;
    }
    if (!text) return;
    if (streamType === 'activity') {
      if (/^turn\s+(started|completed)\b/i.test(text) || /^thread status:/i.test(text)) {
        return;
      }
      const execActivity = parseActivityText(text);
      if (execActivity?.kind === 'running' || execActivity?.kind === 'done' || execActivity?.kind === 'failed') {
        const next = {
          id: buildStreamMessageID(streamType, actor, sequence),
          role: 'exec',
          content: execActivity.command || text,
          created_at: createdAt,
          updated_at: createdAt,
          pending: false,
          exec_command: execActivity.command || text,
          exec_status: execActivity.kind === 'failed' ? 'failed' : (execActivity.kind === 'done' ? 'done' : 'running'),
          exec_exit_code: Number(execActivity.exitCode || 0) || 0,
          exec_output: '',
          exec_output_source: 'live'
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      if (/^command output:/i.test(text)) {
        const outputText = text.replace(/^command output:\s*/i, '').trim();
        if (appendExecOutputToLatest(outputText, createdAt)) {
          autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
          return;
        }
      }
      const fileOpLabel = parseFileOperationText(text);
      if (fileOpLabel) {
        const next = {
          id: buildStreamMessageID(streamType, actor, sequence),
          role: 'activity',
          content: fileOpLabel,
          file_op: fileOpLabel,
          created_at: createdAt,
          updated_at: createdAt,
          pending: false,
          internal_runner: true
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      const subagentActivity = parseSubagentActivityText(text);
      if (subagentActivity?.toolName) {
        const next = {
          id: buildStreamMessageID(streamType, actor, sequence),
          role: 'subagent',
          content: subagentActivity.title || text,
          created_at: createdAt,
          updated_at: createdAt,
          pending: false,
          subagent_status: subagentActivity.status || 'running',
          subagent_phase: subagentActivity.phase || '',
          subagent_key: subagentActivity.key || '',
          subagent_lifecycle_key: subagentActivity.key || '',
          subagent_title: subagentActivity.title || text,
          subagent_tool: subagentActivity.toolName || '',
          subagent_ids: Array.isArray(subagentActivity.ids) ? subagentActivity.ids : [],
          subagent_target_id: subagentActivity.targetID || '',
          subagent_nickname: subagentActivity.nickname || '',
          subagent_role: subagentActivity.agentType || '',
          subagent_model: subagentActivity.model || '',
          subagent_reasoning: subagentActivity.reasoning || '',
          subagent_prompt: subagentActivity.prompt || '',
          subagent_summary: subagentActivity.summary || '',
          subagent_raw: subagentActivity.raw || {}
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
      const parsedMCPText = parseMCPActivityText(text);
      if (parsedMCPText?.text) {
        if (parsedMCPText.generic) {
          return;
        }
        const next = {
          id: buildStreamMessageID(streamType, actor, sequence),
          role: 'activity',
          content: parsedMCPText.text,
          mcp_activity: true,
          mcp_activity_generic: Boolean(parsedMCPText.generic),
          created_at: createdAt,
          updated_at: createdAt,
          pending: false,
          internal_runner: true
        };
        if (lane) next.lane = lane;
        if (actor) next.actor = actor;
        if (sequence > 0) next.sequence = sequence;
        messages = mergeMessagesChronologically(messages, [next]);
        autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
        return;
      }
    }
    const role = streamType === 'stderr' ? 'stderr' : 'activity';
    const next = {
      id: buildStreamMessageID(streamType, actor, sequence),
      role,
      content: text,
      created_at: createdAt,
      updated_at: createdAt,
      pending: false,
      internal_runner: Boolean(
        actor === 'executor' ||
        streamType === 'stderr'
      )
    };
    if (lane) next.lane = lane;
    if (actor) next.actor = actor;
    if (sequence > 0) next.sequence = sequence;
    if (role === 'activity' && /^mcp\b/i.test(text)) {
      next.mcp_activity = true;
      next.mcp_activity_generic = true;
    }
    messages = mergeMessagesChronologically(messages, [next]);
    autoFollowStreamIfNeeded(shouldAutoFollow, targetViewport);
  }

  let stopButtonLabel = $derived.by(() => {
    if (!(sending || backgroundProcessing)) return 'Send';
    if (forceStopArmed) return 'Force Stop';
    return 'Stop';
  });

  let effectiveViewStatus = $derived.by(() => {
    if (stopRequested) {
      return 'Stopping...';
    }
    if (sending || backgroundProcessing) {
      return 'Streaming...';
    }
    return viewStatus;
  });

  function renderedMessagesForView() {
    return projectMessagesForView(messages, {
      buildExecAwareMessages,
      rawMode: false,
      alreadyCanonical: true
    });
  }

  function activeLiveBubbleMessageID() {
    return findActiveLiveMessageID(renderedMessages, isInternalRunnerActivity);
  }

  function openExecOutputModal(message) {
    if (!message) return;
    const status = normalizeExecStatus(message?.exec_status || 'running');
    const exitCode = Number(message?.exec_exit_code || 0) || 0;
    selectedExecEntry = {
      command: String(message?.exec_command || message?.content || '-').trim() || '-',
      status,
      statusLabel: execStatusLabel(status, exitCode),
      exitCode,
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
    const detail = subagentDetailFields(message);
    const raw = message?.subagent_raw || {};
    let rawText = '{}';
    try {
      rawText = JSON.stringify(raw, null, 2);
    } catch {
      rawText = '{}';
    }
    const status = String(message?.subagent_status || 'done').trim().toLowerCase();
    selectedSubagentEntry = {
      title: String(subagentDisplayTitle(message) || message?.subagent_title || message?.content || 'Subagent Activity').trim(),
      status,
      statusLabel: subagentStatusLabel(status),
      tool: String(message?.subagent_tool || '').trim(),
      nickname: detail.name,
      agentName: detail.agentName,
      role: detail.role,
      nameRole: detail.nameRole,
      model: detail.model,
      reasoning: detail.reasoning,
      targetID: detail.target,
      ids: Array.isArray(message?.subagent_ids) ? message.subagent_ids.filter(Boolean) : [],
      prompt: detail.prompt,
      summary: detail.summary,
      raw: sanitizeSensitiveLogText(rawText),
      when: String(message?.updated_at || message?.created_at || '')
    };
    showSubagentDetailModal = true;
  }

  function closeSubagentDetailModal() {
    showSubagentDetailModal = false;
    selectedSubagentEntry = null;
  }

  function hasAssistantAfterLatestUser(rows) {
    const list = Array.isArray(rows) ? rows : [];
    let latestUserIndex = -1;
    for (let i = 0; i < list.length; i += 1) {
      if (String(list[i]?.role || '').trim().toLowerCase() === 'user') {
        latestUserIndex = i;
      }
    }
    if (latestUserIndex < 0) return false;
    for (let i = latestUserIndex + 1; i < list.length; i += 1) {
      const item = list[i] || {};
      if (String(item?.role || '').trim().toLowerCase() !== 'assistant') continue;
      if (String(item?.content || '').trim()) return true;
    }
    return false;
  }

  function hasVisibleOutcomeAfterLatestUser(rows) {
    const list = Array.isArray(rows) ? rows : [];
    let latestUserIndex = -1;
    for (let i = 0; i < list.length; i += 1) {
      if (String(list[i]?.role || '').trim().toLowerCase() === 'user') {
        latestUserIndex = i;
      }
    }
    if (latestUserIndex < 0) return false;
    for (let i = latestUserIndex + 1; i < list.length; i += 1) {
      const item = list[i] || {};
      const role = String(item?.role || '').trim().toLowerCase();
      const content = String(item?.content || '').trim();
      if (role === 'assistant' && content) return true;
      if (role === 'stderr' && content) return true;
      if (role === 'exec') {
        const command = String(item?.exec_command || item?.content || '').trim();
        const output = String(item?.exec_output || '').trim();
        const status = String(item?.exec_status || '').trim().toLowerCase();
        if (command || output || status === 'done' || status === 'failed') return true;
      }
      if (role === 'subagent') {
        const title = String(item?.subagent_title || item?.content || '').trim();
        const phase = String(item?.subagent_phase || '').trim().toLowerCase();
        const status = String(item?.subagent_status || '').trim().toLowerCase();
        if (title || phase === 'completed' || status === 'done' || status === 'failed') return true;
      }
      if (role === 'activity') {
        if (item?.file_op) return true;
        if (item?.mcp_activity && !item?.mcp_activity_generic && content) return true;
        const recovery = parseRuntimeRecoveryActivity(content);
        if (recovery?.phase === 'failed' || recovery?.phase === 'completed') return true;
        if (content && !/^thread\./i.test(content) && !/^turn\./i.test(content)) return true;
      }
    }
    return false;
  }

  function hasVisibleOutcomeSince(rows, startedAt, actor = '') {
    const list = Array.isArray(rows) ? rows : [];
    const startedText = String(startedAt || '').trim();
    const startedMs = Date.parse(startedText);
    const normalizedActor = String(actor || '').trim().toLowerCase();
    for (const item of list) {
      const itemRole = String(item?.role || '').trim().toLowerCase();
      const itemActor = String(item?.actor || '').trim().toLowerCase();
      if (normalizedActor && itemActor && itemActor !== normalizedActor) continue;
      if (!Number.isNaN(startedMs)) {
        const itemMs = Date.parse(String(item?.updated_at || item?.created_at || '').trim());
        if (!Number.isNaN(itemMs) && itemMs < startedMs - 1000) {
          continue;
        }
      }
      const content = String(item?.content || '').trim();
      if (itemRole === 'assistant' && content) return true;
      if (itemRole === 'stderr' && content) return true;
      if (itemRole === 'exec') {
        const command = String(item?.exec_command || item?.content || '').trim();
        const output = String(item?.exec_output || '').trim();
        const status = String(item?.exec_status || '').trim().toLowerCase();
        if (command || output || status === 'done' || status === 'failed') return true;
      }
      if (itemRole === 'subagent') {
        const title = String(item?.subagent_title || item?.content || '').trim();
        const phase = String(item?.subagent_phase || '').trim().toLowerCase();
        const status = String(item?.subagent_status || '').trim().toLowerCase();
        if (title || phase === 'completed' || status === 'done' || status === 'failed') return true;
      }
      if (itemRole === 'activity') {
        if (item?.file_op) return true;
        if (item?.mcp_activity && !item?.mcp_activity_generic && content) return true;
        const recovery = parseRuntimeRecoveryActivity(content);
        if (recovery?.phase === 'failed' || recovery?.phase === 'completed') return true;
        if (content && !/^thread\./i.test(content) && !/^turn\./i.test(content)) return true;
      }
    }
    return false;
  }

  function hasRunnerReplyAfterLatestUser(rows) {
    const list = Array.isArray(rows) ? rows : [];
    let latestUserIndex = -1;
    for (let i = 0; i < list.length; i += 1) {
      if (String(list[i]?.role || '').trim().toLowerCase() === 'user') {
        latestUserIndex = i;
      }
    }
    if (latestUserIndex < 0) return false;
    for (let i = latestUserIndex + 1; i < list.length; i += 1) {
      const item = list[i] || {};
      if (String(item?.role || '').trim().toLowerCase() === 'assistant' && String(item?.content || '').trim()) {
        return true;
      }
      const actor = messageActor(item);
      if (actor === 'executor') {
        return true;
      }
    }
    return false;
  }

  function shouldReleaseBusyStateFromMessages(rows, session = activeSession()) {
    return false;
  }

  function releaseComposerLockIfAssistantAppeared(rows) {
    if (!composerLockedUntilAssistant) return;
    if (hasVisibleOutcomeAfterLatestUser(rows)) {
      composerLockedUntilAssistant = false;
    }
  }

  function formatComposerError(raw) {
    const text = String(raw || '').trim();
    const lower = text.toLowerCase();
    if (
      lower.includes('account_deactivated') ||
      lower.includes('account has been deactivated')
    ) {
      return 'Codex account was deactivated. Switched CLI account automatically if another account was available.';
    }
    if (lower.includes('unexpected status 401') || lower.includes('auth error: 401')) {
      return 'Codex authorization failed. Switched CLI account automatically if another account was available.';
    }
    if (text.length <= 220) return text;
    return `${text.slice(0, 217).trimEnd()}...`;
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
      const activeAccount =
        items.find((item) => Boolean(item?.active_cli)) ||
        items.find((item) => Boolean(item?.active_api)) ||
        items.find((item) => Boolean(item?.active));
      codexCLIEmail = String(activeAccount?.email || '').trim();
    } catch {
      codexCLIEmail = '';
    }
  }

  async function loadSessions({ autoSelect = true } = {}) {
    loadingSessions = true;
    try {
      const data = await req('/api/coding/sessions');
      sessions = sortSessionsByRecency(Array.isArray(data.sessions) ? data.sessions : []);
      if (sessions.length === 0) {
        activeSessionID = '';
      }
      if (autoSelect && sessions.length > 0 && !sessions.find((item) => item.id === activeSessionID)) {
        activeSessionID = sessions[0].id;
      }
      const active = activeSession();
      if (active) {
        syncComposerControlsFromSession(active);
      }
    } finally {
      loadingSessions = false;
    }
  }

  async function refreshSessionMetadata(sessionID) {
    const sid = String(sessionID || '').trim();
    if (!sid) return null;
    const data = await req('/api/coding/sessions');
    sessions = sortSessionsByRecency(Array.isArray(data.sessions) ? data.sessions : []);
    const updated = sessions.find((item) => String(item?.id || '').trim() === sid) || null;
    if (updated && sid === String(activeSessionID || '').trim()) {
      syncComposerControlsFromSession(updated, { preserveMode: true });
    }
    return updated;
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
    return isViewportNearBottom(messagesViewport, nearBottomThresholdPx);
  }

  function resolveViewportForStreamEvent() {
    return messagesViewport;
  }

  function refreshScrollAffordance() {
    showScrollBottomButton = Boolean(messagesViewport && !isMessagesViewportNearBottom());
  }

  async function fetchCodingMessagePage(sessionID, { limit, beforeID = '', viewMode = 'compact' } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) return { messages: [], has_more: false, oldest_id: '', newest_id: '', source: 'raw' };
    const params = new URLSearchParams({
      session_id: sid,
      limit: String(Number(limit || initialMessagesPageSize) || initialMessagesPageSize),
      view: String(viewMode || 'compact').trim().toLowerCase() === 'raw' ? 'raw' : 'compact'
    });
    const cursor = String(beforeID || '').trim();
    if (cursor) {
      params.set('before_id', cursor);
    }
    return req(`/api/coding/messages?${params.toString()}`);
  }

  function clearCompactSnapshotPersistTimer() {
    if (compactSnapshotPersistTimer) {
      clearTimeout(compactSnapshotPersistTimer);
      compactSnapshotPersistTimer = null;
    }
    queuedCompactSnapshot = null;
  }

  function applyCompactSnapshot(snapshotState) {
    if (!snapshotState) return;
    const incoming = mergeMessageLanesFromSnapshot(
      Array.isArray(snapshotState?.messages) ? snapshotState.messages : [],
      snapshotState?.laneProjections || null
    );
    const pendingID = String(snapshotState?.pendingID || '').trim();
    const sentContent = String(snapshotState?.sentContent || '').trim();
    const pendingRows = pendingID ? messages.filter((item) => item?.id === pendingID) : [];
    const existingRows = pendingID ? messages.filter((item) => item?.id !== pendingID) : messages;
    const normalizedExistingRows = (hasAssistantRows(incoming) || hasVisibleOutcomeAfterLatestUser(incoming))
      ? stripTransientStreamRows(stripPendingAssistantPlaceholders(existingRows))
      : existingRows;
    const pendingContentCandidates = [
      sentContent,
      ...pendingRows.map((item) => String(item?.content || '').trim())
    ].filter(Boolean);
    const incomingHasUser = incoming.some(
      (item) =>
        String(item?.role || '').trim().toLowerCase() === 'user' &&
        pendingContentCandidates.includes(String(item?.content || '').trim())
    );
    messageLoadSource = 'canonical';
    let next = reconcileLiveMessagesWithPersisted(normalizedExistingRows, incoming, liveMessageIDs(normalizedExistingRows));
    if (!incomingHasUser && pendingRows.length > 0) {
      // Keep optimistic user turn before incoming outcome rows when the
      // compact snapshot has not projected user rows yet.
      next = mergeMessagesChronologically(pendingRows, next);
    }
    messages = sortMessagesChronologically(next);
    releaseComposerLockIfAssistantAppeared(messages);
    streamingPending = false;
    scrollMessagesToBottom();
  }

  function flushCompactSnapshotQueue() {
    if (compactSnapshotPersistTimer) {
      clearTimeout(compactSnapshotPersistTimer);
      compactSnapshotPersistTimer = null;
    }
    if (!queuedCompactSnapshot) return;
    const nextSnapshot = queuedCompactSnapshot;
    queuedCompactSnapshot = null;
    applyCompactSnapshot(nextSnapshot);
  }

  function scheduleCompactSnapshotPersist(snapshotState, delayMs = 90) {
    queuedCompactSnapshot = snapshotState;
    if (compactSnapshotPersistTimer) return;
    compactSnapshotPersistTimer = setTimeout(() => {
      compactSnapshotPersistTimer = null;
      const nextSnapshot = queuedCompactSnapshot;
      queuedCompactSnapshot = null;
      applyCompactSnapshot(nextSnapshot);
    }, delayMs);
  }

  function laneFromProjectionRows(rows = []) {
    const map = new Map();
    const list = Array.isArray(rows) ? rows : [];
    for (const raw of list) {
      const row = raw && typeof raw === 'object' ? raw : null;
      if (!row) continue;
      const lane = String(row?.lane || '').trim().toLowerCase();
      if (!lane) continue;
      const payload = row?.payload && typeof row.payload === 'object' ? row.payload : null;
      const messageID = String(payload?.id || '').trim();
      if (!messageID) continue;
      if (!map.has(messageID)) map.set(messageID, lane);
    }
    return map;
  }

  function mergeMessageLanesFromSnapshot(messagesInput = [], laneProjections = null) {
    const list = Array.isArray(messagesInput) ? messagesInput : [];
    const projections = laneProjections && typeof laneProjections === 'object' ? laneProjections : null;
    if (!projections) return list;
    const laneMap = new Map();
    for (const laneName of ['chat', 'executor']) {
      const rows = projections?.[laneName];
      const partial = laneFromProjectionRows(rows);
      for (const [messageID, lane] of partial.entries()) {
        if (!laneMap.has(messageID)) laneMap.set(messageID, lane);
      }
    }
    if (laneMap.size === 0) return list;
    return list.map((item) => {
      const row = item && typeof item === 'object' ? item : null;
      if (!row) return item;
      const messageID = String(row?.id || '').trim();
      if (!messageID) return row;
      const inferredLane = String(laneMap.get(messageID) || '').trim().toLowerCase();
      if (!inferredLane) return row;
      if (String(row?.lane || '').trim().toLowerCase() === inferredLane) return row;
      return { ...row, lane: inferredLane };
    });
  }

  async function loadMessages(sessionID, { silent = false, preserveViewport = false, preserveLoadedHistory = false } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) {
      messages = [];
      messageLoadSource = 'canonical';
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
      const data = await fetchCodingMessagePage(sid, { limit: initialMessagesPageSize, viewMode: 'compact' });
      if (String(activeSessionID || '').trim() !== sid) {
        return;
      }
      const incomingMessages = Array.isArray(data?.messages) ? data.messages : [];
      const currentBaseMessages = (hasAssistantRows(incomingMessages) || hasVisibleOutcomeAfterLatestUser(incomingMessages))
        ? stripTransientStreamRows(stripPendingAssistantPlaceholders(messages))
        : messages;
      const currentLiveIDs = liveMessageIDs(currentBaseMessages);
      const mergedMessages = currentLiveIDs.length > 0
        ? reconcileLiveMessagesWithPersisted(currentBaseMessages, incomingMessages, currentLiveIDs)
        : incomingMessages;
      const keepLoadedHistory = Boolean(
        messages.length > 0 &&
        (
          (preserveViewport && !autoFollowBottom) ||
          preserveLoadedHistory
        )
      );
      messageLoadSource = 'canonical';
      if (keepLoadedHistory) {
        messages = reconcileLiveMessagesWithPersisted(messages, mergedMessages, collectLiveMessageIDs(messages));
        historyExpandedManually = historyExpandedManually || preserveLoadedHistory;
        hasMoreMessages = hasMoreMessages || Boolean(data?.has_more);
        oldestLoadedMessageID = String(data?.oldest_id || messages?.[0]?.id || oldestLoadedMessageID || '').trim();
        newestLoadedMessageID = String(data?.newest_id || messages?.[messages.length - 1]?.id || newestLoadedMessageID || '').trim();
      } else {
        messages = mergedMessages;
        visibleRenderedMessageLimit = initialMessagesPageSize;
        historyExpandedManually = false;
        hasMoreMessages = Boolean(data?.has_more);
        oldestLoadedMessageID = String(data?.oldest_id || messages?.[0]?.id || '').trim();
        newestLoadedMessageID = String(data?.newest_id || messages?.[messages.length - 1]?.id || '').trim();
      }
      releaseComposerLockIfAssistantAppeared(messages);
      if (shouldReleaseBusyStateFromMessages(messages, activeSession())) {
        backgroundProcessing = false;
      }
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

  async function waitForSettledVisibleOutcome(sessionID, { actor = '', timeoutMs = 7000, intervalMs = 350 } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) return;
    const deadline = Date.now() + Math.max(0, Number(timeoutMs || 0));
    while (Date.now() < deadline) {
      if (String(activeSessionID || '').trim() !== sid) return;
      const pending = hasPendingAssistantPlaceholder(actor);
      const visibleOutcome = hasVisibleOutcomeAfterLatestUser(messages);
      if (!pending && visibleOutcome) return;
      await new Promise((resolve) => setTimeout(resolve, intervalMs));
      if (String(activeSessionID || '').trim() !== sid) return;
      await loadMessages(sid, { silent: true, preserveViewport: false, preserveLoadedHistory: historyExpandedManually });
      if (!hasPendingAssistantPlaceholder(actor) && hasVisibleOutcomeAfterLatestUser(messages)) {
        return;
      }
    }
    if (hasVisibleOutcomeAfterLatestUser(messages)) {
      dropPendingAssistantPlaceholders();
      composerLockedUntilAssistant = false;
    }
  }

  async function loadOlderMessages() {
    const sid = String(activeSessionID || '').trim();
    let beforeID = String(oldestLoadedMessageID || '').trim();
    if (!sid || loadingOlderMessages || loadingMessages) return;
    if (!messagesViewport) return;
    const hiddenLocalCount = hiddenRenderedMessagesCount(renderedMessages, visibleRenderedMessageLimit);
    if (hiddenLocalCount > 0) {
      loadingOlderMessages = true;
      historyExpandedManually = true;
      const prevScrollHeight = messagesViewport.scrollHeight;
      const prevScrollTop = messagesViewport.scrollTop;
      try {
        visibleRenderedMessageLimit = Math.min(renderedMessages.length, visibleRenderedMessageLimit + olderMessagesPageSize);
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
      return;
    }
    if (!beforeID || !hasMoreMessages) return;
    loadingOlderMessages = true;
    historyExpandedManually = true;
    const prevScrollHeight = messagesViewport.scrollHeight;
    const prevScrollTop = messagesViewport.scrollTop;
    try {
      let data = null;
      for (let attempt = 0; attempt < 2; attempt += 1) {
        data = await fetchCodingMessagePage(sid, { limit: olderMessagesPageSize, beforeID, viewMode: 'compact' });
        if (String(activeSessionID || '').trim() !== sid) {
          return;
        }
        const older = Array.isArray(data?.messages) ? data.messages : [];
        const hasMore = Boolean(data?.has_more);
        if (older.length > 0 || !hasMore || attempt > 0) {
          break;
        }
        await loadMessages(sid, { silent: true, preserveViewport: true, preserveLoadedHistory: true });
        const refreshedBeforeID = String(oldestLoadedMessageID || '').trim();
        if (!refreshedBeforeID || refreshedBeforeID === beforeID) {
          break;
        }
        beforeID = refreshedBeforeID;
      }
      if (!data || String(activeSessionID || '').trim() !== sid) {
        return;
      }
      messageLoadSource = 'canonical';
      const older = Array.isArray(data?.messages) ? data.messages : [];
      if (older.length > 0) {
        const existingIDs = new Set(messages.map((item) => String(item?.id || '').trim()).filter(Boolean));
        const dedupedOlder = older.filter((item) => !existingIDs.has(String(item?.id || '').trim()));
        if (dedupedOlder.length > 0) {
          messages = [...dedupedOlder, ...messages];
          visibleRenderedMessageLimit += dedupedOlder.length;
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
  }

  async function refreshBackgroundStatus(sessionID, { syncMessages = false } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) {
      backgroundProcessing = false;
      return false;
    }
    const data = await req(`/api/coding/status?session_id=${encodeURIComponent(sid)}`);
    const inFlight = Boolean(data?.in_flight);
    let session = sessions.find((item) => String(item?.id || '').trim() === sid) || null;
    const startedAt = String(data?.started_at || '').trim();
    const becameDone = backgroundProcessing && !inFlight;
    backgroundProcessing = inFlight;
    if (inFlight && !sending) {
      streamingPending = !hasVisibleOutcomeSince(messages, startedAt);
    }
    if (syncMessages && (becameDone || !inFlight)) {
      session = await refreshSessionMetadata(sid).catch(() => session);
    }
    if (inFlight) {
      if (syncMessages) {
        await loadMessages(sid, { silent: true, preserveViewport: true, preserveLoadedHistory: historyExpandedManually });
      }
      if (hasVisibleOutcomeSince(messages, startedAt) && shouldReleaseBusyStateFromMessages(messages, session)) {
        backgroundProcessing = false;
        streamingPending = false;
        if (!sending) {
          viewStatus = completedViewStatus(messages, { messageActor });
        }
        return false;
      }
      if (!sending) {
        if (stopRequested) {
          viewStatus = 'Stopping...';
        } else {
          viewStatus = 'Streaming...';
        }
      }
      return true;
    }
    if (syncMessages) {
      await loadMessages(sid, { silent: true, preserveViewport: false, preserveLoadedHistory: historyExpandedManually });
      session = await refreshSessionMetadata(sid).catch(() => session);
    }
    streamingPending = false;
    if (stopRequested) {
      composerLockedUntilAssistant = false;
    }
    if (becameDone) {
      viewStatus = latestFailureStatusLabel(messages) || 'Background processing finished.';
    }
    return false;
  }

  async function waitForBackgroundSettle(sessionID, { timeoutMs = 2500, intervalMs = 120 } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) return false;
    const deadline = Date.now() + Math.max(0, Number(timeoutMs || 0));
    while (Date.now() <= deadline) {
      const stillInFlight = await refreshBackgroundStatus(sid, { syncMessages: true }).catch(() => true);
      if (!stillInFlight) return true;
      await new Promise((resolve) => setTimeout(resolve, Math.max(50, Number(intervalMs || 120))));
    }
    return false;
  }

  function stopBackgroundMonitor() {
    if (backgroundMonitorTimer) {
      clearInterval(backgroundMonitorTimer);
      backgroundMonitorTimer = null;
    }
  }

  async function startBackgroundMonitor(sessionID, { intervalMs = 3000 } = {}) {
    const sid = String(sessionID || '').trim();
    stopBackgroundMonitor();
    if (!sid) {
      backgroundProcessing = false;
      streamingPending = false;
      return;
    }
    try {
      const inFlight = await refreshBackgroundStatus(sid, { syncMessages: !sending });
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
      }, Math.max(250, Number(intervalMs || 3000)));
    } catch {
    }
  }

  function scrollMessagesToBottom(force = false) {
    if (!messagesViewport) return;
    if (!force && !autoFollowBottom) return;
    scrollViewportToBottom(messagesViewport, { updatePrimary: true });
  }

  function scrollViewportToBottom(viewport, { updatePrimary = false } = {}) {
    if (!viewport) return;
    viewport.scrollTop = viewport.scrollHeight;
    if (updatePrimary) {
      lastMessagesScrollTop = viewport.scrollTop;
      refreshScrollAffordance();
    }
    if (typeof window !== 'undefined') {
      window.requestAnimationFrame(() => {
        if (!viewport) return;
        viewport.scrollTop = viewport.scrollHeight;
        if (updatePrimary) {
          lastMessagesScrollTop = viewport.scrollTop;
          refreshScrollAffordance();
        }
      });
      setTimeout(() => {
        if (!viewport) return;
        viewport.scrollTop = viewport.scrollHeight;
        if (updatePrimary) {
          lastMessagesScrollTop = viewport.scrollTop;
          refreshScrollAffordance();
        }
      }, 60);
    }
    if (updatePrimary) {
      refreshScrollAffordance();
    }
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
      await openSessionByID(created.id, { fallbackSession: created });
    }
    viewStatus = 'New session created.';
    return created;
  }

  function openNewSessionModal() {
    newSessionPath = selectedWorkDir || '~/';
    showNewSessionModal = true;
    loadPathSuggestions(newSessionPath).catch(() => {});
  }

  function closeNewSessionModal() {
    if (sending || creatingSessionFlow) return;
    showNewSessionModal = false;
  }

  async function openSessionByID(sessionID, { fallbackSession = null } = {}) {
    const sid = String(sessionID || '').trim();
    if (!sid) return;
    closeExecOutputModal();
    closeSubagentDetailModal();
    const previousSessionID = String(activeSessionID || '').trim();
    if (previousSessionID && previousSessionID !== sid) {
      stopBackgroundMonitor();
      messages = [];
      messageLoadSource = 'canonical';
      visibleRenderedMessageLimit = initialMessagesPageSize;
      historyExpandedManually = false;
      hasMoreMessages = false;
      oldestLoadedMessageID = '';
      newestLoadedMessageID = '';
      streamingPending = false;
      backgroundProcessing = false;
      clearCompactSnapshotPersistTimer();
    }
    activeSessionID = sid;
    syncSessionIDToURL(sid);
    const selected = sessions.find((item) => item.id === sid) || fallbackSession;
    if (selected) {
      syncComposerControlsFromSession(selected);
    }
    if (!sending && !backgroundProcessing) {
      viewStatus = 'Loading session...';
    }
    await loadMessages(sid);
    draftMessage = loadSessionDraft(draftStoragePrefix, sid);
    await refreshBackgroundStatus(sid, { syncMessages: false }).catch(() => false);
    void startBackgroundMonitor(sid);
    if (!sending && !backgroundProcessing) {
      const currentSession = sessions.find((item) => item?.id === sid) || selected || fallbackSession || null;
      viewStatus = 'Ready.';
    }
    showSessionDrawer = false;
  }

  async function createSessionFromModal() {
    const path = String(newSessionPath || '').trim() || '~/';
    creatingSessionFlow = true;
    composerError = '';
    try {
      const created = await createSession({ autoOpen: false, workDir: path });
      showNewSessionModal = false;
      await openSessionByID(created?.id, { fallbackSession: created });
    } catch (error) {
      composerError = formatComposerError(String(error?.message || 'Failed to create session.'));
      viewStatus = composerError;
    } finally {
      creatingSessionFlow = false;
    }
  }

  async function ensureSessionOnFirstOpen() {
    initialSessionBootstrap = true;
    try {
      if (readSessionIDFromURL()) {
        viewStatus = 'Loading session...';
      }
      await loadSessions({ autoSelect: true });
      if (sessions.length === 0) {
        activeSessionID = '';
        messages = [];
        draftMessage = '';
        syncSessionIDToURL('');
        stopBackgroundMonitor();
        openNewSessionModal();
        viewStatus = 'Choose how to start your first session.';
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
        syncComposerControlsFromSession(active);
      }
      await openSessionByID(activeSessionID, { fallbackSession: active });
    } finally {
      initialSessionBootstrap = false;
    }
  }

  async function selectSession(sessionID) {
    const sid = String(sessionID || '').trim();
    if (!sid || sid === activeSessionID) return;
    await openSessionByID(sid);
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
      clearSessionDraft(draftStoragePrefix, sid);
      viewStatus = 'Session deleted.';
      await loadSessions({ autoSelect: deletingActive });
      if (sessions.length === 0) {
        activeSessionID = '';
        messages = [];
        draftMessage = '';
        syncSessionIDToURL('');
        stopBackgroundMonitor();
        openNewSessionModal();
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
          draftMessage = loadSessionDraft(draftStoragePrefix, activeSessionID);
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

    const slashMeta = parseSupportedSlashCommand(content, intentMode());
    if (slashMeta.error) {
      viewStatus = slashMeta.error;
      return;
    }
    composerError = '';
    const prepared = prepareMessageContent(content);
    const sentContent = String(slashMeta.contentOverride ?? prepared);
    const pendingID = `pending-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const sendStartedAt = new Date().toISOString();
    const liveAssistantActor = liveAssistantActorForSession(session);
    const pendingMessage = {
      id: pendingID,
      role: 'user',
      content,
      created_at: sendStartedAt,
      pending: true
    };
    messageLoadSource = 'canonical';
    dropPendingAssistantPlaceholders();
    messages = [...messages, pendingMessage];
    ensureStreamAssistantPlaceholder(liveAssistantActor, sendStartedAt);
    autoFollowBottom = true;
    sending = true;
    stopRequested = false;
    forceStopArmed = false;
    expectedWSDetach = false;
    backgroundProcessing = false;
    await tick();
    scrollMessagesToBottom(true);

    streamingPending = true;
    composerLockedUntilAssistant = true;
    draftMessage = '';
    viewStatus = 'Streaming...';
    let monitorAfterSend = false;
    try {
      let donePayload = null;
      donePayload = await streamChatViaWebSocket({
        session_id: session.id,
        content: sentContent,
        model: selectedModel,
        reasoning_level: normalizeReasoningLevel(selectedReasoningLevel),
        work_dir: selectedWorkDir || '~/',
        sandbox_mode: selectedSandboxMode || 'write',
        command: 'chat',
        session_intent: intentMode(),
        last_seen_event_seq: Number(session?.last_applied_event_seq || 0)
      }, (evt) => {
        const eventType = String(evt?.event || evt?.event_type || '').trim().toLowerCase();
          if (eventType === 'session.snapshot') {
            scheduleCompactSnapshotPersist({
              messages: Array.isArray(evt?.messages) ? evt.messages : Array.isArray(evt?.payload?.messages) ? evt.payload.messages : [],
              laneProjections: evt?.lane_projections || evt?.payload?.lane_projections || null,
              pendingID,
              sentContent: sentContent.trim()
            });
            return;
          }
          if (eventType === 'session.stream') {
            appendStreamEventMessage(evt);
            return;
          }
          if (eventType === 'session.error') return;
        });

      if (!donePayload) {
        throw new Error('Streaming ended before completion.');
      }
      flushCompactSnapshotQueue();

      const userMessage = donePayload?.user;
      streamingPending = false;
      if (userMessage) {
        mergePendingUserMessage(pendingID, userMessage);
      } else {
        messages = messages.filter((item) => item?.id !== pendingID);
      }
      mergeDoneAssistantRows(donePayload);
      messageLoadSource = 'canonical';
      clearSessionDraft(draftStoragePrefix, session.id);
      clearCompactSnapshotPersistTimer();
      composerLockedUntilAssistant = false;

      if (donePayload?.session?.id) {
        const updated = donePayload.session;
        sessions = sessions.map((item) => (item.id === updated.id ? updated : item));
        sessions = [...sessions].sort((a, b) => String(b.last_message_at || '').localeCompare(String(a.last_message_at || '')));
        syncComposerControlsFromSession(updated, { preserveMode: true });
      }
      await loadMessages(session.id, { silent: true, preserveViewport: false, preserveLoadedHistory: historyExpandedManually });
      if (hasPendingAssistantPlaceholder(liveAssistantActor) || !hasVisibleOutcomeAfterLatestUser(messages)) {
        await waitForSettledVisibleOutcome(session.id, { actor: liveAssistantActor });
      } else if (!hasSettledAssistantSince(sendStartedAt, liveAssistantActor)) {
        await new Promise((resolve) => setTimeout(resolve, 250));
        await loadMessages(session.id, { silent: true, preserveViewport: false, preserveLoadedHistory: historyExpandedManually });
      }
      await tick();
      scrollMessagesToBottom();
      viewStatus = completedViewStatus(messages, { messageActor });
      composerError = '';
    } catch (error) {
      flushCompactSnapshotQueue();
      clearCompactSnapshotPersistTimer();
      const busy = String(error?.message || '').toLowerCase().includes('already processing');
      const failReason = formatComposerError(String(error?.message || 'Failed to send message.'));
      const detachedBackground = failReason.toLowerCase().includes('websocket_detached_background');
      const stopDrivenDetach = detachedBackground && (stopRequested || expectedWSDetach);
      if (detachedBackground) {
        expectedWSDetach = false;
      }
      if (!busy && !detachedBackground) {
        messages = messages.map((item) =>
          item?.id === pendingID
            ? {
                ...item,
                pending: false,
                failed: true
              }
            : item
        );
      }
      dropPendingAssistantPlaceholders();
      const aborted =
        String(error?.name || '').trim() === 'AbortError' ||
        String(error?.message || '').toLowerCase().includes('aborted');
      if (busy || detachedBackground) {
        composerError = '';
        const inFlight = await refreshBackgroundStatus(session.id, { syncMessages: true }).catch(() => false);
        if (inFlight) {
          viewStatus = stopDrivenDetach ? (forceStopArmed ? 'Force stopping...' : 'Stopping...') : 'Streaming...';
          monitorAfterSend = true;
        } else if (stopDrivenDetach) {
          composerLockedUntilAssistant = false;
          composerError = '';
          viewStatus = 'Stopped.';
          backgroundProcessing = false;
        } else {
          composerLockedUntilAssistant = false;
          composerError = aborted ? '' : failReason;
          viewStatus = aborted ? (stopRequested ? 'Stopped.' : 'Streaming canceled.') : failReason;
          backgroundProcessing = false;
        }
      } else if (stopRequested) {
        composerLockedUntilAssistant = false;
        composerError = '';
        viewStatus = 'Stopped.';
        backgroundProcessing = false;
      } else {
        composerLockedUntilAssistant = false;
        composerError = aborted ? '' : failReason;
        viewStatus = aborted ? (stopRequested ? 'Stopped.' : 'Streaming canceled.') : failReason;
        backgroundProcessing = false;
      }
      streamingPending = false;
    } finally {
      sending = false;
      if (streamingPending) streamingPending = false;
      if (!stopRequested && monitorAfterSend) {
        startBackgroundMonitor(session.id);
      } else {
        backgroundProcessing = false;
      }
      stopRequested = false;
      forceStopArmed = false;
    }
  }

  async function requestStop(sessionID, force) {
    try {
      return await req('/api/coding/stop', {
        method: 'POST',
        body: JSON.stringify({
          session_id: sessionID,
          force
        })
      });
    } catch {
      return null;
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
      const stopResponse = await requestStop(session.id, force);
      if (stopResponse && stopResponse.stopped === false) {
        stopRequested = false;
        forceStopArmed = false;
        expectedWSDetach = false;
        viewStatus = 'No active run to stop.';
        return;
      }
    }
    try {
      if (wsStreamSocket) {
        expectedWSDetach = true;
        wsStreamSocket.close();
      }
    } catch {
    }
    if (session?.id) {
      viewStatus = 'Stopping...';
      const settled = await waitForBackgroundSettle(session.id, {
        timeoutMs: force ? 3000 : 1800,
        intervalMs: 120
      }).catch(() => false);
      if (settled) {
        sending = false;
        streamingPending = false;
        backgroundProcessing = false;
        composerLockedUntilAssistant = false;
        forceStopArmed = false;
        stopRequested = false;
        expectedWSDetach = false;
        return;
      }
      startBackgroundMonitor(session.id, { intervalMs: 400 });
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

  function activeStreamingLabel() {
    return 'Streaming';
  }

  async function sendWSControlCommand(payload, { waitFor = [], laneHint = '' } = {}) {
    const sid = String(payload?.session_id || '').trim();
    if (!sid) throw new Error('session_id is required');
    const requestID = String(payload?.request_id || nextWSRequestID(String(payload?.type || 'ws'))).trim();
    const candidates = buildWSURLCandidates('/api/coding/ws');
    if (candidates.length === 0) {
      throw new Error('WebSocket endpoint unavailable.');
    }
    const expected = new Set((Array.isArray(waitFor) ? waitFor : []).map((item) => String(item || '').trim().toLowerCase()).filter(Boolean));
    return new Promise((resolve, reject) => {
      let attemptIndex = 0;
      let settled = false;
      let ws = null;
      const finish = (fn, value) => {
        if (settled) return;
        settled = true;
        try {
          if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) ws.close();
        } catch {
        }
        fn(value);
      };
      const connect = () => {
        if (settled) return;
        if (attemptIndex >= candidates.length) {
          finish(reject, new Error('WebSocket control command failed.'));
          return;
        }
        ws = new WebSocket(candidates[attemptIndex]);
        attemptIndex += 1;
        let didOpen = false;
        ws.onopen = () => {
          didOpen = true;
          try {
            ws.send(JSON.stringify({ ...payload, request_id: requestID }));
          } catch (error) {
            finish(reject, error instanceof Error ? error : new Error('Failed to send control command.'));
          }
        };
        ws.onmessage = (event) => {
          let evt = {};
          try {
            evt = JSON.parse(String(event?.data || '{}'));
          } catch {
            return;
          }
          const eventType = String(evt?.event || evt?.event_type || '').trim().toLowerCase();
          const evtRequestID = String(evt?.request_id || evt?.payload?.request_id || '').trim();
          const evtLane = String(evt?.lane || evt?.payload?.lane || '').trim().toLowerCase();
          if (evtRequestID && evtRequestID !== requestID) return;
          if (laneHint && evtLane && evtLane !== laneHint) return;
          if (eventType === 'session.error') {
            const msg = String(evt?.message || evt?.payload?.message || 'Command failed.');
            finish(reject, new Error(msg));
            return;
          }
          if (eventType === 'session.duplicate_request') {
            finish(resolve, evt);
            return;
          }
          if (expected.size === 0 || expected.has(eventType)) {
            finish(resolve, evt);
          }
        };
        ws.onerror = () => {
          try {
            ws.close();
          } catch {
          }
        };
        ws.onclose = () => {
          if (settled) return;
          if (!didOpen) {
            connect();
            return;
          }
          finish(reject, new Error('WebSocket control command closed before completion.'));
        };
      };
      connect();
    });
  }

  function parseSupportedSlashCommand(input, sessionIntent = 'chat') {
    const raw = String(input || '').trim();
    if (!raw.startsWith('/')) return { contentOverride: null, error: '' };
    const parts = raw.split(/\s+/);
    const cmd = String(parts[0] || '').toLowerCase();
    const arg = raw.slice(parts[0].length).trim();
    const normalizedMode = String(sessionIntent || 'chat').trim().toLowerCase() || 'chat';
    if (cmd === '/status') {
      return { contentOverride: '/status', error: '' };
    }
    if (cmd === '/mcp') {
      return { contentOverride: '/mcp', error: '' };
    }
    return { contentOverride: raw, error: '' };
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
      let runtimeStatusDoneTimer = null;
      const cancelRuntimeStatusDoneTimer = () => {
        if (!runtimeStatusDoneTimer) return;
        clearTimeout(runtimeStatusDoneTimer);
        runtimeStatusDoneTimer = null;
      };
      const requestID = nextWSRequestID('send');
      const expectedSessionID = String(payload?.session_id || '').trim();
      const finish = (fn, value) => {
        if (settled) return;
        settled = true;
        cancelRuntimeStatusDoneTimer();
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
              type: 'session.send',
              request_id: requestID,
              session_id: expectedSessionID,
              content: payload.content,
              model: payload.model,
              reasoning_level: payload.reasoning_level,
              work_dir: payload.work_dir,
              sandbox_mode: payload.sandbox_mode,
              command: 'chat',
              last_seen_event_seq: Number(payload?.last_seen_event_seq || 0)
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
          const eventType = String(evt?.event || evt?.event_type || '').trim().toLowerCase();
          if (runtimeStatusDoneTimer) {
            if (eventType === 'session.stream' || eventType === 'session.snapshot' || eventType === 'session.started') {
              cancelRuntimeStatusDoneTimer();
            }
          }
          if (String(evt?.session_id || '').trim() && String(evt?.session_id || '').trim() !== expectedSessionID) {
            return;
          }
          if (onEvent) onEvent(evt);
          if (eventType === 'session.started') {
            startedAck = true;
            return;
          }
          if (eventType === 'session.done') {
            cancelRuntimeStatusDoneTimer();
            finish(resolve, evt);
            return;
          }
          if (eventType === 'session.error') {
            finish(reject, new Error(String(evt?.message || evt?.payload?.message || 'Streaming failed.')));
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
    if (draftPersistTimer) {
      clearTimeout(draftPersistTimer);
    }
    draftPersistTimer = setTimeout(() => {
      saveSessionDraft(draftStoragePrefix, sid, draftMessage);
      draftPersistTimer = null;
    }, 300);
    return () => {
      if (draftPersistTimer) {
        clearTimeout(draftPersistTimer);
        draftPersistTimer = null;
      }
    };
  });

  $effect(() => {
    renderedMessages = renderedMessagesForView();
  });

  $effect(() => {
    const nextVisibleCount = Math.min(
      renderedMessages.length,
      Math.max(initialMessagesPageSize, visibleRenderedMessageLimit)
    );
    visibleRenderedMessages = renderedMessages.slice(-nextVisibleCount);
  });

  $effect(() => {
    const sid = String(activeSessionID || '').trim();
    if (!sid) return;
    if (!wsHealthReady) return;
    wsHealthKeepAlive = true;
    connectWSHealthSocket();
  });

  onDestroy(() => {
    clearCompactSnapshotPersistTimer();
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
  <CodingTopbar
    wsHealthStatus={wsHealthStatus}
    selectedWorkDir={selectedWorkDir}
    bind:selectedModel
    bind:selectedReasoningLevel
    {models}
    {reasoningLevels}
    {loadingSessions}
    {sending}
    {backgroundProcessing}
    {deleting}
    {activeSessionID}
    onBack={backToDashboard}
    onOpenSessions={() => (showSessionDrawer = true)}
    onModelChange={queuePersistSessionPreferences}
    onReasoningChange={onReasoningLevelChange}
    onNewSession={openNewSessionModal}
    onDeleteSession={deleteActiveSession}
  />

  <div class="coding-layout">
    <section class="coding-chat-area full" aria-label="Coding chat">
      {#if !activeSessionID}
      {#if initialSessionBootstrap || loadingSessions}
          <div class="empty-state">Loading chat...</div>
        {:else}
        <div class="empty-state">Open a session to start.</div>
        {/if}
      {:else}
        <CodingMessagesPane
          canLoadMoreChat={canLoadMoreChat(renderedMessages, visibleRenderedMessageLimit, hasMoreMessages)}
          {loadingOlderMessages}
          {loadingMessages}
          {sending}
          {backgroundProcessing}
          {streamingPending}
          streamingLabel={activeStreamingLabel()}
          bind:messagesViewport
          {visibleRenderedMessages}
          {showScrollBottomButton}
          {shouldHideRenderedMessage}
          {isInternalRunnerActivity}
          {isMessageExpanded}
          {messageRoleClass}
          {messageRoleLabel}
          {messageDisplayContent}
          {parsePlanningFinalPlan}
          {messagePreviewContent}
          {messageShowsAssistantUsage}
          {assistantUsageSummary}
          {shouldCollapseContent}
          {execStatusLabel}
          {subagentStatusLabel}
          {subagentDisplayTitle}
          {subagentPreview}
          {normalizeExecCommandForDisplay}
          {formatWhen}
          {fileOperationDisplayParts}
          {fileOpTone}
          activeLiveMessageID={activeLiveBubbleMessageID()}
          onScroll={onMessagesScroll}
          onLoadOlder={loadOlderMessages}
          onJumpToLatest={jumpToLatestMessages}
          onToggleExpand={toggleMessageExpanded}
          onOpenExec={openExecOutputModal}
          onOpenSubagent={openSubagentDetailModal}
        />

        <CodingComposer
          bind:draftMessage
          bind:composerError
          {sending}
          {backgroundProcessing}
          {selectedSandboxMode}
          stopLabel={stopButtonLabel}
          {composerLockedUntilAssistant}
          onKeydown={onComposerKeydown}
          onOpenSkillModal={openSkillModal}
          onToggleSandboxMode={toggleSandboxMode}
          onSend={sendMessage}
          onCancel={cancelStreaming}
        />
      {/if}
    </section>
  </div>
  <CodingStatusLine
    viewStatus={effectiveViewStatus}
    {persistingSessionPrefs}
  />
</section>

{#if showExecOutputModal && selectedExecEntry}
  <CodingExecOutputModal entry={selectedExecEntry} onClose={closeExecOutputModal} />
{/if}

{#if showSubagentDetailModal && selectedSubagentEntry}
  <CodingSubagentDetailModal entry={selectedSubagentEntry} onClose={closeSubagentDetailModal} />
{/if}

{#if showNewSessionModal}
  <CodingNewSessionModal
    {sending}
    {creatingSessionFlow}
    bind:newSessionPath
    {pathSuggestions}
    {loadingPathSuggestions}
    onClose={closeNewSessionModal}
    onPathInput={schedulePathSuggestions}
    onPathFocus={schedulePathSuggestions}
    onRefreshSuggestions={loadPathSuggestions}
    onCreate={createSessionFromModal}
  />
{/if}


{#if showSessionDrawer}
  <CodingSessionDrawer
    {loadingSessions}
    {sessions}
    {activeSessionID}
    {deleting}
    {sending}
    sessionDisplayID={sessionDisplayID}
    onClose={() => (showSessionDrawer = false)}
    onSelect={selectSession}
    onDelete={(sessionID) => deleteSessionByID(sessionID, { fromDrawer: true }).catch(() => {})}
  />
{/if}

{#if showSkillModal}
  <CodingSkillModal
    {skillSearchQuery}
    {loadingSkills}
    skills={filteredSkills()}
    onClose={closeSkillModal}
    onSearch={(value) => (skillSearchQuery = value)}
    onInsert={insertSkillToken}
  />
{/if}
