function nextDashboardPageSize(value, currentSize, pageSizeOptions) {
  const raw = Number(value)
  const next = pageSizeOptions.includes(raw) ? raw : 20
  return { next, changed: next !== currentSize }
}

function nextDashboardPage(value, currentPage) {
  const n = Number(value)
  const next = Number.isFinite(n) && n > 0 ? Math.floor(n) : 1
  return { next, changed: next !== currentPage }
}

function nextFilterValue(value, currentValue) {
  const next = String(value ?? '')
  return { next, changed: next !== currentValue }
}

export {
  nextDashboardPage,
  nextDashboardPageSize,
  nextFilterValue
}
