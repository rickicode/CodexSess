<script>
  export let viewStatus = 'Ready.';
  export let persistingSessionPrefs = false;
</script>

<div class="coding-status-line" aria-live="polite">
  <div class="coding-status-main">
    {#if String(viewStatus || '').toLowerCase().startsWith('streaming')}
      <div class="coding-streaming-note coding-streaming-inline" role="status" aria-live="polite">
        <span class="streaming-pulse" aria-hidden="true"></span>
        <span class="streaming-label">Streaming</span>
        <span class="streaming-dots" aria-hidden="true"></span>
        <span class="streaming-bar" aria-hidden="true"><i></i></span>
      </div>
    {:else}
      <span class="coding-status-text">{viewStatus}</span>
    {/if}
    {#if persistingSessionPrefs}
      <span class="coding-status-saving">Saving settings...</span>
    {/if}
  </div>
</div>
