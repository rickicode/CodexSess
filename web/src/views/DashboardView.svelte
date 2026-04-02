<script>
  let {
    apiMode,
    accounts,
    totalAccounts,
    filteredCount,
    page,
    totalPages,
    perPage,
    pageStart,
    pageEnd,
    pageSizeOptions,
    onSetPage,
    onSetPageSize,
    showAccountEmail,
    busy,
    accountSearchQuery,
    accountTypeFilter,
    usageAvailabilityFilter,
    accountStatusFilter,
    accountTypeOptions,
    accountStatusOptions,
    usageAvailabilityOptions,
    onSetAccountSearchQuery,
    onSetAccountTypeFilter,
    onSetUsageAvailabilityFilter,
    onSetAccountStatusFilter,
    onOpenAddAccountModal,
    onBackupAccounts,
    onRestoreAccounts,
    onDeleteRevokedAccounts,
    onUseApiAccount,
    onUseCliAccount,
    onRefreshUsage,
    onOpenRemoveModal,
    usageLabel,
    clampPercent,
    parseUsageWindows,
    formatResetLabel,
    nowTick,
    activeUsageAlert
  } = $props();

  function pickRestoreFile(event) {
    const file = event?.currentTarget?.files?.[0];
    if (file) onRestoreAccounts(file);
    if (event?.currentTarget) event.currentTarget.value = '';
  }
</script>

<section class="panel">
  <div class="panel-header">
    <h2>Dashboard</h2>
  </div>
  <div class="panel-actions">
    <button class="btn btn-primary" onclick={onOpenAddAccountModal} disabled={busy}>Add Account</button>
    <button class="btn btn-secondary" onclick={onBackupAccounts} disabled={busy}>Backup All Accounts</button>
    <label class="btn btn-secondary btn-file {busy ? 'is-disabled' : ''}">
      Restore Accounts
      <input type="file" accept=".json,application/json" onchange={pickRestoreFile} disabled={busy} />
    </label>
    <button class="btn btn-danger" onclick={onDeleteRevokedAccounts} disabled={busy}>Delete Revoked</button>
  </div>
  <div class="dashboard-filters">
    <input
      aria-label="Search account"
      placeholder="Search account (email, alias, id)"
      value={accountSearchQuery}
      oninput={(event) => onSetAccountSearchQuery(event.currentTarget.value)}
    />
    <select
      aria-label="Filter account type"
      value={accountTypeFilter}
      onchange={(event) => onSetAccountTypeFilter(event.currentTarget.value)}
    >
      {#each accountTypeOptions as option}
        <option value={option.value}>{option.label}</option>
      {/each}
    </select>
    <select
      aria-label="Filter token status"
      value={accountStatusFilter}
      onchange={(event) => onSetAccountStatusFilter(event.currentTarget.value)}
    >
      {#each accountStatusOptions as option}
        <option value={option.value}>{option.label}</option>
      {/each}
    </select>
    <select
      aria-label="Filter usage availability"
      value={usageAvailabilityFilter}
      onchange={(event) => onSetUsageAvailabilityFilter(event.currentTarget.value)}
    >
      {#each usageAvailabilityOptions as option}
        <option value={option.value}>{option.label}</option>
      {/each}
    </select>
  </div>
</section>

{#if activeUsageAlert}
  <section class="status-banner {activeUsageAlert.level === 'critical' ? 'status-error' : 'status-info'}" aria-live="polite">
    <span class="status-icon">{activeUsageAlert.level === 'critical' ? '!' : 'i'}</span>
    <p>{activeUsageAlert.message}</p>
  </section>
{/if}

<section class="panel" aria-label="Managed accounts">
  <div class="panel-header panel-header-inline">
    <h2>Managed Accounts</h2>
    <span class="panel-meta">{pageStart}-{pageEnd} shown • Total: {totalAccounts}</span>
  </div>
  <div class="dashboard-pagination">
    <div class="dashboard-pagination-right">
      <label class="dashboard-page-size-control">
        <span class="panel-meta">Per page</span>
        <select
          class="dashboard-page-size-select"
          aria-label="Accounts per page"
          value={String(perPage)}
          onchange={(event) => onSetPageSize(event.currentTarget.value)}
        >
          {#each pageSizeOptions as size}
            <option value={String(size)}>{size}</option>
          {/each}
        </select>
      </label>
      <div class="dashboard-page-nav">
        <button
          class="btn btn-small btn-secondary pagination-nav-btn"
          onclick={() => onSetPage(page - 1)}
          disabled={page <= 1}
        >
          Prev
        </button>
        <span class="dashboard-page-label panel-meta">Page {page} / {totalPages}</span>
        <button
          class="btn btn-small btn-secondary pagination-nav-btn"
          onclick={() => onSetPage(page + 1)}
          disabled={page >= totalPages}
        >
          Next
        </button>
      </div>
    </div>
  </div>

  {#if accounts.length === 0}
    <div class="empty-state">No accounts yet. Add via Browser Callback or Device Login.</div>
  {:else}
    <div class="accounts-grid">
      {#each accounts as account (account.id)}
        {@const usageWindows = parseUsageWindows(account.usage)}
        {@const revoked = account?.revoked === true}
        {@const revokedReason = String(account?.revoked_reason || '').trim()}
        <article class="account-card {account.active ? 'is-active' : ''}">
          <div class="account-head">
            <div>
              <p class="account-email">{showAccountEmail ? (account.email || '-') : (account.id || '-')}</p>
            </div>
            <span class="account-state {account.active_cli ? 'state-active cli-state' : ''}">
              {account.active_cli ? 'CODEX ACTIVE' : 'IDLE'}
            </span>
          </div>
          <div class="inline-actions">
            <span class="account-state api-state {account.active_api ? 'state-active' : ''}">
              API {account.active_api ? 'ACTIVE' : 'IDLE'}
            </span>
          </div>

          {#if revoked}
            <div class="empty-state compact status-revoked">
              <strong>Status: Revoked</strong><br/>
              <span style="font-size: 0.9em; opacity: 0.8;">{revokedReason || 'Token invalid, exhausted, or suspended.'}</span>
            </div>
          {:else if usageWindows.length === 0}
            <div class="empty-state compact">Usage unavailable. Click refresh.</div>
          {:else}
            <div class="usage-list">
              {#each usageWindows as window (window.key)}
                <div class="usage-item">
                  <div class="usage-top">
                    <p>{window.name}</p>
                    <p>{usageLabel(window.percent)}</p>
                  </div>
                  <div
                    class="usage-track"
                    role="progressbar"
                    aria-valuemin="0"
                    aria-valuemax="100"
                    aria-valuenow={window.percent}
                  >
                    <span style={`width: ${clampPercent(window.percent)}%`}></span>
                  </div>
                  <p class="usage-reset">{formatResetLabel(window.resetAt, nowTick)}</p>
                </div>
              {/each}
            </div>
          {/if}

          <div class="account-foot">
            <span class="plan-type">{(account.plan_type || 'unknown').toUpperCase()}</span>
            <div class="inline-actions">
              <button
                class="btn btn-small btn-primary"
                onclick={() => onUseApiAccount(account.id)}
                disabled={busy || account.active_api || revoked}
                title={revoked ? (revokedReason || 'Token revoked') : ''}
              >
                Use API
              </button>
              <button
                class="btn btn-small btn-secondary"
                onclick={() => onUseCliAccount(account.id)}
                disabled={busy || account.active_cli || revoked}
                title={revoked ? (revokedReason || 'Token revoked') : ''}
              >
                Use CLI
              </button>
              <button
                class="btn btn-small btn-secondary btn-icon-only btn-refresh-icon"
                onclick={() => onRefreshUsage(account.id)}
                disabled={busy}
                aria-label="Refresh usage"
                title="Refresh usage"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M12 5a7 7 0 0 1 6.93 6H21l-3.2 3.2L14.6 11h2.28A4.99 4.99 0 0 0 12 7a5 5 0 0 0-4.9 4H5.07A7 7 0 0 1 12 5Zm6.83 8a7 7 0 0 1-13.66 0H3l3.2-3.2L9.4 13H7.12A4.99 4.99 0 0 0 12 17a5 5 0 0 0 4.9-4h1.93Z"></path>
                </svg>
              </button>
              <button
                class="btn btn-small btn-danger btn-icon-only btn-remove-icon"
                onclick={() => onOpenRemoveModal(account)}
                disabled={busy}
                aria-label="Remove account"
                title="Remove account"
              >
                <svg viewBox="0 0 24 24" aria-hidden="true">
                  <path d="M9 3h6l1 2h4v2H4V5h4l1-2Zm1 6h2v8h-2V9Zm4 0h2v8h-2V9ZM7 9h2v8H7V9Zm1 11h8a2 2 0 0 0 2-2V8H6v10a2 2 0 0 0 2 2Z"></path>
                </svg>
              </button>
            </div>
          </div>
        </article>
      {/each}
    </div>
  {/if}
</section>
