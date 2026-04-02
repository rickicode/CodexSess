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
  formatSubagentFallbackName,
  inferSubagentRoleFromText,
  isFallbackSubagentName,
  normalizeActivityCommandKey,
  normalizeExecIdentityCommandKey,
  normalizeExecStatus,
  resolveLegacyRuntimeOwnership,
  normalizeSubagentRole,
  normalizeSubagentPromptKey,
  normalizeSubagentToolFamily,
  parseMCPActivityPayload,
  parseToolArguments,
  parseActivityText,
  parseFileOperationPayload,
  parseFileOperationText
} from '../../lib/coding/activityParsing.js'
import {
  subagentDisplayName,
  subagentDisplayRole
} from './messageView.js'

function parseSubagentActivityText(rawText) {
  const text = String(rawText || '').trim();
  if (!text) return null;
  if (!text.startsWith('•')) {
    const lower = text.toLowerCase();
    const timelineWaiting = lower.includes('timeline event: `waiting`') || lower.includes('timeline event: "waiting"');
    const timelineCompleted = lower.includes('timeline event: `completed`') || lower.includes('timeline event: "completed"');
    const finished = lower.includes('the subagent finished');
    if (!timelineWaiting && !timelineCompleted && !finished) {
      return null;
    }
    const inferredIdentity = extractSubagentIdentityFromText(text);
    const ids = [...new Set(Array.isArray(inferredIdentity?.ids) ? inferredIdentity.ids : [])];
    const nickname = String(inferredIdentity?.nickname || '').trim();
    const agentType = normalizeSubagentRole(String(inferredIdentity?.agentType || '').trim());
    const targetID = ids[0] || '';
    const summary = cleanSubagentDetailText(text, ids);
    const key = buildSubagentMergeKey('wait_agent', {
      ids,
      targetID,
      nickname,
      summary
    });
    if (timelineWaiting) {
      const title = nickname
        ? `Waiting ${nickname}${agentType ? ` [${agentType}]` : ''}`
        : (targetID ? `Waiting ${targetID}` : 'Waiting for agents');
      return {
        key,
        toolName: 'wait_agent',
        status: 'running',
        phase: 'started',
        title,
        nickname,
        agentType,
        targetID,
        ids,
        prompt: '',
        summary,
        raw: { text }
      };
    }
    return {
      key,
      toolName: 'wait_agent',
      status: 'done',
      phase: 'completed',
      title: 'Subagent wait completed',
      nickname,
      agentType,
      targetID,
      ids,
      prompt: '',
      summary,
      raw: { text }
    };
  }
  const lines = text.split('\n').map((line) => String(line || '').trim()).filter(Boolean);
  if (lines.length === 0) return null;
  const head = lines[0];
  const detailLineRaw = lines.slice(1).join('\n').replace(/^└\s*/i, '').trim();
  const idMatches = [...new Set([...extractLikelyAgentIDs(detailLineRaw), ...extractLikelyAgentIDs(head)])];
  const detailLine = cleanSubagentDetailText(detailLineRaw, idMatches);

  if (/^•\s*spawned\s+/i.test(head)) {
    const m = head.match(/^•\s*spawned\s+(.+?)(?:\s+\[(.+)\])?(?:\s+\((.+?)\))?$/i);
    const nickname = String(m?.[1] || '').trim().replace(/subagent$/i, '').trim();
    const role = normalizeSubagentRole(String(m?.[2] || '').trim());
    const runtime = String(m?.[3] || '').trim();
    const runtimeParts = runtime ? runtime.split(/\s+/).filter(Boolean) : [];
    const reasoning = runtimeParts.length > 1 ? runtimeParts[runtimeParts.length - 1] : '';
    const model = runtimeParts.length > 1 ? runtimeParts.slice(0, -1).join(' ') : runtime;
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
      model,
      reasoning,
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
    const payload = JSON.parse(text);
    const method = String(payload?.method || '').trim();
    const params = payload?.params && typeof payload.params === 'object' ? payload.params : null;
    if (method && params) {
      return {
        type: method,
        ...params
      };
    }
    return payload;
  } catch {
    return null;
  }
}

function parseMCPActivityText(rawText) {
  const text = String(rawText || '').trim();
  if (!text) return null;
  if (!/^mcp\b/i.test(text) && !/^running mcp:/i.test(text)) return null;
  const genericStatus = text.match(/^mcp server status:\s*(.+)$/i);
  const generic = (() => {
    if (!genericStatus) return false;
    const lower = String(genericStatus[1] || '').toLowerCase();
    return lower.includes('"status":"starting"') || lower.includes('"status":"ready"');
  })();
  return {
    text,
    generic
  };
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

function normalizeAppServerItemType(rawType) {
  const value = String(rawType || '').trim().toLowerCase();
  switch (value) {
    case 'agentmessage':
    case 'agent_message':
      return 'agentmessage';
    case 'usermessage':
    case 'user_message':
      return 'usermessage';
    case 'commandexecution':
    case 'command_execution':
      return 'command_execution';
    case 'filechange':
    case 'file_change':
      return 'file_change';
    case 'fileread':
    case 'file_read':
      return 'file_read';
    case 'functioncall':
    case 'function_call':
      return 'function_call';
    case 'functioncalloutput':
    case 'function_call_output':
      return 'function_call_output';
    default:
      return value;
  }
}

function normalizeAppServerEventType(rawType) {
  return String(rawType || '').trim().toLowerCase().replaceAll('/', '.');
}

function shouldIgnoreRawEventPayload(payload) {
  const evt = payload && typeof payload === 'object' ? payload : null;
  if (!evt) return false;
  return normalizeAppServerEventType(evt?.type) === 'thread.started';
}

function parseExecEventFromPayload(payload) {
  const evt = payload && typeof payload === 'object' ? payload : null;
  if (!evt) return null;
  const type = normalizeAppServerEventType(evt?.type);
  const item = evt?.item && typeof evt.item === 'object' ? evt.item : {};
  const itemType = normalizeAppServerItemType(item?.type);
  const fn = item?.function && typeof item.function === 'object' ? item.function : {};
  const toolName = String(item?.tool || item?.tool_name || item?.name || fn?.name || '').trim().toLowerCase();
  if (itemType === 'collab_tool_call') return null;
  if (!type.startsWith('item.') && !type.startsWith('tool.') && type !== 'rawresponseitem.completed') {
    return null;
  }
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
    ((itemType === 'function_call' || itemType === 'tool_call') && toolName === 'exec_command');
  if (!isExplicitExec) return null;
  if (!command) return null;
  let status = '';
  let exitCode = 0;
  if (type === 'item.started' || type === 'item.updated' || type === 'tool.started' || type === 'tool.call.started') {
    status = 'running';
  } else if (type === 'item.completed' || type === 'rawresponseitem.completed' || type === 'tool.completed' || type === 'tool.call.completed') {
    exitCode = Number(item?.exit_code ?? item?.exitCode ?? evt?.exit_code ?? evt?.exitCode ?? 0) || 0;
    status = exitCode !== 0 ? 'failed' : 'done';
  }
  const output = extractExecOutputText(
    item?.aggregated_output ||
    item?.aggregatedOutput ||
    item?.output ||
    item?.result ||
    evt?.aggregated_output ||
    evt?.aggregatedOutput ||
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
  const type = normalizeAppServerEventType(evt?.type);
  const firstText = (...values) => {
    for (const value of values) {
      if (value == null) continue;
      const text = String(value || '').trim();
      if (text) return text;
    }
    return '';
  };
  if (type === 'thread.started') {
    const thread = evt?.thread && typeof evt.thread === 'object' ? evt.thread : {};
    const targetID = firstText(thread?.id, evt?.thread_id);
    let nickname = firstText(
      thread?.agentNickname,
      thread?.agent_nickname,
      thread?.nickname,
      thread?.name,
      thread?.displayName,
      thread?.display_name
    );
    let agentType = normalizeSubagentRole(firstText(
      thread?.agentRole,
      thread?.agent_role,
      thread?.role,
      thread?.agentType,
      thread?.agent_type,
      thread?.type
    ));
    if (!nickname && !agentType) {
      return null;
    }
    const ids = [...new Set([
      ...extractLikelyAgentIDs(targetID),
      ...extractLikelyAgentIDs(firstText(thread?.id))
    ])];
    if (!nickname) {
      nickname = formatSubagentFallbackName(ids, targetID);
    }
    if (!agentType) {
      agentType = 'subagent';
    }
    const key = buildSubagentMergeKey('spawn_agent', {
      ids,
      targetID,
      nickname,
      prompt: '',
      summary: ''
    });
    return {
      key,
      callID: '',
      toolName: 'spawn_agent',
      status: 'running',
      phase: 'started',
      title: nickname ? `Spawned ${nickname}${agentType ? ` [${agentType}]` : ''}` : 'Spawned subagent',
      nickname,
      agentType,
      targetID,
      ids,
      prompt: '',
      summary: '',
      raw: evt
    };
  }
  const item = evt?.item && typeof evt.item === 'object' ? evt.item : {};
  const itemType = normalizeAppServerItemType(item?.type);
  if (!itemType || (itemType !== 'tool_call' && itemType !== 'function_call' && itemType !== 'collab_tool_call')) {
    return null;
  }
  if (!type.startsWith('item.') && !type.startsWith('tool.') && type !== 'rawresponseitem.completed') return null;
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
  const model = firstText(
    args?.model,
    args?.agent_model,
    args?.agentModel,
    argAgent?.model,
    argAgent?.agent_model,
    itemAgent?.model,
    itemAgent?.agent_model,
    findFirstTextByKeys([item?.output, item?.result, item?.response, item?.agents_states], ['model', 'agent_model'])
  );
  const reasoning = firstText(
    args?.reasoning_effort,
    args?.reasoning,
    args?.reasoning_level,
    args?.agent_reasoning,
    argAgent?.reasoning_effort,
    argAgent?.reasoning,
    itemAgent?.reasoning_effort,
    itemAgent?.reasoning,
    findFirstTextByKeys([item?.output, item?.result, item?.response, item?.agents_states], ['reasoning_effort', 'reasoning', 'reasoning_level'])
  );
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
    title = isCompleted
      ? 'Subagent wait completed'
      : (nickname
        ? `Waiting ${nickname}${agentType ? ` [${agentType}]` : ''}`
        : (idsWithHints.length === 1 ? `Waiting ${idsWithHints[0]}` : (idsWithHints.length > 1 ? `Waiting ${idsWithHints.length} agents` : 'Waiting for agents')));
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
    model,
    reasoning,
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
  const latestExecByKey = new Map();
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
  const isGenericMCPActivityText = (value) => /^running mcp call$/i.test(String(value || '').trim());

  const upsertExecEntry = (entry) => {
    const command = String(entry?.command || '').trim();
    if (!command) return null;
    const ownership = resolveLegacyRuntimeOwnership(entry?.actor, entry?.lane);
    const actor = ownership.actor;
    const lane = ownership.lane;
    const key = normalizeExecIdentityCommandKey(command);
    if (!key) return null;
    const nowISO = new Date().toISOString();
    const nextStatus = normalizeExecStatus(entry?.status || 'running');
    const nextExitCode = Number(entry?.exitCode || 0) || 0;
    const nextOutput = String(entry?.output || '').trim();
    const nextCreatedAt = String(entry?.createdAt || nowISO);
    const nextCreatedAtMs = Date.parse(nextCreatedAt);
    const indexedActive = activeExecByKey.get(key);
    const indexedExisting = typeof indexedActive === 'number' ? indexedActive : undefined;
    if (typeof indexedExisting === 'number' && out[indexedExisting]) {
      const prev = out[indexedExisting];
      const prevStatus = normalizeExecStatus(prev?.exec_status);
      const prevOutput = String(prev?.exec_output || '').trim();
      const prevExitCode = Number(prev?.exec_exit_code || 0) || 0;
      const canUpdateExisting =
        prevStatus === 'running' ||
        (prevStatus === nextStatus && prevExitCode === nextExitCode && nextStatus !== 'running');
      if (canUpdateExisting) {
        out[indexedExisting] = {
          ...prev,
          ...(actor ? { actor } : {}),
          ...(lane ? { lane } : {}),
          updated_at: nextCreatedAt,
          exec_status: nextStatus,
          exec_exit_code: nextExitCode,
          exec_output: mergeExecOutput(prevOutput, nextOutput),
          exec_output_source: mergeExecOutputSource(prev?.exec_output_source, entry?.source || 'live')
        };
        latestExecByKey.set(key, indexedExisting);
        if (nextStatus === 'running') {
          activeExecByKey.set(key, indexedExisting);
          lastExecKey = command;
        } else {
          activeExecByKey.delete(key);
          if (normalizeActivityCommandKey(lastExecKey) === key) {
            lastExecKey = '';
          }
        }
        return out[indexedExisting];
      }
    }
    if (entry?.source === 'live' && nextStatus !== 'running') {
      for (let i = out.length - 1; i >= 0 && i >= out.length - 8; i -= 1) {
        const prev = out[i];
        if (String(prev?.role || '').trim().toLowerCase() !== 'exec') continue;
        if (normalizeExecIdentityCommandKey(prev?.exec_command || prev?.content || '') !== key) continue;
        if (normalizeExecStatus(prev?.exec_status) !== nextStatus) continue;
        if ((Number(prev?.exec_exit_code || 0) || 0) !== nextExitCode) continue;
        const prevOutput = String(prev?.exec_output || '').trim();
        const prevMs = Date.parse(String(prev?.updated_at || prev?.created_at || ''));
        const windowOk = !Number.isNaN(nextCreatedAtMs) && !Number.isNaN(prevMs) && Math.abs(nextCreatedAtMs - prevMs) <= 1800;
        const outputOk =
          prevOutput === nextOutput ||
          prevOutput === '' ||
          nextOutput === '' ||
          prevOutput.includes(nextOutput) ||
          nextOutput.includes(prevOutput);
        if (!windowOk || !outputOk) continue;
        out[i] = {
          ...prev,
          ...(actor ? { actor } : {}),
          ...(lane ? { lane } : {}),
          updated_at: nextCreatedAt,
          exec_status: nextStatus,
          exec_exit_code: nextExitCode,
          exec_output: mergeExecOutput(prevOutput, nextOutput),
          exec_output_source: mergeExecOutputSource(prev?.exec_output_source, entry?.source || 'live')
        };
        latestExecByKey.set(key, i);
        activeExecByKey.delete(key);
        if (normalizeActivityCommandKey(lastExecKey) === key) {
          lastExecKey = '';
        }
        return out[i];
      }
    }
    for (let i = out.length - 1; i >= 0 && i >= out.length - 8; i -= 1) {
      const prev = out[i];
      if (String(prev?.role || '').trim().toLowerCase() !== 'exec') continue;
      if (normalizeExecIdentityCommandKey(prev?.exec_command || prev?.content || '') !== key) continue;
      if (normalizeExecStatus(prev?.exec_status) !== nextStatus) continue;
      if ((Number(prev?.exec_exit_code || 0) || 0) !== nextExitCode) continue;
      const prevOutput = String(prev?.exec_output || '').trim();
      const outputCompatible =
        prevOutput === nextOutput ||
        prevOutput === '' ||
        nextOutput === '' ||
        prevOutput.includes(nextOutput) ||
        nextOutput.includes(prevOutput);
      if (!outputCompatible) continue;
      const prevMs = Date.parse(String(prev?.updated_at || prev?.created_at || ''));
      if (!Number.isNaN(nextCreatedAtMs) && !Number.isNaN(prevMs) && Math.abs(nextCreatedAtMs - prevMs) > 1800) continue;
      return prev;
    }
    const next = {
      id: `exec-${String(entry?.sourceID || key)}`,
      role: 'exec',
      actor,
      lane,
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
    latestExecByKey.set(key, out.length - 1);
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
    const ownership = resolveLegacyRuntimeOwnership(entry?.actor, entry?.lane);
    const actor = ownership.actor;
    const lane = ownership.lane;
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
        ...(actor ? { actor } : {}),
        ...(lane ? { lane } : {}),
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
      actor,
      lane,
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
    if (role === 'exec') {
      const command = String(item?.exec_command || item?.content || '').trim();
      if (command) {
        upsertExecEntry({
          command,
          actor: item?.actor,
          lane: item?.lane,
          status: item?.exec_status || 'done',
          exitCode: item?.exec_exit_code,
          output: item?.exec_output || '',
          sourceID: item?.id,
          createdAt: item?.created_at,
          source: 'persisted'
        });
        continue;
      }
      out.push(item);
      continue;
    }
    if (role === 'activity') {
      if (isGenericMCPActivityText(item?.content || '')) {
        out.push({
          ...item,
          mcp_activity_generic: true
        });
        continue;
      }
      const subagentActivity = parseSubagentActivityText(item?.content || '');
      if (subagentActivity?.toolName) {
        upsertSubagentEntry({
          ...subagentActivity,
          actor: item?.actor,
          lane: item?.lane,
          sourceID: item?.id,
          createdAt: item?.created_at
        });
        continue;
      }
      const parsed = parseActivityText(item?.content || '');
      if (parsed.kind === 'running' && parsed.command) {
        upsertExecEntry({
          command: parsed.command,
          actor: item?.actor,
          lane: item?.lane,
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
          actor: item?.actor,
          lane: item?.lane,
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
      const parsedMCP = parseMCPActivityPayload(payload);
      const rawText = String(item?.content || '').trim();
      const fileOpText = parseFileOperationPayload(payload) || parseFileOperationText(rawText);
      let handled = false;
      if (parsedExec?.command) {
        const recordExec = upsertExecEntry({
          ...parsedExec,
          actor: item?.actor,
          lane: item?.lane,
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
          actor: item?.actor,
          lane: item?.lane,
          sourceID: item?.id,
          createdAt: item?.created_at
        });
        void recordSubagent;
        handled = true;
      }
      if (parsedMCP?.text) {
        const ownership = resolveLegacyRuntimeOwnership(item?.actor, item?.lane);
        out.push({
          id: `mcp-${String(item?.id || Date.now())}`,
          role: 'activity',
          actor: ownership.actor,
          lane: ownership.lane,
          content: parsedMCP.text,
          mcp_activity: true,
          mcp_activity_target: String(parsedMCP.target || '').trim(),
          mcp_activity_server: String(parsedMCP.server || '').trim(),
          mcp_activity_tool: String(parsedMCP.tool || '').trim(),
          created_at: String(item?.created_at || new Date().toISOString()),
          updated_at: String(item?.created_at || new Date().toISOString()),
          pending: false
        });
        handled = true;
      }
      if (!handled && rawText) {
        const activeKey = normalizeActivityCommandKey(lastExecKey);
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
        const ownership = resolveLegacyRuntimeOwnership(item?.actor, item?.lane);
        out.push({
          id: `fileop-${String(item?.id || Date.now())}`,
          role: 'activity',
          actor: ownership.actor,
          lane: ownership.lane,
          content: fileOpText,
          file_op: fileOpText,
          created_at: String(item?.created_at || new Date().toISOString()),
          updated_at: String(item?.created_at || new Date().toISOString()),
          pending: false
        });
        if (!includeRawEvents) {
          continue;
        }
      }
      if (!handled && shouldIgnoreRawEventPayload(payload)) {
        continue;
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
        const activeKey = normalizeActivityCommandKey(lastExecKey);
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
      if (row?.mcp_activity_generic) {
        const rowCreatedAt = Date.parse(String(row?.created_at || row?.updated_at || ''));
        const hasDetailedNeighbor = out.some((candidate) => {
          if (!candidate || candidate === row) return false;
          if (!candidate?.mcp_activity) return false;
          const candidateCreatedAt = Date.parse(String(candidate?.created_at || candidate?.updated_at || ''));
          if (Number.isNaN(rowCreatedAt) || Number.isNaN(candidateCreatedAt)) return true;
          return Math.abs(candidateCreatedAt - rowCreatedAt) <= 2500;
        });
        if (hasDetailedNeighbor) return false;
      }
      if (String(row?.role || '').trim().toLowerCase() !== 'subagent') return true;
      const tool = String(row?.subagent_tool || '').trim().toLowerCase();
      const status = String(row?.subagent_status || '').trim().toLowerCase();
      const isRunning = status === 'running';
      const title = String(row?.subagent_title || row?.content || '').trim().toLowerCase();
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


export {
  buildExecAwareMessages,
  parseExecEventFromPayload,
  parseMCPActivityText,
  parseRawEventPayload,
  parseSubagentActivityText,
  parseSubagentEventFromPayload
}
