import type {HostedSession} from '@core/index.ts';
import {accountIdentity, currentAccountWork, sameAccount, type ServicesAccount} from './session';

/** The invitation controller receives the existing family, never another session. */
export function servicesInvitationAccount(account: ServicesAccount, session: Omit<HostedSession, 'refreshToken'>) {
  const identity = accountIdentity(session);
  return {
    origin: account.api.origin,
    accountId: identity.accountId,
    username: session.account.username,
    isCurrent: () => {
      const current = account.service.getSnapshot().session;
      return Boolean(current?.familyId && sameAccount(identity, accountIdentity(current)));
    },
    request: <T,>(path: string, method?: string, body?: unknown, signal?: AbortSignal) =>
      currentAccountWork(account, current => account.api.send<T>(path, method ?? 'GET', body, signal, current.accessToken), identity),
  };
}
