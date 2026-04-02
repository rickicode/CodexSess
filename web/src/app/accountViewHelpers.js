function activeAPIAccount(accounts) {
  return accounts.find((account) => account?.active_api) || null;
}

function activeCLIAccount(accounts) {
  return accounts.find((account) => account?.active_cli) || null;
}

function accountDisplayLabel(account, showAccountEmail) {
  if (!account) return 'Not selected';
  return showAccountEmail ? (account.email || account.id || '-') : (account.id || '-');
}

function accountTypeOptions(accounts, normalizeAccountTypeLabel) {
  const values = new Set();
  for (const account of accounts) {
    const v = String(account?.plan_type || '').trim().toLowerCase();
    if (v) values.add(v);
  }
  const opts = [...values].sort((a, b) => a.localeCompare(b)).map((value) => ({
    value,
    label: normalizeAccountTypeLabel(value)
  }));
  return [{ value: 'all', label: 'All Account Types' }, ...opts];
}

function accountUsageSortScore(account, parseUsageWindows, clampPercent) {
  const windows = parseUsageWindows(account?.usage || null);
  if (!Array.isArray(windows) || windows.length === 0) return -1;
  const scoreFromWindow = (window) => {
    const raw = Number(window?.percent);
    if (!Number.isFinite(raw)) return -1;
    return clampPercent(raw);
  };
  const hourly = windows.find((window) => String(window?.name || '').trim().toLowerCase().startsWith('5h'));
  const hourlyScore = hourly ? scoreFromWindow(hourly) : -1;
  if (hourlyScore >= 0) return hourlyScore;
  const weekly = windows.find((window) => String(window?.name || '').trim().toLowerCase().includes('weekly'));
  const weeklyScore = weekly ? scoreFromWindow(weekly) : -1;
  if (weeklyScore >= 0) return weeklyScore;
  return -1;
}

function paginatedAccounts(accounts, totalFilteredAccounts, dashboardPageSize, dashboardPage) {
  const totalPages = Math.max(1, Math.ceil(totalFilteredAccounts / (dashboardPageSize > 0 ? dashboardPageSize : 20)));
  let page = dashboardPage;
  if (page > totalPages) page = totalPages;
  if (page < 1) page = 1;

  const perPage = dashboardPageSize > 0 ? dashboardPageSize : 20;
  const start = (page - 1) * perPage;
  const end = Math.min(start + accounts.length, totalFilteredAccounts);

  return {
    items: accounts,
    totalFiltered: totalFilteredAccounts,
    totalPages,
    page,
    perPage,
    startIndex: accounts.length === 0 ? 0 : start + 1,
    endIndex: end
  };
}

export {
  accountDisplayLabel,
  accountTypeOptions,
  accountUsageSortScore,
  activeAPIAccount,
  activeCLIAccount,
  paginatedAccounts
};
