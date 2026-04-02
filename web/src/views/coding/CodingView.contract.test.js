import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

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
});
