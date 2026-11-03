import {forwardIdentity} from '../detail/context';
import {useCallback, useLayoutEffect, useState, useSyncExternalStore} from 'react';
import {useSearch} from '@tanstack/react-router';
import {defineWebScreen} from '../../route-registry';
import {useProtectedWebScreenContext} from '../../route-registry-view';
import {router} from '../../router';
import {useCommittedLibraryScreenContext, type ProtectedLibraryScreen} from '../browse/screen-context';
import type {ScreenContext} from '../detail/context';
import {DetailLoading} from '../detail/Loading';
import {createSearchBindings, type SearchBindings} from './bindings';
import {createSearchSelection, type SearchSelection} from './selection';
import {SearchScreen, type SearchAddress} from './Search';
import {searchQuery} from '../../shell/navigation';

type SearchLease = Readonly<{
  context: ScreenContext; assembly: ProtectedLibraryScreen['context']; runtime: NonNullable<ProtectedLibraryScreen['binding']['playback']>['runtime'] | undefined;
  binding: SearchBindings; selection: SearchSelection; current: () => boolean;
}>;

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
    const binding = createSearchBindings(original, assembly.freshness.queries);
    const selection = createSearchSelection(original, binding.results,
      {serverId: viewer.serverId, authority: viewer.authority, accountId: viewer.accountId, profileId: viewer.profileId},
      runtime);
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

function SearchSession({owned, search}: {owned: SearchLease; search: SearchAddress}) {
  const {binding, selection} = owned;
  const query = useSyncExternalStore(binding.service.subscribe, binding.service.getSnapshot, binding.service.getSnapshot);
  const onNow = useSyncExternalStore(binding.onNow.subscribe, binding.onNow.getSnapshot, binding.onNow.getSnapshot);
  // The address holds the settled search: typing replaces it, so Back returns to these results.
  const settle = useCallback((next: SearchAddress) => {
    if (!owned.current() || router.state.location.pathname !== '/search') return;
    const held = router.state.location.search as SearchAddress;
    if ((held.q ?? '') === next.q && held.group === next.group) return;
    void router.navigate({to: '/search', search: {q: next.q, ...(next.group ? {group: next.group} : {})}, replace: true, resetScroll: false})
      .catch(error => {if (owned.current()) binding.context.effects.reportError(error);});
  }, [owned, binding]);
  const openLive = useCallback((sourceId?: string) => {
    if (!owned.current() || binding.service.getSnapshot() !== query || binding.onNow.getSnapshot() !== onNow
      || query.query?.q.trim() || onNow.phase !== 'ready' || !onNow.cards.length
      || sourceId !== undefined && !onNow.cards.some(card => card.channel.sourceId === sourceId)) return;
    void router.navigate({to: '/live', search: sourceId ? {sourceId} : {}}).catch(error => {if (owned.current()) binding.context.effects.reportError(error);});
  }, [owned, binding, query, onNow]);
  return <SearchScreen binding={binding} selection={selection} address={search} onSettle={settle} onOpenLive={openLive}/>;
}
function BoundSearch({captured}: {captured: ProtectedLibraryScreen}) {
  const search = searchQuery(useSearch({from: '/search'}));
  const context = useCommittedLibraryScreenContext(captured, 'search', () => router.state.location.pathname === '/search');
  const owned = useSearchLease(context, captured);
  return owned ? <SearchSession owned={owned} search={{q: search.q, ...(search.group ? {group: search.group} : {})}}/> : <DetailLoading/>;
}
function SearchRouteScreen() {
  const captured = useProtectedWebScreenContext();
  return captured ? <BoundSearch captured={captured}/> : null;
}
export default defineWebScreen({id: 'search', component: SearchRouteScreen});
