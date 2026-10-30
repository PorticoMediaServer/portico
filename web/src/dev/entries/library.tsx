/**
 * Library browse — the public DEV capture of the registered `/library/$libraryId` route at
 * `design/ui-v1/desktop-browse.html`, 1440 wide: `#grid` (and its list view `#list`), `#big`,
 * `#sort`, `#filters`, `#collections` and `#empty`.
 *
 * The screen is the production router, shell and Library route; this module only supplies the
 * server answers a public fixture would send — the drawn films and collections, the movies
 * library's published vocabulary, its page, counts and counted facets — and records the
 * commands the screen issues. Facts come from the frame and from the server's own published
 * vocabulary (`packages/contracts/src/routes.ts`, `server/internal/catalog/browse*.go`) and the
 * raw captures the frame's data came from (`design/ui-v1/raw/caps_movies.json`,
 * `browse_movies.json`). Artwork slots are one neutral inline image: no frame artwork enters
 * source.
 *
 * The state is chosen by the specimen page's own `?state=`:
 *   grid | list    the eighty drawn films, title order
 *   big            the frame's twenty-one, under a 48,212 count
 *   sort|filters   the six animated films of the 2010s, newest first, two filters on
 *   empty          Horror in the 2010s: nothing matches
 *   collections    the frame's ten series tiles
 *
 * The entry fails closed outside a development build and only mounts over a root element, so a
 * node test can read the same fixture documents without a DOM.
 */
import {defaultI18n} from '@i18n';
import type {BrowseNode, BrowseSortSelection} from '@core/browse.ts';
import type {HttpLocalApi, Library, LocalSession, Viewer} from '@core/index.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {ContentEntry} from '@core/library-content.ts';
import {films, bigTitles, listTitles, collections, type FilmFact} from './library-films.ts';

if (import.meta.env && !import.meta.env.DEV) throw new Error('Library specimens require the development server.');

export type LibraryRecord = Readonly<{kind: string; at: number; value?: unknown}>;
export type LibraryState = 'grid' | 'list' | 'big' | 'sort' | 'filters' | 'empty' | 'collections';

const libraryId = 'movies', serverId = 'specimen-server', revisionToken = 'browse-r46-library';
const revision = {catalog: 1, viewer: 1};
const allKinds = ['movie', 'collection'];
const dated = ['movie', 'collection'];
const kindOf = (id: string): 'movie' | 'collection' => id === 'collections' ? 'collection' : 'movie';

/* ---------- what the frame's states ask the library for ---------- */
const predicate = (field: string, operator: string, value: unknown): BrowseNode => Object.freeze({variant: 'predicate', field, operator, value}) as BrowseNode;
const animated = predicate('genre', 'contains-any', ['Animation']);
const animated2010s = Object.freeze({variant: 'group', all: Object.freeze([animated, predicate('decade', 'equals', 2010)])});
const horror2010s = Object.freeze({variant: 'group', all: Object.freeze([predicate('genre', 'contains-any', ['Horror']), predicate('decade', 'equals', 2010)])});
const newestFirst: readonly BrowseSortSelection[] = Object.freeze([Object.freeze({field: 'year', direction: 'desc' as const})]);

/** The route this specimen's state opens; the router validates it like any other arrival. */
export function entryPath(state: LibraryState): string {
  if (state === 'collections') return `/library/${libraryId}?view=browse&pivot=collections`;
  const search: string[] = ['view=browse', 'pivot=movies'];
  if (state === 'sort' || state === 'filters') {
    search.push(`query=${encodeURIComponent(JSON.stringify(animated2010s))}`, `sort=${encodeURIComponent(JSON.stringify(newestFirst))}`);
  }
  if (state === 'empty') search.push(`query=${encodeURIComponent(JSON.stringify(horror2010s))}`);
  return `/library/${libraryId}?${search.join('&')}`;
}

/* ---------- the published vocabulary: a movies library's own ---------- */
type FieldRow = readonly [id: string, labelKey: string, type: string, operators: readonly string[], controlHint: string, complexity: string, cost: string,
  extra: Readonly<{allowed?: readonly string[]; facet?: string}>];
const fieldRows: readonly FieldRow[] = [
  ['genre', 'browse.field.genre', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'quick', 'indexed-join', {facet: 'genre'}],
  ['year', 'browse.field.year', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'quick', 'indexed', {}],
  ['decade', 'browse.field.decade', 'number', ['equals', 'not-equals', 'in', 'not-in', 'between'], 'facet-multi-select', 'quick', 'indexed', {facet: 'decade'}],
  ['contentRating', 'browse.field.contentRating', 'string', ['equals', 'not-equals', 'in', 'not-in', 'is-present'], 'facet-multi-select', 'quick', 'indexed-join', {facet: 'contentRating'}],
  ['studio', 'browse.field.studio', 'string', ['equals', 'contains', 'in', 'is-present'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'studio'}],
  ['collection', 'browse.field.collection', 'identity-set', ['contains', 'in', 'not-in'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'collection'}],
  ['actor', 'browse.field.actor', 'identity-set', ['contains', 'in'], 'text', 'advanced', 'indexed-join', {}],
  ['director', 'browse.field.director', 'identity-set', ['contains', 'in'], 'text', 'advanced', 'indexed-join', {}],
  ['writer', 'browse.field.writer', 'identity-set', ['contains', 'in'], 'text', 'advanced', 'indexed-join', {}],
  ['tag', 'browse.field.tag', 'identity-set', ['contains', 'in', 'not-in'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'tag'}],
  ['label', 'browse.field.label', 'identity-set', ['contains', 'in', 'not-in'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'label'}],
  ['communityRating', 'browse.field.communityRating', 'number', ['equals', 'at-least', 'at-most', 'between', 'is-present'], 'number-range', 'standard', 'indexed-join', {}],
  ['criticRating', 'browse.field.criticRating', 'number', ['equals', 'at-least', 'at-most', 'between'], 'number-range', 'standard', 'indexed-join', {}],
  ['releaseDate', 'browse.field.releaseDate', 'date', ['equals', 'between', 'is-present'], 'date-range', 'standard', 'indexed-join', {}],
  ['playState', 'browse.field.playState', 'enum', ['equals', 'not-equals', 'in', 'not-in'], 'select', 'quick', 'indexed-join', {allowed: ['unplayed', 'in-progress', 'played']}],
  ['favorite', 'browse.field.favorite', 'boolean', ['equals', 'not-equals'], 'toggle', 'quick', 'indexed-join', {}],
  ['watchlisted', 'browse.field.watchlisted', 'boolean', ['equals', 'not-equals'], 'toggle', 'quick', 'indexed-join', {}],
  ['personalRating', 'browse.field.personalRating', 'number', ['equals', 'at-least', 'between'], 'number-range', 'standard', 'indexed-join', {}],
  ['lastPlayedAt', 'browse.field.lastPlayedAt', 'date', ['equals', 'at-least', 'between', 'is-present'], 'date-range', 'standard', 'indexed-join', {}],
  ['resolution', 'browse.field.resolution', 'enum', ['equals', 'in', 'not-in'], 'facet-multi-select', 'standard', 'indexed-join', {allowed: ['sd', '720p', '1080p', '4k'], facet: 'resolution'}],
  ['dynamicRange', 'browse.field.dynamicRange', 'identity-set', ['contains', 'in'], 'facet-multi-select', 'standard', 'indexed-join', {}],
  ['audioLanguage', 'browse.field.audioLanguage', 'identity-set', ['contains', 'in'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'audioLanguage'}],
  ['durationMs', 'browse.field.durationSeconds', 'number', ['at-least', 'at-most', 'between'], 'number-range', 'standard', 'indexed-join', {}],
  ['dateAdded', 'browse.field.dateAdded', 'date', ['equals', 'at-least', 'between'], 'date-range', 'standard', 'indexed', {}],
  ['availability', 'browse.field.availability', 'enum', ['equals', 'not-equals'], 'select', 'quick', 'indexed-join', {allowed: ['available', 'partial', 'unavailable']}],
];
const fields = fieldRows.map(([id, labelKey, type, operators, controlHint, complexity, cost, extra]) => Object.freeze({id, labelKey, type, operators,
  controlHint, complexity, cost, applicableKinds: allKinds, ...(extra.allowed ? {allowedValues: extra.allowed} : {}),
  ...(extra.facet ? {facetSource: {endpoint: '/v1/browse/facets', field: extra.facet}} : {})}));
const filterIds = fieldRows.map(([id]) => id);
const sortRows = [
  // The server's own movies menu: For you is expensive; Random is the seeded shuffle.
  {id: 'forYou', labelKey: 'sort.forYou', defaultDirection: 'desc' as const}, {id: 'title', labelKey: 'sort.title', defaultDirection: 'asc' as const}, {id: 'added', labelKey: 'sort.added', defaultDirection: 'desc' as const},
  {id: 'year', labelKey: 'sort.year', defaultDirection: 'desc' as const}, {id: 'duration', labelKey: 'sort.duration', defaultDirection: 'desc' as const},
  {id: 'communityRating', labelKey: 'sort.communityRating', defaultDirection: 'desc' as const}, {id: 'criticRating', labelKey: 'sort.criticRating', defaultDirection: 'desc' as const},
  {id: 'personalRating', labelKey: 'sort.personalRating', defaultDirection: 'desc' as const}, {id: 'lastPlayed', labelKey: 'sort.lastPlayed', defaultDirection: 'desc' as const},
  {id: 'playCount', labelKey: 'sort.playCount', defaultDirection: 'desc' as const}, {id: 'resolution', labelKey: 'sort.resolution', defaultDirection: 'desc' as const},
  {id: 'releaseDate', labelKey: 'sort.releaseDate', defaultDirection: 'desc' as const}, {id: 'random', labelKey: 'sort.random', defaultDirection: 'asc' as const},
];
const sorts = sortRows.map(row => Object.freeze({...row, directions: ['asc', 'desc'], expensive: row.id === 'forYou', seeded: row.id === 'random',
  requiresGroup: false, applicableKinds: allKinds}));
const quickFilters = Object.freeze([
  {id: 'unwatched', labelKey: 'filter.unwatched', query: {variant: 'predicate', field: 'playState', operator: 'equals', value: 'unplayed'}},
  {id: 'in-progress', labelKey: 'filter.inProgress', query: {variant: 'predicate', field: 'playState', operator: 'equals', value: 'in-progress'}},
  {id: 'favorites', labelKey: 'filter.favorites', query: {variant: 'predicate', field: 'favorite', operator: 'equals', value: true}},
  {id: 'watchlist', labelKey: 'filter.watchlist', query: {variant: 'predicate', field: 'watchlisted', operator: 'equals', value: true}},
  {id: 'watched', labelKey: 'filter.watched', query: {variant: 'predicate', field: 'playState', operator: 'equals', value: 'played'}},
  {id: 'available', labelKey: 'filter.available', query: {variant: 'predicate', field: 'availability', operator: 'equals', value: 'available'}},
  {id: 'missing', labelKey: 'filter.missing', query: {variant: 'predicate', field: 'availability', operator: 'equals', value: 'unavailable'}},
].map(value => Object.freeze(value)));
const pivotRows = [
  {id: 'discover', labelKey: 'library.tab.discover', entityKinds: ['movie'], supportedViews: ['rail'], browsable: false, paging: 'keyset', defaultSort: [{field: 'title', direction: 'asc'}]},
  {id: 'movies', labelKey: 'library.tab.movies', entityKinds: ['movie'], supportedViews: ['grid', 'list'], browsable: true, paging: 'keyset', defaultSort: [{field: 'title', direction: 'asc'}]},
  {id: 'collections', labelKey: 'library.tab.collections', entityKinds: ['collection'], supportedViews: ['grid', 'list'], browsable: true, paging: 'keyset', defaultSort: [{field: 'title', direction: 'asc'}]},
];
const custom = {
  library: Object.freeze({id: libraryId, name: 'Movies', kind: 'movie', defaultView: 'discover', pinned: false}),
  pivots: Object.freeze(pivotRows.map(row => Object.freeze({...row, sorts: sortRows.map(sort => sort.id), filters: filterIds}))),
  fields: Object.freeze(fields), sorts: Object.freeze(sorts), quickFilters,
  queryLimits: Object.freeze({maximumDepth: 5, maximumClauses: 40, maximumBytes: 65_536, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1_800_000}),
};

/* ---------- the entries the frame draws ---------- */
const slug = (title: string) => title.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
const filmById = new Map(films.map(fact => [fact[0], fact]));
function filmEntry(fact: FilmFact): ContentEntry {
  const [title, year, minutes, rating, genre, watched, progress] = fact;
  const id = slug(title);
  return Object.freeze({id, kind: 'movie', libraryId, title, year, genres: Object.freeze([genre]), available: true, missing: false,
    watched: watched === true, ...(progress === undefined ? {} : {progressSeconds: Math.round(minutes * 60 * progress)}),
    duration: minutes * 60, ...(rating === undefined ? {} : {rating}), resolution: 'sd' as const, contentRating: 'PG-13',
    addedAt: '2026-10-17T03:10:02.549Z', favorite: false, watchlisted: false, posterUrl: `specimen-art://poster/${id}`,
    navigation: Object.freeze({view: 'item' as const, entityId: id}), playback: Object.freeze({itemId: id, startSeconds: 0})});
}
/** A collection's tile: its films' years, its watched count and its own banner. */
function collectionEntry(fact: (typeof collections)[number]): ContentEntry {
  const id = slug(fact.name), covers = films.slice(0, Math.min(4, fact.films)).map(film => `specimen-art://poster/${slug(film[0])}`);
  return Object.freeze({id: `collection-${id}`, kind: 'collection', libraryId, title: fact.name, count: fact.films, yearMin: fact.from, yearMax: fact.to,
    ...(fact.watched === undefined ? {} : {watchedCount: fact.watched, unwatchedCount: fact.films - fact.watched}),
    origin: 'automatic', available: true, missing: false, watched: fact.watched === undefined ? false : fact.watched >= fact.films,
    addedAt: '2026-10-17T03:10:02.549Z', artworkPaths: Object.freeze(covers), backdropUrl: `specimen-art://backdrop/${id}`,
    posterUrl: `specimen-art://poster/${id}`, navigation: Object.freeze({view: 'browse' as const, category: id}),
    playback: Object.freeze({itemId: `collection-${id}`, startSeconds: 0})});
}
const movieEntries = Object.freeze(films.map(filmEntry));
const collectionEntries = Object.freeze(collections.map(collectionEntry));
const unfilmed: FilmFact = ['Untitled', 2026, 100, undefined, 'Drama'];
const bigEntries = Object.freeze(bigTitles.map(title => filmEntry(filmById.get(title) ?? unfilmed)));
const listEntries = Object.freeze(listTitles.map(title => filmEntry(filmById.get(title) ?? unfilmed)));

/** What the specimen's own state answers with, and how many the library says it holds. */
function catalogue(state: LibraryState) {
  if (state === 'collections') return {entries: collectionEntries, total: collectionEntries.length, totalIsLowerBound: false};
  if (state === 'big') return {entries: bigEntries, total: 48_212, totalIsLowerBound: false};
  if (state === 'list') return {entries: listEntries, total: 80, totalIsLowerBound: false};
  return {entries: movieEntries, total: 80, totalIsLowerBound: false};
}

/* ---------- the page: the fixture's own semantics over its facts ---------- */
const decadeOf = (year: number | undefined) => year === undefined ? undefined : Math.floor(year / 10) * 10;
function matches(entry: ContentEntry, node: BrowseNode | null | undefined): boolean {
  if (!node) return true;
  if ('all' in node) return node.all.every(child => matches(entry, child));
  if ('any' in node) return node.any.some(child => matches(entry, child));
  if ('not' in node) return !matches(entry, node.not);
  const {field, operator} = node, value = node.value;
  const playState = entry.watched ? 'played' : entry.progressSeconds !== undefined ? 'in-progress' : 'unplayed';
  const one = (expected: unknown) => {
    switch (field) {
      case 'genre': case 'tag': case 'label': case 'collection': return typeof expected === 'string' && (entry.genres ?? []).some(genre => genre.toLowerCase() === expected.toLowerCase());
      case 'year': return Number(expected) === entry.year;
      case 'decade': return Number(expected) === decadeOf(entry.year);
      case 'playState': return expected === playState;
      case 'favorite': return expected === entry.favorite;
      case 'watchlisted': return expected === entry.watchlisted;
      case 'availability': return expected === 'available' ? entry.available !== false : expected === 'unavailable' ? entry.missing === true : false;
      case 'resolution': return expected === entry.resolution;
      case 'contentRating': return expected === entry.contentRating;
      case 'communityRating': return Number(expected) === entry.rating;
      case 'durationMs': return Number(expected) === (entry.duration === undefined ? undefined : entry.duration * 1000);
      case 'entityKind': return expected === entry.kind;
      default: return false;
    }
  };
  const expected = Array.isArray(value) ? value : [value];
  switch (operator) {
    case 'equals': case 'in': case 'contains': case 'contains-any': case 'contains-all': return expected.some(one);
    case 'not-equals': case 'not-in': return !expected.some(one);
    case 'between': return expected.length === 2 && typeof entry.year === 'number' && entry.year >= Number(expected[0]) && entry.year <= Number(expected[1]);
    case 'is-present': return field === 'genre' ? (entry.genres ?? []).length > 0 : entry.year !== undefined;
    case 'is-missing': return field === 'genre' ? (entry.genres ?? []).length === 0 : entry.year === undefined;
    default: return false;
  }
}
function sortBy(selection: BrowseSortSelection | undefined) {
  const key = selection?.field;
  // Title order is the page's own alphabetical order, as the frame draws it; a seeded shuffle is the server's.
  if (!key || key === 'title' || key === 'added' || key === 'random') return () => 0;
  const sign = selection?.direction === 'desc' ? -1 : 1;
  return (a: ContentEntry, b: ContentEntry) => {
    const left = key === 'duration' ? a.duration : key === 'communityRating' ? a.rating : (a as unknown as Record<string, unknown>)[key];
    const right = key === 'duration' ? b.duration : key === 'communityRating' ? b.rating : (b as unknown as Record<string, unknown>)[key];
    const order = typeof left === 'number' && typeof right === 'number' ? left - right : String(left).localeCompare(String(right));
    return order * sign;
  };
}
const anchorLetters = (entries: readonly ContentEntry[]) => {
  const index: {key: string; index: number}[] = [];
  entries.forEach((entry, position) => {
    const key = entry.sortLetter ?? entry.title.replace(/^(the|a|an)\s+/i, '')[0]!.toUpperCase();
    if (!index.some(anchor => anchor.key === key)) index.push({key, index: position});
  });
  return index;
};
export type BrowseRequest = Readonly<{query?: BrowseNode | null; sort?: readonly BrowseSortSelection[]; limit?: number; range?: Readonly<{start: number}>}>;

export function createLibrarySpecimen(state: LibraryState) {
  const records: LibraryRecord[] = [];
  const record = (kind: string, value?: unknown): void => { records.push(Object.freeze({kind, at: Date.now(), ...(value === undefined ? {} : {value})})); };
  const built = catalogue(state);
  const page = (body: BrowseRequest) => {
    const start = body.range?.start ?? 0, limit = body.limit ?? 60;
    const kept = built.entries.filter(candidate => matches(candidate, body.query ?? null)).sort(sortBy(body.sort?.[0]));
    const slice = kept.slice(start, start + limit);
    // The drawn 15-row list is one page of the library's drawn 80; only a query narrows the total.
    const total = body.query ? kept.length : built.total;
    record('browse', {start, limit, total, sort: body.sort?.[0]?.field ?? 'title'});
    return {pivot: state === 'collections' ? 'collections' : 'movies', applied: {query: body.query ?? null, sort: body.sort ?? [], seek: null}, entries: slice,
      pageInfo: {start, total, totalIsLowerBound: built.totalIsLowerBound, revision: revisionToken,
        hasMore: start + slice.length < total, nextCursor: ''},
      positionIndex: (body.sort?.[0]?.field ?? 'title') === 'title' || !body.sort?.length ? anchorLetters(kept) : []};
  };
  const counts = (body: Readonly<{queries: readonly Readonly<{id: string; query: BrowseNode | null}>[]}>) => ({
    counts: body.queries.map(group => ({id: group.id, total: group.query ? built.entries.filter(candidate => matches(candidate, group.query)).length : built.total,
      totalIsLowerBound: built.totalIsLowerBound, revision: revisionToken})),
  });
  const facets = (url: URL) => {
    const field = url.searchParams.get('field') ?? '', values: {value: string; label: string; count: number}[] = [];
    // The panel asks for counts inside the filters already applied ("6 movies match").
    const query = url.searchParams.get('query') ? JSON.parse(url.searchParams.get('query')!) as BrowseNode : null;
    const scope = (candidate: ContentEntry) => matches(candidate, query);
    const tally = (of: (item: ContentEntry) => string | number | undefined) => built.entries.forEach(candidate => {
      if (!scope(candidate)) return;
      const key = of(candidate);
      if (key === undefined) return;
      const found = values.find(value => value.value === String(key));
      if (found) found.count += 1; else values.push({value: String(key), label: String(key), count: 1});
    });
    if (field === 'genre') built.entries.forEach(candidate => {if (!scope(candidate)) return;(candidate.genres ?? []).forEach(genre => {
      const found = values.find(value => value.value === genre);
      if (found) found.count += 1; else values.push({value: genre, label: genre, count: 1});
    });});
    else if (field === 'decade') tally(candidate => decadeOf(candidate.year));
    else if (field === 'resolution') tally(candidate => candidate.resolution);
    else if (field === 'studio') tally(() => undefined);
    record('facets', {field});
    // The published total is the number of choices, not the sum of their counts.
    return {field, total: values.length, values, revision: revisionToken};
  };
  const request = async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (signal?.aborted) throw Object.assign(new Error('aborted'), {name: 'AbortError'});
    const route = path.split('?')[0]!, url = new URL(path, 'http://specimen.invalid');
    record('request', {path: route, method});
    if (method === 'GET' && route === `/v1/libraries/${libraryId}/browse-capabilities`) {
      const pivot = url.searchParams.get('pivot') ?? 'movies', resolved = custom.pivots.find(row => row.id === pivot) ?? custom.pivots[1]!;
      return {...custom, library: custom.library, resolvedPivot: resolved} as T;
    }
    if (method === 'POST' && route === `/v1/libraries/${libraryId}/browse`) return page(body as BrowseRequest) as T;
    if (method === 'POST' && route === `/v1/libraries/${libraryId}/browse/counts`) return counts(body as Parameters<typeof counts>[0]) as T;
    if (method === 'GET' && route === `/v1/libraries/${libraryId}/facets`) return facets(url) as T;
    record('request-unanswered', {path: route, method});
    throw Object.assign(new Error(`the library specimen has no answer for ${method} ${route}`), {code: 'not_found', status: 404});
  };
  return Object.freeze({serverId, libraryId, state, records, record, request, capabilities: custom, page, counts, entries: built.entries, total: built.total});
}


/** A specimen records the failure's stable code for its own diagnostics; the
 * server's error text is never rendered or kept as specimen output. */
const failureCode = (error: unknown): string => {
  const code = error && typeof error === 'object' && 'code' in error ? (error as {code?: unknown}).code : undefined;
  return typeof code === 'string' && /^[a-z0-9_]{1,64}$/.test(code) ? code : 'error';
};

/* ---------- the entry: production router, shell and route ---------- */
const root = typeof document === 'undefined' ? null : document.getElementById('root');
const state = (typeof location === 'undefined' ? 'grid' : new URLSearchParams(location.search).get('state') ?? 'grid') as LibraryState;
if (root) void mount(root);

async function mount(element: HTMLElement) {
  const [{createRoot}, {RouterProvider, createMemoryHistory}, {defaultI18n}, {router}, {WebScreenProvider}, {ShellBindingsProvider},
    {createOwnerActivity}, {createArtworkRuntime}, {IconSprite}] = await Promise.all([
    import('react-dom/client'), import('@tanstack/react-router'), import('@i18n'), import('../../router'),
    import('../../route-registry-view'), import('../../shell/bindings'), import('../../shell/Activity'),
    import('../../kit/artwork/source'), import('../../kit/Icon')]);
  await Promise.all([import('../../kit/tokens.css'), import('../../kit/fonts.css'), import('../../bootstrap.css')]);
  const specimen = createLibrarySpecimen(state);
  const record = specimen.record;
  const time = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
  const viewer: Viewer = {serverId: specimen.serverId, authority: 'local', accountId: 'specimen-account', profileId: 'demo', role: 'owner'};
  const session: LocalSession = {
    accessToken: 'specimen-public', refreshToken: '', sessionFamilyId: 'specimen-family', tokenGeneration: '1',
    installationId: 'i'.repeat(43), expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer,
  };
  const libraries: readonly Library[] = [
    {id: libraryId, name: 'Movies', kind: 'movie'}, {id: 'tv', name: 'TV Shows', kind: 'tv'}, {id: 'anime', name: 'Anime', kind: 'anime'},
    {id: 'music', name: 'Music', kind: 'music'}, {id: 'audiobooks', name: 'Audiobooks', kind: 'audiobook'},
  ];
  const identity = {
    viewer, username: 'demo', profileName: 'Demo', profileArt: 'blue', serverName: 'Portico demo',
    primaryProfile: true, profiles: [], canManageProfiles: false, accountFacts: [], libraries, liveAvailable: true,
    unread: 3, showNotificationDot: true,
    capabilities: {manageAccount: false, managePeople: false, manageServer: true, manageSignIn: false},
  };
  const image = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR42mOoctMBAAIqAO0ezPdVAAAAAElFTkSuQmCC';
  const runtime = createArtworkRuntime({
    scopeKey: `library-${state}-specimen`, classify: () => ({retryable: false}),
    load: async (_request, signal) => {
      const response = await fetch(image, {signal});
      const blob = await response.blob(), bitmap = await createImageBitmap(blob);
      const width = bitmap.width, height = bitmap.height;
      bitmap.close();
      return {src: URL.createObjectURL(blob), bytes: blob.size, width, height};
    },
    release: value => { try { URL.revokeObjectURL(value.src); } catch { /* A closed page owns nothing. */ } },
  });
  const artwork = {...runtime,
    foreground: (url?: string) => url ? runtime.source({url: image, cacheKey: `foreground:${url}`}) : undefined,
    ambient: (url?: string) => url ? runtime.ambient({url: image, cacheKey: `ambient:${url}`, variant: 'ambient-v1', maxEdge: 64, version: 'a'.repeat(64)}) : undefined,
  };
  const preferences = {
    deviceClass: 'web', registry: {fields: [], revision: 'library-specimen'}, documents: [], clampedFields: [], registryRevision: 'library-specimen',
    effectiveSource: {}, subscribe: () => () => {},
    effective: {'appearance.showBackdrops': true, 'appearance.cardSizePercent': 100, 'appearance.libraryView': state === 'list' ? 'list' : 'grid',
      'appearance.motion': 'follow', 'appearance.textSize': 'standard', 'appearance.layout': 'auto', 'appearance.episodesLayout': 'auto'},
  } as unknown as PreferenceSnapshot;
  const events = {on: () => () => {}, onResync: () => () => {}, onAny: () => () => {}, getStatus: () => ({connected: true}), subscribeStatus: () => () => {}};
  const api = {
    request: specimen.request, requestRaw: async () => ({status: 404, headers: {}, body: {error: {code: 'not_found'}}}),
    getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {},
    reading: () => { const life = new AbortController(); return {signal: life.signal, versions: async () => ({versions: []}), dispose: () => life.abort()}; },
    dispose: () => runtime.dispose(),
  } as unknown as HttpLocalApi;
  const contextBase = {
    api, session: () => session, events, artwork, facts: {identity, preferences, serverName: identity.serverName},
    preferences: {getSnapshot: () => preferences, subscribe: () => () => {}},
    epoch: 1, isCurrent: () => true, signOut: () => record('sign-out'), signOutPorticoAccount: () => record('sign-out-portico'),
    switchProfile: async () => {}, switchServer: async () => {}, savedServers: [],
  };
  const activity = createOwnerActivity(contextBase as never, time, () => record('open-dashboard'), () => record('open-update'), issue => record('open-issue', issue));
  const playback = {artwork, environment: time, skips: {back: 10, forward: 30}, reportError: (error: unknown) => record('report-error', failureCode(error)),
    runtime: {playFromQueue: async (source: unknown, _context: unknown, options: {shuffle?: boolean}) => { record('play-from-queue', {source, shuffle: options.shuffle === true}); },
      cataloguePlayback: () => undefined}};
  const home = {
    service: {getSnapshot: () => ({phase: 'idle'}), subscribe: () => () => {}}, artwork, time, serverName: identity.serverName, reducedMotion: true,
    selection: {toolbar: null, card: () => undefined, channel: () => undefined, collection: () => undefined, registerWindow: () => undefined,
      registerSpotlight: () => undefined, moreAvailable: () => false, moreExpanded: () => false},
    effects: {play: async () => record('play'), playChannel: async () => {}, openEntry: () => record('open-entry'), openSeeAll: () => record('open-see-all'),
      openMore: () => record('open-more'), setMyList: async () => {}, reportError: (error: unknown) => record('report-error', failureCode(error))},
  };
  const binding = {api, events, scopeKey: `library-${state}-specimen`, playback,
    shell: {identity, OwnerActivity: activity.OwnerActivity, dockVisible: false, onSignOut: () => record('sign-out')}, home};
  router.update({history: createMemoryHistory({initialEntries: [entryPath(state)]})});
  createRoot(element).render(<WebScreenProvider value={{binding, context: contextBase} as never}>
    <IconSprite />
    <ShellBindingsProvider value={binding as never}>
      <RouterProvider router={router} />
    </ShellBindingsProvider>
  </WebScreenProvider>);
  // The fixture and the commands the screen issued are inspectable from the page; no credentials.
  (window as Window & {__porticoLibrarySpecimen?: unknown}).__porticoLibrarySpecimen = {state, records: specimen.records, capabilities: specimen.capabilities, total: specimen.total};
}
