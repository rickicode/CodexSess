<script>
  let {
    busy,
    systemLogs,
    systemLogsTotal,
    onLoadSystemLogs,
    onClearSystemLogs,
    onOpenSystemLogDetail,
    formatLogTimestamp
  } = $props();

  let query = $state('');
  let kindFilter = $state('all');

  const kindOptions = $derived.by(() => {
    return ['all', ...Array.from(new Set((systemLogs || []).map((e) => String(e.kind || '').trim()).filter(Boolean)))];
  });
  const filteredLogs = $derived.by(() => {
    return (systemLogs || []).filter((entry) => {
      const kind = String(entry.kind || '').trim();
      if (kindFilter !== 'all' && kind !== kindFilter) return false;
      const needle = query.trim().toLowerCase();
      if (!needle) return true;
      const hay = `${entry.kind || ''} ${entry.message || ''} ${entry.metaJSON || ''}`.toLowerCase();
      return hay.includes(needle);
    });
  });
</script>

<section class="logs-panel">
  <div class="panel-header panel-header-inline logs-header">
    <h2>System Logs</h2>
    <div class="panel-actions">
      <button class="btn btn-secondary" onclick={onLoadSystemLogs} disabled={busy}>Refresh</button>
      <button class="btn btn-danger" onclick={onClearSystemLogs} disabled={busy}>Clear</button>
    </div>
  </div>
  <p class="panel-note logs-note">Tracks account switches and manual usage refresh actions.</p>
  <p class="panel-meta">Total: {systemLogsTotal}</p>

  <div class="system-logs-filters">
    <input
      aria-label="Search system logs"
      placeholder="Search logs (message/meta/kind)"
      value={query}
      oninput={(event) => (query = event.currentTarget.value)}
      disabled={busy}
    />
    <select
      aria-label="Filter log kind"
      value={kindFilter}
      onchange={(event) => (kindFilter = event.currentTarget.value)}
      disabled={busy}
    >
      {#each kindOptions as kindOption}
        <option value={kindOption}>{kindOption === 'all' ? 'All kinds' : kindOption}</option>
      {/each}
    </select>
  </div>

  <div class="logs-box">
    {#if filteredLogs.length === 0}
      <p class="empty-note">No system logs yet.</p>
    {:else}
      {#each filteredLogs as entry (entry.id)}
        <article class="log-row">
          <div class="log-main">
            <div class="log-topline">
              <code class="log-path">{entry.kind}</code>
              <span class="log-method">system</span>
            </div>
            <p class="log-subline">
              <span>{formatLogTimestamp(entry.createdAt)}</span>
              <span>{entry.message}</span>
            </p>
          </div>
          <div class="inline-actions">
            <button class="btn btn-small btn-secondary" onclick={() => onOpenSystemLogDetail(entry)}>Detail</button>
          </div>
        </article>
      {/each}
    {/if}
  </div>
</section>
