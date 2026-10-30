import {useLayoutEffect} from 'react';
import {findNodeHandle, TVEventHandler, type HWEvent, type View} from 'react-native';
import type {FocusRegistry} from './registry';
import {createTVKeyAdapter} from '../../platform/tv-key-adapter';
import {tvKeySource} from '../../platform/tv-key-source';

type Page = {hasMore: boolean; failed?: boolean; loading: boolean; waitingId: string; requestNext: () => void};
/** Observe the native tail boundary to retry data. Never consumes a key or selects a focus target. */
export function useRowPageBoundary(enabled: boolean, registry: FocusRegistry | null, regionId: string | undefined, tailId: string | undefined, page: Page | undefined) {
  const waitingId = page?.waitingId, retry = page?.requestNext;
  const failed = !!page?.hasMore && !!page.failed && !page.loading;
  useLayoutEffect(() => {
    if (!enabled || !registry || !regionId || !tailId || !failed || !waitingId || !retry) return;
    const keys = createTVKeyAdapter({source: tvKeySource});
    const listener = TVEventHandler.addListener((raw: HWEvent) => {
      const event = keys.read(raw);
      if (!event || event.key !== 'right' || event.phase !== 'press' || event.repeat) return;
      const active = registry.capture();
      if (active?.regionId !== regionId || active.childId !== tailId) return;
      const host = registry.child(active);
      if (!host) return;
      const tag = findNodeHandle(host as View);
      if (event.tag !== undefined && event.tag !== tag) return;
      retry();
    });
    return () => listener?.remove();
  }, [enabled, failed, regionId, registry, retry, tailId, waitingId]);
}
