import test from "node:test";
import assert from "node:assert/strict";

import { cancelStreamingFlow } from "./stopOrchestration.js";

test("cancelStreamingFlow returns noop when nothing is running", async () => {
  const result = await cancelStreamingFlow({
    sending: false,
    backgroundProcessing: false,
  });

  assert.equal(result, "noop");
});

test("cancelStreamingFlow rejects stop when backend reports no active run", async () => {
  const patches = [];
  const result = await cancelStreamingFlow({
    sending: true,
    forceStopArmed: false,
    sessionID: "sess-1",
    requestStop: async () => ({ stopped: false }),
    applyRunStatePatch: (patch) => patches.push(patch),
    createStopRequestState: ({ force }) => ({ stopRequested: true, forceStopArmed: force }),
    createStopRejectedState: () => ({ stopRequested: false, forceStopArmed: false, viewStatus: "No active run to stop." }),
    createStopSettledState: () => ({ viewStatus: "Stopped." }),
  });

  assert.equal(result, "rejected");
  assert.deepEqual(patches, [
    { stopRequested: true, forceStopArmed: false },
    { stopRequested: false, forceStopArmed: false, viewStatus: "No active run to stop." },
  ]);
});

test("cancelStreamingFlow settles immediately when background wait succeeds", async () => {
  const patches = [];
  let closed = 0;
  const result = await cancelStreamingFlow({
    sending: true,
    forceStopArmed: true,
    sessionID: "sess-2",
    requestStop: async () => ({ stopped: true }),
    closeActiveStream: () => { closed += 1; },
    waitForBackgroundSettle: async () => true,
    applyRunStatePatch: (patch) => patches.push(patch),
    createStopRequestState: ({ force }) => ({ stopRequested: true, forceStopArmed: force }),
    createStopRejectedState: () => ({ viewStatus: "No active run to stop." }),
    createStopSettledState: () => ({ viewStatus: "Stopped." }),
  });

  assert.equal(result, "settled");
  assert.equal(closed, 1);
  assert.deepEqual(patches, [
    { stopRequested: true, forceStopArmed: true },
    { viewStatus: "Stopped." },
  ]);
});

test("cancelStreamingFlow starts monitoring when stop does not settle immediately", async () => {
  let monitored = null;
  const result = await cancelStreamingFlow({
    sending: false,
    backgroundProcessing: true,
    forceStopArmed: false,
    sessionID: "sess-3",
    requestStop: async () => ({ stopped: true }),
    waitForBackgroundSettle: async () => false,
    startBackgroundMonitor: (sid, options) => { monitored = { sid, options }; },
    createStopRequestState: ({ force }) => ({ stopRequested: true, forceStopArmed: force }),
    createStopRejectedState: () => ({ viewStatus: "No active run to stop." }),
    createStopSettledState: () => ({ viewStatus: "Stopped." }),
  });

  assert.equal(result, "monitoring");
  assert.deepEqual(monitored, {
    sid: "sess-3",
    options: { intervalMs: 400 },
  });
});
