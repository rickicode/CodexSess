async function completeSendFlow({
  donePayload,
  sessionID = "",
  pendingID = "",
  liveAssistantActor = "",
  sendStartedAt = "",
  historyExpandedManually = false,
  draftStoragePrefix = "",
  messageActor,
  applyRunStatePatch = () => {},
  createSendSuccessState,
  mergePendingUserMessage = () => {},
  removePendingUserMessage = () => {},
  mergeDoneAssistantRows = () => {},
  clearSessionDraft = () => {},
  clearCompactSnapshotPersistTimer = () => {},
  updateSessions = () => {},
  syncComposerControlsFromSession = () => {},
  loadMessages = async () => {},
  hasPendingAssistantPlaceholder = () => false,
  hasVisibleOutcomeAfterLatestUser = () => true,
  hasSettledAssistantSince = () => true,
  waitForSettledVisibleOutcome = async () => {},
  delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
  tick = async () => {},
  scrollMessagesToBottom = () => {},
  completedViewStatus,
  messages = [],
} = {}) {
  const userMessage = donePayload?.user;
  if (userMessage) {
    mergePendingUserMessage(pendingID, userMessage);
  } else {
    removePendingUserMessage(pendingID);
  }

  mergeDoneAssistantRows(donePayload);
  clearSessionDraft(draftStoragePrefix, sessionID);
  clearCompactSnapshotPersistTimer();

  if (donePayload?.session?.id) {
    updateSessions(donePayload.session);
    syncComposerControlsFromSession(donePayload.session, { preserveMode: true });
  }

  await loadMessages(sessionID, { silent: true, preserveViewport: false, preserveLoadedHistory: historyExpandedManually });
  if (hasPendingAssistantPlaceholder(liveAssistantActor) || !hasVisibleOutcomeAfterLatestUser(messages)) {
    await waitForSettledVisibleOutcome(sessionID, { actor: liveAssistantActor });
  } else if (!hasSettledAssistantSince(sendStartedAt, liveAssistantActor)) {
    await delay(250);
    await loadMessages(sessionID, { silent: true, preserveViewport: false, preserveLoadedHistory: historyExpandedManually });
  }

  await tick();
  scrollMessagesToBottom();
  applyRunStatePatch(createSendSuccessState({
    viewStatus: completedViewStatus(messages, { messageActor }),
  }));
}

export {
  completeSendFlow,
};
