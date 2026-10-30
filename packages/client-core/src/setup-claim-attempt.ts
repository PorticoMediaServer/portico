import type {ClaimExpected, ClaimStatus} from './claim-onboarding.ts';
import type {SetupClaimHandoff} from './setup-claim-client.ts';
import type {verifySetupClaimStart} from './setup-claim-start.ts';
import type {SetupDraft} from './setup-onboarding.ts';
import type {DirectSignIn} from './profile-management.ts';
import {routeBase64, routeBytes, sameServerPin, type RouteCrypto, type ServerPin} from './route-identity.ts';

/** The pure predicates of one initial Portico setup claim, shared by web and native.
 * Each platform keeps its own custody around them: protected journal reads and
 * writes, consumption before I/O, selection leases, adoption and rollback. These
 * checks never consult storage and never grant anything by themselves. */

/** One protected setup attempt. `retired` is native's tombstone for an explicitly
 * abandoned attempt whose draft is already gone. */
export type SetupAttempt = Readonly<{
  version: 1; kind: 'setup'; attemptId: string; origin: string; hostedOrigin: string; pin: ServerPin;
  expectedRevision: number; setupRequestId: string; setupToken: string; setupRevision: string;
  installationId: string; state: string; verifier: string; codeChallenge: string; createdAt: number; expiresAt: string;
  expected?: ClaimExpected; handoff?: SetupClaimHandoff; retired?: true;
}>;
export type SetupProof = Awaited<ReturnType<typeof verifySetupClaimStart>>;
type SignedIn = Extract<DirectSignIn, {kind: 'signed-in'}>;

const LIFETIME_MS = 300_000, CLOCK_SKEW_MS = 60_000;
const object = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const keys = (value: Record<string, unknown>) => Object.keys(value).sort().join(',');

/** A fresh attempt: 32-byte verifier with its SHA-256 challenge, and a five-minute window. */
export async function createSetupAttempt(input: Readonly<{origin: string; hostedOrigin: string; pin: ServerPin; selectionRevision: number;
  draft: Readonly<{requestId: string; setupToken: string}>; setupRevision: string; installationId: string; now: number;
  crypto: Pick<RouteCrypto, 'random' | 'sha256'>}>): Promise<SetupAttempt> {
  const verifier = routeBase64(input.crypto.random(32));
  const codeChallenge = routeBase64(await input.crypto.sha256(new TextEncoder().encode(verifier)));
  return {version: 1, kind: 'setup', attemptId: routeBase64(input.crypto.random(32)), origin: input.origin, hostedOrigin: input.hostedOrigin, pin: {...input.pin},
    expectedRevision: input.selectionRevision, setupRequestId: input.draft.requestId, setupToken: input.draft.setupToken, setupRevision: input.setupRevision,
    installationId: input.installationId, state: routeBase64(input.crypto.random(32)), verifier, codeChallenge,
    createdAt: input.now, expiresAt: new Date(input.now + LIFETIME_MS).toISOString()};
}

/** The attempt still belongs to this page/app, installation, selection revision and
 * protected setup draft, and its window is open: created no later than a minute
 * ahead, expiring in the future and at most five minutes after creation. Without a
 * draft only a retired tombstone is still live. */
export function setupAttemptLive(record: SetupAttempt, facts: Readonly<{draft: SetupDraft | undefined; selectionRevision: number; origin: string; installationId: string; now: number}>): boolean {
  const {draft, now} = facts, expires = Date.parse(record.expiresAt);
  return record.version === 1 && record.kind === 'setup' && record.origin === facts.origin && record.installationId === facts.installationId
    && facts.selectionRevision === record.expectedRevision
    && (!draft ? record.retired === true : draft.version === 1 && draft.authMode === 'hosted' && draft.serverId === record.pin.serverId
      && draft.requestId === record.setupRequestId && draft.setupToken === record.setupToken)
    && Number.isSafeInteger(record.createdAt) && record.createdAt >= 0 && record.createdAt <= now + CLOCK_SKEW_MS
    && Number.isFinite(expires) && expires > now && expires <= record.createdAt + LIFETIME_MS;
}

/** The setup operation a prepare answer names, or undefined when it is not a fresh
 * setup approval with no current owner, previous role or return registration. */
export function preparedSetupOperation(status: ClaimStatus): ClaimExpected | undefined {
  const approval = status.approvalRequest;
  if (status.state !== 'prepared' || !status.operation || approval?.purpose !== 'setup' || approval.currentOwnerAccountId !== ''
    || approval.previousOwnerRole !== '' || approval.returnRegistrationId !== '') return undefined;
  return status.operation;
}

/** The stored handoff is bound to exactly this attempt and its captured operation. */
export function setupHandoffBound<T extends SetupAttempt>(record: T): record is T & {handoff: SetupClaimHandoff; expected: ClaimExpected} {
  const handoff = record.handoff, expected = record.expected, binding = handoff?.binding;
  return !!handoff && !!expected && !!binding && binding.origin === record.origin && binding.hostedOrigin === record.hostedOrigin && sameServerPin(binding.pin, record.pin)
    && binding.state === record.state && binding.installationId === record.installationId && binding.codeChallenge === record.codeChallenge
    && binding.operationId === expected.operationId && binding.setupRequestId === record.setupRequestId && binding.setupRevision === record.setupRevision
    && binding.localGeneration === expected.localGeneration && JSON.stringify(handoff.expected) === JSON.stringify(expected)
    && Date.parse(record.expiresAt) <= Date.parse(handoff.expiresAt);
}

/** The verified signed start answers this attempt's own verifier and window. A
 * malformed verifier throws. */
export async function setupProofMatches(record: SetupAttempt & {handoff: SetupClaimHandoff}, proof: SetupProof, crypto: Pick<RouteCrypto, 'sha256'>): Promise<boolean> {
  const digest = routeBase64(await crypto.sha256(new TextEncoder().encode(record.verifier)));
  routeBytes(record.verifier, 32);
  return digest === proof.start.codeChallenge && record.handoff.expiresAt === proof.start.expiresAt;
}

/** What the original server says about this attempt's continuation. Only an exact
 * installed shape (no registration) completes admission; prepared and cancelled are explicit
 * non-grant outcomes; anything else that claims this operation is invalid. */
export type SetupContinuation = Readonly<{phase: 'installed'}> | Readonly<{phase: 'pending'}> | Readonly<{phase: 'cancelled'}> | Readonly<{phase: 'invalid'}>;
export function setupContinuation(raw: unknown, status: ClaimStatus, record: SetupAttempt & {handoff: SetupClaimHandoff; expected: ClaimExpected}): SetupContinuation {
  const expected = record.expected, handoff = record.handoff, setup = object(raw) ? raw.setupContinuation : undefined;
  const sameOperation = status.identity.localGeneration === expected.localGeneration && status.operation?.operationId === expected.operationId
    && status.operation.localGeneration === expected.localGeneration && BigInt(status.operation.revision) >= BigInt(expected.revision);
  const sameWindow = object(setup) && setup.transactionId === handoff.transactionId && typeof setup.expiresAt === 'string'
    && Date.parse(setup.expiresAt) === Date.parse(handoff.expiresAt);
  if (status.state === 'cancelled' && sameOperation && sameWindow && setup.phase === 'cancelled' && keys(setup) === 'expiresAt,phase,transactionId') return {phase: 'cancelled'};
  if (status.state !== 'installed') return {phase: 'pending'};
  if (!sameOperation || !sameWindow) return {phase: 'invalid'};
  // The installed claim can still be provisioning its real TLS/route/return
  // registration. That exact pending shape grants nothing and retains PKCE.
  if (setup.phase === 'prepared' && keys(setup) === 'expiresAt,phase,transactionId') return {phase: 'pending'};
  if (setup.phase !== 'installed' || keys(setup) !== 'expiresAt,phase,transactionId') return {phase: 'invalid'};
  return {phase: 'installed'};
}

/** The exchange named this server's enabled owner account and its primary profile,
 * with no reset-file, password-change or two-step setup still required. */
export function setupOwnerGrantValid(grant: DirectSignIn, record: SetupAttempt): grant is SignedIn {
  return grant.kind === 'signed-in' && grant.snapshot.serverId === record.pin.serverId && grant.snapshot.account.role === 'owner' && !grant.snapshot.account.disabled
    && !!grant.snapshot.account.hostedAccountId && grant.session.viewer.profileId === grant.snapshot.account.primaryProfileId
    && !grant.resetFileOwnerSession && !grant.passwordChangeRequired && !grant.snapshot.twoStepSetupRequired;
}

/** The granted session itself is an unexpired owner session for this installation
 * and the original pinned server identity. */
export function setupGrantSessionBound(grant: SignedIn, record: SetupAttempt, now: number): boolean {
  const session = grant.session;
  return session.viewer.role === 'owner' && session.installationId === record.installationId && session.viewer.serverId === record.pin.serverId
    && session.serverIdentity?.publicKey === record.pin.publicKey && session.serverIdentity.fingerprint === record.pin.fingerprint
    && Date.parse(session.expiresAt) > now;
}

/** The approval code in a Portico Account claim URL bound to this attempt's hosted
 * origin, operation and server name, or undefined. A malformed code throws. */
export function setupApprovalCode(url: string, expected: Readonly<{hostedOrigin: string; operationId: string; serverName: string}>): string | undefined {
  const parsed = new URL(url), fragment = new URLSearchParams(parsed.hash.slice(1)), code = fragment.get('code') ?? '';
  if (parsed.origin !== expected.hostedOrigin || parsed.pathname !== '/claim' || parsed.search || parsed.username || parsed.password
    || [...fragment.keys()].sort().join(',') !== 'code,operation,server' || fragment.get('operation') !== expected.operationId
    || fragment.get('server') !== expected.serverName || !code.startsWith('claim_')) return undefined;
  routeBytes(code.slice(6), 32);
  return code;
}
