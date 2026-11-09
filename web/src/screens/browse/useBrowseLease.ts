import {forwardIdentity} from '../detail/context';
import {useRef, useLayoutEffect, useState} from 'react';
import {LibraryContentService} from '@core/library-content.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';

export type BrowseLease = Readonly<{
  id: string; context: ScreenContext; screen: ScreenContext; routeKey: string; playbackOwner: unknown;
  life: ReturnType<typeof screenLifetime>; service: LibraryContentService;
}>;

/** A committed opening owns terminal resources. Strict effect replay creates
 * another opening; it never restarts the API it just disposed.
 * Each returned API/action keeps the original route predicate and authority.
 * `routeKey` is the page's address without its query, so a filter or a sort is never another
 * opening. The grid itself is not the opening's: it comes from the viewer's cache under its
 * query (browse-cache.ts) and outlives the screen. */
export function useBrowseLease(context: ScreenContext, routeKey: string, routeCurrent: () => boolean, playbackOwner?: unknown): BrowseLease | undefined {
  const [published, publish] = useState<BrowseLease>();
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({routeCurrent});
  useLayoutEffect(() => {setupPorts.current = {routeCurrent};});
  useLayoutEffect(() => {
    const {routeCurrent} = setupPorts.current;
    const original = screenLifetime(context);
    let live = true;
    const current = () => live && original.current() && routeCurrent();
    const assert = () => {if (!current()) throw new DOMException('Browse opening retired', 'AbortError');};
    const life: ReturnType<typeof screenLifetime> = {...original, current, assert,
      api: {
        async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
          assert();const value = await original.api.request<T>(path, method, body, signal);assert();return value;
        },
        // The preparation reader guards publication and needs late raw handles for cleanup.
        requestRaw: context.api.requestRaw.bind(context.api),
      },
      dispose: () => {live = false;original.dispose();},
    };
    const service = new LibraryContentService({queries: context.queries, scope: context.scope});
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const lease: BrowseLease = {id: crypto.randomUUID(), context, screen, routeKey, playbackOwner, life, service};
    publish(lease);
    return () => {life.dispose();service.dispose();};
    // routeCurrent is deliberately captured by this opening, never replaced by
    // a successor closure. routeKey identifies changes to that predicate.
  }, [context, routeKey, playbackOwner]);
  return published?.context === context && published.routeKey === routeKey
    && published.playbackOwner === playbackOwner && published.life.current() ? published : undefined;
}
