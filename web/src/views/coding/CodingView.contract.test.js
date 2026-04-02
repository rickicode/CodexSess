import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { reconcileLiveMessagesWithPersisted } from "../../lib/coding/messageMerge.js";
import { buildExecAwareMessages } from "./liveMessagePipeline.js";
import { collectLiveMessageIDs } from "./messageView.js";

const codingViewSource = readFileSync(
  new URL("../CodingView.svelte", import.meta.url),
  "utf8",
);
const codingTopbarSource = readFileSync(
  new URL("./CodingTopbar.svelte", import.meta.url),
  "utf8",
);
const codingComposerSource = readFileSync(
  new URL("./CodingComposer.svelte", import.meta.url),
  "utf8",
);
const codingMessagesPaneSource = readFileSync(
  new URL("./CodingMessagesPane.svelte", import.meta.url),
  "utf8",
);
const codingStatusLineSource = readFileSync(
  new URL("./CodingStatusLine.svelte", import.meta.url),
  "utf8",
);
const appCssSource = readFileSync(
  new URL("../../app.css", import.meta.url),
  "utf8",
);

test("CodingView keeps one message pane and no dual-lane markup", () => {
  const messagePaneCount = Array.from(
    codingViewSource.matchAll(/\n\s*<CodingMessagesPane\b/g),
  ).length;

  assert.equal(messagePaneCount, 1);
  assert.doesNotMatch(codingViewSource, /coding-legacy-lanes/);
  assert.doesNotMatch(codingViewSource, /coding-lane-header/);
  assert.doesNotMatch(codingViewSource, /Legacy Owner lane/);
  assert.doesNotMatch(codingViewSource, /Working lane/);
  assert.match(codingViewSource, /<CodingTopbar\b/);
  assert.match(codingViewSource, /<CodingComposer\b/);
  assert.match(codingViewSource, /<CodingSessionDrawer\b/);
  assert.match(codingViewSource, /<CodingSkillModal\b/);
});

test("CodingView removes dead legacy lane state from the main chat view", () => {
  assert.doesNotMatch(codingViewSource, /\bactiveRunnerRole\b/);
  assert.doesNotMatch(codingViewSource, /\bsessionIntentSwitching\b/);
  assert.doesNotMatch(codingViewSource, /\bkickoffPlanningSessionID\b/);
  assert.doesNotMatch(codingViewSource, /\bplanningDraftSessionID\b/);
  assert.doesNotMatch(codingViewSource, /\bsessionHasAutopilotLaneEvidence\b/);
  assert.doesNotMatch(codingViewSource, /\bshouldKeepWaitingForAutopilotReply\b/);
  assert.doesNotMatch(codingViewSource, /\bisAutopilotChatHandoffInFlight\b/);
  assert.doesNotMatch(codingViewSource, /\bwaitForHandoffStopFinalization\b/);
  assert.doesNotMatch(codingViewSource, /\brunnerBadgeLabel\b/);
  assert.doesNotMatch(codingViewSource, /\bcomposerRunnerBadge\b/);
  assert.doesNotMatch(codingViewSource, /\bpredictedRunnerRole\b/);
  assert.doesNotMatch(codingViewSource, /\blast_mode_transition_summary\b/);
  assert.doesNotMatch(codingViewSource, /\btransitionStatusLabel\b/);
});

test("chat chrome uses concise single-timeline copy", () => {
  assert.match(codingTopbarSource, /<strong>Codex Chat<\/strong>/);
  assert.match(codingComposerSource, /Ask Codex to inspect, edit, or verify the workspace\./);
  assert.match(codingMessagesPaneSource, /Show earlier messages/);
  assert.doesNotMatch(codingTopbarSource, /CodexSess Coding/);
  assert.doesNotMatch(codingComposerSource, /Write coding task here/);
  assert.doesNotMatch(codingMessagesPaneSource, /Load earlier messages/);
});

test("chat stylesheet drops leftover legacy lane selectors", () => {
  assert.doesNotMatch(appCssSource, /\.legacy-toggle-btn\b/);
  assert.doesNotMatch(appCssSource, /\.coding-view-mode-toggle\b/);
  assert.doesNotMatch(appCssSource, /\.coding-runner-badge\b/);
  assert.doesNotMatch(appCssSource, /\.coding-legacy-chip\b/);
});

test("force stop does not surface websocket detached background as a composer error", () => {
  assert.match(
    codingViewSource,
    /const stopDrivenDetach = detachedBackground && \(stopRequested \|\| expectedWSDetach\);[\s\S]*if \(busy \|\| detachedBackground\) \{[\s\S]*if \(inFlight\) \{[\s\S]*viewStatus = stopDrivenDetach \? \(forceStopArmed \? 'Force stopping\.\.\.' : 'Stopping\.\.\.'\) : 'Streaming\.\.\.';[\s\S]*\} else if \(stopDrivenDetach\) \{[\s\S]*composerLockedUntilAssistant = false;[\s\S]*composerError = '';[\s\S]*viewStatus = 'Stopped\.';[\s\S]*backgroundProcessing = false;/,
  );
  assert.match(
    codingViewSource,
    /let effectiveViewStatus = \$derived\.by\(\(\) => \{[\s\S]*const recoveryStatus = currentRecoveryStatus\(messages\);[\s\S]*if \(sending \|\| backgroundProcessing\) \{[\s\S]*return recoveryStatus \|\| 'Streaming\.\.\.';/,
  );
});

test("streaming status line reuses coding-streaming-note markup", () => {
  assert.match(codingStatusLineSource, /coding-streaming-note/);
  assert.doesNotMatch(codingStatusLineSource, /status-streaming-pulse/);
  assert.doesNotMatch(codingStatusLineSource, /status-streaming-label/);
  assert.doesNotMatch(codingStatusLineSource, /status-streaming-dots/);
});

test("message pane keeps streaming note outside assistant bubbles", () => {
  assert.match(codingMessagesPaneSource, /\{#if \(sending \|\| backgroundProcessing \|\| streamingPending\)/);
  assert.doesNotMatch(codingMessagesPaneSource, /coding-streaming-note coding-streaming-inline/);
});

test("composer keeps controls in a footer below the textarea with send pinned as the right-side primary action", () => {
  assert.match(codingComposerSource, /coding-composer-body/);
  assert.match(codingComposerSource, /coding-composer-footer/);
  assert.match(codingComposerSource, /coding-composer-secondary-actions/);
  assert.match(codingComposerSource, /coding-composer-actions/);
  assert.doesNotMatch(codingComposerSource, /coding-composer-rail/);
  assert.match(appCssSource, /\.coding-composer-body\b/);
  assert.match(appCssSource, /\.coding-composer-footer\b/);
  assert.match(appCssSource, /\.coding-composer-secondary-actions\b/);
  assert.match(appCssSource, /\.coding-composer-footer\s*\{[\s\S]*border-top:/);
  assert.match(appCssSource, /\.coding-composer-actions \.btn\s*\{[\s\S]*border:\s*0/);
  assert.match(appCssSource, /\.btn-send\s*\{[\s\S]*margin-left:\s*auto/);
});

test("contract view preserves live order after snapshot replacement", () => {
  const projectedLive = buildExecAwareMessages([
    {
      id: "stream-raw-exec-1",
      role: "event",
      actor: "",
      lane: "",
      source_event_type: "item/completed",
      source_thread_id: "thread-chat-1",
      source_turn_id: "turn-chat-1",
      source_item_id: "item-exec-1",
      source_item_type: "commandexecution",
      event_seq: 3001,
      content: JSON.stringify({
        method: "item/completed",
        params: {
          item: {
            type: "commandExecution",
            command: "pwd-a",
            aggregatedOutput: "/tmp/a",
            exitCode: 0,
          },
        },
      }),
      created_at: "2026-04-02T13:00:00.000Z",
      updated_at: "2026-04-02T13:00:00.000Z",
    },
    {
      id: "stream-raw-exec-2",
      role: "event",
      actor: "",
      lane: "",
      source_event_type: "item/completed",
      source_thread_id: "thread-chat-1",
      source_turn_id: "turn-chat-1",
      source_item_id: "item-exec-2",
      source_item_type: "commandexecution",
      event_seq: 3002,
      content: JSON.stringify({
        method: "item/completed",
        params: {
          item: {
            type: "commandExecution",
            command: "pwd-b",
            aggregatedOutput: "/tmp/b",
            exitCode: 0,
          },
        },
      }),
      created_at: "2026-04-02T13:00:00.010Z",
      updated_at: "2026-04-02T13:00:00.010Z",
    },
  ]);

  const persistedRows = [
    {
      id: "db-exec-1",
      role: "exec",
      content: "pwd-a",
      exec_command: "pwd-a",
      exec_status: "done",
      exec_exit_code: 0,
      exec_output: "/tmp/a",
      source_event_type: "item/completed",
      source_thread_id: "thread-chat-1",
      source_turn_id: "turn-chat-1",
      source_item_id: "item-exec-1",
      source_item_type: "commandexecution",
      event_seq: 3001,
      created_at: "2026-04-02T13:00:00.100Z",
      updated_at: "2026-04-02T13:00:00.100Z",
    },
    {
      id: "db-exec-2",
      role: "exec",
      content: "pwd-b",
      exec_command: "pwd-b",
      exec_status: "done",
      exec_exit_code: 0,
      exec_output: "/tmp/b",
      source_event_type: "item/completed",
      source_thread_id: "thread-chat-1",
      source_turn_id: "turn-chat-1",
      source_item_id: "item-exec-2",
      source_item_type: "commandexecution",
      event_seq: 3002,
      created_at: "2026-04-02T13:00:00.110Z",
      updated_at: "2026-04-02T13:00:00.110Z",
    },
  ];

  assert.deepEqual(
    projectedLive.map((row) => row?.source_item_id),
    ["item-exec-1", "item-exec-2"],
  );
  const merged = reconcileLiveMessagesWithPersisted(
    projectedLive,
    persistedRows,
    collectLiveMessageIDs(projectedLive),
  );
  assert.deepEqual(
    merged.map((row) => row?.source_item_id),
    ["item-exec-1", "item-exec-2"],
  );
});
