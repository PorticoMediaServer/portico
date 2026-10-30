import {throwIfAborted} from './abort-lifetime.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import type {HttpLocalApi} from './index.ts';
import type {ClaimExpected} from './claim-onboarding.ts';
import {installationIdentity, validInstallationId} from './installation.ts';
import {verifySetupClaimStart, type SetupStartExpectation} from './setup-claim-start.ts';
import {routeBytes, routeOrigin, sameServerPin, webRouteCrypto, type RouteCrypto} from './route-identity.ts';
import {setupAddressAllowed} from './setup-address.ts';
import {CodedError} from './server-messages.ts';

export type SetupSignedProof = Readonly<{payload: string; signature: string}>;
/** Private protected-journal value, never a public progress snapshot or URL.
 * The caller already holds the verifier and exact pending setup request. */
export type SetupClaimHandoff = Readonly<{
  expected: Readonly<ClaimExpected>; binding: SetupStartExpectation;
  transactionId: string; expiresAt: string; claim: SetupSignedProof; start: SetupSignedProof;
}>;
export type SetupClaimStartPorts = Readonly<{
  /** Before any network: require actual secure context/encrypted atomic storage,
   * and re-read the existing pending attempt, setup request/revision,
   * installation and operation. Repeat after every await; refuse successors. */
  assertPendingCurrent: () => Promise<void>;
  /** Atomically bind ONLY the same pending attempt to this verified response.
   * A failed/stale write must preserve the successor and must not navigate. */
  bindPending: (value: SetupClaimHandoff) => Promise<void>;
}>;
type Api = Pick<HttpLocalApi, 'baseUrl' | 'request' | 'getRouteConnection'>;
const invalid = () => new CodedError('invalid_return', 'This setup continuation no longer matches its captured operation.');
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value) && [Object.prototype, null].includes(Object.getPrototypeOf(value));
const exactKeys = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).sort().join(',') === keys.sort().join(',');
const counter = (value: unknown): value is string => typeof value === 'string' && /^(0|[1-9][0-9]{0,18})$/.test(value) && BigInt(value) <= 9223372036854775807n;
const identifier = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(value);
function signed(value: unknown): SetupSignedProof {
  if (!object(value) || !exactKeys(value, ['payload', 'signature']) || typeof value.payload !== 'string' || typeof value.signature !== 'string') throw invalid();
  // Copy before crypto/storage awaits; callers cannot retarget mutable response objects.
  return Object.freeze({payload: value.payload, signature: value.signature});
}

/** Initiate only an already prepared initial Portico setup. This never
 * issues a Local session, chooses a profile, follows a redirect, or admits a
 * session. The signed Server producer owns all current authority and expiry.
 * Return an approval URL only after the protected pending record commits. */
export async function prepareSetupClaimHandoff(api: Api, expected: ClaimExpected, binding: SetupStartExpectation,
  ports: SetupClaimStartPorts, options: Readonly<{signal?: AbortSignal; now?: () => number; crypto?: RouteCrypto;
    /** The same setup whose claim already INSTALLED, renewing its expired start (a slow
     * certificate). The answer is then the installed operation, bound to its account. */
    renewal?: true}> = {}): Promise<string> {
  expected = Object.freeze({operationId: expected.operationId, localGeneration: expected.localGeneration, revision: expected.revision});
  binding = Object.freeze({pin: Object.freeze({...binding.pin}), origin: binding.origin, hostedOrigin: binding.hostedOrigin,
    operationId: binding.operationId, localGeneration: binding.localGeneration, setupRequestId: binding.setupRequestId, setupRevision: binding.setupRevision, state: binding.state, codeChallenge: binding.codeChallenge, installationId: binding.installationId});
  const now = options.now ?? Date.now;
  if (!identifier(expected.operationId) || !counter(expected.localGeneration) || !counter(expected.revision) || expected.revision === '0' ||
      expected.operationId !== binding.operationId || expected.localGeneration !== binding.localGeneration || !validInstallationId(binding.installationId) || !/^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(binding.setupRequestId) || !counter(binding.setupRevision) || binding.setupRevision === '0' ||
      routeOrigin(binding.origin) !== binding.origin || api.baseUrl !== binding.origin || routeOrigin(binding.hostedOrigin) !== binding.hostedOrigin || !binding.hostedOrigin.startsWith('https://')) throw invalid();
  if (!setupAddressAllowed(binding.origin)) throw invalid();
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
    setupStart: {state: binding.state, codeChallenge: binding.codeChallenge, installationId: binding.installationId}}, options.signal);
  // Capture the response before any asynchronous current-setup check.
  const shape = options.renewal
    ? object(raw) && raw.state === 'installed' && raw.installationAcknowledged === true && typeof raw.accountId === 'string' && identifier(raw.accountId) && raw.accountId !== 'account-unbound'
    : object(raw) && raw.state === 'prepared' && raw.accountId === 'account-unbound' && raw.installationAcknowledged === false && raw.approvalRequired === true
      && Array.isArray(raw.actions) && raw.actions.every(action => action === 'cancel' || action === 'approve');
  if (!shape || !object(raw) || !object(raw.identity) || raw.identity.serverId !== binding.pin.serverId || raw.identity.localGeneration !== binding.localGeneration ||
      !object(raw.operation) || !exactKeys(raw.operation, ['operationId', 'localGeneration', 'revision']) || raw.operation.operationId !== expected.operationId ||
      raw.operation.localGeneration !== expected.localGeneration || raw.operation.revision !== expected.revision ||
      typeof raw.approvalUrl !== 'string' || typeof raw.claimCode !== 'string' || !identifier(raw.claimCode) || !object(raw.setupContinuation)) throw invalid();
  const continuation = raw.setupContinuation;
  if (!exactKeys(continuation, ['transactionId', 'phase', 'expiresAt', 'claim', 'start']) || continuation.phase !== 'prepared' || !identifier(continuation.transactionId) || typeof continuation.expiresAt !== 'string') throw invalid();
  const handoff: SetupClaimHandoff = Object.freeze({expected, binding, transactionId: continuation.transactionId, expiresAt: continuation.expiresAt,
    claim: signed(continuation.claim), start: signed(continuation.start)});
  const approvalURL = raw.approvalUrl, code = raw.claimCode;
  await current();
  const verified = await verifySetupClaimStart(handoff.claim, handoff.start, binding, now(), options.crypto ?? webRouteCrypto);
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
