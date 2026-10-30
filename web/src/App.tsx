import {RouterProvider} from '@tanstack/react-router';
import {useEffect, useSyncExternalStore} from 'react';
import {IconSprite} from './kit/Icon';
import {FreshnessProvider} from './platform/freshness';
import {EntryMetadataHost} from './shell/entry-metadata';
import {EntryActionHost} from './shell/entry-actions.tsx';
import {bindWebAccountEntry} from './route-registry-lifecycle';
import {WebScreenProvider, webScreens} from './route-registry-view';
import type {ShellAssemblyContext} from './shell/bootstrap';
import {router, serverScope} from './router';
import {bindServerScope} from './shell/server-scope-binding';
import {LastResortBoundary} from './shell/Errors';
import {ShellBindingsProvider, type ShellBindings} from './shell/bindings';
import {bindShellRouteMotion} from './shell/route-motion';
import {ShellBootstrapState} from './shell/BootstrapState';
import type {ShellBootstrap} from './shell/bootstrap';
import {PlaybackHost} from './shell/Playback';
import type {TakeAccountReturn} from './route-registry-return';
import type {TakeAccountMove} from './route-registry-arrival';
export function App({binding, bootstrap, takeAccountReturn, takeAccountMove}: {binding?: ShellBindings; bootstrap?: ShellBootstrap; takeAccountReturn?: TakeAccountReturn; takeAccountMove?: TakeAccountMove} = {}) {
  // A failure in the app itself has no shell left to draw; keep the last-resort page outside every provider.
  return <LastResortBoundary>{bootstrap ? <RestoredApp bootstrap={bootstrap} takeAccountReturn={takeAccountReturn} takeAccountMove={takeAccountMove} /> : <BoundApp binding={binding} takeAccountReturn={takeAccountReturn} takeAccountMove={takeAccountMove} />}</LastResortBoundary>;
}
function RestoredApp({bootstrap, takeAccountReturn, takeAccountMove}: {bootstrap: ShellBootstrap; takeAccountReturn?: TakeAccountReturn; takeAccountMove?: TakeAccountMove}) {
  const snapshot = useSyncExternalStore(bootstrap.subscribe, bootstrap.getSnapshot, bootstrap.getSnapshot);
  useEffect(() => bindWebAccountEntry(router, bootstrap), [bootstrap]);
  useEffect(() => bindServerScope(router as unknown as Parameters<typeof bindServerScope>[0], bootstrap, serverScope), [bootstrap]);
  return <ShellBootstrapState value={{snapshot, retry: bootstrap.start, retryNow: bootstrap.retryNow}}><BoundApp binding={snapshot.binding} context={snapshot.context} bootstrap={bootstrap} takeAccountReturn={takeAccountReturn} takeAccountMove={takeAccountMove} /></ShellBootstrapState>;
}
function BoundApp({binding, context, bootstrap, takeAccountReturn, takeAccountMove}: {binding?: ShellBindings; context?: ShellAssemblyContext; bootstrap?: ShellBootstrap; takeAccountReturn?: TakeAccountReturn; takeAccountMove?: TakeAccountMove}) {
  useEffect(() => bindShellRouteMotion(router), []);
  const MediaHost = webScreens.get('player')?.playbackHost ?? PlaybackHost;
  const viewer = binding?.shell.identity.viewer;
  // The player host renders a retained title body, which reads freshness, from
  // outside every route. One provider covers the router and the host together;
  // a second provider would detach the first one's feed when either unmounts.
  const content = <>
    <IconSprite />
    <div data-playback-route><ShellBindingsProvider value={binding}><RouterProvider router={router} /></ShellBindingsProvider></div>
    <MediaHost binding={binding?.playback} />
    <EntryActionHost />
    <EntryMetadataHost />
  </>;
  return <WebScreenProvider value={{binding, context, bootstrap, takeAccountReturn, takeAccountMove}}>
    {viewer && binding ? <FreshnessProvider key={binding.scopeKey} api={binding.api} events={binding.events} serverId={viewer.serverId} viewerId={JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])}>{content}</FreshnessProvider> : content}
  </WebScreenProvider>;
}
