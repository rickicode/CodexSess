async function cancelStreamingFlow({
  sending = false,
  backgroundProcessing = false,
  forceStopArmed = false,
  sessionID = "",
  requestStop,
  closeActiveStream = () => {},
  waitForBackgroundSettle = async () => false,
  startBackgroundMonitor = () => {},
  applyRunStatePatch = () => {},
  createStopRequestState,
  createStopRejectedState,
  createStopSettledState,
} = {}) {
  if (!sending && !backgroundProcessing) return "noop";

  const force = Boolean(forceStopArmed);
  applyRunStatePatch(createStopRequestState({ force }));

  const sid = String(sessionID || "").trim();
  if (sid) {
    const stopResponse = await requestStop(sid, force);
    if (stopResponse && stopResponse.stopped === false) {
      applyRunStatePatch(createStopRejectedState());
      return "rejected";
    }
  }

  closeActiveStream();

  if (!sid) {
    return "stopping";
  }

  const settled = await waitForBackgroundSettle(sid, {
    timeoutMs: force ? 3000 : 1800,
    intervalMs: 120,
  }).catch(() => false);

  if (settled) {
    applyRunStatePatch(createStopSettledState());
    return "settled";
  }

  startBackgroundMonitor(sid, { intervalMs: 400 });
  return "monitoring";
}

export {
  cancelStreamingFlow,
};
