function postClientEvent({ type, message, data = {}, level = 'info', toAPIURL, jsonHeaders, fetchImpl = fetch }) {
  const payload = {
    type: String(type || 'event'),
    source: 'web-console',
    level: String(level || 'info'),
    message: String(message || ''),
    data: (data && typeof data === 'object') ? data : {}
  }
  return fetchImpl(toAPIURL('/api/events/log'), {
    method: 'POST',
    headers: jsonHeaders,
    credentials: 'same-origin',
    body: JSON.stringify(payload)
  }).catch(() => {})
}

async function requestJSON(url, options = {}, { toAPIURL, jsonHeaders, fetchImpl = fetch, windowObj = typeof window !== 'undefined' ? window : undefined } = {}) {
  const targetURL = toAPIURL(url)
  const response = await fetchImpl(targetURL, {
    headers: jsonHeaders,
    ...options
  })
  if (response.redirected && String(response.url || '').includes('/auth/login')) {
    if (windowObj) {
      windowObj.location.href = '/auth/login'
    }
    throw new Error('Authentication required')
  }
  const bodyText = await response.text()
  if (response.ok) {
    const contentType = String(response.headers.get('content-type') || '').toLowerCase()
    if (contentType.includes('text/html') && /<html/i.test(bodyText) && /login/i.test(bodyText)) {
      if (windowObj) {
        windowObj.location.href = '/auth/login'
      }
      throw new Error('Authentication required')
    }
  }
  let body = {}
  try {
    body = JSON.parse(bodyText || '{}')
  } catch {
    body = {}
  }
  if (!response.ok) {
    const msg = typeof body?.error === 'string'
      ? body.error
      : (body?.error?.message || body?.message || bodyText || `HTTP ${response.status}`)
    throw new Error(msg)
  }
  return body
}

export {
  postClientEvent,
  requestJSON
}
