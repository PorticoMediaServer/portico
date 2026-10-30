import React, {useEffect, useId, useMemo, useRef, useState, useSyncExternalStore} from 'react';
import {ScrollView, Text, View, useWindowDimensions} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry, SearchGroup} from '../../../../../packages/client-core/src/search';
import {cardText} from '../../../../../packages/client-core/src/presentation/kit-model';
import {orderedSearchGroups, SEARCH_CHIPS, topSearchResult, topResultKindLabel} from '../../../../../packages/client-core/src/presentation/search-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import {Artwork, StandardBackdrop} from '../../kit/artwork';
import {Chip, Focusable} from '../../kit/controls';
import {ArtFrame, CardFrame, Parts, Progress} from '../../kit/cards/primitives';
import {FocusRegion, useFocusRegistry} from '../../kit/focus';
import {Icon} from '../../kit/Icon';
import {StateView} from '../../kit/states';
import {alpha, browseType} from '../../kit/browse/presentation';
import {useTheme} from '../../kit/theme';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeDestinationFocusIds, type NativeDestinationActivationPort} from '../destinations/focus';
import {homeSubject} from '../home/model';
import {NativeSearchDestination, nativeSearchEntryKey} from './model';
import {TVSearchSection} from './TVSearchSection';
import {NativeTVKeyboard} from './TVKeyboard';
import {useTVVoiceSearch} from '../settings-live/use-tv-voice-search';
import {retainRecognizerReturn} from '../../kit/focus/recognizer-return';
import {sameAuthority} from '../../shell/model';
import type {NativeSearchActions, NativeSearchExplorePort, NativeSearchGenre, NativeSearchSelectionPort, NativeSearchTVCopy} from './types';
import {nativeSearchGenreTiles} from './genre-tiles';

const t = defaultI18n.t;
const media = (entry: SearchEntry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel';
/** tv.html#f5 draws the top title twice: once as Top result and once in its own
 * section. Both are the product, so the top card needs its own focus identity;
 * sharing the section card's is a duplicate live child in one region. */
const tvTopResultFocusId = (group: SearchGroup, entry: SearchEntry): string => `top:${nativeSearchEntryKey(group, entry)}`;
/** Drawn measurements from tv.html#f5 (.sres left/right/.top-r/.meta/.trk) and states.html#f18 (.genres right inset). */
const resultsLeft = 780;
/** A ScrollView clips its children. The results region starts this far left of and above the
 * drawn edge and pads its content by the same amount, so the drawn cards stay at x 780 / y 64
 * while a focused card's 1.08 scale and 8 px ring (and a chip's 1.05) stay visible. */
const tvSearchFocusRoom = 40;
const tvSearchR46 = {
  results: {position: 'absolute', left: resultsLeft - tvSearchFocusRoom, right: 0, top: 64 - tvSearchFocusRoom, bottom: 0},
  /** Chips keep their natural width: a squeezed row ellipsised every label and blanked the first. */
  chips: {flexDirection: 'row', flexWrap: 'wrap', gap: 12},
  chip: {flexShrink: 0},
  chipLabel: {flexShrink: 0},
  section: {marginTop: 46},
  heading: {flexDirection: 'row', alignItems: 'baseline', gap: 14},
  track: {flexDirection: 'row', gap: 28, marginTop: 30},
  /** The composite (art plus words) grows from its left edge so the art never shifts into the clip. */
  topResult: {marginTop: 44, transformOrigin: 'left center'},
  topRow: {flexDirection: 'row', gap: 36, alignItems: 'center'},
  meta: {flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 26, rowGap: 10, marginBottom: 14},
  metaProgress: {width: 160},
  /** tv.html#f5 `.mprog`: the bar, then the drawn resume point, on its own line. */
  resumeLine: {flexDirection: 'row', alignItems: 'center', gap: 14},
  genreClip: {position: 'absolute', top: 0, right: 0, bottom: 0, left: 0, borderRadius: 16, overflow: 'hidden'},
  genreCopy: {position: 'absolute', left: 30, bottom: 26},
} as const;
export type NativeTVSearchScreenProps = Readonly<{
  focusScope?: string; destination: NativeSearchDestination; runtime: NativeShellRuntime; activation: NativeDestinationActivationPort;
  explore: NativeSearchExplorePort; actions: NativeSearchActions; selection?: NativeSearchSelectionPort; copy: NativeSearchTVCopy;
}>;

/** The actual Search destination keeps its own service, IDs and navigation intent.
 * Only the shared keyboard requests entry focus; result arrival never moves it. */
export function TVSearchScreen({focusScope, destination, runtime, activation, explore, actions, selection, copy}: NativeTVSearchScreenProps) {
  useSyncExternalStore(actions.menuState?.subscribe ?? (() => () => {}), actions.menuState?.getSnapshot ?? (() => 0));
  const theme = useTheme(), dimensions = useWindowDimensions(), scroll = useRef<ScrollView>(null), [keyboardRow, setKeyboardRow] = useState<string>();
  const contentRoot = useRef<View>(null), [viewport, setViewport] = useState({y: 0, height: 1016}), [contentHeight, setContentHeight] = useState(0);
  const state = useSyncExternalStore(destination.subscribe, destination.getSnapshot), landing = useSyncExternalStore(explore.subscribe, explore.getSnapshot);
  const active = useSyncExternalStore(activation.subscribe, activation.getSnapshot), authority = runtime.getSnapshot().authority;
  const ids = useMemo(() => nativeDestinationFocusIds(authority, focusScope), [authority, focusScope]), registry = useFocusRegistry();
  useEffect(() => {
    if (!active.visible) return;
    return runtime.registerTVPageBack?.(() => {
      const current = activation.getSnapshot();
      // Retained/hidden pages, overlays and the rail never lend Search their Back. Results now
      // live in their own regions under the surrounding one, so a captured descendant counts too.
      const captured = registry.capture()?.regionId;
      const inResults = (regionId: string | undefined): boolean => {
        for (let id = regionId; id; id = registry.config(id)?.parent) if (id === ids.searchResults) return true;
        return false;
      };
      if (!current.visible || current.revision !== active.revision || !inResults(captured)) return false;
      // focusRegion uses only current enabled native key hosts and their real row memory.
      scroll.current?.scrollTo({y: 0, animated: false});
      return registry.focusRegion(keyboardRow ?? ids.searchKeyboardRow(0));
    }, {kind: 'search'});
  }, [activation, active.visible, active.revision, ids, keyboardRow, registry, runtime]);
  const voiceLifetime = useMemo(() => {
    const signal = runtime.retirementSignal ?? new AbortController().signal;
    return {signal, current: () => {
      if (signal.aborted) return false;
      const now = activation.getSnapshot();
      return now.visible && now.revision === active.revision && sameAuthority(authority, runtime.getSnapshot().authority);
    }};
  }, [runtime, activation, active.revision, authority]);
  const voice = useTVVoiceSearch(theme.isTV && active.visible && !!runtime.retirementSignal, voiceLifetime,
    text => destination.setText(text), runtime.reportError);
  const typed = !!state.text.trim(), groups = orderedSearchGroups(state.groups);
  const genreTiles = nativeSearchGenreTiles(landing.genres, 'tv');
  const empty = typed && state.search.phase === 'ready' && groups.every(group => group.status === 'success' && group.totalCount === 0);
  // Top result is chosen from the actual preview read, never from a later continuation.
  const preview = state.search.data?.groups ?? groups;
  const top = state.group === 'all' ? topSearchResult(state.text, orderedSearchGroups(preview)) : undefined;
  const shown = groups.filter(group => (state.group === 'all' || state.group === group.id) && (group.items.length > 0 || group.status === 'error'));
  const problem = state.problem === 'long' ? t('web.search.tooLong') : state.problem === 'words' ? t('web.search.tooManyWords') : state.problem ? t('web.search.tooShort') : undefined;
  // The drawn row keeps every available type while one is selected; in All it
  // drops the types this answer proved empty (tv.html#f5 counts the rest).
  const chips = SEARCH_CHIPS.filter(chip => chip.id === 'all' || (state.search.data?.capabilities ?? state.capabilities)?.groups.some(group => group.id === chip.id && group.available))
    .filter(chip => chip.id === 'all' || state.group !== 'all' || !groups.some(group => group.id === chip.id && group.status === 'success' && group.totalCount === 0));
  const chipsDrawn = typed && !empty && !problem && !!(state.search.data?.capabilities ?? state.capabilities);
  // Each result section is its own region, so the surrounding region never names a result
  // entry: it delegates to the first drawn section and that section's own remembered row.
  const keyboard = keyboardRow ?? ids.searchKeyboardRow(0);
  const keyboardKey = registry.remembered(keyboard);
  const sections = typed && !empty && !problem ? shown : [];
  useEffect(() => runtime.registerScrollToTop({kind: 'search'}, () => scroll.current?.scrollTo({y: 0, animated: !theme.reduceMotion})), [runtime, theme.reduceMotion]);
  const run = (work: Promise<void>) => {void work.catch(runtime.reportError);};
  const resultsWidth = Math.max(0, dimensions.width - resultsLeft - theme.layout.gutter.tvRight);
  return <View testID="native-tv-search-screen" pointerEvents={active.visible ? 'auto' : 'none'} accessibilityElementsHidden={!active.visible}
    importantForAccessibility={active.visible ? 'auto' : 'no-hide-descendants'} style={{flex: 1, backgroundColor: theme.colors.ink}}>
    <StandardBackdrop/>
    <View testID="tv-search-query" accessibilityLabel={t('web.shell.search')} style={{position: 'absolute', left: theme.gutter, top: 64, width: 500, height: 80, borderRadius: 16,
      backgroundColor: alpha(theme.colors.text, .08), paddingHorizontal: 24, flexDirection: 'row', alignItems: 'center', gap: 16}}>
      <Icon name="i-search" size={32} color={theme.colors.textTertiary}/>
      <Text numberOfLines={1} style={[browseType(theme, 34, 700), {color: theme.colors.text, flexShrink: 1}]}>{state.text || t('web.search.placeholder')}</Text>
      {typed && <View style={{width: 3, height: 40, marginLeft: -12, borderRadius: 2, backgroundColor: theme.colors.accentStrong}}/>}
    </View>
    <View style={{position: 'absolute', left: theme.gutter, top: 176}}><NativeTVKeyboard id={ids.searchKeyboard} value={state.text} onRowFocus={setKeyboardRow}
      onChangeText={value => destination.setText(value)} activation={active} leftRegionId={ids.rail} rightRegionId={ids.searchResults}/></View>
    {!empty && <FocusRegion id={`${ids.searchKeyboard}.voice`} entry="voice" enabled={active.visible && !!voice.begin} edges={{left: ids.rail, right: ids.searchResults, up: keyboard}} style={{position: 'absolute', left: theme.gutter, top: 792, width: 500, flexDirection: 'row', alignItems: 'flex-start', gap: 18}}>
      <Focusable focusId="voice" disabled={!voice.begin || voice.busy || !active.visible} focusableWhenDisabled={voice.busy} accessibilityRole="button" accessibilityLabel={t(copy.preferToTalk)}
        onPress={() => {
          const finish = retainRecognizerReturn(registry, keyboardKey ? {regionId: keyboard, childId: keyboardKey} : undefined);
          if (!voice.begin?.(finish)) finish(false);
        }} style={{flexDirection: 'row', alignItems: 'flex-start', gap: 18, width: '100%'}}>
      <View style={{width: 56, height: 56, borderRadius: 28, alignItems: 'center', justifyContent: 'center', backgroundColor: alpha(theme.colors.text, .08)}}><Icon name="i-mic" size={28} color={theme.colors.textSecondary}/></View>
      <Text style={[browseType(theme, 20, 400, 0, 29), {color: theme.colors.textTertiary, flex: 1}]}><Text style={[browseType(theme, 20, 700), {color: theme.colors.textSecondary}]}>{t(copy.preferToTalk)}</Text>{' '}{t(copy.voiceHint)}</Text>
      </Focusable>
    </FocusRegion>}
    <FocusRegion id={ids.searchResults} entryRegion={typed && !empty && !problem && top ? ids.searchTop : sections[0] ? ids.searchSection(sections[0].id) : genreTiles.length ? ids.searchLanding : state.recent.length ? ids.searchRecent : chipsDrawn ? ids.searchChips : undefined}
      enabled={active.visible} edges={{left: keyboardRow ?? ids.searchKeyboardRow(0)}} style={tvSearchR46.results}>
      <ScrollView ref={scroll} onContentSizeChange={(_width, height) => setContentHeight(height)} onLayout={event => {const {height} = event.nativeEvent.layout; setViewport(current => ({...current, height}));}} contentContainerStyle={{paddingLeft: tvSearchFocusRoom, paddingTop: tvSearchFocusRoom, paddingBottom: 72}} scrollEventThrottle={100} onScroll={event => {
        setViewport({y: event.nativeEvent.contentOffset.y, height: event.nativeEvent.layoutMeasurement.height});
        if (state.group === 'all' || state.search.phase !== 'ready') return;
        const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
        const group = groups.find(value => value.id === state.group);
        if (group?.nextCursor && contentOffset.y + layoutMeasurement.height >= contentSize.height - 600) run(destination.next(group.id));
      }}><View ref={contentRoot} collapsable={false}>
        {chipsDrawn && <FocusRegion id={ids.searchChips} enabled={active.visible} orientation="horizontal" entry={`chip:${state.group}`} stops={{up: true}}
          edges={{left: keyboard, ...(top ? {down: ids.searchTop} : sections[0] ? {down: ids.searchSection(sections[0].id)} : {})}} style={tvSearchR46.chips}>{chips.map(chip => {
          const group = groups.find(value => value.id === chip.id);
          const count = chip.id === 'all' ? groups.every(value => value.totalCountExact) ? groups.reduce((sum, value) => sum + value.totalCount, 0) : undefined : group?.totalCountExact ? group.totalCount : undefined;
          return <Chip key={chip.id} focusId={`chip:${chip.id}`} testID={`tv-search-chip-${chip.id}`} selected={state.group === chip.id} count={count} retry={group?.status === 'error'}
            disabled={!active.visible} style={tvSearchR46.chip} textStyle={tvSearchR46.chipLabel} onPress={() => destination.selectGroup(chip.id)}>{t(chip.label)}</Chip>;
        })}</FocusRegion>}
        {state.search.error && <StateView testID="tv-search-read-error" {...runtime.error(state.search.error, () => run(destination.retry()))}/>}
        {problem && <StateView variant="search" title={problem}/>}
        {empty && <View><Text accessibilityRole="header" style={[browseType(theme, 52, 800, -.04, 54.6), {color: theme.colors.text, marginBottom: 14}]}>{t('next.states.noMatches', {query: state.text.trim()})}</Text>
          <Text style={[browseType(theme, 26, 400), {color: theme.colors.textSecondary, marginBottom: 54}]}>{t('next.states.noMatchesBody')}</Text></View>}
        {(!typed || empty) && <>
          {!!genreTiles.length && <Text accessibilityRole="header" style={[browseType(theme, 32, 700, -.019), {color: theme.colors.text, marginBottom: 22}]}>{t(empty ? landing.emptyHeading : landing.initialHeading)}</Text>}
          {landing.phase === 'error' && <StateView testID="tv-search-landing-error" {...runtime.error(landing.error, () => run(explore.load()))}/>}
          <FocusRegion id={ids.searchLanding} enabled={active.visible} grid={{columns: 2, count: genreTiles.length}} order={genreTiles.map(genre => `genre:${genre.id}`)}
            edges={{left: keyboard, ...(!typed && state.recent.length ? {down: ids.searchRecent} : {})}} stops={{up: true, right: true, ...(!typed && state.recent.length ? {} : {down: true})}} style={{flexDirection: 'row', flexWrap: 'wrap', gap: 26}}>{genreTiles.map(genre => <TVSearchGenre key={genre.id} genre={genre} width={(resultsWidth - 26) / 2}
            disabled={!active.visible} runtime={runtime} onPress={() => explore.openGenre(genre)}/>)}</FocusRegion>
          {!typed && !!state.recent.length && <FocusRegion id={ids.searchRecent} enabled={active.visible} orientation="vertical" entry={`recent:${state.recent[0]?.q}`} edges={{left: keyboard, ...(genreTiles.length ? {up: ids.searchLanding} : {})}} style={{marginTop: 36}}><Text accessibilityRole="header" style={[browseType(theme, 32, 700), {color: theme.colors.text, marginBottom: 16}]}>{t('web.search.recent')}</Text>
            {state.recent.map(recent => <Focusable key={recent.q} focusId={`recent:${recent.q}`} focusKind="row" disabled={!active.visible} onPress={() => destination.useRecent(recent.q)}
              style={{flexDirection: 'row', alignItems: 'center', gap: 16, minHeight: 64}}><Icon name="i-clock" size={26} color={theme.colors.textTertiary}/><Text style={[browseType(theme, 24, 600), {color: theme.colors.textSecondary}]}>{recent.q}</Text></Focusable>)}</FocusRegion>}
        </>}
        {typed && !empty && !problem && top && <FocusRegion id={ids.searchTop} enabled={active.visible} entry={tvTopResultFocusId(top.group, top.entry)} stops={{right: true}}
          edges={{left: keyboard, ...(chipsDrawn ? {up: ids.searchChips} : {}), ...(sections[0] ? {down: ids.searchSection(sections[0].id)} : {})}}><TVSearchTopResult entry={top.entry} group={top.group} runtime={runtime} actions={actions} selection={selection?.item(top.entry, top.group)} disabled={!active.visible}/></FocusRegion>}
        {sections.map((group, index) => <TVSearchSection key={group.id} group={group} destination={destination} runtime={runtime} actions={actions} selection={selection}
          enabled={active.visible} singleRow={state.group === 'all'} regionId={ids.searchSection(group.id)} keyboardRegion={keyboard}
          previousRegion={sections[index - 1] ? ids.searchSection(sections[index - 1]!.id) : top ? ids.searchTop : chipsDrawn ? ids.searchChips : undefined}
          previousFallback={sections[index - 1] ? ids.searchSection(sections[index - 1]!.id) : top ? ids.searchTop : undefined}
          nextRegion={sections[index + 1] ? ids.searchSection(sections[index + 1]!.id) : undefined}
          screenStart={keyboardKey ? {regionId: keyboard, childId: keyboardKey} : undefined} scrollY={viewport.y} viewportHeight={viewport.height}
          contentRoot={contentRoot} width={resultsWidth} focusRoom={tvSearchFocusRoom} layoutRevision={contentHeight} paging={state.group === 'all' ? state.paging?.[group.id] : {loading: state.search.phase === 'loading', error: state.search.error}}/>)}
      </View></ScrollView>
    </FocusRegion>
    {selection?.render(path => {const source = runtime.artwork(path); return source ? <Artwork source={source} width={48} height={48}/> : undefined;})}
  </View>;
}

export function TVSearchGenre({genre, runtime, width, disabled, onPress}: {genre: NativeSearchGenre; runtime: NativeShellRuntime; width: number; disabled: boolean; onPress: () => void}) {
  const theme = useTheme(), gradient = useId();
  return <Focusable focusId={`genre:${genre.id}`} testID={`tv-search-genre-${encodeURIComponent(genre.id)}`} focusKind="card" disabled={disabled} onPress={onPress}
    accessibilityLabel={genre.name} focusRadius={16} style={{width, height: 200, borderRadius: 16, backgroundColor: theme.colors.raised}}>
    <View style={tvSearchR46.genreClip}>
      <Artwork source={runtime.artwork(genre.backdropPath)} width={width} height={200}/>
      <Svg pointerEvents="none" width={width} height={200} style={{position: 'absolute', inset: 0}}><Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.88}/><Stop offset="0.55" stopColor={theme.colors.ink} stopOpacity={.5}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.15}/></LinearGradient></Defs><Rect width={width} height={200} fill={`url(#${gradient})`}/></Svg>
    </View>
    <View style={tvSearchR46.genreCopy}><Text style={[browseType(theme, 36, 800, -.025), {color: theme.colors.text}]}>{genre.name}</Text>
      <Text style={[browseType(theme, 20, 400), {color: theme.colors.textSecondary, marginTop: 4}]}>{t('next.selection.titles', {count: genre.count})}</Text></View>
  </Focusable>;
}

/** tv.html#f5 draws the season count as .tv .tag; tagSpec is the canonical tag. */
export function TVSearchTag({children}: {children: React.ReactNode}) {
  const theme = useTheme(), tag = theme.spec.tag;
  return <View style={{paddingHorizontal: tag.paddingX[theme.platform], paddingVertical: tag.paddingY, borderRadius: tag.radius[theme.platform],
    borderWidth: tag.borderWidth[theme.platform], borderColor: alpha(theme.colors.text, tag.borderAlpha)}}>
    <Text style={[theme.type.tag, {color: theme.colors.text}]}>{children}</Text>
  </View>;
}

export function TVSearchTopResult({entry, group, runtime, actions, selection, disabled}: Pick<NativeTVSearchScreenProps, 'runtime' | 'actions'> & {entry: SearchEntry; group: SearchGroup; selection?: ReturnType<NativeSearchSelectionPort['item']>; disabled: boolean}) {
  // tv.html#f5 calls a show a "TV show"; the shared kind label keeps "Show".
  const theme = useTheme(), anchor = useRef<View>(null), content = media(entry) ? entry : undefined,
    kind = group === 'shows' ? 'next.search.tvShow' : topResultKindLabel(group);
  const text = content && cardText(homeSubject(content), 'search', runtime.text), title = content?.displayTitle || entry.title;
  const progress = text?.progress, resume = text?.resume, caption = text?.caption.filter(part => part.id !== 'year') ?? [];
  return <CardFrame focusId={tvTopResultFocusId(group, entry)} testID="tv-search-top-result" title={title} disabled={disabled} selection={selection}
    menuAnchorRef={anchor} moreExpanded={actions.isMenuOpen?.(entry, group)} onNavigate={() => actions.open(entry, group)} onMore={() => actions.menu(entry, group, anchor)}
    style={tvSearchR46.topResult} pressableStyle={tvSearchR46.topRow}>
    {state => <><ArtFrame width={480} height={270} radius={14} focused={state.focused} hovered={state.hovered} holding={state.holding} selected={selection?.selected}>
      <Artwork source={runtime.artwork(content?.backdropUrl ?? entry.posterUrl)} width={480} height={270}/></ArtFrame>
      <View style={{flex: 1}}><Text style={[browseType(theme, 20, 700), {color: theme.colors.accentStrong, marginBottom: 10}]}>{t('search.topResult')}</Text>
        <Text numberOfLines={3} style={[browseType(theme, 52, 800, -.04, 52), {color: theme.colors.text, marginBottom: 14}]}>{title}</Text>
        <View style={tvSearchR46.meta}>{content?.year !== undefined && <Text style={[browseType(theme, 24, 700), {color: theme.colors.text}]}>{content.year}</Text>}
          {kind && <Text style={[browseType(theme, 24, 400), {color: theme.colors.textSecondary}]}>{t(kind)}</Text>}
          {caption.map(part => part.id === 'season-count' ? <TVSearchTag key={part.id}>{part.text}</TVSearchTag>
            : <Text key={part.id} style={[browseType(theme, 24, 400), {color: theme.colors.textSecondary}]}>{part.text}</Text>)}</View>
        {((progress !== undefined && progress > 0) || resume) && <View style={tvSearchR46.resumeLine}>
          {progress !== undefined && progress > 0 && <View style={tvSearchR46.metaProgress}><Progress value={progress} height={6} radius={6}/></View>}
          {resume && <Text numberOfLines={1} style={[browseType(theme, 20, 700), {color: theme.colors.text}]}>{resume.text}</Text>}</View>}
        <Parts parts={text?.detail} style={browseType(theme, 24, 400)}/></View></>}
  </CardFrame>;
}
