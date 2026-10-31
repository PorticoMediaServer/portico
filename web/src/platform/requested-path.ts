import {accountReturnPath} from '../route-registry-return';

/**
 * The page a tab asked for before sign-in or Who's watching came first (C-DESIGN §2, the web half).
 * Kept per tab in sessionStorage, so a reload in the middle of signing in keeps it and no other tab
 * sees it. Used once: the first profile opened afterwards goes there instead of Home.
 */
const KEY = 'portico.requested-path.v1';
/** Long enough to sign in and pick a profile; an abandoned attempt never sends a later one elsewhere. */
export const REQUESTED_PATH_MAX_AGE_MS = 30 * 60_000;
const ENTRY_PATHS = new Set(['/', '/sign-in', '/profiles', '/servers', '/setup', '/account', accountReturnPath]);

/** In-app pages only: same origin, not an entry page, not a developer page. */
function wanted(href: string): boolean {
  if (!href.startsWith('/') || href.startsWith('//') || href.length > 2048) return false;
  let path: string;
  try {path = new URL(href, 'https://portico.invalid').pathname;} catch {return false;}
  return !ENTRY_PATHS.has(path) && !path.startsWith('/dev/') && !path.startsWith('/auth/');
}

export function rememberRequestedPath(href: string, nowMs = Date.now()): void {
  if (!wanted(href)) return;
  try {sessionStorage.setItem(KEY, JSON.stringify({href, at: nowMs}));} catch { /* Refused storage: Home opens instead. */ }
}

/** The remembered page, once; undefined when none, too old or unreadable. */
export function takeRequestedPath(nowMs = Date.now()): string | undefined {
  let raw: string | null = null;
  try {raw = sessionStorage.getItem(KEY); sessionStorage.removeItem(KEY);} catch {return undefined;}
  if (!raw) return undefined;
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== 'object') return undefined;
    const {href, at} = value as {href?: unknown; at?: unknown};
    if (typeof href !== 'string' || typeof at !== 'number' || nowMs - at > REQUESTED_PATH_MAX_AGE_MS || at > nowMs + 60_000) return undefined;
    return wanted(href) ? href : undefined;
  } catch {return undefined;}
}
