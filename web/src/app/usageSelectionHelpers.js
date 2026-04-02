function getPrimaryUsageWindow(account, parseUsageWindows, clampPercent) {
  const windows = parseUsageWindows(account?.usage)
  const fiveHour = windows.find((w) => String(w.name || '').trim().toLowerCase().startsWith('5h'))
  const weekly = windows.find((w) => String(w.name || '').trim().toLowerCase().includes('weekly'))
  if (fiveHour) return { type: '5h', window: fiveHour }
  if (weekly) return { type: 'weekly', window: weekly }
  if (windows.length > 0) return { type: 'weekly', window: windows[0] }
  return null
}

function getActiveAccount(accounts) {
  return accounts.find((account) => account?.active_api || account?.active) || null
}

function backupUsageCandidates(accounts, currentActiveID, { getPrimaryUsageWindow, clampPercent }) {
  return accounts
    .filter((account) => account?.id && account.id !== currentActiveID)
    .map((account) => {
      const metric = getPrimaryUsageWindow(account)
      return {
        account,
        percent: metric ? clampPercent(metric.window?.percent) : -1
      }
    })
    .filter((item) => item.percent >= 0)
    .sort((a, b) => b.percent - a.percent)
}

export {
  backupUsageCandidates,
  getActiveAccount,
  getPrimaryUsageWindow
}
