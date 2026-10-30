import {routePath} from '../../../packages/contracts/src/routes.ts';

/** UI context only. This predicate never supplies server or account authority. */
export function accountSecurityUIURL(kind: 'server-account' | 'hosted-account', origin: string, href: string) {
  let page: URL;
  try {if (!origin.startsWith('https://') || new URL(origin).origin !== origin) return false;page = new URL(href, origin);} catch {return false;}
  const keys = [...page.searchParams.keys()].sort().join(',');
  if (page.origin !== origin || page.hash || page.username || page.password) return false;
  return kind === 'server-account' ? page.pathname === '/settings' && keys === 'scope,section' && page.searchParams.getAll('scope').length === 1 && page.searchParams.getAll('section').length === 1 && page.searchParams.get('scope') === 'account' && page.searchParams.get('section') === 'profile'
    : page.pathname === '/account' && keys === 'tab' && page.searchParams.getAll('tab').length === 1 && page.searchParams.get('tab') === 'security';
}

/** Capture transport values before an original-context check waits. A readonly
 * page may GET or perform the existing nonce possession read, never mutate.
 * All original Security traffic stays at its registered origin without redirects. */
export function accountSecurityRequest(input: RequestInfo | URL, init: RequestInit | undefined, origin: string, readonly: boolean) {
  if (!origin.startsWith('https://') || new URL(origin).origin !== origin) throw new Error('The original Account Settings request ended.');
  const message = input instanceof Request ? input.clone() : input instanceof URL ? new URL(input.href) : input;
  const target = new URL(typeof message === 'string' ? message : message instanceof URL ? message.href : message.url);
  const method = (init?.method ?? (message instanceof Request ? message.method : 'GET')).toUpperCase();
  if (target.origin !== origin || target.username || target.password || target.hash || readonly && method !== 'GET' && !(method === 'POST' && !target.search && target.pathname === routePath('prove_network_identity'))) throw new Error('The original Account Settings request ended.');
  const inheritedSignal = init?.signal ?? (message instanceof Request ? message.signal : undefined);
  const request: RequestInit = {...init, method, redirect: 'error', headers: new Headers(init?.headers ?? (message instanceof Request ? message.headers : undefined)), ...(inheritedSignal ? {signal: inheritedSignal} : {})};
  return {message, request};
}

/** Availability matches protected storage's real browser prerequisites. It
 * allocates no locator, opens no storage and supplies no account authority. */
export function accountSecurityBrowserAvailable() {
  return globalThis.isSecureContext === true && location.protocol === 'https:' && Boolean(globalThis.crypto?.subtle) && typeof indexedDB !== 'undefined'
    && typeof LockManager !== 'undefined' && navigator.locks instanceof LockManager;
}
