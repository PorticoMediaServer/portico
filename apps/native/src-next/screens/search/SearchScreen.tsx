import React, {useCallback, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {FlatList, Keyboard, ScrollView, Text, View, useWindowDimensions, type TextInput} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {useSafeAreaInsets} from 'react-native-safe-area-context';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry, SearchGroup} from '../../../../../packages/client-core/src/search';
import {orderedSearchGroups, SEARCH_CHIPS, searchGroupLabel, topSearchResult, topResultKindLabel} from '../../../../../packages/client-core/src/presentation/search-model';
import {Artwork, StandardBackdrop} from '../../kit/artwork';
import {GenreTile, type CardSelection} from '../../kit/cards';
import {CardFrame} from '../../kit/cards/primitives';
import {Chip, Focusable, TextField} from '../../kit/controls';
import {SelectionCircle} from '../../kit/selection';
import {GridSkeleton, LoadingPaint, SkeletonBlock, StateView} from '../../kit/states';
import {Icon} from '../../kit/Icon';
import {Banner} from '../../kit/overlays';
import {browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {homeCardText} from '../home/model';
import {NativeSearchDestination, nativeSearchEntryKey} from './model';
import {SearchGridCard} from './SearchGridCard';
import {TVSearchScreen} from './TVSearchScreen';
import type {NativeDestinationActivationPort} from '../destinations/focus';
import type {NativeSearchActions, NativeSearchExplorePort, NativeSearchGenre, NativeSearchSelectionPort, NativeSearchTVCopy} from './types';
import {nativeSearchGenreTiles} from './genre-tiles';
import {channelTint} from '../../../../../packages/client-core/src/presentation/kit-model';

const media = (entry: SearchEntry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel';

export type NativeSearchScreenProps = Readonly<{
  focusScope?: string; destination: NativeSearchDestination; runtime: NativeShellRuntime;
  explore: NativeSearchExplorePort; actions: NativeSearchActions; selection?: NativeSearchSelectionPort;
  activation: NativeDestinationActivationPort; tvCopy: NativeSearchTVCopy;
}>;

/** Actual SearchService results and per-profile history. The shell owns authority lifetime. */
export function SearchScreen({focusScope, destination, runtime, explore, actions, selection, activation, tvCopy}: NativeSearchScreenProps) {
  const t = runtime.text.t;
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const theme = useTheme(), insets = useSafeAreaInsets(), dimensions = useWindowDimensions();
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot);
  const landing = useSyncExternalStore(explore.subscribe, explore.getSnapshot);
  const activeRoute = useSyncExternalStore(activation.subscribe, activation.getSnapshot);
  const scroll = useRef<ScrollView>(null), grid = useRef<FlatList<SearchEntry>>(null), input = useRef<TextInput>(null), focusedOnEntry = useRef(false), [focused, setFocused] = useState(false), [elapsed, setElapsed] = useState(0);
  // The shell's sidebar narrows this page; measure the content box instead of the window.
  const [surface, setSurface] = useState(dimensions.width);
  const groups = useMemo(() => orderedSearchGroups(state.groups), [state.groups]);
  const typed = state.text.trim().length > 0;
  const empty = typed && state.search.phase === 'ready' && state.groups.every(group => group.status === 'success' && group.totalCount === 0);
  const loading = state.search.phase === 'loading' && !state.groups.length;
  const top = state.group === 'all' ? topSearchResult(state.text, state.groups) : undefined;
  const run = useCallback((work: Promise<void>) => {void work.catch(runtime.reportError);}, [runtime]);
  const initialized = useRef(false);
  useEffect(() => {
    if (!activeRoute.visible) return;
    if (!initialized.current) {initialized.current = true; run(destination.refreshHistory());}
    run(explore.load());
  }, [activeRoute.visible, destination, explore, run]);
  useEffect(() => {
    // tablet10: choosing Search from the rail or sidebar opens the page with its field focused.
    if (!activeRoute.visible) {focusedOnEntry.current = false; return;}
    if (focusedOnEntry.current || theme.isTV || theme.platform === 'phone') return;
    focusedOnEntry.current = true;
    const frame = requestAnimationFrame(() => input.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [activeRoute.visible, theme.isTV, theme.platform]);
  useEffect(() => selection?.register(state.search, state.groups), [selection, state.groups, state.search]);
  useEffect(() => theme.isTV ? undefined : runtime.registerScrollToTop({kind: 'search'}, () => {
    scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion});
    grid.current?.scrollToOffset({offset: 0, animated: !theme.reduceMotion});
  }), [runtime, theme.isTV, theme.reduceMotion]);
  useEffect(() => {
    if (!loading) {setElapsed(0); return;}
    const start = Date.now(), timer = setTimeout(() => setElapsed(Math.max(300, Date.now() - start)), 300);
    return () => clearTimeout(timer);
  }, [loading]);
  const problem = state.problem === 'long' ? t('web.search.tooLong') : state.problem === 'words' ? t('web.search.tooManyWords') : state.problem ? t('web.search.tooShort') : undefined;
  const available = SEARCH_CHIPS.filter(chip => chip.id === 'all' || state.search.data?.capabilities.groups.some(group => group.id === chip.id && group.available))
    // One chip is selected: the answer covers that group alone, so the drawn row keeps its other types.
    .filter(chip => chip.id === 'all' || state.group !== 'all' || !state.groups.some(group => group.id === chip.id && group.status === 'success' && group.totalCount === 0));
  const chips = typed && !empty && !problem && state.search.data && <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{gap: 8, paddingHorizontal: 20, paddingTop: 12}}>
    {available.map(chip => {
      const group = state.groups.find(value => value.id === chip.id);
      const count = chip.id === 'all' ? state.groups.every(value => value.totalCountExact) ? state.groups.reduce((sum, value) => sum + value.totalCount, 0) : undefined : group?.totalCountExact ? group.totalCount : undefined;
      return <Chip key={chip.id} testID={`search-chip-${chip.id}`} selected={state.group === chip.id} count={count} retry={group?.status === 'error'} disabled={!!group && group.status === 'success' && group.totalCount === 0}
        onPress={() => destination.selectGroup(chip.id)}>{t(chip.label)}</Chip>;
    })}
  </ScrollView>;
  const fields = <View style={{paddingTop: Math.max(typed || focused ? 56 : 58, insets.top), backgroundColor: theme.colors.ink}}>
    {!typed && !focused && <Text accessibilityRole="header" style={[browseType(theme, 32, 800, -.035, 36.8), {color: theme.colors.text, marginHorizontal: 20, marginBottom: 12}]}>{t('web.shell.search')}</Text>}
    <View style={{flexDirection: 'row', alignItems: 'center', gap: 10, marginHorizontal: 16}}>
      <View style={{flex: 1}}><TextField ref={input} testID="search-input" focusId="search-input" variant="search" leadingIcon="i-search" value={state.text}
        cursorColor={theme.colors.accentStrong} selectionColor={theme.colors.accentStrong}
        onChangeText={destination.setText.bind(destination)} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
        accessibilityLabel={t('web.shell.search')} placeholder={t('web.search.placeholder')} returnKeyType="search" autoCapitalize="none" autoCorrect={false}
        onSubmitEditing={() => run(destination.commit())} fieldStyle={{height: 44, borderRadius: 12, paddingRight: typed ? 38 : 12}}/>
        {typed && <Focusable testID="search-clear" accessibilityLabel={t('web.search.clear')} onPress={() => destination.setText('')} hitSlop={12} focusRadius={10}
          style={{position: 'absolute', right: 12, top: 12, width: 20, height: 20, borderRadius: 10, backgroundColor: theme.colors.lineStrong, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-x" size={12} color={theme.colors.raised}/></Focusable>}
      </View>
      {(typed || focused) && <Focusable testID="search-cancel" accessibilityLabel={t('action.cancel')} onPress={() => {destination.setText(''); Keyboard.dismiss(); setFocused(false);}} style={{height: 44, justifyContent: 'center'}}><Text style={[browseType(theme, 15.5, 600), {color: theme.colors.textSecondary}]}>{t('action.cancel')}</Text></Focusable>}
    </View>{chips}
  </View>;
  const history = !!state.recent.length && <>
    <SectionHeading title={t('web.search.recent')} action={t('web.search.clearRecent')} disabled={state.clearing} onAction={() => run(destination.clearHistory())}/>
    {state.recent.map(recent => <Focusable key={recent.q} testID={`search-recent-${encodeURIComponent(recent.q)}`} accessibilityLabel={recent.q} onPress={() => destination.useRecent(recent.q)}
      style={{flexDirection: 'row', alignItems: 'center', gap: 12, height: 44, paddingHorizontal: 20}}>
      <Icon name="i-clock" size={19} color={theme.colors.textTertiary}/><Text numberOfLines={1} style={[browseType(theme, 15.5, 600), {color: theme.colors.textSecondary, flex: 1}]}>{recent.q}</Text>
    </Focusable>)}
  </>;
  const genreTiles = landing.phase === 'ready' ? nativeSearchGenreTiles(empty ? landing.backdropGenres ?? [] : landing.genres, empty ? 'phone-empty' : 'phone') : [];
  const genres = !genreTiles.length && landing.phase !== 'error' ? null : <>
    {!!genreTiles.length && <SectionHeading title={t(empty ? landing.emptyHeading : landing.initialHeading)}/>}
    {/* The genre shelf is optional: its failure is a quiet note, never the page's "Search couldn't finish" (P2-74 E-mobile-28). */}
    {landing.phase === 'error' && <View style={{paddingHorizontal: 16, paddingTop: 8}}><Banner placement="inline" variant="notice" tone="warning" icon="i-alert" message={t('next.search.genresUnavailable')}
      action={{label: t('next.states.tryAgain'), onPress: () => run(explore.load())}}/></View>}
    <View style={{paddingHorizontal: 16, paddingTop: 4, flexDirection: 'row', flexWrap: 'wrap', gap: 10}}>
      {genreTiles.map(genre => <SearchGenre key={genre.id} genre={genre} empty={empty} runtime={runtime} width={(surface - 42) / 2} onPress={() => explore.openGenre(genre)}/>)}
    </View>
  </>;
  const error = state.search.error && <StateView {...runtime.error(state.search.error, () => run(destination.retry()), 'search')}/>;
  const resultRows = groups.flatMap(group => group.items.filter(entry => !top || entry !== top.entry).slice(0, 3).map((entry, index) => ({entry, group: group.id, heading: index === 0})));
  const active = groups.find(group => group.id === state.group), wideSearch = theme.layoutMode === 'desktop' && !theme.isTV;
  if (theme.isTV) return <TVSearchScreen focusScope={focusScope} destination={destination} runtime={runtime} activation={activation} explore={explore} actions={actions} selection={selection} copy={tvCopy}/>;
  return <View testID="native-search-screen" pointerEvents={activeRoute.visible ? 'auto' : 'none'} accessibilityElementsHidden={!activeRoute.visible}
    importantForAccessibility={activeRoute.visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1}} onLayout={event => setSurface(event.nativeEvent.layout.width)}><StandardBackdrop/>
    {fields}
    {state.group === 'all' || empty || !typed ? <ScrollView ref={scroll} keyboardDismissMode="on-drag" keyboardShouldPersistTaps="handled" contentContainerStyle={{paddingBottom: 28}}>
      {problem && <StateView variant="search" title={problem} style={{margin: 20}}/>}
      {error}
      {empty && <StateView testID="search-no-matches" variant="search" title={t('next.states.noMatches', {query: state.text.trim()})} body={t('next.states.noMatchesBody')} style={{paddingTop: 26, marginHorizontal: 20}}/>}
      {(!typed || empty) && (empty ? <>{genres}{history}</> : <>{history}{genres}</>)}
      {!wideSearch && top && <SearchTopResult entry={top.entry} group={top.group} runtime={runtime} actions={actions} selection={selection?.item(top.entry, top.group)} width={surface - 32}/>}
      {wideSearch ? groups.filter(group => group.items.length).map(group => <View key={group.id}>
        <View style={{flexDirection: 'row', alignItems: 'baseline', gap: 8, marginHorizontal: 20, marginTop: 22, marginBottom: 10}}>
          <Text accessibilityRole="header" style={[browseType(theme, 20, 700, -.02), {color: theme.colors.text}]}>{t(searchGroupLabel(group.id))}</Text>
          {group.totalCountExact && <Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary}]}>{group.totalCount}</Text>}
        </View>
        <FlatList horizontal testID={`search-shelf-${group.id}`} data={group.items} showsHorizontalScrollIndicator={false}
          contentContainerStyle={{paddingHorizontal: 20}} ItemSeparatorComponent={() => <View style={{width: 14}}/>}
          keyExtractor={entry => nativeSearchEntryKey(group.id, entry)} initialNumToRender={7} maxToRenderPerBatch={10} windowSize={5}
          getItemLayout={(_, index) => {const length = Math.floor((surface - 40 - 6 * 14) / 7) + 14; return {length, offset: 20 + length * index, index};}}
          onEndReachedThreshold={10 * (Math.floor((surface - 40 - 6 * 14) / 7) + 14) / Math.max(1, surface)}
          onEndReached={() => {
            const page = state.paging?.[group.id];
            if (activeRoute.visible && state.search.phase === 'ready' && group.nextCursor && !page?.loading && !page?.error) run(destination.next(group.id));
          }}
          renderItem={({item}) => <SearchGridCard entry={item} group={group.id} runtime={runtime} actions={actions}
            selection={selection?.item(item, group.id)} width={Math.floor((surface - 40 - 6 * 14) / 7)}/>}
          ListFooterComponent={state.paging?.[group.id]?.loading ? <LoadingPaint ready={false} style={{marginLeft: 14}}
            skeleton={<View style={{flexDirection: 'row', gap: 14}}>{[0, 1, 2].map(index => <SkeletonBlock key={index}
              width={Math.floor((surface - 40 - 6 * 14) / 7)} height={Math.floor((surface - 40 - 6 * 14) / 7) * (group.id === 'movies' || group.id === 'shows' ? 1.5 : 1)}/>)}</View>}/>
            : state.paging?.[group.id]?.error ? <StateView {...runtime.error(state.paging?.[group.id]?.error, () => run(destination.retryGroup(group.id)), 'search')} style={{width: 240}}/> : null}/>
      </View>) : resultRows.map(({entry, group, heading}) => <React.Fragment key={nativeSearchEntryKey(group, entry)}>
        {heading && <Text accessibilityRole="header" style={[browseType(theme, 16, 700), {color: theme.colors.text, marginHorizontal: 20, marginTop: 18, marginBottom: 6}]}>{t(searchGroupLabel(group))}</Text>}
        <SearchResultRow entry={entry} group={group} runtime={runtime} actions={actions} selection={selection?.item(entry, group)}/>
      </React.Fragment>)}
      {loading && <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={Math.max(0, dimensions.height - 180)} width={surface} columns={3} totalCount={9}/>}
    </ScrollView> : <FlatList ref={grid} key={state.group} data={active?.items ?? []} numColumns={3} keyboardDismissMode="on-drag" keyboardShouldPersistTaps="handled"
      keyExtractor={entry => nativeSearchEntryKey(state.group as SearchGroup, entry)} contentContainerStyle={{padding: 16, gap: 16, paddingBottom: 28}}
      columnWrapperStyle={{gap: 10}} onEndReachedThreshold={.5} onEndReached={() => {if (active?.nextCursor && state.search.phase === 'ready') run(destination.next(active.id));}}
      renderItem={({item}) => <SearchGridCard entry={item} group={state.group as SearchGroup} runtime={runtime} actions={actions} selection={selection?.item(item, state.group as SearchGroup)} width={(surface - 52) / 3}/>}
      ListHeaderComponent={error || null} ListEmptyComponent={loading ? <GridSkeleton elapsedMs={elapsed} scrollOffset={0} viewportHeight={dimensions.height - 180} width={surface} columns={3} totalCount={9}/> : null}/>}
    {selection?.render(path => {const source = runtime.artwork(path); return source ? <Artwork source={source} width={48} height={48}/> : undefined;})}
  </View>;
}

function SectionHeading({title, action, disabled, onAction}: {title: string; action?: string; disabled?: boolean; onAction?: () => void}) {
  const theme = useTheme();
  return <View style={{flexDirection: 'row', alignItems: 'baseline', justifyContent: 'space-between', marginTop: 24, marginHorizontal: 20, marginBottom: 6}}>
    <Text accessibilityRole="header" style={[browseType(theme, 19, 700, -.015), {color: theme.colors.text}]}>{title}</Text>
    {action && onAction && <Focusable accessibilityLabel={action} disabled={disabled} onPress={onAction} hitSlop={12}><Text style={[browseType(theme, 14, 600), {color: theme.colors.textTertiary}]}>{action}</Text></Focusable>}
  </View>;
}

/** Ruling 36: phone 7 uses two covers; the no-match state 17a uses a backdrop. */
function SearchGenre({genre, empty, runtime, width, onPress}: {genre: NativeSearchGenre; empty: boolean; runtime: NativeShellRuntime; width: number; onPress: () => void}) {
  const t = runtime.text.t;
  const theme = useTheme(), gradient = useId();
  const tint = channelTint(genre.id);
  if (!empty) return <GenreTile testID={`search-genre-${encodeURIComponent(genre.id)}`} title={genre.name} variant="video" width={width}
    covers={genre.coverPaths.slice(0, 2).flatMap(path => {const source = runtime.artwork(path); return source ? [source] : [];})}
    palette={{first: tint, second: tint}} onNavigate={onPress}/>;
  return <Focusable testID={`search-genre-${encodeURIComponent(genre.id)}`} accessibilityLabel={genre.name} onPress={onPress} focusRadius={12} style={{width, height: 84, borderRadius: 12, overflow: 'hidden', backgroundColor: theme.colors.raised}}>
    <Artwork source={runtime.artwork(genre.backdropPath)} width={width} height={84}/>
    <Svg pointerEvents="none" width={width} height={84} style={{position: 'absolute', inset: 0}}><Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.88}/><Stop offset="0.55" stopColor={theme.colors.ink} stopOpacity={.5}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.15}/></LinearGradient></Defs><Rect width={width} height={84} fill={`url(#${gradient})`}/></Svg>
    <View style={{position: 'absolute', left: 14, bottom: 12, right: 10}}><Text numberOfLines={1} style={[browseType(theme, 16, 800, -.025), {color: theme.colors.text}]}>{genre.name}</Text><Text style={[browseType(theme, 12, 400), {color: theme.colors.textSecondary, marginTop: 2}]}>{t('next.selection.titles', {count: genre.count})}</Text></View>
  </Focusable>;
}

function SearchResultRow({entry, group, runtime, actions, selection}: {entry: SearchEntry; group: SearchGroup; runtime: NativeShellRuntime; actions: NativeSearchActions; selection?: CardSelection}) {
  const t = runtime.text.t;
  const theme = useTheme(), menuAnchorRef = useRef<View>(null), content = media(entry) ? entry : undefined, text = content ? homeCardText(content, false, runtime.text) : undefined;
  const shape = group === 'movies' || group === 'shows' ? 'poster' : group === 'episodes' || group === 'live-tv' ? 'wide' : 'square';
  const width = shape === 'poster' ? 46 : shape === 'wide' ? 100 : 56, height = shape === 'poster' ? 69 : 56;
  const key = nativeSearchEntryKey(group, entry), title = content?.displayTitle || entry.title;
  return <View style={{flexDirection: 'row', alignItems: 'center', paddingVertical: 5, paddingHorizontal: 20, gap: 13}}>
    <CardFrame testID={`search-result-${key}`} focusId={key} title={title} selection={selection} menuAnchorRef={menuAnchorRef} moreExpanded={actions.isMenuOpen?.(entry, group)}
      onNavigate={() => actions.open(entry, group)} onMore={actions.canMenu?.(entry, group) === false ? undefined : () => actions.menu(entry, group, menuAnchorRef)} style={{flex: 1}} pressableStyle={{flexDirection: 'row', gap: 13, alignItems: 'center'}}>
      {() => <>
      {selection?.active && <SelectionCircle selected={selection?.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/>}
      <View style={{width, height, borderRadius: 6, overflow: 'hidden', backgroundColor: theme.colors.raised}}><Artwork source={runtime.artwork(group === 'episodes' ? content?.stillUrl ?? content?.backdropUrl ?? entry.posterUrl : entry.posterUrl)} width={width} height={height}/></View>
      <View style={{flex: 1, minWidth: 0}}><Text numberOfLines={1} style={[browseType(theme, 15, 700), {color: selection?.selected ? theme.colors.accentStrong : theme.colors.text}]}>{title}</Text>
        <View style={{flexDirection: 'row', gap: 12, marginTop: 2}}>{text?.caption.map(part => <Text key={part.id} numberOfLines={1} style={[browseType(theme, 12.5, 400), {color: theme.colors.textTertiary, flexShrink: 1}]}>{part.text}</Text>)}</View>
      </View>
      </>}
    </CardFrame>
    {!selection?.active && actions.canMenu?.(entry, group) !== false && <Focusable testID={`search-more-${key}`} accessibilityLabel={t('card.more', {title})} onPress={() => actions.menu(entry, group, menuAnchorRef)} hitSlop={3} focusRadius={19}
      style={{width: 38, height: 38, borderRadius: 19, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-more" size={20} color={theme.colors.textTertiary}/></Focusable>}
  </View>;
}


function SearchTopResult({entry, group, runtime, actions, selection, width}: {entry: SearchEntry; group: SearchGroup; runtime: NativeShellRuntime; actions: NativeSearchActions; selection?: CardSelection; width: number}) {
  const t = runtime.text.t;
  const theme = useTheme(), menuAnchorRef = useRef<View>(null), gradient = useId(), kind = topResultKindLabel(group), title = media(entry) ? entry.displayTitle || entry.title : entry.title;
  return <CardFrame testID="search-top-result" title={title} selection={selection} menuAnchorRef={menuAnchorRef} moreExpanded={actions.isMenuOpen?.(entry, group)}
    onNavigate={() => actions.open(entry, group)} onMore={actions.canMenu?.(entry, group) === false ? undefined : () => actions.menu(entry, group, menuAnchorRef)}
    style={{marginHorizontal: 16, marginTop: 14, marginBottom: 2}} pressableStyle={{height: 178, borderRadius: 14, backgroundColor: theme.colors.dim, overflow: 'hidden'}}>
    {() => <>
    <Artwork source={runtime.artwork(media(entry) ? entry.backdropUrl ?? entry.posterUrl : entry.posterUrl)} height={178}/>
    <Svg pointerEvents="none" width={width} height={178} style={{position: 'absolute', inset: 0}}>
      <Defs>
        <LinearGradient id={`${gradient}-side`} x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.92}/><Stop offset="0.45" stopColor={theme.colors.ink} stopOpacity={.55}/><Stop offset="0.75" stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient>
        <LinearGradient id={`${gradient}-foot`} x1="0" y1="1" x2="0" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.7}/><Stop offset="0.5" stopColor={theme.colors.ink} stopOpacity={0}/></LinearGradient>
      </Defs>
      <Rect width={width} height={178} fill={`url(#${gradient}-side)`}/><Rect width={width} height={178} fill={`url(#${gradient}-foot)`}/>
    </Svg>
    <View style={{position: 'absolute', left: 16, right: 70, bottom: 14}}>
      <Text style={[browseType(theme, 12.5, 700), {color: theme.colors.accentStrong}]}>{t('search.topResult')}</Text>
      <Text numberOfLines={2} style={[browseType(theme, 27, 800, -.035, 28.35), {color: theme.colors.text, marginTop: 4, marginBottom: 6}]}>{title}</Text>
      {kind && <Text numberOfLines={1} style={[browseType(theme, 13, 400), {color: theme.colors.textSecondary}]}>{t(kind)}</Text>}
    </View>
    {selection?.active ? <View style={{position: 'absolute', right: 14, top: 14}}><SelectionCircle selected={selection?.selected} selecting interactive={false} variant="row" size={24} accessibilityLabel={t('card.select', {title})}/></View>
      : media(entry) && entry.playback && <Focusable testID="search-top-play" accessibilityLabel={t('card.play', {title})} onPress={() => actions.play(entry, group)} focusRadius={23}
        style={{position: 'absolute', right: 14, bottom: 14, width: 46, height: 46, borderRadius: 23, backgroundColor: theme.colors.text, alignItems: 'center', justifyContent: 'center'}}><Icon name="i-play" size={20} color={theme.colors.ink}/></Focusable>}
    </>}
  </CardFrame>;
}
