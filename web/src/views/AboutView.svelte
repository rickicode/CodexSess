<script>
  let copied = $state(false);
  let {
    busy,
    appVersion,
    latestVersion,
    updateAvailable,
    updateCheckedAt,
    updateCheckError,
    updateCheckBusy,
    latestChangelog,
    releaseURL,
    onCheckForUpdates
  } = $props();

  const updateScriptCommand = 'curl -fsSL https://raw.githubusercontent.com/rickicode/CodexSess/main/scripts/install.sh | bash -s -- --mode update';

  async function copyUpdateScript() {
    try {
      if (!navigator?.clipboard?.writeText) return;
      await navigator.clipboard.writeText(updateScriptCommand);
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 1400);
    } catch {
    }
  }
</script>

<section class="panel">
  <div class="panel-header panel-header-inline">
    <h2>About</h2>
    <button class="btn btn-secondary" onclick={onCheckForUpdates} disabled={busy || updateCheckBusy}>
      {#if updateCheckBusy}Checking...{:else}Check Update{/if}
    </button>
  </div>

  <div class="settings-list">
    <section class="setting-category">
      <h3 class="setting-category-title">CodexSess</h3>
      <div class="setting-row">
        <p class="setting-title">CodexSess is a web-first account management gateway for Codex/OpenAI usage.</p>
        <p class="setting-title">It provides account switching, usage automation, and OpenAI-compatible proxy APIs in one binary.</p>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Version</h3>
      <div class="setting-row">
        <p class="setting-title">Current: <strong>v{appVersion || 'dev'}</strong></p>
        <p class="setting-title">
          {#if latestVersion}
            Latest: <strong>v{latestVersion}</strong>
            {#if updateAvailable}(update available){:else}(up to date){/if}
          {:else}
            Latest: unavailable
          {/if}
        </p>
        <p class="setting-title">Last checked: {updateCheckedAt ? new Date(updateCheckedAt).toLocaleString() : 'Never'}</p>
        {#if updateCheckError}
          <p class="setting-title">Update check error: {updateCheckError}</p>
        {/if}
        {#if releaseURL}
          <div class="panel-actions">
            <a class="btn btn-primary" href={releaseURL} target="_blank" rel="noreferrer">Open Release</a>
          </div>
        {/if}
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Latest Changelog</h3>
      <div class="setting-row">
        {#if latestChangelog}
          <div class="code-box">
            <pre>{latestChangelog}</pre>
          </div>
        {:else}
          <p class="setting-title">No changelog available from latest release.</p>
        {/if}
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Quick Update</h3>
      <div class="setting-row">
        <p class="setting-title">Run this command to auto-detect existing GUI/CLI install and update in place.</p>
        <div class="code-box">
          <pre>{updateScriptCommand}</pre>
        </div>
        <div class="panel-actions">
          <button class="btn btn-secondary" onclick={copyUpdateScript}>{copied ? 'Copied' : 'Copy Command'}</button>
        </div>
      </div>
    </section>
  </div>
</section>
