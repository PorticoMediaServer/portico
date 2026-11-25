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
  dispose?: () => void;
}>;

/** Bound to one account sign-in on this page: signing out or starting another sign-in (here or in
 * another tab) ends it. Call `dispose` when finished to release the session listener. */
export function porticoServerAccess(accountId: string, origin = location.origin): PorticoServerAccess {
  const signIns = accountSession.signIns();
  let ended = !accountSession.signedIn();
  let off: (() => void) | undefined;
  const retire = () => {
    ended = true;
    off?.();
    off = undefined;
  };
  if (!ended) {
    off = accountSession.subscribe(() => {
      if (!accountSession.signedIn() || accountSession.signIns() !== signIns) retire();
    });
  }
  return Object.freeze({
    origin, accountId,
    isCurrent: () => !ended && accountSession.signedIn() && accountSession.signIns() === signIns,
    observe: (changed: () => void) => ended ? () => {} : accountSession.subscribe(changed),
    identity: (serverId: string, challenge: string, signal?: AbortSignal) => account.serverIdentity(serverId, challenge, signal),
    dispose: retire,
  });
}
