<script>
  export let sending = false;
  export let creatingSessionFlow = false;
  export let newSessionPath = '~/';
  export let pathSuggestions = ['~/'];
  export let loadingPathSuggestions = false;
  export let onClose = () => {};
  export let onPathInput = () => {};
  export let onPathFocus = () => {};
  export let onRefreshSuggestions = () => {};
  export let onCreate = () => {};
</script>

<div class="modal-backdrop modal-backdrop-coding" role="presentation">
  <div class="modal-card modal-card-coding" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && onClose()}>
    <div class="modal-head">
      <div>
        <h3>New Session</h3>
        <p class="modal-subtitle">Start a normal coding chat session.</p>
      </div>
      <button class="btn btn-secondary btn-small" onclick={onClose} disabled={sending || creatingSessionFlow}>Close</button>
    </div>
    <div class="modal-body">
      <label for="sessionWorkDir">Workspace Path</label>
      <input
        id="sessionWorkDir"
        list="sessionWorkDirSuggestions"
        bind:value={newSessionPath}
        placeholder="~/"
        oninput={(event) => onPathInput(event.currentTarget.value)}
        onfocus={() => onPathFocus(newSessionPath)}
        disabled={sending || creatingSessionFlow}
      />
      <datalist id="sessionWorkDirSuggestions">
        {#each pathSuggestions as option}
          <option value={option}></option>
        {/each}
      </datalist>
      <p class="setting-title">
        {#if loadingPathSuggestions}
          Loading path suggestions...
        {:else}
          Default path is `~/`. Suggestions are loaded from current folder listing.
        {/if}
      </p>

      <div class="panel-actions">
        <button class="btn btn-secondary" onclick={() => onRefreshSuggestions(newSessionPath)} disabled={sending || loadingPathSuggestions}>
          Refresh Suggestions
        </button>
        <button
          class="btn btn-primary"
          onclick={onCreate}
          disabled={sending || creatingSessionFlow || !newSessionPath.trim()}
        >
          {#if creatingSessionFlow}
            Creating...
          {:else}
            Create Session
          {/if}
        </button>
      </div>
    </div>
  </div>
</div>
