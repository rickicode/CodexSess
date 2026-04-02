function isLoopbackHost(hostname) {
  const host = String(hostname || '').trim().toLowerCase()
  return host === '127.0.0.1' || host === 'localhost' || host === '::1' || host === '[::1]'
}

function resolvedAPIBase(apiBase, windowObj = typeof window !== 'undefined' ? window : undefined) {
  if (!apiBase) return ''
  if (!windowObj) return apiBase
  try {
    const parsed = new URL(apiBase, windowObj.location.origin)
    if (isLoopbackHost(parsed.hostname) && !isLoopbackHost(windowObj.location.hostname)) {
      return ''
    }
    return `${parsed.origin}`.replace(/\/+$/, '')
  } catch {
    return apiBase
  }
}

function toAPIURL(url, apiBase, windowObj = typeof window !== 'undefined' ? window : undefined) {
  const raw = String(url || '').trim()
  if (/^https?:\/\//i.test(raw)) return raw
  const base = resolvedAPIBase(apiBase, windowObj)
  if (!base) return raw
  if (raw.startsWith('/')) return `${base}${raw}`
  return `${base}/${raw}`
}

function toWSURL(path, apiBase, windowObj = typeof window !== 'undefined' ? window : undefined) {
  const raw = String(path || '').trim()
  const base = toAPIURL(raw, apiBase, windowObj)
  try {
    const u = new URL(base, windowObj ? windowObj.location.origin : undefined)
    if (u.protocol === 'https:') u.protocol = 'wss:'
    else if (u.protocol === 'http:') u.protocol = 'ws:'
    return u.toString()
  } catch {
    if (windowObj) {
      const proto = windowObj.location.protocol === 'https:' ? 'wss:' : 'ws:'
      return `${proto}//${windowObj.location.host}${raw.startsWith('/') ? raw : `/${raw}`}`
    }
    return raw
  }
}

function buildWSURLCandidates(path, apiBase, windowObj = typeof window !== 'undefined' ? window : undefined) {
  const raw = String(path || '').trim()
  const out = []
  if (windowObj) {
    const proto = windowObj.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const sameOrigin = `${proto}//${windowObj.location.host}${raw.startsWith('/') ? raw : `/${raw}`}`
    out.push(sameOrigin)
  }
  const fromBase = toWSURL(raw, apiBase, windowObj)
  if (fromBase && !out.includes(fromBase)) {
    out.push(fromBase)
  }
  return out.filter(Boolean)
}

async function requestJSON(url, options = {}, { apiBase, jsonHeaders, fetchImpl = fetch, windowObj = typeof window !== 'undefined' ? window : undefined } = {}) {
  const response = await fetchImpl(toAPIURL(url, apiBase, windowObj), {
    headers: jsonHeaders,
    credentials: 'same-origin',
    ...options
  })
  if (response.redirected && String(response.url || '').includes('/auth/login')) {
    if (windowObj) windowObj.location.href = '/auth/login'
    throw new Error('Authentication required')
  }
  const text = await response.text()
  let body = {}
  try {
    body = JSON.parse(text || '{}')
  } catch {
    body = {}
  }
  if (!response.ok) {
    const message = body?.error?.message || body?.message || text || `HTTP ${response.status}`
    throw new Error(message)
  }
  return body
}

export {
  buildWSURLCandidates,
  isLoopbackHost,
  requestJSON,
  resolvedAPIBase,
  toAPIURL,
  toWSURL
}
