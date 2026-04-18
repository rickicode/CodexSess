<script>
  let {
    busy,
    showAccountEmail,
    onToggleShowAccountEmail,
    directAPIStrategy,
    codingTemplateHome,
    codingTemplateBusy,
    usageAlertThreshold,
    usageAlertThresholdInput,
    usageAutoSwitchThreshold,
    usageAutoSwitchThresholdInput,
    usageSchedulerIntervalMinutes,
    usageSchedulerIntervalMinutesInput,
    usageSoundEnabled,
    onSetDirectAPIStrategy,
    onInitializeCodingTemplateHome,
    onResyncCodingTemplateHome,
    onRefreshCodingTemplateHome,
    onSetUsageAlertThresholdInput,
    onCommitUsageAlertThresholdInput,
    onSetUsageAutoSwitchThresholdInput,
    onCommitUsageAutoSwitchThresholdInput,
    onSetUsageSchedulerIntervalInput,
    onCommitUsageSchedulerIntervalInput,
    onNudgeUsageAlertThreshold,
    onNudgeUsageAutoSwitchThreshold,
    onNudgeUsageSchedulerInterval,
    onToggleUsageSoundEnabled
  } = $props();

  function nudgeAlert(delta) {
    onNudgeUsageAlertThreshold(delta);
  }

  function nudgeAutoSwitch(delta) {
    onNudgeUsageAutoSwitchThreshold(delta);
  }

  function nudgeSchedulerInterval(delta) {
    onNudgeUsageSchedulerInterval(delta);
  }
</script>

<section class="panel">
  <div class="panel-header">
    <h2>Settings</h2>
  </div>

  <div class="settings-list">
    <section class="setting-category">
      <h3 class="setting-category-title">API Execution</h3>
      <div class="setting-row">
        <p class="setting-title">Proxy APIs always use Direct API</p>
        <p class="setting-title">Direct API Account Strategy</p>
        <div class="api-mode-switch" role="group" aria-label="direct api strategy switch">
          <button
            type="button"
            class="btn btn-secondary api-mode-btn {directAPIStrategy === 'round_robin' ? 'is-active' : ''}"
            onclick={() => onSetDirectAPIStrategy('round_robin')}
            disabled={busy || directAPIStrategy === 'round_robin'}
            aria-pressed={directAPIStrategy === 'round_robin'}
          >
            Round Robin
          </button>
          <button
            type="button"
            class="btn btn-secondary api-mode-btn {directAPIStrategy === 'load_balance' ? 'is-active' : ''}"
            onclick={() => onSetDirectAPIStrategy('load_balance')}
            disabled={busy || directAPIStrategy === 'load_balance'}
            aria-pressed={directAPIStrategy === 'load_balance'}
          >
            Load Balance
          </button>
        </div>
        <p class="setting-title">
          {#if directAPIStrategy === 'load_balance'}Select account by highest fresh remaining usage.{:else}Rotate account every request to distribute load.{/if}
        </p>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Account Display</h3>
      <div class="setting-row">
        <p class="setting-title">Managed Account Information</p>
        <div class="setting-actions-grid with-three">
          <input value={showAccountEmail ? 'Email is visible in Managed Accounts' : 'Email is hidden, showing account ID'} readonly disabled />
          <button class="btn btn-secondary" onclick={onToggleShowAccountEmail}>
            {#if showAccountEmail}Hide Information{:else}Show Information{/if}
          </button>
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Template Home</h3>
      <div class="setting-row">
        <p class="setting-title">Base Codex Home</p>
        <div class="setting-actions-grid with-three">
          <input value={codingTemplateHome?.root_path || 'Not loaded'} readonly disabled />
          <button class="btn btn-secondary" onclick={onRefreshCodingTemplateHome} disabled={busy || codingTemplateBusy}>
            Refresh Status
          </button>
          <button class="btn btn-secondary" onclick={onInitializeCodingTemplateHome} disabled={busy || codingTemplateBusy}>
            Initialize
          </button>
          <button class="btn btn-secondary" onclick={onResyncCodingTemplateHome} disabled={busy || codingTemplateBusy}>
            Resync
          </button>
        </div>
        <p class="setting-title">
          {#if codingTemplateHome?.ready}
            Template is ready with baseline MCP servers.
          {:else if codingTemplateHome}
            Template is missing baseline fields: {(codingTemplateHome.missing_baseline_fields || []).join(', ') || 'unknown'}
          {:else}
            Template status has not been loaded yet.
          {/if}
        </p>
        <p class="setting-title">
          {codingTemplateHome?.config_path ? `Config: ${codingTemplateHome.config_path}` : ''}
        </p>
        <p class="setting-title">
          {codingTemplateHome?.runtime_home_count != null ? `Runtime homes: ${codingTemplateHome.runtime_home_count}` : ''}
        </p>
      </div>
      <div class="setting-row">
        <p class="setting-title">Seeded MCP</p>
        <p class="setting-title">
          {(codingTemplateHome?.enabled_mcp_servers || []).join(', ') || 'None'}
        </p>
        <p class="setting-title">
          Disabled: {(codingTemplateHome?.disabled_mcp_servers || []).join(', ') || 'None'}
        </p>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Usage Automation</h3>
      <div class="setting-row">
        <p class="setting-title">Background Usage Scheduler</p>
        <p class="setting-title">Backend job ini refresh usage semua akun non-revoked di database setiap siklus sesuai interval settings. Job ini tidak menjalankan auto-switch akun aktif.</p>
      </div>

      <div class="setting-row">
        <p class="setting-title">Background Usage Refresh Interval (minutes)</p>
        <div class="slider-wrap">
        <div class="slider-head">
          <span class="setting-title">Run background refresh every</span>
          <div class="inline-actions">
            <button type="button" class="btn btn-small btn-secondary" onclick={() => nudgeSchedulerInterval(-1)}>-</button>
            <span class="slider-value">{usageSchedulerIntervalMinutes}m</span>
            <button type="button" class="btn btn-small btn-secondary" onclick={() => nudgeSchedulerInterval(1)}>+</button>
          </div>
        </div>
          <input
            class="threshold-slider"
            type="range"
            min="10"
            max="300"
            step="1"
            value={usageSchedulerIntervalMinutes}
            oninput={(event) => onSetUsageSchedulerIntervalInput(event.currentTarget.value)}
            onmouseup={(event) => onCommitUsageSchedulerIntervalInput(event.currentTarget.value)}
            onchange={(event) => onCommitUsageSchedulerIntervalInput(event.currentTarget.value)}
            aria-label="Usage scheduler interval minutes"
          />
          <div class="slider-scale">
            <span>10m</span>
            <span>60m</span>
            <span>300m</span>
          </div>
        </div>
        <p class="setting-title">Current scheduler interval: {usageSchedulerIntervalMinutesInput} minutes</p>
        <p class="setting-title">Each cycle refreshes account usage in batches from the database-backed account pool.</p>
      </div>

      <div class="setting-row">
        <p class="setting-title">Active Account Auto-Switch Job</p>
        <p class="setting-title">Backend job ini berjalan tetap setiap 5 menit. Job ini hanya cek usage akun API active dan CLI active, lalu switch jika usage aktif turun di bawah threshold.</p>
      </div>

      <div class="setting-row">
        <p class="setting-title">Usage Alert Threshold (%)</p>
        <div class="slider-wrap">
        <div class="slider-head">
          <span class="setting-title">Alert when remaining usage is below</span>
          <div class="inline-actions">
            <button type="button" class="btn btn-small btn-secondary" onclick={() => nudgeAlert(-1)}>-</button>
            <span class="slider-value">{usageAlertThreshold}%</span>
            <button type="button" class="btn btn-small btn-secondary" onclick={() => nudgeAlert(1)}>+</button>
          </div>
        </div>
          <input
            class="threshold-slider"
            type="range"
            min="0"
            max="100"
            step="1"
            value={usageAlertThreshold}
            oninput={(event) => onSetUsageAlertThresholdInput(event.currentTarget.value)}
            onmouseup={(event) => onCommitUsageAlertThresholdInput(event.currentTarget.value)}
            onchange={(event) => onCommitUsageAlertThresholdInput(event.currentTarget.value)}
            aria-label="Usage alert threshold percent"
          />
          <div class="slider-scale">
            <span>0%</span>
            <span>50%</span>
            <span>100%</span>
          </div>
        </div>
        <p class="setting-title">Current alert threshold: {usageAlertThreshold}%</p>
      </div>

      <div class="setting-row">
        <p class="setting-title">Auto-Switch Threshold (%) for Active Accounts</p>
        <div class="slider-wrap">
        <div class="slider-head">
          <span class="setting-title">Auto-switch when remaining usage is below</span>
          <div class="inline-actions">
            <button type="button" class="btn btn-small btn-secondary" onclick={() => nudgeAutoSwitch(-1)}>-</button>
            <span class="slider-value">{usageAutoSwitchThreshold}%</span>
            <button type="button" class="btn btn-small btn-secondary" onclick={() => nudgeAutoSwitch(1)}>+</button>
          </div>
        </div>
          <input
            class="threshold-slider"
            type="range"
            min="0"
            max="100"
            step="1"
            value={usageAutoSwitchThreshold}
            oninput={(event) => onSetUsageAutoSwitchThresholdInput(event.currentTarget.value)}
            onmouseup={(event) => onCommitUsageAutoSwitchThresholdInput(event.currentTarget.value)}
            onchange={(event) => onCommitUsageAutoSwitchThresholdInput(event.currentTarget.value)}
            aria-label="Usage auto switch threshold percent"
          />
          <div class="slider-scale">
            <span>0%</span>
            <span>50%</span>
            <span>100%</span>
          </div>
        </div>
        <p class="setting-title">Current auto-switch threshold: {usageAutoSwitchThreshold}%</p>
        <p class="setting-title">Active auto-switch runs every 5 minutes, refreshes the current API and CLI active accounts directly from upstream, then switches only if the active usage is below this threshold.</p>
        <p class="setting-title">Backup selection uses database snapshots and only considers accounts with weekly usage at least 80% or 5h usage at least 80%.</p>
        <p class="setting-title">Default logic: alert at 5%, auto-switch at 15%.</p>
      </div>

      <div class="setting-row">
        <p class="setting-title">Notification Sound</p>
        <div class="setting-actions-grid">
          <input value={usageSoundEnabled ? 'Sound enabled for use/switch/alert events' : 'Sound disabled'} readonly disabled />
          <button class="btn btn-secondary" onclick={onToggleUsageSoundEnabled}>
            {#if usageSoundEnabled}Disable Sound{:else}Enable Sound{/if}
          </button>
        </div>
      </div>
    </section>

  </div>
</section>
