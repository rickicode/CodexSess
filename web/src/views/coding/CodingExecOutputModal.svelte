<script>
  import { formatWhen } from '../../lib/coding/timeFormat.js';

  export let entry = null;
  export let onClose = () => {};
</script>

<div class="modal-backdrop modal-backdrop-coding" role="presentation">
  <div class="modal-card modal-card-coding modal-card-exec-output" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && onClose()}>
    <div class="modal-head">
      <div>
        <h3>Terminal Output</h3>
        <p class="modal-subtitle">{entry?.statusLabel || 'Done'}</p>
        <p class="setting-title">Source: {entry?.source || 'live'}</p>
      </div>
      <button class="btn btn-secondary btn-small" onclick={onClose}>Close</button>
    </div>
    <div class="modal-body">
      <p class="setting-title">Command</p>
      <pre class="coding-exec-modal-command mono">{entry?.command || '-'}</pre>
      <p class="setting-title">Output</p>
      <pre class="coding-exec-modal-output mono">{entry?.output || 'No output captured.'}</pre>
      {#if entry?.when}
        <p class="setting-title">{formatWhen(entry.when)}</p>
      {/if}
    </div>
  </div>
</div>
