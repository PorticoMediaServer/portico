import {hasCapturedAccessToken} from '../session-authority.ts';
import {ApiError, type HostedSession} from '../index.ts';
import type {CredentialSessionService} from '../credentials.ts';
import type {BrowserAuthority} from './browser-account-api.ts';

export type AccountIdentity = Readonly<{accountId: string; familyId: string}>;
export function accountIdentity(session: Pick<HostedSession, 'account' | 'familyId'>): AccountIdentity {
  if (!session.familyId) throw new ApiError(401, 'account_signed_out', 'account_signed_out', false);
  return {accountId: session.account.id, familyId: session.familyId};
}
export function sameAccount(a: AccountIdentity, b: AccountIdentity) {
  return a.accountId === b.accountId && a.familyId === b.familyId;
}

type AccountPort = {service: CredentialSessionService<BrowserAuthority>};
/** Shared browser account/family boundary. Cookie renewal and cross-tab
 * synchronization remain exclusively in the canonical CredentialSessionService. */
export async function currentAccountWork<T>(account: AccountPort,
  work: (session: Omit<HostedSession, 'refreshToken'>) => Promise<T>, expected?: AccountIdentity, signal?: AbortSignal): Promise<T> {
  return accountWork(account, work, expected, false, signal);
}

/** Only a declared side-effect-free read may run once more after renewal. */
export async function currentAccountRead<T>(account: AccountPort,
  read: (session: Omit<HostedSession, 'refreshToken'>) => Promise<T>, expected?: AccountIdentity, signal?: AbortSignal): Promise<T> {
  return accountWork(account, read, expected, true, signal);
}

async function accountWork<T>(account: AccountPort,
  work: (session: Omit<HostedSession, 'refreshToken'>) => Promise<T>, expected: AccountIdentity | undefined, retryRead: boolean, signal?: AbortSignal): Promise<T> {
  const required = expected && {accountId: expected.accountId, familyId: expected.familyId};
  signal?.throwIfAborted();
  await account.service.synchronize();
  const session = await account.service.accessSession();
  const identity = accountIdentity(session);
  const accessToken = session.accessToken;
  signal?.throwIfAborted();
  if (required && !sameAccount(identity, required)) throw accountChanged();
  const admitted = (token: string) => {
    const current = account.service.getSnapshot().session;
    if (!current?.familyId || !sameAccount(identity, accountIdentity(current)) || !hasCapturedAccessToken(current, token)) throw accountChanged();
  };
  admitted(accessToken);
  let result: T;
  try {result = await work(session);}
  catch (error) {
    if ((error as {status?: unknown})?.status !== 401) throw error;
    signal?.throwIfAborted();
    await account.service.synchronize();
    signal?.throwIfAborted();
    const current = account.service.getSnapshot().session;
    if (!current?.familyId || !sameAccount(identity, accountIdentity(current))) throw accountChanged();
    // The expectation is checked again inside the canonical coordinator, after
    // its storage awaits. A queued old 401 cannot renew a successor family.
    const renewed = await account.service.accessSession(true, {...identity, accessToken});
    signal?.throwIfAborted();
    if (!sameAccount(identity, accountIdentity(renewed))) throw accountChanged();
    if (!retryRead) throw error; // never replay a mutation or multi-step operation
    admitted(renewed.accessToken);
    result = await work(renewed); // one retry only; a second 401 is terminal here
  }
  await account.service.synchronize();
  signal?.throwIfAborted();
  const current = account.service.getSnapshot().session;
  if (!current || !sameAccount(identity, accountIdentity(current))) throw accountChanged();
  return result;
}

function accountChanged() { return new ApiError(409, 'account_changed', 'account_changed', false); }
