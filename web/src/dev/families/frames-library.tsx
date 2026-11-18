import {useEffect, useMemo, useRef, useState, type ComponentType} from 'react';
import {defaultI18n, formatMessage, type MessageId} from '@i18n';
import {createWindowedCollection} from '@core/collections/windowed';
import type {BrowseCapabilities, BrowseNode, BrowseSortSelection} from '@core/browse';
import {browseNameId} from '@core/presentation/browse-toolbar';
import {libraryTabs} from '@core/presentation/library-tabs';
import {appliedFilterParts, browseToolbarModel, type FactPart} from '@portico/client-core/kit-model';
import {libraryFramesCapabilities, libraryFramesFixtureMessages, libraryFramesPosters, type LibraryFramesFixtureId} from '../../../../packages/i18n/src/catalog/en-US/next/dev/library-frames-fixtures';
import {defineGalleryFamily, type GalleryCase} from '../gallery-registry';
import {frameForm, frameSpec} from '@design';
import {AmbientArt, Artwork} from '../../kit/artwork';
import {BrowseToolbar, FilterBuilder, FilteredEmpty, LibraryMenu, LibrarySwitcher, type BrowseLabels, type FilterFieldChange, type FilterFieldState} from '../../kit/browse';
import {CollectionTile, LiveCard, PosterCard, SquareCard, WideCard} from '../../kit/cards';
import {Button} from '../../kit/controls';
import {BrowseFrame, HomeHero} from '../../kit/frames';
import {AlphabetRail, Shelf, WindowedGrid, type WindowedGridHandle} from '../../kit/layout';
import {usePhone} from '../../kit/overlays/shared';
import {OwnerTag} from '../../kit/shell';
import {Page, Region, Window, f, none, useReferenceArtwork, wides} from './frames';

/** Family B, round 8: the library and Home drawn FULL in BrowseFrame, every region `frameRegions.browse` and `.home` name, from
 * the kit's own parts, reference text and the capture run's artwork, with the native gallery's ids. Serve-only. */
const t = defaultI18n.t;
const l = (id: LibraryFramesFixtureId) => formatMessage(libraryFramesFixtureMessages[id]);
const posters = libraryFramesPosters.map(([id, year]) => ({id, year, title: l(`next.libraryFrames.fixture.poster.${id}`)}));
type Poster = typeof posters[number];
const year = (poster: Poster): FactPart[] => [{id: 'year', text: String(poster.year), role: 'identity'}];
const watched = new Set(['alien', 'blade-runner']), progress: Readonly<Record<string, number>> = {'avengers-endgame': .4, barbie: .7};
/** The grid's own lists are constants: a new list each render would be a new collection each render. */
const animated = posters.filter(poster => ['how-to-train-your-dragon', 'incredibles-2', 'inside-out', 'moana', 'monsters-inc', 'interstellar'].includes(poster.id)), recent = posters.slice(20);
const count = (n: number) => t('lib.count', {count: n, pivot: 'movies'});
const capabilities = libraryFramesCapabilities as unknown as BrowseCapabilities;
const name = (value: {id: string; labelKey: string}) => {const key = browseNameId(value.id, value.labelKey, defaultI18n.has); return key && defaultI18n.has(key) ? t(key) : value.labelKey;};
const labels: BrowseLabels = {field: name, sort: name, quick: quick => defaultI18n.has(quick.labelKey) ? t(quick.labelKey as MessageId) : quick.labelKey};
const env = {t, locale: 'en-US', owner: true};
const libraries = (['movies', 'tvShows', 'anime', 'music', 'audiobooks'] as const).map(id => ({id, name: f(id)}));

/** The library's header regions: the switcher in the title (centred at phone width), ⋯, Select and the tabs. */
function useLibraryHeader({tab = 'browse', kind = 'movie', menuOpen = false}: {tab?: string; kind?: string; menuOpen?: boolean} = {}) {
  const phone = usePhone(), [library, setLibrary] = useState(kind === 'music' ? 'music' : 'movies'), [current, setCurrent] = useState(tab), [open, setOpen] = useState(menuOpen);
  return {
    title: libraries.find(entry => entry.id === library)!.name,
    back: {label: f('libraries'), onPress: none},
    switcher: <LibrarySwitcher libraries={libraries} value={library} onValueChange={setLibrary} compact={phone} />,
    menu: <LibraryMenu owner ownerMark={<OwnerTag />} open={open} onOpenChange={setOpen} onSaveView={none} onMarkAll={none} onEditPins={none} onLibrarySettings={none} onScan={none} countSummary={count(80)} />,
    tabs: {value: current, onValueChange: setCurrent, items: libraryTabs(kind).map(item => ({value: item.id, label: t(item.label as MessageId)}))},
  };
}

/** The Filters bar: Filters with its count, quick chips, the count, sort with order, grid or list, card size, Save view, applied chips. */
function useToolbar(filtered: boolean, total = filtered ? 6 : 80) {
  const phone = usePhone(), [sort, setSort] = useState<BrowseSortSelection>({field: 'title', direction: 'asc'});
  const [predicates, setPredicates] = useState<readonly BrowseNode[]>(filtered ? [{variant: 'predicate', field: 'genre', operator: 'contains-any', value: [l('next.libraryFrames.fixture.animation')]}, {variant: 'predicate', field: 'decade', operator: 'in', value: [2010]}] : []);
  const [view, setView] = useState<'grid' | 'list'>('grid'), [size, setSize] = useState('medium'), [open, setOpen] = useState(filtered);
  const model = useMemo(() => browseToolbarModel(capabilities, 'movie', predicates, sort, env), [predicates, sort]);
  const chips = model.applied.flatMap((node, index) => 'field' in node ? [{id: String(index), parts: appliedFilterParts(node, id => labels.field(capabilities.fields.find(entry => entry.id === id) ?? {id, labelKey: id} as BrowseCapabilities['fields'][number]), env,
    value => typeof value === 'number' ? t('next.kit.filter.decade', {year: String(value)}) : String(value))}] : []);
  const toolbar = <BrowseToolbar model={model} labels={labels} platform={phone ? 'phone' : 'desktop'} resultCount={count(total)} filtersOpen={open} onFilters={() => setOpen(true)} onSortChange={setSort} topInset={0}
    onReshuffle={() => setSort(previous => ({...previous, seed: (previous.seed ?? 0) + 1}))}
    onQuickChange={(id, checked) => {const quick = model.quick.find(item => item.id === id); if (quick) setPredicates(previous => checked ? [...previous, quick.query] : previous.filter(item => JSON.stringify(item) !== JSON.stringify(quick.query)));}}
    applied={chips} onRemoveFilter={id => setPredicates(previous => previous.filter(item => item !== model.applied[Number(id)]))} onClearFilters={() => setPredicates([])}
    view={{value: view, onValueChange: setView}} cardSize={phone ? undefined : {value: size, options: (['small', 'medium', 'large'] as const).map(value => ({value, label: t(`next.browse.${value}`)})), onValueChange: setSize}}
    save={{canSave: predicates.length > 0, onSave: none}} />;
  return {toolbar, open, setOpen, model};
}

const genreKeys = ['animation', 'adventure', 'action', 'scifi', 'family', 'comedy', 'drama', 'fantasy', 'romance'] as const, genreCounts = [6, 8, 6, 6, 4, 2, 2, 1, 1];
function FiltersFields({model}: {model: ReturnType<typeof browseToolbarModel>}) {
  const phone = usePhone();
  const [fields, setFields] = useState<Record<string, FilterFieldState>>(() => ({
    genre: {kind: 'values', query: '', match: 'any', included: ['animation'], excluded: [], values: genreKeys.map((value, index) => ({value, label: l(`next.libraryFrames.fixture.${value}`), count: genreCounts[index]})), total: 13, status: 'ready', summary: l('next.libraryFrames.fixture.animation')},
    decade: {kind: 'values', query: '', match: 'any', included: ['2010'], excluded: [], values: [1980, 1990, 2000, 2010, 2020].map(value => ({value: String(value), label: t('next.kit.filter.decade', {year: String(value)})})), total: 5, status: 'ready', summary: l('next.libraryFrames.fixture.decade2010')},
    contentRating: {kind: 'values', query: '', match: 'any', included: [], excluded: [], values: [], total: 0, status: 'ready'}, studio: {kind: 'values', query: '', match: 'any', included: [], excluded: [], values: [], total: 0, status: 'ready'},
    collection: {kind: 'values', query: '', match: 'any', included: [], excluded: [], values: [], total: 0, status: 'ready'}, resolution: {kind: 'values', query: '', match: 'any', included: [], excluded: [], values: [], total: 0, status: 'ready'},
    playState: {kind: 'choice', value: undefined, options: [{value: 'unplayed', label: t('next.kit.browse.unwatched')}, {value: 'in-progress', label: t('next.kit.browse.inProgress')}, {value: 'played', label: t('next.kit.browse.watched')}]},
    favorite: {kind: 'toggle', value: undefined}, watchlisted: {kind: 'toggle', value: undefined},
  }));
  // A phone sheet opens Genre in place (phone-app 6); on a desktop a field opens as its own popover (H10), so none is open here.
  const [openFields, setOpenFields] = useState<readonly string[]>(phone ? ['genre'] : []);
  const change = (id: string, change: FilterFieldChange) => setFields(current => {
    const previous = current[id];
    if (!previous) return current;
    if (change.kind === 'facet' && previous.kind === 'values') return {...current, [id]: {...previous, included: [...previous.included.filter(value => value !== change.value), ...(change.selection === 'include' ? [change.value] : [])], excluded: [...previous.excluded.filter(value => value !== change.value), ...(change.selection === 'exclude' ? [change.value] : [])]}};
    if (change.kind === 'match' && previous.kind === 'values') return {...current, [id]: {...previous, match: change.value}};
    if (change.kind === 'choice' && previous.kind === 'choice') return {...current, [id]: {...previous, value: change.value}};
    if (change.kind === 'toggle' && previous.kind === 'toggle') return {...current, [id]: {...previous, value: change.value}};
    return current;
  });
  return <FilterBuilder model={model} labels={labels} platform={phone ? 'phone' : 'desktop'} fields={fields} openFields={openFields} onFieldChange={change}
    onFieldOpenChange={(id, opened) => setOpenFields(previous => opened ? [...previous.filter(value => value !== id), id] : previous.filter(value => value !== id))}
    onSearchChange={(id, query) => setFields(current => {const previous = current[id]; return previous?.kind === 'values' ? {...current, [id]: {...previous, query}} : current;})} onShowAll={none} onRetry={none} />;
}

/** The poster grid: the kit's WindowedGrid of PosterCards with title and year, the A–Z rail beside it in the right gutter. */
function useGrid(items: readonly Poster[] = posters) {
  const art = useReferenceArtwork(), phone = usePhone(), handle = useRef<WindowedGridHandle>(null), [visible, setVisible] = useState({first: 0, last: 20});
  const grid = frameSpec.browse.grid, form = frameForm(typeof window === 'undefined' ? 1440 : window.innerWidth);
  const anchors = useMemo(() => [...new Set(items.map(item => /^\d/.test(item.title) ? '#' : item.title[0]!.toUpperCase()))].map(key => ({key, index: items.findIndex(item => (/^\d/.test(item.title) ? '#' : item.title[0]!.toUpperCase()) === key)})), [items]);
  const collection = useMemo(() => createWindowedCollection({pageSize: 40, maxResidentPages: 4, maxConcurrent: 1, keyOf: (item: Poster) => item.id,
    fetchPage: async (start, size) => ({items: items.slice(start, start + size), total: items.length})}), [items]);
  useEffect(() => () => collection.dispose(), [collection]);
  const element = <WindowedGrid ref={handle} collection={collection} label={f('movies')} height="100%" fill cardWidth={grid.card[form]} rowHeight={width => width * 1.5 + (phone ? 50 : 66.6)} gap={grid.columnGap[form]} rowGap={grid.rowGap[form]}
    onVisibleRange={(first, last) => setVisible(previous => previous.first === first && previous.last === last ? previous : {first, last})}
    renderPlaceholder={() => null} renderItem={(item, context) => <PosterCard model={{title: item.title, caption: year(item), progress: progress[item.id]}} watched={watched.has(item.id)} artwork={art(`pool/movies/${item.id}.jpg`)} style={{width: context.width}} context="grid" density="grid" onNavigate={none} />} />;
  const alphabet = <AlphabetRail anchors={anchors} visibleRange={visible} onJump={anchor => handle.current?.scrollToIndex(anchor.index)} platform={phone ? 'phone' : 'desktop'} />;
  return {grid: element, alphabet};
}

function PosterRow({title, subtitle, fresh = false, start = 0}: {title: string; subtitle?: string; fresh?: boolean; start?: number}) {
  const art = useReferenceArtwork(), phone = usePhone(), items = posters.slice(start, start + 10);
  return <Shelf heading={title} subtitle={subtitle} onSeeAll={none} platform={phone ? 'phone-web' : 'desktop'} items={items} getKey={item => item.id}
    renderItem={item => <PosterCard isNew={fresh && items.indexOf(item) < 3} model={{title: item.title, caption: year(item)}} artwork={art(`pool/movies/${item.id}.jpg`)} style={{width: 'var(--card-poster-width)'}} onNavigate={none} />} />;
}

/** Continue Watching: an episode says its number and name, a film its year; the time left follows. */
const continuing = [
  {id: 'tlou', title: f('tlou'), art: wides[0]!.art, progress: .25, caption: [{id: 'episode-label', text: l('next.libraryFrames.fixture.episodeLabel'), role: 'identity'}, {id: 'episode-title', text: l('next.libraryFrames.fixture.episodeTitle'), role: 'context'}] as FactPart[], left: l('next.libraryFrames.fixture.timeLeft')},
  {id: 'inside-out', title: f('insideOut'), art: wides[1]!.art, progress: .3, caption: [{id: 'year', text: '2015', role: 'identity'}] as FactPart[], left: l('next.libraryFrames.fixture.filmLeft')},
  {id: 'rick', title: l('next.libraryFrames.fixture.rickTitle'), art: 'bd-rick.jpg', progress: .6, caption: [{id: 'episode-label', text: l('next.libraryFrames.fixture.rickLabel'), role: 'identity'}, {id: 'episode-title', text: l('next.libraryFrames.fixture.rickEpisode'), role: 'context'}] as FactPart[], left: l('next.libraryFrames.fixture.rickLeft')},
  {id: 'spirited', title: f('spirited'), art: wides[2]!.art, progress: .5, caption: [{id: 'year', text: '2001', role: 'identity'}] as FactPart[], left: l('next.libraryFrames.fixture.filmLeft')},
];
function ContinueRow() {
  const art = useReferenceArtwork(), phone = usePhone();
  return <Shelf heading={f('continueWatching')} platform={phone ? 'phone-web' : 'desktop'} items={continuing} getKey={item => item.id}
    renderItem={item => <WideCard model={{title: item.title, caption: item.caption, detail: [{id: 'remaining', text: item.left, role: 'context'}], progress: item.progress}} artwork={art(item.art)} style={{width: 'var(--card-wide-width)'}} onNavigate={none} />} />;
}
function LiveRow() {
  const phone = usePhone(), shows = [{id: 'summit', title: l('next.libraryFrames.fixture.programme'), channel: l('next.libraryFrames.fixture.channel'), mark: l('next.libraryFrames.fixture.channelMark'), range: l('next.libraryFrames.fixture.endsAt'), progress: .2},
    {id: 'northwind', title: l('next.libraryFrames.fixture.programmeTwo'), channel: l('next.libraryFrames.fixture.channelTwo'), mark: l('next.libraryFrames.fixture.channelMarkTwo'), range: l('next.libraryFrames.fixture.endsAtTwo'), progress: .8}];
  return <Shelf heading={l('next.libraryFrames.fixture.onNow')} subtitle={l('next.libraryFrames.fixture.onNowSubtitle')} onSeeAll={none} platform={phone ? 'phone-web' : 'desktop'} items={shows} getKey={item => item.id}
    renderItem={item => <LiveCard channel={{sourceId: 'gallery', id: item.id, name: item.channel, mark: {sourceId: 'gallery', id: item.id, text: item.mark, seed: item.id}}} programme={{title: item.title, facts: [{id: 'range', text: item.range, role: 'context'}], progress: item.progress, live: true}} onNavigate={none} />} />;
}
function ListeningRow() {
  const art = useReferenceArtwork(), phone = usePhone();
  const items = [{id: 'hobbit', title: l('next.libraryFrames.fixture.hobbit'), by: l('next.libraryFrames.fixture.tolkien'), art: 'sq-hobbit.jpg', progress: .3},
    {id: 'after-hours', title: l('next.libraryFrames.fixture.afterHours'), by: l('next.libraryFrames.fixture.weeknd'), art: 'sq-afterhours.jpg', progress: .6},
    {id: 'hail-mary', title: l('next.libraryFrames.fixture.poster.interstellar'), by: l('next.libraryFrames.fixture.tolkien'), art: 'sq-hailmary.jpg', progress: .1}];
  return <Shelf heading={l('next.libraryFrames.fixture.continueListening')} subtitle={l('next.libraryFrames.fixture.continueListeningSubtitle')} onSeeAll={none} platform={phone ? 'phone-web' : 'desktop'} items={items} getKey={item => item.id}
    renderItem={item => <SquareCard model={{title: item.title, caption: [{id: 'by', text: item.by, role: 'identity'}], progress: item.progress}} artwork={art(item.art)} style={{width: 'var(--card-square-width)'}} onNavigate={none} />} />;
}

/** The hero's facts: the first is the year; a runtime or a season count is the kit's outlined chip ('duration', 'season-count'). */
const heroFacts = (text: string): FactPart[] => text.split(' · ').map((part, index) => ({id: index === 0 ? 'year' : /\d+ h|\d+ min$/.test(part) ? 'duration' : / seasons?$/.test(part) ? 'season-count' : `fact-${index}`, text: part, role: index === 0 ? 'identity' : 'context'}));
const heroes = [{id: 'spirited', title: f('spirited'), reason: f('spiritedReason'), overview: f('tlouSynopsis'), facts: heroFacts(f('spiritedFacts')), art: 'bd-spirited.jpg', navigationLabel: f('spirited')},
  ...continuing.filter(item => item.id !== 'spirited').map(item => ({id: item.id, title: item.title, reason: undefined, overview: f('tlouSynopsis'), facts: heroFacts(f('tlouFacts')), art: item.art, navigationLabel: item.title}))];
function BrowseHome() {
  // The sample hero holds its first title: the real one moves on by itself, and a shot taken mid-change shows a faded hero.
  const art = useReferenceArtwork(), phone = usePhone();
  return <Page current="home" home><BrowseFrame hero={<HomeHero candidates={heroes} activeIndex={0} onIndexChange={none}
    renderReason={entry => entry.reason}
    renderArtwork={(entry, context) => <AmbientArt treatment={phone ? 'phone' : 'hero'} foreground={art(entry.art)} visible={context.visible} onReady={context.onReady} onError={context.onError} />}
    renderThumbnail={entry => <Artwork source={art(entry.art)} alt="" fit="cover" />}
    renderActions={() => <><Button variant="primary" phone={phone} icon="i-play">{f('play')}</Button><Button phone={phone} icon="i-plus">{f('myList')}</Button></>} />}>
    <ContinueRow />
    <PosterRow fresh title={f('newOnServer')} subtitle={l('next.libraryFrames.fixture.newSubtitle')} start={20} />
    <LiveRow />
    <ListeningRow />
    <PosterRow title={l('next.libraryFrames.fixture.popular')} start={8} />
  </BrowseFrame></Page>;
}

/** The Movies tab: the grid with its rail under the Filters bar. */
function BrowseList({menuOpen = false}: {menuOpen?: boolean}) {
  const header = useLibraryHeader({menuOpen}), {toolbar} = useToolbar(false), {grid, alphabet} = useGrid();
  return <Page current="movies"><BrowseFrame header={header} toolbar={toolbar} grid={grid} alphabet={alphabet} /></Page>;
}
function BrowseDiscover({kind = 'movie'}: {kind?: string}) {
  const phone = usePhone(), header = useLibraryHeader({tab: 'discover', kind});
  // Music's header carries Shuffle before ⋯ (desktop-listen music); a phone bar draws none.
  const trailing = kind === 'music' && !phone ? <Button size="small" icon="i-shuffle" onClick={none}>{l('next.libraryFrames.fixture.shuffle')}</Button> : undefined;
  return <Page current="movies"><BrowseFrame header={{...header, trailing}}>
    {kind === 'music' ? <ListeningRow /> : <ContinueRow />}
    <PosterRow fresh title={l('next.libraryFrames.fixture.recentlyAdded')} subtitle={l('next.libraryFrames.fixture.newSubtitle')} start={20} />
    <PosterRow title={l('next.libraryFrames.fixture.recommended')} start={4} />
  </BrowseFrame></Page>;
}
function BrowseFilters() {
  const header = useLibraryHeader(), {toolbar, open, setOpen, model} = useToolbar(true);
  const {grid, alphabet} = useGrid(animated);
  return <Page current="movies"><BrowseFrame header={header} toolbar={toolbar} grid={grid} alphabet={alphabet}
    filters={{open, onClose: () => setOpen(false), onClear: none, count: count(6), save: {canSave: true, onSave: none}, children: <FiltersFields model={model} />}} /></Page>;
}
function BrowseCollections() {
  const art = useReferenceArtwork(), header = useLibraryHeader({tab: 'collections'});
  const tiles = ([['dune', 'collectionDune', 'twoFilms', ['dune', 'dune-part-two']], ['alien', 'collectionAlien', 'twoFilms', ['alien', 'aliens']], ['avengers', 'collectionAvengers', 'fourFilms', ['avengers-endgame', 'avengers-infinity-war']], ['pixar', 'collectionToyStory', 'sixFilms', ['inside-out', 'incredibles-2', 'monsters-inc']]] as const);
  return <Page current="movies"><BrowseFrame header={header}>
    <div style={{display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 360px), 1fr))', gap: 'var(--row-gap)', padding: '0 var(--g)'}}>
      {tiles.map(([id, title, films, covers]) => <CollectionTile key={id} model={{title: l(`next.libraryFrames.fixture.${title}`), caption: [{id: 'count', text: l(`next.libraryFrames.fixture.${films}`), role: 'identity'}]}} artwork={art(`pool/movies/${covers[0]}--bd.jpg`)} covers={covers.map(cover => art(`pool/movies/${cover}.jpg`))} onNavigate={none} />)}
    </div>
  </BrowseFrame></Page>;
}
function BrowseSeeAll() {
  const phone = usePhone(), {toolbar} = useToolbar(false, recent.length), {grid, alphabet} = useGrid(recent);
  return <Page current="movies"><BrowseFrame header={{title: f('newOnServer'), back: {label: f('movies'), onPress: none}, intro: l('next.libraryFrames.fixture.seeAllBody'),
    actions: <><Button variant="primary" phone={phone} icon="i-play" onClick={none}>{f('play')}</Button><Button phone={phone} icon="i-shuffle" onClick={none}>{l('next.libraryFrames.fixture.shuffle')}</Button></>}}
    toolbar={toolbar} grid={grid} alphabet={alphabet} /></Page>;
}
function BrowseEmpty() {
  const header = useLibraryHeader(), {toolbar} = useToolbar(true);
  const empty = <FilteredEmpty description={l('next.libraryFrames.fixture.emptyDescription')} onClear={none} suggestions={[{id: 'year', label: l('next.libraryFrames.fixture.withoutYear'), count: 6, onSelect: none}, {id: 'genre', label: l('next.libraryFrames.fixture.withoutGenre'), count: 11, onSelect: none}]} />;
  return <Page current="movies"><BrowseFrame header={header} toolbar={toolbar} grid={empty} /></Page>;
}
function BrowseLoading() {
  const header = useLibraryHeader(), {toolbar} = useToolbar(false);
  return <Page current="movies"><BrowseFrame header={header} toolbar={toolbar} state={{kind: 'loading'}} /></Page>;
}
function RegionsBrowse() {
  const box = (label: string, height = 'var(--frame-row-two-line)') => <div style={{margin: '0 var(--g)'}}><Region label={label} height={height} /></div>;
  return <Window><BrowseFrame header={{title: f('movies'), switcher: <Region label="header.switcher" height="44px" />, menu: <Region label="header.menu" height="44px" />, tabs: {value: 'browse', onValueChange: none, items: [{value: 'discover', label: 'header.tabs'}, {value: 'browse', label: f('movies')}]}}}
    toolbar={box('toolbar')} grid={<Region name="regionRows" flexible />} alphabet={<Region label="alphabet" height="400px" />} /></Window>;
}

const section = (id: string, label: string, render: ComponentType, height = 900): GalleryCase => ({id, labelKey: `next.platform.frame.${label}` as MessageId, width: 1440, height, render});
export default defineGalleryFamily({id: 'frames-library', labelKey: 'next.platform.frame.libraryFamily', cases: [
  section('browse-home', 'browseHome', BrowseHome),
  section('browse-list', 'browseList', () => <BrowseList />),
  section('browse-discover', 'browseDiscover', () => <BrowseDiscover />),
  section('browse-music', 'browseMusic', () => <BrowseDiscover kind="music" />),
  section('browse-filters', 'browseFilters', BrowseFilters),
  section('browse-collections', 'browseCollections', BrowseCollections),
  section('browse-see-all', 'browseSeeAll', BrowseSeeAll),
  section('browse-empty', 'browseEmpty', BrowseEmpty),
  section('browse-menu', 'browseMenu', () => <BrowseList menuOpen />),
  section('browse-loading', 'browseLoading', BrowseLoading),
  section('regions-browse', 'regionsBrowse', RegionsBrowse),
]});
