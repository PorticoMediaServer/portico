import type {SignInNote} from '@core/portico-server-sign-in.ts';
import {account, accountSession} from '../services/account-session';

/** The Portico Account this page holds, as a server sign-in uses it: who it is, whether it is still
 * held, and Portico Services' note for one server's challenge. The note's account is checked
 * against `accountId` before it goes to a server (`porticoServerSignIn`). */
export type PorticoServerAccess = Readonly<{
  origin: string; accountId: string;
  isCurrent: () => boolean;
  observe: (changed: () => void) => () => void;
  identity: (serverId: string, challenge: string, signal?: AbortSignal) => Promise<SignInNote>;
}>;

/** Bound to one account on this page: signing out (here or in another tab) ends it. */
export function porticoServerAccess(accountId: string, origin = location.origin): PorticoServerAccess {
  let ended = false;
  const off = accountSession.subscribe(() => {if (!accountSession.signedIn()) {ended = true; off();}});
  return Object.freeze({
    origin, accountId,
    isCurrent: () => !ended && accountSession.signedIn(),
    observe: (changed: () => void) => accountSession.subscribe(changed),
    identity: (serverId: string, challenge: string, signal?: AbortSignal) => account.serverIdentity(serverId, challenge, signal),
  });
}
