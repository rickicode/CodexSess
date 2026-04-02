function normalizeThresholdInput(value, currentValue, parseFn) {
  const normalized = parseFn(value, currentValue)
  return {
    value: normalized,
    input: String(normalized)
  }
}

function commitThresholdInput(nextValue, currentInput, currentValue, parseFn) {
  const sourceValue = nextValue === null || nextValue === undefined ? currentInput : nextValue
  return normalizeThresholdInput(sourceValue, currentValue, parseFn)
}

function nudgeThresholdInput(currentValue, delta, parseFn) {
  return normalizeThresholdInput(Number(currentValue) + Number(delta), currentValue, parseFn)
}

function buildQueuedThresholdPayload({ usageAlertThreshold, usageAutoSwitchThreshold, usageSchedulerIntervalMinutes, changedType, parsePercentInput, parseSchedulerIntervalInput }) {
  return {
    alertThreshold: parsePercentInput(usageAlertThreshold, 5),
    autoSwitchThreshold: parsePercentInput(usageAutoSwitchThreshold, 2),
    schedulerIntervalMinutes: parseSchedulerIntervalInput(usageSchedulerIntervalMinutes, 60),
    changedType
  }
}

function shouldSkipThresholdPersist(next, lastSaved) {
  return (
    lastSaved.alert === next.alertThreshold &&
    lastSaved.switch === next.autoSwitchThreshold &&
    lastSaved.interval === next.schedulerIntervalMinutes
  )
}

function thresholdPersistSuccessMessage(changedType, { savedAlert, savedSwitch, savedInterval }) {
  if (changedType === 'alert') {
    return `Usage alert threshold set to ${savedAlert}%.`
  }
  if (changedType === 'interval') {
    return `Scheduler interval set to ${savedInterval} minutes.`
  }
  return `Usage auto-switch threshold set to ${savedSwitch}%.`
}

export {
  buildQueuedThresholdPayload,
  commitThresholdInput,
  normalizeThresholdInput,
  nudgeThresholdInput,
  shouldSkipThresholdPersist,
  thresholdPersistSuccessMessage
}
