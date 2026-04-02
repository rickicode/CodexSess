import {
  choosePreferredSubagentName,
  choosePreferredSubagentRole,
  extractLikelyAgentIDs,
  fileOperationDedupKey,
  fileOperationDisplayParts,
  formatSubagentFallbackName,
  isFallbackSubagentName,
  normalizeActivityCommandKey,
  normalizeExecIdentityCommandKey,
  normalizeExecOutputSource,
  normalizeExecStatus,
  normalizeLegacyRuntimeActor,
  normalizeSubagentPromptKey,
  normalizedFileOperationLabel,
  parseToolArguments,
  parseRuntimeRecoveryActivity,
  parseActivityText,
} from "../../lib/coding/activityParsing.js";
import { sortMessagesChronologically } from "../../lib/coding/messageMerge.js";
import { isExecutionReadyPlan, parsePlanningReviewPlan } from "./planningReview.js";

function completedViewStatus(inputMessages, { messageActor }) {
  const src = Array.isArray(inputMessages) ? inputMessages : [];
  for (let idx = src.length - 1; idx >= 0; idx -= 1) {
    const message = src[idx];
    const actor = messageActor(message);
    if (
      (actor === "executor" ||
        Boolean(message?.internal_runner) ||
        (String(message?.role || "").trim().toLowerCase() === "activity" &&
          String(message?.actor || "").trim().toLowerCase() === "executor")) &&
      !message?.pending
    ) {
      return "Executor finished.";
    }
    if (
      String(message?.role || "")
        .trim()
        .toLowerCase() === "stderr"
    ) {
      return "Run failed.";
    }
  }
  return "Response received.";
}

function currentRecoveryStatus(inputMessages) {
  const src = Array.isArray(inputMessages) ? inputMessages : [];
  for (let idx = src.length - 1; idx >= 0; idx -= 1) {
    const recovery = runtimeRecoveryActivity(src[idx]);
    const kind = String(recovery?.kind || "").trim().toLowerCase();
    const reason = String(recovery?.reason || "").trim().toLowerCase();
    if (!kind) continue;
    switch (kind) {
      case "recovery_detected":
        if (reason === "usage_limit") return "Retrying after usage limit...";
        if (reason === "auth_failure") return "Retrying after auth failure...";
        return "Retrying...";
      case "account_switch_started":
      case "account_switch_completed":
        return "Retrying with another account...";
      case "auth_sync_started":
      case "auth_sync_completed":
        return "Syncing auth for retry...";
      case "restart_started":
      case "restart_completed":
        return "Restarting runtime for retry...";
      case "continue_started":
      case "recovery_failed":
        return "";
      default:
        continue;
    }
  }
  return "";
}

function collectLiveMessageIDs(inputMessages) {
  const src = Array.isArray(inputMessages) ? inputMessages : [];
  return src
    .map((item) => {
      const id = String(item?.id || "").trim();
      if (!id) return "";
      const role = String(item?.role || "").trim().toLowerCase();
      if (
        id.startsWith("stream-") ||
        /^pending-\d+-[a-z0-9]+$/i.test(id)
      ) {
        return id;
      }
      if (role === "exec" && String(item?.exec_status || "").trim().toLowerCase() === "running") {
        return id;
      }
      if (role === "subagent" && String(item?.subagent_status || "").trim().toLowerCase() === "running") {
        return id;
      }
      return "";
    })
    .filter(Boolean);
}

function collapseTransientActivityMessages(inputMessages) {
  const src = Array.isArray(inputMessages) ? inputMessages : [];
  let lastGenericActivityIndex = -1;
  for (let idx = 0; idx < src.length; idx += 1) {
    const item = src[idx];
    if (
      String(item?.role || "")
        .trim()
        .toLowerCase() === "activity" &&
      !item?.file_op &&
      !item?.mcp_activity &&
      !isInternalRunnerActivity(item)
    ) {
      lastGenericActivityIndex = idx;
    }
  }
  if (lastGenericActivityIndex < 0) return src;
  const hasLaterSubstantiveMessage = src
    .slice(lastGenericActivityIndex + 1)
    .some((item) => {
      const role = String(item?.role || "")
        .trim()
        .toLowerCase();
      return role !== "activity" || Boolean(item?.file_op) || Boolean(item?.mcp_activity);
    });
  return src.filter((item, idx) => {
    const isGenericActivity =
      String(item?.role || "")
        .trim()
        .toLowerCase() === "activity" &&
      !item?.file_op &&
      !item?.mcp_activity;
    if (isGenericActivity && isInternalRunnerActivity(item)) return true;
    if (!isGenericActivity) return true;
    if (hasLaterSubstantiveMessage) return false;
    return idx === lastGenericActivityIndex;
  });
}

function isInternalRunnerActivity(message) {
  const role = String(message?.role || "")
    .trim()
    .toLowerCase();
  if (role !== "activity") return false;
  if (message?.file_op || message?.mcp_activity) return false;
  if (Boolean(message?.internal_runner)) return true;
  return Boolean(runtimeRecoveryActivity(message));
}

function execStatusLabel(status, exitCode) {
  const normalized = normalizeExecStatus(status);
  if (normalized === "running") return "Running";
  if (normalized === "failed")
    return `Failed${Number(exitCode || 0) ? ` (exit ${Number(exitCode || 0)})` : ""}`;
  return "Done";
}

function normalizeExecCommandForDisplay(command, maxLength = 180) {
  let text = String(command || "")
    .trim()
    .replace(/\s+/g, " ");
  if (!text) return "-";
  const shellPrefix = text.match(/^\/(?:usr\/bin\/|bin\/)?bash\s+-lc\s+(.+)$/i);
  if (shellPrefix?.[1]) {
    text = String(shellPrefix[1] || "").trim();
  }
  if (
    (text.startsWith('"') && text.endsWith('"')) ||
    (text.startsWith("'") && text.endsWith("'"))
  ) {
    text = text.slice(1, -1).trim();
  }
  if (!text) return "-";
  if (text.length <= maxLength) return text;
  return `${text.slice(0, Math.max(0, maxLength - 3)).trimEnd()}...`;
}

function subagentStatusLabel(status) {
  const normalized = String(status || "")
    .trim()
    .toLowerCase();
  if (normalized === "running") return "Running";
  if (normalized === "failed") return "Failed";
  return "Done";
}

function formatSubagentNameRole(name, role) {
  const safeName = String(name || "").trim();
  const safeRole = String(role || "").trim();
  const compactRole = /^subagent$/i.test(safeRole) ? "" : safeRole;
  if (safeName && compactRole) return `${safeName} [${compactRole}]`;
  if (safeName) return safeName;
  if (compactRole) return compactRole;
  return "";
}

function normalizeSubagentReasoning(value) {
  const text = String(value || "").trim().toLowerCase();
  if (!text) return "";
  if (text === "medium" || text === "med") return "medium";
  if (text === "high") return "high";
  if (text === "low") return "low";
  if (text === "xhigh" || text === "very_high" || text === "very-high") return "xhigh";
  return text;
}

function extractSubagentRuntimeFromRaw(message) {
  const raw = message?.subagent_raw;
  if (!raw || typeof raw !== "object") return { model: "", reasoning: "" };
  const item = raw?.item && typeof raw.item === "object" ? raw.item : {};
  const fn = item?.function && typeof item.function === "object" ? item.function : {};
  const args = parseToolArguments(
    item?.arguments ||
      item?.input ||
      item?.params ||
      item?.payload ||
      fn?.arguments ||
      fn?.input ||
      raw?.arguments ||
      raw?.input ||
      raw?.params ||
      raw?.payload,
  );
  const agent =
    args?.agent && typeof args.agent === "object" && !Array.isArray(args.agent)
      ? args.agent
      : {};
  const model = String(
    args?.model ||
      args?.model_name ||
      args?.model_id ||
      agent?.model ||
      agent?.model_name ||
      raw?.model ||
      raw?.model_name ||
      raw?.thread?.model ||
      raw?.thread?.model_name ||
      item?.model ||
      item?.model_name ||
      "",
  ).trim();
  const reasoning = normalizeSubagentReasoning(
    args?.reasoning_level ||
      args?.reasoning ||
      agent?.reasoning_level ||
      agent?.reasoning ||
      raw?.reasoning_level ||
      raw?.reasoning ||
      "",
  );
  return { model, reasoning };
}

function subagentRuntimeLabel(message) {
  let model = String(message?.subagent_model || "").trim();
  let reasoning = normalizeSubagentReasoning(message?.subagent_reasoning || "");
  if (!model && !reasoning) {
    const fallback = extractSubagentRuntimeFromRaw(message);
    model = String(fallback?.model || "").trim();
    reasoning = normalizeSubagentReasoning(fallback?.reasoning || "");
  }
  if (model && reasoning) return `${model} ${reasoning}`;
  if (model) return model;
  if (reasoning) return reasoning;
  return "";
}

function subagentDisplayName(message) {
  const explicitName = String(message?.subagent_name || "").trim();
  const rawNickname = String(message?.subagent_nickname || "").trim();
  const nickname =
    !rawNickname ||
    rawNickname === "-" ||
    /^subagent$/i.test(rawNickname) ||
    extractLikelyAgentIDs(rawNickname).length > 0
      ? ""
      : rawNickname;
  const ids = [
    ...(Array.isArray(message?.subagent_ids) ? message.subagent_ids : []),
    ...extractLikelyAgentIDs(String(message?.subagent_target_id || "")),
    ...extractLikelyAgentIDs(String(message?.subagent_prompt || "")),
    ...extractLikelyAgentIDs(String(message?.subagent_summary || "")),
    ...extractLikelyAgentIDs(JSON.stringify(message?.subagent_raw || {})),
  ];
  return String(
    nickname ||
      explicitName ||
      formatSubagentFallbackName(ids, message?.subagent_target_id || ""),
  ).trim();
}

function subagentDisplayRole(message) {
  const tool = String(message?.subagent_tool || "")
    .trim()
    .toLowerCase();
  if (tool && !["spawn_agent", "wait_agent", "wait"].includes(tool)) {
    return "";
  }
  const role = String(message?.subagent_role || "").trim();
  if (role && role !== "-") return role;
  return "";
}

function isGenericSubagentTitle(text) {
  const value = String(text || "")
    .trim()
    .toLowerCase();
  if (!value) return true;
  if (value === "spawned subagent") return true;
  if (value === "subagent activity") return true;
  if (value === "waiting for agents") return true;
  if (value === "subagent wait completed") return true;
  if (/^waiting\s+[0-9a-f-]{20,}$/i.test(value)) return true;
  return false;
}

function subagentDisplayTitle(message) {
  const base = String(
    message?.subagent_title || message?.content || "Subagent Activity",
  ).trim();
  const name = subagentDisplayName(message);
  const role = subagentDisplayRole(message);
  const family = subagentDisplayFamily(message);
  if (/^waiting\b/i.test(base)) {
    const safeName = name || "subagent";
    const runtime = subagentRuntimeLabel(message);
    return `Waiting for ${safeName}${role ? ` [${role}]` : ""}${runtime ? ` (${runtime})` : ""}`;
  }
  if (family === "spawn" && name) {
    const runtime = subagentRuntimeLabel(message);
    return `Spawned ${name}${role ? ` [${role}]` : ""}${runtime ? ` (${runtime})` : ""}`;
  }
  if (family === "wait" && name) {
    const runtime = subagentRuntimeLabel(message);
    return `Waiting for ${name}${role ? ` [${role}]` : ""}${runtime ? ` (${runtime})` : ""}`;
  }
  if (!isGenericSubagentTitle(base)) return base;
  if (name && /^spawned\b/i.test(base)) {
    const runtime = subagentRuntimeLabel(message);
    return `Spawned ${name}${role ? ` [${role}]` : ""}${runtime ? ` (${runtime})` : ""}`;
  }
  if (name && /^waiting\b/i.test(base))
    return `Waiting for ${name}${role ? ` [${role}]` : ""}${subagentRuntimeLabel(message) ? ` (${subagentRuntimeLabel(message)})` : ""}`;
  return base;
}

function subagentDetailFields(message) {
  const name = String(subagentDisplayName(message) || "").trim();
  const agentName = String(message?.subagent_name || "").trim();
  const role = String(subagentDisplayRole(message) || "").trim();
  const title = String(
    subagentDisplayTitle(message) || message?.content || "",
  ).trim();
  const prompt = String(message?.subagent_prompt || "").trim();
  const summary = String(message?.subagent_summary || "").trim();
  const targetRaw = String(message?.subagent_target_id || "").trim();
  const target =
    !targetRaw ||
    /^subagent$/i.test(targetRaw) ||
    targetRaw === name ||
    targetRaw === role
      ? ""
      : targetRaw;
  const normalizedPrompt = normalizeSubagentPromptKey(prompt);
  const normalizedSummary = normalizeSubagentPromptKey(summary);
  const collapsedSummary =
    !summary ||
    summary === title ||
    summary === targetRaw ||
    (normalizedPrompt &&
      normalizedSummary &&
      normalizedPrompt === normalizedSummary)
      ? ""
      : summary;
  return {
    name,
    agentName: agentName && agentName !== name ? agentName : "",
    role,
    nameRole: formatSubagentNameRole(name, role),
    model: String(message?.subagent_model || "").trim(),
    reasoning: normalizeSubagentReasoning(message?.subagent_reasoning || ""),
    runtime: subagentRuntimeLabel(message),
    target,
    prompt,
    summary: collapsedSummary,
  };
}

function subagentPreview(message) {
  const detail = subagentDetailFields(message);
  const title = String(subagentDisplayTitle(message) || "")
    .trim()
    .toLowerCase();
  const isWaiting = title.startsWith("waiting");
  if (isWaiting) return "";
  const summaryText = String(detail.summary || detail.prompt || "").trim();
  if (summaryText) {
    const trimmed =
      summaryText.length > 240 ? `${summaryText.slice(0, 240)}...` : summaryText;
    return `└ ${trimmed}`;
  }
  const parts = [];
  const ids = Array.isArray(message?.subagent_ids)
    ? message.subagent_ids.filter(Boolean)
    : [];
  if (detail.prompt)
    parts.push(
      `${detail.prompt.slice(0, 220)}${detail.prompt.length > 220 ? "..." : ""}`,
    );
  if (detail.summary)
    parts.push(
      `${detail.summary.slice(0, 220)}${detail.summary.length > 220 ? "..." : ""}`,
    );
  if (parts.length === 0 && detail.target) parts.push(`Target: ${detail.target}`);
  if (parts.length === 0 && ids.length > 0) {
    parts.push(`ID: ${ids[0]}${ids.length > 1 ? ` (+${ids.length - 1})` : ""}`);
  }
  if (parts.length === 0) return "";
  return parts.join("\n");
}

function subagentDisplayFamily(message) {
  const tool = String(message?.subagent_tool || "")
    .trim()
    .toLowerCase();
  const title = String(message?.subagent_title || message?.content || "")
    .trim()
    .toLowerCase();
  if (tool === "spawn_agent" || /^spawned\b/i.test(title)) return "spawn";
  if (
    tool === "wait_agent" ||
    tool === "wait" ||
    /^waiting\b/i.test(title) ||
    /^subagent wait completed\b/i.test(title)
  )
    return "wait";
  return tool || title;
}

function subagentIdentityKey(message) {
  const lifecycleKey = normalizeActivityCommandKey(
    message?.subagent_lifecycle_key || "",
  );
  if (lifecycleKey) return lifecycleKey;
  const key = normalizeActivityCommandKey(message?.subagent_key || "");
  if (key) return key;
  const ids = Array.isArray(message?.subagent_ids)
    ? message.subagent_ids
        .map((id) =>
          String(id || "")
            .trim()
            .toLowerCase(),
        )
        .filter(Boolean)
        .sort()
    : [];
  const name = String(subagentDisplayName(message) || "")
    .trim()
    .toLowerCase();
  const target = String(message?.subagent_target_id || "")
    .trim()
    .toLowerCase();
  const prompt = normalizeSubagentPromptKey(message?.subagent_prompt || "");
  const strongParts = [ids.join(","), target, prompt].filter(Boolean);
  if (strongParts.length === 0) return "";
  return [...strongParts, name].filter(Boolean).join("|");
}

function subagentWeakIdentityKey(message) {
  const name = String(subagentDisplayName(message) || "")
    .trim()
    .toLowerCase();
  if (!name || isFallbackSubagentName(name)) return "";
  const family = subagentDisplayFamily(message);
  if (family !== "spawn" && family !== "wait") return "";
  return `${family}|${name}`;
}

function subagentDetailScore(message) {
  let score = 0;
  if (String(message?.subagent_prompt || "").trim()) score += 5;
  if (String(message?.subagent_summary || "").trim()) score += 4;
  if (String(message?.subagent_name || "").trim()) score += 3;
  if (
    Array.isArray(message?.subagent_ids) &&
    message.subagent_ids.filter(Boolean).length > 0
  )
    score += 2;
  if (String(message?.subagent_nickname || "").trim()) score += 1;
  return score;
}

function shouldPreferSubagentRow(next, current) {
  const nextStatus = String(next?.subagent_status || "")
    .trim()
    .toLowerCase();
  const currentStatus = String(current?.subagent_status || "")
    .trim()
    .toLowerCase();
  const nextFamily = subagentDisplayFamily(next);
  const currentFamily = subagentDisplayFamily(current);
  if (nextFamily === "wait" && currentFamily === "wait") {
    if (currentStatus === "running" && nextStatus !== "running") return true;
    if (currentStatus !== "running" && nextStatus === "running") return false;
  }
  const nextScore = subagentDetailScore(next);
  const currentScore = subagentDetailScore(current);
  if (nextScore !== currentScore) return nextScore > currentScore;
  const nextUpdated = String(next?.updated_at || next?.created_at || "");
  const currentUpdated = String(
    current?.updated_at || current?.created_at || "",
  );
  return nextUpdated >= currentUpdated;
}

function collapseCanonicalSubagentMessages(input) {
  const src = Array.isArray(input) ? input : [];
  const keepByGroup = new Map();
  for (let idx = 0; idx < src.length; idx += 1) {
    const row = src[idx];
    if (
      String(row?.role || "")
        .trim()
        .toLowerCase() !== "subagent"
    )
      continue;
    const family = subagentDisplayFamily(row);
    if (family !== "spawn" && family !== "wait") continue;
    const identity = subagentIdentityKey(row);
    if (!identity) continue;
    const groupKey = `${family}|${identity}`;
    const existingIdx = keepByGroup.get(groupKey);
    if (typeof existingIdx !== "number") {
      keepByGroup.set(groupKey, idx);
      continue;
    }
    if (shouldPreferSubagentRow(row, src[existingIdx])) {
      keepByGroup.set(groupKey, idx);
    }
  }

  const keepByWeakGroup = new Map();
  for (let idx = 0; idx < src.length; idx += 1) {
    const row = src[idx];
    if (
      String(row?.role || "")
        .trim()
        .toLowerCase() !== "subagent"
    )
      continue;
    const family = subagentDisplayFamily(row);
    if (family !== "spawn" && family !== "wait") continue;
    const weakKey = subagentWeakIdentityKey(row);
    if (!weakKey) continue;
    if (subagentIdentityKey(row)) continue;
    const existingIdx = keepByWeakGroup.get(weakKey);
    if (typeof existingIdx !== "number") {
      keepByWeakGroup.set(weakKey, idx);
      continue;
    }
    const current = src[existingIdx];
    const currentStatus = String(current?.subagent_status || "")
      .trim()
      .toLowerCase();
    const nextStatus = String(row?.subagent_status || "")
      .trim()
      .toLowerCase();
    const nextScore = subagentDetailScore(row);
    const currentScore = subagentDetailScore(current);
    const shouldCollapse =
      currentScore > 0 ||
      nextScore > 0 ||
      currentStatus !== "running" ||
      nextStatus !== "running";
    if (!shouldCollapse) continue;
    if (shouldPreferSubagentRow(row, current)) {
      keepByWeakGroup.set(weakKey, idx);
    }
  }

  return src.filter((row, idx) => {
    if (
      String(row?.role || "")
        .trim()
        .toLowerCase() !== "subagent"
    )
      return true;
    const family = subagentDisplayFamily(row);
    if (family !== "spawn" && family !== "wait") return true;
    const identity = subagentIdentityKey(row);
    if (identity) {
      return keepByGroup.get(`${family}|${identity}`) === idx;
    }
    const weakKey = subagentWeakIdentityKey(row);
    if (!weakKey) return true;
    if (!keepByWeakGroup.has(weakKey)) return true;
    return keepByWeakGroup.get(weakKey) === idx;
  });
}

function collapseAdjacentSubagentMessages(input) {
  const src = Array.isArray(input) ? input : [];
  const out = [];
  for (const row of src) {
    const current = row || {};
    if (
      String(current?.role || "")
        .trim()
        .toLowerCase() !== "subagent"
    ) {
      out.push(current);
      continue;
    }
    const prev = out.length > 0 ? out[out.length - 1] : null;
    if (
      !prev ||
      String(prev?.role || "")
        .trim()
        .toLowerCase() !== "subagent"
    ) {
      out.push(current);
      continue;
    }
    const currentFamily = subagentDisplayFamily(current);
    const prevFamily = subagentDisplayFamily(prev);
    if (
      (currentFamily !== "spawn" && currentFamily !== "wait") ||
      currentFamily !== prevFamily
    ) {
      out.push(current);
      continue;
    }
    const currentName = String(subagentDisplayName(current) || "")
      .trim()
      .toLowerCase();
    const prevName = String(subagentDisplayName(prev) || "")
      .trim()
      .toLowerCase();
    if (
      !currentName ||
      !prevName ||
      currentName !== prevName ||
      isFallbackSubagentName(currentName) ||
      isFallbackSubagentName(prevName)
    ) {
      out.push(current);
      continue;
    }
    out[out.length - 1] = shouldPreferSubagentRow(current, prev)
      ? current
      : prev;
  }
  return out;
}

function collapseNearbyNamedSubagentMessages(input) {
  const src = Array.isArray(input) ? input : [];
  const out = [];
  for (const row of src) {
    const current = row || {};
    if (
      String(current?.role || "")
        .trim()
        .toLowerCase() !== "subagent"
    ) {
      out.push(current);
      continue;
    }
    const currentFamily = subagentDisplayFamily(current);
    if (currentFamily !== "spawn" && currentFamily !== "wait") {
      out.push(current);
      continue;
    }
    const currentName = String(subagentDisplayName(current) || "")
      .trim()
      .toLowerCase();
    if (!currentName || isFallbackSubagentName(currentName)) {
      out.push(current);
      continue;
    }
    let merged = false;
    for (
      let idx = out.length - 1, steps = 0;
      idx >= 0 && steps < 6;
      idx -= 1, steps += 1
    ) {
      const prev = out[idx];
      if (
        String(prev?.role || "")
          .trim()
          .toLowerCase() !== "subagent"
      )
        continue;
      if (subagentDisplayFamily(prev) !== currentFamily) continue;
      const prevName = String(subagentDisplayName(prev) || "")
        .trim()
        .toLowerCase();
      if (
        !prevName ||
        prevName !== currentName ||
        isFallbackSubagentName(prevName)
      )
        continue;
      out[idx] = shouldPreferSubagentRow(current, prev) ? current : prev;
      merged = true;
      break;
    }
    if (!merged) {
      out.push(current);
    }
  }
  return out;
}

function shouldHideRenderedMessage(message) {
  const role = String(message?.role || "")
    .trim()
    .toLowerCase();
  if (
    role === "assistant" &&
    Boolean(message?.pending) &&
    !String(message?.content || "").trim()
  ) {
    return true;
  }
  if (role === "event") {
    return isSpamEventMessage(message?.content || "");
  }
  if (role === "activity") {
    const recoveryKind = String(runtimeRecoveryActivity(message)?.kind || "")
      .trim()
      .toLowerCase();
    if (
      recoveryKind === "interrupt_requested" ||
      recoveryKind === "stop_started" ||
      recoveryKind === "stop_completed" ||
      recoveryKind === "restart_started" ||
      recoveryKind === "restart_completed" ||
      recoveryKind === "account_switch_started" ||
      recoveryKind === "auth_sync_started" ||
      recoveryKind === "auth_sync_completed"
    ) {
      return true;
    }
    const content = String(message?.content || "").trim();
    if (/^command output:\s*\.?$/i.test(content)) {
      return true;
    }
    const contentLower = content.toLowerCase();
    if (
      contentLower.startsWith("thread/started:") ||
      contentLower.startsWith("thread.started:") ||
      contentLower.startsWith("rawresponseitem/completed:") ||
      contentLower.startsWith("thread/tokenusage/updated:") ||
      contentLower.startsWith("event log truncated:")
    ) {
      return true;
    }
  }
  if (
    role === "activity" &&
    message?.mcp_activity &&
    (Boolean(message?.mcp_activity_generic) ||
      /^mcp server status:/i.test(String(message?.content || "").trim()))
  ) {
    return true;
  }
  if (role === "stderr") {
    const content = String(message?.content || "")
      .trim()
      .toLowerCase();
    if (!content || content === "[redacted]") return true;
  }
  if (
    role !== "subagent"
  )
    return false;
  const title = String(subagentDisplayTitle(message) || "")
    .trim()
    .toLowerCase();
  const status = String(message?.subagent_status || "")
    .trim()
    .toLowerCase();
  return title.startsWith("waiting") && status !== "running";
}

function isSpamEventMessage(rawContent) {
  const content = String(rawContent || "").trim().toLowerCase();
  if (!content) return true;
  return (
    content.startsWith("thread/started:") ||
    content.startsWith("thread.started:") ||
    content.startsWith("item/started:") ||
    content.startsWith("item.started:") ||
    content.startsWith("item/completed:") ||
    content.startsWith("item.completed:") ||
    content.startsWith("rawresponseitem/completed:") ||
    content.startsWith("rawresponseitem.completed:") ||
    content.startsWith("thread/tokenusage/updated:") ||
    content.startsWith("thread.tokenusage.updated:")
  );
}

function dedupeAdjacentStderrMessages(input) {
  const src = Array.isArray(input) ? input : [];
  const out = [];
  for (const row of src) {
    const current = row || {};
    const role = String(current?.role || "")
      .trim()
      .toLowerCase();
    if (role !== "stderr") {
      out.push(current);
      continue;
    }
    const content = String(current?.content || "").trim();
    if (!content || content === "[redacted]") {
      continue;
    }
    const prev = out.length > 0 ? out[out.length - 1] : null;
    const prevRole = String(prev?.role || "")
      .trim()
      .toLowerCase();
    const prevContent = String(prev?.content || "").trim();
    if (prevRole !== "stderr" || prevContent !== content) {
      out.push(current);
      continue;
    }
    out[out.length - 1] = {
      ...prev,
      ...current,
      id: String(current?.id || prev?.id || ""),
      content,
      created_at: String(prev?.created_at || current?.created_at || ""),
      updated_at: String(
        current?.updated_at ||
          current?.created_at ||
          prev?.updated_at ||
          prev?.created_at ||
          "",
      ),
    };
  }
  return out;
}

function collapseRecoveryActivityBursts(inputMessages) {
  const src = Array.isArray(inputMessages) ? inputMessages : [];
  const out = [];
  let idx = 0;
  while (idx < src.length) {
    const current = src[idx];
    const role = String(current?.role || "").trim().toLowerCase();
    const recovery = role === "activity" ? runtimeRecoveryActivity(current) : null;
    const recoveryOwner = normalizeLegacyRuntimeActor(recovery?.role || current?.actor);
    const mergeableKinds = new Set([
      "recovery_detected",
      "stop_started",
      "stop_completed",
      "account_switch_started",
      "account_switch_completed",
      "auth_sync_started",
      "auth_sync_completed",
      "restart_started",
      "restart_completed",
      "continue_started",
      "recovery_failed",
    ]);
    if (!recovery || !mergeableKinds.has(String(recovery?.kind || "").trim().toLowerCase())) {
      out.push(current);
      idx += 1;
      continue;
    }

    const burst = [current];
    let cursor = idx + 1;
    while (cursor < src.length) {
      const next = src[cursor];
      const nextRole = String(next?.role || "").trim().toLowerCase();
      const nextRecovery = nextRole === "activity" ? runtimeRecoveryActivity(next) : null;
      const nextRecoveryOwner = normalizeLegacyRuntimeActor(nextRecovery?.role || next?.actor);
      if (!nextRecovery) break;
      if (recoveryOwner !== nextRecoveryOwner) break;
      if (burst.length > 0 && String(nextRecovery?.kind || "").trim().toLowerCase() === "recovery_detected") {
        break;
      }
      burst.push(next);
      const nextKind = String(nextRecovery?.kind || "").trim().toLowerCase();
      if (nextKind === "continue_started" || nextKind === "recovery_failed") {
        cursor += 1;
        break;
      }
      cursor += 1;
    }

    if (burst.length === 1) {
      out.push(current);
      idx += 1;
      continue;
    }

    const summaryParts = [];
    for (const item of burst) {
      const parsed = parseRuntimeRecoveryActivity(item?.content || "");
      const text = String(parsed?.text || item?.content || "").trim();
      if (!text) continue;
      if (!summaryParts.includes(text)) {
        summaryParts.push(text);
      }
    }
    const first = burst[0];
    const last = burst[burst.length - 1];
    out.push({
      ...first,
      id: String(last?.id || first?.id || ""),
      content: summaryParts.join("\n"),
      updated_at: String(last?.updated_at || last?.created_at || first?.updated_at || first?.created_at || ""),
      recovery_kind: "recovery_summary",
    });
    idx = cursor;
  }
  return out;
}

function runtimeRecoveryActivity(message) {
  const parsed = parseRuntimeRecoveryActivity(message?.content || "");
  if (parsed) return parsed;
  const kind = String(message?.recovery_kind || "").trim().toLowerCase();
  if (!kind) return null;
  return {
    kind,
    role: normalizeLegacyRuntimeActor(message?.actor),
    text: String(message?.content || "").trim(),
  };
}

function dedupeAdjacentSpawnSubagentMessages(input) {
  const src = Array.isArray(input) ? input : [];
  const out = [];
  for (const row of src) {
    const current = row || {};
    const role = String(current?.role || "")
      .trim()
      .toLowerCase();
    if (role !== "subagent") {
      out.push(current);
      continue;
    }
    const prev = out.length > 0 ? out[out.length - 1] : null;
    const currentTool = String(current?.subagent_tool || "")
      .trim()
      .toLowerCase();
    const prevTool = String(prev?.subagent_tool || "")
      .trim()
      .toLowerCase();
    const currentTitle = String(
      current?.subagent_title || current?.content || "",
    )
      .trim()
      .toLowerCase();
    const prevTitle = String(prev?.subagent_title || prev?.content || "")
      .trim()
      .toLowerCase();
    const currentIsSpawn =
      currentTool === "spawn_agent" || /^spawned\b/i.test(currentTitle);
    const prevIsSpawn =
      prevTool === "spawn_agent" || /^spawned\b/i.test(prevTitle);
    const currentStatus = String(current?.subagent_status || "")
      .trim()
      .toLowerCase();
    const prevStatus = String(prev?.subagent_status || "")
      .trim()
      .toLowerCase();
    const currentPrompt = normalizeSubagentPromptKey(
      current?.subagent_prompt || current?.subagent_summary || "",
    );
    const prevPrompt = normalizeSubagentPromptKey(
      prev?.subagent_prompt || prev?.subagent_summary || "",
    );
    const promptMatches = Boolean(
      currentPrompt &&
      prevPrompt &&
      (currentPrompt === prevPrompt ||
        currentPrompt.includes(prevPrompt) ||
        prevPrompt.includes(currentPrompt)),
    );
    const shouldMerge = Boolean(
      prev &&
      String(prev?.role || "")
        .trim()
        .toLowerCase() === "subagent" &&
      prevIsSpawn &&
      currentIsSpawn &&
      prevStatus === "running" &&
      (currentStatus === "done" || currentStatus === "failed") &&
      (promptMatches || !currentPrompt || !prevPrompt),
    );
    if (!shouldMerge) {
      out.push(current);
      continue;
    }
    out[out.length - 1] = {
      ...prev,
      updated_at: String(current?.updated_at || prev?.updated_at || ""),
      subagent_status: currentStatus,
      subagent_phase: String(
        current?.subagent_phase || prev?.subagent_phase || "",
      )
        .trim()
        .toLowerCase(),
      subagent_title:
        subagentDisplayTitle(current) || subagentDisplayTitle(prev),
      subagent_tool: String(
        current?.subagent_tool || prev?.subagent_tool || "spawn_agent",
      ).trim(),
      subagent_ids: [
        ...new Set([
          ...(Array.isArray(prev?.subagent_ids) ? prev.subagent_ids : []),
          ...(Array.isArray(current?.subagent_ids) ? current.subagent_ids : []),
        ]),
      ],
      subagent_target_id: String(
        current?.subagent_target_id || prev?.subagent_target_id || "",
      ).trim(),
      subagent_nickname: choosePreferredSubagentName(
        String(current?.subagent_nickname || "").trim() ||
          subagentDisplayName(current),
        String(prev?.subagent_nickname || "").trim() ||
          subagentDisplayName(prev),
      ),
      subagent_role: choosePreferredSubagentRole(
        String(current?.subagent_role || "").trim() ||
          subagentDisplayRole(current),
        String(prev?.subagent_role || "").trim() || subagentDisplayRole(prev),
      ),
      subagent_model: String(
        current?.subagent_model || prev?.subagent_model || "",
      ).trim(),
      subagent_reasoning: String(
        current?.subagent_reasoning || prev?.subagent_reasoning || "",
      ).trim(),
      subagent_prompt: String(
        current?.subagent_prompt || prev?.subagent_prompt || "",
      ).trim(),
      subagent_summary: String(
        current?.subagent_summary || prev?.subagent_summary || "",
      ).trim(),
      subagent_raw: current?.subagent_raw || prev?.subagent_raw || {},
    };
  }
  return out;
}

function mergeExecOutput(startText, endText) {
  const normalize = (value) => {
    const text = String(value || "").trim();
    return text === "No output captured." ? "" : text;
  };
  const first = normalize(startText);
  const second = normalize(endText);
  if (!first) return second;
  if (!second) return first;
  if (first.includes(second)) return first;
  if (second.includes(first)) return second;
  return `${first}\n${second}`;
}

function dedupeAdjacentExecMessages(input) {
  const src = Array.isArray(input) ? input : [];
  const out = [];
  const activeRunningByKey = new Map();
  const normalizeOwnership = (row) => {
    const actorRaw = String(row?.actor || "").trim().toLowerCase();
    const laneRaw = String(row?.lane || "").trim().toLowerCase();
    const actor = actorRaw;
    const lane = (() => {
      if (laneRaw) return laneRaw;
      if (actor === "executor") return actor;
      return "";
    })();
    return { actor, lane };
  };
  const ownershipKey = (row) => {
    const { actor, lane } = normalizeOwnership(row);
    return `${actor}|${lane}`;
  };
  const timestampMs = (row) => {
    const value = Date.parse(
      String(row?.updated_at || row?.created_at || "").trim(),
    );
    return Number.isNaN(value) ? 0 : value;
  };

  for (const row of src) {
    const current = row || {};
    const role = String(current?.role || "")
      .trim()
      .toLowerCase();
    if (role !== "exec") {
      out.push(current);
      continue;
    }
    const prev = out.length > 0 ? out[out.length - 1] : null;
    if (
      !prev ||
      String(prev?.role || "")
        .trim()
        .toLowerCase() !== "exec"
    ) {
      out.push(current);
      continue;
    }
    const prevCommandKey = normalizeExecIdentityCommandKey(
      String(prev?.exec_command || prev?.content || "").trim(),
    );
    const currentCommandKey = normalizeExecIdentityCommandKey(
      String(current?.exec_command || current?.content || "").trim(),
    );
    const prevStatus = normalizeExecStatus(prev?.exec_status);
    const currentStatus = normalizeExecStatus(current?.exec_status);
    const prevExitCode = Number(prev?.exec_exit_code || 0) || 0;
    const currentExitCode = Number(current?.exec_exit_code || 0) || 0;
    const sameTerminalDisplayCommand = (() => {
      if (prevStatus === "running" || currentStatus === "running") return false;
      const prevDisplay = normalizeExecCommandForDisplay(
        String(prev?.exec_command || prev?.content || ""),
      );
      const currentDisplay = normalizeExecCommandForDisplay(
        String(current?.exec_command || current?.content || ""),
      );
      if (!prevDisplay || !currentDisplay || prevDisplay !== currentDisplay) return false;
      if (ownershipKey(prev) !== ownershipKey(current)) return false;
      const prevTS = timestampMs(prev);
      const currentTS = timestampMs(current);
      if (prevTS > 0 && currentTS > 0 && Math.abs(currentTS - prevTS) > 1_500) {
        return false;
      }
      return true;
    })();
    if ((!prevCommandKey || !currentCommandKey || prevCommandKey !== currentCommandKey) && !sameTerminalDisplayCommand) {
      out.push(current);
      continue;
    }
    const isTerminalTransition =
      prevStatus === "running" && (currentStatus === "done" || currentStatus === "failed");
    const isOutOfOrderRunningAfterTerminal =
      (prevStatus === "done" || prevStatus === "failed") &&
      currentStatus === "running";
    if (isOutOfOrderRunningAfterTerminal) {
      out[out.length - 1] = {
        ...prev,
        updated_at: String(current?.updated_at || prev?.updated_at || ""),
        exec_output: mergeExecOutput(
          prev?.exec_output || "",
          current?.exec_output || "",
        ),
        exec_output_source: normalizeExecOutputSource(
          prev?.exec_output_source === current?.exec_output_source
            ? prev?.exec_output_source
            : "persisted-merge",
        ),
      };
      continue;
    }
    if (!isTerminalTransition && (prevStatus !== currentStatus || prevExitCode !== currentExitCode)) {
      out.push(current);
      continue;
    }
    out[out.length - 1] = {
      ...prev,
      updated_at: String(current?.updated_at || prev?.updated_at || ""),
      exec_status: currentStatus,
      exec_exit_code: currentExitCode,
      exec_output: mergeExecOutput(
        prev?.exec_output || "",
        current?.exec_output || "",
      ),
      exec_output_source: normalizeExecOutputSource(
        prev?.exec_output_source === current?.exec_output_source
          ? prev?.exec_output_source
          : "persisted-merge",
      ),
    };

    if (currentStatus === "running") {
      activeRunningByKey.set(
        `${currentCommandKey}|${ownershipKey(current)}`,
        out.length - 1,
      );
    } else {
      activeRunningByKey.delete(
        `${currentCommandKey}|${ownershipKey(current)}`,
      );
    }
  }

  // Merge non-adjacent running->terminal transitions for the same command/owner.
  // This prevents duplicate terminal bubbles when intermediate rows are inserted.
  const collapsed = [];
  activeRunningByKey.clear();
  for (const row of out) {
    const current = row || {};
    const role = String(current?.role || "").trim().toLowerCase();
    if (role !== "exec") {
      collapsed.push(current);
      continue;
    }
    const commandKey = normalizeExecIdentityCommandKey(
      String(current?.exec_command || current?.content || "").trim(),
    );
    if (!commandKey) {
      collapsed.push(current);
      continue;
    }
    const status = normalizeExecStatus(current?.exec_status);
    const key = `${commandKey}|${ownershipKey(current)}`;
    if (status === "running") {
      const existing = activeRunningByKey.get(key);
      if (typeof existing === "number" && collapsed[existing]) {
        const prev = collapsed[existing];
        collapsed[existing] = {
          ...prev,
          ...current,
          id: String(prev?.id || current?.id || ""),
          exec_status: "running",
          exec_exit_code: Number(prev?.exec_exit_code || 0) || 0,
          exec_output: mergeExecOutput(
            String(prev?.exec_output || ""),
            String(current?.exec_output || ""),
          ),
        };
        continue;
      }
      collapsed.push(current);
      activeRunningByKey.set(key, collapsed.length - 1);
      continue;
    }

    let runningIdx = activeRunningByKey.get(key);
    if (typeof runningIdx !== "number") {
      const { actor, lane } = normalizeOwnership(current);
      if (!actor && !lane) {
        const prefix = `${commandKey}|`;
        let fallbackIdx = -1;
        let fallbackCount = 0;
        for (const [candidateKey, candidateIdx] of activeRunningByKey.entries()) {
          if (!String(candidateKey || "").startsWith(prefix)) continue;
          if (typeof candidateIdx !== "number" || !collapsed[candidateIdx]) continue;
          fallbackIdx = candidateIdx;
          fallbackCount += 1;
          if (fallbackCount > 1) break;
        }
        if (fallbackCount === 1 && fallbackIdx >= 0) {
          runningIdx = fallbackIdx;
        }
      }
    }
    if (typeof runningIdx === "number" && collapsed[runningIdx]) {
      const running = collapsed[runningIdx];
      const runningMs = timestampMs(running);
      const terminalMs = timestampMs(current);
      const withinWindow =
        runningMs > 0 && terminalMs > 0
          ? Math.abs(terminalMs - runningMs) <= 30_000
          : true;
      if (withinWindow) {
        collapsed[runningIdx] = {
          ...running,
          ...current,
          id: String(running?.id || current?.id || ""),
          updated_at: String(
            current?.updated_at ||
              current?.created_at ||
              running?.updated_at ||
              running?.created_at ||
              "",
          ),
          exec_status: status,
          exec_exit_code: Number(current?.exec_exit_code || 0) || 0,
          exec_output: mergeExecOutput(
            String(running?.exec_output || ""),
            String(current?.exec_output || ""),
          ),
          exec_output_source: normalizeExecOutputSource(
            running?.exec_output_source === current?.exec_output_source
              ? running?.exec_output_source
              : "persisted-merge",
          ),
        };
        activeRunningByKey.delete(key);
        continue;
      }
    }
    collapsed.push(current);
    activeRunningByKey.delete(key);
  }
  return collapsed;
}

function dedupeAdjacentFileOperationActivities(input) {
  const src = Array.isArray(input) ? input : [];
  const out = [];
  for (const row of src) {
    const current = row || {};
    const role = String(current?.role || "")
      .trim()
      .toLowerCase();
    if (role !== "activity") {
      out.push(current);
      continue;
    }
    const currentKey = fileOperationDedupKey(current);
    if (!currentKey) {
      out.push(current);
      continue;
    }
    const prev = out.length > 0 ? out[out.length - 1] : null;
    const prevRole = String(prev?.role || "")
      .trim()
      .toLowerCase();
    const prevKey = prevRole === "activity" ? fileOperationDedupKey(prev) : "";
    if (!prevKey || prevKey !== currentKey) {
      out.push({ ...current, file_op: normalizedFileOperationLabel(current) });
      continue;
    }
    const prevLabel = normalizedFileOperationLabel(prev);
    const nextLabel = normalizedFileOperationLabel(current);
    const prevParts = fileOperationDisplayParts(prevLabel);
    const nextParts = fileOperationDisplayParts(nextLabel);
    const preferredLabel =
      nextParts.stats || !prevParts.stats
        ? nextLabel || prevLabel
        : prevLabel || nextLabel;
    out[out.length - 1] = {
      ...prev,
      ...current,
      id: String(prev?.id || current?.id || ""),
      file_op: preferredLabel || "",
      content:
        preferredLabel || String(current?.content || prev?.content || ""),
      created_at: String(prev?.created_at || current?.created_at || ""),
      updated_at: String(
        current?.updated_at ||
          current?.created_at ||
          prev?.updated_at ||
          prev?.created_at ||
          "",
      ),
    };
  }
  return out;
}

function shouldCollapseContent(content) {
  const text = String(content || "");
  if (!text) return false;
  if (text.length > 1600) return true;
  return text.split("\n").length > 20;
}

function messagePreviewContent(content) {
  const text = String(content || "");
  if (!shouldCollapseContent(text)) return text;
  const lines = text.split("\n");
  if (lines.length > 20) {
    return `${lines.slice(0, 20).join("\n")}\n...`;
  }
  return `${text.slice(0, 1600)}\n...`;
}

function sanitizeSensitiveLogText(input) {
  let text = String(input || "");
  if (!text) return "";
  text = text.replace(
    /("(?:access_token|refresh_token|id_token|api[_-]?key|authorization|anthropic_auth_token)"\s*:\s*")([^"]+)(")/gi,
    "$1[REDACTED]$3",
  );
  text = text.replace(
    /\b((?:access_token|refresh_token|id_token|api[_-]?key|authorization|anthropic_auth_token)\s*=\s*)(\S+)/gi,
    "$1[REDACTED]",
  );
  text = text.replace(/\bBearer\s+[A-Za-z0-9._-]+/gi, "Bearer [REDACTED]");
  text = text.replace(/\bsk-[A-Za-z0-9]{12,}\b/g, "sk-[REDACTED]");
  text = text.replace(/\/home\/[^/\s]+/g, "/home/[user]");
  return text;
}

function fileOpTone(action) {
  const text = String(action || "")
    .trim()
    .toLowerCase();
  if (!text) return "";
  if (text.startsWith("create")) return "is-create";
  if (text.startsWith("edit")) return "is-edit";
  if (text.startsWith("read")) return "is-read";
  if (text.startsWith("delete")) return "is-delete";
  if (text.startsWith("move")) return "is-move";
  if (text.startsWith("rename")) return "is-rename";
  return "";
}

function isPlanningAssistantMessage(message) {
  const role = String(message?.role || "")
    .trim()
    .toLowerCase();
  return role === "assistant";
}

function parsePlanningFinalPlan(message) {
  if (!isPlanningAssistantMessage(message)) return null;
  const text = String(message?.content || "").trim();
  if (!text) return null;
  const normalized = text.replace(/\r\n/g, "\n");
  const parsed = parsePlanningReviewPlan(normalized);
  const summary = String(parsed?.summary || "").trim();
  const tasks = Array.isArray(parsed?.tasks) ? parsed.tasks : [];
  const stopConditions = Array.isArray(parsed?.stopConditions) ? parsed.stopConditions : [];
  const confidence =
    Number.isFinite(parsed?.confidence) ? Number(parsed.confidence) : null;
  if (!summary || tasks.length === 0) return null;

  return {
    summary,
    tasks,
    stopConditions,
    ready: isExecutionReadyPlan({ rawPlan: normalized, summary, tasks }),
    confidence,
  };
}

function messageDisplayContent(message) {
  const role = String(message?.role || "")
    .trim()
    .toLowerCase();
  const content = String(message?.content || "-");
  if (role === "activity") {
    const recovery = parseRuntimeRecoveryActivity(content);
    if (recovery?.text) return recovery.text;
    if (message?.mcp_activity) {
      const target = String(
        message?.mcp_activity_target || message?.mcp_activity_tool || "",
      )
        .trim()
        .toLowerCase();
      const contentLower = content.trim().toLowerCase();
      const searchLabel = (() => {
        if (
          target.includes("web_search") ||
          target.includes("search_web") ||
          target.includes("search the web")
        ) {
          return "the web";
        }
        if (
          target.includes("search_code") ||
          target.includes("grep") ||
          target.includes("code_search")
        ) {
          return "code";
        }
        if (
          target.includes("docs") ||
          target.includes("documentation") ||
          target.includes("ref_search_documentation") ||
          target.includes("get_code_context")
        ) {
          return "docs";
        }
        return "";
      })();
      if (searchLabel && /^running mcp:/i.test(contentLower)) {
        return `Searching ${searchLabel}`;
      }
      if (searchLabel && /^mcp done:/i.test(contentLower)) {
        return `Searched ${searchLabel}`;
      }
    }
  }
  if (role === "event" || role === "stderr" || role === "activity") {
    return sanitizeSensitiveLogText(content);
  }
  return content;
}

function hasPlanningPlanSections(rawText) {
  const raw = String(rawText || "").trim().toLowerCase();
  if (!raw) return false;
  const hasImplementationPlan = raw.includes("implementation plan");
  const hasChecklistTasks = /(^|\n)- \[[ xX]\]\s+/.test(raw);
  return hasImplementationPlan && hasChecklistTasks;
}

function planningFinalPlanSections(message) {
  const role = String(message?.role || "")
    .trim()
    .toLowerCase();
  if (role !== "assistant") return null;
  const content = String(message?.content || "").trim();
  if (!content) return null;
  if (!hasPlanningPlanSections(content)) return null;
  const parsed = parsePlanningReviewPlan(content);
  return {
    summary: String(parsed?.summary || "").trim(),
    tasks: Array.isArray(parsed?.tasks) ? parsed.tasks : [],
    stopConditions: Array.isArray(parsed?.stopConditions)
      ? parsed.stopConditions
      : [],
    confidence: Number.isFinite(parsed?.confidence)
      ? parsed.confidence
      : null,
  };
}

function projectMessagesForView(
  inputMessages,
  { buildExecAwareMessages, rawMode = false, alreadyCanonical = false } = {},
) {
  if (alreadyCanonical && !rawMode) {
    let rendered = sortMessagesChronologically(
      Array.isArray(inputMessages) ? inputMessages : [],
    );
    rendered = rendered.filter((item) => !shouldHideRenderedMessage(item));
    rendered = collapseRecoveryActivityBursts(rendered);
    rendered = collapseTransientActivityMessages(rendered);
    rendered = dedupeAdjacentFileOperationActivities(rendered);
    rendered = dedupeAdjacentExecMessages(rendered);
    rendered = dedupeAdjacentStderrMessages(rendered);
    rendered = collapseAdjacentSubagentMessages(rendered);
    rendered = collapseNearbyNamedSubagentMessages(rendered);
    rendered = collapseCanonicalSubagentMessages(rendered);
    return rendered;
  }
  let rendered = buildExecAwareMessages(inputMessages, rawMode);
  rendered = dedupeAdjacentSpawnSubagentMessages(rendered);
  rendered = rendered.filter((item) => {
    const role = String(item?.role || "")
      .trim()
      .toLowerCase();
    if (role !== "activity") return true;
    const parsed = parseActivityText(item?.content || "");
    if (!parsed?.command) return true;
    return parsed.kind === "other";
  });
  if (!rawMode) {
    rendered = rendered.filter((item) => !shouldHideRenderedMessage(item));
  }
  rendered = sortMessagesChronologically(rendered);
  rendered = collapseRecoveryActivityBursts(rendered);
  rendered = collapseTransientActivityMessages(rendered);
  rendered = dedupeAdjacentFileOperationActivities(rendered);
  rendered = dedupeAdjacentExecMessages(rendered);
  rendered = dedupeAdjacentStderrMessages(rendered);
  return rendered;
}

export {
  collectLiveMessageIDs,
  completedViewStatus,
  currentRecoveryStatus,
  execStatusLabel,
  fileOpTone,
  isInternalRunnerActivity,
  parsePlanningFinalPlan,
  messageDisplayContent,
  messagePreviewContent,
  normalizeExecCommandForDisplay,
  planningFinalPlanSections,
  projectMessagesForView,
  sanitizeSensitiveLogText,
  shouldCollapseContent,
  shouldHideRenderedMessage,
  subagentDetailFields,
  subagentDisplayName,
  subagentDisplayRole,
  subagentDisplayTitle,
  subagentPreview,
  subagentStatusLabel,
};
