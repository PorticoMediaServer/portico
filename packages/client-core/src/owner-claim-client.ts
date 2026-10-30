import {throwIfAborted} from './abort-lifetime.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {HttpLocalApi} from './index.ts';
import type {ClaimExpected} from './claim-onboarding.ts';
import {installationIdentity, validInstallationId} from './installation.ts';
import {verifyOwnerClaimStart, type OwnerSignedProof, type OwnerStartExpectation} from './owner-claim-start.ts';
import {routeBytes, routeOrigin, sameServerPin, webRouteCrypto, type RouteCrypto} from './route-identity.ts';
import {CodedError} from './server-messages.ts';

/** Private protected-journal value, never a public progress snapshot or URL.
 * The caller already holds the verifier and exact selected owner family. */
export type OwnerClaimHandoff = Readonly<{
  expected: Readonly<ClaimExpected>; binding: OwnerStartExpectation;
  transactionId: string; expiresAt: string; claim: OwnerSignedProof; start: OwnerSignedProof;
}>;
export type OwnerClaimStartPorts = Readonly<{
  /** Before any network: require actual secure context/encrypted atomic storage,
   * and re-read the existing pending attempt, selection revision, owner family,
   * installation and operation. Repeat after every await; refuse successors. */
  assertPendingCurrent: () => Promise<void>;
  /** Atomically bind ONLY the same pending attempt to this verified response.
   * A failed/stale write must preserve the successor and must not navigate. */
  bindPending: (value: OwnerClaimHandoff) => Promise<void>;
}>;
type Api = Pick<HttpLocalApi, 'baseUrl' | 'request' | 'getRouteConnection'>;
const invalid = () => new CodedError('invalid_return', 'This owner continuation no longer matches its captured operation.');
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value) && [Object.prototype, null].includes(Object.getPrototypeOf(value));
const exactKeys = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).sort().join(',') === keys.sort().join(',');
const counter = (value: unknown): value is string => typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
function signed(value: unknown): OwnerSignedProof {
  if (!object(value) || !exactKeys(value, ['payload', 'signature']) || typeof value.payload !== 'string' || typeof value.signature !== 'string') throw invalid();
  // Copy before crypto/storage awaits; callers cannot retarget mutable response objects.
  return Object.freeze({payload: value.payload, signature: value.signature});
}

/** Initiate only an already prepared first Local -> Portico switch. This never
 * prepares a setup claim, chooses a profile, follows a redirect, or admits a
 * session. The signed Server producer owns all current authority and expiry.
 * Return an approval URL only after the protected pending record commits. */
export async function prepareOwnerClaimHandoff(api: Api, expected: ClaimExpected, binding: OwnerStartExpectation,
  ports: OwnerClaimStartPorts, options: Readonly<{signal?: AbortSignal; now?: () => number; crypto?: RouteCrypto}> = {}): Promise<string> {
  expected = Object.freeze({operationId: expected.operationId, localGeneration: expected.localGeneration, revision: expected.revision});
  // Binding facts are persisted runtime data. Reject an unknown purpose and the
  // mixed omitted/switch + owner-facts shapes before the freeze; otherwise the
  // omitted case would silently drop facts and normalize to a switch.
  const purpose = binding.purpose;
  if (purpose !== undefined && purpose !== 'switch' && purpose !== 'change-owner') invalid();
  if (purpose === undefined || purpose === 'switch') {
    if (binding.currentOwnerAccountId !== undefined || binding.previousOwnerRole !== undefined) invalid();
  } else if (!binding.currentOwnerAccountId || !binding.previousOwnerRole) invalid();
  binding = Object.freeze({pin: Object.freeze({...binding.pin}), origin: binding.origin, hostedOrigin: binding.hostedOrigin,
    operationId: binding.operationId, localGeneration: binding.localGeneration, state: binding.state, codeChallenge: binding.codeChallenge, installationId: binding.installationId,
    ...(purpose === undefined ? {} : {purpose, currentOwnerAccountId: binding.currentOwnerAccountId, previousOwnerRole: binding.previousOwnerRole})});
  const now = options.now ?? Date.now;
  if (!identifier(expected.operationId) || !counter(expected.localGeneration) || !counter(expected.revision) || expected.revision === '0' ||
      expected.operationId !== binding.operationId || expected.localGeneration !== binding.localGeneration || !validInstallationId(binding.installationId) ||
      routeOrigin(binding.origin) !== binding.origin || api.baseUrl !== binding.origin || routeOrigin(binding.hostedOrigin) !== binding.hostedOrigin || !binding.hostedOrigin.startsWith('https://')) throw invalid();
  const origin = new URL(binding.origin);
  if (origin.protocol !== 'https:' && !(origin.protocol === 'http:' && origin.hostname === 'localhost')) throw invalid();
  routeBytes(binding.state, 32); routeBytes(binding.codeChallenge, 32);
  const current = async () => {
    throwIfAborted(options.signal);
    await ports.assertPendingCurrent();
    throwIfAborted(options.signal);
    const route = api.getRouteConnection();
    if (!route || route.getSnapshot().phase !== 'ready' || !sameServerPin(route.pin, binding.pin) || installationIdentity()?.installationId !== binding.installationId) throw invalid();
  };
  await current();
  const raw: unknown = await api.request(routePath('post_networking_claim_web_approval'), 'POST', {expected,
    ownerStart: {state: binding.state, codeChallenge: binding.codeChallenge, installationId: binding.installationId}}, options.signal);
  // Capture the response before any asynchronous current-selection check.
  if (!object(raw) || raw.state !== 'prepared' || !object(raw.identity) || raw.identity.serverId !== binding.pin.serverId || raw.identity.localGeneration !== binding.localGeneration ||
      !object(raw.operation) || !exactKeys(raw.operation, ['operationId', 'localGeneration', 'revision']) || raw.operation.operationId !== expected.operationId ||
      raw.operation.localGeneration !== expected.localGeneration || raw.operation.revision !== expected.revision || raw.accountId !== 'account-unbound' ||
      raw.installationAcknowledged !== false || raw.approvalRequired !== true || !Array.isArray(raw.actions) || raw.actions.some(action => action !== 'cancel' && action !== 'approve') ||
      typeof raw.approvalUrl !== 'string' || typeof raw.claimCode !== 'string' || !identifier(raw.claimCode) || !object(raw.ownerContinuation)) throw invalid();
  const continuation = raw.ownerContinuation;
  if (!exactKeys(continuation, ['transactionId', 'phase', 'expiresAt', 'claim', 'start']) || continuation.phase !== 'prepared' || !identifier(continuation.transactionId) || typeof continuation.expiresAt !== 'string') throw invalid();
  const handoff: OwnerClaimHandoff = Object.freeze({expected, binding, transactionId: continuation.transactionId, expiresAt: continuation.expiresAt,
    claim: signed(continuation.claim), start: signed(continuation.start)});
  const approvalURL = raw.approvalUrl, code = raw.claimCode;
  await current();
  const verified = await verifyOwnerClaimStart(handoff.claim, handoff.start, binding, now(), options.crypto ?? webRouteCrypto);
  await current();
  const url = new URL(approvalURL), fragment = new URLSearchParams(url.hash.slice(1));
  if (handoff.expiresAt !== verified.start.expiresAt || !exactKeys(Object.fromEntries(fragment), ['code', 'operation', 'server']) || [...fragment.keys()].length !== 3 ||
      url.origin !== binding.hostedOrigin || url.pathname !== '/claim' || url.search || url.username || url.password ||
      fragment.get('code') !== code || fragment.get('operation') !== expected.operationId || fragment.get('server') !== verified.claim.name) throw invalid();
  const fresh = () => {const time = now(); if (!Number.isSafeInteger(time) || time >= Date.parse(handoff.expiresAt)) throw invalid();};
  fresh();
  await ports.bindPending(handoff);
  await current();
  fresh();
  return approvalURL;
}
