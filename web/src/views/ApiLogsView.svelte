<script>
  let {
    busy,
    apiLogs,
    onLoadAPILogs,
    onOpenLogDetail,
    formatLogTimestamp,
    logStatusClass
  } = $props();
</script>

<section class="logs-panel">
  <div class="panel-header panel-header-inline logs-header">
    <h2>API Logs</h2>
    <button class="btn btn-secondary" onclick={onLoadAPILogs} disabled={busy}>Refresh</button>
  </div>
  <p class="panel-note logs-note">Only proxy API traffic is logged (OpenAI/Claude). Dashboard requests are excluded.</p>

  <div class="logs-box">
    {#if apiLogs.length === 0}
      <p class="empty-note">No traffic logs yet.</p>
    {:else}
      {#each apiLogs as entry (entry.id)}
        <article class="log-row">
          <div class="log-main">
            <div class="log-topline">
              <code class="log-path">{entry.path}</code>
              <span class="log-method">{entry.method}</span>
              <span class="log-method">{entry.protocol}</span>
            </div>
            <p class="log-subline">
              <span>{formatLogTimestamp(entry.timestamp)}</span>
              <span>{entry.latencyMS} ms</span>
              {#if entry.requestTokens || entry.responseTokens || entry.totalTokens}
                <span>tok {entry.requestTokens || 0}/{entry.responseTokens || 0}/{entry.totalTokens || 0}</span>
              {/if}
              {#if entry.model}
                <span>{entry.model}</span>
              {/if}
              {#if entry.accountEmail || entry.accountID || entry.accountHint}
                <span>{entry.accountEmail || entry.accountID || entry.accountHint}</span>
              {/if}
            </p>
          </div>
          <div class="inline-actions">
            <span class="log-status {logStatusClass(entry.status)}">{entry.status || '-'}</span>
            <button class="btn btn-small btn-secondary" onclick={() => onOpenLogDetail(entry)}>Detail</button>
          </div>
        </article>
      {/each}
    {/if}
  </div>
</section>
