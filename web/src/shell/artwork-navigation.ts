import {useLayoutEffect, useState} from 'react';
import {router} from '../router';
import {routeMotionOwnedByBrowser} from './route-motion';
import {consumeSourceArtActivation} from '../kit/cards/source-art-activation';
import {admittedTitleArtwork, backTitleArtwork, navigateTitleArtwork, releaseTitleArtwork, retireTitleArtworkScope, type ArtworkLocation} from '../platform/card-title-motion';
import type {SourceArtClaim} from '../kit/cards/source-art-transition';

export function artworkLocation(): ArtworkLocation {
  const value = router.state.location;
  return {key: value.state.__TSR_key ?? '', href: value.href, pathname: value.pathname};
}
type ItemDestination = {to: string; params?: Readonly<Record<string, string>>};
/** The route effect supplies its actual resolved destination and original entry.
 * The kit's synchronous evidence never supplies an id, scope or navigation. */
export function navigateArtworkEntry(entry: object, destination: ItemDestination, scope: string, admitted: () => boolean, principalCurrent: () => boolean,
  navigate: (manual: boolean) => Promise<void>): Promise<void> {
  if (!admitted()) return Promise.reject(new DOMException('Artwork opening retired', 'AbortError'));
  const physical = consumeSourceArtActivation(), source = artworkLocation();
  return navigateTitleArtwork(destination.to === '/item/$itemId' ? physical?.art : undefined, {
    scope, entry, sourceCurrent: physical?.current ?? (() => false), itemId: destination.params?.itemId ?? '', source, admitted, principalCurrent, navigate,
    location: artworkLocation, browserOwns: routeMotionOwnedByBrowser,
    subscribe: listener => router.subscribe('onBeforeNavigate', event => listener({
      from: event.fromLocation ? {key: event.fromLocation.state.__TSR_key ?? '', href: event.fromLocation.href, pathname: event.fromLocation.pathname} : undefined,
      to: {key: event.toLocation.state.__TSR_key ?? '', href: event.toLocation.href, pathname: event.toLocation.pathname},
    })),
  });
}
export function retireArtworkScope(scope: string): void {if (typeof document !== 'undefined') retireTitleArtworkScope(document, scope);}
export function useAdmittedTitleArtwork(scope: string, itemId: string, entry: ArtworkLocation, current: () => boolean): SourceArtClaim | undefined {
  const [published, publish] = useState<{scope: string; itemId: string; entry: ArtworkLocation; claim: SourceArtClaim}>();
  const {key, href, pathname} = entry;
  useLayoutEffect(() => {
    if (!current()) return;
    const claim = admittedTitleArtwork(document, scope, itemId, {key, href, pathname});
    if (!claim) return;
    publish({scope, itemId, entry: {key, href, pathname}, claim});
    return () => releaseTitleArtwork(claim);
    // Original current predicate is captured for this exact committed tuple.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scope, itemId, key, href, pathname]);
  return published?.scope === scope && published.itemId === itemId && published.entry.key === key && published.entry.href === href && published.entry.pathname === pathname ? published.claim : undefined;
}

export function backArtworkEntry(scope: string, admitted: () => boolean, back: () => void): void {
  if (!admitted()) throw new DOMException('Artwork Back retired', 'AbortError');
  const original = artworkLocation();
  backTitleArtwork(document, scope, () => {const accepted = admitted(), now = artworkLocation();if (!accepted || now.key !== original.key || now.href !== original.href || now.pathname !== original.pathname) throw new DOMException('Artwork Back retired', 'AbortError');back();}, listener => router.subscribe('onRendered', event => {
    const value = event.toLocation;
    listener({key: value.state.__TSR_key ?? '', href: value.href, pathname: value.pathname});
  }));
}
