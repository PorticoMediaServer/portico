import {dispatchNativeAccountReturn} from '../platform/native-account-return';
import {parseJoinLink} from '@core/join-link.ts';
import {captureServicesLink, scrubUnsupportedServicesFragment} from './intake';
import {captureHostedSignIn} from './sign-in';
import {captureHostedOwnerClaim} from './owner-claim';

/** No rendering, storage or account work can precede the Apps-owned native scrub/dispatch. */
export function captureServicesEntry(page: Pick<Location, 'href' | 'replace' | 'pathname' | 'search' | 'hash'>,
  history: Pick<History, 'state' | 'replaceState'>, expectedHostedOrigin: string) {
  const native = dispatchNativeAccountReturn(page, history, expectedHostedOrigin);
  if (native) return {native};
  const url = new URL(page.href);
  if (['/', '/account'].includes(url.pathname) && !url.hash && [...url.searchParams.keys()].length === 1 && url.searchParams.has('identityError')) {
    const value = url.searchParams.get('identityError');
    history.replaceState(null, '', url.pathname === '/account' ? '/account?tab=security' : '/');
    return {providerFailure: value === 'google' || value === 'apple' ? value : 'unknown'} as const;
  }
  const signIn = captureHostedSignIn(page, history, expectedHostedOrigin);
  if (signIn) return {signIn};
  const owner = captureHostedOwnerClaim(page, history, expectedHostedOrigin);
  if (owner) return {owner};
  if (page.pathname === '/join' || page.pathname === '/join/') {
    const href = page.href;
    // Canonical invitations stay only in this document. A provider navigation requires reopening
    // the invitation; it is never serialized into another continuation or the account store.
    const join = parseJoinLink(href);
    const url = new URL(href);
    const state = history.state && typeof history.state === 'object' ? history.state as Record<string, unknown> : {};
    const scrubbed = state.porticoJoinScrubbed === true;
    // A plain /join is the typed-entry composition only before any capability attempt.
    // No query, fragment, empty delimiter (literal ? or #) or earlier scrub may turn
    // a malformed or lost link into it.
    const delimiterFree = !page.search && !page.hash && !url.search && !url.hash && url.pathname === page.pathname && url.href === url.origin + url.pathname;
    if (!join && delimiterFree && !scrubbed) return {plainShortJoin: true, invalidContinuation: false};
    if (page.hash) history.replaceState({...state, porticoJoinScrubbed: true}, '', page.pathname + page.search);
    return {join, invalidContinuation: !join};
  }
  const capture = captureServicesLink(page, history);
  return {capture, invalidContinuation: scrubUnsupportedServicesFragment(page, history)};
}
