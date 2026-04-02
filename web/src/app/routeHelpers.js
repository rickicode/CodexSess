function menuFromPath(path) {
  const p = String(path || '').trim().toLowerCase();
  if (p === '/settings') return 'settings';
  if (p === '/api-activity') return 'logs';
  if (p === '/workspaces') return 'api-endpoints';
  if (p === '/systemlogs') return 'system-logs';
  if (p === '/about') return 'about';
  if (p === '/' || p === '/dashboard') return 'dashboard';
  if (p === '/chat' || p.startsWith('/chat/')) return 'coding';
  return 'dashboard';
}

function pathForMenu(menu) {
  switch (String(menu || '').trim().toLowerCase()) {
    case 'settings':
      return '/settings';
    case 'logs':
      return '/api-activity';
    case 'api-endpoints':
      return '/workspaces';
    case 'system-logs':
      return '/systemlogs';
    case 'about':
      return '/about';
    case 'coding':
      return '/chat';
    default:
      return '/';
  }
}

function detectChatRoute(path) {
  const value = String(path || '').trim().toLowerCase();
  return value === '/chat' || value.startsWith('/chat/');
}

function documentTitleByMenu(menu) {
  const base = 'CodexSess Console';
  const key = String(menu || '').trim().toLowerCase();
  switch (key) {
    case 'coding':
      return `Chat - ${base}`;
    case 'settings':
      return `Settings - ${base}`;
    case 'api-endpoints':
      return `Workspaces - ${base}`;
    case 'logs':
      return `API Activity - ${base}`;
    case 'system-logs':
      return `System Logs - ${base}`;
    case 'about':
      return `About - ${base}`;
    default:
      return base;
  }
}

export {
  detectChatRoute,
  documentTitleByMenu,
  menuFromPath,
  pathForMenu
};
