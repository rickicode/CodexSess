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

  async function writeClipboardText(text) {
    const value = String(text || '');
    if (!value) return false;
    try {
      if (navigator?.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
        return true;
      }
    } catch {
      // Continue to fallback copy flow.
    }
    try {
      const textarea = document.createElement('textarea');
      textarea.value = value;
      textarea.setAttribute('readonly', '');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      textarea.style.pointerEvents = 'none';
      textarea.style.top = '-1000px';
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      textarea.setSelectionRange(0, textarea.value.length);
      const ok = Boolean(document.execCommand && document.execCommand('copy'));
      document.body.removeChild(textarea);
      return ok;
    } catch {
      return false;
    }
  }

  async function copyUpdateScript() {
    const copiedOK = await writeClipboardText(updateScriptCommand);
    if (copiedOK) {
      copied = true;
      setTimeout(() => {
        copied = false;
      }, 1400);
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
        <p class="setting-title"><strong>CodexSess</strong> is an operational console for teams running multiple Codex accounts in production workflows.</p>
        <p class="setting-title">It centralizes account lifecycle, active-session routing, usage visibility, and OpenAI-compatible API proxying in one runtime.</p>
        <p class="setting-title">Designed for high-uptime usage: separate API and CLI active account control, fast failover when limits are low, and one dashboard for day-to-day operations.</p>
        <p class="setting-title">Use CodexSess when you need predictable account switching behavior, clearer usage signals, and less manual overhead during sustained automation.</p>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">By HIJINetwork</h3>
      <div class="setting-row">
        <p class="setting-title">HIJINetwork provides VPN, hosted apps, licensed applications, and digital products for online business operations.</p>
        <div class="panel-actions">
          <a class="btn btn-primary" href="https://hijinetwork.net" target="_blank" rel="noreferrer">Visit HIJINetwork</a>
        </div>
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
      <h3 class="setting-category-title">Latest Changelog {latestVersion ? `v${latestVersion}` : (appVersion ? `v${appVersion}` : '')}</h3>
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
        <p class="setting-title">Linux only: run this command to auto-detect existing GUI/CLI install and update in place.</p>
        <p class="setting-title">Windows: download the latest `.exe` directly from Release.</p>
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
