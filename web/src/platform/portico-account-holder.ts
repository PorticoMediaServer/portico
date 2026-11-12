import type {Account} from '@core/portico-account.ts';
import {openAccountServerList} from '@core/portico-server-list.ts';
import {webRouteCrypto} from '@core/route-identity.ts';
import {account, accountSession} from '../services/account-session';
import {porticoServerAccess} from './portico-server-access';

/** One of the account's servers, as its signed list gives it (`portico-server-list.ts`). The list
 * says nothing about whether a server is up, so a row is `listed`: neither online nor offline. */
export type ListedServerRow = Readonly<{
  id: string; name: string; baseUrl: string; publicKey: string;
  role?: 'owner' | 'admin' | 'member'; ownedByMe: boolean;
  status: 'listed'; presence: Readonly<{lastSeenAt: null}>;
}>;
export type ListedServers = Readonly<{accountId: string; items: readonly ListedServerRow[]}>;

/** The one Portico Account this browser holds, for the app at web.getportico.tv: the account
 * pages' sign-in (`services/account-session.ts`: the refresh token in Portico Services' HttpOnly
 * cookie, the access token in this page's memory). The account id is the scope everywhere a
 * server sign-in or a server list is bound to it. */
export const porticoAccountHolder = Object.freeze({
  subscribe: accountSession.subscribe,
  signedIn: accountSession.signedIn,
  signIns: accountSession.signIns,
  /** The account signed in now (one read per sign-in on this page). */
  who: (signal?: AbortSignal): Promise<Account> => {
    if (heldFor !== accountSession.signIns()) {held = undefined; heldFor = accountSession.signIns();}
    held ??= account.account(signal).catch(error => {held = undefined; throw error;});
    return held;
  },
  /** The account's checked server list. */
  servers: async (signal?: AbortSignal): Promise<ListedServers> => {
    const list = await openAccountServerList(account, webRouteCrypto, {signal});
    return {accountId: list.accountId, items: list.items.map(item => Object.freeze({id: item.id, name: item.name, baseUrl: item.baseUrl, publicKey: item.publicKey,
      ...(item.role ? {role: item.role} : {}), ownedByMe: item.ownedByMe === true, status: 'listed' as const, presence: Object.freeze({lastSeenAt: null})}))};
  },
  access: (accountId: string) => porticoServerAccess(accountId),
  leave: (serverId: string) => account.leaveServer(serverId),
  remove: (serverId: string) => account.removeServer(serverId),
  signOut: () => account.signOut(),
});
let held: Promise<Account> | undefined, heldFor = accountSession.signIns();
accountSession.subscribe(() => {if (!accountSession.signedIn()) held = undefined;});

/** Before the new Portico Services, the app kept its own Portico Account sign-in in this
 * browser's storage. Nothing of the account is kept there now; what an older version left is
 * removed the first time this module loads. */
export function forgetOldAccountStorage(storage: Pick<Storage, 'length' | 'key' | 'removeItem'>) {
  const old: string[] = [];
  for (let i = 0; i < storage.length; i++) {
    const key = storage.key(i);
    if (key && (key.startsWith('portico.account.v1:') || key.startsWith('portico.account.csrf.'))) old.push(key);
  }
  for (const key of old) storage.removeItem(key);
}
try {if (typeof localStorage !== 'undefined') forgetOldAccountStorage(localStorage);} catch {/* private window */}
