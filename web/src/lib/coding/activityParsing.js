function normalizeActivityCommandKey(command) {
  return String(command || "")
    .trim()
    .replace(/\s+/g, " ")
    .toLowerCase();
}

function normalizeExecIdentityCommandKey(command) {
  let text = String(command || "")
    .trim()
    .replace(/\s+/g, " ");
  if (!text) return "";
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
  return text
    .replace(/\.\.\.$/u, "")
    .trim()
    .toLowerCase()
    .slice(0, 120);
}

function normalizeExecStatus(status) {
  const v = String(status || "")
    .trim()
    .toLowerCase();
  if (v === "running" || v === "done" || v === "failed") return v;
  return "running";
}

function normalizeExecOutputSource(value) {
  const v = String(value || "")
    .trim()
    .toLowerCase();
  if (v === "live" || v === "persisted" || v === "persisted-merge") return v;
  return "live";
}

function normalizeLegacyRuntimeActor(value) {
  const actor = String(value || "")
    .trim()
    .toLowerCase();
  if (actor === "executor") return actor;
  return "";
}

function normalizeLegacyRuntimeLane(value) {
  const lane = String(value || "")
    .trim()
    .toLowerCase();
  if (lane === "executor") return lane;
  if (lane === "chat" || lane === "assistant" || lane === "user") return "chat";
  return "";
}

function resolveLegacyRuntimeOwnership(actorValue, laneValue) {
  const actor = normalizeLegacyRuntimeActor(actorValue);
  const lane =
    normalizeLegacyRuntimeLane(laneValue) ||
    normalizeLegacyRuntimeLane(actorValue);
  return { actor, lane };
}

function parseActivityText(rawText) {
  const text = String(rawText || "").trim();
  if (!text) return { kind: "other", command: "", exitCode: 0 };
  let m = text.match(/^Running:\s+(.+)$/i);
  if (m) {
    return { kind: "running", command: String(m[1] || "").trim(), exitCode: 0 };
  }
  m = text.match(/^Command done:\s+(.+)$/i);
  if (m) {
    return { kind: "done", command: String(m[1] || "").trim(), exitCode: 0 };
  }
  m = text.match(/^Command failed\s+\(exit\s+(\d+)\):\s+(.+)$/i);
  if (m) {
    return {
      kind: "failed",
      command: String(m[2] || "").trim(),
      exitCode: Number(m[1]) || 0,
    };
  }
  return { kind: "other", command: "", exitCode: 0 };
}

function parseRuntimeRecoveryActivity(rawText) {
  const text = String(rawText || "").trim();
  if (!text) return null;
  const normalizeRecoveryRole = (value) => {
    const role = String(value || "")
      .trim()
      .toLowerCase();
    if (role === "chat" || role === "executor") return role;
    return "";
  };
  const recoveryRoleLabel = (role, fallback = "runtime") =>
    normalizeRecoveryRole(role) || fallback;

  let m = text.match(
    /^thread\.resume_started\s+role=([a-z_]+)(?:\s+thread_id=([^\s]+))?$/i,
  );
  if (m) {
    const role = normalizeRecoveryRole(m[1]);
    const threadID = String(m[2] || "").trim();
    let label = `Resuming ${recoveryRoleLabel(role)} thread`;
    if (threadID) label += `: ${threadID}`;
    return {
      kind: "resume_started",
      role,
      threadID,
      text: label,
    };
  }

  m = text.match(
    /^thread\.resume_completed\s+attempts?=(\d+)\s+role=([a-z_]+)(?:\s+thread_id=([^\s]+))?$/i,
  );
  if (m) {
    const attempt = Number(m[1]) || 0;
    const role = normalizeRecoveryRole(m[2]);
    const threadID = String(m[3] || "").trim();
    let label = `Resume completed for ${recoveryRoleLabel(role)} thread`;
    if (threadID) label += `: ${threadID}`;
    if (attempt > 0)
      label += ` after ${attempt} attempt${attempt === 1 ? "" : "s"}`;
    return {
      kind: "resume_completed",
      role,
      attempt,
      threadID,
      text: label,
    };
  }

  m = text.match(
    /^thread\.resume_failed\s+attempts?=(\d+)\s+role=([a-z_]+)(?:\s+thread_id=([^\s]+))?(?:\s+reason=(.+))?$/i,
  );
  if (m) {
    const attempt = Number(m[1]) || 0;
    const role = normalizeRecoveryRole(m[2]);
    const threadID = String(m[3] || "").trim();
    const reason = String(m[4] || "").trim();
    let label = `Resume failed for ${recoveryRoleLabel(role)} thread`;
    if (threadID) label += `: ${threadID}`;
    if (attempt > 0)
      label += ` after ${attempt} attempt${attempt === 1 ? "" : "s"}`;
    if (reason) label += ` (${reason})`;
    return {
      kind: "resume_failed",
      role,
      attempt,
      threadID,
      reason,
      text: label,
    };
  }

  m = text.match(
    /^thread\.rebootstrap_started\s+role=([a-z_]+)\s+previous_thread_id=([^\s]+)$/i,
  );
  if (m) {
    const role = normalizeRecoveryRole(m[1]);
    const previousThreadID = String(m[2] || "").trim();
    let label = `Starting a fresh ${recoveryRoleLabel(role)} thread after resume failure`;
    if (previousThreadID) label += `: ${previousThreadID}`;
    return {
      kind: "rebootstrap_started",
      role,
      previousThreadID,
      text: label,
    };
  }

  m = text.match(/^turn\.interrupt_requested\s+role=([a-z_]+)$/i);
  if (m) {
    const role = normalizeRecoveryRole(m[1]);
    return {
      kind: "interrupt_requested",
      role,
      text: `Interrupt requested for ${recoveryRoleLabel(role)} runtime`,
    };
  }

  m = text.match(
    /^turn\.continue_started\s+role=([a-z_]+)(?:\s+thread_id=(.+))?$/i,
  );
  if (m) {
    const role = normalizeRecoveryRole(m[1]);
    const threadID = String(m[2] || "").trim();
    let label = `Continuing ${recoveryRoleLabel(role)} runtime after recovery`;
    if (threadID) label += `: ${threadID}`;
    return {
      kind: "continue_started",
      role,
      threadID,
      text: label,
    };
  }

  m = text.match(
    /^runtime\.(recovery_detected|recovery_failed|stop_started|stop_completed|restart_started|restart_completed)\s+role=([a-z_]+)(?:\s+reason=([a-z_]+))?$/i,
  );
  if (m) {
    const kind = String(m[1] || "")
      .trim()
      .toLowerCase();
    const role = normalizeRecoveryRole(m[2]);
    const reason = String(m[3] || "")
      .trim()
      .toLowerCase();
    let label = "";
    switch (kind) {
      case "recovery_detected":
        label = `Recovery detected for ${recoveryRoleLabel(role)} runtime`;
        if (reason) label += ` (${reason})`;
        break;
      case "recovery_failed":
        label = `Recovery failed for ${recoveryRoleLabel(role)} runtime`;
        if (reason) label += ` (${reason})`;
        break;
      case "stop_started":
        label = `Stopping ${recoveryRoleLabel(role)} runtime`;
        break;
      case "stop_completed":
        label = `Stopped ${recoveryRoleLabel(role)} runtime`;
        break;
      case "restart_started":
        label = `Restarting ${recoveryRoleLabel(role)} runtime`;
        break;
      case "restart_completed":
        label = `Restarted ${recoveryRoleLabel(role)} runtime`;
        break;
      default:
        label = text;
        break;
    }
    return {
      kind,
      role,
      reason,
      text: label,
    };
  }

  m = text.match(
    /^account\.switch_(started|completed)\s+role=([a-z_]+)(?:\s+account_email=([^\s]+))?(?:\s+account_id=([^\s]+))?$/i,
  );
  if (m) {
    const phase = String(m[1] || "")
      .trim()
      .toLowerCase();
    const role = normalizeRecoveryRole(m[2]);
    const accountEmail = String(m[3] || "").trim();
    const accountID = String(m[4] || "").trim();
    let label =
      phase === "completed"
        ? `Switched account for ${recoveryRoleLabel(role)} runtime`
        : `Switching account for ${recoveryRoleLabel(role)} runtime`;
    if (accountEmail) {
      label += `: ${accountEmail}`;
    } else if (accountID) {
      label += `: ${accountID}`;
    }
    return {
      kind: `account_switch_${phase}`,
      role,
      accountEmail,
      accountID,
      text: label,
    };
  }

  m = text.match(/^auth\.sync_(started|completed)\s+role=([a-z_]+)$/i);
  if (m) {
    const phase = String(m[1] || "")
      .trim()
      .toLowerCase();
    const role = normalizeRecoveryRole(m[2]);
    return {
      kind: `auth_sync_${phase}`,
      role,
      text:
        phase === "completed"
          ? `Auth sync completed for ${recoveryRoleLabel(role)} runtime`
          : `Auth sync started for ${recoveryRoleLabel(role)} runtime`,
    };
  }

  return null;
}

function firstNonEmptyText(...values) {
  for (const value of values) {
    const text = String(value || "").trim();
    if (text) return text;
  }
  return "";
}

function normalizeAppServerItemType(rawType) {
  const value = String(rawType || "")
    .trim()
    .toLowerCase();
  switch (value) {
    case "agentmessage":
    case "agent_message":
      return "agentmessage";
    case "usermessage":
    case "user_message":
      return "usermessage";
    case "commandexecution":
    case "command_execution":
      return "command_execution";
    case "filechange":
    case "file_change":
      return "file_change";
    case "fileread":
    case "file_read":
      return "file_read";
    case "functioncall":
    case "function_call":
      return "function_call";
    case "functioncalloutput":
    case "function_call_output":
      return "function_call_output";
    default:
      return value;
  }
}

function normalizeAppServerEventType(rawType) {
  return String(rawType || "")
    .trim()
    .toLowerCase()
    .replaceAll("/", ".");
}

function formatMCPActivityTarget(server, tool, fallbackName = "") {
  const safeServer = String(server || "").trim();
  const safeTool = String(tool || "").trim();
  const safeFallback = String(fallbackName || "").trim();
  if (safeServer && safeTool) return `${safeServer}.${safeTool}`;
  if (safeTool) return safeTool;
  if (safeServer) return safeServer;
  return safeFallback;
}

function parseMCPActivityPayload(payload) {
  if (!payload || typeof payload !== "object") return null;

  const eventType = normalizeAppServerEventType(payload?.type);
  const item =
    payload?.item && typeof payload.item === "object" ? payload.item : null;
  if (!item) return null;

  const itemType = normalizeAppServerItemType(item?.type);
  if (!itemType || itemType === "collab_tool_call") return null;
  if (itemType !== "tool_call" && itemType !== "function_call") return null;

  const functionRef =
    item?.function && typeof item.function === "object" ? item.function : null;
  const rawToolName = firstNonEmptyText(
    item?.tool,
    item?.tool_name,
    item?.name,
    functionRef?.name,
  );
  const args = parseToolArguments(
    item?.arguments ||
      item?.input ||
      item?.params ||
      item?.payload ||
      functionRef?.arguments ||
      functionRef?.input,
  );

  const isMCPToolCall =
    rawToolName.toLowerCase().startsWith("mcp__") ||
    Boolean(
      firstNonEmptyText(
        args?.server,
        args?.server_name,
        args?.mcp_server,
        args?.mcpServer,
        item?.server,
        item?.server_name,
        item?.mcp_server,
      ),
    );
  if (!isMCPToolCall) return null;

  let server = "";
  let tool = "";
  if (rawToolName.toLowerCase().startsWith("mcp__")) {
    const segments = rawToolName
      .split("__")
      .map((part) => String(part || "").trim())
      .filter(Boolean);
    server = String(segments[1] || "").trim();
    tool = String(segments.slice(2).join("__") || "").trim();
  }

  server = firstNonEmptyText(
    server,
    args?.server,
    args?.server_name,
    args?.mcp_server,
    args?.mcpServer,
    item?.server,
    item?.server_name,
    item?.mcp_server,
  );
  tool = firstNonEmptyText(
    tool,
    args?.tool,
    args?.tool_name,
    args?.toolName,
    args?.name,
    args?.mcp_tool,
    args?.mcpTool,
  );

  const target = formatMCPActivityTarget(server, tool, rawToolName);
  if (!target) return null;
  const summary = String(
    extractSummaryFromAny(
      item?.output ??
        item?.result ??
        item?.response ??
        item?.content ??
        item?.data ??
        item?.output_text ??
        item?.text ??
        "",
    ) || "",
  ).trim();
  const compactSummary = summary.replace(/\s+/g, " ").trim();
  const hasUsableSummary =
    compactSummary &&
    compactSummary.toLowerCase() !== target.toLowerCase() &&
    compactSummary.toLowerCase() !== rawToolName.toLowerCase();

  const hasError = Boolean(item?.error || payload?.error);
  const isCompleted =
    eventType === "item.completed" ||
    eventType === "tool.completed" ||
    eventType === "tool.call.completed";
  const isStarted =
    eventType === "item.started" ||
    eventType === "item.updated" ||
    eventType === "tool.started" ||
    eventType === "tool.call.started";

  let text = "";
  if (hasError) {
    text = `MCP failed: ${target}`;
  } else if (isCompleted) {
    text = `MCP done: ${target}`;
  } else if (isStarted) {
    text = `Running MCP: ${target}`;
  } else {
    return null;
  }
  if (hasUsableSummary && (hasError || isCompleted)) {
    text += `\n  └ ${compactSummary}`;
  }

  return {
    text,
    status: hasError ? "failed" : isCompleted ? "done" : "running",
    target,
    server,
    tool,
  };
}

function parseFileOperationText(rawText) {
  const text = String(rawText || "").trim();
  if (!text) return "";
  const m = text.match(
    /^\[(Edited|Read|Created|Deleted|Moved|Renamed)\s+(.+)\]$/i,
  );
  if (!m) return "";
  const action = String(m[1] || "").trim();
  const target = String(m[2] || "").trim();
  if (!action || !target) return "";
  return `${action}: ${target}`;
}

function normalizeFileOperationAction(kind) {
  const value = String(kind || "")
    .trim()
    .toLowerCase();
  if (!value) return "";
  if (["read", "read_file", "open", "opened"].includes(value)) return "Read";
  if (["create", "created", "add", "added", "new"].includes(value))
    return "Created";
  if (["delete", "deleted", "remove", "removed"].includes(value))
    return "Deleted";
  if (["move", "moved"].includes(value)) return "Moved";
  if (["rename", "renamed"].includes(value)) return "Renamed";
  if (
    [
      "edit",
      "edited",
      "update",
      "updated",
      "modify",
      "modified",
      "write",
      "written",
    ].includes(value)
  )
    return "Edited";
  return "";
}

function extractFileOperationKind(value) {
  if (value == null) return "";
  if (typeof value === "string") return String(value || "").trim();
  if (typeof value === "object") {
    return String(
      value?.type || value?.kind || value?.action || value?.name || "",
    ).trim();
  }
  return "";
}

function normalizeFileOperationPath(path) {
  return String(path || "")
    .trim()
    .replace(/\/home\/[^/\s]+/g, "/home/[user]")
    .replace(/\s+/g, " ");
}

function extractSummaryFromAny(value) {
  if (value == null) return "";
  if (typeof value === "string") {
    const text = String(value || "").trim();
    if (!text) return "";
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
    return "";
  }
  if (typeof value === "object") {
    for (const key of [
      "final_message",
      "message",
      "summary",
      "text",
      "status",
    ]) {
      const text = extractSummaryFromAny(value?.[key]);
      if (text) return text;
    }
    for (const key of [
      "result",
      "output",
      "data",
      "response",
      "agents_states",
    ]) {
      const text = extractSummaryFromAny(value?.[key]);
      if (text) return text;
    }
    for (const nested of Object.values(value)) {
      const text = extractSummaryFromAny(nested);
      if (text) return text;
    }
  }
  return "";
}

function parseFileOperationPayload(payload) {
  if (!payload || typeof payload !== "object") return "";

  const eventType = normalizeAppServerEventType(payload?.type);
  const item =
    payload?.item && typeof payload.item === "object" ? payload.item : null;
  const itemType = normalizeAppServerItemType(item?.type);

  if (eventType.includes("compact") || itemType.includes("compact")) {
    const rawSummary = extractSummaryFromAny(item || payload);
    if (rawSummary) return `Context compacted\n  └ ${rawSummary}`;
    return "Context compacted";
  }

  if (itemType === "file_read" || eventType === "file_read") {
    const path = String(item?.path || payload?.path || "").trim();
    return path ? `Read: ${path}` : "Read file";
  }

  const fileChange = itemType === "file_change" || eventType === "file_change";
  if (!fileChange) return "";

  const changes = Array.isArray(item?.changes)
    ? item.changes
    : Array.isArray(payload?.changes)
      ? payload.changes
      : [];
  const targets = [];
  let primaryAction = "";
  for (const change of changes) {
    const action = normalizeFileOperationAction(
      extractFileOperationKind(change?.kind) ||
        extractFileOperationKind(change?.action) ||
        extractFileOperationKind(change?.type),
    );
    if (!primaryAction && action) {
      primaryAction = action;
    }
    let target = String(
      change?.path || change?.file || change?.target || "",
    ).trim();
    const added = Number(
      change?.added_lines || change?.lines_added || change?.insertions || 0,
    );
    const deleted = Number(
      change?.deleted_lines || change?.lines_deleted || change?.deletions || 0,
    );
    if (target && (added > 0 || deleted > 0)) {
      target += ` (+${added} -${deleted})`;
    }
    if (target) targets.push(target);
  }

  const action = primaryAction || "Edited";
  const uniqueTargets = [...new Set(targets)];
  if (uniqueTargets.length === 0) return action;
  if (uniqueTargets.length === 1) return `${action}: ${uniqueTargets[0]}`;
  return `${action}\n  └ ${uniqueTargets.slice(0, 3).join("\n  └ ")}${uniqueTargets.length > 3 ? `\n  └ +${uniqueTargets.length - 3} more` : ""}`;
}

function extractLikelyAgentIDs(raw) {
  const text = String(raw || "").trim();
  if (!text) return [];
  const uuidPattern =
    /\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi;
  const out = [];
  for (const match of text.matchAll(uuidPattern)) {
    const v = String(match?.[0] || "").trim();
    if (v) out.push(v);
  }
  if (out.length > 0) return [...new Set(out)];
  const tokens = text
    .split(/[,\s]+/)
    .map((item) => String(item || "").trim())
    .filter(Boolean);
  for (const token of tokens) {
    if (/^agent-[a-z0-9_-]{4,}$/i.test(token)) {
      out.push(token);
      continue;
    }
    if (
      token.length >= 20 &&
      (token.match(/-/g) || []).length >= 2 &&
      /^[a-z0-9_-]+$/i.test(token)
    ) {
      out.push(token);
    }
  }
  return [...new Set(out)];
}

function normalizeSubagentRole(raw) {
  const text = String(raw || "").trim();
  if (!text) return "";
  if (extractLikelyAgentIDs(text).length > 0) return "";
  const low = text.toLowerCase();
  const blocked = new Set([
    "collab_tool_call",
    "tool_call",
    "command_execution",
    "exec_command",
    "spawn_agent",
    "wait_agent",
    "wait",
    "send_input",
    "resume_agent",
    "close_agent",
    "item.started",
    "item.updated",
    "item.completed",
    "tool.started",
    "tool.completed",
    "tool.call.started",
    "tool.call.completed",
  ]);
  if (blocked.has(low)) return "";
  return text;
}

function extractSubagentIdentityFromText(raw) {
  const text = String(raw || "").trim();
  if (!text) return { nickname: "", agentType: "", ids: [] };
  const ids = extractLikelyAgentIDs(text);
  const spawnMatch = text.match(/spawned\s+(.+?)(?:\s+\[([^\]]+)\])?(?:$|\n)/i);
  let nickname = String(spawnMatch?.[1] || "").trim();
  let agentType = String(spawnMatch?.[2] || "").trim();
  if (!nickname) {
    nickname = String(
      text.match(/(?:^|\n)\s*(?:name|nickname)\s*:\s*([^\n]+)/i)?.[1] || "",
    ).trim();
  }
  if (!agentType) {
    agentType = String(
      text.match(/(?:^|\n)\s*(?:role|agent[_\s-]?type)\s*:\s*([^\n]+)/i)?.[1] ||
        "",
    ).trim();
  }
  if (
    /^subagent$/i.test(nickname) ||
    extractLikelyAgentIDs(nickname).length > 0
  ) {
    nickname = "";
  }
  if (!nickname) {
    const statusName = text.match(
      /(?:^|\n)\s*([A-Za-z][A-Za-z0-9_-]{2,})\s+status\s*:/i,
    );
    nickname = String(statusName?.[1] || "").trim();
  }
  return { nickname, agentType: normalizeSubagentRole(agentType), ids };
}

function normalizeSubagentToolFamily(raw) {
  const tool = String(raw || "")
    .trim()
    .toLowerCase();
  if (tool === "wait_agent" || tool === "wait") return "wait";
  if (tool === "spawn_agent") return "spawn";
  if (tool === "send_input") return "send_input";
  if (tool === "resume_agent") return "resume_agent";
  if (tool === "close_agent") return "close_agent";
  return tool;
}

function cleanSubagentDetailText(raw, knownIDs = []) {
  const text = String(raw || "").trim();
  if (!text) return "";
  const idSet = new Set(
    (Array.isArray(knownIDs) ? knownIDs : [])
      .map((item) => String(item || "").trim())
      .filter(Boolean),
  );
  const lines = text
    .split("\n")
    .map((line) => String(line || "").trim())
    .filter(Boolean);
  const filtered = [];
  for (const line of lines) {
    if (idSet.has(line)) continue;
    if (
      extractLikelyAgentIDs(line).length === 1 &&
      line.split(/\s+/).length === 1
    )
      continue;
    if (filtered.length > 0 && filtered[filtered.length - 1] === line) continue;
    filtered.push(line);
  }
  return filtered.join("\n").trim();
}

function formatSubagentFallbackName(ids = [], targetID = "") {
  const firstID = [
    ...(Array.isArray(ids) ? ids : []),
    String(targetID || "").trim(),
  ].find((item) => String(item || "").trim());
  const id = String(firstID || "").trim();
  if (!id) return "";
  const fallbackNames = [
    "Huygens",
    "Curie",
    "Gauss",
    "Noether",
    "Lovelace",
    "Turing",
    "Faraday",
    "Kepler",
    "Hypatia",
    "Ramanujan",
    "Euler",
    "Bohr",
    "Fermi",
    "Mendel",
    "Tesla",
    "Darwin",
    "Sagan",
    "Franklin",
    "Pasteur",
    "Planck",
    "Dirac",
    "Raman",
    "Hopper",
    "Shannon",
  ];
  const compact = id.includes("-") ? id.replace(/-/g, "") : id;
  let hash = 0;
  for (let i = 0; i < compact.length; i += 1) {
    hash = (hash * 31 + compact.charCodeAt(i)) >>> 0;
  }
  const picked = fallbackNames[hash % fallbackNames.length];
  return String(picked || "").trim();
}

function isFallbackSubagentName(raw) {
  const value = String(raw || "").trim();
  if (!value) return false;
  if (/^agent-[a-z0-9_-]{4,}$/i.test(value)) return true;
  const fallbackNames = new Set([
    "huygens",
    "curie",
    "gauss",
    "noether",
    "lovelace",
    "turing",
    "faraday",
    "kepler",
    "hypatia",
    "ramanujan",
    "euler",
    "bohr",
    "fermi",
    "mendel",
    "tesla",
    "darwin",
    "sagan",
    "franklin",
    "pasteur",
    "planck",
    "dirac",
    "raman",
    "hopper",
    "shannon",
  ]);
  return fallbackNames.has(value.toLowerCase());
}

function inferSubagentRoleFromText(...inputs) {
  const text = inputs
    .map((value) =>
      String(value || "")
        .trim()
        .toLowerCase(),
    )
    .filter(Boolean)
    .join("\n");
  if (!text) return "";

  const explicitRole = text.match(
    /\b(frontend-developer|react-specialist|nextjs-developer|vue-expert|svelte-specialist|ui-fixer|backend-developer|api-designer|fullstack-developer|code-mapper|browser-debugger|javascript-pro|typescript-pro|golang-pro|sql-pro|code-reviewer|debugger|qa-expert|test-automator|security-auditor|performance-engineer|accessibility-tester|api-documenter|seo-specialist|build-engineer|dependency-manager|deployment-engineer|docker-expert|kubernetes-specialist|websocket-engineer)\b/i,
  );
  if (explicitRole?.[1])
    return String(explicitRole[1] || "")
      .trim()
      .toLowerCase();

  if (/\bsvelte|sveltekit\b/i.test(text)) return "svelte-specialist";
  if (/\bnext\.?js|app router|pages router\b/i.test(text))
    return "nextjs-developer";
  if (/\breact|jsx|tsx\b/i.test(text)) return "react-specialist";
  if (/\bvue|nuxt\b/i.test(text)) return "vue-expert";
  if (/\btypescript|tsc|type\b/i.test(text)) return "typescript-pro";
  if (/\bjavascript|node\b/i.test(text)) return "javascript-pro";
  if (/\bsql|query|database|migration|postgres|mysql|sqlite\b/i.test(text))
    return "sql-pro";
  if (/\bgolang|\bgo\b|goroutine|go service\b/i.test(text)) return "golang-pro";
  if (/\bsecurity|vuln|owasp|xss|csrf|auth\b/i.test(text))
    return "security-auditor";
  if (/\btest|qa|e2e|playwright|vitest|regression\b/i.test(text))
    return "qa-expert";
  if (/\breview|bug|risk|scan|audit|risky assumptions\b/i.test(text))
    return "code-reviewer";
  if (/\bapi|backend|service|httpapi\b/i.test(text)) return "backend-developer";
  if (/\bui|layout|css|frontend\b/i.test(text)) return "frontend-developer";
  return "";
}

function fileOperationDisplayParts(label) {
  const text = String(label || "").trim();
  if (!text) return { action: "", path: "", stats: "" };
  const m = text.match(
    /^(Edited|Read|Created|Deleted|Moved|Renamed):\s+(.+?)(?:\s+\(([^)]+)\))?$/i,
  );
  if (!m) return { action: text, path: "", stats: "" };
  return {
    action: String(m[1] || "").trim(),
    path: String(m[2] || "").trim(),
    stats: String(m[3] || "").trim(),
  };
}

function normalizeMessageLoadSource(source) {
  const value = String(source || "")
    .trim()
    .toLowerCase();
  if (value === "snapshot" || value === "canonical") return "canonical";
  return "raw";
}

function normalizedFileOperationLabel(message) {
  if (!message || typeof message !== "object") return "";
  const explicit = String(message?.file_op || "").trim();
  if (explicit) return explicit;
  return parseFileOperationText(message?.content || "");
}

function fileOperationDedupKey(message) {
  const label = normalizedFileOperationLabel(message);
  if (!label) return "";
  const parts = fileOperationDisplayParts(label);
  if (!parts.action || !parts.path) return "";
  return `${parts.action.toLowerCase()}|${normalizeFileOperationPath(parts.path).toLowerCase()}`;
}

function choosePreferredSubagentName(incoming = "", existing = "") {
  const next = String(incoming || "").trim();
  const prev = String(existing || "").trim();
  if (next && !isFallbackSubagentName(next)) return next;
  if (prev && !isFallbackSubagentName(prev)) return prev;
  return next || prev;
}

function choosePreferredSubagentRole(incoming = "", existing = "") {
  const next = String(incoming || "").trim();
  const prev = String(existing || "").trim();
  if (next && next.toLowerCase() !== "subagent") return next;
  if (prev && prev.toLowerCase() !== "subagent") return prev;
  return next || prev;
}

function buildSubagentMergeKey(toolName, details = {}) {
  const name = String(toolName || "")
    .trim()
    .toLowerCase();
  const ids = Array.isArray(details?.ids)
    ? [
        ...new Set(
          details.ids.map((item) => String(item || "").trim()).filter(Boolean),
        ),
      ].sort()
    : [];
  const targetID = String(details?.targetID || "").trim();
  const nickname = String(details?.nickname || "").trim();
  const prompt = String(details?.prompt || "").trim();
  const summary = String(details?.summary || "").trim();
  const callID = String(details?.callID || "").trim();
  const idSeed = ids[0] || targetID;
  if (name === "spawn_agent") {
    return normalizeActivityCommandKey(
      `spawn|${idSeed || nickname || prompt || summary || callID}`,
    );
  }
  if (name === "wait_agent" || name === "wait") {
    return normalizeActivityCommandKey(
      `wait|${ids.join(",") || targetID || nickname || prompt || summary || callID}`,
    );
  }
  if (name === "send_input") {
    return normalizeActivityCommandKey(
      `send_input|${targetID || ids[0] || prompt || callID}`,
    );
  }
  if (name === "resume_agent") {
    return normalizeActivityCommandKey(
      `resume_agent|${targetID || ids[0] || callID}`,
    );
  }
  if (name === "close_agent") {
    return normalizeActivityCommandKey(
      `close_agent|${targetID || ids[0] || callID}`,
    );
  }
  return normalizeActivityCommandKey(
    `${name}|${callID || idSeed || prompt || summary}`,
  );
}

function buildSubagentLifecycleKey(details = {}) {
  const ids = Array.isArray(details?.ids)
    ? [
        ...new Set(
          details.ids.map((item) => String(item || "").trim()).filter(Boolean),
        ),
      ].sort()
    : [];
  const targetID = String(details?.targetID || "").trim();
  const nickname = String(details?.nickname || "").trim();
  const callID = String(details?.callID || "").trim();
  const prompt = String(details?.prompt || "").trim();
  const summary = String(details?.summary || "").trim();
  const seed = callID || ids[0] || targetID || nickname || prompt || summary;
  return normalizeActivityCommandKey(`subagent|${seed}`);
}

function normalizeSubagentPromptKey(raw) {
  const text = String(raw || "")
    .trim()
    .toLowerCase()
    .replace(/\.{3,}/g, " ")
    .replace(/[^\p{L}\p{N}\s_-]+/gu, " ")
    .replace(/\s+/g, " ");
  return normalizeActivityCommandKey(text);
}

function parseToolArguments(raw) {
  if (raw == null) return {};
  if (typeof raw === "object" && !Array.isArray(raw)) return raw;
  const text = String(raw || "").trim();
  if (!text || (!text.startsWith("{") && !text.startsWith("["))) return {};
  try {
    const parsed = JSON.parse(text);
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed))
      return parsed;
  } catch {}
  return {};
}

function extractStringList(value) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => String(item || "").trim()).filter(Boolean);
}

export {
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
  isFallbackSubagentName,
  normalizeActivityCommandKey,
  normalizeExecIdentityCommandKey,
  normalizeExecOutputSource,
  normalizeExecStatus,
  normalizeLegacyRuntimeActor,
  normalizeLegacyRuntimeLane,
  normalizeFileOperationAction,
  normalizeMessageLoadSource,
  normalizeSubagentRole,
  normalizeSubagentToolFamily,
  normalizeSubagentPromptKey,
  normalizedFileOperationLabel,
  parseMCPActivityPayload,
  parseRuntimeRecoveryActivity,
  parseToolArguments,
  parseActivityText,
  parseFileOperationPayload,
  parseFileOperationText,
  resolveLegacyRuntimeOwnership,
};
