import React, {useMemo, useRef, useState} from 'react';
import {View} from 'react-native';
import {defaultI18n, formatMessage, type MessageId} from '../../../../../../packages/i18n/src';
import {libraryFramesFixtureMessages, libraryFramesPosters, type LibraryFramesFixtureId} from '../../../../../../packages/i18n/src/catalog/en-US/next/dev/library-frames-fixtures';
import {createWindowedCollection} from '../../../../../../packages/client-core/src/collections/windowed';
import {appliedFilterParts, browseToolbarModel, type AppliedFilter} from '../../../../../../packages/client-core/src/presentation/kit-model';
import {libraryTabs} from '../../../../../../packages/client-core/src/presentation/library-tabs';
import type {BrowseNode, BrowseSortSelection} from '../../../../../../packages/client-core/src/browse';
import {AmbientArt} from '../../../kit/artwork/index';
import {BrowseToolbar, FilterBuilder, FilteredEmpty, LibraryMenu, LibrarySwitcher, type FilterFieldState} from '../../../kit/browse';
import {CollectionTile, LiveCard, PosterCard, SquareCard, WideCard} from '../../../kit/cards';
import {cardCaptionHeight} from '../../../kit/cards/caption-metrics';
import {Button, KeyHints} from '../../../kit/controls';
import {BrowseFrame, HomeHero, browseGridCard, type HomeHeroEntry} from '../../../kit/frames';
import {useFrameMetrics} from '../../../kit/frames/metrics';
import {AlphabetRail, Row, WindowedGrid} from '../../../kit/layout';
import {OwnerTag} from '../../../kit/shell';
import {useTheme} from '../../../kit/theme';
import {frameSpec} from '@portico/design';
import {PhoneHomeHeader} from '../../../shell/PhoneHomeHeader';
import type {Gallery} from '../../gallery-registry';
import {capability, clearedFields, fieldNames, initialFields, labels, updateField} from '../browse/browse.gallery';
import {Block, Page, Region, Window, asset, card, f, fact, libraries, none, selection, wides} from './frames.gallery';

/** Family B, round 8: the library and Home drawn FULL in BrowseFrame, every region `frameRegions.browse` and `.home` name,
 * from the app's own kit parts, reference text and the capture run's artwork. FRAMES.md lists the regions per form. */
const t = defaultI18n.t;
const l = (id: LibraryFramesFixtureId) => formatMessage(libraryFramesFixtureMessages[id]);
export const posters = libraryFramesPosters.map(([id, year]) => ({id, year, title: l(`next.libraryFrames.fixture.poster.${id}`), art: asset(`pool/movies/${id}.jpg`)}));
type Poster = typeof posters[number];
const year = (poster: Poster) => [{id: 'year', text: String(poster.year), role: 'identity' as const}];
const watched = new Set(['alien', 'blade-runner']), progress: Readonly<Record<string, number>> = {'avengers-endgame': .4, barbie: .7};
/** The grid's own lists are constants: a new list each render would be a new collection each render. */
const animated = posters.filter(poster => ['how-to-train-your-dragon', 'incredibles-2', 'inside-out', 'moana', 'monsters-inc', 'interstellar'].includes(poster.id)), recent = posters.slice(20);
const count = (n: number) => t('lib.count', {count: n, pivot: 'movies'});

/** The library's own header regions: the switcher in the title, ⋯, Select (tablet) and the tabs. */
function useLibraryHeader({tab = 'browse', kind = 'movie', menuOpen = false}: {tab?: string; kind?: string; menuOpen?: boolean} = {}) {
  const {form} = useFrameMetrics(), [library, setLibrary] = useState(kind === 'music' ? 'music' : 'movie'), [current, setCurrent] = useState(tab);
  const name = libraries.find(entry => entry.id === library)?.name ?? f('movies');
  return {
    title: name,
    back: {label: f('libraries'), onPress: none},
    switcher: <LibrarySwitcher libraries={libraries} value={library} onValueChange={setLibrary} trigger={form === 'phone' ? 'name' : 'chevron'}/>,
    menu: <LibraryMenu owner ownerTag={<OwnerTag compact/>} open={menuOpen || undefined} onSaveView={none} onMarkAll={none} onEditPins={none} onLibrarySettings={none} onScan={none} count={count(80)}/>,
    trailing: <Button variant="text" size="compact" icon="i-check" onPress={none}>{t('next.selection.select')}</Button>,
    tabs: {value: current, onValueChange: setCurrent, items: libraryTabs(kind).map(item => ({value: item.id, label: t(item.label as MessageId)}))},
  };
}

/** The Filters bar: Filters with its count, quick chips, the count, sort with order, grid or list, card size, Save view, applied chips. */
function useToolbar(filtered: boolean, total = filtered ? 6 : 80) {
  const [sort, setSort] = useState<BrowseSortSelection>({field: 'title', direction: 'asc'});
  const [predicates, setPredicates] = useState<readonly BrowseNode[]>(filtered ? [{variant: 'predicate', field: 'genre', operator: 'contains-any', value: [l('next.libraryFrames.fixture.animation')]}, {variant: 'predicate', field: 'decade', operator: 'in', value: ['2010']}] : []);
  const [view, setView] = useState<'grid' | 'list'>('grid'), [size, setSize] = useState('medium'), [open, setOpen] = useState(filtered);
  const model = useMemo(() => browseToolbarModel(capability, 'movie', predicates, sort, {t, owner: true}), [predicates, sort]);
  const applied = model.applied.flatMap((predicate, index) => 'field' in predicate ? [{id: String(index), parts: appliedFilterParts(predicate as AppliedFilter, id => t(fieldNames[id]!), {t})}] : []);
  const toolbar = <BrowseToolbar model={model} labels={labels} resultCount={count(total)} filtersOpen={open} onFilters={() => setOpen(true)} onSortChange={setSort}
    onReshuffle={() => setSort(previous => ({...previous, seed: (previous.seed ?? 0) + 1}))}
    onQuickChange={(id, checked) => {const query = model.quick.find(item => item.id === id)!.query; setPredicates(previous => checked ? [...previous, query] : previous.filter(item => item !== query));}}
    applied={applied} onRemoveFilter={id => setPredicates(previous => previous.filter(item => item !== model.applied[Number(id)]))} onClearFilters={() => setPredicates([])}
    view={{value: view, onValueChange: setView}} cardSize={{value: size, options: ['small', 'medium', 'large'].map(value => ({value, label: t(`next.browse.native.${value}` as MessageId)})), onValueChange: setSize}}
    save={{canSave: predicates.length > 0, onSave: none}} onJump={none}/>;
  return {toolbar, open, setOpen};
}

function FiltersFields() {
  const theme = useTheme(), [fields, setFields] = useState<Record<string, FilterFieldState>>(initialFields), [openFields, setOpenFields] = useState<readonly string[]>([theme.isTV ? 'year' : 'genre']);
  const model = useMemo(() => browseToolbarModel(capability, 'movie', [], {field: 'title', direction: 'asc'}, {t, owner: true, textEntry: !theme.isTV}), [theme.isTV]);
  return <FilterBuilder focusPrefix="library-filters" model={model} labels={labels} openFields={openFields} fields={fields} onFieldChange={(id, value) => setFields(previous => updateField(previous, id, value))}
    onFieldOpenChange={(id, next) => setOpenFields(previous => next ? [...new Set([...previous, id])] : previous.filter(value => value !== id))}
    onSearchChange={(id, search) => setFields(previous => ({...previous, [id]: {...previous[id], search} as FilterFieldState}))} onShowAll={undefined}
    onRetry={() => setFields(clearedFields)}/>;
}

/** The poster grid: the kit's WindowedGrid of PosterCards with title and year, and the A–Z rail beside it. */
export function useGrid(items: readonly Poster[] = posters, filtersDocked = false) {
  const theme = useTheme(), frame = useFrameMetrics(), [visible, setVisible] = useState({first: 0, last: 0}), grid = useRef<React.ComponentRef<typeof WindowedGrid>>(null);
  const {width} = browseGridCard(frame, filtersDocked), caption = cardCaptionHeight(theme, 2);
  const positionIndex = useMemo(() => Object.fromEntries([...new Set(items.map(item => /^\d/.test(item.title) ? '#' : item.title[0]!.toUpperCase()))].map(key => [key, items.findIndex(item => (/^\d/.test(item.title) ? '#' : item.title[0]!.toUpperCase()) === key)])), [items]);
  const collection = useMemo(() => createWindowedCollection({pageSize: 40, maxResidentPages: 4, maxConcurrent: 1, keyOf: (item: Poster) => item.id,
    fetchPage: async (start, size) => ({items: items.slice(start, start + size), total: items.length, positionIndex})}), [items, positionIndex]);
  const element = <WindowedGrid ref={grid} collection={collection} getKey={item => item.id} itemWidth={width} itemHeight={width * 1.5 + caption} captionHeight={caption} selection={selection} gap={frameSpec.browse.grid.columnGap[frame.form]} rowGap={frameSpec.browse.grid.rowGap[frame.form]}
    gutter={frame.gutter} rightGutter={frame.rightGutter} paddingBottom={frame.bottomClear} onVisibleRange={(first, last) => setVisible(previous => previous.first === first && previous.last === last ? previous : {first, last})}
    renderPlaceholder={() => null}
    renderItem={(item, context) => <PosterCard {...card(context)} focusId={item.id} title={item.title} caption={year(item)} artwork={item.art} width={width} watched={watched.has(item.id)} progress={progress[item.id]} onNavigate={none}/>}/>;
  const alphabet = <AlphabetRail positionIndex={positionIndex} total={items.length} firstVisible={visible.first} lastVisible={visible.last} onJump={index => grid.current?.scrollToIndex(index)} height={Math.min(514, frame.width)}/>;
  return {grid: element, alphabet, start: items[0]!.id};
}

function PosterRow({title, subtitle, fresh = false, start = 0}: {title: string; subtitle?: string; fresh?: boolean; start?: number}) {
  const theme = useTheme(), width = theme.spec.card.width.poster[theme.column], caption = cardCaptionHeight(theme, 2), items = posters.slice(start, start + 10);
  return <Row title={title} subtitle={subtitle} onSeeAll={none} items={items} getKey={item => item.id} itemWidth={width} itemHeight={width * 1.5 + caption} captionHeight={caption} selection={selection}
    renderItem={(item, context) => <PosterCard {...card(context)} title={item.title} caption={year(item)} artwork={item.art} width={width} isNew={fresh && items.indexOf(item) < 3} onNavigate={none}/>}/>;
}

/** Continue Watching: an episode says its number and name, a film its year; the time left follows (phone: inside, with the number). */
const continuing = [
  {id: 'tlou', title: f('tlou'), art: wides[0]!.art, progress: .25, caption: [{id: 'episode-label', text: l('next.libraryFrames.fixture.episodeLabel'), role: 'identity' as const}, {id: 'episode-title', text: l('next.libraryFrames.fixture.episodeTitle'), role: 'context' as const}], left: l('next.libraryFrames.fixture.timeLeft')},
  {id: 'inside-out', title: f('insideOut'), art: wides[1]!.art, progress: .3, caption: [{id: 'year', text: '2015', role: 'identity' as const}], left: l('next.libraryFrames.fixture.filmLeft')},
  {id: 'rick', title: l('next.libraryFrames.fixture.rickTitle'), art: asset('bd-rick.jpg'), progress: .6, caption: [{id: 'episode-label', text: l('next.libraryFrames.fixture.rickLabel'), role: 'identity' as const}, {id: 'episode-title', text: l('next.libraryFrames.fixture.rickEpisode'), role: 'context' as const}], left: l('next.libraryFrames.fixture.rickLeft')},
  {id: 'spirited', title: f('spirited'), art: wides[2]!.art, progress: .5, caption: [{id: 'year', text: '2001', role: 'identity' as const}], left: l('next.libraryFrames.fixture.filmLeft')},
];
type Continuing = typeof continuing[number];
function ContinueRow({onFocusEntry}: {onFocusEntry?: (entry: Continuing) => void}) {
  const theme = useTheme(), width = theme.spec.card.width.wide[theme.column];
  return <Row title={f('continueWatching')} items={continuing} getKey={item => item.id} itemWidth={width} itemHeight={width * 9 / 16} selection={selection}
    renderItem={(item, context) => <WideCard {...card(context)} onFocus={() => {context.onFocus?.(); onFocusEntry?.(item);}} title={item.title} artwork={item.art} width={width} progress={item.progress}
      caption={item.caption} detail={[{id: 'remaining', text: item.left, role: 'context'}]} primaryAction="play" onPlay={none} onNavigate={none}/>}/>;
}

function LiveRow() {
  const shows = [{id: 'summit', title: l('next.libraryFrames.fixture.programme'), channel: l('next.libraryFrames.fixture.channel'), mark: l('next.libraryFrames.fixture.channelMark'), range: l('next.libraryFrames.fixture.endsAt'), progress: .2},
    {id: 'northwind', title: l('next.libraryFrames.fixture.programmeTwo'), channel: l('next.libraryFrames.fixture.channelTwo'), mark: l('next.libraryFrames.fixture.channelMarkTwo'), range: l('next.libraryFrames.fixture.endsAtTwo'), progress: .8}];
  const theme = useTheme(), width = theme.isTV ? 400 : 244;
  return <Row title={l('next.libraryFrames.fixture.onNow')} subtitle={l('next.libraryFrames.fixture.onNowSubtitle')} onSeeAll={none} items={shows} getKey={item => item.id} itemWidth={width} itemHeight={theme.isTV ? 225 : 140} selection={selection}
    renderItem={(item, context) => <LiveCard {...card(context)} width={width} title={item.title} channel={item.channel} mark={{sourceId: 'gallery', id: item.id, text: item.mark, seed: item.id}}
      flags={[{id: 'live', text: t('next.kit.live'), role: 'status'}]} range={item.range} progress={item.progress} onNavigate={none}/>}/>;
}

function ListeningRow() {
  const theme = useTheme(), width = theme.spec.card.width.square[theme.column], caption = cardCaptionHeight(theme, 2);
  const items = [{id: 'hobbit', title: l('next.libraryFrames.fixture.hobbit'), by: l('next.libraryFrames.fixture.tolkien'), art: asset('sq-hobbit.jpg'), progress: .3, detail: l('next.libraryFrames.fixture.chapter')},
    {id: 'after-hours', title: l('next.libraryFrames.fixture.afterHours'), by: l('next.libraryFrames.fixture.weeknd'), art: asset('sq-afterhours.jpg'), progress: .6, detail: undefined},
    {id: 'hail-mary', title: l('next.libraryFrames.fixture.poster.oppenheimer'), by: l('next.libraryFrames.fixture.tolkien'), art: asset('sq-hailmary.jpg'), progress: .1, detail: undefined}];
  return <Row title={l('next.libraryFrames.fixture.continueListening')} subtitle={l('next.libraryFrames.fixture.continueListeningSubtitle')} onSeeAll={none} items={items} getKey={item => item.id} itemWidth={width} itemHeight={width + caption} captionHeight={caption} selection={selection}
    renderItem={(item, context) => <SquareCard {...card(context)} width={width} title={item.title} caption={[{id: 'by', text: item.by, role: 'identity'}]} artwork={item.art} progress={item.progress} onNavigate={none}/>}/>;
}

/** The hero follows the focused card (final3-tv-01): an episode's number and name are the kicker over the show's title; its facts,
 * then the progress and the time left. */
const heroFacts: Readonly<Record<string, LibraryFramesFixtureId>> = {tlou: 'next.libraryFrames.fixture.tlouHeroFacts', 'inside-out': 'next.libraryFrames.fixture.insideOutHeroFacts',
  rick: 'next.libraryFrames.fixture.rickHeroFacts', spirited: 'next.libraryFrames.fixture.spiritedHeroFacts'};
const heroes: readonly HomeHeroEntry[] = continuing.map(item => ({id: item.id, title: item.title, overview: f('tlouSynopsis'), facts: fact(l(heroFacts[item.id]!)), progress: item.progress, remaining: item.left,
  reason: item.caption.some(part => part.id === 'episode-label') ? item.caption.map(part => part.text).join('  ') : undefined}));
const art = (id: string) => continuing.find(item => item.id === id)?.art;

function BrowseHome() {
  const theme = useTheme(), {form} = useFrameMetrics(), [focused, setFocused] = useState<HomeHeroEntry>(heroes[0]!), profileRef = useRef<View>(null);
  const hero = <HomeHero candidates={heroes} activeIndex={0} onIndexChange={none} focusedEntry={focused}
    hints={() => <KeyHints hints={[{key: t('next.layout.ok'), label: t('next.home.resume')}, {key: t('next.layout.holdOk'), label: t('next.layout.moreOptions')}]}/>}
    renderArtwork={(entry, context) => <AmbientArt foreground={art(entry.id)} visible={context.visible} foregroundStyle={context.foregroundStyle} onReady={context.onReady} onError={context.onError}/>}
    renderActions={() => <><Button icon="i-play" onPress={none}>{t('next.home.resume')}</Button><Button variant="glass" icon="i-plus" onPress={none}>{f('myList')}</Button></>}/>;
  return <>
    <BrowseFrame testID="frames-browse-home" startFocus={continuing[0]!.id} hero={hero}>
      <ContinueRow onFocusEntry={entry => setFocused(heroes.find(item => item.id === entry.id)!)}/>
      <PosterRow fresh title={f('newOnServer')} subtitle={l('next.libraryFrames.fixture.newSubtitle')} start={20}/>
      {!theme.isTV && <LiveRow/>}
      <ListeningRow/>
      <PosterRow title={l('next.libraryFrames.fixture.popular')} start={8}/>
    </BrowseFrame>
    {/* The shell's own wordmark bar over the hero on a phone (phone-app 1): wordmark, Play on and the avatar with its dot. */}
    {form === 'phone' && <PhoneHomeHeader profile={{id: 'gallery', name: f('profileInitial'), initial: f('profileInitial'), color: 'blue'}} unread avatarRef={profileRef} onProfile={none} viewerRole="member"
      castControl={<Button variant="icon" iconOnly icon="i-cast" accessibilityLabel={f('playOn')} onPress={none}/>}/>}
  </>;
}

/** The Movies tab: the grid with its rail under the Filters bar. */
function BrowseList({menuOpen = false}: {menuOpen?: boolean}) {
  const header = useLibraryHeader({menuOpen}), {toolbar} = useToolbar(false), {grid, alphabet, start} = useGrid();
  return <BrowseFrame testID="frames-browse-list" startFocus={start} header={header} toolbar={toolbar} grid={grid} alphabet={alphabet}/>;
}

/** The Discover tab: Continue Watching, then rows with New badges, See all and their grey subtitle. */
function BrowseDiscover({kind = 'movie'}: {kind?: string}) {
  const header = useLibraryHeader({tab: 'discover', kind});
  return <BrowseFrame testID={`frames-browse-${kind === 'music' ? 'music' : 'discover'}`} startFocus={kind === 'music' ? 'hobbit' : continuing[0]!.id} header={header}>
    {kind === 'music' ? <ListeningRow/> : <ContinueRow/>}
    <PosterRow fresh title={l('next.libraryFrames.fixture.recentlyAdded')} subtitle={l('next.libraryFrames.fixture.newSubtitle')} start={20}/>
    <PosterRow title={l('next.libraryFrames.fixture.recommended')} start={4}/>
  </BrowseFrame>;
}

/** Filters open over (or docked beside) a filtered grid: applied chips with Clear all, and the count. */
function BrowseFilters() {
  const header = useLibraryHeader(), {toolbar, open, setOpen} = useToolbar(true), {form} = useFrameMetrics();
  const {grid, alphabet, start} = useGrid(animated, open && form === 'expanded');
  return <BrowseFrame testID="frames-browse-filters" startFocus={start} header={header} toolbar={toolbar} grid={grid} alphabet={alphabet}
    filters={{open, onClose: () => setOpen(false), onClear: none, onDone: () => setOpen(false), count: count(6), save: {canSave: true, onSave: none}, children: <FiltersFields/>}}/>;
}

/** The Collections tab: a grid of wide collection tiles with their film counts. */
function BrowseCollections() {
  const theme = useTheme(), frame = useFrameMetrics(), header = useLibraryHeader({tab: 'collections'});
  // As many columns of at least 300 (TV 520) as fit, stretched to both gutters, as the poster grid's are.
  const room = frame.width - frame.gutter - frame.rightGutter, gap = theme.spec.shelf.gap[theme.column], columns = Math.max(1, Math.floor((room + gap) / ((theme.isTV ? 520 : 300) + gap))), width = Math.floor((room - gap * (columns - 1)) / columns);
  const tiles = ([['dune', 'collectionDune', 'twoFilms', ['dune', 'dune-part-two']], ['alien', 'collectionAlien', 'twoFilms', ['alien', 'aliens']], ['avengers', 'collectionAvengers', 'fourFilms', ['avengers-endgame', 'avengers-infinity-war']], ['pixar', 'collectionToyStory', 'sixFilms', ['inside-out', 'incredibles-2', 'monsters-inc']]] as const);
  return <BrowseFrame testID="frames-browse-collections" startFocus="dune" header={header}>
    <View style={{flexDirection: 'row', flexWrap: 'wrap', gap: theme.spec.shelf.gap[theme.column], paddingLeft: frame.gutter, paddingRight: frame.rightGutter}}>
      {tiles.map(([id, name, films, covers]) => <CollectionTile key={id} focusId={id} width={width} title={l(`next.libraryFrames.fixture.${name}`)} covers={covers.map(cover => asset(`pool/movies/${cover}.jpg`))} backdrop={asset(`pool/movies/${covers[0]}--bd.jpg`)}
        caption={[{id: 'count', text: l(`next.libraryFrames.fixture.${films}`), role: 'identity'}]} onNavigate={none}/>)}
    </View>
  </BrowseFrame>;
}

/** See all: a pushed page with Back and a plain title, Play and Shuffle beside it, the Filters bar and the grid. */
function BrowseSeeAll() {
  const {toolbar} = useToolbar(false, recent.length), {grid, alphabet, start} = useGrid(recent);
  return <BrowseFrame testID="frames-browse-see-all" startFocus={start} header={{title: f('newOnServer'), back: {label: f('movies'), onPress: none}, intro: l('next.libraryFrames.fixture.seeAllBody'),
    actions: <><Button icon="i-play" onPress={none}>{f('play')}</Button><Button variant="glass" icon="i-shuffle" onPress={none}>{l('next.libraryFrames.fixture.shuffle')}</Button></>}}
    toolbar={toolbar} grid={grid} alphabet={alphabet}/>;
}

/** Filters that match nothing: the toolbar and its chips stay; the grid's place says so and offers what would match. */
function BrowseEmpty() {
  const header = useLibraryHeader(), {toolbar} = useToolbar(true), movies = f('movies');
  const empty = <FilteredEmpty description={t('next.browse.native.filteredEmptyDescription', {library: movies, genre: l('next.libraryFrames.fixture.animation').toLocaleLowerCase('en-US'), decade: '2010'})} onClearFilters={none}
    suggestionsLabel={t('next.browse.native.orTry')} suggestions={[{id: 'year', label: l('next.libraryFrames.fixture.withoutYear'), count: 6, onSelect: none}, {id: 'genre', label: l('next.libraryFrames.fixture.withoutGenre'), count: 11, onSelect: none}]}/>;
  return <BrowseFrame testID="frames-browse-empty" startFocus="clear-filters" header={header} toolbar={toolbar} grid={empty}/>;
}

/** Loading keeps the header, tabs and toolbar the loaded page has, and draws the grid's boxes. */
function BrowseLoading() {
  const header = useLibraryHeader(), {toolbar} = useToolbar(false);
  return <BrowseFrame testID="frames-browse-loading" header={header} toolbar={toolbar} state={{kind: 'loading'}}/>;
}

/** Each region of the library frame, outlined: the screen's in blue, the flexible region in amber. */
function RegionsBrowse() {
  const frame = useFrameMetrics(), box = (label: string, rows = 1) => <View style={{marginHorizontal: frame.gutter}}><Region label={label} height={frame.rowTwoLine * rows}/></View>;
  return <Window><BrowseFrame header={{title: f('movies'), switcher: <Region label="header.switcher" height={frame.rowTwoLine * .6}/>, menu: <Region label="header.menu" height={frame.rowTwoLine * .6}/>, tabs: {value: 'browse', onValueChange: none, items: [{value: 'discover', label: 'header.tabs'}, {value: 'browse', label: f('movies')}]}}}
    toolbar={box('toolbar')} grid={<Block name="regionRows" flexible rows={6}/>} alphabet={<Region label="alphabet" height={frame.rowTwoLine * 4}/>}/></Window>;
}

/** Each case is drawn inside the gallery's `Page` (the shell's rail and its page inset), so its hooks measure the page, not the window. */
const whole = {screen: true} as const;
const galleries: readonly Gallery[] = [
  {id: 'frames-browse-home', title: 'next.platform.frame.browseHome', render: () => <Page destination={{kind: 'home'}}><BrowseHome/></Page>, ...whole},
  {id: 'frames-browse-list', title: 'next.platform.frame.browseList', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseList/></Page>, ...whole},
  {id: 'frames-browse-discover', title: 'next.platform.frame.browseDiscover', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseDiscover/></Page>, ...whole},
  {id: 'frames-browse-music', title: 'next.platform.frame.browseMusic', render: () => <Page destination={{kind: 'library', libraryId: 'music'}}><BrowseDiscover kind="music"/></Page>, ...whole},
  {id: 'frames-browse-filters', title: 'next.platform.frame.browseFilters', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseFilters/></Page>, ...whole},
  {id: 'frames-browse-collections', title: 'next.platform.frame.browseCollections', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseCollections/></Page>, ...whole},
  {id: 'frames-browse-see-all', title: 'next.platform.frame.browseSeeAll', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseSeeAll/></Page>, ...whole},
  {id: 'frames-browse-empty', title: 'next.platform.frame.browseEmpty', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseEmpty/></Page>, ...whole},
  {id: 'frames-browse-menu', title: 'next.platform.frame.browseMenu', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseList menuOpen/></Page>, platforms: ['phone', 'tablet', 'web'], ...whole},
  {id: 'frames-browse-loading', title: 'next.platform.frame.browseLoading', render: () => <Page destination={{kind: 'library', libraryId: 'movie'}}><BrowseLoading/></Page>, ...whole},
  {id: 'frames-regions-browse', title: 'next.platform.frame.regionsBrowse', render: RegionsBrowse, ...whole},
];
export default galleries;
