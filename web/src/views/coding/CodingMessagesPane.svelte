<script>
  let {
    canLoadMoreChat = false,
    loadingOlderMessages = false,
    loadingMessages = false,
    sending = false,
    backgroundProcessing = false,
    streamingPending = false,
    streamingLabel = 'Streaming',
    alwaysShowStreamingNote = false,
    messagesViewport = $bindable(null),
    visibleRenderedMessages = [],
    showScrollBottomButton = false,
    shouldHideRenderedMessage = () => false,
    isInternalRunnerActivity = () => false,
    isMessageExpanded = () => false,
    messageRoleClass = () => '',
    messageRoleLabel = () => '',
    messageDisplayContent = () => '',
    parsePlanningFinalPlan = () => null,
    messagePreviewContent = () => '',
    messageShowsAssistantUsage = () => false,
    assistantUsageSummary = () => '',
    shouldCollapseContent = () => false,
    execStatusLabel = () => '',
    subagentStatusLabel = () => '',
    subagentDisplayTitle = () => '',
    subagentPreview = () => '',
    normalizeExecCommandForDisplay = (value) => String(value || '').trim(),
    formatWhen = () => '',
    fileOperationDisplayParts = () => ({ action: '', path: '', stats: '' }),
    fileOpTone = () => '',
    activeLiveMessageID = '',
    onScroll = () => {},
    onLoadOlder = () => {},
    onJumpToLatest = () => {},
    onToggleExpand = () => {},
    onOpenExec = () => {},
    onOpenSubagent = () => {}
  } = $props()
</script>

<div class="coding-messages-wrap">
  <div class="coding-messages" bind:this={messagesViewport} onscroll={onScroll}>
    {#if canLoadMoreChat}
      <div class="coding-load-more-wrap">
        <button class="btn btn-secondary btn-small coding-load-more-btn" type="button" onclick={onLoadOlder} disabled={loadingOlderMessages || loadingMessages}>
          {loadingOlderMessages ? 'Loading history...' : 'Show earlier messages'}
        </button>
      </div>
    {/if}
    {#if loadingOlderMessages}
      <p class="setting-title coding-loading-older">Loading earlier messages...</p>
    {/if}

    {#if loadingMessages && visibleRenderedMessages.length === 0}
      <p class="empty-note">Loading messages...</p>
    {:else if visibleRenderedMessages.length === 0}
      <p class="empty-note">No messages in this session yet.</p>
    {:else}
      {#each visibleRenderedMessages as message (message.id)}
        {#if !shouldHideRenderedMessage(message)}
          {@const internalRunnerActivity = isInternalRunnerActivity(message)}
          {@const planningFinalPlan = parsePlanningFinalPlan(message)}
          <article class={`coding-message ${messageRoleClass(message)} ${message.pending && !internalRunnerActivity ? 'pending' : ''} ${message.failed ? 'failed' : ''} ${String(activeLiveMessageID || '').trim() === String(message.id || '').trim() ? 'is-live' : ''} ${internalRunnerActivity ? 'is-static-activity' : ''}`}>
            <div class="coding-message-head">
              <strong>{messageRoleLabel(message)}</strong>
              <span>{formatWhen(message.updated_at || message.created_at)}</span>
            </div>

            {#if message.role === 'exec'}
              <button class="coding-exec-summary" type="button" onclick={() => onOpenExec(message)}>
                <span class={`coding-exec-state ${String(message.exec_status || 'running').trim().toLowerCase()}`}>{execStatusLabel(message.exec_status, message.exec_exit_code)}</span>
                <code class="mono" title={normalizeExecCommandForDisplay(message.exec_command || message.content || '-', 500)}>
                  {normalizeExecCommandForDisplay(message.exec_command || message.content || '-')}
                </code>
              </button>
            {:else if message.role === 'activity' && message.mcp_activity}
              <div class={`coding-mcp-summary ${message.mcp_activity_generic ? 'is-status' : 'is-tool'}`}>
                <span class="coding-mcp-badge">MCP</span>
                <pre>{messageDisplayContent(message) || '-'}</pre>
              </div>
            {:else if message.role === 'subagent'}
              <!-- svelte-ignore a11y_click_events_have_key_events -->
              <!-- svelte-ignore a11y_no_static_element_interactions -->
              <div class="coding-subagent-summary" onclick={() => onOpenSubagent(message)}>
                <code class="mono" title={subagentDisplayTitle(message)}>
                  {subagentDisplayTitle(message)}
                </code>
                {#if subagentPreview(message)}
                  <pre>{subagentPreview(message)}</pre>
                {/if}
              </div>
            {:else if message.role === 'activity' && internalRunnerActivity}
              <div class={`coding-runner-activity ${String(activeLiveMessageID || '').trim() === String(message.id || '').trim() ? 'is-live' : ''}`} role="status" aria-live="polite">
                <span class="coding-runner-activity-actor">{messageRoleLabel(message)}</span>
                <span class="coding-runner-activity-sep" aria-hidden="true">·</span>
                <span class="coding-runner-activity-text">{messageDisplayContent(message) || '-'}</span>
              </div>
            {:else if message.role === 'activity' && message.file_op}
              {@const fileOp = fileOperationDisplayParts(message.file_op)}
              {@const tone = fileOpTone(fileOp.action || message.file_op)}
              <div class="coding-activity-fileop">
                <span class={`coding-fileop-label ${tone}`}>
                  <span class="coding-fileop-action">{fileOp.action || message.file_op}</span>
                  {#if fileOp.path}
                    <span class="coding-fileop-sep">·</span>
                    <span class="coding-fileop-path mono" title={fileOp.path}>{fileOp.path}</span>
                  {/if}
                  {#if fileOp.stats}
                    <span class="coding-fileop-sep">·</span>
                    <span class="coding-fileop-stats mono">{fileOp.stats}</span>
                  {/if}
                </span>
              </div>
            {:else if planningFinalPlan}
              <div class="coding-plan-card">
                <section class="coding-plan-section">
                  <p class="coding-plan-heading">Summary</p>
                  <p class="coding-plan-summary">{planningFinalPlan.summary}</p>
                </section>
                <section class="coding-plan-section">
                  <p class="coding-plan-heading">Tasks</p>
                  <ul class="coding-plan-list">
                    {#each planningFinalPlan.tasks as task}
                      <li class="mono">{task.replace(/^- \[ \]\s+/i, '').trim()}</li>
                    {/each}
                  </ul>
                </section>
                <section class="coding-plan-section">
                  <p class="coding-plan-heading">Stop Conditions</p>
                  <ul class="coding-plan-list coding-plan-list-stop">
                    {#each planningFinalPlan.stopConditions as item}
                      <li>{item.replace(/^-\s+/, '').trim()}</li>
                    {/each}
                  </ul>
                </section>
                <div class="coding-plan-meta">
                  {#if planningFinalPlan.ready}
                    <span class="coding-plan-ready mono">IMPLEMENTATION PLAN: READY</span>
                  {/if}
                  {#if planningFinalPlan.confidence !== null}
                    <span class="coding-plan-confidence mono">Confidence: {planningFinalPlan.confidence}%</span>
                  {/if}
                </div>
              </div>
              {#if messageShowsAssistantUsage(message) && assistantUsageSummary(message)}
                <p class="setting-title coding-assistant-usage mono">{assistantUsageSummary(message)}</p>
              {/if}
            {:else}
              {@const bodyText = messageDisplayContent(message) || ''}
              <pre>{isMessageExpanded(message.id) ? bodyText : messagePreviewContent(bodyText)}</pre>
              {@const showMoreAvailable = shouldCollapseContent(bodyText)}
              {#if messageShowsAssistantUsage(message) && assistantUsageSummary(message)}
                <p class="setting-title coding-assistant-usage mono">{assistantUsageSummary(message)}</p>
              {/if}
              {#if showMoreAvailable}
                <div class="coding-message-actions">
                  <div class="coding-message-actions-right">
                    {#if showMoreAvailable}
                      <button class="coding-inline-action coding-show-more" type="button" onclick={() => onToggleExpand(message.id)}>
                        {isMessageExpanded(message.id) ? 'Show less' : 'Show more'}
                      </button>
                    {/if}
                  </div>
                </div>
              {/if}
            {/if}
          </article>
        {/if}
      {/each}
    {/if}

    {#if (sending || backgroundProcessing || streamingPending) && (alwaysShowStreamingNote || !String(activeLiveMessageID || '').trim())}
      <div class="coding-streaming-note" role="status" aria-live="polite">
        <span class="streaming-pulse" aria-hidden="true"></span>
        <span class="streaming-label">{streamingLabel}</span>
        <span class="streaming-dots" aria-hidden="true"></span>
        <span class="streaming-bar" aria-hidden="true"><i></i></span>
      </div>
    {/if}
  </div>

  {#if showScrollBottomButton}
    <button class="btn btn-secondary btn-small coding-scroll-bottom-btn" type="button" onclick={onJumpToLatest}>
      Jump to latest
    </button>
  {/if}
</div>
