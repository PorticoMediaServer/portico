import {ApiError} from '@core/index.ts';
import {routeBody, routePath} from '../../../packages/contracts/src/hosted/routes.ts';
import {verificationLink} from './fragments';
import {accountIdentity, type ServicesAccount} from './session';

export async function resetPassword(account: Pick<ServicesAccount, 'client'>, token: string,
  password: string, factor: string, signal: AbortSignal): Promise<void> {
  // This endpoint deliberately does not adopt a session after changing the password.
  await account.client.request(routePath('account_recovery_complete'), 'POST',
    routeBody('account_recovery_complete', {token, newPassword: password, factor: factor.trim()}), signal);
}

export type VerificationOutcome = 'confirmed' | 'elsewhere' | 'changed';
export async function verifyEmail(account: ServicesAccount, token: string, signal: AbortSignal): Promise<VerificationOutcome> {
  const link = verificationLink(token);
  if (link?.kind === 'registration') {
    // Completion may adopt a session only for a browser that was signed out when this
    // opening began, and only if no family appears before completion. Both facts are
    // captured before the private read and retired monotonically for the whole proof.
    const start = account.service.getSnapshot();
    // Only a confirmed signed-out opening may complete; restoring/error/unread is not one.
    let retired = !(start.phase === 'signedOut' && start.session === undefined);
    const release = account.service.subscribe(() => {
      if (!retired && account.service.getSnapshot().session !== undefined) retired = true;
    });
    try {
      // Capture this browser's own private registration before the public proof, so a
      // concurrent sign-in or a consumed binding cannot substitute a successor.
      const registration = await account.client.registration(undefined, signal).catch(error => {
        if ((error as {status?: unknown})?.status === 404) return null;
        throw error;
      });
      if (signal.aborted) throw signal.reason;
      // The mailed link proves the address through the public capability, whichever
      // browser opens it, and never signs this browser in.
      const value = await account.client.request(routePath('email_link_confirm'), 'POST', routeBody('email_link_confirm', {token}), signal);
      const result = value as {kind?: unknown; status?: unknown} | undefined;
      if (!result || result.kind !== 'registration' || !['confirmed', 'already_confirmed'].includes(String(result.status)))
        throw new ApiError(502, 'invalid_response', 'invalid_response', false);
      // The caller may have retired this proof while the confirmation was queued.
      signal.throwIfAborted();
      // A family present at the opening, adopted during the proof, or reappearing after
      // a sign-out keeps the confirmed-elsewhere result instead of adopting this one.
      if (retired || registration?.registrationId !== link.resource) return 'elsewhere';
      try {
        // Complete only that captured registration, and only while the canonical
        // coordinator still admits a signed-out original opening (its own third
        // argument stays the only admission decision).
        await account.service.authenticateIfSignedOut(
          inner => account.client.verifyRegistration(link.resource, {code: link.code}, AbortSignal.any([signal, inner])),
          {},
          () => !signal.aborted && !retired && account.service.getSnapshot().session === undefined,
        );
        return 'confirmed';
      } catch (error) {
        // The address is confirmed; a concurrent adoption or family keeps its place.
        const code = (error as {code?: unknown})?.code;
        if (code === 'account_changed' || (code === 'cancelled' && !signal.aborted)) return 'elsewhere';
        throw error;
      }
    } finally {
      release();
    }
  }
  if (link?.kind === 'email_change') {
    const session = account.service.getSnapshot().session;
    const original = session?.familyId ? accountIdentity(session) : undefined;
    signal.throwIfAborted();
    const result = await account.client.request(routePath('email_link_confirm'), 'POST',
      routeBody('email_link_confirm', {token}), signal) as {kind?: unknown; status?: unknown; accountId?: unknown} | undefined;
    if (!result || result.kind !== 'email_change' || !['email_changed', 'already_confirmed'].includes(String(result.status)) ||
      typeof result.accountId !== 'string' || !/^[A-Za-z0-9_-]{1,128}$/.test(result.accountId))
      throw new ApiError(502, 'invalid_response', 'invalid_response', false);
    // A real new change revoked the affected account's families. Its known ACK
    // retires only the original locally captured family, even after view exit.
    // A receipt replay revokes nobody and must preserve a fresh valid sign-in.
    if (result.status === 'email_changed' && original?.accountId === result.accountId)
      await account.service.logoutIfCurrent(original);
    signal.throwIfAborted();
    return 'changed';
  }
  const value = await account.client.request(routePath('email_link_confirm'), 'POST', routeBody('email_link_confirm', {token}), signal);
  const result = value as {kind?: unknown; status?: unknown} | undefined;
  if (!result || !['registration', 'email_change'].includes(String(result.kind)) ||
    !['confirmed', 'already_confirmed', 'email_changed'].includes(String(result.status)))
    throw new ApiError(502, 'invalid_response', 'invalid_response', false);
  return result.status === 'email_changed' ? 'changed' : 'elsewhere';
}
