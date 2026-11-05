import type {BrowserHostedAccount} from './hosted-account-transport.ts';
import {AccountNotificationsClient} from '../account-notifications.ts';
import {AccountSessionReviewService} from '../account-sessions.ts';
import {currentAccountWork, currentAccountRead, type AccountIdentity} from './browser-account-work.ts';
import {createAccountServerDirectory} from './browser-account-directory.ts';

/** Every read/mutation is bound to the account and family that opened this workspace. */
export function workspaceApi(account: BrowserHostedAccount, identity: AccountIdentity) {
  const servers = createAccountServerDirectory(account, identity);
  const request = <T>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal) =>
    (method === 'GET' && body === undefined ? currentAccountRead : currentAccountWork)(account, session => {
      signal?.throwIfAborted();
      return account.api.send<T>(path, method, body, signal, session.accessToken);
    }, identity, signal);
  return {
    request,
    notifications: new AccountNotificationsClient(request),
    sessions: () => new AccountSessionReviewService({scope: identity, api: {request}}),
    security: (signal?: AbortSignal) => currentAccountRead(account, session => {
      signal?.throwIfAborted();return account.client.state(identity, session.accessToken, signal);
    }, identity, signal),
    servers: servers.read,
  };
}

export type WorkspaceApi = ReturnType<typeof workspaceApi>;
export type WorkspaceServers = Awaited<ReturnType<WorkspaceApi['servers']>>;
