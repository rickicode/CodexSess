function normalizeReasoningLevel(value) {
  const v = String(value || '').trim().toLowerCase()
  if (v === 'low' || v === 'high') return v
  return 'medium'
}

export {
  normalizeReasoningLevel
}
