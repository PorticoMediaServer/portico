/**
 * See all: Recommended for you — the public DEV capture of the registered Home row route
 * (`/home/rows/$rowId`) at `design/ui-v1/desktop-browse.html#seeall`, 1440 wide.
 *
 * The screen is the production router, shell and HomeRow route; this module only supplies the
 * Home row answer that the screen reads for its preview and later pages. The titles and reason
 * come from the frame. Artwork slots are one neutral inline image.
 *
 * The entry fails closed outside a development build and only mounts over a root element, so a
 * node test can read the same fixture documents without a DOM.
 */
import {FreshnessProvider, freshnessFor} from '../../platform/freshness';
import {viewerQueryScope} from '@core/query/viewer-queries.ts';
import {defaultI18n} from '@i18n';
import {serverPath} from '@core/query/server-api.ts';
import type {HttpLocalApi, Library, LocalSession, Viewer} from '@core/index.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';

if (import.meta.env && !import.meta.env.DEV) throw new Error('See all specimens require the development server.');

export type SeeAllRecord = Readonly<{kind: string; at: number; value?: unknown}>;

const serverId = 'specimen-server';
export const rowId = 'recommended';
const viewerFence = 'specimen-viewer-fence';
const revision = {catalog: 1, viewer: 1};

/* ---------- the drawn page: 49 films, 16 shows and 7 anime, in the frame's order ---------- */
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
const entries = Object.freeze(titles.map(entry));

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

export function createSeeAllSpecimen() {
  const records: SeeAllRecord[] = [];
  const record = (kind: string, value?: unknown): void => { records.push(Object.freeze({kind, at: Date.now(), ...(value === undefined ? {} : {value})})); };
  const rowPath = serverPath('GET /v1/home/rows/{id}', {id: rowId});
  const request = async <T,>(path: string, method = 'GET', _body?: unknown, signal?: AbortSignal): Promise<T> => {
    if (signal?.aborted) throw Object.assign(new Error('aborted'), {name: 'AbortError'});
    const url = new URL(path, 'http://specimen.invalid');
    record('request', {path: url.pathname, method});
    if (method === 'GET' && url.pathname === rowPath) {
      const start = Number(url.searchParams.get('cursor') ?? url.searchParams.get('at') ?? 0);
      const limit = Number(url.searchParams.get('limit') ?? 60), end = Math.min(entries.length, start + limit);
      const hasMore = end < entries.length;
      return {...row, entries: entries.slice(start, end), start, limit, hasMore, nextCursor: hasMore ? String(end) : ''} as T;
    }
    record('request-unanswered', {path: url.pathname, method});
    throw Object.assign(new Error(`the see-all specimen has no answer for ${method} ${url.pathname}`), {code: 'not_found', status: 404});
  };
  return Object.freeze({serverId, rowId, viewerFence, records, record, request, row});
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
    // The app's own freshness runtime over the specimen's answers, as the shell's bootstrap makes it.
    freshness: freshnessFor(api, viewerQueryScope(session.viewer)),
    api, session: () => session, events, artwork, facts: {identity, preferences, serverName: identity.serverName},
    preferences: {getSnapshot: () => preferences, subscribe: () => () => {}},
    epoch: 1, isCurrent: () => true, signOut: () => record('sign-out'),
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
      <FreshnessProvider api={api} viewerId={viewerQueryScope(session.viewer).viewerId} serverId={session.viewer.serverId} events={null}><RouterProvider router={router} /></FreshnessProvider>
    </ShellBindingsProvider>
  </WebScreenProvider>);
  // The fixture and the commands the screen issued are inspectable from the page; no credentials.
  (window as Window & {__porticoSeeAllSpecimen?: unknown}).__porticoSeeAllSpecimen = {records: specimen.records, row: specimen.row};
}
