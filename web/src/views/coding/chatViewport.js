function hiddenRenderedMessagesCount(renderedMessages, visibleRenderedMessageLimit) {
  const total = Array.isArray(renderedMessages) ? renderedMessages.length : 0
  const limit = Number(visibleRenderedMessageLimit || 0) || 0
  return Math.max(0, total - limit)
}

function canLoadMoreChat(renderedMessages, visibleRenderedMessageLimit, hasMoreMessages) {
  return hiddenRenderedMessagesCount(renderedMessages, visibleRenderedMessageLimit) > 0 || Boolean(hasMoreMessages)
}

function isViewportNearBottom(viewport, thresholdPx = 120) {
  if (!viewport) return true
  const threshold = Number(thresholdPx || 0) || 0
  const distance = viewport.scrollHeight - (viewport.scrollTop + viewport.clientHeight)
  return distance <= threshold
}

export {
  canLoadMoreChat,
  hiddenRenderedMessagesCount,
  isViewportNearBottom
}
