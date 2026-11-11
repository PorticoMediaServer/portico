import {porticoAccount, type HeldSignIn, type Tokens} from '@core/portico-account.ts';

/** This browser's sign-in to its Portico Account. It is kept in this browser's storage so the
 * person stays signed in across tabs and visits (a sign-in lasts 90 days from last use), and is
 * shared by every tab: signing out in one signs out all. */
const key = 'portico.account.signIn';
const listeners = new Set<() => void>();
let current: Tokens | undefined = read();

function read(): Tokens | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(key) ?? 'null') as Tokens | null;
    const text = (field: unknown) => typeof field === 'string';
    return value && text(value.accessToken) && text(value.refreshToken) ? value : undefined;
  } catch { return undefined; }
}
function write(tokens: Tokens | undefined) {
  current = tokens;
  try { if (tokens) localStorage.setItem(key, JSON.stringify(tokens)); else localStorage.removeItem(key); } catch { /* private window: this tab only */ }
  for (const listener of listeners) listener();
}
if (typeof window !== 'undefined') window.addEventListener('storage', event => {
  if (event.key !== key) return;
  current = read();
  for (const listener of listeners) listener();
});

const held: HeldSignIn = {current: () => current, keep: write, forget: tokens => { if (current === tokens) write(undefined); }};

export const accountSession = {
  subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  signedIn: () => current !== undefined,
  keep(tokens: Tokens) { write(tokens); },
  /** Forget whatever sign-in is held (after a password reset ended them all). */
  forget() { write(undefined); },
};

/** The Portico Account at the Portico Services this page was served by. */
export const account = porticoAccount(held);
