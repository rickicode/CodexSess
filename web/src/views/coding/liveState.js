function findActiveLiveMessageID(renderedMessages = [], isInternalRunnerActivity = () => false) {
  const lastIndex = Array.isArray(renderedMessages) ? renderedMessages.length - 1 : -1;
  for (let idx = renderedMessages.length - 1; idx >= 0; idx -= 1) {
    const message = renderedMessages[idx];
    const role = String(message?.role || "").trim().toLowerCase();
    if (role === "assistant" && message?.pending) {
      return String(message?.id || "").trim();
    }
    if (role === "exec" && String(message?.exec_status || "").trim().toLowerCase() === "running") {
      return String(message?.id || "").trim();
    }
    if (role === "subagent" && String(message?.subagent_status || "").trim().toLowerCase() === "running") {
      return String(message?.id || "").trim();
    }
    if (role === "activity" && idx === lastIndex && isInternalRunnerActivity(message)) {
      return String(message?.id || "").trim();
    }
  }
  return "";
}

export { findActiveLiveMessageID };
