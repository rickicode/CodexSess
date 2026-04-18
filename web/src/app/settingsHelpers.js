function normalizeDirectAPIStrategy(value) {
  return String(value || '').trim().toLowerCase() === 'load_balance' ? 'load_balance' : 'round_robin'
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
    provider: 'codex'
  }
}

export {
  buildClaudeCodeIntegration,
  normalizeDirectAPIStrategy
}
