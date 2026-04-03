import test from "node:test";
import assert from "node:assert/strict";

import { completeSendFlow } from "./sendCompletion.js";

test("completeSendFlow merges user and assistant rows and applies success state", async () => {
  const calls = [];
  await completeSendFlow({
    donePayload: {
      user: { id: "user-1" },
      session: { id: "sess-1", last_message_at: "2026-04-03T10:00:01Z" },
    },
    sessionID: "sess-1",
    pendingID: "pending-1",
    liveAssistantActor: "",
    sendStartedAt: "2026-04-03T10:00:00Z",
    draftStoragePrefix: "draft:",
    messageActor: () => "",
    applyRunStatePatch: (patch) => calls.push(["patch", patch]),
    createSendSuccessState: ({ viewStatus }) => ({ viewStatus, composerError: "" }),
    mergePendingUserMessage: (pendingID, user) => calls.push(["mergePendingUserMessage", pendingID, user.id]),
    removePendingUserMessage: (pendingID) => calls.push(["removePendingUserMessage", pendingID]),
    mergeDoneAssistantRows: () => calls.push(["mergeDoneAssistantRows"]),
    clearSessionDraft: (prefix, sessionID) => calls.push(["clearSessionDraft", prefix, sessionID]),
    clearCompactSnapshotPersistTimer: () => calls.push(["clearCompactSnapshotPersistTimer"]),
    updateSessions: (session) => calls.push(["updateSessions", session.id]),
    syncComposerControlsFromSession: (session, options) => calls.push(["syncComposerControlsFromSession", session.id, options.preserveMode]),
    loadMessages: async (sid) => calls.push(["loadMessages", sid]),
    hasPendingAssistantPlaceholder: () => false,
    hasVisibleOutcomeAfterLatestUser: () => true,
    hasSettledAssistantSince: () => true,
    waitForSettledVisibleOutcome: async () => calls.push(["waitForSettledVisibleOutcome"]),
    tick: async () => calls.push(["tick"]),
    scrollMessagesToBottom: () => calls.push(["scrollMessagesToBottom"]),
    completedViewStatus: () => "Completed.",
    messages: [],
  });

  assert.deepEqual(calls, [
    ["mergePendingUserMessage", "pending-1", "user-1"],
    ["mergeDoneAssistantRows"],
    ["clearSessionDraft", "draft:", "sess-1"],
    ["clearCompactSnapshotPersistTimer"],
    ["updateSessions", "sess-1"],
    ["syncComposerControlsFromSession", "sess-1", true],
    ["loadMessages", "sess-1"],
    ["tick"],
    ["scrollMessagesToBottom"],
    ["patch", { viewStatus: "Completed.", composerError: "" }],
  ]);
});

test("completeSendFlow removes pending message and waits when visible outcome is not ready", async () => {
  const calls = [];
  await completeSendFlow({
    donePayload: {},
    sessionID: "sess-2",
    pendingID: "pending-2",
    liveAssistantActor: "chat",
    sendStartedAt: "2026-04-03T10:00:00Z",
    messageActor: () => "",
    applyRunStatePatch: () => {},
    createSendSuccessState: ({ viewStatus }) => ({ viewStatus }),
    mergePendingUserMessage: () => calls.push(["mergePendingUserMessage"]),
    removePendingUserMessage: (pendingID) => calls.push(["removePendingUserMessage", pendingID]),
    mergeDoneAssistantRows: () => calls.push(["mergeDoneAssistantRows"]),
    clearSessionDraft: () => calls.push(["clearSessionDraft"]),
    clearCompactSnapshotPersistTimer: () => calls.push(["clearCompactSnapshotPersistTimer"]),
    updateSessions: () => calls.push(["updateSessions"]),
    syncComposerControlsFromSession: () => calls.push(["syncComposerControlsFromSession"]),
    loadMessages: async () => calls.push(["loadMessages"]),
    hasPendingAssistantPlaceholder: () => true,
    hasVisibleOutcomeAfterLatestUser: () => false,
    hasSettledAssistantSince: () => false,
    waitForSettledVisibleOutcome: async (sid, options) => calls.push(["waitForSettledVisibleOutcome", sid, options.actor]),
    tick: async () => calls.push(["tick"]),
    scrollMessagesToBottom: () => calls.push(["scrollMessagesToBottom"]),
    completedViewStatus: () => "Completed.",
    messages: [],
  });

  assert.deepEqual(calls, [
    ["removePendingUserMessage", "pending-2"],
    ["mergeDoneAssistantRows"],
    ["clearSessionDraft"],
    ["clearCompactSnapshotPersistTimer"],
    ["loadMessages"],
    ["waitForSettledVisibleOutcome", "sess-2", "chat"],
    ["tick"],
    ["scrollMessagesToBottom"],
  ]);
});
