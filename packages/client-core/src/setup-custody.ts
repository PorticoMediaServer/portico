import type {DirectSignIn} from './profile-management.ts';
import {routeBase64, routeBytes, type RouteCrypto} from './route-identity.ts';
import type {SetupClaimHandoff} from './setup-claim-client.ts';
import type {verifySetupClaimStart} from './setup-claim-start.ts';
import type {SetupAttempt, SetupRecord} from './setup-store.ts';

/** The checks that let a v2 setup attempt finish (B-DESIGN §6.3): its bound handoff, its own PKCE verifier, and the
 * owner grant the exchange answers. Pure; web and native call them around their own storage and pairing. */

type Bound = SetupAttempt & {handoff: SetupClaimHandoff};
type SignedIn = Extract<DirectSignIn, {kind: 'signed-in'}>;
export type SetupProof = Awaited<ReturnType<typeof verifySetupClaimStart>>;

/** The handoff was bound to exactly this attempt, for this setup record, and the attempt's window ends no later than it. */
export function setupAttemptBound(attempt: SetupAttempt, record: SetupRecord): attempt is Bound {
  const handoff = attempt.handoff, binding = handoff?.binding;
  return !!handoff && !!binding && attempt.serverId === record.serverId && attempt.requestId === record.requestId && binding.pin.serverId === attempt.serverId
    && binding.origin === attempt.origin && binding.hostedOrigin === attempt.hostedOrigin && binding.state === attempt.state
    && binding.installationId === attempt.installationId && binding.codeChallenge === attempt.codeChallenge && binding.setupRequestId === attempt.requestId
    && binding.operationId === handoff.expected.operationId && binding.localGeneration === handoff.expected.localGeneration
    && Date.parse(attempt.expiresAt) <= Date.parse(handoff.expiresAt);
}

/** The verified signed start answers this attempt's own verifier and window. A malformed verifier throws. */
export async function setupAttemptProofMatches(attempt: Bound, proof: SetupProof, crypto: Pick<RouteCrypto, 'sha256'>): Promise<boolean> {
  routeBytes(attempt.verifier, 32);
  const digest = routeBase64(await crypto.sha256(new TextEncoder().encode(attempt.verifier)));
  return digest === proof.start.codeChallenge && attempt.handoff.expiresAt === proof.start.expiresAt;
}

/** The exchange answered an enabled Portico Account owner of this server on their main profile, with nothing left to do
 * first, in an unexpired owner session for this installation and the pinned server identity. */
export function setupOwnerGrantFor(grant: DirectSignIn, attempt: Bound, now: number): grant is SignedIn {
  if (grant.kind !== 'signed-in') return false;
  const {snapshot, session} = grant, pin = attempt.handoff.binding.pin;
  return snapshot.serverId === attempt.serverId && snapshot.account.role === 'owner' && !snapshot.account.disabled && !!snapshot.account.hostedAccountId
    && session.viewer.profileId === snapshot.account.primaryProfileId && !grant.resetFileOwnerSession && !grant.passwordChangeRequired && !snapshot.twoStepSetupRequired
    && session.viewer.role === 'owner' && session.installationId === attempt.installationId && session.viewer.serverId === attempt.serverId
    && session.serverIdentity?.publicKey === pin.publicKey && session.serverIdentity.fingerprint === pin.fingerprint && Date.parse(session.expiresAt) > now;
}
