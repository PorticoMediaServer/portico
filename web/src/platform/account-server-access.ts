import type {HostedRequestApi} from '@core/portico-servers.ts';
import type {browserAccount} from './account';
import {accountIdentity, currentAccountWork, currentAccountRead, sameAccount, type AccountIdentity} from './account-work';

export type AccountServerAccess = Readonly<{
  origin: string; accountId: string; request: HostedRequestApi['request'];
  isCurrent: () => boolean; observe?: (changed: () => void) => () => void;
}>;

/** Bind the caller's actual browser account to one directory selection. Only
 * the canonical transport reads its bearer; views hold no copied credential. */
export function createAccountServerAccess(account: Pick<ReturnType<typeof browserAccount>, 'api' | 'service'>, scope: AccountIdentity): AccountServerAccess {
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
