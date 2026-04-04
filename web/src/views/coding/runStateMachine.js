function deriveRunPhase({
  sending = false,
  backgroundProcessing = false,
  stopRequested = false,
  forceStopArmed = false,
} = {}) {
  if (stopRequested && forceStopArmed) return 'force_stopping';
  if (stopRequested) return 'stopping';
  if (sending || backgroundProcessing) return 'streaming';
  return 'idle';
}

function computeStopButtonLabel(state = {}) {
  const phase = deriveRunPhase(state);
  if (phase === 'idle') return 'Send';
  if (phase === 'force_stopping') return 'Force Stop';
  return 'Stop';
}

function computeEffectiveViewStatus(state = {}) {
  const {
    viewStatus = 'Ready.',
    recoveryStatus = '',
  } = state;
  const phase = deriveRunPhase(state);
  if (phase === 'force_stopping' || phase === 'stopping') {
    return 'Stopping...';
  }
  if (phase === 'streaming') {
    return recoveryStatus || 'Streaming...';
  }
  return viewStatus;
}

function createRunStartState() {
  return {
    sending: true,
    stopRequested: false,
    forceStopArmed: false,
    expectedWSDetach: false,
    backgroundProcessing: false,
    streamingPending: true,
    composerLockedUntilAssistant: true,
    viewStatus: 'Streaming...',
  };
}

function createStopRequestState({ force = false } = {}) {
  return {
    stopRequested: true,
    forceStopArmed: true,
    viewStatus: force ? 'Force stopping...' : 'Stopping... press Force Stop to kill immediately.',
  };
}

function createStopRejectedState() {
  return {
    stopRequested: false,
    forceStopArmed: false,
    expectedWSDetach: false,
    viewStatus: 'No active run to stop.',
  };
}

function createStopSettledState() {
  return {
    sending: false,
    streamingPending: false,
    backgroundProcessing: false,
    composerLockedUntilAssistant: false,
    forceStopArmed: false,
    stopRequested: false,
    expectedWSDetach: false,
    viewStatus: 'Stopped.',
  };
}

function createSendFailureState({
  busy = false,
  detachedBackground = false,
  stopDrivenDetach = false,
  forceStopArmed = false,
  inFlight = false,
  stopRequested = false,
  aborted = false,
  failReason = 'Failed to send message.',
} = {}) {
  if (busy || detachedBackground) {
    if (inFlight) {
      return {
        composerLockedUntilAssistant: true,
        composerError: '',
        backgroundProcessing: true,
        viewStatus: stopDrivenDetach
          ? (forceStopArmed ? 'Force stopping...' : 'Stopping...')
          : 'Streaming...',
        monitorAfterSend: true,
      };
    }
    if (stopDrivenDetach) {
      return {
        composerLockedUntilAssistant: false,
        composerError: '',
        backgroundProcessing: false,
        viewStatus: 'Stopped.',
        monitorAfterSend: false,
      };
    }
  }

  if (stopRequested) {
    return {
      composerLockedUntilAssistant: false,
      composerError: '',
      backgroundProcessing: false,
      viewStatus: 'Stopped.',
      monitorAfterSend: false,
    };
  }

  return {
    composerLockedUntilAssistant: false,
    composerError: aborted ? '' : failReason,
    backgroundProcessing: false,
    viewStatus: aborted ? 'Streaming canceled.' : failReason,
    monitorAfterSend: false,
  };
}

function createSendSuccessState({ viewStatus = 'Ready.' } = {}) {
  return {
    streamingPending: false,
    composerLockedUntilAssistant: false,
    composerError: '',
    viewStatus,
  };
}

function createSendFinallyState({
  stopRequested = false,
  monitorAfterSend = false,
} = {}) {
  const next = {
    sending: false,
    streamingPending: false,
    stopRequested: false,
    forceStopArmed: false,
  };
  if (stopRequested || !monitorAfterSend) {
    next.backgroundProcessing = false;
  }
  return next;
}

export {
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
};
