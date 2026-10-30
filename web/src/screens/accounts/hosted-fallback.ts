import {addressClass} from '@core/network-locality.ts';
import type {DirectoryFallback} from '../../kit/accounts/DirectoryStatus';
import type {DirectoryItem, DirectorySnapshot} from '../../services/directory-flow';

/** The single way to the server's own page, worded for the case (P2-66 D-4). Its address is
 * one this browser already proved, or the account's verified direct address. */
export function fallbackFor(state: Pick<DirectorySnapshot, 'status'>, target: DirectoryItem, saved: ReadonlyMap<string, string>, open: (address: string) => void): DirectoryFallback | undefined {
  const lastGood = saved.get(target.id), local = lastGood && isLocalRoute(lastGood) ? lastGood : undefined;
  const go = (address: string) => () => open(new URL('/', address).href);
  if (state.status === 'update') return target.baseUrl ? {kind: 'direct', onPress: go(target.baseUrl)} : undefined;
  if (state.status === 'account-waiting' && target.ownedByMe) return local ? {kind: 'local', onPress: go(local)} : target.baseUrl ? {kind: 'direct', onPress: go(target.baseUrl)} : undefined;
  if ((state.status === 'waiting' || state.status === 'unreachable') && local) return {kind: 'local', onPress: go(local)};
  return undefined;
}
/** A server's own home-network address: a private or loopback IP, a Portico direct name that
 * encodes one, or a home-network name (P2-69's locality rule). */
export function isLocalRoute(origin: string): boolean {
  try {
    const cls = addressClass(new URL(origin).hostname);
    return cls === 'lan' || cls === 'loopback';
  } catch {return false;}
}
