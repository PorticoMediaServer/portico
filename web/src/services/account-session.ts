import {porticoAccount, type HeldSignIn, type Tokens} from '@core/portico-account.ts';

/** This browser's sign-in to its Portico Account. Portico Services keeps its refresh token in an
 * HttpOnly cookie that no script here can read, and this page keeps the access token in memory
 * only, so a new tab or a reload renews once to get one. Storage holds only the flag that this
 * browser has a sign-in: no call is spent renewing when it has none, and every tab hears when
 * another signs in or out (a sign-in lasts 90 days from last use, and signing out in one tab
 * signs out all). */
const flag = 'portico.account.signedIn';
const listeners = new Set<() => void>();
let current: Tokens | undefined;
let flagged = readFlag();
// Before the cookie, this browser kept both tokens in storage; leave none behind.
try { localStorage.removeItem('portico.account.signIn'); } catch { /* private window */ }

function readFlag(): boolean {
  try { return localStorage.getItem(flag) === '1'; } catch { return false; }
}
function notify() { for (const listener of listeners) listener(); }
function write(tokens: Tokens | undefined) {
  current = tokens;
  if (flagged !== (tokens !== undefined)) {
    flagged = tokens !== undefined;
    try { if (flagged) localStorage.setItem(flag, '1'); else localStorage.removeItem(flag); } catch { /* private window: this tab only */ }
  }
  notify();
}
if (typeof window !== 'undefined') window.addEventListener('storage', event => {
  if (event.key !== flag && event.key !== null) return;
  // Another tab signed in or out: this tab's access token, if any, is no longer the one to use.
  current = undefined;
  flagged = readFlag();
  notify();
});

const held: HeldSignIn = {
  current: () => current,
  keep: write,
  // compareSessionAuthority is for media server sign-ins; a Portico Services sign-in has no session family or generation, so the sign-in held is compared as itself.
  forget: tokens => { if (current === tokens) write(undefined); },
  kept: () => flagged,
};

export const accountSession = {
  subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  signedIn: () => current !== undefined || flagged,
  keep(tokens: Tokens) { write(tokens); },
  /** Forget whatever sign-in is held (after a password reset ended them all). */
  forget() { write(undefined); },
};

/** The Portico Account at the Portico Services this page was served by. */
export const account = porticoAccount(held, '', {cookie: true});
