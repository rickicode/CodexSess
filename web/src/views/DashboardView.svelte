<script>
  let {
    accounts,
    totalAccounts,
    showAccountEmail,
    busy,
    accountSearchQuery,
    accountTypeFilter,
    accountTypeOptions,
    onSetAccountSearchQuery,
    onSetAccountTypeFilter,
    onOpenAddAccountModal,
    onRefreshAllUsage,
    onUseApiAccount,
    onUseCliAccount,
    onRefreshUsage,
    onOpenRemoveModal,
    usageLabel,
    clampPercent,
    parseUsageWindows,
    formatResetLabel,
    activeUsageAlert
  } = $props();
</script>

<section class="panel">
  <div class="panel-header">
    <h2>Dashboard</h2>
  </div>
  <div class="panel-actions">
    <button class="btn btn-primary" onclick={onOpenAddAccountModal} disabled={busy}>Add Account</button>
    <button class="btn btn-secondary" onclick={onRefreshAllUsage} disabled={busy}>Refresh All Usage</button>
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
    <span class="panel-meta">{accounts.length} of {totalAccounts} account(s)</span>
  </div>

  {#if accounts.length === 0}
    <div class="empty-state">No accounts yet. Add via Browser Callback or Device Login.</div>
  {:else}
    <div class="accounts-grid">
      {#each accounts as account (account.id)}
        {@const usageWindows = parseUsageWindows(account.usage)}
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

          {#if usageWindows.length === 0}
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
                  <p class="usage-reset">{formatResetLabel(window.resetAt)}</p>
                </div>
              {/each}
            </div>
          {/if}

          <div class="account-foot">
            <span class="plan-type">{(account.plan_type || 'unknown').toUpperCase()}</span>
            <div class="inline-actions">
              <button class="btn btn-small btn-primary" onclick={() => onUseApiAccount(account.id)} disabled={busy || account.active_api}>Use API</button>
              <button class="btn btn-small btn-secondary" onclick={() => onUseCliAccount(account.id)} disabled={busy || account.active_cli}>Use CLI</button>
              <button class="btn btn-small btn-secondary" onclick={() => onRefreshUsage(account.id)} disabled={busy}>Refresh</button>
              <button class="btn btn-small btn-danger" onclick={() => onOpenRemoveModal(account)} disabled={busy}>Remove</button>
            </div>
          </div>
        </article>
      {/each}
    </div>
  {/if}
</section>
