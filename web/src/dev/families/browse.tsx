import {useEffect, useMemo, useState, type ComponentType, type ReactNode} from 'react';
import {defaultI18n, formatMessage, type MessageValues} from '@i18n';
import {parseBrowseCapabilities, parseBrowseResult, type BrowseCapabilities, type BrowseNode, type BrowseSortSelection} from '@core/browse';
import {libraryTabs} from '@core/presentation/library-tabs';
import {browseNameId, filterControlOf, rangePresets} from '@core/presentation/browse-toolbar';
import {appliedFilterParts, browseToolbarModel, cardText, titleFactParts} from '@portico/client-core/kit-model';
import {browseFixtures} from '../../../../packages/i18n/src/catalog/en-US/next/dev/browse-fixtures';
import {defineGalleryFamily} from '../gallery-registry';
import {loadGalleryInputs} from 'virtual:portico-kit-families';
import {Button, Tabs} from '../../kit/controls';
import {Artwork, AmbientArt, createArtworkRuntime, type ArtworkSource, type AmbientArtSource} from '../../kit/artwork';
import {Popover} from '../../kit/overlays';
import {OwnerTag} from '../../kit/shell';
import {SelectionCircle} from '../../kit/selection';
import {BrowseToolbar, FilterBuilder, FilteredEmpty, LibraryHeader, LibrarySwitcher, SongRow, SortMenu, StickyBrowseHeader, TrackTable, FieldTile,
  type BrowseLabels, type BrowsePlatform, type FilterFieldChange, type FilterFieldState, type TrackRowData} from '../../kit/browse';
import styles from '../../kit/browse/gallery.module.css';

const {movieCapabilities, musicCapabilities, songResponse, drawnListen, drawnTv, drawnBrowse, drawnRead, drawnPhone, albumResponse} = await loadGalleryInputs('browse');

const t = defaultI18n.t;
const fixture = (id: keyof typeof browseFixtures, values?: MessageValues) => formatMessage(browseFixtures[id], values ?? {}, {locale: 'en-US'});
const env = {t, locale: 'en-US', owner: true};
const parsedMovies = parseBrowseCapabilities(JSON.parse(movieCapabilities)), parsedMusic = parseBrowseCapabilities(JSON.parse(musicCapabilities));
const movies: BrowseCapabilities = {...parsedMovies, resolvedPivot: parsedMovies.pivots.find(pivot => pivot.id === 'movies')!};
const music: BrowseCapabilities = {...parsedMusic, resolvedPivot: parsedMusic.pivots.find(pivot => pivot.id === 'songs')!};
const songs = parseBrowseResult(JSON.parse(songResponse)).entries, albums = parseBrowseResult(JSON.parse(albumResponse)).entries;
const name = (value: {id: string; labelKey: string}) => { const key = browseNameId(value.id, value.labelKey, defaultI18n.has); return key && defaultI18n.has(key) ? t(key) : value.labelKey; };
const labels: BrowseLabels = {field: name, sort: name, quick: quick => defaultI18n.has(quick.labelKey) ? t(quick.labelKey) : quick.labelKey};
const genreKeys = ['animation', 'adventure', 'action', 'scifi', 'family', 'comedy', 'drama', 'fantasy', 'romance', 'horror', 'crime', 'documentary', 'thriller'] as const;
const genreCounts = [6, 8, 6, 6, 4, 2, 2, 1, 1, 6, 2, 1, 2];
const genres = genreKeys.map((value, index) => ({value, label: fixture(`next.browse.example.${value}`), count: genreCounts[index]}));
const valueNames = {'unplayed': 'next.kit.browse.unwatched', 'played': 'next.kit.browse.watched', 'in-progress': 'next.kit.browse.inProgress', 'available': 'next.kit.browse.available', 'unavailable': 'next.kit.browse.unavailable'} as const;
const valueName = (value: string) => Object.hasOwn(valueNames, value) ? t(valueNames[value as keyof typeof valueNames]) : value;
const initialPredicates: readonly BrowseNode[] = [{field: 'genre', operator: 'contains-any', value: [genres[0]!.label]}, {field: 'decade', operator: 'in', value: [2010]}];
function Stage({children, outcome}: Readonly<{children: ReactNode; outcome?: ReactNode}>) {
  return <div className={styles.stage}>{children}<output className={styles.outcome} aria-live="polite">{outcome}</output></div>;
}

function useReferenceArt() {
  const [runtime, setRuntime] = useState<ReturnType<typeof createArtworkRuntime> | null>(null);
  useEffect(() => {
    const next = createArtworkRuntime({scopeKey: 'browse-reference-gallery', classify: () => ({retryable: false}), load: async ({url}, signal) => {
      const image = new Image(); image.src = url;
      const abort = () => { image.src = ''; };
      signal.addEventListener('abort', abort, {once: true});
      try { await image.decode(); if (signal.aborted) throw new DOMException('Aborted', 'AbortError'); return {src: url, width: image.naturalWidth, height: image.naturalHeight}; }
      finally { signal.removeEventListener('abort', abort); }
    }});
    setRuntime(next); return () => next.dispose();
  }, []);
  return useMemo(() => {
    const sources = new Map<string, ArtworkSource>();
    for (const album of albums) {
      const slug = album.title === 'Abbey Road (2019 Mix)' ? 'abbey-road-2019-mix' : album.title === 'After Hours' ? 'after-hours' : undefined;
      if (runtime && slug) {
        const url = `http://100.120.50.24:8787/assets/pool/albums/${slug}.jpg`;
        sources.set(album.id, runtime.source({url, cacheKey: url}));
      }
    }
    return sources;
  }, [runtime]);
}

let ambientEvidenceSource: AmbientArtSource | undefined;
/** DEV evidence binds an actual platform runtime and genuine authenticated handle. */
export function bindBrowseAmbientEvidence(source?: AmbientArtSource) {
  ambientEvidenceSource = source;
  window.dispatchEvent(new Event('browse-ambient-evidence'));
}
function useBrowseAmbientEvidence() {
  const [source, setSource] = useState(ambientEvidenceSource);
  useEffect(() => {
    const update = () => setSource(ambientEvidenceSource);
    window.addEventListener('browse-ambient-evidence', update);
    return () => window.removeEventListener('browse-ambient-evidence', update);
  }, []);
  return source;
}

function headerCase(mode: 'desktop' | 'music' | 'phone' | 'switcher' | 'owner' | 'member' | 'read' | 'long'): ComponentType {
  return function HeaderCase() {
    const ambient = useBrowseAmbientEvidence();
    const [library, setLibrary] = useState(mode === 'music' ? 'music' : mode === 'read' ? 'books' : 'movies');
    const [switcherOpen, setSwitcherOpen] = useState(mode === 'switcher');
    const [outcome, setOutcome] = useState(''), [pivot, setPivot] = useState(['music', 'phone', 'switcher', 'long'].includes(mode) ? 'discover' : mode === 'read' ? '1' : 'browse');
    const choices: {id: string; name: string; count?: string}[] = (['movies', 'tv', 'anime', 'music', 'audiobooks', 'books', 'comics'] as const).map(id => ({id, name: fixture(`next.browse.example.${id}`)}));
    if (mode === 'switcher') {
      const reference = new DOMParser().parseFromString(drawnPhone, 'text/html');
      const countText = Array.from(reference.querySelector('.lsm')!.querySelectorAll('small')).map(item => item.textContent!);
      choices.forEach((choice, index) => {if (countText[index]) choice.count = countText[index]!;});
    }
    const current = choices.find(choice => choice.id === library)!;
    if (mode === 'long') current.name = fixture('next.browse.example.chapter6');
    const phone = !['desktop', 'music'].includes(mode);
    const switcher = <LibrarySwitcher libraries={choices} value={library} onValueChange={setLibrary} compact={phone} open={switcherOpen} onOpenChange={setSwitcherOpen}/>;
    const kind: Readonly<Record<string, string>> = {movies: 'movie', tv: 'tv', anime: 'anime', music: 'music', audiobooks: 'audiobook'};
    const tabItems = mode === 'read' ? Array.from(new DOMParser().parseFromString(drawnRead, 'text/html').querySelectorAll('#grid-s .tabs .tab')).map((item, index) => ({value: String(index), label: item.textContent!})) : libraryTabs(kind[library] ?? 'movie').map(tab => ({value: tab.id, label: t(tab.label)}));
    const tabs = <Tabs phone={phone} value={pivot} onValueChange={setPivot} className={phone ? styles.phoneTabs : [styles.headerTabs, mode === 'music' && styles.musicHeaderTabs].filter(Boolean).join(' ')}
      items={tabItems}/>;
    return <Stage outcome={outcome}>{phone ? <StickyBrowseHeader name={current.name} switcher={switcher} topInset={0} onLibraries={() => setOutcome(t('next.browse.libraries'))}>{tabs}</StickyBrowseHeader> :
      <LibraryHeader ambient={mode === 'desktop' && ambient ? <AmbientArt source={ambient} treatment="library" width="100%" height={560}/> : undefined} name={current.name} switcher={switcher} menu={mode === 'music' ? <><Button size="small" icon="i-shuffle" className={styles.headerAction} onClick={() => setOutcome(new DOMParser().parseFromString(drawnListen, 'text/html').querySelector('#music .lib-head .right button')!.textContent!)}>{new DOMParser().parseFromString(drawnListen, 'text/html').querySelector('#music .lib-head .right button')!.textContent}</Button></> : undefined} counts={mode === 'music' ? [
        {id: 'artists', text: fixture('next.browse.example.musicCounts'), role: 'value'}, {id: 'albums', text: fixture('next.browse.example.albumCounts'), role: 'value'},
        {id: 'songs', text: fixture('next.browse.example.songCount', {count: 40}), role: 'value'},
      ] : undefined}>{tabs}</LibraryHeader>}</Stage>;
  };
}

function toolbarCase(platform: BrowsePlatform, filtered = false, sticky = false, audio = false): ComponentType {
  return function ToolbarCase() {
    const [predicates, setPredicates] = useState<readonly BrowseNode[]>(filtered ? initialPredicates : []), [sort, setSort] = useState<BrowseSortSelection>({field: filtered ? 'year' : 'title', direction: filtered ? 'desc' : 'asc'});
    const [view, setView] = useState<'grid' | 'list'>('grid'), [size, setSize] = useState('medium'), [filtersOpen, setFiltersOpen] = useState(false), [outcome, setOutcome] = useState('');
    const model = browseToolbarModel(audio ? music : movies, audio ? 'music' : 'movie', predicates, sort, {...env, textEntry: platform !== 'tv'});
    const chips = model.applied.flatMap((predicate, index) => 'field' in predicate ? [{id: String(index), parts: appliedFilterParts(predicate, id => name((audio ? music : movies).fields.find(field => field.id === id) ?? {id, labelKey: id}), env)}] : []);
    return <Stage outcome={outcome}><BrowseToolbar model={model} labels={labels} platform={platform} sticky={sticky} topInset={0}
      resultCount={fixture(audio ? 'next.browse.example.songCount' : 'next.browse.example.movieCount', {count: filtered ? 6 : audio ? 40 : 80})}
      onQuickChange={(id, checked) => { const quick = [...model.quick, ...model.filterQuick].find(quick => quick.id === id)!; setPredicates(current => checked ? [...current, quick.query] : current.filter(node => JSON.stringify(node) !== JSON.stringify(quick.query))); }}
      onFilters={() => setFiltersOpen(value => !value)} filtersOpen={filtersOpen} onSortChange={setSort} onReshuffle={() => setSort(current => ({...current, seed: (current.seed ?? 0) + 1}))}
      view={audio || platform === 'tv' ? undefined : {value: view, onValueChange: setView}} cardSize={audio || platform !== 'desktop' ? undefined : {value: size, options: (['small', 'medium', 'large'] as const).map(value => ({value, label: t(`next.browse.${value}`)})), onValueChange: setSize}}
      save={{canSave: filtered || view !== 'grid' || size !== 'medium' || sort.field !== 'title', onSave: () => setOutcome(t('next.browse.saveView'))}}
      applied={chips} onRemoveFilter={id => setPredicates(current => current.filter(node => node !== model.applied[Number(id)]))} onClearFilters={() => setPredicates([])}
      onJump={platform === 'tv' ? () => setOutcome(t('next.browse.jump')) : undefined}/>
      {filtersOpen && <FilterSpecimen platform={platform}/>}</Stage>;
  };
}

function sortCase(field = 'year', owner = true): ComponentType { return function SortCase() {
  const [sort, setSort] = useState<BrowseSortSelection>({field, direction: field === 'year' ? 'desc' : 'asc'}), [open, setOpen] = useState(true);
  const model = browseToolbarModel(movies, 'movie', initialPredicates, sort, {...env, owner});
  return <Stage><div className={styles.menuStage}><SortMenu sorts={model.sorts} value={model.sort} label={labels.sort} open={open} onOpenChange={setOpen} onValueChange={setSort} onReshuffle={() => setSort(current => ({...current, seed: (current.seed ?? 0) + 1}))}/></div><output className={styles.outcome}>{JSON.stringify(sort)}</output></Stage>;
};}

function makeFields(model: ReturnType<typeof browseToolbarModel>, phone: boolean): Record<string, FilterFieldState> {
  return Object.fromEntries(model.groups.flatMap(group => group.fields.map(field => {
    const kind = filterControlOf(field);
    let state: FilterFieldState;
    if (kind === 'values') state = {kind, query: '', match: 'any', included: field.id === 'genre' ? [phone ? 'animation' : 'animation', ...(phone ? ['family'] : [])] : field.id === 'year' ? ['2010'] : [], excluded: phone && field.id === 'genre' ? ['horror'] : [],
      values: field.id === 'genre' ? genres.slice(0, phone ? 11 : 9) : field.id === 'year' ? [2010, 2000, 1990, 1980].map(year => ({value: String(year), label: t('next.kit.filter.decade', {year: String(year)})})) : (field.allowedValues ?? []).map(value => ({value, label: value})), moreLabel: field.id === 'genre' ? new DOMParser().parseFromString(drawnBrowse, 'text/html').querySelector('#filters .show-all')?.textContent?.trim() : undefined, total: field.id === 'genre' ? 13 : field.id === 'year' ? 4 : field.allowedValues?.length ?? 0, status: 'ready', summary: field.id === 'genre' ? fixture('next.browse.example.animation') : field.id === 'year' ? t('next.kit.filter.decade', {year: '2010'}) : undefined};
    else if (kind === 'toggle') state = {kind, value: undefined};
    else if (kind === 'choice') state = {kind, value: undefined, options: (field.allowedValues ?? []).map(value => ({value, label: valueName(value)}))};
    else if (kind === 'range') state = {kind, value: undefined, options: rangePresets(field, new Date('2026-10-22T12:00:00Z')).map(preset => ({value: preset.id, label: t(preset.label.id, preset.label.values)}))};
    else state = {kind, value: ''};
    return [field.id, state];
  })));
}
function FilterSpecimen({platform, state = 'ready', field = 'genre', literalPhone = false}: Readonly<{platform: BrowsePlatform; state?: 'ready' | 'loading' | 'error' | 'empty'; field?: string; literalPhone?: boolean}>) {
  const fullModel = browseToolbarModel(movies, 'movie', initialPredicates, {field: 'title', direction: 'asc'}, {...env, textEntry: platform !== 'tv'});
  const model = literalPhone ? {...fullModel, groups: fullModel.groups.map(group => ({...group, fields: group.fields.filter(item => (group.id === 'title' ? ['genre', 'year', 'contentRating'] : group.id === 'activity' ? ['favorite', 'watchlisted'] : ['resolution', 'audioLanguage']).includes(item.id))}))} : fullModel;
  const [initialFields] = useState(() => {
    const initial = makeFields(model, platform === 'phone'), genre = initial.genre;
    if (literalPhone && genre?.kind === 'values') {
      const doc = new DOMParser().parseFromString(drawnPhone, 'text/html'), group = doc.querySelector('.fsb .fxo')!;
      const chips = Array.from(group.querySelectorAll('.pill:not(.more)'));
      const values = chips.map((el, index) => ({value: String(index), label: Array.from(el.childNodes).filter(node => node.nodeType === Node.TEXT_NODE).map(node => node.textContent).join('').trim(), count: Number(el.querySelector('small')?.textContent)}));
      if (initial.year?.kind === 'values') initial.year = {...initial.year, included: [], excluded: [], summary: undefined};
      initial.genre = {...genre, values, included: chips.flatMap((el,index) => el.classList.contains('on') ? [String(index)] : []), excluded: chips.flatMap((el,index) => el.classList.contains('no') ? [String(index)] : []), summary: group.querySelector('.v')?.textContent?.trim(), total: 18, moreLabel: group.querySelector('.pill.more')?.textContent?.trim()};
    }
    if (platform === 'tv' && field === 'year' && initial.year?.kind === 'values') {
      const doc = new DOMParser().parseFromString(drawnTv, 'text/html'), list = doc.querySelector('#f4 .flist')!;
      const chips = Array.from(list.querySelectorAll('.fchips .fchip'));
      const values = chips.map((el, index) => ({value: String(index), label: Array.from(el.childNodes).filter(node => node.nodeType === Node.TEXT_NODE).map(node => node.textContent).join('').trim(), count: Number(el.querySelector('em')?.textContent)}));
      initial.year = {...initial.year, values, included: chips.flatMap((el,index) => el.classList.contains('sel') ? [String(index)] : []), excluded: [], summary: list.querySelector('.frow.open .v')?.textContent?.trim(), hint: list.querySelector('.fopen .find')?.textContent?.trim(), total: values.length};
      if (genre?.kind === 'values') initial.genre = {...genre, included: [], excluded: [], summary: undefined};
    }
    if (!literalPhone && !(platform === 'tv' && field === 'year') && genre?.kind === 'values') initial.genre = {...genre, status: state === 'empty' ? 'ready' : state, values: state === 'empty' ? [] : genre.values,
      query: state === 'empty' ? fixture('next.browse.example.documentary') : '', error: state === 'error' ? fixture('next.browse.example.facetError') : undefined};
    return initial;
  });
  const [fields, setFields] = useState(initialFields);
  const [openFields, setOpenFields] = useState<readonly string[]>([field]), [outcome, setOutcome] = useState('');
  const change = (field: string, change: FilterFieldChange) => setFields(current => {
    const previous = current[field]!;
    if (change.kind === 'clear') return {...current, [field]: {...makeFields(model, false)[field]!, summary: undefined, ...(previous.kind === 'values' ? {included: [], excluded: []} : {})}};
    if (change.kind === 'facet' && previous.kind === 'values') {
      const included = [...previous.included.filter(value => value !== change.value), ...(change.selection === 'include' ? [change.value] : [])];
      const excluded = [...previous.excluded.filter(value => value !== change.value), ...(change.selection === 'exclude' ? [change.value] : [])];
      const source = initialFields[field];
      const summary = source?.kind === 'values' ? defaultI18n.list(source.values.filter(value => included.includes(value.value) || excluded.includes(value.value)).map(value => value.label)) : undefined;
      return {...current, [field]: {...previous, included, excluded, summary: summary || undefined}};
    }
    if (change.kind === 'match' && previous.kind === 'values') return {...current, [field]: {...previous, match: change.value}};
    if (change.kind === 'choice' && (previous.kind === 'choice' || previous.kind === 'range')) return {...current, [field]: {...previous, value: change.value}};
    if (change.kind === 'toggle' && previous.kind === 'toggle') return {...current, [field]: {...previous, value: change.value}};
    if (change.kind === 'text' && previous.kind === 'text') return {...current, [field]: {...previous, value: change.value}};
    return current;
  });
  return <div className={styles.filterCrop}><FilterBuilder model={model} labels={labels} platform={platform} fields={fields} openFields={openFields} moreSummary={Object.fromEntries(Array.from(new DOMParser().parseFromString(drawnBrowse, 'text/html').querySelectorAll('#filters .ftiles .more-f .fv')).map((node, index) => [index === 0 ? 'title' : 'file', node.textContent!]))}
    onFieldOpenChange={(field, open) => setOpenFields(current => open ? [...current, field] : current.filter(value => value !== field))} onFieldChange={change}
    onSearchChange={(field, query) => setFields(current => {
      const previous = current[field];
      return previous?.kind === 'values' ? {...current, [field]: {...previous, query, values: initialFields[field]?.kind === 'values' ? initialFields[field].values.filter(value => value.label.toLocaleLowerCase().includes(query.toLocaleLowerCase())) : previous.values}} : current;
    })}
    onShowAll={field => { setOutcome(field); setFields(current => {const previous = current[field]; return previous?.kind === 'values' ? {...current, [field]: {...previous, values: genres, total: genres.length}} : current;}); }}
    onRetry={field => setFields(current => ({...current, [field]: {...current[field]!, status: 'ready', error: undefined}}))}/><output className={styles.outcome}>{outcome}</output></div>;
}
function filterCase(platform: BrowsePlatform, state: 'ready' | 'loading' | 'error' | 'empty' = 'ready', field = 'genre', literalPhone = false): ComponentType { return () => <Stage><FilterSpecimen platform={platform} state={state} field={field} literalPhone={literalPhone}/></Stage>; }

function readFieldsCase(rich: boolean): ComponentType { return function ReadFieldsCase() {
  const [opened, setOpened] = useState<string>();
  const source = new DOMParser().parseFromString(drawnRead, 'text/html');
  const body = source.querySelector(rich ? '#filters .fp-body' : '#filters-s .fp-body')!;
  const fields = Array.from(body.querySelectorAll('.ft')).filter(item => !item.classList.contains('more-f'));
  return <Stage outcome={opened}><div className={styles.readFields} data-fixture-kind="drawn-synthetic">{fields.map((item, index) => <FieldTile forward key={index} label={item.querySelector('.fl')?.textContent ?? ''} summary={item.querySelector('.fv')?.textContent ?? undefined} expanded={opened === String(index)} onClick={() => setOpened(current => current === String(index) ? undefined : String(index))}/>)}</div></Stage>;
};}
function StickyScrollCase() {
  const [library, setLibrary] = useState('movies'), [outcome, setOutcome] = useState('');
  const model = browseToolbarModel(movies, 'movie', [], {field: 'title', direction: 'asc'}, env);
  return <Stage outcome={outcome}><div className={styles.scrollStage} data-scroll-fixture>
    <div className={styles.scrollLead}/><StickyBrowseHeader name={fixture('next.browse.example.movies')} onLibraries={() => setOutcome(t('next.browse.libraries'))}
      switcher={<LibrarySwitcher compact libraries={[{id: 'movies', name: fixture('next.browse.example.movies')}, {id: 'music', name: fixture('next.browse.example.music')}]} value={library} onValueChange={setLibrary}/>}>
      <BrowseToolbar platform="phone" model={model} labels={labels} resultCount={fixture('next.browse.example.movieCount', {count: 80})} onQuickChange={id => setOutcome(id)} onFilters={() => setOutcome(t('next.browse.filters'))} onSortChange={value => setOutcome(value.field)}/>
    </StickyBrowseHeader><div className={styles.scrollRows}>{songs.map(song => <SongRow key={song.id} row={{id: song.id, number: song.trackNumber, text: cardText({id: song.id, kind: 'song', title: song.title, durationSeconds: song.duration}, 'song-row', env)}} onPlay={setOutcome}/>)}</div>
  </div></Stage>;
}
function EmptyCase() {
  const [cleared, setCleared] = useState(false), [removed, setRemoved] = useState('');
  return <Stage outcome={removed}>{cleared ? <div className={styles.controlStage}><Button onClick={() => setCleared(false)}>{t('next.browse.filters')}</Button></div> :
    <FilteredEmpty description={fixture('next.browse.example.emptyDescription')} onClear={() => setCleared(true)} suggestions={[
      {id: 'year', label: fixture('next.browse.example.withoutYear'), count: 6, onSelect: () => setRemoved('year')},
      {id: 'genre', label: fixture('next.browse.example.withoutGenre'), count: 11, onSelect: () => setRemoved('genre')},
    ].filter(suggestion => suggestion.id !== removed)}/>}</Stage>;
}

// These controlled examples reproduce the drawing's invented durations and plays. They are DEV-only,
// never a contract adapter, and are separate from the genuine parsed-response cases above.
function drawnTrackRows(variant: 'album' | 'top-songs' | 'songs', env: Parameters<typeof cardText>[2]): readonly TrackRowData[] {
  const doc = new DOMParser().parseFromString(drawnListen, 'text/html');
  const table = doc.querySelector(variant === 'album' ? '#album .tl' : variant === 'top-songs' ? '#artist .tl' : '#songs .tl')!;
  return Array.from(table.querySelectorAll('.tl-r')).map((row, index) => {
    const title = row.querySelector('.ti b')!.textContent!;
    const cells = Array.from(row.children), clock = cells[variant === 'songs' ? 4 : 3]!.textContent!.trim();
    const [minutes, seconds] = clock.split(':').map(Number), durationSeconds = minutes! * 60 + seconds!;
    const artist = variant === 'songs' ? cells[2]!.textContent!.trim() : undefined;
    const album = variant === 'top-songs' ? row.querySelector('.ti small')?.textContent ?? undefined : variant === 'songs' ? cells[3]!.textContent!.trim() : undefined;
    const image = row.querySelector('img')?.getAttribute('src');
    return {id: `drawn-${variant}-${index}`, number: index + 1, text: cardText({id: `drawn-${variant}-${index}`, kind: 'song', title, artist, album, durationSeconds}, 'song-row', env),
      plays: Number(cells[variant === 'songs' ? 5 : 2]!.textContent), addedAt: variant === 'songs' ? '2026-10-17T12:00:00Z' : undefined,
      playing: row.classList.contains('playing'), favorite: row.classList.contains('faved'),
      artwork: image ? <img className={styles.trackArt} src={`http://100.120.50.24:8787/${image}`} alt=""/> : undefined};
  });
}
function tableCase(variant: 'album' | 'top-songs' | 'songs' | 'chapters' | 'compact' | 'sparse' | 'disc' | 'search', drawn = false, platform: BrowsePlatform = 'desktop'): ComponentType {
  return function TableCase() {
    const sources = useReferenceArt(), [playing, setPlaying] = useState(drawn && (variant === 'album' || variant === 'top-songs' || variant === 'songs') ? drawnTrackRows(variant, env).find(row => row.playing)?.id : variant === 'album' ? songs.find(song => song.title === 'Hardest To Love')?.id : undefined);
    const [favorites, setFavorites] = useState<readonly string[]>(drawn && (variant === 'album' || variant === 'top-songs' || variant === 'songs') ? drawnTrackRows(variant, env).filter(row => row.favorite).map(row => row.id) : playing ? [playing] : []), [more, setMore] = useState<string | undefined>(), [outcome, setOutcome] = useState('');
    const [selectedIds, setSelectedIds] = useState<readonly string[]>([]);
    const [range, setRange] = useState({start: 0, end: 14}), [sort, setSort] = useState({field: 'title', direction: 'asc' as 'asc' | 'desc'});
    const rows: readonly TrackRowData[] = useMemo(() => {
      if (drawn && (variant === 'album' || variant === 'top-songs' || variant === 'songs')) return drawnTrackRows(variant, env).map(row => ({...row, playing: playing ? playing === row.id : row.playing, favorite: favorites.includes(row.id)}));
      if (variant === 'chapters') return Array.from({length: 11}, (_, index) => {
        const current = index === 5, title = fixture(`next.browse.example.chapter${index + 1}` as keyof typeof browseFixtures);
        const duration = [1663, 1335, 1510, 1598, 2387, 2119, 1649, 1263, 1820, 1499, 1415][index]!;
        const remaining = titleFactParts({durationSeconds: duration * .48}, env).find(part => part.id === 'duration')?.text;
        return {id: `reference-chapter-${index + 1}`, number: index + 1, text: cardText({id: `reference-chapter-${index + 1}`, kind: 'chapter', title}, 'ordinary', env),
          done: index < 5, current, progress: current ? .52 : undefined, duration: {id: 'duration', text: fixture(`next.browse.example.chapterDuration${index + 1}` as keyof typeof browseFixtures), role: 'supplement'}, remaining: remaining ? {id: 'remaining', text: t('next.kit.remaining', {time: remaining}), role: 'supplement'} : undefined};
      });
      const selected = variant === 'album' || variant === 'disc' ? songs.filter(song => song.album?.name === 'After Hours') : variant === 'top-songs' ? songs.filter(song => song.album?.name === 'Abbey Road (2019 Mix)') : songs;
      return selected.map((entry, index) => {
        if (entry.kind !== 'song') throw new Error('invalid_song_fixture');
        const album = albums.find(album => album.id === entry.album?.id), source = entry.album && sources.get(entry.album.id);
        return {id: entry.id, number: variant === 'songs' ? index + 1 : entry.trackNumber, text: cardText({id: entry.id, kind: entry.kind, title: entry.title,
          artist: variant === 'sparse' ? undefined : album?.artist?.name, album: variant === 'sparse' ? undefined : entry.album?.name, year: entry.year, durationSeconds: variant === 'sparse' ? undefined : entry.duration}, 'song-row', env),
          plays: entry.plays, addedAt: entry.addedAt ?? undefined, playing: playing === entry.id, favorite: favorites.includes(entry.id),
          artwork: ['top-songs', 'songs', 'compact', 'search'].includes(variant) && source ? <Artwork source={source} alt="" className={styles.trackArt}/> : undefined};
      });
    }, [favorites, playing, sources]);
    const actions = {onPlay: (id: string) => {setPlaying(id); setOutcome(id);}, onMore: setMore, onFavorite: (id: string, favorite: boolean) => setFavorites(current => favorite ? [...current, id] : current.filter(value => value !== id))};
    const selected = rows.find(row => row.id === more);
    const selection = (row: TrackRowData) => <SelectionCircle selected={selectedIds.includes(row.id)} accessibilityLabel={row.text.title} variant="row" onSelectedChange={() => setSelectedIds(current => current.includes(row.id) ? current.filter(id => id !== row.id) : [...current, row.id])}/>;
    return <Stage outcome={outcome}><div data-fixture-kind={drawn ? 'drawn-synthetic' : 'parsed-reference'}>{variant === 'compact' || variant === 'search' ? rows.slice(0, 4).map(row => <SongRow key={row.id} row={row} context={variant === 'search' ? 'search' : 'list'} selection={variant === 'search' ? selection(row) : undefined} onPlay={actions.onPlay} onMore={variant === 'compact' ? undefined : actions.onMore}/>) :
      <TrackTable rows={variant === 'songs' ? rows.slice(range.start, range.end) : rows} variant={variant === 'sparse' || variant === 'disc' ? 'album' : variant} label={t(variant === 'chapters' ? 'next.browse.chapters' : 'next.browse.songs')}
        {...actions} platform={platform} renderSelection={variant === 'songs' ? selection : undefined} discNumber={variant === 'disc' ? 2 : undefined} sort={sort} onSort={field => setSort(current => ({field, direction: field === current.field && current.direction === 'asc' ? 'desc' : 'asc'}))}
        height={variant === 'songs' ? 560 : undefined} startIndex={variant === 'songs' ? range.start : undefined} totalCount={rows.length} onWindowChange={variant === 'songs' ? setRange : undefined}/>}</div>
      {selected && <Popover open onOpenChange={open => {if (!open) setMore(undefined);}} title={selected.text.title} trigger={<Button className={styles.outcome}>{selected.text.title}</Button>}>
        <div className={styles.trackMenu}><Button onClick={() => {actions.onPlay(selected.id); setMore(undefined);}}>{t('next.browse.play', {title: selected.text.title})}</Button>
          <Button onClick={() => {actions.onFavorite(selected.id, !selected.favorite); setMore(undefined);}}>{t(selected.favorite ? 'next.browse.unfavorite' : 'next.browse.favorite', {title: selected.text.title})}</Button></div>
      </Popover>}</Stage>;
  };
}

export default defineGalleryFamily({id: 'browse', labelKey: 'next.browse.family', cases: [
  {id: 'library-header', labelKey: 'next.browse.libraryHeader', width: 1440, height: 244, render: headerCase('desktop')},
  {id: 'library-music', labelKey: 'next.browse.libraryHeader', width: 1440, height: 294, render: headerCase('music')},
  {id: 'library-long', labelKey: 'next.browse.phoneHeader', width: 390, height: 94, render: headerCase('long')},
  {id: 'library-phone', labelKey: 'next.browse.phoneHeader', width: 390, height: 94, render: headerCase('phone')},
  {id: 'library-switcher', labelKey: 'next.browse.switchLibrary', width: 390, height: 450, render: headerCase('switcher')},
  {id: 'toolbar-desktop', labelKey: 'next.browse.toolbarCase', width: 1440, height: 80, render: toolbarCase('desktop')},
  {id: 'toolbar-filtered', labelKey: 'next.browse.toolbarCase', width: 1440, height: 120, render: toolbarCase('desktop', true)},
  {id: 'toolbar-sticky', labelKey: 'next.browse.toolbarCase', width: 1440, height: 100, render: toolbarCase('desktop', true, true)},
  {id: 'toolbar-phone', labelKey: 'next.browse.toolbarCase', width: 390, height: 160, render: toolbarCase('phone', true)},
  {id: 'toolbar-tv', labelKey: 'next.browse.toolbarCase', width: 1624, height: 150, render: toolbarCase('tv', true)},
  {id: 'toolbar-music', labelKey: 'next.browse.toolbarCase', width: 1440, height: 120, render: toolbarCase('desktop', false, false, true)},
  {id: 'sort-menu', labelKey: 'next.browse.sortBy', width: 390, height: 780, render: sortCase()},
  ...(['title', 'random', 'forYou'] as const).map(field => ({id: `sort-${field.toLowerCase()}`, labelKey: 'next.browse.sortBy' as const, width: 390, height: 800, render: sortCase(field)})),
  {id: 'sort-member', labelKey: 'next.browse.sortBy', width: 390, height: 800, render: sortCase('year', false)},
  {id: 'library-sticky-scroll', labelKey: 'next.browse.phoneHeader', width: 390, height: 600, render: StickyScrollCase},
  ...(['actor', 'playState', 'favorite', 'durationMs', 'year'] as const).map(field => ({id: `field-${field.replace(/Ms$/, '').toLowerCase()}`, labelKey: 'next.browse.filtersCase' as const, width: 520, height: 760, render: filterCase('desktop', 'ready', field)})),
  {id: 'filter-phone-drawn', labelKey: 'next.browse.filtersCase', width: 390, height: 610, render: filterCase('phone', 'ready', 'genre', true)},
  {id: 'filter-tv-year', labelKey: 'next.browse.filtersCase', width: 576, height: 746, render: filterCase('tv', 'ready', 'year')},
  ...([false, true] as const).map(rich => ({id: rich ? 'read-fields-full' : 'read-fields-sparse', labelKey: 'next.browse.filtersCase' as const, width: 520, height: 600, render: readFieldsCase(rich)})),
  {id: 'filter-desktop', labelKey: 'next.browse.filtersCase', width: 520, height: 1140, render: filterCase('desktop')},
  {id: 'filter-phone', labelKey: 'next.browse.filtersCase', width: 390, height: 610, render: filterCase('phone')},
  {id: 'filter-tv', labelKey: 'next.browse.filtersCase', width: 576, height: 850, render: filterCase('tv')},
  ...(['loading', 'error', 'empty'] as const).map(state => ({id: `filter-${state}`, labelKey: 'next.browse.filtersCase' as const, width: 520, height: 650, render: filterCase('desktop', state)})),
  {id: 'filtered-empty', labelKey: 'next.browse.emptyCase', width: 1440, height: 650, render: EmptyCase},
  ...(['album', 'top-songs', 'songs'] as const).map(variant => ({id: `drawn-${variant}`, labelKey: 'next.browse.tracksCase' as const, width: variant === 'songs' ? 1308 : 880, height: variant === 'songs' ? 608 : 288, render: tableCase(variant, true)})),
  {id: 'tracks-phone', labelKey: 'next.browse.tracksCase', width: 390, height: 300, render: tableCase('album', true, 'phone')},
  {id: 'chapters-phone', labelKey: 'next.browse.tracksCase', width: 390, height: 676, render: tableCase('chapters', false, 'phone')},
  {id: 'song-search', labelKey: 'next.browse.songCase', width: 625, height: 232, render: tableCase('search')},
  {id: 'tracks-album', labelKey: 'next.browse.tracksCase', width: 880, height: 288, render: tableCase('album')},
  {id: 'tracks-top', labelKey: 'next.browse.tracksCase', width: 880, height: 288, render: tableCase('top-songs')},
  {id: 'tracks-songs', labelKey: 'next.browse.tracksCase', width: 1312, height: 608, render: tableCase('songs')},
  {id: 'tracks-chapters', labelKey: 'next.browse.tracksCase', width: 880, height: 676, render: tableCase('chapters')},
  {id: 'tracks-sparse', labelKey: 'next.browse.tracksCase', width: 880, height: 288, render: tableCase('sparse')},
  {id: 'tracks-disc', labelKey: 'next.browse.tracksCase', width: 880, height: 352, render: tableCase('disc')},
  {id: 'song-row', labelKey: 'next.browse.songCase', width: 416, height: 248, render: tableCase('compact')},
]});
