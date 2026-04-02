function markCopiedState(key, { getCopiedAction, setCopiedAction, getResetTimer, setResetTimer, clearTimer = clearTimeout, setDelay = setTimeout, delayMs = 1300 }) {
  if (!key) return
  setCopiedAction(key)
  const activeTimer = getResetTimer()
  if (activeTimer) {
    clearTimer(activeTimer)
    setResetTimer(null)
  }
  const nextTimer = setDelay(() => {
    if (getCopiedAction() === key) setCopiedAction('')
    setResetTimer(null)
  }, delayMs)
  setResetTimer(nextTimer)
}

function isCopiedState(currentKey, key) {
  return currentKey === key
}

async function copyTextWithFeedback(value, label, key, { writeClipboardText, markCopied, setStatus }) {
  const text = String(value || '').trim()
  if (!text) return
  const copied = await writeClipboardText(text)
  if (copied) {
    markCopied(key)
    setStatus(`${label} copied.`, 'success')
  } else {
    setStatus(`Failed to copy ${label}.`, 'error')
  }
}

export {
  copyTextWithFeedback,
  isCopiedState,
  markCopiedState
}
