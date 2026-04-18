function buildOpenAIExample({ endpoint, apiKey }) {
  return `curl ${endpoint || 'http://127.0.0.1:3061/v1/chat/completions'} \\\n  -H "Authorization: Bearer ${apiKey || 'sk-...'}" \\\n  -H "Content-Type: application/json" \\\n  -d '{
    "model": "gpt-5.2-codex",
    "messages": [{"role":"user","content":"Reply exactly with: OK"}],
    "stream": false
  }'`;
}

function buildClaudeExample({ endpoint, apiKey }) {
  return `curl ${endpoint || 'http://127.0.0.1:3061/v1/messages'} \\\n  -H "x-api-key: ${apiKey || 'sk-...'}" \\\n  -H "Content-Type: application/json" \\\n  -d '{
    "model": "gpt-5.2-codex",
    "max_tokens": 512,
    "messages": [{"role":"user","content":"Reply exactly with: OK"}],
    "stream": false
  }'`;
}

function buildAuthJSONExample({ endpoint, apiKey }) {
  return `curl ${endpoint || 'http://127.0.0.1:3061/v1/auth.json'} \\\n  -H "Authorization: Bearer ${apiKey || 'sk-...'}" \\\n  -o auth.json`;
}

function buildUsageStatusExample({ endpoint, apiKey }) {
  return `curl "${endpoint || 'http://127.0.0.1:3061/v1/usage'}?refresh=1" \\\n   -H "Authorization: Bearer ${apiKey || 'sk-...'}"`;
}

export {
  buildAuthJSONExample,
  buildClaudeExample,
  buildOpenAIExample,
  buildUsageStatusExample
};
