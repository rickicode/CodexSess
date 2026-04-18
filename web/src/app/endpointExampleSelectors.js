function openAIExample({ openAIEndpoint, apiKey, buildOpenAIExample }) {
  return buildOpenAIExample({ endpoint: openAIEndpoint, apiKey })
}

function claudeExample({ claudeEndpoint, apiKey, buildClaudeExample }) {
  return buildClaudeExample({ endpoint: claudeEndpoint, apiKey })
}

function authJSONExample({ authJSONEndpoint, apiKey, buildAuthJSONExample }) {
  return buildAuthJSONExample({ endpoint: authJSONEndpoint, apiKey })
}

function usageStatusExample({ usageStatusEndpoint, apiKey, buildUsageStatusExample }) {
  return buildUsageStatusExample({ endpoint: usageStatusEndpoint, apiKey })
}

export {
  authJSONExample,
  claudeExample,
  openAIExample,
  usageStatusExample
}
