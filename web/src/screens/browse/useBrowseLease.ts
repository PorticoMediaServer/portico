import {forwardIdentity} from '../detail/context';
import {useRef, useLayoutEffect, useState} from 'react';
import {LibraryContentService} from '@core/library-content.ts';
import {screenLifetime, type ScreenContext} from '../detail/context';
import {BrowseController, type BrowseSource} from './controller';

export type BrowseLease = Readonly<{
  id: string; context: ScreenContext; screen: ScreenContext; sourceKey: string; routeKey: string; playbackOwner: unknown;
  life: ReturnType<typeof screenLifetime>; controller: BrowseController; service: LibraryContentService;
}>;

/** A committed opening owns terminal resources. Strict effect replay creates
 * another opening; it never restarts the controller or API it just disposed.
 * Each returned API/action keeps the original route predicate and authority. */
export function useBrowseLease(context: ScreenContext, sourceKey: string, routeKey: string, routeCurrent: () => boolean, playbackOwner?: unknown): BrowseLease | undefined {
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
    const controller = new BrowseController(life.api, JSON.parse(sourceKey) as BrowseSource, current);
    const service = new LibraryContentService({api: life.api, scope: context.scope});
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const lease: BrowseLease = {id: crypto.randomUUID(), context, screen, sourceKey, routeKey, playbackOwner, life, controller, service};
    publish(lease);
    return () => {life.dispose();controller.dispose();service.dispose();};
    // routeCurrent is deliberately captured by this opening, never replaced by
    // a successor closure. routeKey identifies changes to that predicate.
  }, [context, sourceKey, routeKey, playbackOwner]);
  return published?.context === context && published.sourceKey === sourceKey && published.routeKey === routeKey
    && published.playbackOwner === playbackOwner && published.life.current() ? published : undefined;
}
