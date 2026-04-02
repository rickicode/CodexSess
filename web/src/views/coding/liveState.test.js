import test from "node:test";
import assert from "node:assert/strict";

import { findActiveLiveMessageID } from "./liveState.js";

test("live selection only highlights the last active message", () => {
  const renderedMessages = [
    { id: "assistant-old", role: "assistant", pending: true },
    { id: "activity-last", role: "activity", content: "Orchestrator gathering context" },
  ];
  const activeID = findActiveLiveMessageID(
    renderedMessages,
    (message) => String(message?.content || "").includes("Orchestrator gathering context"),
  );
  assert.equal(activeID, "activity-last");
});

test("planning gathering stops being live once a newer message appears", () => {
  const renderedMessages = [
    { id: "activity-old", role: "activity", content: "Orchestrator gathering context" },
    { id: "assistant-new", role: "assistant", content: "Orchestrator update" },
  ];
  const activeID = findActiveLiveMessageID(
    renderedMessages,
    (message) => String(message?.content || "").includes("Orchestrator gathering context"),
  );
  assert.equal(activeID, "");
});

test("live selection prefers the newest running runtime bubble over an older pending assistant", () => {
  const renderedMessages = [
    { id: "assistant-pending", role: "assistant", pending: true },
    { id: "exec-running", role: "exec", exec_status: "running" },
  ];
  const activeID = findActiveLiveMessageID(renderedMessages);
  assert.equal(activeID, "exec-running");
});
