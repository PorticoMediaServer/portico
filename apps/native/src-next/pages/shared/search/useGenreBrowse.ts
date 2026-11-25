import {useEffect, useMemo, useState, useSyncExternalStore} from 'react';
import type {BrowseSortSelection} from '../../../../../../packages/client-core/src/browse';
import {browseNameId} from '../../../../../../packages/client-core/src/presentation/browse-toolbar';
import {browseToolbarModel} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {searchGenreBrowsePivot, searchGenreBrowseRequest} from '../../../../../../packages/client-core/src/search-genres';
import {defaultI18n, type MessageId} from '../../../../../../packages/i18n/src';
import type {BrowseLabels} from '../../../kit/browse';
import {useTheme} from '../../../kit/theme';
import type {NativeShellRuntime} from '../../../shell/runtime';
import type {NativeDestinationActivationPort} from '../../../screens/destinations/focus';
import type {createNativeEntryActions} from '../../../screens/destinations/entry-actions';
import type {NativeLibraryFilterDraft} from '../../../screens/library/filters';
import type {NativeGenreBrowseDestination} from '../../../screens/search/genre-browse';
import {browseFailure} from '../library/useLibrary';

export type GenreBrowseProps = Readonly<{
  focusScope: string; name: string; value: string; destination: NativeGenreBrowseDestination; activation: NativeDestinationActivationPort;
  runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>; owner: boolean; onBack: () => void;
  /** The retained route this page belongs to, for the touch shell's native stack. */
  stackRoute?: object;
}>;

const label = (value: {id: string; labelKey?: string}, t: NativeShellRuntime['text']['t']) => {
  const key = browseNameId(value.id, value.labelKey, defaultI18n.has);
  if (!key) throw Object.assign(new Error('native_browse_copy_unavailable'), {code: 'invalid_response'});
  return t(key as MessageId);
};

/** A genre from Search's Browse by genre (a genuine across-video Browse source): its read, the toolbar's sort and
 * filters over it, the count, and the filters sheet's draft while it is open. No Library('*') or local genre expansion. */
export function useGenreBrowse({value, destination, activation, runtime, owner}: GenreBrowseProps) {
  const {isTV} = useTheme(), t = runtime.text.t, locale = runtime.text.locale;
  const labels = useMemo((): BrowseLabels => ({field: item => label(item, t), sort: item => label(item, t), quick: item => label(item, t)}), [t]);
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot), active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const [filters, setFilters] = useState<NativeLibraryFilterDraft>(), [elapsed, setElapsed] = useState(0);
  const fixed = useMemo(() => {const query = searchGenreBrowseRequest(value).query; return query ? [query] : [];}, [value]);
  useEffect(() => {if (active.visible && state.phase === 'idle') void destination.load();}, [active.visible, destination, state.phase]);
  useEffect(() => {if (!active.visible) {filters?.dispose(); setFilters(undefined);}}, [active.visible, filters]);
  useEffect(() => () => filters?.dispose(), [filters]);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now();
    const initial = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    const slow = setTimeout(() => setElapsed(Math.max(3001, Date.now() - start)), 3001);
    return () => {clearTimeout(initial); clearTimeout(slow);};
  }, [state.phase]);
  const ready = state.phase === 'ready' && !!state.capabilities && !!state.sort && !!state.collection;
  const model = useMemo(() => ready ? browseToolbarModel(state.capabilities!, state.capabilities!.library.kind, state.predicates, state.sort!, {t, locale, textEntry: !isTV, owner}) : undefined,
    [ready, owner, t, locale, isTV, state.capabilities, state.predicates, state.sort]);
  const collection = state.collection;
  const page = useSyncExternalStore(collection?.subscribe ?? noSubscribe, collection?.getSnapshot ?? noSnapshot);
  const onSort = (selection: BrowseSortSelection) => {
    const capability = state.capabilities?.sorts.find(sort => sort.id === selection.field);
    destination.setQuery(state.predicates, capability?.seeded && selection.seed === undefined ? {...selection, seed: Math.floor(Math.random() * 4294967296)} : selection);
  };
  // A quick chip (Unwatched, In progress) is one more predicate beside the genre's own.
  const onQuickChange = (id: string, checked: boolean) => {
    const quick = model?.quick.find(value => value.id === id);
    if (!quick || !state.sort) return;
    const remaining = state.predicates.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query));
    destination.setQuery(checked ? [...remaining, quick.query] : remaining, state.sort);
  };
  const reshuffle = () => {if (state.sort) onSort({...state.sort, seed: Math.floor(Math.random() * 4294967296)});};
  const total = page && 'total' in page ? page.total : undefined;
  const count = total === undefined ? undefined : t(page && 'totalIsLowerBound' in page && page.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: total, pivot: searchGenreBrowsePivot});
  return {t, labels, state, active, ready, model, collection, count, total, elapsed, fixed, filters, onSort, onQuickChange, reshuffle,
    openFilters: () => {if (active.visible) setFilters(destination.createFilters());}, closeFilters: () => setFilters(undefined),
    failure: state.phase === 'error' ? browseFailure(runtime.error(state.error, destination.retry)) : undefined,
    gridFailure: page && 'status' in page && page.status === 'error' ? runtime.error(page.error, destination.retry) : undefined};
}

const noSubscribe = () => () => {};
const noSnapshot = () => undefined;
