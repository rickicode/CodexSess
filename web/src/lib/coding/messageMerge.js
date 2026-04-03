import { normalizeLegacyRuntimeLane } from './activityParsing.js'

function timestampFromMessage(message) {
  const candidates = [
    String(message?.created_at || '').trim(),
    String(message?.updated_at || '').trim()
  ]
  for (const raw of candidates) {
    if (!raw) continue
    const ms = Date.parse(raw)
    if (!Number.isNaN(ms)) return ms
  }
  return 0
}

function sequenceFromMessage(message) {
  const value = Number(
    message?.sequence ??
      message?.seq ??
      message?.view_seq ??
      message?.sort_seq ??
      message?.sort_index ??
      0
  )
  if (Number.isFinite(value) && value > 0) return value
  const id = String(message?.id || '').trim()
  const idSuffix = id.match(/-(\d{3,})$/)
  if (idSuffix?.[1]) {
    const parsed = Number(idSuffix[1])
    if (Number.isFinite(parsed) && parsed > 0) return parsed
  }
  return 0
}

function eventSequenceFromMessage(message) {
  const value = Number(message?.event_seq ?? message?.eventSeq ?? 0)
  if (Number.isFinite(value) && value > 0) return value
  return 0
}

function sortMessagesChronologically(input) {
  const src = Array.isArray(input) ? input : []
  return src
    .map((item, idx) => ({ item, idx }))
    .sort((a, b) => {
      const sa = sequenceFromMessage(a.item)
      const sb = sequenceFromMessage(b.item)
      if (sa > 0 && sb > 0 && sa !== sb) return sa - sb
      const ea = eventSequenceFromMessage(a.item)
      const eb = eventSequenceFromMessage(b.item)
      if (ea > 0 && eb > 0 && ea !== eb) return ea - eb
      const ta = timestampFromMessage(a.item)
      const tb = timestampFromMessage(b.item)
      if (ta !== tb) return ta - tb
      return a.idx - b.idx
    })
    .map((entry) => entry.item)
}

function mergeMessagesChronologically(currentMessages, incomingMessages) {
  const existing = Array.isArray(currentMessages) ? currentMessages : []
  const incoming = Array.isArray(incomingMessages) ? incomingMessages : []
  if (incoming.length === 0) return existing
  const merged = [...existing]
  const known = new Set(
    existing.map((item) => String(item?.id || '').trim()).filter(Boolean)
  )
  for (const item of incoming) {
    const id = String(item?.id || '').trim()
    if (id && known.has(id)) continue
    if (id) known.add(id)
    merged.push(item)
  }
  return sortMessagesChronologically(merged)
}

function mergeMessagesPreferringIncoming(currentMessages, incomingMessages) {
  const current = Array.isArray(currentMessages) ? currentMessages : []
  const incoming = Array.isArray(incomingMessages) ? incomingMessages : []
  if (incoming.length === 0) return current
  const incomingByID = new Map()
  for (const item of incoming) {
    const id = String(item?.id || '').trim()
    if (id) incomingByID.set(id, item)
  }
  const merged = []
  const seen = new Set()
  for (const item of current) {
    const id = String(item?.id || '').trim()
    if (id && incomingByID.has(id)) {
      merged.push(incomingByID.get(id))
      seen.add(id)
      continue
    }
    if (id) seen.add(id)
    merged.push(item)
  }
  for (const item of incoming) {
    const id = String(item?.id || '').trim()
    if (id && seen.has(id)) continue
    if (id) seen.add(id)
    merged.push(item)
  }
  return sortMessagesChronologically(merged)
}

function ensureUniqueMessageIDs(ids = []) {
  return [...new Set((Array.isArray(ids) ? ids : []).map((id) => String(id || '').trim()).filter(Boolean))]
}

function normalizeMessageMatchPart(value) {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/\s+/g, ' ')
}

function normalizeSourceItemType(value) {
  let itemType = String(value || '').trim().toLowerCase()
  switch (itemType) {
    case 'agentmessage':
    case 'agent_message':
      itemType = 'agentmessage'
      break
    case 'commandexecution':
    case 'command_execution':
      itemType = 'commandexecution'
      break
    case 'filechange':
    case 'file_change':
      itemType = 'filechange'
      break
    default:
      break
  }
  return itemType
}

function sourceIdentityKey(item) {
  const role = String(item?.role || '').trim().toLowerCase()
  const itemID = String(item?.source_item_id ?? item?.sourceItemID ?? '').trim()
  if (!role || !itemID) return ''
  const turnID = String(item?.source_turn_id ?? item?.sourceTurnID ?? '').trim()
  const itemType = normalizeSourceItemType(item?.source_item_type ?? item?.sourceItemType ?? '')
  return `${role}|${turnID}|${itemID}|${itemType}`
}

function messageMatchKey(item) {
  const role = String(item?.role || '').trim().toLowerCase()
  if (role === 'subagent') {
    const lifecycleKey = normalizeMessageMatchPart(item?.subagent_lifecycle_key || item?.subagent_key)
    if (lifecycleKey) return `${role}|${lifecycleKey}`
    const tool = normalizeMessageMatchPart(item?.subagent_tool)
    const identity = normalizeMessageMatchPart(
      item?.subagent_name ||
        item?.subagent_nickname ||
        item?.subagent_target_id ||
        item?.subagent_title
    )
    const prompt = normalizeMessageMatchPart(item?.subagent_prompt)
    const summary = normalizeMessageMatchPart(item?.subagent_summary)
    const content = normalizeMessageMatchPart(item?.content)
    return `${role}|${tool}|${identity || prompt || summary || content}`
  }
  const content = normalizeMessageMatchPart(item?.content)
  return `${role}|${content}`
}

function normalizeExecCommandKey(input) {
  return String(input || '')
    .trim()
    .toLowerCase()
    .replace(/\s+/g, ' ')
}

function execReplacementIndex(current, persistedRow, usedIndexes = new Set()) {
  const persistedRole = String(persistedRow?.role || '').trim().toLowerCase()
  if (persistedRole !== 'exec') return -1
  const persistedCommand = normalizeExecCommandKey(
    persistedRow?.exec_command || persistedRow?.content || '',
  )
  if (!persistedCommand) return -1

  const persistedActor = String(persistedRow?.actor || '').trim().toLowerCase()
  const persistedLane = normalizeLegacyRuntimeLane(persistedRow?.lane || persistedActor)
  const persistedTS = timestampFromMessage(persistedRow)
  const maxWindowMs = 4000

  for (let idx = current.length - 1; idx >= 0; idx -= 1) {
    if (usedIndexes.has(idx)) continue
    const row = current[idx]
    if (String(row?.role || '').trim().toLowerCase() !== 'exec') continue
    const currentCommand = normalizeExecCommandKey(
      row?.exec_command || row?.content || '',
    )
    if (!currentCommand || currentCommand !== persistedCommand) continue
    const currentActor = String(row?.actor || '').trim().toLowerCase()
    const currentLane = normalizeLegacyRuntimeLane(row?.lane || currentActor)
    if (persistedLane && currentLane && persistedLane !== currentLane) continue

    const currentTS = timestampFromMessage(row)
    if (persistedTS > 0 && currentTS > 0 && Math.abs(currentTS - persistedTS) > maxWindowMs) continue
    return idx
  }
  return -1
}

function reconcileLiveMessagesWithPersisted(currentMessages, persistedMessages, liveMessageIDs = []) {
  const current = Array.isArray(currentMessages) ? [...currentMessages] : []
  const persisted = Array.isArray(persistedMessages) ? persistedMessages : []
  if (persisted.length === 0) return current
  const liveSet = new Set((Array.isArray(liveMessageIDs) ? liveMessageIDs : []).map((id) => String(id || '').trim()).filter(Boolean))
  if (liveSet.size === 0) {
    const replaced = new Set()
    const appendOnly = []
    for (const row of persisted) {
      const replaceIdx = execReplacementIndex(current, row, replaced)
      if (replaceIdx >= 0) {
        current[replaceIdx] = row
        replaced.add(replaceIdx)
        continue
      }
      appendOnly.push(row)
    }
    return mergeMessagesChronologically(current, appendOnly)
  }

  const liveIndexByIdentity = new Map()
  const liveIndexByLegacyKey = new Map()
  const matchWindowMs = 4000
  for (let idx = 0; idx < current.length; idx += 1) {
    const row = current[idx]
    const id = String(row?.id || '').trim()
    if (!id || !liveSet.has(id)) continue
    const candidate = { idx, ts: timestampFromMessage(row) }
    const identity = sourceIdentityKey(row)
    if (identity) {
      const bucket = liveIndexByIdentity.get(identity) || []
      bucket.push(candidate)
      liveIndexByIdentity.set(identity, bucket)
      continue
    }
    const key = messageMatchKey(row)
    if (!key) continue
    const bucket = liveIndexByLegacyKey.get(key) || []
    bucket.push(candidate)
    liveIndexByLegacyKey.set(key, bucket)
  }

  const unmatchedPersisted = []
  const replacedIndexes = new Set()
  for (const row of persisted) {
    const identity = sourceIdentityKey(row)
    const key = messageMatchKey(row)
    const bucket = identity
      ? (liveIndexByIdentity.get(identity) || liveIndexByLegacyKey.get(key) || [])
      : (liveIndexByLegacyKey.get(key) || [])
    if (bucket.length > 0) {
      const persistedTS = timestampFromMessage(row)
      let pickAt = 0
      if (persistedTS > 0) {
        for (let i = 0; i < bucket.length; i += 1) {
          const candidateTS = Number(bucket[i]?.ts || 0)
          if (candidateTS <= 0) continue
          if (Math.abs(candidateTS - persistedTS) <= matchWindowMs) {
            pickAt = i
            break
          }
        }
        const firstTS = Number(bucket[0]?.ts || 0)
        if (firstTS > 0 && Math.abs(firstTS - persistedTS) > matchWindowMs && pickAt === 0) {
          unmatchedPersisted.push(row)
          continue
        }
      }
      const [picked] = bucket.splice(pickAt, 1)
      const idx = Number(picked?.idx ?? -1)
      if (idx >= 0 && idx < current.length) {
        current[idx] = row
        continue
      }
      unmatchedPersisted.push(row)
      continue
    }
    const replaceIdx = execReplacementIndex(current, row, replacedIndexes)
    if (replaceIdx >= 0) {
      current[replaceIdx] = row
      replacedIndexes.add(replaceIdx)
      continue
    }
    unmatchedPersisted.push(row)
  }
  return mergeMessagesChronologically(current, unmatchedPersisted)
}

export {
  ensureUniqueMessageIDs,
  mergeMessagesChronologically,
  mergeMessagesPreferringIncoming,
  messageMatchKey,
  reconcileLiveMessagesWithPersisted,
  eventSequenceFromMessage,
  sequenceFromMessage,
  sortMessagesChronologically,
  timestampFromMessage
}
