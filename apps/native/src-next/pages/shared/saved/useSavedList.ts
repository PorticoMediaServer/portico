import {useEffect, useMemo, useSyncExternalStore, type RefObject} from 'react';
import type {View} from 'react-native';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {ContentEntry} from '../../../../../../packages/client-core/src/library-content';
import type {SavedListFilter} from '../../../../../../packages/client-core/src/saved';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeEntryMenuState} from '../../../screens/destinations/entry-actions';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import {savedTitleCount} from '../../../screens/saved/presentation';
import type {NativeSavedViewDestination} from '../../../screens/saved/view-model';
import type {NativeSavedSelectionPort} from './useSavedPage';

const t = defaultI18n.t;

type ListView = 'watchlist' | 'favorites' | 'history';
export type NativeSavedListRoute = Readonly<{view: ListView; sort?: string; direction?: 'asc' | 'desc'; filter?: SavedListFilter; period?: '24h' | '7d' | '30d' | '90d' | 'all'; shape?: 'posters' | 'squares'}>;

export type NativeSavedListProps = Readonly<{
  route: NativeSavedListRoute;
  destination: NativeSavedViewDestination;
  activation: NativeDestinationActivationPort;
  runtime: NativeShellRuntime;
  actions: NativeListActions & Readonly<{menuState?: NativeEntryMenuState}>;
  back: () => void;
  selection?: NativeSavedSelectionPort;
  inline?: boolean;
}>;

const emptyCopy: Readonly<Record<ListView, Readonly<{title: MessageId; body: MessageId}>>> = Object.freeze({
  watchlist: {title: 'web.saved.empty.watchlist', body: 'web.saved.empty.watchlistBody'},
  favorites: {title: 'web.saved.empty.favorites', body: 'web.saved.empty.favoritesBody'},
  history: {title: 'web.saved.empty.history', body: 'web.saved.empty.historyBody'},
});

export type NativeListActions = Readonly<{open: (entry: ContentEntry) => void; play: (entry: ContentEntry) => void; menu: (entry: ContentEntry, anchorRef?: RefObject<View | null>, select?: () => void) => void; isMenuOpen?: (entry: ContentEntry) => boolean}>;

export function useSavedList({route, destination, activation, runtime, actions, selection}: NativeSavedListProps) {
  useSyncExternalStore(selection?.subscribe ?? (() => () => {}), selection?.getSnapshot ?? (() => 0));
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const snapshot = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  useEffect(() => {void destination.select(route).catch(runtime.reportError);}, [destination, route, runtime]);
  const personal = snapshot.personal, saved = snapshot.saved;
  const projection = saved?.projection, entries: readonly ContentEntry[] = route.view === 'history'
    ? (personal?.entries ?? []).flatMap(entry => !entry.hidden && entry.media ? [entry.media] : [])
    : (projection?.sections ?? []).flatMap(section => section.entries.flatMap(entry => entry.kind === 'playlist' || entry.kind === 'playlist_entry' ? [] : [entry]));
  const error = snapshot.error ?? saved?.error ?? personal?.error;
  const loading = (snapshot.phase === 'idle' || snapshot.phase === 'loading') && !projection && !personal?.entries.length;
  const sorts = useMemo(() => projection?.sorts ?? [], [projection?.sorts]), filters = projection?.filters ?? [];
  const title = t(route.view === 'watchlist' ? 'saved.tab.watchlist' : route.view === 'favorites' ? 'saved.tab.favorites' : 'saved.tab.history');
  const count = route.view === 'history' ? personal?.entries.length : (projection?.sections[0]?.totalCount ?? entries.length);
  const retry = () => {void destination.retry().catch(runtime.reportError);};
  const chooseFilter = (filter: SavedListFilter) => {
    if (route.view === 'history' || !activation.getSnapshot().visible || destination.getSnapshot().saved?.projection !== projection) return;
    void destination.select({...route, filter,
      ...(projection?.query.sort ? {sort: projection.query.sort, direction: projection.query.direction as 'asc' | 'desc'} : {})}).catch(runtime.reportError);
  };
  const chooseSort = (sort: string, direction: 'asc' | 'desc') => {
    if (!activation.getSnapshot().visible || destination.getSnapshot().saved?.projection !== projection) return;
    void destination.select({...route, sort, direction,
      ...(projection?.query.category ? {filter: projection.query.category as SavedListFilter} : {})}).catch(runtime.reportError);
  };
  const hasMore = route.view === 'history' ? !!personal?.nextCursor : projection?.sections.some(section => section.nextCursor) === true;
  const next = () => {
    const current = destination.getSnapshot();
    if (!hasMore || !activation.getSnapshot().visible || current.phase !== 'ready'
      || (route.view === 'history' ? current.personal !== personal : current.saved?.projection !== projection)) return;
    void destination.next(projection?.sections.find(section => section.nextCursor)?.id).catch(runtime.reportError);
  };
  return {active, phase: snapshot.phase, projection, entries, error, loading, sorts, filters, title,
    count: savedTitleCount(count), empty: emptyCopy[route.view], retry, chooseFilter, chooseSort, hasMore, next};
}
