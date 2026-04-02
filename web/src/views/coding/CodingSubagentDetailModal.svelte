<script>
  import { formatWhen } from '../../lib/coding/timeFormat.js';

  export let entry = null;
  export let onClose = () => {};
</script>

<div class="modal-backdrop modal-backdrop-coding" role="presentation">
  <div class="modal-card modal-card-coding modal-card-subagent-detail" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && onClose()}>
    <div class="modal-head">
      <div>
        <h3>Subagent Detail</h3>
        <p class="modal-subtitle">{entry?.statusLabel || 'Done'}</p>
      </div>
      <button class="btn btn-secondary btn-small" onclick={onClose}>Close</button>
    </div>
    <div class="modal-body">
      <p class="setting-title">Activity</p>
      <pre class="coding-subagent-modal-block mono">{entry?.title || 'Subagent Activity'}</pre>
      <p class="setting-title">Tool</p>
      <pre class="coding-subagent-modal-block mono">{entry?.tool || '-'}</pre>
      <p class="setting-title">Agent</p>
      <pre class="coding-subagent-modal-block mono">{entry?.nameRole || '-'}</pre>
      {#if entry?.agentName}
        <p class="setting-title">Name</p>
        <pre class="coding-subagent-modal-block mono">{entry.agentName}</pre>
      {/if}
      {#if entry?.targetID}
        <p class="setting-title">Target</p>
        <pre class="coding-subagent-modal-block mono">{entry.targetID}</pre>
      {/if}
      <p class="setting-title">IDs</p>
      <pre class="coding-subagent-modal-block mono">{entry?.ids?.length ? entry.ids.join('\n') : '-'}</pre>
      <p class="setting-title">Prompt</p>
      <pre class="coding-subagent-modal-block mono">{entry?.prompt || '-'}</pre>
      <p class="setting-title">Summary</p>
      <pre class="coding-subagent-modal-block mono">{entry?.summary || '-'}</pre>
      <p class="setting-title">Raw Event</p>
      <pre class="coding-subagent-modal-raw mono">{entry?.raw || '{}'}</pre>
      {#if entry?.when}
        <p class="setting-title">{formatWhen(entry.when)}</p>
      {/if}
    </div>
  </div>
</div>
