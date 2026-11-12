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
/** Counts the sign-ins this page has seen begin, here or in another tab. */
let signIns = 0;
// The flag's value changes at each new sign-in (not at a renewal), so other tabs also hear a
// sign-in made over another one with no sign-out between.
let mark = readFlag();
let flagged = mark !== null;
// Before the cookie, this browser kept both tokens in storage; leave none behind.
try { localStorage.removeItem('portico.account.signIn'); } catch { /* private window */ }

function readFlag(): string | null {
  try { return localStorage.getItem(flag); } catch { return null; }
}
function notify() { for (const listener of listeners) listener(); }
function write(tokens: Tokens | undefined, began = false) {
  current = tokens;
  const next = tokens === undefined ? null : began ? Date.now().toString(36) : mark ?? '1';
  if (next !== mark) {
    mark = next; flagged = next !== null;
    try { if (next !== null) localStorage.setItem(flag, next); else localStorage.removeItem(flag); } catch { /* private window: this tab only */ }
  }
  notify();
}
if (typeof window !== 'undefined') window.addEventListener('storage', event => {
  if (event.key !== flag && event.key !== null) return;
  // Another tab signed in or out: this tab's access token, if any, is no longer the one to use.
  current = undefined;
  const next = readFlag();
  if (next !== null && next !== mark) signIns++;
  mark = next; flagged = next !== null;
  notify();
});

const held: HeldSignIn = {
  current: () => current,
  keep: write,
  // compareSessionAuthority is for media server sign-ins; a Portico Services sign-in has no session family or generation, so the sign-in held is compared as itself.
  forget: tokens => { if (current === tokens) write(undefined); },
  kept: () => flagged,
  began: tokens => { signIns++; write(tokens, true); },
};

export const accountSession = {
  subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  signedIn: () => current !== undefined || flagged,
  /** Changes when a sign-in begins (here or in another tab): the account may now be another one. */
  signIns: () => signIns,
  keep(tokens: Tokens) { write(tokens); },
  /** Forget whatever sign-in is held (after a password reset ended them all). */
  forget() { write(undefined); },
};

/** The Portico Account at the Portico Services this page was served by. */
export const account = porticoAccount(held, '', {cookie: true});
