import {forwardIdentity} from '../detail/context';
import {useRef, useLayoutEffect, useState} from 'react';
import {LibraryContentService, type LibraryContentSnapshot} from '@core/library-content.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {usePageMemory} from '../../platform/freshness';
import {BrowseController, type BrowsePageSeed, type BrowseSource} from './controller';

/** The page one browse opening shows, keyed by the viewer and the exact route query. */
export function browsePageKey(context: Pick<ScreenContext, 'scope'>, sourceKey: string): string {
  return JSON.stringify(['browse-page', context.scope.serverId, context.scope.viewerId, sourceKey]);
}
/** The Discover list beside the same opening (the service the discovery screen reads). */
export function browseDiscoverKey(context: Pick<ScreenContext, 'scope'>, sourceKey: string): string {
  return JSON.stringify(['browse-discover', context.scope.serverId, context.scope.viewerId, sourceKey]);
}

export type BrowseLease = Readonly<{
  id: string; context: ScreenContext; screen: ScreenContext; sourceKey: string; routeKey: string; playbackOwner: unknown;
  life: ReturnType<typeof screenLifetime>; controller: BrowseController; service: LibraryContentService;
}>;

/** A committed opening owns terminal resources. Strict effect replay creates
 * another opening; it never restarts the controller or API it just disposed.
 * Each returned API/action keeps the original route predicate and authority. */
export function useBrowseLease(context: ScreenContext, sourceKey: string, routeKey: string, routeCurrent: () => boolean, playbackOwner?: unknown): BrowseLease | undefined {
  const pages = usePageMemory();
  const gridKey = browsePageKey(context, sourceKey), discoverKey = browseDiscoverKey(context, sourceKey);
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
        async acknowledgeUserMutation(intentId: string): Promise<void> {assert();await original.api.acknowledgeUserMutation(intentId);assert();},
        async request<T>(path: string, method?: string, body?: unknown, signal?: AbortSignal): Promise<T> {
          assert();const value = await original.api.request<T>(path, method, body, signal);assert();return value;
        },
        // The preparation reader guards publication and needs late raw handles for cleanup.
        requestRaw: context.api.requestRaw.bind(context.api),
      },
      dispose: () => {live = false;original.dispose();},
    };
    // Back and Forward re-open this exact query: the grid and the Discover list it drew are
    // shown at once, then read again in place (the controller and the service do that).
    const controller = new BrowseController(life.api, JSON.parse(sourceKey) as BrowseSource, current, pages.recall<BrowsePageSeed>(gridKey));
    const service = new LibraryContentService({api: life.api, scope: context.scope, initial: pages.recall<LibraryContentSnapshot>(discoverKey)});
    const rememberGrid = () => {const page = controller.pageSeed(); if (page) pages.remember(gridKey, page);};
    const rememberDiscover = () => {const snapshot = service.getSnapshot(); if (snapshot.phase === 'ready' && snapshot.projection) pages.remember(discoverKey, snapshot);};
    rememberGrid(); rememberDiscover();
    const offGrid = controller.subscribe(rememberGrid), offDiscover = service.subscribe(rememberDiscover);
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const lease: BrowseLease = {id: crypto.randomUUID(), context, screen, sourceKey, routeKey, playbackOwner, life, controller, service};
    publish(lease);
    return () => {offGrid();offDiscover();life.dispose();controller.dispose();service.dispose();};
    // routeCurrent is deliberately captured by this opening, never replaced by
    // a successor closure. routeKey identifies changes to that predicate.
  }, [context, sourceKey, routeKey, playbackOwner, pages, gridKey, discoverKey]);
  return published?.context === context && published.sourceKey === sourceKey && published.routeKey === routeKey
    && published.playbackOwner === playbackOwner && published.life.current() ? published : undefined;
}
