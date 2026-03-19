<script>
  let {
    busy,
    apiMode,
    onSetAPIMode,
    showAccountEmail,
    onToggleShowAccountEmail,
    autoRefreshEnabled,
    autoRefreshMinutes,
    autoRefreshMinutesInput,
    usageAlertThreshold,
    usageAlertThresholdInput,
    usageAutoSwitchThreshold,
    usageAutoSwitchThresholdInput,
    usageSoundEnabled,
    onToggleAutoRefreshEnabled,
    onSetAutoRefreshMinutesInput,
    onCommitAutoRefreshMinutesInput,
    onSetUsageAlertThresholdInput,
    onCommitUsageAlertThresholdInput,
    onSetUsageAutoSwitchThresholdInput,
    onCommitUsageAutoSwitchThresholdInput,
    onNudgeUsageAlertThreshold,
    onNudgeUsageAutoSwitchThreshold,
    onToggleUsageSoundEnabled,
    autoRefreshBusy,
    backgroundRefreshError,
    backgroundRefreshLastAt
  } = $props();

  function formatBackgroundRefreshTime(timestamp) {
    const ts = Number(timestamp || 0);
    if (!Number.isFinite(ts) || ts <= 0) return 'Never';
    const date = new Date(ts);
    if (Number.isNaN(date.getTime())) return 'Never';
    return date.toLocaleString();
  }

  function nudgeAlert(delta) {
    onNudgeUsageAlertThreshold(delta);
  }

  function nudgeAutoSwitch(delta) {
    onNudgeUsageAutoSwitchThreshold(delta);
  }
</script>

<section class="panel">
  <div class="panel-header">
    <h2>Settings</h2>
  </div>

  <div class="settings-list">
    <section class="setting-category">
      <h3 class="setting-category-title">API Mode</h3>
      <div class="setting-row">
        <p class="setting-title">Proxy Execution Mode</p>
        <div class="api-mode-switch" role="group" aria-label="API mode switch">
          <button
            type="button"
            class="btn btn-secondary api-mode-btn {apiMode === 'codex_cli' ? 'is-active' : ''}"
            onclick={() => onSetAPIMode('codex_cli')}
            disabled={busy || apiMode === 'codex_cli'}
            aria-pressed={apiMode === 'codex_cli'}
          >
            Codex CLI
          </button>
          <button
            type="button"
            class="btn btn-secondary api-mode-btn {apiMode === 'direct_api' ? 'is-active' : ''}"
            onclick={() => onSetAPIMode('direct_api')}
            disabled={busy || apiMode === 'direct_api'}
            aria-pressed={apiMode === 'direct_api'}
          >
            Direct API
          </button>
        </div>
        <p class="setting-title">
          {#if apiMode === 'direct_api'}
            /v1 endpoints call ChatGPT backend API directly.
          {:else}
            /v1 endpoints call local codex CLI execution pipeline.
          {/if}
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
      <h3 class="setting-category-title">Usage Automation</h3>
      <div class="setting-row">
        <p class="setting-title">Background Auto Refresh Usage</p>
        <div class="setting-actions-grid with-three">
          <input
            type="number"
            min="1"
            value={autoRefreshMinutesInput}
            oninput={(event) => onSetAutoRefreshMinutesInput(event.currentTarget.value)}
            onblur={onCommitAutoRefreshMinutesInput}
            onkeydown={(event) => event.key === 'Enter' && onCommitAutoRefreshMinutesInput()}
            disabled={!autoRefreshEnabled}
            aria-label="Auto refresh interval in minutes"
          />
          <button class="btn btn-secondary" onclick={onToggleAutoRefreshEnabled}>
            {#if autoRefreshEnabled}Disable{:else}Enable{/if}
          </button>
        </div>
        <p class="setting-title">
          {#if autoRefreshEnabled}
            Usage refresh runs every {autoRefreshMinutes} minute(s) in background.
          {:else}
            Auto refresh is disabled. Default interval is 30 minutes.
          {/if}
        </p>
        <p class="setting-title">Last background refresh: {formatBackgroundRefreshTime(backgroundRefreshLastAt)}</p>
        {#if autoRefreshBusy}
          <p class="setting-title">Background refresh is running.</p>
        {/if}
        {#if backgroundRefreshError}
          <p class="setting-title">Background refresh error: {backgroundRefreshError}</p>
        {/if}
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
        <p class="setting-title">Usage Auto-Switch Threshold (%)</p>
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
        <p class="setting-title">Default logic: alert at 5%, auto-switch at 2%.</p>
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
