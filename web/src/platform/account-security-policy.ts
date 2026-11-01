/** UI context only. This predicate never supplies server or account authority. */
export function accountSecurityUIURL(origin: string, href: string) {
  let page: URL;
  try {if (!origin.startsWith('https://') || new URL(origin).origin !== origin) return false;page = new URL(href, origin);} catch {return false;}
  const keys = [...page.searchParams.keys()].sort().join(',');
  if (page.origin !== origin || page.hash || page.username || page.password) return false;
  return page.pathname === '/account' && keys === 'tab' && page.searchParams.getAll('tab').length === 1 && page.searchParams.get('tab') === 'security';
}

/** Availability matches protected storage's real browser prerequisites. It
 * allocates no locator, opens no storage and supplies no account authority. */
export function accountSecurityBrowserAvailable() {
  return globalThis.isSecureContext === true && location.protocol === 'https:' && Boolean(globalThis.crypto?.subtle) && typeof indexedDB !== 'undefined'
    && typeof LockManager !== 'undefined' && navigator.locks instanceof LockManager;
}
