import test from "node:test";
import assert from "node:assert/strict";

import {
  computeEffectiveViewStatus,
  computeStopButtonLabel,
  createSendFailureState,
  createSendFinallyState,
  createSendSuccessState,
  createRunStartState,
  createStopRejectedState,
  createStopRequestState,
  createStopSettledState,
  deriveRunPhase,
} from "./runStateMachine.js";

test("deriveRunPhase distinguishes idle streaming stopping and force stopping", () => {
  assert.equal(deriveRunPhase({}), "idle");
  assert.equal(deriveRunPhase({ sending: true }), "streaming");
  assert.equal(deriveRunPhase({ backgroundProcessing: true }), "streaming");
  assert.equal(deriveRunPhase({ sending: true, stopRequested: true }), "stopping");
  assert.equal(
    deriveRunPhase({ backgroundProcessing: true, stopRequested: true, forceStopArmed: true }),
    "force_stopping",
  );
});

test("computeStopButtonLabel maps run phases to composer button labels", () => {
  assert.equal(computeStopButtonLabel({}), "Send");
  assert.equal(computeStopButtonLabel({ sending: true }), "Stop");
  assert.equal(
    computeStopButtonLabel({ sending: true, stopRequested: true, forceStopArmed: true }),
    "Force Stop",
  );
});

test("computeEffectiveViewStatus prefers stopping then recovery then idle status", () => {
  assert.equal(
    computeEffectiveViewStatus({ sending: true, recoveryStatus: "Retrying with another account..." }),
    "Retrying with another account...",
  );
  assert.equal(
    computeEffectiveViewStatus({ sending: true, stopRequested: true }),
    "Stopping...",
  );
  assert.equal(
    computeEffectiveViewStatus({ viewStatus: "Ready." }),
    "Ready.",
  );
});

test("createRunStartState initializes the streaming lifecycle flags", () => {
  assert.deepEqual(createRunStartState(), {
    sending: true,
    stopRequested: false,
    forceStopArmed: false,
    expectedWSDetach: false,
    backgroundProcessing: false,
    streamingPending: true,
    composerLockedUntilAssistant: true,
    viewStatus: "Streaming...",
  });
});

test("stop transition helpers cover arm reject and settled states", () => {
  assert.deepEqual(createStopRequestState({ force: false }), {
    stopRequested: true,
    forceStopArmed: true,
    viewStatus: "Stopping... press Force Stop to kill immediately.",
  });
  assert.deepEqual(createStopRequestState({ force: true }), {
    stopRequested: true,
    forceStopArmed: true,
    viewStatus: "Force stopping...",
  });
  assert.deepEqual(createStopRejectedState(), {
    stopRequested: false,
    forceStopArmed: false,
    expectedWSDetach: false,
    viewStatus: "No active run to stop.",
  });
  assert.deepEqual(createStopSettledState(), {
    sending: false,
    streamingPending: false,
    backgroundProcessing: false,
    composerLockedUntilAssistant: false,
    forceStopArmed: false,
    stopRequested: false,
    expectedWSDetach: false,
    viewStatus: "Stopped.",
  });
});

test("createSendFailureState handles detached background in-flight and terminal failure cases", () => {
  assert.deepEqual(
    createSendFailureState({
      detachedBackground: true,
      stopDrivenDetach: true,
      forceStopArmed: true,
      inFlight: true,
    }),
    {
      composerLockedUntilAssistant: true,
      composerError: "",
      backgroundProcessing: true,
      viewStatus: "Force stopping...",
      monitorAfterSend: true,
    },
  );

  assert.deepEqual(
    createSendFailureState({
      stopRequested: true,
    }),
    {
      composerLockedUntilAssistant: false,
      composerError: "",
      backgroundProcessing: false,
      viewStatus: "Stopped.",
      monitorAfterSend: false,
    },
  );

  assert.deepEqual(
    createSendFailureState({
      aborted: true,
      failReason: "ignored",
    }),
    {
      composerLockedUntilAssistant: false,
      composerError: "",
      backgroundProcessing: false,
      viewStatus: "Streaming canceled.",
      monitorAfterSend: false,
    },
  );
});

test("send success and finally helpers reset the expected lifecycle flags", () => {
  assert.deepEqual(createSendSuccessState({ viewStatus: "Completed." }), {
    streamingPending: false,
    composerLockedUntilAssistant: false,
    composerError: "",
    viewStatus: "Completed.",
  });

  assert.deepEqual(createSendFinallyState({ stopRequested: false, monitorAfterSend: true }), {
    sending: false,
    streamingPending: false,
    stopRequested: false,
    forceStopArmed: false,
  });

  assert.deepEqual(createSendFinallyState({ stopRequested: true, monitorAfterSend: false }), {
    sending: false,
    streamingPending: false,
    backgroundProcessing: false,
    stopRequested: false,
    forceStopArmed: false,
  });
});
