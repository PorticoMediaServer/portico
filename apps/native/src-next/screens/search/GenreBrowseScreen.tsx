import {useNativeStackHosted, useNativeStackPlacement} from '../../kit/navigation/stack-slot';
import React, {useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {BrowseSortSelection} from '../../../../../packages/client-core/src/browse';
import {browseNameId} from '../../../../../packages/client-core/src/presentation/browse-toolbar';
import {browseToolbarModel} from '../../../../../packages/client-core/src/presentation/kit-model';
import {searchGenreBrowsePivot, searchGenreBrowseRequest} from '../../../../../packages/client-core/src/search-genres';
import {defaultI18n, type MessageId} from '../../../../../packages/i18n/src';
import {FiltersButton, type BrowseLabels} from '../../kit/browse';
import {Focusable} from '../../kit/controls';
import {Icon} from '../../kit/Icon';
import {WindowedGrid, type WindowedGridHandle, type LayoutItemContext} from '../../kit/layout';
import {GridSkeleton, StateView} from '../../kit/states';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import type {createNativeEntryActions} from '../destinations/entry-actions';
import {LibrarySortMenu} from '../library/LibrarySortMenu';
import type {NativeLibraryFilterDraft} from '../library/filters';
import {nativeLibraryEntryKey} from '../library/model';
import {homeCardText} from '../home/model';
import {PosterCard} from '../../kit/cards';
import type {NativeGenreBrowseDestination} from './genre-browse';
import {GenreFilters} from './GenreFilters';
import {StandardBackdrop} from '../../kit/artwork';

const label = (value: {id: string; labelKey?: string}, t: NativeShellRuntime['text']['t']) => {
  const key = browseNameId(value.id, value.labelKey, defaultI18n.has);
  if (!key) throw Object.assign(new Error('native_browse_copy_unavailable'), {code: 'invalid_response'});
  return t(key as MessageId);
};
const browseLabels = (t: NativeShellRuntime['text']['t']): BrowseLabels => ({field: value => label(value, t), sort: value => label(value, t), quick: value => label(value, t)});

/** A genuine across-video Browse source. The header/filter/grid parts are the
 * current Library composition; no Library('*') or local genre expansion exists. */
export function GenreBrowseScreen({focusScope, name, value, destination, activation, runtime, actions, owner, onBack, stackRoute}: Readonly<{
  focusScope: string; name: string; value: string; destination: NativeGenreBrowseDestination; activation: NativeDestinationActivationPort;
  runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>; owner: boolean; onBack: () => void;
  /** The retained route this page belongs to, for the touch shell's native stack. */
  stackRoute?: object;
}>) {
  const t = runtime.text.t;
  const labels = useMemo(() => browseLabels(t), [t]);
  const theme = useTheme(), dimensions = useWindowDimensions(), insets = useSafeAreaInsets();
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot), active = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const [filters, setFilters] = useState<NativeLibraryFilterDraft>(), [elapsed, setElapsed] = useState(0);
  const place = useNativeStackPlacement(stackRoute), stacked = useNativeStackHosted();
  const fixed = useMemo(() => {const query = searchGenreBrowseRequest(value).query; return query ? [query] : [];}, [value]);
  useEffect(() => {if (active.visible && state.phase === 'idle') void destination.load();}, [active.visible, destination, state.phase]);
  useEffect(() => {if (!active.visible) {filters?.dispose(); setFilters(undefined);}}, [active.visible, filters]);
  useEffect(() => () => filters?.dispose(), [filters]);
  useEffect(() => {
    if (state.phase !== 'loading') {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [state.phase]);
  return place(<View testID="native-search-genre" pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} style={{position: 'absolute', inset: 0, display: active.visible || stacked ? 'flex' : 'none'}}><StandardBackdrop/>
    <View style={{paddingTop: insets.top, backgroundColor: theme.colors.ink}}>
      <View style={{height: 44, paddingHorizontal: 12, justifyContent: 'center'}}><Focusable testID="search-genre-back" accessibilityLabel={t('action.back')} onPress={onBack}
        style={{width: 38, height: 38, justifyContent: 'center', alignItems: 'center'}}><Icon name="i-left" size={22} color={theme.colors.text}/></Focusable></View>
      <Text accessibilityRole="header" style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text, marginHorizontal: 20, marginBottom: 8}]}>{name}</Text>
    </View>
    {state.phase === 'ready' && state.capabilities && state.sort && state.collection ? <GenreGrid focusScope={focusScope} destination={destination} runtime={runtime} actions={actions} owner={owner} labels={labels}
      openFilters={() => {if (active.visible) setFilters(destination.createFilters());}} filtersOpen={!!filters}/>
      : state.phase === 'error' ? <StateView variant="centered" {...runtime.error(state.error, destination.retry)}/>
      : <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={dimensions.height - 160} width={dimensions.width} totalCount={9} columns={3}/>}
    {filters && state.capabilities && state.sort && <GenreFilters draft={filters} capabilities={state.capabilities} sort={state.sort} fixed={fixed} runtime={runtime} owner={owner} labels={labels} onClose={() => setFilters(undefined)}/>}
  </View>);
}

function GenreGrid({focusScope, destination, runtime, actions, owner, labels, openFilters, filtersOpen}: Readonly<{
  focusScope: string; destination: NativeGenreBrowseDestination; runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>; owner: boolean; labels: BrowseLabels; openFilters: () => void; filtersOpen: boolean;
}>) {
  const t = runtime.text.t, locale = runtime.text.locale;
  const theme = useTheme(), dimensions = useWindowDimensions(), source = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const collection = source.collection!, state = useSyncExternalStore(collection.subscribe, collection.getSnapshot), grid = useRef<WindowedGridHandle>(null);
  useSyncExternalStore(actions.menuState.subscribe, actions.menuState.getSnapshot);
  const model = useMemo(() => browseToolbarModel(source.capabilities!, source.capabilities!.library.kind, source.predicates, source.sort!, {t, locale, textEntry: true, owner}),
    [owner, t, locale, source.capabilities, source.predicates, source.sort]);
  const [first, setFirst] = useState(destination.getMemory().firstVisibleIndex), offset = useRef(destination.getMemory().offset);
  const width = theme.platform === 'phone' ? (dimensions.width - 16 - 30 - 20) / 3 : 148;
  const pageSubscribe = useCallback((listener: () => void) => collection.subscribePage(collection.pageOf(first), listener), [collection, first]);
  const pageVersion = useCallback(() => collection.getPageVersion(collection.pageOf(first)), [collection, first]);
  useSyncExternalStore(pageSubscribe, pageVersion);
  const onSort = (value: BrowseSortSelection) => {
    const capability = source.capabilities!.sorts.find(sort => sort.id === value.field);
    destination.setQuery(source.predicates, capability?.seeded && value.seed === undefined ? {...value, seed: Math.floor(Math.random() * 4294967296)} : value);
  };
  useEffect(() => {if (source.queryRevision > 0) grid.current?.scrollToOffset(destination.getMemory().offset);}, [destination, source.queryRevision]);
  return <>
    <ScrollView horizontal showsHorizontalScrollIndicator={false} style={{flexGrow: 0}} contentContainerStyle={{paddingHorizontal: 20, paddingTop: 10, gap: 8}}>
      <FiltersButton count={model.applied.length} open={filtersOpen} onPress={openFilters}/>
      <LibrarySortMenu focusScope={focusScope} sorts={model.sorts} value={model.sort} label={labels.sort} translate={t} onValueChange={onSort}
        onReshuffle={() => onSort({...source.sort!, seed: Math.floor(Math.random() * 4294967296)})}/>
    </ScrollView>
    <Text style={[browseType(theme, 13, 600), {color: theme.colors.textTertiary, paddingHorizontal: 20, paddingVertical: 10}]}>
      {state.total === undefined ? null : t(state.totalIsLowerBound ? 'lib.countAtLeast' : 'lib.count', {count: state.total, pivot: searchGenreBrowsePivot})}
    </Text>
    {state.status === 'error' && <StateView {...runtime.error(state.error, destination.retry)}/>}
    <WindowedGrid ref={grid} style={{flex: 1}} collection={collection} getKey={nativeLibraryEntryKey} itemWidth={width} itemHeight={width * 1.5 + 44} captionHeight={44} gap={10} rowGap={16} gutter={16} rightGutter={30}
      paddingTop={12} paddingBottom={24} initialIndex={destination.getMemory().firstVisibleIndex}
      onVisibleRange={start => {setFirst(start); destination.remember(start, offset.current);}}
      onScrollOffset={value => {offset.current = value; destination.remember(first, value);}}
      renderPlaceholder={index => <View key={index} style={{width, height: width * 1.5, borderRadius: 10, backgroundColor: theme.colors.raised}}/>}
      renderItem={(entry, context) => <GenreCard entry={entry} context={context} width={width} runtime={runtime} actions={actions}/>}/>

  </>;
}

function GenreCard({entry, context, width, runtime, actions}: Readonly<{
  entry: ContentEntry; context: LayoutItemContext; width: number; runtime: NativeShellRuntime; actions: ReturnType<typeof createNativeEntryActions>;
}>) {
  const anchor = useRef<View>(null);
  return <PosterCard testID={`genre-card-${context.id}`} focusId={context.id} onFocus={context.onFocus} onBlur={context.onBlur}
    title={entry.displayTitle || entry.title} density="grid" width={width} artwork={runtime.artwork(entry.posterUrl)}
    caption={homeCardText(entry, false, runtime.text).caption} onNavigate={() => actions.open(entry)} onPlay={entry.playback ? () => actions.play(entry) : undefined}
    menuAnchorRef={anchor} moreExpanded={actions.isMenuOpen(entry)} onMore={() => actions.menu(entry, anchor)}/>;
}
