async function writeClipboardText(text, navigatorObj = globalThis.navigator, documentObj = globalThis.document) {
  const value = String(text || '');
  if (!value) return false;

  try {
    if (navigatorObj?.clipboard?.writeText) {
      await navigatorObj.clipboard.writeText(value);
      return true;
    }
  } catch {
    // Fallback below for non-secure context / denied clipboard permissions.
  }

  try {
    const textarea = documentObj.createElement('textarea');
    textarea.value = value;
    textarea.setAttribute('readonly', '');
    textarea.style.position = 'fixed';
    textarea.style.opacity = '0';
    textarea.style.pointerEvents = 'none';
    textarea.style.top = '-1000px';
    documentObj.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    textarea.setSelectionRange(0, textarea.value.length);
    const ok = Boolean(documentObj.execCommand && documentObj.execCommand('copy'));
    documentObj.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

function loadUIPreferences(storageKey, storage = globalThis.localStorage) {
  const fallback = {
    showAccountEmail: true,
    usageSoundEnabled: true
  };
  try {
    const raw = storage?.getItem(storageKey);
    if (!raw) return fallback;
    const parsed = JSON.parse(raw);
    return {
      showAccountEmail: parsed?.showAccountEmail !== false,
      usageSoundEnabled: parsed?.usageSoundEnabled !== false
    };
  } catch {
    return fallback;
  }
}

function saveUIPreferences(storageKey, payload, storage = globalThis.localStorage) {
  try {
    storage?.setItem(storageKey, JSON.stringify(payload));
  } catch {
  }
}

export {
  loadUIPreferences,
  saveUIPreferences,
  writeClipboardText
};
