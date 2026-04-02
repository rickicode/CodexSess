function clearIntervalTimer(timerHandle) {
  if (timerHandle) {
    clearInterval(timerHandle)
  }
  return null
}

function cancelBrowserLoginSessionRequest({ browserLoginURL, browserLoginID, jsonHeaders, navigatorObj = typeof navigator !== 'undefined' ? navigator : undefined, fetchImpl = fetch }) {
  if (!browserLoginURL && !browserLoginID) return
  const payload = JSON.stringify({ login_id: browserLoginID || null })
  try {
    if (navigatorObj && typeof navigatorObj.sendBeacon === 'function') {
      const blob = new Blob([payload], { type: 'application/json' })
      navigatorObj.sendBeacon('/api/auth/browser/cancel', blob)
      return
    }
  } catch {
    // fallback to fetch
  }
  fetchImpl('/api/auth/browser/cancel', {
    method: 'POST',
    headers: jsonHeaders,
    body: payload,
    keepalive: true
  }).catch(() => {})
}

export {
  cancelBrowserLoginSessionRequest,
  clearIntervalTimer
}
