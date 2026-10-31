import type {ClaimExpected} from './claim-onboarding.ts';
import {verifySetupClaimStart, type SetupStartExpectation} from './setup-claim-start.ts';
import type {RouteCrypto} from './route-identity.ts';
import {CodedError} from './server-messages.ts';

export type SetupSignedProof = Readonly<{payload: string; signature: string}>;
/** Private protected-journal value, never a public progress snapshot or URL.
 * The caller already holds the verifier and exact pending setup request. */
export type SetupClaimHandoff = Readonly<{
  expected: Readonly<ClaimExpected>; binding: SetupStartExpectation;
  transactionId: string; expiresAt: string; claim: SetupSignedProof; start: SetupSignedProof;
}>;
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

/** The claim status answer to a setup approval request, copied and checked before any await: the operation it names,
 * the approval link and code, and the signed pair. `renewal`: the setup's installed claim renewing its start. */
export function captureSetupHandoff(raw: unknown, expected: ClaimExpected, binding: SetupStartExpectation, renewal: boolean) {
  const shape = renewal
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
  return {handoff, approvalURL: raw.approvalUrl, code: raw.claimCode};
}

/** The signed pair verifies against its binding, and the approval link is exactly Portico Services' claim page for it. */
export async function checkSetupHandoff(handoff: SetupClaimHandoff, approvalURL: string, code: string, now: number, crypto: RouteCrypto): Promise<void> {
  const verified = await verifySetupClaimStart(handoff.claim, handoff.start, handoff.binding, now, crypto);
  const url = new URL(approvalURL), fragment = new URLSearchParams(url.hash.slice(1));
  if (handoff.expiresAt !== verified.start.expiresAt || !exactKeys(Object.fromEntries(fragment), ['code', 'operation', 'server']) || [...fragment.keys()].length !== 3 ||
      url.origin !== handoff.binding.hostedOrigin || url.pathname !== '/claim' || url.search || url.username || url.password ||
      fragment.get('code') !== code || fragment.get('operation') !== handoff.expected.operationId || fragment.get('server') !== verified.claim.name) throw invalid();
}
