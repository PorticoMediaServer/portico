import type {HostedSession} from '../../../../../packages/client-core/src';
import {captureTVApprovalGrant} from '../../platform/tv-approval-grant';
import {createTVApprovalCredentialOpening, type TVApprovalCredentials, type TVApprovalCredentialOpening} from './tv-approval-credentials';
import type {CredentialSnapshot} from '../../../../../packages/client-core/src/credentials';
import type {SetupCodeService} from '../../../../../packages/client-core/src/setup-code';
import {presentError} from '../../../../../packages/client-core/src/presentation/errors';

export type TVApprovalSnapshot = Readonly<{
  phase: 'ready' | 'busy' | 'failed' | 'terminal' | 'complete' | 'retired';
  adopted: boolean; error?: unknown; retryAt?: number;
}>;
const changed = () => Object.assign(new Error('native_tv_approval_changed'), {code: 'account_changed'});
const sameFamily = (session: CredentialSnapshot['session'], grant: HostedSession) =>
  session?.account.id === grant.account.id && session.familyId === grant.familyId;
const terminalCodes = new Set(['invalid_grant', 'invalid_token', 'expired_token', 'session_expired', 'unauthorized',
  'invalid_consent', 'identity_mismatch', 'account_changed', 'account_session_changed', 'consent_retired']);

/** One approved grant, its canonical credential admission and its durable journal
 * completion. A retry resumes only unfinished work and never manufactures consent. */
export function createTVApprovalFinalization(options: Readonly<{
  service: SetupCodeService<HostedSession>; grant: HostedSession;
  credentials: TVApprovalCredentials; credentialOpening?: TVApprovalCredentialOpening;
  adopt: (grant: HostedSession, current: () => boolean) => Promise<unknown>;
  current: () => boolean; now?: () => number;
}>) {
  const {service, grant, credentials} = options, now = options.now ?? Date.now;
  const captured = captureTVApprovalGrant(grant);
  const credentialOpening = options.credentialOpening ?? createTVApprovalCredentialOpening(credentials, {current: options.current});
  let state: TVApprovalSnapshot = {phase: 'ready', adopted: false};
  let busy = false, retired = false, attempted = false, uncertain = false, adopted = false, unresolvedTerminal: unknown;
  const current = () => {if (retired || !credentialOpening.current() || !captured.current()) return false; const admitted = options.current(); return !retired && credentialOpening.current() && captured.current() && admitted;};
  const publish = (update: TVApprovalSnapshot) => {state = Object.freeze(update);};
  const retire = () => {retired = true; credentialOpening.retire(); publish({phase: 'retired', adopted});};
  const requireCurrent = () => {if (!current()) {retire(); return false;} return true;};
  const reconcile = async () => {
    if (!requireCurrent()) throw changed();
    await credentials.synchronize();
    if (!requireCurrent()) throw changed();
    const session = credentials.getSnapshot().session;
    if (sameFamily(session, captured.session) && attempted) {adopted = true; uncertain = false;}
    else if (session || adopted) {retire(); throw changed();}
    else uncertain = false;
  };
  return Object.freeze({
    getSnapshot: () => state,
    // A pending/uncertain canonical commit may have adopted the family already.
    // Abandoning the UI may pause that journal, but must never revoke that family.
    canCancel: () => !adopted && !uncertain,
    retire,
    async run(): Promise<TVApprovalSnapshot['phase']> {
      if (!current()) {retire(); return state.phase;}
      if (busy || state.phase === 'complete' || state.phase === 'terminal' || (state.retryAt ?? 0) > now()) return state.phase;
      busy = true; publish({phase: 'busy', adopted});
      try {
        if (!service.isCurrent(grant) || !requireCurrent()) {retire(); return state.phase;}
        await reconcile(); if (!requireCurrent()) return state.phase;
        if (!adopted) {
          if (!uncertain && unresolvedTerminal) throw unresolvedTerminal;
          if (!credentialOpening.allowAttempt(captured.session) || !requireCurrent()) {retire(); return state.phase;}
          // The first admission requires a signed-out original opening. Only a
          // commit attempted by THIS operation may reconcile the exact issued family.
          attempted = true; uncertain = true;
          await options.adopt(captured.session, () => current() && service.isCurrent(grant) && current());
          await reconcile(); if (!requireCurrent()) return state.phase;
          if (!adopted) throw changed();
        }
        if (!service.isCurrent(grant) || !requireCurrent()) {retire(); return state.phase;}
        // Once admitted, only the original journal remains. Never authenticate
        // again if this write fails, and never interpret false as navigation authority.
        const completed = await service.complete(grant);
        if (!requireCurrent() || !completed) {retire(); return state.phase;}
        publish({phase: 'complete', adopted});
      } catch (error) {
        // A storage operation can fail after committing. Read back through the
        // canonical coordinator before deciding which stage may safely retry.
        if (uncertain && current()) {
          try {await reconcile();} catch {/* Keep uncertainty; no cancellation authority. */}
        }
        if (!requireCurrent()) return state.phase;
        const presentation = presentError(error, 'portico-account');
        const terminalCause = terminalCodes.has(presentation.code ?? '') || presentation.category === 'cancelled';
        if (uncertain && terminalCause) unresolvedTerminal = error;
        const terminal = !adopted && !uncertain && terminalCause;
        publish({phase: terminal ? 'terminal' : 'failed', adopted, error,
          retryAt: presentation.retryAfterSeconds ? now() + presentation.retryAfterSeconds * 1000 : undefined});
      } finally {busy = false;}
      return state.phase;
    },
  });
}

export type TVApprovalFinalization = ReturnType<typeof createTVApprovalFinalization>;
