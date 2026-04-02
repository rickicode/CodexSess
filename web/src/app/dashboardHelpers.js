function pageSizeOptions() {
  return [20, 50, 100, 500, 1000];
}

function accountStatusOptions() {
  return [
    { value: 'all', label: 'All Token Status' },
    { value: 'revoked', label: 'Revoked (401)' },
    { value: 'not_revoked', label: 'Not Revoked' }
  ];
}

function usageAvailabilityOptions() {
  return [
    { value: 'all', label: 'All Usage' },
    { value: 'exhausted', label: 'Exhausted (Weekly or 5h)' },
    { value: 'available', label: 'Has Remaining Usage' }
  ];
}

function formatResetTimestamp(dateObj) {
  const mm = String(dateObj.getMonth() + 1).padStart(2, '0');
  const dd = String(dateObj.getDate()).padStart(2, '0');
  const hh = String(dateObj.getHours()).padStart(2, '0');
  const mi = String(dateObj.getMinutes()).padStart(2, '0');
  return `${mm}/${dd} ${hh}:${mi}`;
}

function formatRemaining(diffMs) {
  const totalMinutes = Math.max(0, Math.floor(diffMs / 60000));
  const days = Math.floor(totalMinutes / (60 * 24));
  const hours = Math.floor((totalMinutes % (60 * 24)) / 60);
  const mins = totalMinutes % 60;
  if (days > 0) return mins > 0 ? `${days}d ${hours}h ${mins}m` : `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}

function formatResetLabel(resetAtISO, nowMs = Date.now()) {
  if (!resetAtISO) return '-';
  const target = new Date(resetAtISO);
  if (Number.isNaN(target.getTime())) return '-';
  const diffMs = target.getTime() - nowMs;
  if (diffMs <= 0) return `reset (${formatResetTimestamp(target)})`;
  return `${formatRemaining(diffMs)} (${formatResetTimestamp(target)})`;
}

export {
  accountStatusOptions,
  formatRemaining,
  formatResetLabel,
  formatResetTimestamp,
  pageSizeOptions,
  usageAvailabilityOptions
};
