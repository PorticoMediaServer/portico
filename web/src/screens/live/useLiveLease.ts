import {forwardIdentity} from '../detail/context';
import {useRef, useLayoutEffect, useState} from 'react';
import type {LegacyGuideOptions} from '@core/guide/legacy-source.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback';
import type {ScreenContext} from '../detail/context';
import {createLiveBindings, type LiveBindings} from './bindings';

export type LiveLease = {
  id: string;
  /** The original committed screen context, and the route/lease-fenced view of it. */
  context: ScreenContext;
  screen: ScreenContext;
  optionsKey: string;
  options: LegacyGuideOptions;
  routeKey: string;
  routeCurrent: () => boolean;
  playbackOwner: WebShellPlaybackRuntime | undefined;
  revision: number;
  /** False once this opening's layout retirement ran, before its bindings abort. */
  live: boolean;
  bindings: LiveBindings;
  life: LiveBindings['life'];
};

/** A committed guide opening owns its bindings, guide options and playback
 * runtime. Strict effect replay creates another opening and layout retirement
 * retires this one before disposing it; a successor never revives an
 * already-aborted store. The route predicate is captured by this opening,
 * never replaced by a later one. An authority failure aborts the API life but
 * keeps the opening itself live, so its failure surface can still be drawn. */
export function useLiveLease(context: ScreenContext, optionsKey: string, routeKey: string, routeCurrent: () => boolean,
  playbackOwner: WebShellPlaybackRuntime | undefined, revision: number): LiveLease | undefined {
  const [published, publish] = useState<LiveLease>();
  // Publish only at commit, then sample once for this setup's original lifetime.
  const setupPorts = useRef({routeCurrent});
  useLayoutEffect(() => {setupPorts.current = {routeCurrent};});
  useLayoutEffect(() => {
    const {routeCurrent} = setupPorts.current;
    let live = true, authorityFailed = false;
    const current = () => live && !authorityFailed && context.isCurrent() && routeCurrent();
    const screen: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const options = JSON.parse(optionsKey) as LegacyGuideOptions;
    const bindings = createLiveBindings(screen, options);
    // A 401/403 aborts the API life. It is still this opening's authority
    // failure, not a disposed route, so the opening stays live for its surface
    // while every later request and retained action refuses.
    const off = bindings.failure.subscribe(() => {authorityFailed = !!bindings.failure.getSnapshot();});
    authorityFailed = !!bindings.failure.getSnapshot();
    const lease: LiveLease = {id: crypto.randomUUID(), context, screen, optionsKey, options, routeKey, routeCurrent,
      playbackOwner, revision, live: true, bindings, life: bindings.life};
    publish(lease);
    return () => {live = false;lease.live = false;off();bindings.dispose();};
    // routeCurrent belongs to this exact opening; a successor never adopts it.
  }, [context, optionsKey, routeKey, playbackOwner, revision]);
  return published?.live && published.context === context && published.optionsKey === optionsKey && published.routeKey === routeKey
    && published.playbackOwner === playbackOwner && published.revision === revision ? published : undefined;
}
