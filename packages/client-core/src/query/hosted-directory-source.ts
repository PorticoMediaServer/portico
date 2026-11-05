import {HttpLocalApi} from '../index.ts';
import type {CredentialAccessExpectation, CredentialSessionService} from '../credentials.ts';
import type {HostedConnectionSource} from '../server-connections.ts';
import {RouteError} from '../route-identity.ts';
import {nativeAccountRead, accountExpectation} from './native-account-work.ts';

import {createHostedAccountApi} from './hosted-account-transport.ts';
export function createNativeHostedSource(
  origin: string,
  fetcher: typeof fetch,
  credentials: CredentialSessionService | (() => CredentialSessionService),
  accountId: string,
  expected?: CredentialAccessExpectation,
): HostedConnectionSource {
  const captured = expected && Object.freeze({...expected});
  return {
    origin,
    accountId,
    request: async <T>(path: string, signal: AbortSignal) => {
      if (signal.aborted) throw new RouteError('cancelled', 'account_route_cancelled');
      const owner = typeof credentials === 'function' ? credentials() : credentials;
      const original = owner.getSnapshot().session;
      if (!original || original.account.id !== accountId || captured && (captured.accountId !== accountId || original.familyId !== captured.familyId))
        throw new RouteError('account_changed', 'Your Portico Account changed.');
      return nativeAccountRead(owner, session => createHostedAccountApi(owner, session, fetcher)
        .request<T>(path, 'GET', undefined, signal), {expected: captured ?? accountExpectation(original), signal,
          isCurrent: () => !signal.aborted});
    },
  };
}

/** A read-only directory port for paths already built by the connection service. */
export function createHostedDirectoryReadPort(request: import('../portico-servers.ts').HostedRequestApi['request']) {
  return <T>(path: string, signal: AbortSignal) => request<T>(path, 'GET', undefined, signal);
}

export function createBrowserHostedSource(account: Pick<import('./hosted-account-transport.ts').BrowserHostedAccount, 'api' | 'service'>, accountId: string): HostedConnectionSource {
  return {origin: account.api.origin, accountId, request: async <T>(path: string, signal: AbortSignal): Promise<T> => {
    await account.service.synchronize(); const session = await account.service.accessSession();
    if (session.account.id !== accountId) throw new Error('The saved server belongs to a different Portico Account.');
    return account.api.send<T>(path, 'GET', undefined, signal, session.accessToken);
  }};
}
