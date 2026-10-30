import {throwIfAborted} from '../../../../packages/client-core/src/abort-lifetime';
import {ApiError, type HostedSession} from '../../../../packages/client-core/src';
import type {CredentialAccessExpectation, CredentialSessionService} from '../../../../packages/client-core/src/credentials';

type Session = Omit<HostedSession, 'refreshToken'>;
type Opening = Readonly<{expected: CredentialAccessExpectation; isCurrent: () => boolean; signal?: AbortSignal}>;
const changed = () => new ApiError(409, 'account_changed', 'account_changed', false);

/** Only a declared side-effect-free Hosted GET can repeat after canonical renewal. */
export function nativeAccountRead<T>(owner: CredentialSessionService, read: (session: Session) => Promise<T>, opening: Opening): Promise<T> {
  return accountWork(owner, read, opening, true);
}
/** Commands reconcile definitive refusal, but their original callback is never replayed. */
export function nativeAccountWork<T>(owner: CredentialSessionService, work: (session: Session) => Promise<T>, opening: Opening): Promise<T> {
  return accountWork(owner, work, opening, false);
}
export function accountExpectation(session: Session): CredentialAccessExpectation {
  return Object.freeze({accountId: session.account.id, familyId: session.familyId, accessToken: session.accessToken});
}
async function accountWork<T>(owner: CredentialSessionService, work: (session: Session) => Promise<T>, opening: Opening, retryRead: boolean): Promise<T> {
  const expected = Object.freeze({...opening.expected}), current = opening.isCurrent, signal = opening.signal;
  let retired = false;
  const sameFamily = () => {const live = owner.getSnapshot().session;
    return live?.account.id === expected.accountId && live.familyId === expected.familyId;};
  // Monotonic for this request, including A -> B -> A while HTTP/storage is held.
  const off = owner.subscribe(() => {if (!sameFamily()) retired = true;});
  const check = (token?: string) => {
    throwIfAborted(signal);
    if (retired || !current()) throw changed();
    throwIfAborted(signal);
    if (retired || !sameFamily() || token !== undefined && owner.getSnapshot().session?.accessToken !== token) throw changed();
  };
  try {
    check(); await owner.synchronize(); check();
    const session = await owner.accessSession(false, expected); check(session.accessToken);
    const issued = accountExpectation(session);
    let result: T;
    try {result = await work(session);}
    catch (error) {
      if (!(error instanceof ApiError) || error.status !== 401) throw error;
      check(); await owner.synchronize(); check();
      // Canonical owner rechecks this exact issuing tuple after its coordinator/storage awaits.
      const renewed = await owner.accessSession(true, issued); check(renewed.accessToken);
      if (!retryRead) throw error;
      result = await work(renewed); // exactly one safe read retry; never loop on a second 401
    }
    check(); await owner.synchronize(); check(); return result;
  } finally {off();}
}
