/** Signing in with a Portico Account at a server's own HTTPS page (option 2, OPTION2-RETURN-NOTE):
 * this page asks its server for a challenge, keeps it in this tab with a random `state`, and sends
 * the person to web.getportico.tv, which sends back a note for exactly this page's address in the
 * fragment of `/auth/portico-return`. The fragment is cleared before anything runs
 * (`route-registry-return.ts`); this record is what ties the note to the tab that asked for it. */
const key = 'portico.accountReturn';
const tenMinutes = 10 * 60_000;
export const returnStatePattern = /^[A-Za-z0-9_-]{43}$/;
export const returnServerPattern = /^srv_[A-Za-z0-9_-]{43}$/;
/** The server's challenge as Portico Services takes it (22 to 128 characters); the server's own carry dots. */
export const returnChallengePattern = /^[A-Za-z0-9._~-]{22,128}$/;

/** `hosted` is where the Portico Account signs in (web.getportico.tv), kept for remembering the sign-in under it. */
export type AccountReturnRecord = Readonly<{state: string; challenge: string; serverId: string; origin: string; hosted: string; at: number}>;
type TabStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
const tab = (): TabStorage | undefined => {try {return globalThis.sessionStorage;} catch {return undefined;}};

function base64url(bytes: Uint8Array): string {
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** Keeps this tab's sign-in in progress (one at a time) and answers its random `state`. */
export function keepAccountReturn(input: Readonly<{challenge: string; serverId: string; origin: string; hosted: string}>, now = Date.now(), storage = tab()): string {
  if (!storage) throw Object.assign(new Error('This browser is not keeping anything for this tab.'), {code: 'storage_unavailable'});
  const state = base64url(crypto.getRandomValues(new Uint8Array(32)));
  storage.setItem(key, JSON.stringify({state, challenge: input.challenge, serverId: input.serverId, origin: input.origin, hosted: input.hosted, at: now} satisfies AccountReturnRecord));
  return state;
}

/** This tab's record, deleted on first read whether or not it matches anything; nothing after ten minutes. */
export function takeAccountReturn(now = Date.now(), storage = tab()): AccountReturnRecord | undefined {
  let raw: string | null = null;
  try {raw = storage?.getItem(key) ?? null; storage?.removeItem(key);} catch {return undefined;}
  if (!raw) return undefined;
  try {
    const record = JSON.parse(raw) as AccountReturnRecord;
    if (!returnStatePattern.test(record.state) || !returnChallengePattern.test(record.challenge) || !returnServerPattern.test(record.serverId)
      || typeof record.origin !== 'string' || typeof record.hosted !== 'string' || !httpsOrigin(record.hosted) || !Number.isFinite(record.at) || now - record.at > tenMinutes || record.at > now + 60_000) return undefined;
    return Object.freeze({...record});
  } catch {return undefined;}
}

/** Where this page sends the person: web.getportico.tv's return page, which shows the server and
 * address from the account's signed list before anything is sent. */
export function accountReturnStart(hostedOrigin: string, input: Readonly<{serverId: string; challenge: string; to: string; state: string}>): string {
  const url = new URL('/servers/return', hostedOrigin);
  url.search = new URLSearchParams({server: input.serverId, challenge: input.challenge, to: input.to, state: input.state}).toString();
  return url.href;
}

/** The note travels as base64url JSON in the fragment, which no server receives. */
export function encodeReturnNote(identity: unknown): string {
  return base64url(new TextEncoder().encode(JSON.stringify(identity)));
}
export function decodeReturnNote(text: string): unknown {
  if (!/^[A-Za-z0-9_-]{16,32768}$/.test(text)) return undefined;
  try {
    const padded = text.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - text.length % 4) % 4);
    return JSON.parse(new TextDecoder().decode(Uint8Array.from(atob(padded), c => c.charCodeAt(0))));
  } catch {return undefined;}
}

/** Where web.getportico.tv sends the note: this page's own return address. */
export function accountReturnFinish(to: string, input: Readonly<{identity: unknown; state: string}>): string {
  const url = new URL('/auth/portico-return', to);
  url.hash = new URLSearchParams({note: encodeReturnNote(input.identity), state: input.state}).toString();
  return url.href;
}

/** A captured return fragment that is a note for this tab's sign-in: exactly `note` and `state`. */
export function readReturnFragment(fragment: string): Readonly<{identity: unknown; state: string}> | undefined {
  if (!fragment.startsWith('#') || fragment.length > 40_000) return undefined;
  const values = new URLSearchParams(fragment.slice(1));
  if ([...values.keys()].sort().join(',') !== 'note,state' || values.getAll('note').length !== 1 || values.getAll('state').length !== 1) return undefined;
  const state = values.get('state')!, identity = decodeReturnNote(values.get('note')!);
  if (!returnStatePattern.test(state) || identity === undefined || identity === null || typeof identity !== 'object') return undefined;
  return Object.freeze({identity, state});
}

/** An HTTPS origin and nothing else (no path, query, fragment or credentials). */
export function httpsOrigin(text: string): string | undefined {
  try {
    const url = new URL(text);
    return url.protocol === 'https:' && url.origin === text && !url.username && !url.password ? url.origin : undefined;
  } catch {return undefined;}
}
