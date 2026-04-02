function sortSessionsByRecency(items) {
  const list = Array.isArray(items) ? items : []
  return [...list].sort((a, b) => String(b?.last_message_at || '').localeCompare(String(a?.last_message_at || '')))
}

function sessionDisplayID(session) {
  return String(session?.thread_id || session?.id || '-').trim() || '-'
}

function readSessionIDFromURL(windowObj = typeof window !== 'undefined' ? window : undefined) {
  if (!windowObj) return ''
  try {
    const url = new URL(windowObj.location.href)
    return String(url.searchParams.get('id') || '').trim()
  } catch {
    return ''
  }
}

function syncSessionIDToURL(sessionID, windowObj = typeof window !== 'undefined' ? window : undefined) {
  if (!windowObj) return
  const sid = String(sessionID || '').trim()
  try {
    const url = new URL(windowObj.location.href)
    if (sid) {
      url.searchParams.set('id', sid)
    } else {
      url.searchParams.delete('id')
    }
    windowObj.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`)
  } catch {
  }
}

function draftStorageKey(storagePrefix, sessionID) {
  const sid = String(sessionID || '').trim()
  if (!sid) return ''
  return `${String(storagePrefix || '')}${sid}`
}

function saveDraftForSession(storagePrefix, sessionID, text, storage = typeof localStorage !== 'undefined' ? localStorage : undefined) {
  if (!storage) return
  const key = draftStorageKey(storagePrefix, sessionID)
  if (!key) return
  const value = String(text || '')
  try {
    if (!value.trim()) {
      storage.removeItem(key)
      return
    }
    storage.setItem(key, value)
  } catch {
  }
}

function loadDraftForSession(storagePrefix, sessionID, storage = typeof localStorage !== 'undefined' ? localStorage : undefined) {
  if (!storage) return ''
  const key = draftStorageKey(storagePrefix, sessionID)
  if (!key) return ''
  try {
    return String(storage.getItem(key) || '')
  } catch {
    return ''
  }
}

function clearDraftForSession(storagePrefix, sessionID, storage = typeof localStorage !== 'undefined' ? localStorage : undefined) {
  if (!storage) return
  const key = draftStorageKey(storagePrefix, sessionID)
  if (!key) return
  try {
    storage.removeItem(key)
  } catch {
  }
}

export {
  clearDraftForSession,
  draftStorageKey,
  loadDraftForSession,
  readSessionIDFromURL,
  saveDraftForSession,
  sessionDisplayID,
  sortSessionsByRecency,
  syncSessionIDToURL
}
