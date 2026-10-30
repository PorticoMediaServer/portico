import {subscribeFacts} from '../../shell/fact-source';
import {subscribePresentation, useShellFacts} from '../../shell/fact-hooks';
import {createEntryMetadataEffect, retireEntryMetadata, type EntryMetadataPorts} from '../../shell/entry-metadata';
import {createBrowserDownloadPort} from '../../platform/browser-download';
import {useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import type {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {backArtworkEntry, navigateArtworkEntry} from '../../shell/artwork-navigation';
import type {ShellBindings} from '../../shell/bindings';
import type {ScreenContext} from '../detail/context';
import {screenEntryDestination} from './navigation';
import {openWatchVersionChooser} from '../watch/version-presentation';
import {createScreenPlayEffect, createScreenSeasonPlayEffect, createScreenArtistPlayEffect, createScreenAlbumPlayEffect, createScreenRadioPlayEffect} from './playback-effect';
import {addToDestinationEffect, deleteTitleEffect, entryActions} from '../../shell/entry-actions';

export type ProtectedLibraryScreen = NonNullable<ReturnType<typeof useProtectedWebScreenContext>>;

function libraryScreenOwner(context: ProtectedLibraryScreen['context'], scopeKey: string, getBinding: () => ShellBindings, callerCurrent: () => boolean = () => true) {
  const session = context.session(), viewer = session.viewer;
  if (viewer.authority !== 'local' && viewer.authority !== 'hosted') throw new DOMException('Screen authority unavailable', 'AbortError');
  let active = true;
  const downloadLifetime = new AbortController();
  const downloadSignal = context.libraryRouteSignal ? AbortSignal.any([downloadLifetime.signal, context.libraryRouteSignal]) : downloadLifetime.signal;
  const principalCurrent = () => {
    if (!context.isCurrent() || getBinding().scopeKey !== scopeKey) return false;
    const value = context.session();
    return value.sessionFamilyId === session.sessionFamilyId && value.viewer.serverId === viewer.serverId && value.viewer.authority === viewer.authority
      && value.viewer.accountId === viewer.accountId && value.viewer.profileId === viewer.profileId && value.viewer.role === viewer.role;
  };
  const current = () => active && callerCurrent() && principalCurrent() && active && callerCurrent() && active;
  const assert = () => {if (!current()) throw new DOMException('Screen authority changed', 'AbortError');};
  const reportError = (error: unknown) => {if (current()) getBinding().home.effects.reportError(error);};
  const navigate = (action: Promise<void>) => {void action.catch(reportError);};
  const entryPorts: EntryMetadataPorts = {artwork: context.artwork,pickerIdentity: {authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId, events: context.events}, api: context.api, serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId]), isCurrent: current, requestId: async () => crypto.randomUUID()};
  const entryEffects = {...(viewer.authority === 'local' && viewer.role === 'owner' ? {editMetadata: createEntryMetadataEffect(entryPorts, () => current() && context.session().viewer.role === 'owner' && context.session().viewer.authority === 'local')} : {}), addToPlaylist: addToDestinationEffect(entryPorts, 'playlist'), addToCollection: addToDestinationEffect(entryPorts, 'collection', viewer.role === 'owner'), ...(viewer.role === 'owner' ? {deleteTitle: deleteTitleEffect(entryPorts)} : {})};
  const value: ScreenContext = {
    api: context.api, events: context.events, artwork: context.artwork, get identity() {return context.facts.identity;}, subscribeIdentity: listener => subscribeFacts(context, listener),
    scope: {serverId: viewer.serverId, viewerId: JSON.stringify([viewer.authority, viewer.accountId, viewer.profileId])}, isCurrent: current, libraryRouteSignal: context.libraryRouteSignal,
    preferences: {getSnapshot: () => context.preferences?.getSnapshot() ?? context.facts.preferences,
      subscribe: listener => subscribePresentation(context, listener)},
    effects: {
      download: createBrowserDownloadPort(context.api, current, downloadSignal),
      reportError,
      openEntry(entry) {
        assert();
        const destination = screenEntryDestination(entry);
        navigate(navigateArtworkEntry(entry, destination, scopeKey, current, principalCurrent, manual => router.navigate({...destination, ...(manual ? {viewTransition: false} : {})})));
      },
      openHome() {assert();navigate(router.navigate({to: '/'}));},
      openSettings() {assert();navigate(router.navigate({to: '/settings'}));},
      chooseProfile() {assert();navigate(router.navigate({to: '/profiles'}));},
      back() {assert();const history = router.history, back = history.back.bind(history);backArtworkEntry(scopeKey, current, back);},
      openBrowse(selector) {assert();navigate(router.navigate({to: '/library/$libraryId', params: {libraryId: selector.libraryId},
        search: {pivot: selector.pivot, query: selector.query, sort: selector.sort}}));},
      playSeason: createScreenSeasonPlayEffect({isCurrent: current, runtime: () => getBinding().playback?.runtime}),
      playArtist: createScreenArtistPlayEffect({isCurrent: current, runtime: () => getBinding().playback?.runtime}),
      playAlbum: createScreenAlbumPlayEffect({isCurrent: current, runtime: () => getBinding().playback?.runtime}),
      playRadio: createScreenRadioPlayEffect({isCurrent: current, runtime: () => getBinding().playback?.runtime}),
      play: createScreenPlayEffect({isCurrent: current, runtime: () => getBinding().playback?.runtime}),
      ...entryEffects,
      async chooseVersion(request) {
        assert();request.signal.throwIfAborted();
        await openWatchVersionChooser(context, {...request, isCurrent: () => current() && request.isCurrent()});
      },
    },
  };
  return {value, dispose: () => {active = false; downloadLifetime.abort(); entryActions.retire(entryPorts); retireEntryMetadata(entryPorts);}};
}

/** A current binding can gain its real media owner after Home arrives. The API,
 * viewer and reader remain captured to the original authority throughout. */
export function useLibraryScreenContext(captured: ProtectedLibraryScreen): ScreenContext {
  useShellFacts(captured.context);
  const binding = useRef<ShellBindings>(captured.binding);
  binding.current = captured.binding;
  const context = captured.context, scopeKey = captured.binding.scopeKey;
  const owner = useMemo(() => libraryScreenOwner(context, scopeKey, () => binding.current), [context, scopeKey]);
  useEffect(() => () => owner.dispose(), [owner]);
  return owner.value;
}

/** Fresh committed context for a route's original authority and runtime. Shell
 * publication also updates dock, unread and preference presentation; those
 * publications must not retire the route's controllers or drafts. Each owner
 * keeps the original binding, never adopts a later one, and is replaced only
 * when its authority, route or actual playback runtime changes. */
export function useCommittedLibraryScreenContext(captured: ProtectedLibraryScreen, routeKey: string, routeCurrent: () => boolean): ScreenContext | undefined {
  useShellFacts(captured.context);
  const context = captured.context, binding = captured.binding, scopeKey = binding.scopeKey, runtime = binding.playback?.runtime;
  const [published, publish] = useState<Readonly<{context: typeof context; scopeKey: string; runtime: typeof runtime; routeKey: string; owner: ReturnType<typeof libraryScreenOwner>}>>();
  useLayoutEffect(() => {
    const owner = libraryScreenOwner(context, scopeKey, () => binding, routeCurrent);
    publish({context, scopeKey, runtime, routeKey, owner});
    return owner.dispose;
    // binding and routeCurrent are captured for this tuple, never adopted later.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [context, scopeKey, runtime, routeKey]);
  return published?.context === context && published.scopeKey === scopeKey && published.runtime === runtime && published.routeKey === routeKey && published.owner.value.isCurrent()
    ? published.owner.value : undefined;
}
