function formatWhen(value) {
  const d = new Date(String(value || ''))
  if (Number.isNaN(d.getTime())) return '-'

  const now = new Date()
  const isToday =
    d.getDate() === now.getDate() &&
    d.getMonth() === now.getMonth() &&
    d.getFullYear() === now.getFullYear()

  const yesterday = new Date(now)
  yesterday.setDate(yesterday.getDate() - 1)
  const isYesterday =
    d.getDate() === yesterday.getDate() &&
    d.getMonth() === yesterday.getMonth() &&
    d.getFullYear() === yesterday.getFullYear()

  const timeOptions = { hour: 'numeric', minute: '2-digit', second: '2-digit' }

  if (isToday) {
    return d.toLocaleTimeString(undefined, timeOptions)
  }
  if (isYesterday) {
    return 'Yesterday, ' + d.toLocaleTimeString(undefined, timeOptions)
  }

  const dateOptions = { month: 'short', day: 'numeric' }
  if (d.getFullYear() !== now.getFullYear()) {
    dateOptions.year = 'numeric'
  }

  return d.toLocaleDateString(undefined, dateOptions) + ', ' + d.toLocaleTimeString(undefined, timeOptions)
}

export {
  formatWhen
}
