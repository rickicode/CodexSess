<script>
  export let draftMessage = '';
  export let composerError = '';
  export let sending = false;
  export let backgroundProcessing = false;
  export let selectedSandboxMode = 'write';
  export let stopLabel = 'Send';
  export let composerLockedUntilAssistant = false;
  export let onKeydown = () => {};
  export let onOpenSkillModal = () => {};
  export let onToggleSandboxMode = () => {};
  export let onSend = () => {};
  export let onCancel = () => {};

  let composerInput = null;

  function composerPlaceholder() {
    return 'Ask Codex to inspect, edit, or verify the workspace. Enter to send. Shift+Enter for newline. Supports /status, /mcp, and $skill.';
  }
</script>

<div class="coding-composer">
  <div class="coding-composer-shell">
    <div class="coding-composer-body">
      <textarea
        bind:this={composerInput}
        placeholder={composerPlaceholder()}
        bind:value={draftMessage}
        rows="4"
        onkeydown={onKeydown}
        oninput={() => {
          if (composerError) composerError = '';
        }}
        disabled={backgroundProcessing || (sending && composerLockedUntilAssistant)}
      ></textarea>
      <div class="coding-composer-footer">
        <div class="coding-composer-actions">
          <div class="coding-composer-secondary-actions">
          <button class="btn btn-secondary" type="button" onclick={onOpenSkillModal} disabled={sending || backgroundProcessing}>Skills</button>
          <button
            class="btn btn-secondary sandbox-mode-btn {selectedSandboxMode === 'full-access' ? 'mode-full' : 'mode-write'}"
            type="button"
            onclick={onToggleSandboxMode}
            disabled={sending || backgroundProcessing}
          >
            {selectedSandboxMode === 'full-access' ? 'Full access' : 'Write'}
          </button>
          </div>
          <button
            class="btn {(sending || backgroundProcessing) ? 'btn-danger' : 'btn-primary'} btn-send"
            type="button"
            onclick={() => ((sending || backgroundProcessing) ? onCancel() : onSend())}
            disabled={!(sending || backgroundProcessing) && !draftMessage.trim()}
          >
            <span>{stopLabel}</span>
            <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 12l18-9-6 9 6 9-18-9z"></path></svg>
          </button>
        </div>
      </div>
    </div>
  </div>
  {#if composerError}
    <p class="coding-composer-error">Failed to send: {composerError}</p>
  {/if}
</div>
