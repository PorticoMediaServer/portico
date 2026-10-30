import {forwardIdentity} from '../detail/context';
import {useCallback, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {useLocation, useSearch} from '@tanstack/react-router';
import {randomId} from '@core/random-id.ts';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import type {ScreenContext} from '../detail/context';
import {DetailLoading} from '../detail/Loading';
import {createSearchBindings, type SearchBindings} from './bindings';
import {createSearchSelection, type SearchSelection} from './selection';
import {SearchScreen} from './Search';

type SearchLocation = typeof router.state.location;
type SearchOpening = Readonly<{
  id: string; assembly: ProtectedLibraryScreen['context']; scopeKey: string;
  location: SearchLocation; linkedQuery?: string; accepts: (location: SearchLocation) => boolean;
}> & {consumed: boolean};
type SearchLease = Readonly<{
  context: ScreenContext; assembly: ProtectedLibraryScreen['context']; runtime: NonNullable<ProtectedLibraryScreen['binding']['playback']>['runtime'] | undefined;
  binding: SearchBindings; selection: SearchSelection; current: () => boolean;
}>;
const echoField = 'porticoSearchOpening';

/** A linked navigation is an intent, even when its text matches a previous
 * visit. Only its exact own replace echo can keep the opening after consumption.
 * History stores an opaque marker, never the consumed query or a result model. */
function useSearchOpening(captured: ProtectedLibraryScreen, location: SearchLocation, linkedQuery: string | undefined) {
  const assembly = captured.context, scopeKey = captured.binding.scopeKey;
  const [published, publish] = useState<SearchOpening>();
  const held = published?.assembly === assembly && published.scopeKey === scopeKey && published.accepts(location) ? published : undefined;
  useLayoutEffect(() => {
    if (held) return;
    const id = randomId();
    const opening: SearchOpening = {id, assembly, scopeKey, location, linkedQuery, consumed: false,
      accepts: value => value.pathname === '/search' && (value === location || opening.consumed && value.search.q === ''
        && value.state.__TSR_index === location.state.__TSR_index
        && (value.state as typeof value.state & Readonly<{porticoSearchOpening?: unknown}>)[echoField] === id),
    };
    publish(opening);
  }, [held, assembly, scopeKey, location, linkedQuery]);
  return held;
}

/** Constructors, subscriptions and terminal resources belong to a committed
 * original context/runtime. Query/group updates never recreate this session. */
function useSearchLease(context: ScreenContext | undefined, captured: ProtectedLibraryScreen) {
  const assembly = captured.context, runtime = captured.binding.playback?.runtime;
  const [published, publish] = useState<SearchLease>();
  useLayoutEffect(() => {
    if (!context?.isCurrent()) return;
    const viewer = assembly.session().viewer;
    if (viewer.authority !== 'local' && viewer.authority !== 'hosted') return;
    let live = true;
    const current = () => live && context.isCurrent(), original: ScreenContext = forwardIdentity(context, {isCurrent: current});
    const binding = createSearchBindings(original);
    const selection = createSearchSelection(original, binding.results,
      {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId},
      () => assembly.viewerFence, runtime);
    selection.start();
    // The reviewed screen may replay its passive cancellation after this
    // opening retires. It must not call a terminal service or revive a query.
    const service = binding.service, cancel = () => {if (current()) service.cancel();};
    const methods = new Map<PropertyKey, Readonly<{original: unknown; bound: unknown}>>();
    const screenService = new Proxy(service, {get(target, property) {
      if (property === 'cancel') return cancel;
      const value = Reflect.get(target, property, target);
      if (typeof value !== 'function') return value;
      const held = methods.get(property);
      if (held && held.original === value) return held.bound;
      const bound = value.bind(target);methods.set(property, {original: value, bound});return bound;
    }});
    publish({context, assembly, runtime, binding: {...binding, service: screenService}, selection, current});
    return () => {live = false;selection.dispose();binding.dispose();};
  }, [context, assembly, runtime]);
  return context && published && published.context === context && published.assembly === assembly && published.runtime === runtime
    && published.current() ? published : undefined;
}

function OpenSearch({captured, opening}: {captured: ProtectedLibraryScreen; opening: SearchOpening}) {
  const context = useCommittedLibraryScreenContext(captured, opening.id, () => opening.accepts(router.state.location));
  const owned = useSearchLease(context, captured);
  return owned ? <SearchSession owned={owned} opening={opening}/> : <DetailLoading/>;
}
function SearchSession({owned, opening}: {owned: SearchLease; opening: SearchOpening}) {
  const {binding, selection} = owned, location = useLocation({select: value => value});
  const query = useSyncExternalStore(binding.service.subscribe, binding.service.getSnapshot, binding.service.getSnapshot);
  const onNow = useSyncExternalStore(binding.onNow.subscribe, binding.onNow.getSnapshot, binding.onNow.getSnapshot);
  const consume = useCallback(() => {
    if (!owned.current() || opening.consumed || !opening.linkedQuery || location !== opening.location
      || router.state.location !== location || location.search.q !== opening.linkedQuery || !opening.accepts(location)) return;
    // Before navigation or another effect replay can run, this exact rendered
    // admission is consumed. A retained callback cannot clear a successor link.
    opening.consumed = true;
    void router.navigate({to: '/search', search: {q: ''}, replace: true, resetScroll: false,
      state: previous => ({...previous, [echoField]: opening.id})}).catch(error => {if (owned.current()) binding.context.effects.reportError(error);});
  }, [owned, opening, location, binding]);
  const openLive = useCallback((sourceId?: string) => {
    if (!owned.current() || binding.service.getSnapshot() !== query || binding.onNow.getSnapshot() !== onNow
      || query.query?.q.trim() || onNow.phase !== 'ready' || !onNow.cards.length
      || sourceId !== undefined && !onNow.cards.some(card => card.channel.sourceId === sourceId)) return;
    void router.navigate({to: '/live', search: sourceId ? {sourceId} : {}}).catch(error => {if (owned.current()) binding.context.effects.reportError(error);});
  }, [owned, binding, query, onNow]);
  return <SearchScreen binding={binding} selection={selection} linkedQuery={opening.consumed ? undefined : opening.linkedQuery}
    onOpenLive={openLive} onConsumeLinkedQuery={consume}/>;
}
function BoundSearch({captured}: {captured: ProtectedLibraryScreen}) {
  const location = useLocation({select: value => value}), search = useSearch({from: '/search'});
  const linkedQuery = typeof search.q === 'string' && search.q.length ? search.q : undefined;
  const opening = useSearchOpening(captured, location, linkedQuery);
  return opening ? <OpenSearch captured={captured} opening={opening}/> : <DetailLoading/>;
}
function SearchRouteScreen() {
  const captured = useProtectedWebScreenContext();
  return captured ? <BoundSearch captured={captured}/> : null;
}
export default defineWebScreen({id: 'search', component: SearchRouteScreen});
