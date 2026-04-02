function startEditMappingState(alias, modelMappings, availableModels, fallbackModel = 'gpt-5.2-codex') {
  const key = String(alias || '').trim()
  if (!key) return null
  const current = String(modelMappings?.[key] || '').trim()
  let mappingTargetModel = fallbackModel
  if (current && availableModels.includes(current)) {
    mappingTargetModel = current
  } else if (availableModels.length > 0) {
    mappingTargetModel = availableModels[0]
  }
  return {
    editingMappingAlias: key,
    mappingAlias: key,
    mappingTargetModel
  }
}

function cancelEditMappingState(availableModels, fallbackModel = 'gpt-5.2-codex') {
  return {
    editingMappingAlias: '',
    mappingAlias: '',
    mappingTargetModel: availableModels[0] || fallbackModel
  }
}

export {
  cancelEditMappingState,
  startEditMappingState
}
