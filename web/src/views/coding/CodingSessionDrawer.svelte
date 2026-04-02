<script>
  import { formatWhen } from '../../lib/coding/timeFormat.js';

  export let loadingSessions = false;
  export let sessions = [];
  export let activeSessionID = '';
  export let deleting = false;
  export let sending = false;
  export let sessionDisplayID = () => '-';
  export let onClose = () => {};
  export let onSelect = () => {};
  export let onDelete = () => {};
</script>

<div class="modal-backdrop modal-backdrop-coding" role="presentation">
  <div class="modal-card modal-card-coding drawer-card" role="dialog" aria-modal="true" tabindex="0" onkeydown={(event) => event.key === 'Escape' && onClose()}>
    <div class="modal-head">
      <div>
        <h3>Sessions</h3>
        <p class="modal-subtitle">Pick a session from the list.</p>
      </div>
      <button class="btn btn-secondary btn-small" onclick={onClose}>Close</button>
    </div>
    <div class="coding-sessions-list drawer-list" aria-label="Session list">
      {#if loadingSessions}
        <p class="empty-note">Loading sessions...</p>
      {:else if sessions.length === 0}
        <p class="empty-note">No session yet.</p>
      {:else}
        {#each sessions as session (session.id)}
          <div class="coding-session-item-row">
            <button
              class="coding-session-item {activeSessionID === session.id ? 'is-active' : ''}"
              onclick={() => onSelect(session.id)}
            >
              <strong>{session.title || 'New Session'}</strong>
              <span class="mono">{sessionDisplayID(session)}</span>
              <span>{formatWhen(session.last_message_at)}</span>
              <span class="mono">{session.work_dir || '~/'}</span>
            </button>
            <button
              class="coding-session-delete"
              type="button"
              onclick={(event) => {
                event.stopPropagation();
                onDelete(session.id);
              }}
              disabled={deleting || sending}
              aria-label={`Delete session ${session.title || session.id}`}
              title="Delete session"
            >
              <svg viewBox="0 0 24 24" aria-hidden="true">
                <path d="M9 3h6l1 2h4v2H4V5h4l1-2zm1 6h2v9h-2V9zm4 0h2v9h-2V9zM6 7h12l-1 13H7L6 7z"></path>
              </svg>
            </button>
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>
