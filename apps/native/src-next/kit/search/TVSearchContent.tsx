import React, {useId, useRef, useState, type RefObject} from 'react';
import {ScrollView, Text, View} from 'react-native';
import Svg, {Defs, LinearGradient, Rect, Stop} from 'react-native-svg';
import {fieldSpec, frameSpec} from '@portico/design';
import type {ContentEntry} from '../../../../../packages/client-core/src/library-content';
import type {SearchEntry, SearchGroup} from '../../../../../packages/client-core/src/search';
import {cardText} from '../../../../../packages/client-core/src/presentation/kit-model';
import {searchGroupLabel, topResultKindLabel} from '../../../../../packages/client-core/src/presentation/search-model';
import {defaultI18n} from '../../../../../packages/i18n/src';
import type {NativeShellRuntime} from '../../shell/runtime';
import {nativeDestinationFocusIds} from '../../screens/destinations/focus';
import {homeSubject} from '../../screens/home/model';
import {nativeSearchEntryKey} from '../../screens/search/model';
import {TVSearchSection} from './TVSearchSection';
import type {NativeTVSearchPageProps} from '../../pages/shared/search/useTVSearch';
import type {NativeSearchGenre, NativeSearchSelectionPort} from '../../screens/search/types';
import {nativeSearchGenreTiles} from '../../screens/search/genre-tiles';
import type {useSearch} from '../../pages/shared/search/useSearch';
import {Artwork} from '../artwork';
import {Chip, Focusable} from '../controls';
import {Parts, Progress} from '../cards/primitives';
import {FocusRegion, type FocusRegionProps} from '../focus';
import {Icon} from '../Icon';
import {TopResult} from './Search';
import {TVRow} from '../layout/TVRow';
import {StateView} from '../states';
import {alpha} from '../browse/presentation';
import {useTheme} from '../theme';
import {useFrameMetrics} from '../frames/metrics';

const t = defaultI18n.t;
const media = (entry: SearchEntry): entry is ContentEntry => entry.kind !== 'person' && entry.kind !== 'channel';
/** tv.html#f5 draws the top title twice: once as Top result and once in its own
 * section. Both are the product, so the top card needs its own focus identity;
 * sharing the section card's is a duplicate live child in one region. */
const tvTopResultFocusId = (group: SearchGroup, entry: SearchEntry): string => `top:${nativeSearchEntryKey(group, entry)}`;
const tvSearchR46 = {
  /** Chips keep their natural width: a squeezed row ellipsised every label and blanked the first. */
  chips: {alignItems: 'center', gap: 12},
  chip: {flexShrink: 0},
  chipLabel: {flexShrink: 0},
  meta: {flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 26, rowGap: 10, marginBottom: 14},
  metaProgress: {width: 160},
  /** tv.html#f5 `.mprog`: the bar, then the drawn resume point, on its own line. */
  resumeLine: {flexDirection: 'row', alignItems: 'center', gap: 14},
  genreClip: {position: 'absolute', top: 0, right: 0, bottom: 0, left: 0, borderRadius: 16, overflow: 'hidden'},
  genreCopy: {position: 'absolute', left: 30, bottom: 26},
} as const;
export function TVSearchQuery({text, label, placeholder}: {text: string; label: string; placeholder: string}) {
  const theme = useTheme(), field = fieldSpec.search;
  return <View testID="tv-search-query" accessibilityLabel={label} style={{
    minHeight: field.height[theme.column], borderRadius: field.radius[theme.column], backgroundColor: alpha(theme.colors.text, .08),
    paddingHorizontal: field.paddingX[theme.column], flexDirection: 'row', alignItems: 'center', gap: field.gap[theme.column],
  }}>
    <Icon name="i-search" size={theme.type.rowHeading.fontSize} color={theme.colors.textTertiary}/>
    <Text style={[text ? theme.type.rowHeading : theme.type.intro, {color: text ? theme.colors.text : theme.colors.textTertiary, flexShrink: 1}]}>{text || placeholder}</Text>
  </View>;
}

export function TVSearchVoice({focus, title, description, busy, onPress}: {
  focus: Pick<FocusRegionProps, 'id' | 'edges' | 'enabled'>; title: string; description: string; busy: boolean; onPress: () => void;
}) {
  const theme = useTheme(), size = theme.targets.touch;
  return <FocusRegion {...focus} entry="voice" style={{marginTop: theme.space.sm}}>
    <Focusable focusId="voice" disabled={!focus.enabled || busy} focusableWhenDisabled={busy} accessibilityRole="button" accessibilityLabel={title}
      onPress={onPress} style={{flexDirection: 'row', alignItems: 'flex-start', gap: theme.space.sm}}>
      <View style={{width: size, height: size, borderRadius: size / 2, alignItems: 'center', justifyContent: 'center',
        backgroundColor: alpha(theme.colors.text, .08)}}><Icon name="i-mic" size={theme.type.listLabel.fontSize} color={theme.colors.textSecondary}/></View>
      <Text style={[theme.type.rowHelp, {color: theme.colors.textTertiary, flex: 1}]}>
        <Text style={[theme.type.listLabel, {color: theme.colors.textSecondary}]}>{title}</Text>{' '}{description}
      </Text>
    </Focusable>
  </FocusRegion>;
}

export function TVSearchResults({view, destination, runtime, explore, actions, selection, ids, keyboard, keyboardKey, scroll}: Pick<NativeTVSearchPageProps, 'destination' | 'runtime' | 'explore' | 'actions' | 'selection'> & {
  view: ReturnType<typeof useSearch>; ids: ReturnType<typeof nativeDestinationFocusIds>; keyboard: string; keyboardKey?: string; scroll: RefObject<ScrollView | null>;
}) {
  const theme = useTheme(), frame = useFrameMetrics(), focusRoom = frameSpec.search.tvFocusRoom;
  const {t, state, landing, active, typed, empty, emptyAll, emptyIn, top, groups, problem, chips, chipsDrawn} = view;
  const genreTiles = nativeSearchGenreTiles(landing.genres, 'tv');
  const sections = typed && !empty && !problem ? groups.filter(group =>
    (state.group === 'all' || state.group === group.id) && (group.items.length > 0 || group.status === 'error')) : [];
  const contentRoot = useRef<View>(null), [viewport, setViewport] = useState({y: 0, height: 0}), [contentHeight, setContentHeight] = useState(0);
  const resultsWidth = Math.max(0, frame.width - frame.rightGutter);
  const run = (work: Promise<void>) => {void work.catch(runtime.reportError);};
  return <FocusRegion id={ids.searchResults} entryRegion={typed && !empty && !problem && top ? ids.searchTop : sections[0] ? ids.searchSection(sections[0].id) : emptyIn && chipsDrawn ? ids.searchChips : genreTiles.length ? ids.searchLanding : state.recent.length ? ids.searchRecent : chipsDrawn ? ids.searchChips : undefined}
      enabled={active.visible} edges={{left: keyboard}} style={{flex: 1}}>
      <ScrollView ref={scroll} onContentSizeChange={(_width, height) => setContentHeight(height)} onLayout={event => {const {height} = event.nativeEvent.layout; setViewport(current => ({...current, height}));}} contentContainerStyle={{paddingLeft: focusRoom, paddingTop: focusRoom, paddingBottom: frame.bottomClear}} scrollEventThrottle={100} onScroll={event => {
        setViewport({y: event.nativeEvent.contentOffset.y, height: event.nativeEvent.layoutMeasurement.height});
        if (state.group === 'all' || state.search.phase !== 'ready') return;
        const {contentOffset, contentSize, layoutMeasurement} = event.nativeEvent;
        const group = groups.find(value => value.id === state.group);
        if (group?.nextCursor && contentOffset.y + layoutMeasurement.height >= contentSize.height - 600) run(destination.next(group.id));
      }}><View ref={contentRoot} collapsable={false}>
        {chipsDrawn && <FocusRegion id={ids.searchChips} enabled={active.visible} orientation="horizontal" entry={`chip:${state.group}`} stops={{up: true}}
          edges={{left: keyboard, ...(top ? {down: ids.searchTop} : sections[0] ? {down: ids.searchSection(sections[0].id)} : {})}} style={{maxWidth: resultsWidth, marginLeft: -focusRoom}}>
          <TVRow><ScrollView horizontal showsHorizontalScrollIndicator={false}
            contentContainerStyle={[tvSearchR46.chips, {padding: focusRoom}]}>{chips.map(chip => {
          const group = groups.find(value => value.id === chip.id);
          const count = chip.id === 'all' ? groups.every(value => value.totalCountExact) ? groups.reduce((sum, value) => sum + value.totalCount, 0) : undefined : group?.totalCountExact ? group.totalCount : undefined;
          return <Chip key={chip.id} focusId={`chip:${chip.id}`} testID={`tv-search-chip-${chip.id}`} selected={state.group === chip.id} count={count} retry={group?.status === 'error'}
            disabled={!active.visible} style={tvSearchR46.chip} textStyle={tvSearchR46.chipLabel} onPress={() => destination.selectGroup(chip.id)}>{t(chip.label)}</Chip>;
        })}</ScrollView></TVRow></FocusRegion>}
        {state.search.error && <StateView testID="tv-search-read-error" {...runtime.error(state.search.error, () => run(destination.retry()))}/>}
        {problem && <StateView variant="search" title={problem}/>}
        {emptyAll && <View><Text accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, marginBottom: theme.space.sm}]}>{t('next.states.noMatches', {query: state.text.trim()})}</Text>
          <Text style={[theme.type.intro, {color: theme.colors.textSecondary, marginBottom: frame.sectionGap}]}>{t('next.states.noMatchesBody')}</Text></View>}
        {emptyIn && <Text testID="tv-search-none-in" accessibilityRole="header" style={[theme.type.pageTitle, {color: theme.colors.text, marginBottom: theme.space.sm}]}>{t('web.search.nothingIn', {group: t(searchGroupLabel(emptyIn))})}</Text>}
        {(!typed || emptyAll) && <>
          {!!genreTiles.length && <Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text, marginBottom: frame.headingGap}]}>{t(emptyAll ? landing.emptyHeading : landing.initialHeading)}</Text>}
          {landing.phase === 'error' && <StateView testID="tv-search-landing-error" {...runtime.error(landing.error, () => run(explore.load()))}/>}
          <FocusRegion id={ids.searchLanding} enabled={active.visible} grid={{columns: 2, count: genreTiles.length}} order={genreTiles.map(genre => `genre:${genre.id}`)}
            edges={{left: keyboard, ...(!typed && state.recent.length ? {down: ids.searchRecent} : {})}} stops={{up: true, right: true, ...(!typed && state.recent.length ? {} : {down: true})}} style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.space.md}}>{genreTiles.map(genre => <TVSearchGenre key={genre.id} genre={genre} width={(resultsWidth - theme.space.md) / 2}
            disabled={!active.visible} runtime={runtime} onPress={() => explore.openGenre(genre)}/>)}</FocusRegion>
          {!typed && !!state.recent.length && <FocusRegion id={ids.searchRecent} enabled={active.visible} orientation="vertical" entry={`recent:${state.recent[0]?.q}`} edges={{left: keyboard, ...(genreTiles.length ? {up: ids.searchLanding} : {})}} style={{marginTop: frame.sectionGap, width: resultsWidth}}><Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text, marginBottom: theme.space.sm}]}>{t('web.search.recent')}</Text>
            {state.recent.map(recent => <Focusable key={recent.q} focusId={`recent:${recent.q}`} focusKind="row" disabled={!active.visible} onPress={() => destination.useRecent(recent.q)}
              focusRadius={frame.cardRadius}
              // The ring sits outside the words: the row is padded and pulled out by the same, so the clock and words stay on the heading's line.
              style={{flexDirection: 'row', alignItems: 'center', gap: 16, minHeight: 64, paddingHorizontal: theme.space.md, marginHorizontal: -theme.space.md}}><Icon name="i-clock" size={26} color={theme.colors.textTertiary}/><Text style={[theme.type.listLabel, {color: theme.colors.textSecondary, flexShrink: 1}]}>{recent.q}</Text></Focusable>)}</FocusRegion>}
        </>}
        {typed && !empty && !problem && top && <FocusRegion id={ids.searchTop} enabled={active.visible} entry={tvTopResultFocusId(top.group, top.entry)} stops={{right: true}}
          edges={{left: keyboard, ...(chipsDrawn ? {up: ids.searchChips} : {}), ...(sections[0] ? {down: ids.searchSection(sections[0].id)} : {})}}><TVSearchTopResult entry={top.entry} group={top.group} runtime={runtime} actions={actions} selection={selection?.item(top.entry, top.group)} disabled={!active.visible}/></FocusRegion>}
        {sections.map((group, index) => <TVSearchSection key={group.id} group={group} destination={destination} runtime={runtime} actions={actions} selection={selection}
          enabled={active.visible} singleRow={state.group === 'all'} regionId={ids.searchSection(group.id)} keyboardRegion={keyboard}
          previousRegion={sections[index - 1] ? ids.searchSection(sections[index - 1]!.id) : top ? ids.searchTop : chipsDrawn ? ids.searchChips : undefined}
          previousFallback={sections[index - 1] ? ids.searchSection(sections[index - 1]!.id) : top ? ids.searchTop : undefined}
          nextRegion={sections[index + 1] ? ids.searchSection(sections[index + 1]!.id) : undefined}
          screenStart={keyboardKey ? {regionId: keyboard, childId: keyboardKey} : undefined} scrollY={viewport.y} viewportHeight={viewport.height}
          contentRoot={contentRoot} width={resultsWidth} focusRoom={focusRoom} layoutRevision={contentHeight} paging={state.group === 'all' ? state.paging?.[group.id] : {loading: state.search.phase === 'loading', error: state.search.error}}/>)}
      </View></ScrollView>
    </FocusRegion>;
}

export function TVSearchGenre({genre, runtime, width, disabled, onPress}: {genre: NativeSearchGenre; runtime: NativeShellRuntime; width: number; disabled: boolean; onPress: () => void}) {
  const theme = useTheme(), gradient = useId();
  return <Focusable focusId={`genre:${genre.id}`} testID={`tv-search-genre-${encodeURIComponent(genre.id)}`} focusKind="card" disabled={disabled} onPress={onPress}
    accessibilityLabel={genre.name} focusRadius={16} style={{width, height: 200, borderRadius: 16, backgroundColor: theme.colors.raised}}>
    <View style={tvSearchR46.genreClip}>
      <Artwork source={runtime.artwork(genre.backdropPath)} width={width} height={200}/>
      <Svg pointerEvents="none" width={width} height={200} style={{position: 'absolute', inset: 0}}><Defs><LinearGradient id={gradient} x1="0" y1="0" x2="1" y2="0"><Stop offset="0" stopColor={theme.colors.ink} stopOpacity={.88}/><Stop offset="0.55" stopColor={theme.colors.ink} stopOpacity={.5}/><Stop offset="1" stopColor={theme.colors.ink} stopOpacity={.15}/></LinearGradient></Defs><Rect width={width} height={200} fill={`url(#${gradient})`}/></Svg>
    </View>
    <View style={tvSearchR46.genreCopy}><Text style={[theme.type.rowHeading, {color: theme.colors.text}]}>{genre.name}</Text>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary, marginTop: 4}]}>{t('next.selection.titles', {count: genre.count})}</Text></View>
  </Focusable>;
}

/** tv.html#f5 draws the season count as .tv .tag; tagSpec is the canonical tag. */
export function TVSearchTag({children}: {children: React.ReactNode}) {
  const theme = useTheme(), tag = theme.spec.tag;
  return <View style={{paddingHorizontal: tag.paddingX[theme.column], paddingVertical: tag.paddingY, borderRadius: tag.radius[theme.column],
    borderWidth: tag.borderWidth[theme.column], borderColor: alpha(theme.colors.text, tag.borderAlpha)}}>
    <Text style={[theme.type.tag, {color: theme.colors.text}]}>{children}</Text>
  </View>;
}

export function TVSearchTopResult({entry, group, runtime, actions, selection, disabled}: Pick<NativeTVSearchPageProps, 'runtime' | 'actions'> & {entry: SearchEntry; group: SearchGroup; selection?: ReturnType<NativeSearchSelectionPort['item']>; disabled: boolean}) {
  // tv.html#f5 calls a show a "TV show"; the shared kind label keeps "Show".
  const theme = useTheme(), content = media(entry) ? entry : undefined,
    kind = group === 'shows' ? 'next.search.tvShow' : topResultKindLabel(group);
  const text = content && cardText(homeSubject(content), 'search', runtime.text), title = content?.displayTitle || entry.title, topArt = runtime.artwork(content?.backdropUrl ?? entry.posterUrl);
  const progress = text?.progress, resume = text?.resume, caption = text?.caption.filter(part => part.id !== 'year') ?? [];
  return <TopResult focusId={tvTopResultFocusId(group, entry)} testID="tv-search-top-result" title={title} eyebrow={t('search.topResult')}
    disabled={disabled} selection={selection} artwork={topArt} menuOpen={actions.isMenuOpen?.(entry, group)}
    onPress={() => actions.open(entry, group)} onMenu={actions.canMenu?.(entry, group) === false ? undefined : anchor => actions.menu(entry, group, anchor)}
    facts={<View style={tvSearchR46.meta}>{content?.year !== undefined && <Text style={[theme.type.bodyStrong, {color: theme.colors.text}]}>{content.year}</Text>}
      {kind && <Text style={[theme.type.meta, {color: theme.colors.textSecondary}]}>{t(kind)}</Text>}
      {caption.map(part => part.id === 'season-count' ? <TVSearchTag key={part.id}>{part.text}</TVSearchTag>
        : <Text key={part.id} style={[theme.type.meta, {color: theme.colors.textSecondary}]}>{part.text}</Text>)}</View>}
    details={<>{((progress !== undefined && progress > 0) || resume) && <View style={tvSearchR46.resumeLine}>
      {progress !== undefined && progress > 0 && <View style={tvSearchR46.metaProgress}><Progress value={progress} height={6} radius={6}/></View>}
      {resume && <Text style={[theme.type.listLabel, {color: theme.colors.text}]}>{resume.text}</Text>}</View>}
      <Parts parts={text?.detail} style={theme.type.intro} fit="wrap"/></>}/>;
}

export function TVSearchSelectionArtwork({runtime, path}: {runtime: NativeTVSearchPageProps['runtime']; path?: string}) {
  const source = runtime.artwork(path);
  return source ? <Artwork source={source} width={48} height={48}/> : null;
}
