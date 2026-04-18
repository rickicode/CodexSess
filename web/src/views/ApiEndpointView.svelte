<script>
  let {
    busy,
    apiKey,
    openAIEndpoint,
    openAIResponsesEndpoint,
    claudeEndpoint,
    authJSONEndpoint,
    usageStatusEndpoint,
    claudeCodeIntegration,
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
    onEnableClaudeCodeIntegration,
    onCopyText,
    isCopied,
    openAIExample,
    claudeExample,
    authJSONExample,
    usageStatusExample,
  } = $props();
</script>

<section class="panel">
  <div class="panel-header">
    <h2>Workspaces</h2>
  </div>

  <div class="settings-list">
    <section class="setting-category">
      <h3 class="setting-category-title">Credentials</h3>
      <div class="setting-row">
        <label for="apiKey">API Key</label>
        <div class="setting-actions-grid with-three">
          <input id="apiKey" value={apiKey} readonly disabled />
          <button
            class="btn btn-secondary"
            onclick={() => onCopyText(apiKey, "API key", "api_key")}
            disabled={busy}
          >
            {#if isCopied("api_key")}Copied{:else}Copy{/if}
          </button>
          <button
            class="btn btn-primary"
            onclick={onRegenerateAPIKey}
            disabled={busy}>Regenerate</button
          >
        </div>
      </div>
    </section>

    <section class="setting-category">
      <h3 class="setting-category-title">Claude Code</h3>
      <div class="setting-row">
        <p class="setting-title">Claude Code x CodexSess Integration</p>
        <div class="claude-code-status">
          <span
            class="status-badge {claudeCodeIntegration?.connected
              ? 'connected'
              : 'disconnected'}"
          >
            {#if claudeCodeIntegration?.connected}Connected{:else}Disconnected{/if}
          </span>
          <code>{claudeCodeIntegration?.base_url || "-"}</code>
        </div>
        <div class="setting-actions-grid with-three">
          <input
            value={claudeCodeIntegration?.env_file_path || "No env file yet"}
            readonly
            disabled
          />
          <button
            class="btn btn-primary"
            onclick={onEnableClaudeCodeIntegration}
            disabled={busy}
          >
            Enable Now
          </button>
        </div>
        <p class="setting-title">
          Uses the same `Model Mapping` table below. Default aliases are seeded
          once, then fully editable here.
        </p>
        {#if claudeCodeIntegration?.activate_command}
          <p class="setting-title">
            Run this in your current terminal session:
          </p>
          <div class="code-box">
            <pre>{claudeCodeIntegration.activate_command}</pre>
            <button
              class="btn btn-secondary"
              onclick={() =>
                onCopyText(
                  claudeCodeIntegration.activate_command,
                  "Claude activation command",
                  "claude_activation_command",
                )}
              disabled={busy}
            >
              {#if isCopied("claude_activation_command")}Copied{:else}Copy
                Command{/if}
            </button>
          </div>
        {/if}
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
          <select
            value={mappingTargetModel}
            onchange={(event) =>
              onSetMappingTargetModel(event.currentTarget.value)}
          >
            {#each availableModels as model}
              <option value={model}>{model}</option>
            {/each}
          </select>
          <button
            class="btn btn-primary"
            onclick={onSaveModelMapping}
            disabled={busy}
          >
            {editingMappingAlias ? "Update Mapping" : "Save Mapping"}
          </button>
          {#if editingMappingAlias}
            <button
              class="btn btn-secondary"
              onclick={onCancelEditMapping}
              disabled={busy}>Cancel</button
            >
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
                  <button
                    class="btn btn-small btn-secondary"
                    onclick={() => onStartEditMapping(alias)}
                    disabled={busy}>Edit</button
                  >
                  <button
                    class="btn btn-small btn-danger"
                    onclick={() => onDeleteModelMapping(alias)}
                    disabled={busy}>Delete</button
                  >
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
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(openAIEndpoint, "OpenAI endpoint", "openai_endpoint")}
            disabled={busy}
          >
            {#if isCopied("openai_endpoint")}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="openAiResponsesEndpoint">OpenAI Responses Endpoint</label>
        <div class="setting-actions-grid">
          <input
            id="openAiResponsesEndpoint"
            value={openAIResponsesEndpoint}
            readonly
            disabled
          />
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(
                openAIResponsesEndpoint,
                "OpenAI responses endpoint",
                "openai_responses_endpoint",
              )}
            disabled={busy}
          >
            {#if isCopied("openai_responses_endpoint")}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="claudeEndpoint">Claude Endpoint</label>
        <div class="setting-actions-grid">
          <input id="claudeEndpoint" value={claudeEndpoint} readonly disabled />
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(claudeEndpoint, "Claude endpoint", "claude_endpoint")}
            disabled={busy}
          >
            {#if isCopied("claude_endpoint")}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="authJsonEndpoint">Auth JSON Endpoint</label>
        <div class="setting-actions-grid">
          <input
            id="authJsonEndpoint"
            value={authJSONEndpoint}
            readonly
            disabled
          />
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(
                authJSONEndpoint,
                "Auth JSON endpoint",
                "auth_json_endpoint",
              )}
            disabled={busy}
          >
            {#if isCopied("auth_json_endpoint")}Copied{:else}Copy{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <label for="usageStatusEndpoint">Usage Status Endpoint</label>
        <div class="setting-actions-grid">
          <input
            id="usageStatusEndpoint"
            value={usageStatusEndpoint}
            readonly
            disabled
          />
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(
                usageStatusEndpoint,
                "Usage status endpoint",
                "usage_status_endpoint",
              )}
            disabled={busy}
          >
            {#if isCopied("usage_status_endpoint")}Copied{:else}Copy{/if}
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
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(openAIExample(), "OpenAI example", "openai_example")}
          >
            {#if isCopied("openai_example")}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Claude Request Example</p>
        <div class="code-box">
          <pre>{claudeExample()}</pre>
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(claudeExample(), "Claude example", "claude_example")}
          >
            {#if isCopied("claude_example")}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Auth JSON Download Example</p>
        <div class="code-box">
          <pre>{authJSONExample()}</pre>
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(
                authJSONExample(),
                "Auth JSON example",
                "auth_json_example",
              )}
          >
            {#if isCopied("auth_json_example")}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>

      <div class="setting-row">
        <p class="setting-title">Usage Status Example</p>
        <div class="code-box">
          <pre>{usageStatusExample()}</pre>
          <button
            class="btn btn-secondary"
            onclick={() =>
              onCopyText(
                usageStatusExample(),
                "Usage status example",
                "usage_status_example",
              )}
          >
            {#if isCopied("usage_status_example")}Copied{:else}Copy Example{/if}
          </button>
        </div>
      </div>
    </section>
  </div>
</section>
