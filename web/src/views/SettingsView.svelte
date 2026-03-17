<script>
  let {
    busy,
    apiKey,
    openAIEndpoint,
    claudeEndpoint,
    availableModels,
    modelMappings,
    mappingAlias,
    mappingTargetModel,
    editingMappingAlias,
    onSetMappingAlias,
    onSetMappingTargetModel,
    onSaveModelMapping,
    onCancelEditMapping,
    onStartEditMapping,
    onDeleteModelMapping,
    onCopyText,
    isCopied,
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
    backgroundRefreshLastAt,
    onRegenerateAPIKey,
    openAIExample,
    claudeExample
  } = $props();

  function formatBackgroundRefreshTime(timestamp) {
    const ts = Number(timestamp || 0);
    if (!Number.isFinite(ts) || ts <= 0) return 'Never';
    const date = new Date(ts);
    if (Number.isNaN(date.getTime())) return 'Never';
    return date.toLocaleString();
  }

  function clampPercent(value) {
    const n = Number(value);
    if (!Number.isFinite(n)) return 0;
    if (n < 0) return 0;
    if (n > 100) return 100;
    return Math.round(n);
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

    <section class="setting-category">
      <h3 class="setting-category-title">API & Endpoint</h3>
      <div class="setting-row">
        <label for="apiKey">API Key</label>
        <div class="setting-actions-grid with-three">
          <input id="apiKey" value={apiKey} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(apiKey, 'API key', 'api_key')} disabled={busy}>
            {#if isCopied('api_key')}Copied{:else}Copy{/if}
          </button>
          <button class="btn btn-primary" onclick={onRegenerateAPIKey} disabled={busy}>Regenerate</button>
        </div>
      </div>

      <div class="setting-row">
        <label for="openAiEndpoint">OpenAI Compatible Endpoint</label>
        <div class="setting-actions-grid">
          <input id="openAiEndpoint" value={openAIEndpoint} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(openAIEndpoint, 'OpenAI endpoint', 'openai_endpoint')} disabled={busy}>
            {#if isCopied('openai_endpoint')}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="claudeEndpoint">Claude Endpoint</label>
        <div class="setting-actions-grid">
          <input id="claudeEndpoint" value={claudeEndpoint} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(claudeEndpoint, 'Claude endpoint', 'claude_endpoint')} disabled={busy}>
            {#if isCopied('claude_endpoint')}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Model Mapping</h3>
      <div class="setting-row">
        <p class="setting-title">Available Codex Models</p>
        <div class="simple-list">
          {#if availableModels.length === 0}
            <p class="empty-note">No model list loaded.</p>
          {:else}
            {#each availableModels as model}
              <code>{model}</code>
            {/each}
          {/if}
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Model Mapping</p>
        <div class="mapping-form">
          <input
            placeholder="Alias (e.g. default)"
            value={mappingAlias}
            oninput={(event) => onSetMappingAlias(event.currentTarget.value)}
          />
          <select value={mappingTargetModel} onchange={(event) => onSetMappingTargetModel(event.currentTarget.value)}>
            {#each availableModels as model}
              <option value={model}>{model}</option>
            {/each}
          </select>
          <button class="btn btn-primary" onclick={onSaveModelMapping} disabled={busy}>
            {editingMappingAlias ? 'Update Mapping' : 'Save Mapping'}
          </button>
          {#if editingMappingAlias}
            <button class="btn btn-secondary" onclick={onCancelEditMapping} disabled={busy}>Cancel</button>
          {/if}
        </div>

        <div class="simple-list mapping-list">
          {#if Object.keys(modelMappings).length === 0}
            <p class="empty-note">No mappings yet.</p>
          {:else}
            {#each Object.entries(modelMappings) as [alias, model]}
              <div class="mapping-row">
                <code>{alias}</code>
                <code>{model}</code>
                <div class="inline-actions">
                  <button class="btn btn-small btn-secondary" onclick={() => onStartEditMapping(alias)} disabled={busy}>Edit</button>
                  <button class="btn btn-small btn-danger" onclick={() => onDeleteModelMapping(alias)} disabled={busy}>Delete</button>
                </div>
              </div>
            {/each}
          {/if}
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Examples</h3>
      <div class="setting-row">
        <p class="setting-title">OpenAI Compatible Request Example</p>
        <div class="code-box">
          <pre>{openAIExample()}</pre>
          <button class="btn btn-secondary" onclick={() => onCopyText(openAIExample(), 'OpenAI example', 'openai_example')}>
            {#if isCopied('openai_example')}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Claude Request Example</p>
        <div class="code-box">
          <pre>{claudeExample()}</pre>
          <button class="btn btn-secondary" onclick={() => onCopyText(claudeExample(), 'Claude example', 'claude_example')}>
            {#if isCopied('claude_example')}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>
    </section>
  </div>
</section>
