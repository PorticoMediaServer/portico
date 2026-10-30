import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {Alert, Text, View} from 'react-native';
import {defaultI18n, formatMessage, type MessageValues} from '../../../../../../packages/i18n/src';
import {layoutNativeFixtureMessages} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/layout-native-fixtures';
import {createWindowedCollection} from '../../../../../../packages/client-core/src/collections/windowed';
import {cardText, titleFactParts, type CardSubject} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {AmbientArt, Artwork, type AmbientArtSource, type ArtworkPalette, type ArtworkSource} from '../../../kit/artwork/index';
import {PosterCard, SquareCard, WideCard, CircleCard, EpisodeTile} from '../../../kit/cards';
import {Button} from '../../../kit/controls';
import {AlphabetRail, FlowSections, FocusPreviewHero, Hero, Lockup, Row, WindowedGrid, type ArtworkRenderContext, type HeroCandidate, type LayoutItemContext, type LayoutSelection, type WindowedGridHandle} from '../../../kit/layout';
import {useTheme} from '../../../kit/theme';
import type {Gallery} from '../../gallery-registry';

/** Reference-only records and artwork. This entire module is under the scaffold's __DEV__ boundary. */
const t = defaultI18n.t;
type FixtureName = keyof typeof layoutNativeFixtureMessages extends `next.layout.native.fixture.${infer Name}` ? Name : never;
const fixture = (name: FixtureName, values?: MessageValues) => formatMessage(layoutNativeFixtureMessages[`next.layout.native.fixture.${name}`], values);
type LayoutArtworkEvidence = Readonly<Record<string, {foreground: ArtworkSource; ambient?: AmbientArtSource; palette?: ArtworkPalette}>>;
declare global {
  var __porticoLayoutArtworkEvidence: LayoutArtworkEvidence | undefined;
}
const asset = (path: string): ArtworkSource => ({uri: `http://100.120.50.24:8787/assets/${path}`, cacheKey: `layout-reference:${path}`});
type ReferenceEntry = HeroCandidate & {subject: CardSubject; art: ArtworkSource; poster: ArtworkSource};
const referenceEntries: readonly ReferenceEntry[] = [
  {id: 'spirited-away', title: fixture('spiritedAway'), reason: fixture('becauseInsideOut'), subject: {id: 'spirited-away', kind: 'movie', title: fixture('spiritedAway'), year: 2001, genres: [fixture('animation'), fixture('family')], durationSeconds: 7500}, art: asset('bd-spirited.jpg'), poster: asset('pool/movies/spirited-away.jpg')},
  {id: 'the-last-of-us', title: fixture('lastOfUs'), overview: fixture('lastOfUsOverview'), subject: {id: 'the-last-of-us', kind: 'episode', title: fixture('lastOfUsEpisode'), show: {title: fixture('lastOfUs')}, episode: {mode: 'seasonal', season: 1, episode: 1}, year: 2023, genres: [fixture('drama')], seasonCount: 1, durationSeconds: 4920, positionSeconds: 1080}, art: asset('bd-tlou.jpg'), poster: asset('pool/tv/the-last-of-us.jpg')},
  {id: 'inside-out', title: fixture('insideOut'), reason: t('home.row.recommended'), subject: {id: 'inside-out', kind: 'movie', title: fixture('insideOut'), year: 2015, genres: [fixture('animation'), fixture('family')], durationSeconds: 5640, positionSeconds: 508}, art: asset('bd-insideout.jpg'), poster: asset('pool/movies/inside-out.jpg')},
  {id: 'solo-leveling', title: fixture('soloLeveling'), reason: t('home.row.recommended'), overview: fixture('soloOverview'), subject: {id: 'solo-leveling', kind: 'show', title: fixture('soloLeveling'), year: 2024, genres: [fixture('anime'), fixture('action'), fixture('scifi')], seasonCount: 1}, art: asset('pool/anime/solo-leveling--bd.jpg'), poster: asset('pool/anime/solo-leveling.jpg')},
];
const heroes = referenceEntries.map(entry => ({...entry, facts: titleFactParts(entry.subject, {t})}));
const movies = [
  ['12-angry-men', fixture('twelveAngryMen'), 1957], ['alien', fixture('alien'), 1979], ['aliens', fixture('aliens'), 1986], ['alpha', fixture('alpha'), 2026], ['avatar', fixture('avatar'), 2009], ['the-avengers', fixture('avengers'), 2012], ['avengers-endgame', fixture('avengersEndgame'), 2019], ['avengers-infinity-war', fixture('avengersInfinityWar'), 2018], ['back-to-the-future', fixture('backToFuture'), 1985], ['backrooms', fixture('backrooms'), 2026], ['barbie', fixture('barbie'), 2023], ['blade-runner', fixture('bladeRunner'), 1982], ['coyote-vs-acme', fixture('coyoteAcme'), 2026], ['the-dark-knight', fixture('darkKnight'), 2008], ['drawn-together', fixture('drawnTogether'), 2026], ['dune', fixture('dune'), 2021], ['dune-part-two', fixture('dunePartTwo'), 2024], ['the-empire-strikes-back', fixture('empireStrikesBack'), 1980],
] as const;
type GridEntry = {id: string; title: string; year: number; kind: 'movie'; poster: ArtworkSource};
const gridEntries: readonly GridEntry[] = movies.map(([id, title, year]) => ({id, title, year, kind: 'movie' as const, poster: asset(`pool/movies/${id}.jpg`)}));
const albumEntries = [
  {id: 'random-access-memories', title: fixture('randomAccessMemories'), artist: fixture('daftPunk'), year: 2013, kind: 'album' as const, poster: asset('pool/albums/random-access-memories.jpg')},
  {id: 'rumours', title: fixture('rumours'), artist: fixture('fleetwoodMac'), year: 1977, kind: 'album' as const, poster: asset('pool/albums/rumours.jpg')},
  {id: 'thriller', title: fixture('thriller'), artist: fixture('michaelJackson'), year: 1982, kind: 'album' as const, poster: asset('pool/albums/thriller.jpg')},
  {id: 'the-wall', title: fixture('wall'), artist: fixture('pinkFloyd'), year: 1979, kind: 'album' as const, poster: asset('pool/albums/the-wall.jpg')},
];

const cardContext = ({selection, onFocus, onBlur, focusId, testID}: LayoutItemContext) => ({selection, onFocus, onBlur, focusId, testID});

function Stage({height, children}: {height?: number; children: React.ReactNode}) {
  const theme = useTheme();
  return <View style={{width: theme.isTV ? 1920 : theme.layoutMode === 'desktop' ? theme.width : 390, height, marginLeft: -theme.gutter, position: 'relative', backgroundColor: theme.colors.ink}}>{children}</View>;
}

function useGallerySelection() {
  const [active, setActive] = useState(false), [ids, setIds] = useState<ReadonlySet<string>>(new Set());
  const toggle = useCallback((id: string) => setIds(current => {const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next;}), []);
  const enter = useCallback((id: string) => {setActive(true); setIds(new Set([id]));}, []);
  const selection: LayoutSelection = {active, isSelected: id => ids.has(id), onToggle: toggle, onEnter: enter};
  const forItem = (id: string) => ({active, selected: ids.has(id), onToggle: () => toggle(id), onEnter: () => enter(id)});
  const more = (entry: {id: string; title: string}) => Alert.alert(entry.title, undefined, [
    {text: t('card.select', {title: entry.title}), onPress: () => enter(entry.id)},
    {text: t('action.details'), onPress: () => Alert.alert(entry.title)},
    {text: t('action.cancel'), style: 'cancel'},
  ]);
  return {selection, forItem, more, clear: () => {setActive(false); setIds(new Set());}};
}

function ReferenceArtwork({entry, context, phone}: {entry: ReferenceEntry; context: ArtworkRenderContext; phone: boolean}) {
  const theme = useTheme();
  const real = globalThis.__porticoLayoutArtworkEvidence?.[entry.id];
  return <AmbientArt foreground={real?.foreground ?? entry.art} source={real?.ambient} palette={real?.palette ?? {first: theme.colors.ink, second: theme.colors.ambient}} treatment={phone ? 'phone' : 'hero'}
    foregroundFrameStyle={theme.isTV && !phone ? {width: 1500, height: 900} : undefined}
    arrivalDurationMs={0} foregroundStyle={context.foregroundStyle} visible={context.visible} priority onReady={() => context.onReady()} onError={() => context.onError()}
    position={phone ? {x: .38, y: .3} : undefined} style={{position: 'absolute', inset: 0}} />;
}

function ReferenceReason({entry}: {entry: ReferenceEntry}) {
  const theme = useTheme();
  const parts = entry.subject.kind === 'episode' ? cardText(entry.subject, 'continuation', {t}).caption : undefined;
  return parts ? <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: 12, marginBottom: 16}}>
    {parts.map(part => <Text key={part.id} style={[theme.type.eyebrow, {color: theme.colors.accentStrong}]}>{part.text}</Text>)}
  </View> : entry.reason ? <Text style={[theme.type.eyebrow, {color: theme.colors.accentStrong, textAlign: theme.layoutMode === 'phone' ? 'center' : 'left', marginBottom: theme.isTV ? 16 : theme.layoutMode === 'desktop' ? 14 : 8}]}>{entry.reason}</Text> : null;
}

function HeroGallery({mobileWeb = false}: {mobileWeb?: boolean}) {
  const theme = useTheme(), [index, setIndex] = useState(theme.isTV ? 3 : 0), [active, setActive] = useState(true), [focused, setFocused] = useState(false), [saved, setSaved] = useState(false);
  const desktop = theme.layoutMode === 'desktop';
  const height = theme.isTV ? 1080 : desktop ? theme.spec.hero.homeHeight.web : mobileWeb ? 486 : 560;
  return <>
    <Stage height={height}><Hero testID="dev.layout.hero" candidates={heroes} activeIndex={index} onIndexChange={setIndex} active={active} heroFocused={focused}
      variant={theme.isTV ? 'tv' : desktop ? 'desktop' : mobileWeb ? 'phone-web' : 'phone-app'}
      renderArtwork={(entry, context) => <ReferenceArtwork entry={entry} context={context} phone={theme.layoutMode === 'phone'} />}
      renderThumbnail={desktop ? (entry, context) => <Artwork source={entry.art} visible={context.visible} /> : undefined}
      renderReason={entry => <ReferenceReason entry={entry} />}
      renderActions={entry => <><Button testID={`dev.layout.hero.${entry.id}.play`} icon="i-play" onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} onPress={() => Alert.alert(t('action.play'), entry.title)}>{t('action.play')}</Button>
        <Button testID={`dev.layout.hero.${entry.id}.my-list`} variant="glass" icon={saved ? 'i-check' : 'i-plus'} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} onPress={() => setSaved(value => !value)} accessibilityState={{selected: saved}}>{t('title.watchlist')}</Button>
        {theme.isTV && <Button testID={`dev.layout.hero.${entry.id}.details`} variant="glass" onFocus={() => setFocused(true)} onBlur={() => setFocused(false)} onPress={() => Alert.alert(entry.title)}>{t('action.details')}</Button>}</>} /></Stage>
    <Button testID="dev.layout.hero.rotation" size="small" variant="text" onPress={() => setActive(value => !value)}>{t(active ? 'action.pause' : 'entry.resume')}</Button>
  </>;
}

function LockupGallery() {
  const theme = useTheme();
  return <Stage><View style={{padding: theme.gutter, gap: 44}}>
    <Lockup title={fixture('spiritedAway')} />
    <Lockup title={fixture('lordOfRings')} long lines={3} />
    <Lockup title={fixture('lordOfRings')} long lines={1} />
    {!theme.isTV && <Lockup title={fixture('spiritedAway')} variant="desktop" />}
  </View></Stage>;
}

function PreviewGallery() {
  const theme = useTheme(), [entry, setEntry] = useState(heroes[1]), [repeating, setRepeating] = useState(false);
  const state = useGallerySelection();
  return <Stage height={1080}>
    <FocusPreviewHero testID="dev.layout.preview.hero" focusedEntry={entry} repeating={repeating} renderArtwork={(item, context) => <ReferenceArtwork entry={item} context={context} phone={false} />}
      renderReason={item => <ReferenceReason entry={item} />} renderHint={() => <View style={{flexDirection: 'row', alignItems: 'center', gap: 12}}>
        {[t('next.layout.native.ok'), t('entry.resume'), t('next.layout.native.holdOK'), t('next.layout.native.moreOptions')].map((label, i) => <Text key={label} style={[theme.type.captionStrong, {color: theme.colors.textTertiary, marginLeft: i === 2 ? 24 : 0, ...(i % 2 === 0 ? {borderWidth: 1.5, borderColor: `${theme.colors.text}4D`, borderRadius: 9, paddingHorizontal: 10, paddingVertical: 6} : {})}]}>{label}</Text>)}
      </View>} />
    <View style={{position: 'absolute', left: 0, right: 0, top: 588}}><Row testID="dev.layout.preview.continue" title={t('home.row.continueWatching')} items={heroes.slice(1)} getKey={item => item.id} itemWidth={400} itemHeight={225}
      selection={state.selection} onItemFocus={setEntry} onRepeatingChange={setRepeating} renderItem={(item, context) => {
        const model = cardText(item.subject, 'continuation', {t});
        return <WideCard {...cardContext(context)} title={model.title} caption={model.caption} detail={model.detail} progress={model.progress} artwork={item.art} width={400} primaryAction="play" onPlay={() => Alert.alert(t('action.play'), item.title)} onMore={() => state.more(item)} hasTVPreferredFocus={context.index === 0} />;
      }} /></View>
  </Stage>;
}

function RowsGallery() {
  const theme = useTheme(), state = useGallerySelection();
  const [continued, setContinued] = useState(() => heroes.slice(1));
  const posterWidth = theme.isTV ? 214 : 118, squareWidth = theme.isTV ? 214 : 132;
  const card = (item: typeof gridEntries[number], context: LayoutItemContext) => <PosterCard {...cardContext(context)} title={item.title} caption={cardText(item, 'ordinary', {t}).caption} width={posterWidth} artwork={item.poster} onNavigate={() => Alert.alert(item.title)} onMore={() => state.more(item)} />;
  return <Stage><View style={{gap: theme.isTV ? 44 : 28}}>
    <Row testID="dev.layout.rows.continue" title={t('home.row.continueWatching')} items={continued} getKey={item => item.id} itemWidth={theme.isTV ? 400 : 262} itemHeight={theme.isTV ? 225 : 147.375} selection={state.selection}
      renderItem={(item, context) => {const model = cardText(item.subject, 'continuation', {t}); return <WideCard {...cardContext(context)} width={theme.isTV ? 400 : 262} title={model.title} caption={model.caption} detail={model.detail} progress={model.progress} artwork={item.art} primaryAction="play" onPlay={() => Alert.alert(t('action.play'), item.title)} onMore={() => state.more(item)} />;}} />
    {continued.length > 0 && <Button testID="dev.layout.rows.remove-continue" variant="text" size="small" onPress={() => setContinued(current => current.slice(1))}>{t('entry.removeFromContinueWatching')}</Button>}
    <Row testID="dev.layout.rows.recommended" title={t('home.row.recommended')} items={gridEntries} getKey={item => item.id} itemWidth={posterWidth} itemHeight={posterWidth * 1.5 + (theme.isTV ? 70 : 44)} selection={state.selection} onSeeAll={() => Alert.alert(t('shelf.seeAll'), t('home.row.recommended'))} renderItem={card} />
    <Row testID="dev.layout.rows.albums" title={t('search.chip.albums')} items={albumEntries} getKey={item => item.id} itemWidth={squareWidth} itemHeight={squareWidth + 54} selection={state.selection} renderItem={(item, context) => <SquareCard {...cardContext(context)} title={item.title} caption={cardText(item, 'ordinary', {t}).caption} width={squareWidth} artwork={item.poster} onNavigate={() => Alert.alert(item.title)} onMore={() => state.more(item)} />} />
    <Row testID="dev.layout.rows.artists" title={t('search.chip.artists')} items={albumEntries} getKey={item => item.id} itemWidth={theme.isTV ? 214 : 100} itemHeight={theme.isTV ? 268 : 154} selection={state.selection} renderItem={(item, context) => <CircleCard {...cardContext(context)} title={item.artist} width={theme.isTV ? 214 : 100} art={{covers: [item.poster]}} onNavigate={() => Alert.alert(item.artist)} onMore={() => state.more(item)} />} />
    <Row testID="dev.layout.rows.episodes" title={t('web.segments.episodes')} items={[heroes[1]]} getKey={item => item.id} itemWidth={theme.isTV ? theme.spec.episode.width.tv : 350} itemHeight={theme.isTV ? 380 : 124} selection={state.selection}
      renderItem={(item, context) => {const model = cardText(item.subject, 'in-show', {t}); return <EpisodeTile {...cardContext(context)} title={item.subject.title} label={model.caption} length={model.detail.find(part => part.id === 'duration')?.text} artwork={item.art} backdropFallback synopsis={item.overview} width={theme.isTV ? theme.spec.episode.width.tv : 350} primaryAction="play" onPlay={() => Alert.alert(t('action.play'), item.subject.title)} onMore={() => state.more(item)} />;}} />
    {state.selection.active && <Button testID="dev.layout.rows.cancel" variant="text" size="small" onPress={state.clear}>{t('action.cancel')}</Button>}
  </View></Stage>;
}

function GridGallery({stress = false}: {stress?: boolean}) {
  const theme = useTheme(), grid = useRef<WindowedGridHandle>(null), state = useGallerySelection();
  const [range, setRange] = useState({first: 0, last: 8});
  const total = stress ? 50_000 : gridEntries.length;
  const collection = useMemo(() => createWindowedCollection({pageSize: 30, maxResidentPages: 4, maxConcurrent: 1, keyOf: (item: typeof gridEntries[number]) => item.id,
    fetchPage: async (start, count) => ({items: stress
      ? Array.from({length: Math.max(0, Math.min(count, total - start))}, (_, offset) => ({...gridEntries[(start + offset) % gridEntries.length], id: `layout-stress-${start + offset}`}))
      : gridEntries.slice(start, start + count), total, positionIndex: stress ? undefined : {'#': 0, A: 1, B: 8, C: 12, D: 13}})}), [stress, total]);
  useEffect(() => () => collection.dispose(), [collection]);
  const width = theme.isTV ? 200 : 108, height = theme.isTV ? 367 : 200;
  const visibleRange = useCallback((first: number, last: number) => setRange({first, last}), []);
  return <>
    {stress && <View style={{gap: 12, marginBottom: 16}}>
      <Text style={[theme.type.heading, {color: theme.colors.text}]}>{fixture('stressTitle')}</Text>
      <Button testID="dev.layout.grid.stress.jump-middle" size="small" onPress={() => grid.current?.scrollToIndex(25_000)}>{fixture('jumpMiddle')}</Button>
      <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{fixture('visibleRange', {first: range.first + 1, last: range.last + 1, total})}</Text>
    </View>}
    <Stage height={theme.isTV ? 762 : 630}>
    <WindowedGrid testID={stress ? 'layout-grid-50k' : 'layout-grid-reference'} ref={grid} collection={collection} getKey={item => item.id} itemWidth={width} itemHeight={height} gap={theme.isTV ? 32 : 10} rowGap={theme.isTV ? 40 : 16}
      gutter={theme.isTV ? 200 : 16} rightGutter={theme.isTV ? 96 : 30} paddingTop={theme.isTV ? 0 : 12} selection={state.selection} onVisibleRange={visibleRange} style={{flex: 1}}
      renderPlaceholder={() => <View style={{height: width * 1.5, borderRadius: theme.radii.artwork, backgroundColor: theme.colors.raised}} />}
      renderItem={(item, context) => <PosterCard {...cardContext(context)} testID={`dev.layout.grid.${stress ? 'stress' : 'reference'}.movie.${item.id}`} density="grid" width={width} title={item.title} artwork={item.poster} caption={cardText(item, 'ordinary', {t}).caption} onNavigate={() => Alert.alert(item.title)} onMore={() => state.more(item)} />} />
    {!theme.isTV && !stress && <AlphabetRail testID="dev.layout.grid.alphabet" positionIndex={{'#': 0, A: 1, B: 8, C: 12, D: 13}} total={gridEntries.length} firstVisible={range.first} lastVisible={range.last} height={514} style={{position: 'absolute', right: 4, top: 18}} onJump={index => grid.current?.scrollToIndex(index)} />}
  </Stage>
  {state.selection.active && <Button testID={`dev.layout.grid.${stress ? 'stress' : 'reference'}.cancel`} variant="text" size="small" onPress={state.clear}>{t('action.cancel')}</Button>}
  </>;
}

function SparseGridGallery() {return <GridGallery stress />;}

function AlphabetGallery() {
  const theme = useTheme(), [first, setFirst] = useState(34), [current, setCurrent] = useState('M');
  return <View style={{padding: theme.gutter, alignItems: 'center'}}><AlphabetRail testID="dev.layout.alphabet" positionIndex={{'#': 0, A: 3, B: 8, C: 14, M: 30, S: 60, 'あ': 80, 'Ж': 90}} total={100} firstVisible={first} lastVisible={first + 12} currentKey={current}
    onJump={(index, key) => {setFirst(index); setCurrent(key);}} height={theme.isTV ? 1900 : 514} />
    <Text style={[theme.type.caption, {color: theme.colors.textSecondary}]}>{t('web.filters.jumpToKey', {key: current})}</Text>
  </View>;
}

function FlowGallery() {
  const theme = useTheme(), state = useGallerySelection(), [showArtists, setShowArtists] = useState(true);
  const circleWidth = theme.isTV ? 214 : 100, squareWidth = theme.isTV ? 214 : 132;
  const artists = <View><Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text, marginBottom: 16}]}>{t('search.chip.artists')}</Text>
    <View style={{flexDirection: 'row', gap: 16}}>{albumEntries.slice(0, 2).map(item => <CircleCard testID={`dev.layout.flow.artist.${item.id}`} key={item.id} title={item.artist} width={circleWidth} art={{covers: [item.poster]}} onNavigate={() => Alert.alert(item.artist)} selection={state.forItem(item.id)} onMore={() => state.more(item)} />)}</View></View>;
  const albums = <View><Text accessibilityRole="header" style={[theme.type.rowHeading, {color: theme.colors.text, marginBottom: 16}]}>{t('search.chip.albums')}</Text>
    <View style={{flexDirection: 'row', gap: 16}}>{albumEntries.slice(0, 2).map(item => <SquareCard testID={`dev.layout.flow.album.${item.id}`} key={item.id} title={item.title} width={squareWidth} artwork={item.poster} caption={cardText(item, 'ordinary', {t}).caption} onNavigate={() => Alert.alert(item.title)} selection={state.forItem(item.id)} onMore={() => state.more(item)} />)}</View></View>;
  return <><Button testID="dev.layout.flow.toggle-artists" variant="text" size="small" accessibilityState={{selected: showArtists}} onPress={() => setShowArtists(current => !current)}>{t('search.chip.artists')}</Button><Stage><View style={{paddingHorizontal: theme.gutter}}><FlowSections sections={[
    {id: 'artists', count: showArtists ? 2 : 0, cap: 4, itemWidth: circleWidth, content: artists},
    {id: 'empty', count: 0, cap: 4, itemWidth: 100, content: null},
    {id: 'albums', count: 2, cap: 4, itemWidth: squareWidth, content: albums},
  ]} /></View></Stage></>;
}

const galleries: readonly Gallery[] = [
  {id: 'layout-lockup', title: 'next.layout.native.lockupTitle', render: LockupGallery},
  {id: 'layout-hero', title: 'next.layout.native.heroTitle', render: HeroGallery, platforms: ['phone', 'tablet', 'tv']},
  {id: 'layout-hero-desktop', title: 'next.layout.native.heroTitle', render: HeroGallery, platforms: ['web']},
  {id: 'layout-hero-web', title: 'next.layout.native.heroTitle', render: () => <HeroGallery mobileWeb />, platforms: ['phone', 'tablet']},
  {id: 'layout-preview', title: 'next.layout.native.previewTitle', render: PreviewGallery, platforms: ['tv']},
  {id: 'layout-rows', title: 'next.layout.native.rowsTitle', render: RowsGallery},
  {id: 'layout-grid', title: 'next.layout.native.gridTitle', render: GridGallery},
  {id: 'layout-grid-50k', title: 'next.layout.native.gridTitle', render: SparseGridGallery},
  {id: 'layout-alphabet', title: 'next.layout.native.alphabetTitle', render: AlphabetGallery, platforms: ['phone', 'tablet']},
  {id: 'layout-flow', title: 'next.layout.native.flowTitle', render: FlowGallery},
];
export default galleries;
