import {routeBytes, routeOrigin, serverPin, webRouteCrypto, type RouteCrypto} from './route-identity.ts';
import {validInstallationId} from './installation.ts';
import {currentUTCTimestamp} from './local-session.ts';

export type RegisteredSignInContext = Readonly<{payload: string; signature: string}>;
/** The signing Server bounds every prepare by the Server challenge it answers,
 * so the producer, never this browser, owns the signed issuance lifetime. */
export const REGISTERED_SIGN_IN_LIFETIME_MS = 5 * 60_000;
/** A browser keeps its own clock. Only a signed stamp may run this far ahead of
 * it, and only within the same bounded difference every checkpoint applies. */
export const REGISTERED_SIGN_IN_CLOCK_SKEW_MS = 60_000;
export type RegisteredSignInIssuance = Readonly<{issuedAt: number; expiresAt: number; now: number}>;
/** The only lifetime rule that can grant anything: authenticated facts, an
 * expiry this clock has not reached, one producer lifetime and one bounded skew. */
export function signedRegisteredSignInWithinLifetime({issuedAt, expiresAt, now}: RegisteredSignInIssuance): boolean {
  return [issuedAt, expiresAt, now].every(Number.isSafeInteger) && issuedAt <= now + REGISTERED_SIGN_IN_CLOCK_SKEW_MS &&
    expiresAt > now && expiresAt > issuedAt && expiresAt - issuedAt <= REGISTERED_SIGN_IN_LIFETIME_MS;
}
/** Checkpoints that hold only the producer's expiry plus a browser base time are
 * not grants. They accept exactly the ceiling an authentic issuance can reach —
 * one lifetime after issuance, issued at most one bounded skew ahead of this
 * clock — and still refuse an expiry this browser has reached. The signed facts
 * above remain the authority on the expiry itself. */
export function registeredSignInExpiryWithinLifetime(expiresAt: number, basis: number, now: number): boolean {
  return Number.isSafeInteger(expiresAt) && Number.isSafeInteger(basis) && Number.isSafeInteger(now) &&
    expiresAt > now && expiresAt - basis <= REGISTERED_SIGN_IN_LIFETIME_MS + REGISTERED_SIGN_IN_CLOCK_SKEW_MS;
}
const fields = ['kind', 'version', 'serverId', 'publicKey', 'fingerprint', 'serverName', 'registrationId', 'registrationOrigin', 'hostedOrigin', 'claimOperationId', 'challenge', 'nonce', 'installationId', 'issuedAt', 'expiresAt'] as const;
export type RegisteredSignInContextFacts = Readonly<Record<typeof fields[number], string>>;
function invalid(): never {throw new TypeError('Invalid registered sign-in context.');}
function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value) && [Object.prototype, null].includes(Object.getPrototypeOf(value));
}
function goJSON(value: unknown): string {return JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, c => '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0'));}

/** Every consumer, including Hosted before rendering, verifies the original
 * bytes. A signed display name is never a sign-in grant or a consent gesture.
 * Browser initiation must additionally compare the fresh nonce and pinned key;
 * Hosted must compare every corresponding wrapper/start/registration field. */
export async function verifyRegisteredSignInContext(value: unknown, now = Date.now(), crypto: RouteCrypto = webRouteCrypto): Promise<RegisteredSignInContextFacts> {
  if (!Number.isSafeInteger(now) || !object(value) || Object.keys(value).sort().join(',') !== 'payload,signature') invalid();
  const raw = routeBytes(value.payload, undefined, 6144), signature = routeBytes(value.signature, 64);
  let decoded: unknown;
  try {decoded = JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(raw));} catch {invalid();}
  if (!object(decoded) || Object.keys(decoded).join(',') !== fields.join(',') || fields.some(field => typeof decoded[field] !== 'string')) invalid();
  const facts = decoded as Record<typeof fields[number], string>;
  if (goJSON(facts) !== new TextDecoder().decode(raw) || facts.kind !== 'portico.registered-sign-in-prepare' || facts.version !== '1' ||
    !/^srv_[A-Za-z0-9_-]{43}$/.test(facts.serverId) || !/^ret_[A-Za-z0-9_-]{43}$/.test(facts.registrationId) ||
    !/^[A-Za-z0-9_-]{1,128}$/.test(facts.claimOperationId) || !/^[A-Za-z0-9_-]{32,128}$/.test(facts.challenge) ||
    !validInstallationId(facts.installationId) || !facts.serverName || new TextEncoder().encode(facts.serverName).length > 120 ||
    /[\p{Cc}\p{Cf}]/u.test(facts.serverName) || !facts.serverName.trim()) invalid();
  routeBytes(facts.registrationId.slice(4), 32); routeBytes(facts.nonce, 32);
  for (const origin of [facts.registrationOrigin, facts.hostedOrigin]) if (!origin.startsWith('https://') || routeOrigin(origin) !== origin) invalid();
  const utcNano = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{0,8}[1-9])?Z$/;
  if (!currentUTCTimestamp(facts.issuedAt) || !currentUTCTimestamp(facts.expiresAt) || !utcNano.test(facts.issuedAt) || !utcNano.test(facts.expiresAt)) invalid();
  const issued = Date.parse(facts.issuedAt), expires = Date.parse(facts.expiresAt);
  if (!signedRegisteredSignInWithinLifetime({issuedAt: issued, expiresAt: expires, now})) invalid();
  await serverPin(facts.serverId, facts.publicKey, facts.fingerprint, crypto);
  if (!await crypto.verify(routeBytes(facts.publicKey, 32), signature, raw)) invalid();
  return Object.freeze({...facts});
}
