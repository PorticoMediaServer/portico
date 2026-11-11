import {servicesCall, ServicesError, type Tokens} from './services-api';

/** This browser's sign-in to its Portico Account. It is kept in this browser's storage so the
 * person stays signed in across tabs and visits (a sign-in lasts 90 days from last use), and is
 * shared by every tab: signing out in one signs out all. */
const key = 'portico.account.signIn';
const listeners = new Set<() => void>();
let current: Tokens | undefined = read();
let renewing: Promise<Tokens | undefined> | undefined;

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

export const accountSession = {
  subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  signedIn: () => current !== undefined,
  keep(tokens: Tokens) { write(tokens); },
  forget() { write(undefined); },
};

/** Renews the access token once it has run out. A renewal sent twice (two tabs) gets the same
 * answer from Services, so the tabs never sign each other out. */
async function renew(): Promise<Tokens | undefined> {
  const held = current;
  if (!held) return undefined;
  renewing ??= servicesCall<Tokens>('POST', '/v1/sessions/refresh', {refreshToken: held.refreshToken})
    .then(tokens => { write(tokens); return tokens; })
    .catch(error => { if (error instanceof ServicesError && error.code === 'signed_out' && current?.refreshToken === held.refreshToken) write(undefined); throw error; })
    .finally(() => { renewing = undefined; });
  return renewing;
}

/** A call as the signed-in person. A token that has run out is renewed first; a sign-in that
 * has ended (signed out elsewhere, password changed) is forgotten here too. */
export async function personCall<T>(method: string, path: string, body?: unknown, options: {create?: boolean; signal?: AbortSignal} = {}): Promise<T> {
  let tokens = current;
  if (!tokens) throw new ServicesError(401, 'signed_out');
  if (Date.parse(tokens.expiresAt) - Date.now() < 60_000) tokens = await renew();
  if (!tokens) throw new ServicesError(401, 'signed_out');
  try {
    return await servicesCall<T>(method, path, body, {...options, token: tokens.accessToken});
  } catch (error) {
    if (!(error instanceof ServicesError) || error.code !== 'signed_out') throw error;
    // Another tab may have renewed already: a different held sign-in is the newer one.
    const renewed = current && current !== tokens ? current : await renew().catch(() => undefined);
    if (!renewed) { if (current === tokens) write(undefined); throw error; }
    return servicesCall<T>(method, path, body, {...options, token: renewed.accessToken});
  }
}

/** Sign out here: Services ends this sign-in; this browser forgets it either way. */
export async function signOut(): Promise<void> {
  try { await personCall('DELETE', '/v1/sessions/current'); } catch { /* forgotten below */ }
  write(undefined);
}
