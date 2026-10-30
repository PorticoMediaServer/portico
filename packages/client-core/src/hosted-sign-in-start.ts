import {validInstallationId} from './installation.ts';
import {currentUTCTimestamp} from './local-session.ts';
import {routeBase64, routeBytes, routeOrigin} from './route-identity.ts';
import type {RegisteredSignIn, RegisteredSignInWithContext} from './same-tab-return.ts';
import {verifyRegisteredSignInContext, registeredSignInExpiryWithinLifetime} from './registered-sign-in-context.ts';
import type {RouteCrypto} from './route-identity.ts';

const prefix = '#signIn=v1.';
const maximumURL = 8192;
const topKeys = ['start', 'registration', 'hostedOrigin', 'expiresAt', 'context'];
const startKeys = ['serverId', 'purpose', 'claimOperationId', 'registrationId', 'state', 'codeChallenge', 'serverChallenge', 'installationId'];
const registrationKeys = ['id', 'origin'];
function invalid(): never { throw new TypeError('Invalid registered sign-in start.'); }
function object(value: unknown, keys: readonly string[]): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value) || ![Object.prototype, null].includes(Object.getPrototypeOf(value))) invalid();
  const descriptors = Object.getOwnPropertyDescriptors(value);
  if (Reflect.ownKeys(value).length !== keys.length || keys.some(key => !Object.hasOwn(descriptors, key) || !descriptors[key].enumerable || !Object.hasOwn(descriptors[key], 'value'))) invalid();
  return value as Record<string, unknown>;
}
function httpsOrigin(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('https://') || routeOrigin(value) !== value) invalid();
  return value;
}
function bytes32(value: unknown): string { routeBytes(value, 32); return value as string; }
function registrationID(value: unknown): string {
  if (typeof value !== 'string' || !value.startsWith('ret_')) invalid();
  bytes32(value.slice(4)); return value;
}
function checked(value: unknown, now: number): RegisteredSignInWithContext {
  if (!Number.isSafeInteger(now)) invalid();
  const root = object(value, topKeys), start = object(root.start, startKeys), registration = object(root.registration, registrationKeys);
  if (typeof start.serverId !== 'string' || !start.serverId.startsWith('srv_')) invalid();
  bytes32(start.serverId.slice(4));
  if (start.purpose !== 'sign-in' || typeof start.claimOperationId !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(start.claimOperationId) ||
      typeof start.serverChallenge !== 'string' || !/^[A-Za-z0-9_-]{32,128}$/.test(start.serverChallenge) || !validInstallationId(start.installationId)) invalid();
  const registrationId = registrationID(start.registrationId);
  if (registration.id !== registrationId) invalid();
  if (!currentUTCTimestamp(root.expiresAt) || !root.expiresAt.endsWith('Z') || root.expiresAt[10] !== 'T') invalid();
  const expires = Date.parse(root.expiresAt);
  // This envelope carries only the producer's signed expiry. Its ceiling is the
  // one an authentic issuance can reach: verifyHostedSignInStart proves the
  // signed facts hold this same expiry at most one lifetime after issuance and
  // issued no more than one bounded skew ahead of this browser's clock.
  if (!registeredSignInExpiryWithinLifetime(expires, now, now)) invalid();
  const context = object(root.context, ['payload', 'signature']);
  routeBytes(context.payload, undefined, 6144); routeBytes(context.signature, 64);
  // Construct one canonical order without copying protected or unrelated fields.
  return Object.freeze({start: Object.freeze({serverId: start.serverId, purpose: 'sign-in', claimOperationId: start.claimOperationId,
    registrationId, state: bytes32(start.state), codeChallenge: bytes32(start.codeChallenge), serverChallenge: start.serverChallenge,
    installationId: start.installationId}), registration: Object.freeze({id: registrationId, origin: httpsOrigin(registration.origin)}),
    hostedOrigin: httpsOrigin(root.hostedOrigin), expiresAt: root.expiresAt,
    context: Object.freeze({payload: context.payload as string, signature: context.signature as string})});
}

/** Only the installed server's checked prepare result may enter this ordinary handoff.
 * Verifier, move proofs, owner state and local continuation intent never enter its URL. */
export function buildHostedSignInStartURL(value: RegisteredSignIn, now = Date.now()): string {
  const start = checked(value, now);
  const href = start.hostedOrigin + '/' + prefix + routeBase64(new TextEncoder().encode(JSON.stringify(start)));
  if (href.length > maximumURL) invalid();
  return href;
}

/** Pure strict parser. The caller must scrub history synchronously before calling it. */
export function parseHostedSignInStartURL(href: string, expectedHostedOrigin: string, now = Date.now()): RegisteredSignInWithContext | undefined {
  try {
    if (typeof href !== 'string' || href.length > maximumURL) return;
    const origin = httpsOrigin(expectedHostedOrigin), url = new URL(href);
    if (href !== url.href || url.origin !== origin || url.pathname !== '/' || url.search || url.username || url.password || !url.hash.startsWith(prefix)) return;
    const json = new TextDecoder('utf-8', {fatal: true}).decode(routeBytes(url.hash.slice(prefix.length), undefined, 6144));
    const value = checked(JSON.parse(json), now);
    if (value.hostedOrigin !== origin || JSON.stringify(value) !== json || buildHostedSignInStartURL(value, now) !== href) return;
    return value;
  } catch { return; }
}

/** Hosted checks the original signed prepare context before displaying either
 * credential or cached-account state. Verification never issues an answer. */
export async function verifyHostedSignInStart(value: RegisteredSignInWithContext, now = Date.now(), crypto?: RouteCrypto) {
  const handoff = checked(value, now), facts = await verifyRegisteredSignInContext(handoff.context, now, crypto);
  if (facts.serverId !== handoff.start.serverId || facts.registrationId !== handoff.registration.id ||
      facts.registrationOrigin !== handoff.registration.origin || facts.hostedOrigin !== handoff.hostedOrigin ||
      facts.claimOperationId !== handoff.start.claimOperationId || facts.challenge !== handoff.start.serverChallenge ||
      facts.installationId !== handoff.start.installationId || facts.expiresAt !== handoff.expiresAt) invalid();
  return Object.freeze({handoff, serverName: facts.serverName});
}
