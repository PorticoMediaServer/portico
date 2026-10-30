import {useSyncExternalStore} from 'react';

/** Fullscreen's top layer excludes body siblings of the fullscreen host. */
export function overlayPortalTarget<T>(owner: {readonly fullscreenElement: T | null; readonly body: T}): T {
  return owner.fullscreenElement ?? owner.body;
}

export function subscribeOverlayPortalTarget(listener: () => void, owner: EventTarget = document): () => void {
  owner.addEventListener('fullscreenchange', listener);
  return () => owner.removeEventListener('fullscreenchange', listener);
}

const snapshot = () => overlayPortalTarget(document);
const serverSnapshot = () => null;

/** Shared portals follow real DOM fullscreen entry, exit and host replacement. */
export function useOverlayPortalTarget(): Element | null {
  return useSyncExternalStore(subscribeOverlayPortalTarget, snapshot, serverSnapshot);
}
