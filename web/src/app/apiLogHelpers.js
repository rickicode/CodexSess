function prettyJSONText(raw) {
  const text = String(raw ?? '').trim();
  if (!text) return '';
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

function normalizeUsageTokens(usage) {
  if (!usage || typeof usage !== 'object') return null;
  let requestTokens = Number(usage.prompt_tokens) || 0;
  let responseTokens = Number(usage.completion_tokens) || 0;
  if (!requestTokens) requestTokens = Number(usage.input_tokens) || 0;
  if (!responseTokens) responseTokens = Number(usage.output_tokens) || 0;
  let totalTokens = Number(usage.total_tokens) || 0;
  if (!totalTokens && (requestTokens || responseTokens)) {
    totalTokens = requestTokens + responseTokens;
  }
  if (!requestTokens && !responseTokens && !totalTokens) return null;
  return { requestTokens, responseTokens, totalTokens };
}

function extractUsageTokensFromPayload(payload) {
  if (!payload || typeof payload !== 'object') return null;
  if (payload.usage) return normalizeUsageTokens(payload.usage);
  if (payload.response && payload.response.usage) return normalizeUsageTokens(payload.response.usage);
  if (payload.message && payload.message.usage) return normalizeUsageTokens(payload.message.usage);
  return null;
}

function extractUsageTokensFromResponseBody(raw) {
  const text = String(raw ?? '').trim();
  if (!text) return null;
  try {
    return extractUsageTokensFromPayload(JSON.parse(text));
  } catch {
    return null;
  }
}

function parseAPILogLine(line, index) {
  const rawLine = String(line ?? '');
  const fallback = {
    id: `raw-${index}`,
    rawLine,
    timestamp: null,
    protocol: 'unknown',
    method: '-',
    path: '(raw log line)',
    status: 0,
    latencyMS: 0,
    model: '',
    accountHint: '',
    accountID: '',
    accountEmail: '',
    requestTokens: 0,
    responseTokens: 0,
    totalTokens: 0,
    requestBody: '',
    responseBody: '',
    invalid: true
  };
  if (!rawLine.trim()) return fallback;
  try {
    const obj = JSON.parse(rawLine);
    const timestamp = typeof obj.timestamp === 'string' ? obj.timestamp : null;
    const protocol = String(obj.protocol || 'unknown').toLowerCase();
    const method = String(obj.method || '-').toUpperCase();
    const path = String(obj.path || '/');
    const status = Number(obj.status) || 0;
    const latencyMS = Number(obj.latency_ms) || 0;
    const model = String(obj.model || '').trim();
    const accountHint = String(obj.account_hint || '').trim();
    const accountID = String(obj.account_id || '').trim();
    const accountEmail = String(obj.account_email || '').trim();
    let requestTokens = Number(obj.request_tokens) || 0;
    let responseTokens = Number(obj.response_tokens) || 0;
    let totalTokens = Number(obj.total_tokens) || 0;
    const fallbackTokens = extractUsageTokensFromResponseBody(obj.response_body);
    if (fallbackTokens) {
      if (!requestTokens) requestTokens = fallbackTokens.requestTokens;
      if (!responseTokens) responseTokens = fallbackTokens.responseTokens;
      if (!totalTokens) totalTokens = fallbackTokens.totalTokens;
    }
    if (!totalTokens && (requestTokens || responseTokens)) {
      totalTokens = requestTokens + responseTokens;
    }
    return {
      id: `${timestamp || 'ts'}-${index}-${path}-${status}`,
      rawLine,
      timestamp,
      protocol,
      method,
      path,
      status,
      latencyMS,
      model,
      accountHint,
      accountID,
      accountEmail,
      requestTokens,
      responseTokens,
      totalTokens,
      requestBody: prettyJSONText(obj.request_body),
      responseBody: prettyJSONText(obj.response_body),
      invalid: false
    };
  } catch {
    return fallback;
  }
}

function formatLogTimestamp(ts) {
  if (!ts) return '-';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return '-';
  return d.toLocaleString();
}

function logStatusClass(statusCode) {
  const n = Number(statusCode) || 0;
  if (n >= 500) return 'status-5xx';
  if (n >= 400) return 'status-4xx';
  if (n >= 300) return 'status-3xx';
  if (n >= 200) return 'status-2xx';
  return 'status-unknown';
}

export {
  extractUsageTokensFromResponseBody,
  formatLogTimestamp,
  logStatusClass,
  parseAPILogLine,
  prettyJSONText
};
