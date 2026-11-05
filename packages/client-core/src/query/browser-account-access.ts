import type {HostedRequestApi} from '../portico-servers.ts';
import type {BrowserAccountApi, BrowserAuthority} from './browser-account-api.ts';
import type {CredentialSessionService} from '../credentials.ts';
import {accountIdentity, currentAccountWork, currentAccountRead, sameAccount, type AccountIdentity} from './browser-account-work.ts';

export type AccountServerAccess = Readonly<{
  origin: string; accountId: string; request: HostedRequestApi['request'];
  isCurrent: () => boolean; observe?: (changed: () => void) => () => void;
}>;

/** Bind the caller's actual browser account to one directory selection. Only
 * the canonical transport reads its bearer; views hold no copied credential. */
export function createAccountServerAccess(account: {api: BrowserAccountApi; service: CredentialSessionService<BrowserAuthority>}, scope: AccountIdentity): AccountServerAccess {
  const captured = Object.freeze({...scope});
  return Object.freeze({
    origin: account.api.origin, accountId: captured.accountId,
    isCurrent: () => {
      const session = account.service.getSnapshot().session;
      return !!session && !!session.familyId && sameAccount(accountIdentity(session), captured);
    },
    observe: (changed: () => void) => account.service.subscribe(changed),
    request: async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
      signal?.throwIfAborted();
      const value = await (method === 'GET' && body === undefined ? currentAccountRead : currentAccountWork)(account, async session => {
        signal?.throwIfAborted();
        return account.api.send<T>(path, method, body, signal, session.accessToken);
      }, captured, signal);
      signal?.throwIfAborted();
      return value;
    },
  });
}

/** Profile recovery uses only the family that opened its existing Portico flow. */
export function createBrowserProfileRecoveryTransport(account: {api: BrowserAccountApi}, session: {accessToken: string}, current: () => boolean) {
  return {request: async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (!current()) throw new DOMException('Account changed', 'AbortError');
    const response = await account.api.send<T>(path, method, body, signal, session.accessToken);
    if (!current()) throw new DOMException('Account changed', 'AbortError');
    return response;
  }};
}

/** Registered returns remain commands: canonical renewal never replays a return. */
export function createBrowserRegisteredReturnTransport(account: {api: BrowserAccountApi; service: CredentialSessionService<BrowserAuthority>}, expected: AccountIdentity,
  signal: AbortSignal, current: () => Promise<void>) {
  return {request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    await current();
    return currentAccountWork(account, session => account.api.send<T>(path, method, body, signal, session.accessToken), expected);
  }};
}
