import {forwardIdentity} from '../detail/context';
import {useLayoutEffect, useState} from 'react';
import type {ShowWorkspaceRoute} from '@core/show-workspace.ts';
import type {WebShellPlaybackRuntime} from '../../platform/playback/runtime';
import type {ProtectedLibraryScreen} from '../browse/screen-context';
import type {ScreenContext} from './context';
import {createShowBindings, type ShowBindings} from './show-bindings';
import {createEpisodeSelection} from './episode-selection';

type ShowRouteLease = Readonly<{
  context: ScreenContext; assembly: ProtectedLibraryScreen['context']; targetKey: string;
  runtime?: WebShellPlaybackRuntime;
  current: () => boolean; binding: ShowBindings; selection: ReturnType<typeof createEpisodeSelection>;
}>;

/** One committed original show/season opening. Its binding keeps its own season,
 * expanded episode, view and revalidation state; data updates never reconstruct
 * it. A replaced route/authority/runtime instead receives entirely fresh owners. */
export function useShowRoute(context: ScreenContext | undefined, captured: ProtectedLibraryScreen, target: ShowWorkspaceRoute) {
  const assembly = captured.context, runtime = captured.binding.playback?.runtime, targetKey = JSON.stringify(target);
  const [published, publish] = useState<ShowRouteLease>();
  useLayoutEffect(() => {
    if (!context) return;
    const viewer = assembly.session().viewer;
    if (viewer.authority !== 'local' && viewer.authority !== 'hosted') return;
    const request = new AbortController();
    let live = true;
    const current = () => live && !request.signal.aborted && context.isCurrent();
    const original: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const binding = createShowBindings(original);
    const selection = createEpisodeSelection(original, binding,
      {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId},
      () => assembly.viewerFence, runtime);
    selection.start();
    publish({context, assembly, targetKey, runtime, current, binding, selection});
    const route: ShowWorkspaceRoute = JSON.parse(targetKey);
    void binding.select(route, request.signal).catch(error => {if (current()) context.effects.reportError(error);});
    return () => {
      live = false;request.abort();selection.dispose();binding.dispose();
    };
  }, [context, assembly, runtime, targetKey]);
  return context && published && published.context === context && published.assembly === assembly && published.targetKey === targetKey
    && published.runtime === runtime && published.current() ? published : undefined;
}
