function normalizeAPIMode(value) {
  return String(value || '').trim().toLowerCase() === 'direct_api' ? 'direct_api' : 'codex_cli'
}

function normalizeDirectAPIStrategy(value) {
  return String(value || '').trim().toLowerCase() === 'load_balance' ? 'load_balance' : 'round_robin'
}

function normalizeZoStrategy(value) {
  return String(value || '').trim().toLowerCase() === 'manual' ? 'manual' : 'round_robin'
}

function buildClaudeCodeIntegration(data) {
  const cc = (data?.claude_code && typeof data.claude_code === 'object') ? data.claude_code : {}
  return {
    connected: cc.connected === true,
    base_url: String(cc.base_url || '').trim(),
    env_file_path: String(cc.env_file_path || '').trim(),
    profiles: Array.isArray(cc.profiles) ? cc.profiles : [],
    model_preset: (cc.model_preset && typeof cc.model_preset === 'object') ? cc.model_preset : {},
    activate_command: String(cc.activate_command || '').trim(),
    provider: 'codex',
    zo_model: ''
  }
}

export {
  buildClaudeCodeIntegration,
  normalizeAPIMode,
  normalizeDirectAPIStrategy,
  normalizeZoStrategy
}
