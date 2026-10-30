import type {BrowseCapabilities} from '../browse.ts';
import type {MessageId} from '../../../i18n/src/index.ts';

/**
 * The tabs of a library, authored (Justin, 2 Oct 2026). Discover is first and the default on
 * every library; there is no Categories tab (genres, decades and studios are filters; Music's
 * Genres tab is genres only). Every client renders these in this order with these names: the
 * server's pivots are vocabulary, not layout.
 */
export type LibraryTabId = 'discover' | 'browse' | 'aired' | 'collections' | 'releases' | 'songs' | 'genres' | 'playlists' | 'authors' | 'series' | 'unmatched';

export type LibraryTabSort = Readonly<{field: string; direction: 'asc' | 'desc'}>;
/** A plain browse predicate, tagged as the server requires (R-09b). */
export type LibraryTabPredicate = Readonly<{variant: 'predicate'; field: string; operator: string; value?: unknown}>;

export type LibraryTab = Readonly<{
  id: LibraryTabId;
  label: MessageId;
  /** The browse pivot the tab reads; Discover and Playlists have none. */
  pivot?: string;
  /** How the tab is drawn: Discover's rows, a browsable grid (or the list table), the songs table, genre cards, the viewer's playlists, or a non-browsable facet surface. */
  shows: 'rows' | 'titles' | 'tracks' | 'genres' | 'playlists' | 'unmatched' | 'facet';
  /** A facet surface's endpoint and accepted value field, exactly as the server's capabilities name them (Root §2). */
  facetSource?: Readonly<{endpoint: string; field: 'author' | 'series'}>;
  /** The order the tab opens in (Recently aired: the latest episode first). A starting point only: the viewer may choose any sort the server offers, and the choice is remembered for the tab. */
  defaultSort?: LibraryTabSort;
  /** Conditions the tab always applies (Unmatched). */
  fixedFilter?: readonly LibraryTabPredicate[];
  /** An aggregate pivot: its entries are groups (genres, series) that open another tab filtered. */
  opens?: Readonly<{tab: LibraryTabId; field: string}>;
}>;

const discover: LibraryTab = {id: 'discover', label: 'library.tab.discover', shows: 'rows'};
const collections: LibraryTab = {id: 'collections', label: 'library.tab.collections', pivot: 'collections', shows: 'titles'};
/** Reading libraries (Root §3). A book library opens the bounded Reading list as Books; a comic
 * library opens its own base grid as Volumes and issues. Series, Authors, Magazines and Newspapers
 * appear only where their accepted classification, facet route and producer exist, so they are not
 * offered here as empty destinations. */
const books: readonly LibraryTab[] = [discover, {id: 'browse', label: 'library.tab.books', shows: 'titles'}];
const volumes: readonly LibraryTab[] = [discover, {id: 'browse', label: 'library.tab.volumesAndIssues', shows: 'titles'}];

const TABS: Readonly<Record<string, readonly LibraryTab[]>> = {
  movie: [discover, {id: 'browse', label: 'library.tab.movies', pivot: 'movies', shows: 'titles'}, collections],
  tv: [discover, {id: 'browse', label: 'library.tab.shows', pivot: 'shows', shows: 'titles'}, {id: 'aired', label: 'library.tab.aired', pivot: 'shows', shows: 'titles', defaultSort: {field: 'latestAired', direction: 'desc'}}, collections],
  music: [
    discover,
    {id: 'browse', label: 'library.tab.artists', pivot: 'artists', shows: 'titles'},
    {id: 'releases', label: 'library.tab.albums', pivot: 'albums', shows: 'titles'},
    {id: 'songs', label: 'library.tab.songs', pivot: 'songs', shows: 'tracks'},
    {id: 'genres', label: 'library.tab.genres', pivot: 'genres', shows: 'genres', opens: {tab: 'releases', field: 'genre'}},
    {id: 'playlists', label: 'library.tab.playlists', pivot: 'playlists', shows: 'playlists'},
  ],
  audiobook: [
    discover,
    {id: 'browse', label: 'library.tab.books', pivot: 'books', shows: 'titles'},
    {id: 'authors', label: 'library.tab.authors', pivot: 'authors', shows: 'titles'},
    {id: 'series', label: 'library.tab.series', pivot: 'series', shows: 'titles', opens: {tab: 'browse', field: 'series'}},
  ],
  book: books,
  comic: volumes,
};

/** The public albums browse with its current attention-backed match filter.
 * It never opens the owner's metadata attention or library management APIs. */
const unmatched: LibraryTab = {id: 'unmatched', label: 'library.tab.unmatched', pivot: 'albums', shows: 'titles', fixedFilter: [{variant: 'predicate', field: 'match', operator: 'equals', value: 'unmatched'}]};

/** The tabs of a library of this kind, in order. Anime has television's. */
/** The owner's last pivot is named like the screen it is the other door to, so it is not
 *  mistaken for the public match filter above. */
const ownerUnmatched: LibraryTab = {id: 'unmatched', label: 'next.ownerLibraries.tab.attention', shows: 'unmatched'};

export function libraryTabs(kind: string, options: Readonly<{owner?: boolean; /** True only from the current public albums capability; absent/offline/local-only is false. */ unmatched?: boolean; /** True only when the current library's own metadata agent read says online; unknown is false. */ onlineAttention?: boolean}> = {}): readonly LibraryTab[] {
  const tabs = TABS[kind === 'anime' ? 'tv' : kind] ?? [discover];
  // An owner's last pivot is the real unmatched list (owner-metadata#5), but only for a library
  // the server actually matches online; a local-only library and Audiobooks never show it. Music
  // keeps its public albums match filter for non-owners only, so no two tabs share the name.
  const owner = options.owner === true && options.onlineAttention === true && kind !== 'audiobook' && ['movie', 'tv', 'anime', 'music'].includes(kind);
  const withPublicUnmatched = !owner && kind === 'music' && options.unmatched===true ? [...tabs, unmatched] : tabs;
  return owner ? [...withPublicUnmatched, ownerUnmatched] : withPublicUnmatched;
}

/** Derive the tab offer from an actual current public browse vocabulary.
 * No cached role, private attention endpoint or missing/offline capability can
 * supply it. Clients recompute this when library/source capabilities change. */
export function canBrowseMusicUnmatched(capabilities:BrowseCapabilities|undefined):boolean{
 if(!capabilities||capabilities.library.kind!=='music'||!capabilities.library.id||capabilities.library.id==='*')return false;
 const albums=capabilities.pivots.find(p=>p.id==='albums'&&p.browsable);
 const match=capabilities.fields.find(f=>f.id==='match'&&f.applicableKinds.includes('album')&&f.operators.includes('equals')&&f.allowedValues?.includes('unmatched'));
 return !!albums&&!!match&&albums.filters?.includes('match')===true;
}

/** Root §3: a reading library's tabs come from the pivots its own capabilities publish, in the
 * server's order — a browsable books/comics pivot and the non-browsable Authors/Series facet
 * surfaces it names. Undefined means this server publishes no reading pivots, so callers keep the
 * authored Discover + Books/Volumes fallback (older servers, and the bounded Reading list). */
export function readingTabModel(capabilities: BrowseCapabilities | undefined): readonly LibraryTab[] | undefined {
  if (!capabilities || (capabilities.library.kind !== 'book' && capabilities.library.kind !== 'comic')) return undefined;
  const tabs: LibraryTab[] = [discover];
  for (const pivot of capabilities.pivots) {
    if (pivot.members) continue;
    if (pivot.browsable && pivot.id === 'books') tabs.push({id: 'browse', label: 'library.tab.books', pivot: pivot.id, shows: 'titles'});
    else if (pivot.browsable && pivot.id === 'comics') tabs.push({id: 'browse', label: 'library.tab.volumesAndIssues', pivot: pivot.id, shows: 'titles'});
    else if (!pivot.browsable && pivot.facetSource?.field === 'author') tabs.push({id: 'authors', label: 'library.tab.authors', shows: 'facet', facetSource: {endpoint: pivot.facetSource.endpoint, field: 'author'}});
    else if (!pivot.browsable && pivot.facetSource?.field === 'series') tabs.push({id: 'series', label: 'library.tab.series', shows: 'facet', facetSource: {endpoint: pivot.facetSource.endpoint, field: 'series'}});
  }
  return tabs.length > 1 ? tabs : undefined;
}

/** The tab an address names, or Discover. */
export function libraryTab(kind: string, id: string | undefined, options?: Parameters<typeof libraryTabs>[1]): LibraryTab {
  const tabs = libraryTabs(kind, options);
  return tabs.find(tab => tab.id === id) ?? tabs[0]!;
}

/** The tab that browses a pivot in a library of this kind (a row's "See all" names a pivot). */
export function libraryTabForPivot(kind: string, pivot: string): LibraryTab | undefined {
  return libraryTabs(kind).find(tab => tab.pivot === pivot && !tab.defaultSort && !tab.fixedFilter);
}

/** The order a tab opens in when the viewer has chosen none: the tab's own when the server offers it, else the server's default for the pivot. */
export function libraryTabSort(tab: LibraryTab, offered: readonly Readonly<{id: string}>[] | undefined, pivotDefault: LibraryTabSort | undefined): LibraryTabSort {
  const own = tab.defaultSort && (!offered || offered.some(sort => sort.id === tab.defaultSort!.field)) ? tab.defaultSort : undefined;
  return own ?? pivotDefault ?? {field: 'title', direction: 'asc'};
}

/**
 * Where a category link leads ("genre:Drama", "decade:1990", "studio:Pixar", "series:Dune"): the
 * library tab that lists those titles, with the category as one ordinary filter. The viewer then
 * has every sort and filter the tab has, and can take the category off like any other filter.
 * Undefined for a category this app does not know.
 */
export function categoryDestination(kind: string, category: string): Readonly<{tab: LibraryTabId; predicate: LibraryTabPredicate}> | undefined {
  const at = category.indexOf(':');
  if (at <= 0 || at === category.length - 1) return undefined;
  const field = category.slice(0, at), value = category.slice(at + 1);
  if (field !== 'genre' && field !== 'decade' && field !== 'studio' && field !== 'series') return undefined;
  if (field === 'decade' && !/^\d{4}$/.test(value)) return undefined;
  const group = libraryTabs(kind).find(tab => tab.opens?.field === field);
  // The same condition the filter's own control writes, so the control shows it ticked.
  const operator = field === 'genre' || field === 'series' ? 'contains-any' : 'in';
  return {tab: group?.opens?.tab ?? 'browse', predicate: {variant: 'predicate', field, operator, value: [field === 'decade' ? Number(value) : value]}};
}

/** An artist's releases by type, in the order the page shows them. A type with nothing is left out. */
export type ReleaseGroup<T> = Readonly<{id: 'albums' | 'singles' | 'compilations' | 'appearances'; label: MessageId; entries: readonly T[]}>;
export function artistReleaseGroups<T extends {role?: string}>(entries: readonly T[]): readonly ReleaseGroup<T>[] {
  const groups: ReleaseGroup<T>[] = [
    {id: 'albums', label: 'title.albums', entries: entries.filter(e => e.role === 'album' || e.role === undefined)},
    {id: 'singles', label: 'title.singlesAndEps', entries: entries.filter(e => e.role === 'single' || e.role === 'ep')},
    {id: 'compilations', label: 'title.compilations', entries: entries.filter(e => e.role === 'compilation')},
    {id: 'appearances', label: 'title.appearsOn', entries: entries.filter(e => e.role === 'appearance')},
  ];
  return groups.filter(g => g.entries.length);
}
