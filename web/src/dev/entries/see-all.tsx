/**
 * See all: Recommended for you — the public DEV capture of the registered Home row route
 * (`/home/rows/$rowId`) at `design/ui-v1/desktop-browse.html#seeall`, 1440 wide.
 *
 * The screen is the production router, shell and HomeRow route; this module only supplies the
 * server answers a public fixture would send (the drawn row, the across-libraries `titles`
 * vocabulary, its canonical page and its counts) and records the commands the screen issues.
 * Facts come from the frame and from the server's own published vocabulary
 * (server/internal/catalog/browse_across.go, browse_contract.go) and captures
 * (design/ui-v1/raw/caps_movies.json, caps_tv.json, browse_movies.json, browse_tv.json,
 * browse_anime.json). Artwork slots are one neutral inline image: no frame artwork enters source.
 *
 * The entry fails closed outside a development build and only mounts over a root element, so a
 * node test can read the same fixture documents without a DOM.
 */
import {defaultI18n} from '@i18n';
import type {BrowseNode, BrowseSortSelection} from '@core/browse.ts';
import type {HttpLocalApi, Library, LocalSession, Viewer} from '@core/index.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';

if (import.meta.env && !import.meta.env.DEV) throw new Error('See all specimens require the development server.');

export type SeeAllRecord = Readonly<{kind: string; at: number; value?: unknown}>;

const serverId = 'specimen-server';
export const rowId = 'recommended';
const viewerFence = 'specimen-viewer-fence';
const revision = {catalog: 1, viewer: 1};
const revisionToken = 'browse-r46-see-all';

/* ---------- the drawn page: 49 films, 16 shows and 7 anime, in the frame's order ---------- */
const allKinds = ['movie', 'episode', 'show', 'season', 'collection', 'song', 'album', 'artist', 'audiobook_file', 'book', 'author'];
const datedKinds = ['movie', 'episode', 'show', 'season', 'album', 'song', 'book', 'audiobook_file'];
const releasedKinds = ['movie', 'episode', 'show', 'season', 'album', 'book'];
const videoArtKinds = ['movie', 'episode', 'show', 'season', 'collection'];
const playedKinds = ['movie', 'episode', 'show', 'season', 'collection', 'song', 'album', 'artist', 'audiobook_file', 'book'];
const airedKinds = ['show', 'season'];

type Kind = 'movie' | 'show';
type LibraryId = 'movies' | 'tv' | 'anime';
type Title = readonly [title: string, year: number, kind: Kind, library: LibraryId];
const titles: readonly Title[] = [
  ['JUJUTSU KAISEN', 2020, 'show', 'anime'],
  ['Toy Story 3', 2010, 'movie', 'movies'],
  ['My Hero Academia', 2016, 'show', 'anime'],
  ['Forrest Gump', 1994, 'movie', 'movies'],
  ['Spider-Man: Into the Spider-Verse', 2018, 'movie', 'movies'],
  ['One Night Only', 2026, 'movie', 'movies'],
  ['Drawn Together', 2026, 'movie', 'movies'],
  ['The Mandalorian', 2019, 'show', 'tv'],
  ["One Flew Over the Cuckoo's Nest", 1975, 'movie', 'movies'],
  ['Spirited Away', 2001, 'movie', 'movies'],
  ['The Godfather', 1972, 'movie', 'movies'],
  ['Toy Story 2', 1999, 'movie', 'movies'],
  ['Demon Slayer: Kimetsu no Yaiba', 2019, 'show', 'anime'],
  ['You+Me - Against the World', 2026, 'movie', 'movies'],
  ['The Godfather Part II', 1974, 'movie', 'movies'],
  ['Spider-Man: Across the Spider-Verse', 2023, 'movie', 'movies'],
  ['Practical Magic 2', 2026, 'movie', 'movies'],
  ['WALL·E', 2008, 'movie', 'movies'],
  ['Stranger Things', 2016, 'show', 'tv'],
  ['12 Angry Men', 1957, 'movie', 'movies'],
  ['Alien', 1979, 'movie', 'movies'],
  ['Aliens', 1986, 'movie', 'movies'],
  ['Alpha', 2026, 'movie', 'movies'],
  ['Avatar', 2009, 'movie', 'movies'],
  ['The Avengers', 2012, 'movie', 'movies'],
  ['Avengers: Endgame', 2019, 'movie', 'movies'],
  ['Avengers: Infinity War', 2018, 'movie', 'movies'],
  ['Back to the Future', 1985, 'movie', 'movies'],
  ['Backrooms', 2026, 'movie', 'movies'],
  ['Barbie', 2023, 'movie', 'movies'],
  ['Blade Runner', 1982, 'movie', 'movies'],
  ['Coyote vs. Acme', 2026, 'movie', 'movies'],
  ['The Dark Knight', 2008, 'movie', 'movies'],
  ['Dune', 2021, 'movie', 'movies'],
  ['Dune: Part Two', 2024, 'movie', 'movies'],
  ['The Empire Strikes Back', 1980, 'movie', 'movies'],
  ['The End of Oak Street', 2026, 'movie', 'movies'],
  ['Fight Club', 1999, 'movie', 'movies'],
  ['The Fix', 2026, 'movie', 'movies'],
  ['Gladiator', 2000, 'movie', 'movies'],
  ['GoodFellas', 1990, 'movie', 'movies'],
  ['Grave of the Fireflies', 1988, 'movie', 'movies'],
  ['The Green Mile', 1999, 'movie', 'movies'],
  ['Hope', 2026, 'movie', 'movies'],
  ['How to Train Your Dragon', 2010, 'movie', 'movies'],
  ['Inception', 2010, 'movie', 'movies'],
  ['The Incredibles', 2004, 'movie', 'movies'],
  ['Incredibles 2', 2018, 'movie', 'movies'],
  ['Inside Out', 2015, 'movie', 'movies'],
  ['Interstellar', 2014, 'movie', 'movies'],
  ['Jaws', 1975, 'movie', 'movies'],
  ['Jurassic Park', 1993, 'movie', 'movies'],
  ['The Lord of the Rings: The Fellowship of the Ring', 2001, 'movie', 'movies'],
  ['The Lord of the Rings: The Return of the King', 2003, 'movie', 'movies'],
  ['Breaking Bad', 2008, 'show', 'tv'],
  ['Dark Matter', 2024, 'show', 'tv'],
  ['Game of Thrones', 2011, 'show', 'tv'],
  ['The Gentlemen', 2024, 'show', 'tv'],
  ['Lanterns', 2026, 'show', 'tv'],
  ['The Last of Us', 2023, 'show', 'tv'],
  ['Lioness', 2023, 'show', 'tv'],
  ['MobLand', 2025, 'show', 'tv'],
  ['Monster: The Lizzie Borden Story', 2026, 'show', 'tv'],
  ['Neagley', 2026, 'show', 'tv'],
  ['The Office', 2005, 'show', 'tv'],
  ['Outlander: Blood of My Blood', 2025, 'show', 'tv'],
  ['Reacher', 2022, 'show', 'tv'],
  ['Rick and Morty', 2013, 'show', 'tv'],
  ['Attack on Titan', 2013, 'show', 'anime'],
  ['Hunter x Hunter', 2011, 'show', 'anime'],
  ['Naruto Shippūden', 2007, 'show', 'anime'],
  ['One Piece', 1999, 'show', 'anime'],
];
const slug = (title: string) => title.toLowerCase().normalize('NFKD').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
/** One content entry: the drawn facts, and an artwork slot that is never the frame's picture. */
const entry = ([title, year, kind, libraryId]: Title) => {
  const id = slug(title);
  return Object.freeze({id, kind, libraryId, title, year, available: true, missing: false, watched: false,
    addedAt: '2026-10-17T03:10:02.549Z', posterUrl: `specimen-art://poster/${id}`,
    navigation: Object.freeze(kind === 'show' ? {view: 'show' as const, entityId: id} : {view: 'item' as const, entityId: id}),
    playback: Object.freeze({itemId: id, startSeconds: 0})});
};
const entries = Object.freeze(titles.map(value => entry(value))) as unknown as readonly FixtureEntry[];

/** The frame's reason line, in the server's own words and parameter. */
const why = Object.freeze({code: 'next.home.whyRecommendedFromActivity', params: Object.freeze({titles: 'Inside Out, The Last of Us and Rick and Morty'}),
  fallback: 'Because you’ve been watching Inside Out, The Last of Us and Rick and Morty. Titles you’ve finished are left out.'});
const row = Object.freeze({
  id: rowId, viewerFence, title: defaultI18n.t('home.row.recommended'),
  titleText: Object.freeze({code: 'home.row.recommended', fallback: 'Recommended for you'}),
  kind: 'recommendation', artworkShape: 'poster' as const, endpoint: 'home-rows',
  privacySensitivity: 'personal', policyState: 'current', priority: 90,
  required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
  entries, total: entries.length, start: 0, limit: 60, hasMore: false, nextCursor: '', revision, why,
  whyTitles: Object.freeze([{id: 'inside-out', name: 'Inside Out'}, {id: 'the-last-of-us', name: 'The Last of Us'}, {id: 'rick-and-morty', name: 'Rick and Morty'}]),
  cacheTtlMs: 300_000,
});

/* ---------- the across-libraries `titles` vocabulary the page is read through ---------- */
type FieldRow = readonly [id: string, labelKey: string, type: string, operators: readonly string[], hint: string,
  complexity: string, cost: string, extra: Readonly<{allowed?: readonly string[]; facet?: string; kinds?: readonly string[]}>];
const fieldRows: readonly FieldRow[] = [
  ['library', 'browse.field.library', 'enum', ['equals', 'not-equals', 'in', 'not-in'], 'facet-multi-select', 'quick', 'indexed', {facet: 'library'}],
  ['entityKind', 'browse.field.entityKind', 'enum', ['equals', 'not-equals', 'in', 'not-in'], 'select', 'quick', 'indexed', {allowed: allKinds}],
  ['search', 'browse.field.search', 'string', ['contains'], 'text', 'quick', 'indexed-join', {}],
  ['title', 'browse.field.title', 'string', ['equals', 'not-equals', 'contains', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'text', 'quick', 'indexed', {}],
  ['year', 'browse.field.year', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'quick', 'indexed', {facet: 'year', kinds: datedKinds}],
  ['decade', 'browse.field.decade', 'number', ['equals', 'not-equals', 'in', 'not-in', 'between'], 'facet-multi-select', 'quick', 'indexed', {facet: 'decade', kinds: datedKinds}],
  ['releaseDate', 'browse.field.releaseDate', 'date', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'is-present', 'is-missing'], 'date-range', 'standard', 'indexed-join', {kinds: releasedKinds}],
  ['dateAdded', 'browse.field.dateAdded', 'date', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'is-present', 'is-missing'], 'date-range', 'quick', 'indexed', {}],
  ['playState', 'browse.field.playState', 'enum', ['equals', 'not-equals', 'in', 'not-in'], 'select', 'quick', 'indexed-join', {allowed: ['unplayed', 'in-progress', 'played'], kinds: playedKinds}],
  ['favorite', 'browse.field.favorite', 'boolean', ['equals', 'not-equals'], 'toggle', 'quick', 'indexed-join', {kinds: playedKinds}],
  ['watchlisted', 'browse.field.watchlisted', 'boolean', ['equals', 'not-equals'], 'toggle', 'quick', 'indexed-join', {kinds: playedKinds}],
  ['personalRating', 'browse.field.personalRating', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'standard', 'indexed-join', {kinds: playedKinds}],
  ['lastPlayedAt', 'browse.field.lastPlayedAt', 'date', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'is-present', 'is-missing'], 'date-range', 'standard', 'indexed-join', {kinds: playedKinds}],
  ['genre', 'browse.field.genre', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'quick', 'indexed-join', {facet: 'genre'}],
  ['tag', 'browse.field.tag', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'tag'}],
  ['label', 'browse.field.label', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'label'}],
  ['contentRating', 'browse.field.contentRating', 'string', ['equals', 'not-equals', 'contains', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'quick', 'indexed-join', {facet: 'contentRating', kinds: videoArtKinds}],
  ['communityRating', 'browse.field.communityRating', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'standard', 'indexed-join', {}],
  ['criticRating', 'browse.field.criticRating', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'standard', 'indexed-join', {kinds: videoArtKinds}],
  ['availability', 'browse.field.availability', 'enum', ['equals', 'not-equals', 'in', 'not-in'], 'select', 'quick', 'indexed-join', {allowed: ['available', 'partial', 'unavailable'], kinds: playedKinds}],
  ['resolution', 'browse.field.resolution', 'enum', ['equals', 'not-equals', 'in', 'not-in'], 'facet-multi-select', 'standard', 'indexed-join', {allowed: ['sd', '720p', '1080p', '4k'], facet: 'resolution', kinds: videoArtKinds}],
  ['dynamicRange', 'browse.field.dynamicRange', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {kinds: videoArtKinds}],
  ['audioLanguage', 'browse.field.audioLanguage', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'audioLanguage'}],
  ['studio', 'browse.field.studio', 'string', ['equals', 'not-equals', 'contains', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'studio', kinds: videoArtKinds}],
  ['actor', 'browse.field.actor', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'text', 'advanced', 'indexed-join', {kinds: videoArtKinds}],
  ['director', 'browse.field.director', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'text', 'advanced', 'indexed-join', {kinds: videoArtKinds}],
  ['writer', 'browse.field.writer', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'text', 'advanced', 'indexed-join', {kinds: videoArtKinds}],
  ['durationMs', 'browse.field.durationSeconds', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'standard', 'indexed-join', {kinds: playedKinds}],
  ['subtitleLanguage', 'browse.field.subtitleLanguage', 'identity-set', ['contains', 'contains-any', 'contains-all', 'equals', 'not-equals', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {kinds: videoArtKinds}],
  ['versions', 'browse.field.versions', 'number', ['equals', 'not-equals', 'less-than', 'at-most', 'greater-than', 'at-least', 'between', 'in', 'not-in', 'is-present', 'is-missing'], 'number-range', 'standard', 'indexed-join', {kinds: playedKinds}],
  ['person', 'browse.field.person', 'identity-set', ['equals', 'in', 'not-in'], 'text', 'standard', 'indexed-join', {kinds: videoArtKinds}],
  ['cast', 'browse.field.cast', 'identity-set', ['equals', 'in', 'not-in'], 'text', 'standard', 'indexed-join', {kinds: videoArtKinds}],
  ['crew', 'browse.field.crew', 'identity-set', ['equals', 'in', 'not-in'], 'text', 'standard', 'indexed-join', {kinds: videoArtKinds}],
  ['savedCollection', 'browse.field.savedCollection', 'identity-set', ['equals', 'in', 'not-in'], 'text', 'standard', 'indexed-join', {kinds: playedKinds}],
  ['homeRow', 'browse.field.homeRow', 'string', ['equals'], 'text', 'standard', 'indexed-join', {}],
  ['list', 'browse.field.list', 'identity-set', ['equals', 'in', 'not-in'], 'text', 'standard', 'indexed-join', {kinds: playedKinds}],
  ['network', 'browse.field.network', 'string', ['equals', 'not-equals', 'contains', 'starts-with', 'in', 'not-in', 'is-present', 'is-missing'], 'facet-multi-select', 'standard', 'indexed-join', {facet: 'network', kinds: airedKinds}],
];
const fields = fieldRows.map(([id, labelKey, type, operators, controlHint, complexity, cost, extra]) => Object.freeze({id, labelKey, type, operators,
  controlHint, complexity, cost, applicableKinds: extra.kinds ?? allKinds,
  ...(extra.allowed ? {allowedValues: extra.allowed} : {}),
  ...(extra.facet ? {facetSource: {endpoint: '/v1/browse/facets', field: extra.facet}} : {})}));
const sortRows: readonly Readonly<{id: string; labelKey: string; defaultDirection: 'asc' | 'desc'; seeded?: boolean; requiresGroup?: boolean; kinds?: readonly string[]}>[] = [
  {id: 'title', labelKey: 'sort.title', defaultDirection: 'asc'},
  {id: 'added', labelKey: 'sort.added', defaultDirection: 'desc'},
  {id: 'year', labelKey: 'sort.year', defaultDirection: 'desc', kinds: datedKinds},
  {id: 'duration', labelKey: 'sort.duration', defaultDirection: 'desc'},
  {id: 'communityRating', labelKey: 'sort.communityRating', defaultDirection: 'desc'},
  {id: 'personalRating', labelKey: 'sort.personalRating', defaultDirection: 'desc'},
  {id: 'lastPlayed', labelKey: 'sort.lastPlayed', defaultDirection: 'desc'},
  {id: 'releaseDate', labelKey: 'sort.releaseDate', defaultDirection: 'desc', kinds: releasedKinds},
  {id: 'criticRating', labelKey: 'sort.criticRating', defaultDirection: 'desc', kinds: videoArtKinds},
  {id: 'fileSize', labelKey: 'sort.fileSize', defaultDirection: 'desc'},
  {id: 'resolution', labelKey: 'sort.resolution', defaultDirection: 'desc', kinds: videoArtKinds},
  {id: 'random', labelKey: 'sort.random', defaultDirection: 'asc', seeded: true},
  {id: 'groupOrder', labelKey: 'sort.groupOrder', defaultDirection: 'asc', requiresGroup: true},
  {id: 'playCount', labelKey: 'sort.playCount', defaultDirection: 'desc'},
  {id: 'latestAired', labelKey: 'sort.aired', defaultDirection: 'desc', kinds: ['show']},
  {id: 'episodeCount', labelKey: 'sort.episodeCount', defaultDirection: 'desc', kinds: airedKinds},
];
const sorts = sortRows.map(({id, labelKey, defaultDirection, seeded, requiresGroup, kinds}) => Object.freeze({id, labelKey, directions: ['asc', 'desc'],
  defaultDirection, expensive: false, seeded: seeded === true, requiresGroup: requiresGroup === true, applicableKinds: kinds ?? allKinds}));
const quickFilters = Object.freeze([
  {id: 'in-progress', labelKey: 'filter.inProgress', query: {variant: 'predicate', field: 'playState', operator: 'equals', value: 'in-progress'}},
  {id: 'unwatched', labelKey: 'filter.unwatched', query: {variant: 'predicate', field: 'playState', operator: 'equals', value: 'unplayed'}},
  {id: 'watched', labelKey: 'filter.watched', query: {variant: 'predicate', field: 'playState', operator: 'equals', value: 'played'}},
  {id: 'favorites', labelKey: 'filter.favorites', query: {variant: 'predicate', field: 'favorite', operator: 'equals', value: true}},
  {id: 'watchlist', labelKey: 'filter.watchlist', query: {variant: 'predicate', field: 'watchlisted', operator: 'equals', value: true}},
  {id: 'available', labelKey: 'filter.available', query: {variant: 'predicate', field: 'availability', operator: 'equals', value: 'available'}},
  {id: 'missing', labelKey: 'filter.missing', query: {variant: 'predicate', field: 'availability', operator: 'equals', value: 'unavailable'}},
].map(value => Object.freeze(value)));
const filterIds = fields.map(field => field.id);
const pivot = Object.freeze({id: 'titles', labelKey: 'library.titles', entityKinds: ['movie', 'show', 'album', 'book'],
  defaultSort: [{field: 'title', direction: 'asc' as const}], supportedViews: ['grid', 'list'], sorts: sortRows.map(sort => sort.id), filters: filterIds, paging: 'offset', browsable: true});
const capabilities = Object.freeze({
  surfaces: [{id: 'row-titles', endpoint: '/v1/browse', membershipField: 'homeRow', sorts: pivot.sorts, filters: filterIds, paging: 'offset'}],
  library: {id: '', name: '', kind: '', defaultView: '', pinned: false},
  pivots: [pivot], resolvedPivot: pivot, fields, sorts, quickFilters,
  queryLimits: {maximumDepth: 5, maximumClauses: 40, maximumBytes: 65_536, maximumSorts: 1, defaultLimit: 40, maximumLimit: 100, cursorTtlMs: 1_800_000},
});

/* ---------- the page: the fixture's own semantics over its facts ---------- */
/** A drawn entry plus the fields a filter or sort may read. */
type FixtureEntry = Readonly<Record<string, unknown> & {id: string; kind: Kind; libraryId: LibraryId; title: string; year: number}>;
/** Every drawn entry is available and unwatched, so the facts the fixture carries decide a filter. */
function matches(candidate: FixtureEntry, node: BrowseNode | null | undefined): boolean {
  if (!node) return true;
  if ('all' in node) return node.all.every(child => matches(candidate, child));
  if ('any' in node) return node.any.some(child => matches(candidate, child));
  if ('not' in node) return !matches(candidate, node.not);
  const {field, operator} = node, value = node.value;
  const one = (expected: unknown) => {
    switch (field) {
      case 'homeRow': return true;
      case 'library': return expected === candidate.libraryId;
      case 'entityKind': return expected === candidate.kind;
      case 'title': case 'search': return typeof expected === 'string' && candidate.title.toLowerCase().includes(expected.toLowerCase());
      case 'playState': return expected === 'unplayed';
      case 'favorite': case 'watchlisted': return expected === false;
      case 'availability': return expected === 'available';
      case 'year': return Number(expected) === candidate.year;
      case 'decade': return Math.floor(candidate.year / 10) * 10 === Number(expected);
      default: return false;
    }
  };
  const expected = Array.isArray(value) ? value : [value];
  switch (operator) {
    case 'equals': case 'in': case 'contains': case 'contains-any': return expected.some(one);
    case 'not-equals': case 'not-in': return !expected.some(one);
    case 'between': return expected.length === 2 && Number(candidate[field]) >= Number(expected[0]) && Number(candidate[field]) <= Number(expected[1]);
    case 'is-present': return candidate[field] !== undefined;
    case 'is-missing': return candidate[field] === undefined;
    default: return false;
  }
}
function sortBy(selection: BrowseSortSelection | undefined) {
  const key = selection?.field;
  // The row's own order, a "for you" ranking and a seeded shuffle are the server's; the fixture keeps its drawn order.
  if (!key || key === 'groupOrder' || key === 'forYou' || key === 'random') return () => 0;
  const sign = selection?.direction === 'desc' ? -1 : 1;
  return (a: FixtureEntry, b: FixtureEntry) => {
    const left = a[key], right = b[key];
    return (typeof left === 'number' && typeof right === 'number' ? left - right : String(left).localeCompare(String(right))) * sign;
  };
}
export type BrowseRequest = Readonly<{query?: BrowseNode | null; sort?: readonly BrowseSortSelection[]; limit?: number; range?: Readonly<{start: number}>}>;

export function createSeeAllSpecimen() {
  const records: SeeAllRecord[] = [];
  const record = (kind: string, value?: unknown): void => { records.push(Object.freeze({kind, at: Date.now(), ...(value === undefined ? {} : {value})})); };
  const page = (body: BrowseRequest) => {
    const start = body.range?.start ?? 0, limit = body.limit ?? 60;
    const kept = entries.filter(candidate => matches(candidate, body.query ?? null)).sort(sortBy(body.sort?.[0]));
    const slice = kept.slice(start, start + limit);
    record('browse', {start, limit, total: kept.length, sort: body.sort?.[0]?.field ?? 'groupOrder'});
    return {pivot: 'titles', applied: {query: body.query ?? null, sort: body.sort ?? [], seek: null}, entries: slice,
      pageInfo: {start, total: kept.length, totalIsLowerBound: false, revision: revisionToken, hasMore: start + slice.length < kept.length, nextCursor: ''}, positionIndex: []};
  };
  const counts = (body: Readonly<{queries: readonly Readonly<{id: string; query: BrowseNode | null}>[]}>) => ({
    counts: body.queries.map(group => ({id: group.id, total: entries.filter(candidate => matches(candidate, group.query)).length,
      totalIsLowerBound: false, revision: revisionToken})),
  });
  const facets = (url: URL) => {
    const field = url.searchParams.get('field') ?? '', values: {value: string; label: string; count: number}[] = [];
    const tally = (of: (item: Record<string, unknown>) => string | number | undefined) => entries.forEach(candidate => {
      const key = of(candidate);
      if (key === undefined) return;
      const found = values.find(value => value.value === String(key));
      if (found) found.count += 1; else values.push({value: String(key), label: String(key), count: 1});
    });
    if (field === 'library') entries.forEach(candidate => {
      const id = String(candidate.libraryId), found = values.find(value => value.value === id);
      if (found) found.count += 1; else values.push({value: id, label: id, count: 1});
    });
    else if (field === 'year') tally(candidate => candidate.year as number | undefined);
    else if (field === 'decade') tally(candidate => Math.floor(Number(candidate.year) / 10) * 10);
    record('facets', {field});
    return {field, total: values.reduce((sum, value) => sum + value.count, 0), values};
  };
  const request = async <T,>(path: string, method = 'GET', body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (signal?.aborted) throw Object.assign(new Error('aborted'), {name: 'AbortError'});
    const route = path.split('?')[0]!, url = new URL(path, 'http://specimen.invalid');
    record('request', {path: route, method});
    if (method === 'GET' && route === `/v1/home/rows/${rowId}`) return row as T;
    if (method === 'GET' && route === '/v1/browse-capabilities') return capabilities as T;
    if (method === 'POST' && route === '/v1/browse') return page(body as BrowseRequest) as T;
    if (method === 'POST' && route === '/v1/browse/counts') return counts(body as Parameters<typeof counts>[0]) as T;
    if (method === 'GET' && route === '/v1/browse/facets') return facets(url) as T;
    record('request-unanswered', {path: route, method});
    throw Object.assign(new Error(`the see-all specimen has no answer for ${method} ${route}`), {code: 'not_found', status: 404});
  };
  return Object.freeze({serverId, rowId, viewerFence, records, record, request, row, capabilities, page, counts,
    pageFor: (body: BrowseRequest) => page(body), entries});
}


/** A specimen records the failure's stable code for its own diagnostics; the
 * server's error text is never rendered or kept as specimen output. */
const failureCode = (error: unknown): string => {
  const code = error && typeof error === 'object' && 'code' in error ? (error as {code?: unknown}).code : undefined;
  return typeof code === 'string' && /^[a-z0-9_]{1,64}$/.test(code) ? code : 'error';
};

/* ---------- the entry: production router, shell and route ---------- */
const root = typeof document === 'undefined' ? null : document.getElementById('root');
if (root) void mount(root);

async function mount(element: HTMLElement) {
  const [{createRoot}, {RouterProvider, createMemoryHistory}, {defaultI18n}, {router}, {WebScreenProvider}, {ShellBindingsProvider},
    {createOwnerActivity}, {createArtworkRuntime}, {IconSprite}] = await Promise.all([
    import('react-dom/client'), import('@tanstack/react-router'), import('@i18n'), import('../../router'),
    import('../../route-registry-view'), import('../../shell/bindings'), import('../../shell/Activity'),
    import('../../kit/artwork/source'), import('../../kit/Icon')]);
  await Promise.all([import('../../kit/tokens.css'), import('../../kit/fonts.css'), import('../../bootstrap.css')]);
  const specimen = createSeeAllSpecimen();
  const record = specimen.record;
  const time = {t: defaultI18n.t, locale: 'en-US', timeZone: 'UTC'};
  const viewer: Viewer = {serverId: specimen.serverId, authority: 'local', accountId: 'specimen-account', profileId: 'demo', role: 'owner'};
  const session: LocalSession = {
    accessToken: 'specimen-public', refreshToken: '', sessionFamilyId: 'specimen-family', tokenGeneration: '1',
    installationId: 'i'.repeat(43), expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-02-01T00:00:00Z', viewer,
  };
  const libraries: readonly Library[] = [
    {id: 'movies', name: 'Movies', kind: 'movie'}, {id: 'tv', name: 'TV Shows', kind: 'tv'}, {id: 'anime', name: 'Anime', kind: 'anime'},
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
    scopeKey: 'see-all-specimen', classify: () => ({retryable: false}),
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
    deviceClass: 'web', registry: {fields: [], revision: 'see-all-specimen'}, documents: [], clampedFields: [], registryRevision: 'see-all-specimen',
    effectiveSource: {}, subscribe: () => () => {},
    effective: {'appearance.showBackdrops': true, 'appearance.cardSizePercent': 100, 'appearance.libraryView': 'grid',
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
  const activity = createOwnerActivity(contextBase as never, time, () => record('open-dashboard'), () => record('open-update'), destination => record('open-issue', destination));
  const playback = {artwork, environment: time, skips: {back: 10, forward: 30}, reportError: (error: unknown) => record('report-error', failureCode(error)),
    runtime: {playFromQueue: async (source: unknown, _context: unknown, options: {shuffle?: boolean}) => { record('play-from-queue', {source, shuffle: options.shuffle === true}); }}};
  const home = {
    service: {getSnapshot: () => ({phase: 'idle'}), subscribe: () => () => {}}, artwork, time, serverName: identity.serverName, reducedMotion: true,
    selection: {toolbar: null, card: () => undefined, channel: () => undefined, collection: () => undefined, registerWindow: () => undefined,
      registerSpotlight: () => undefined, moreAvailable: () => false, moreExpanded: () => false},
    effects: {play: async () => record('play'), playChannel: async () => {}, openEntry: () => record('open-entry'), openSeeAll: () => record('open-see-all'),
      openMore: () => record('open-more'), setMyList: async () => {}, reportError: (error: unknown) => record('report-error', failureCode(error))},
  };
  const binding = {api, events, scopeKey: 'see-all-specimen', playback,
    shell: {identity, OwnerActivity: activity.OwnerActivity, dockVisible: false, onSignOut: () => record('sign-out')}, home};
  router.update({history: createMemoryHistory({initialEntries: [`/home/rows/${specimen.rowId}`]})});
  createRoot(element).render(<WebScreenProvider value={{binding, context: contextBase} as never}>
    <IconSprite />
    <ShellBindingsProvider value={binding as never}>
      <RouterProvider router={router} />
    </ShellBindingsProvider>
  </WebScreenProvider>);
  // The fixture and the commands the screen issued are inspectable from the page; no credentials.
  (window as Window & {__porticoSeeAllSpecimen?: unknown}).__porticoSeeAllSpecimen = {records: specimen.records, row: specimen.row, capabilities: specimen.capabilities};
}
