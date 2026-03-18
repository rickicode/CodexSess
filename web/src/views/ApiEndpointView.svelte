<script>
  let {
    busy,
    apiKey,
    openAIEndpoint,
    claudeEndpoint,
    codeReviewEndpoint,
    availableModels,
    modelMappings,
    mappingAlias,
    mappingTargetModel,
    editingMappingAlias,
    onSetMappingAlias,
    onSetMappingTargetModel,
    onSaveModelMapping,
    onCancelEditMapping,
    onStartEditMapping,
    onDeleteModelMapping,
    onRegenerateAPIKey,
    onCopyText,
    isCopied,
    openAIExample,
    claudeExample,
    codeReviewExample
  } = $props();
</script>

<section class="panel">
  <div class="panel-header">
    <h2>API Workspace</h2>
  </div>

  <div class="settings-list">
    <section class="setting-category">
      <h3 class="setting-category-title">Credentials</h3>
      <div class="setting-row">
        <label for="apiKey">API Key</label>
        <div class="setting-actions-grid with-three">
          <input id="apiKey" value={apiKey} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(apiKey, 'API key', 'api_key')} disabled={busy}>
            {#if isCopied('api_key')}Copied{:else}Copy{/if}
          </button>
          <button class="btn btn-primary" onclick={onRegenerateAPIKey} disabled={busy}>Regenerate</button>
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Model Mapping</h3>
      <div class="setting-row">
        <p class="setting-title">Available Codex Models</p>
        <div class="simple-list">
          {#if availableModels.length === 0}
            <p class="empty-note">No model list loaded.</p>
          {:else}
            {#each availableModels as model}
              <code>{model}</code>
            {/each}
          {/if}
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Model Mapping</p>
        <div class="mapping-form">
          <input
            placeholder="Alias (e.g. default)"
            value={mappingAlias}
            oninput={(event) => onSetMappingAlias(event.currentTarget.value)}
          />
          <select value={mappingTargetModel} onchange={(event) => onSetMappingTargetModel(event.currentTarget.value)}>
            {#each availableModels as model}
              <option value={model}>{model}</option>
            {/each}
          </select>
          <button class="btn btn-primary" onclick={onSaveModelMapping} disabled={busy}>
            {editingMappingAlias ? 'Update Mapping' : 'Save Mapping'}
          </button>
          {#if editingMappingAlias}
            <button class="btn btn-secondary" onclick={onCancelEditMapping} disabled={busy}>Cancel</button>
          {/if}
        </div>

        <div class="simple-list mapping-list">
          {#if Object.keys(modelMappings).length === 0}
            <p class="empty-note">No mappings yet.</p>
          {:else}
            {#each Object.entries(modelMappings) as [alias, model]}
              <div class="mapping-row">
                <code>{alias}</code>
                <code>{model}</code>
                <div class="inline-actions">
                  <button class="btn btn-small btn-secondary" onclick={() => onStartEditMapping(alias)} disabled={busy}>Edit</button>
                  <button class="btn btn-small btn-danger" onclick={() => onDeleteModelMapping(alias)} disabled={busy}>Delete</button>
                </div>
              </div>
            {/each}
          {/if}
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Endpoints</h3>
      <div class="setting-row">
        <label for="openAiEndpoint">OpenAI Compatible Endpoint</label>
        <div class="setting-actions-grid">
          <input id="openAiEndpoint" value={openAIEndpoint} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(openAIEndpoint, 'OpenAI endpoint', 'openai_endpoint')} disabled={busy}>
            {#if isCopied('openai_endpoint')}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="claudeEndpoint">Claude Endpoint</label>
        <div class="setting-actions-grid">
          <input id="claudeEndpoint" value={claudeEndpoint} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(claudeEndpoint, 'Claude endpoint', 'claude_endpoint')} disabled={busy}>
            {#if isCopied('claude_endpoint')}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="codeReviewEndpoint">Code Review Endpoint</label>
        <div class="setting-actions-grid">
          <input id="codeReviewEndpoint" value={codeReviewEndpoint} readonly disabled />
          <button class="btn btn-secondary" onclick={() => onCopyText(codeReviewEndpoint, 'Code review endpoint', 'code_review_endpoint')} disabled={busy}>
            {#if isCopied('code_review_endpoint')}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Examples</h3>
      <div class="setting-row">
        <p class="setting-title">OpenAI Compatible Request Example</p>
        <div class="code-box">
          <pre>{openAIExample()}</pre>
          <button class="btn btn-secondary" onclick={() => onCopyText(openAIExample(), 'OpenAI example', 'openai_example')}>
            {#if isCopied('openai_example')}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Claude Request Example</p>
        <div class="code-box">
          <pre>{claudeExample()}</pre>
          <button class="btn btn-secondary" onclick={() => onCopyText(claudeExample(), 'Claude example', 'claude_example')}>
            {#if isCopied('claude_example')}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Code Review Request Example</p>
        <div class="code-box">
          <pre>{codeReviewExample()}</pre>
          <button class="btn btn-secondary" onclick={() => onCopyText(codeReviewExample(), 'Code review example', 'code_review_example')}>
            {#if isCopied('code_review_example')}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>
    </section>

  </div>
</section>
