import {sha256} from '@noble/hashes/sha2';
import {failure} from './events';

/** The SDK owns redirects. These checks contain exposure after it reports the final URL. */
type Session = {accessToken: string; refreshToken?: string; hosted: boolean};
export type Exposure = {origin: string; origins?: readonly string[]; sessions: readonly Session[]};
const bindings = new Map<string, Set<string>>();
const retired = new Set<string>();
const sessions = new Map<string, Map<string, Session>>();
const listeners = new Set<(exposure: Exposure) => void>();
const MAX_BINDINGS = 2048;
const verified = new Map<string, string>();
const verifiedAt = new Map<string, number>();
const proofs = new Map<string, {nonce: string; at: number}>();
const hash = (value: string) => Array.from(sha256(value), byte => byte.toString(16).padStart(2, '0')).join('');
const secretNames = /^(accessToken|refreshToken|trustToken|profileProof|deviceCode|pairingToken)$/i;

export function originOf(url: string): string {
  const target = new URL(url);
  if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password || target.hash) throw failure('credential_origin', 'Invalid credential destination.');
  return target.origin;
}

function secrets(value: unknown, found: string[] = [], depth = 0): string[] {
  if (!value || typeof value !== 'object' || depth > 12 || found.length >= 128) return found;
  for (const [name, entry] of Object.entries(value)) {
    if (secretNames.test(name) && typeof entry === 'string' && entry) found.push(entry);
    else if (entry && typeof entry === 'object') secrets(entry, found, depth + 1);
    if (found.length >= 128) break;
  }
  return found;
}
export function jsonValue(body: string | null): unknown {
  try { return body ? JSON.parse(body) : undefined; } catch { return undefined; }
}
function bind(origin: string, token: string): void {
  const key = hash(token), existing = bindings.get(key);
  if (existing && !existing.has(origin) && Date.now() - (verifiedAt.get(origin) ?? 0) <= 120000 && verified.get(origin) && [...existing].some(issuer => verified.get(issuer) === verified.get(origin))) existing.add(origin);
  if (retired.has(key) || (existing && !existing.has(origin))) throw failure('credential_origin', 'The credential belongs to another connection.');
  if (!existing && bindings.size >= MAX_BINDINGS) throw failure('credential_limit', 'Too many credentials are held by this connection.');
  bindings.set(key, existing ?? new Set([origin]));
}

/** Called on an accepted issuer response or a remembered record, never a redirected response. */
export function rememberCredentials(url: string, value: unknown): void {
  const origin = originOf(url);
  for (const token of secrets(value)) bind(origin, token);
  const visit = (entry: unknown, depth = 0) => {
    if (!entry || typeof entry !== 'object' || depth > 12) return;
    const record = entry as Record<string, unknown>;
    if (typeof record.accessToken === 'string' && record.accessToken) {
      const family = record.sessionFamilyId ?? record.familyId;
      const held = sessions.get(origin) ?? new Map<string, Session>();
      const key = typeof family === 'string' ? family : 'wire';
      if (!held.has(key) && held.size >= 8) throw failure('credential_limit', 'Too many sign-ins are held by this connection.');
      if (key !== 'wire' && held.get('wire')?.accessToken === record.accessToken) held.delete('wire');
      held.set(key, {accessToken: record.accessToken, ...(typeof record.refreshToken === 'string' ? {refreshToken: record.refreshToken} : {}), hosted: typeof record.familyId === 'string'});
      sessions.set(origin, held);
    }
    for (const child of Object.values(record)) if (child && typeof child === 'object') visit(child, depth + 1);
  };
  visit(value);
}

export function requestCredentials(url: string, headers: unknown, body: string | null): boolean {
  const origin = originOf(url);
  const tokens = secrets(jsonValue(body));
  const parsed = new URL(url);
  const proof = jsonValue(body) as {baseUrl?: string; nonce?: string} | undefined;
  const publicProof = parsed.pathname === '/v1/networking/identity-proof' && proof && typeof proof === 'object' &&
    Object.keys(proof).length === 2 && proof.baseUrl === origin && typeof proof.nonce === 'string' && /^[A-Za-z0-9_-]{43}$/.test(proof.nonce);
  let sensitive = body !== null && !publicProof;
  if (publicProof) {
    if (proofs.size >= 64) proofs.delete(proofs.keys().next().value!);
    proofs.set(origin, {nonce: proof.nonce!, at: Date.now()});
  }
  if (headers && typeof headers === 'object') for (const [name, value] of Object.entries(headers)) {
    if (typeof value !== 'string') continue;
    if (/^(cookie|cookie2)$/i.test(name)) throw failure('credential_origin', 'Cookies are not used by this connection.');
    if (/^(authorization|x-profile-proof)$/i.test(name)) {
      const token = value.replace(/^Bearer\s+/i, '');
      tokens.push(token); sensitive = true;
      if (/^authorization$/i.test(name) && ![...(sessions.get(origin)?.values() ?? [])].some(session => session.accessToken === token)) rememberCredentials(url, {accessToken: token});
    }
  }
  // Media URLs already carry short-lived playback/resource grants, never the account bearer.
  if (parsed.pathname.startsWith('/v1/media/')) {
    sensitive = true;
    const parts = parsed.pathname.split('/');
    const grant = parts[3] === 'subtitles' ? parts[4] : parts[3] === 'linear' ? parts[6] : parts[3];
    if (grant) tokens.push(grant);
  }
  for (const [name, value] of parsed.searchParams) if (/token|grant|proof|key/i.test(name)) { sensitive = true; tokens.push(value); }
  for (const token of tokens) bind(origin, token);
  return sensitive;
}

export function onCredentialExposure(listener: (exposure: Exposure) => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function exposeCredentials(url: string): void {
  const origin = originOf(url);
  const identity = verified.get(origin);
  const origins = new Set([origin, ...[...verified].filter(([, fingerprint]) => identity && fingerprint === identity).map(([address]) => address)]);
  const held = [...new Map([...origins].flatMap(address => [...(sessions.get(address)?.values() ?? [])]).map(session => [session.accessToken, session])).values()];
  for (const address of origins) sessions.delete(address);
  let changed = false;
  for (const [key, issuers] of bindings) if ([...issuers].some(address => origins.has(address)) && !retired.has(key)) { retired.add(key); changed = true; }
  // Also notify for password/pairing/media-only requests, which may not have issued a session.
  if (!changed && !held.length && exposed.has(origin)) return;
  exposed.add(origin);
  for (const listener of [...listeners]) { try { listener({origin, origins: [...origins], sessions: held}); } catch { /* containment still runs */ } }
}
const exposed = new Set<string>();

/** No response bytes are accepted before this check, including errors and streamed chunks. */
export function checkResponse(url: string, answered: string | undefined | null, status: number, sensitive: boolean): void {
  if (status === 0 && !answered) throw failure('request_transport', 'The connection did not return a response.');
  const asked = new URL(url);
  let final: URL | undefined;
  try { if (answered) final = new URL(answered); } catch { /* invalid final address */ }
  const differentOrigin = !final || final.origin !== asked.origin || !!final.username || !!final.password;
  if (differentOrigin && sensitive) exposeCredentials(url);
  if (!final || final.href !== asked.href || (status >= 300 && status < 400 && status !== 304)) {
    throw failure(differentOrigin && sensitive ? 'credential_exposed' : 'request_redirect', 'The response came from a different address.');
  }
}

/** Only the crypto bridge calls this after verifying the signed, client-nonce-bound proof.
 * The shared route owner still validates the pin, identity, clock and route before dispatch.
 */
export function verifiedCredentialOrigin(value: unknown): void {
  if (!value || typeof value !== 'object') return;
  const proof = value as Record<string, unknown>;
  if (proof.kind !== 'portico.route-proof' || proof.version !== '1' || typeof proof.baseUrl !== 'string' || typeof proof.fingerprint !== 'string') return;
  const held = proofs.get(proof.baseUrl);
  if (!held || held.nonce !== proof.nonce || Date.now() - held.at > 120000) return;
  proofs.delete(proof.baseUrl);
  if (verified.size >= 64 && !verified.has(proof.baseUrl)) {
    const oldest = verified.keys().next().value!; verified.delete(oldest); verifiedAt.delete(oldest);
  }
  verified.set(originOf(proof.baseUrl), proof.fingerprint);
  verifiedAt.set(originOf(proof.baseUrl), Date.now());
}

/** Successful sign-out releases the retired family from the small containment inventory. */
export function forgetSession(url: string, headers: Record<string, string>): void {
  originOf(url);
  const token = Object.entries(headers).find(([name]) => /^authorization$/i.test(name))?.[1].replace(/^Bearer\s+/i, '');
  if (!token) return;
  for (const [address, held] of sessions) for (const [family, session] of held) {
    if (session.accessToken !== token) continue;
    retired.add(hash(session.accessToken));
    if (session.refreshToken) retired.add(hash(session.refreshToken));
    held.delete(family);
    if (!held.size) sessions.delete(address);
  }
}
