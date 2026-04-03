function findAssistantStreamTargetIndex(messages = [], { actor = "", assistantKey = "" } = {}) {
  const normalizedActor = String(actor || "").trim().toLowerCase();
  const normalizedAssistantKey = String(assistantKey || "").trim();
  const list = Array.isArray(messages) ? messages : [];

  if (normalizedAssistantKey) {
    for (let idx = list.length - 1; idx >= 0; idx -= 1) {
      const candidate = list[idx];
      if (!candidate) continue;
      if (String(candidate?.role || "").trim().toLowerCase() !== "assistant") continue;
      if (String(candidate?.stream_identity_key || "").trim() !== normalizedAssistantKey) continue;
      return idx;
    }
    return -1;
  }

  for (let idx = list.length - 1; idx >= 0; idx -= 1) {
    const candidate = list[idx];
    if (!candidate) continue;
    if (String(candidate?.role || "").trim().toLowerCase() !== "assistant") continue;
    const candidateActor = String(candidate?.actor || "").trim().toLowerCase();
    if (candidateActor !== normalizedActor) continue;
    if (candidate?.pending) return idx;
  }

  return -1;
}

export { findAssistantStreamTargetIndex };
