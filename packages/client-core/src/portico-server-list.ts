/** A Portico Account's servers as Portico Services signs them (`portico.server-list`), checked here
 * the way `server-next/internal/hosted/trust` checks it: the envelope's certificate against the root
 * this app was built with, the list's signature against the certified key, the account, a version no
 * lower than the one held, and every server's id against the digest of its key. */
import type {RouteCrypto} from './route-identity.ts';
import type {HostedServer} from './http.ts';
import {porticoAccount} from './portico-account.ts';
import {heldFor} from './portico-services-native.ts';

/** `trust/hosted-root-public-key.pem` (a test keeps the two equal): the raw Ed25519 key is the
 * last 32 bytes of its SubjectPublicKeyInfo. */
export const PORTICO_ROOT_PUBLIC_KEY = 'MCowBQYDK2VwAyEAmD0kQgS+0iEk+ZToKZmpGWNWmoT9Z7EzkIipPtvJ1O0=';
const rootKey = (spki = PORTICO_ROOT_PUBLIC_KEY) => bytes(atob(spki)).slice(-32);

export type Certificate = Readonly<{keyId: string; publicKey: string; purpose: string; notBefore: string; notAfter: string; revision: number; revoked: readonly string[]; signature: string}>;
export type Envelope = Readonly<{payload: string; signature: string; keyId: string; certificate?: Certificate}>;
export type ListedServer = Readonly<{serverId: string; publicKey: string; name: string; role: string; owner: boolean; addresses: readonly string[]}>;
export type ServerList = Readonly<{kind: 'portico.server-list'; accountId: string; version: number; servers: readonly ListedServer[]; issuedAt: string}>;

export class ServerListRefused extends Error {
  readonly code = 'invalid_response';
  constructor() { super('This server list is not signed by Portico Services.'); }
}
const refuse = (): never => { throw new ServerListRefused(); };
const bytes = (binary: string) => Uint8Array.from(binary, c => c.charCodeAt(0));
const utf8 = (text: string) => new TextEncoder().encode(text);
/** Strict unpadded base64url, as Go's `base64.RawURLEncoding.Strict()` reads it. */
function base64url(value: unknown): Uint8Array {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]*$/.test(value) || value.length % 4 === 1) return refuse();
  const out = bytes(atob(value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4)));
  if (encode(out) !== value) return refuse();
  return out;
}
const encode = (raw: Uint8Array) => btoa(String.fromCharCode(...raw)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const hex = (raw: Uint8Array) => Array.from(raw, b => b.toString(16).padStart(2, '0')).join('');
const concat = (a: Uint8Array, b: Uint8Array) => { const out = new Uint8Array(a.length + b.length); out.set(a); out.set(b, a.length); return out; };

const MAXIMUM_CERTIFICATE_LIFETIME_MS = 400 * 86400000;
const certificateLabel = 'portico.hosted.signing-certificate.v1\x00';
/** The bytes the root signed: the label, then the certificate as Go marshals it with an empty
 * signature (its fields in order; every value here is plain ASCII, so JSON.stringify writes the
 * same bytes as Go's encoder). */
const signedBytes = (c: Certificate) => utf8(certificateLabel + JSON.stringify({keyId: c.keyId, publicKey: c.publicKey, purpose: c.purpose,
  notBefore: c.notBefore, notAfter: c.notAfter, revision: c.revision, revoked: c.revoked, signature: ''}));

async function certifiedKey(c: Certificate | undefined, root: Uint8Array, now: number, crypto: RouteCrypto): Promise<Uint8Array> {
  if (!c || typeof c !== 'object' || typeof c.keyId !== 'string' || !c.keyId || c.keyId.length > 128 || !Array.isArray(c.revoked) || c.revoked.length > 4096
    || c.revoked.some(id => typeof id !== 'string') || !Number.isSafeInteger(c.revision) || c.revision < 1 || c.purpose !== 'documents'
    || typeof c.publicKey !== 'string' || typeof c.notBefore !== 'string' || typeof c.notAfter !== 'string') return refuse();
  const from = Date.parse(c.notBefore), until = Date.parse(c.notAfter);
  if (!Number.isFinite(from) || !Number.isFinite(until) || now < from || now >= until || until - from > MAXIMUM_CERTIFICATE_LIFETIME_MS) return refuse();
  if (!await crypto.verify(root, base64url(c.signature), signedBytes(c))) return refuse();
  if (c.revoked.includes(c.keyId)) return refuse();
  const key = base64url(c.publicKey);
  return key.length === 32 ? key : refuse();
}

/** The envelope's payload bytes, once its certificate and signature hold (`Envelope.Verify`). */
export async function openEnvelope(e: Envelope, crypto: RouteCrypto, now = Date.now(), withdrawn: readonly string[] = [], root: Uint8Array = rootKey()): Promise<Uint8Array> {
  if (!e || typeof e !== 'object') return refuse();
  const key = await certifiedKey(e.certificate, root, now, crypto);
  if (e.keyId !== e.certificate!.keyId || withdrawn.includes(e.keyId)) return refuse();
  const payload = base64url(e.payload);
  if (payload.length > 1 << 20) return refuse();
  if (!await crypto.verify(key, base64url(e.signature), payload)) return refuse();
  return payload;
}

/** A server's id is the digest of its key (`trust.ServerID`). */
export async function serverIdOf(publicKey: Uint8Array, crypto: RouteCrypto): Promise<string> {
  if (publicKey.length !== 32) return refuse();
  return 'srv_' + encode(await crypto.sha256(concat(utf8('portico.server.identity.v1\x00'), publicKey)));
}

/** `trust.Verifier.OpenServerList`: `held` is the version of the list this device already holds. */
export async function openServerList(e: Envelope, accountId: string, held: number, crypto: RouteCrypto, now = Date.now(), root?: Uint8Array): Promise<ServerList> {
  let list: ServerList;
  try { list = JSON.parse(new TextDecoder().decode(await openEnvelope(e, crypto, now, [], root))) as ServerList; } catch (error) { if (error instanceof ServerListRefused) throw error; return refuse(); }
  if (!list || list.kind !== 'portico.server-list' || list.accountId !== accountId || !Number.isSafeInteger(list.version) || list.version < 1 || list.version < held
    || !Array.isArray(list.servers)) return refuse();
  for (const server of list.servers) {
    if (!server || typeof server.serverId !== 'string' || typeof server.name !== 'string' || !Array.isArray(server.addresses)
      || server.addresses.some((address: unknown) => typeof address !== 'string')) return refuse();
    if (await serverIdOf(base64url(server.publicKey), crypto) !== server.serverId) return refuse();
  }
  return list;
}

/** A checked list's servers in the shape the apps' server lists read. The first address is the one
 * to try first; signing in to a listed server is its own step. */
export function listedServers(list: ServerList): HostedServer[] {
  return list.servers.map(server => ({id: server.serverId, name: server.name, baseUrl: server.addresses[0] ?? '', publicKey: server.publicKey,
    policyRevision: 0, ...(['owner', 'admin', 'member'].includes(server.role) ? {role: server.role as 'owner' | 'admin' | 'member', ownedByMe: server.owner} : {})}));
}

/** `GET /v1/servers` with the person's access token, checked. */
export async function readPorticoServerList(origin: string, accessToken: string, accountId: string, crypto: RouteCrypto,
  options: Readonly<{held?: number; fetcher?: typeof fetch; signal?: AbortSignal; now?: number}> = {}): Promise<{items: HostedServer[]; version: number; listToken: string}> {
  const answer = await porticoAccount(heldFor(accessToken), origin, {fetcher: options.fetcher}).serverList(options.signal);
  const list = await openServerList(answer.list as Envelope, accountId, options.held ?? 0, crypto, options.now);
  return {items: listedServers(list), version: list.version, listToken: answer.listToken};
}

/** The same for a page that holds the account's sign-in in a `porticoAccount` (the account pages at
 * web.getportico.tv): the account it names, then its checked list. */
export async function openAccountServerList(account: Pick<ReturnType<typeof porticoAccount>, 'account' | 'serverList'>, crypto: RouteCrypto,
  options: Readonly<{held?: number; signal?: AbortSignal; now?: number}> = {}): Promise<{accountId: string; items: HostedServer[]; version: number; listToken: string}> {
  const [me, answer] = await Promise.all([account.account(options.signal), account.serverList(options.signal)]);
  const list = await openServerList(answer.list as Envelope, me.id, options.held ?? 0, crypto, options.now);
  return {accountId: me.id, items: listedServers(list), version: list.version, listToken: answer.listToken};
}
