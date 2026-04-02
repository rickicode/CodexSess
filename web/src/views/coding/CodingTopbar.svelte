<script>
  export let wsHealthStatus = 'disconnected';
  export let selectedWorkDir = '~/';
  export let selectedModel = '';
  export let selectedReasoningLevel = 'medium';
  export let models = [];
  export let reasoningLevels = [];
  export let loadingSessions = false;
  export let sending = false;
  export let deleting = false;
  export let activeSessionID = '';
  export let onBack = () => {};
  export let onOpenSessions = () => {};
  export let onModelChange = () => {};
  export let onReasoningChange = () => {};
  export let onNewSession = () => {};
  export let onDeleteSession = () => {};
</script>

<header class="coding-topbar">
  <div class="coding-topbar-left">
    <button class="btn btn-secondary topbar-icon-btn" type="button" onclick={onBack} aria-label="Dashboard">
      <span class="topbar-btn-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24"><path d="M3 11.5 12 4l9 7.5v8.5a1 1 0 0 1-1 1h-5.5v-6h-5v6H4a1 1 0 0 1-1-1z"></path></svg>
      </span>
      <span class="topbar-btn-label">Dashboard</span>
    </button>
    <button class="btn btn-secondary topbar-icon-btn" type="button" onclick={onOpenSessions} aria-label="Sessions">
      <span class="topbar-btn-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24"><path d="M4 5h16v4H4zm0 5.5h16v4H4zM4 16h16v3H4z"></path></svg>
      </span>
      <span class="topbar-btn-label">Sessions</span>
    </button>
    <div class="coding-topbar-title">
      <div class="coding-topbar-title-row">
        <strong>Codex Chat</strong>
        <span class="coding-topbar-ws {wsHealthStatus}">
          {wsHealthStatus === 'connected' ? 'WS Connected' : wsHealthStatus === 'connecting' ? 'WS Connecting' : 'WS Offline'}
        </span>
      </div>
      <span title={selectedWorkDir || '~/'}>
        {selectedWorkDir || '~/'}
      </span>
    </div>
  </div>
  <div class="coding-topbar-right">
    <div class="coding-topbar-selects">
      <select class="coding-model-select" bind:value={selectedModel} onchange={onModelChange} aria-label="Model for coding session">
        {#each models as model}
          <option value={model}>{model}</option>
        {/each}
      </select>
      <select class="coding-reasoning-select" bind:value={selectedReasoningLevel} onchange={onReasoningChange} aria-label="Reasoning level for coding session">
        {#each reasoningLevels as level}
          <option value={level.value}>Reasoning: {level.label}</option>
        {/each}
      </select>
    </div>
    <button class="btn btn-secondary topbar-icon-btn" onclick={onNewSession} disabled={loadingSessions || sending} aria-label="New Session">
      <span class="topbar-btn-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24"><path d="M12 5v14m-7-7h14" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round"></path></svg>
      </span>
      <span class="topbar-btn-label">New Session</span>
    </button>
    <button class="btn btn-danger topbar-icon-btn" onclick={onDeleteSession} disabled={!activeSessionID || deleting || sending} aria-label="Delete Session">
      <span class="topbar-btn-icon" aria-hidden="true">
        <svg viewBox="0 0 24 24"><path d="M6 7h12l-1 13H7zm3-3h6l1 2H8z"></path></svg>
      </span>
      <span class="topbar-btn-label">Delete</span>
    </button>
  </div>
</header>
