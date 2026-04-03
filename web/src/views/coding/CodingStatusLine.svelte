<script>
  export let viewStatus = 'Ready.';
  export let persistingSessionPrefs = false;
</script>

<div class="coding-status-line" aria-live="polite">
  <div class="coding-status-main">
    {#if String(viewStatus || '').toLowerCase().startsWith('streaming')}
      <span class="status-streaming-pulse" aria-hidden="true"></span>
      <span class="status-streaming-label">Streaming</span>
      <span class="status-streaming-dots" aria-hidden="true"></span>
    {:else}
      <span class="coding-status-text">{viewStatus}</span>
    {/if}
    {#if persistingSessionPrefs}
      <span class="coding-status-saving">Saving settings...</span>
    {/if}
  </div>
</div>
