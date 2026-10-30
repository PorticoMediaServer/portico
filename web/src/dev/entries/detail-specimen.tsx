import {detailFixtureText as fixtureText, type DetailFixtureId} from '../../../../packages/i18n/src/catalog/en-US/next/dev/detail-fixtures.ts';
import type {HttpLocalApi} from '@core/index.ts';
import type {EventsClient} from '@core/playback-v1/events.ts';
import type {PreferenceSnapshot} from '@core/preferences.ts';
import type {ShellArtwork} from '../../shell/artwork';
import type {AmbientArtSource, ArtworkSource} from '../../kit/artwork/types';

/**
 * The title page's public DEV facts. The frame's own artwork bytes never enter source: every
 * image slot is fed by one neutral inline image, and the measured comparator excludes artwork
 * contents. The screen itself is the registered production Item route and its real bindings; this
 * module only supplies the server answers a public fixture would send and records the real
 * commands the screen issues. It is imported by the DEV entry alone, which fails closed outside
 * the development server.
 *
 * Facts and copy come from `design/ui-v1/desktop-detail.html` and
 * `design/ui-v1/notes/detail-desktop.md`: frame 1 (Dune: Part Two, not started, More like this),
 * frame 2 (Inside Out, nine per cent in, Resume and Start over) and frame 3 (Dune: Part Two,
 * Details, cast and crew, its collection banner).
 */
export type SpecimenEvent = Readonly<{kind: string; at: number; value?: unknown}>;
export type SpecimenFrame = '1' | '2' | '3';

const serverId = 'specimen-server';
const libraryId = 'movies';
const viewerFence = 'specimen-viewer-fence';
const personalFence = JSON.stringify(['local', 'specimen-account', 'demo']);
const image = 'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl+0AAAAASUVORK5CYII=';
const artPath = (kind: string, id: string) => `specimen-art://${kind}/${encodeURIComponent(id)}`;

const posterRow = (items: readonly (readonly [string, number])[]) => items.map(([title, year]) => {
  const id = title.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');
  return {
    id, kind: 'movie', libraryId, title, year, available: true, posterUrl: artPath('poster', id),
    navigation: {view: 'item', entityId: id}, playback: {itemId: id},
  } as const;
});

const moreLikeDune = posterRow([
  [fixtureText('next.detail.fixture.dune'), 2021], [fixtureText('next.detail.fixture.bladeRunner'), 1982], [fixtureText('next.detail.fixture.interstellar'), 2014], [fixtureText('next.detail.fixture.starWars'), 1977],
  [fixtureText('next.detail.fixture.avatar'), 2009], [fixtureText('next.detail.fixture.empire'), 1980], [fixtureText('next.detail.fixture.matrix'), 1999], [fixtureText('next.detail.fixture.gladiator'), 2000],
]);
const viewersAlso = posterRow([
  [fixtureText('next.detail.fixture.oppenheimer'), 2023], [fixtureText('next.detail.fixture.inception'), 2010], [fixtureText('next.detail.fixture.darkKnight'), 2008], [fixtureText('next.detail.fixture.barbie'), 2023],
  [fixtureText('next.detail.fixture.spiderVerse'), 2023], [fixtureText('next.detail.fixture.fightClub'), 1999], [fixtureText('next.detail.fixture.alien'), 1979], [fixtureText('next.detail.fixture.hailMary'), 2026],
]);
const moreLikeInsideOut = posterRow([
  [fixtureText('next.detail.fixture.toyStory'), 1995], [fixtureText('next.detail.fixture.monsters'), 2001], [fixtureText('next.detail.fixture.wallE'), 2008], [fixtureText('next.detail.fixture.incredibles'), 2004],
  [fixtureText('next.detail.fixture.spiritedAway'), 2001], [fixtureText('next.detail.fixture.yourName'), 2016], [fixtureText('next.detail.fixture.dragon'), 2010], [fixtureText('next.detail.fixture.toyStory3'), 2010],
]);

type FixtureCredit = Readonly<{id: string; name: string; role: string; department: string; provider: string}>;
const credit = (id: string, name: DetailFixtureId, role: DetailFixtureId, department: DetailFixtureId): FixtureCredit => ({id, name: fixtureText(name), role: fixtureText(role), department: fixtureText(department), provider: 'specimen'});

const directing: DetailFixtureId = 'next.detail.fixture.directingDepartment';
const acting: DetailFixtureId = 'next.detail.fixture.actingDepartment';
const writing: DetailFixtureId = 'next.detail.fixture.writingDepartment';

const duneCredits: readonly FixtureCredit[] = [
  credit('denis-villeneuve', 'next.detail.fixture.director', 'next.detail.fixture.directorRole', directing),
  credit('timothee-chalamet', 'next.detail.fixture.timothee', 'next.detail.fixture.paulRole', acting),
  credit('zendaya', 'next.detail.fixture.zendaya', 'next.detail.fixture.chaniRole', acting),
  credit('rebecca-ferguson', 'next.detail.fixture.rebecca', 'next.detail.fixture.jessicaRole', acting),
  credit('javier-bardem', 'next.detail.fixture.javier', 'next.detail.fixture.stilgarRole', acting),
  credit('josh-brolin', 'next.detail.fixture.brolin', 'next.detail.fixture.gurneyRole', acting),
  credit('austin-butler', 'next.detail.fixture.butler', 'next.detail.fixture.feydRole', acting),
  credit('jon-spaihts', 'next.detail.fixture.jon', 'next.detail.fixture.screenplayRole', writing),
  credit('frank-herbert', 'next.detail.fixture.herbert', 'next.detail.fixture.novelRole', writing),
  credit('hans-zimmer', 'next.detail.fixture.zimmer', 'next.detail.fixture.musicRole', 'next.detail.fixture.musicRole'),
  credit('greig-fraser', 'next.detail.fixture.fraser', 'next.detail.fixture.cinematographyRole', 'next.detail.fixture.cinematographyRole'),
];
const insideOutCredits: readonly FixtureCredit[] = [
  credit('pete-docter', 'next.detail.fixture.docter', 'next.detail.fixture.directorRole', directing),
  credit('amy-poehler', 'next.detail.fixture.poehler', 'next.detail.fixture.joyRole', acting),
  credit('phyllis-smith', 'next.detail.fixture.phyllis', 'next.detail.fixture.sadnessRole', acting),
  credit('richard-kind', 'next.detail.fixture.richard', 'next.detail.fixture.bingRole', acting),
  credit('bill-hader', 'next.detail.fixture.hader', 'next.detail.fixture.fearRole', acting),
  credit('meg-lefauve', 'next.detail.fixture.leFauve', 'next.detail.fixture.screenplayRole', writing),
  credit('josh-cooley', 'next.detail.fixture.cooley', 'next.detail.fixture.screenplayRole', writing),
];

function basePersonal() {
  return {watchlisted: false, favorite: false, rating: null as number | null, revision: 1, watched: false, progressMs: 0, lastPlayedAt: '', status: 'current', conflicts: [] as unknown[]};
}

const metric = {catalog: 1, viewer: 1};

function fixtures(frame: SpecimenFrame) {
  if (frame === '2') {
    const itemId = 'inside-out';
    return {
      itemId, title: fixtureText('next.detail.fixture.insideOut'), year: 2015, durationMs: 5_700_000, progressMs: 513_000,
      overview: fixtureText('next.detail.fixture.insideOutOverview'), rating: fixtureText('next.detail.fixture.rating'), tagline: fixtureText('next.detail.fixture.insideOutTagline'),
      releaseDate: '2015-06-19', studio: fixtureText('next.detail.fixture.pixar'), country: fixtureText('next.detail.fixture.country'),
      genres: [['animation', 'next.detail.fixture.animation'], ['family', 'next.detail.fixture.family']] as const,
      credits: insideOutCredits, creditTotals: {cast: 4, crew: 24}, audio: ['en'], subtitles: ['en'],
      rows: [{id: 'more-like', title: fixtureText('next.detail.fixture.moreLikeInsideOut'), entries: moreLikeInsideOut}],
    } as const;
  }
  const itemId = 'dune-part-two';
  return {
    itemId, title: fixtureText('next.detail.fixture.title'), year: 2024, durationMs: 9_960_000, progressMs: 0,
    overview: fixtureText('next.detail.fixture.overview'), rating: fixtureText('next.detail.fixture.rating'), tagline: fixtureText('next.detail.fixture.tagline'),
    releaseDate: '2024-03-01', studio: fixtureText('next.detail.fixture.studio'),
    genres: [['science-fiction', 'next.detail.fixture.scienceFiction'], ['adventure', 'next.detail.fixture.adventure']] as const,
    credits: duneCredits, creditTotals: frame === '3' ? {cast: 40, crew: 24} : {cast: 4, crew: 2},
    audio: frame === '3' ? ['en', 'fr', 'es'] : ['en'], subtitles: frame === '3' ? ['en', 'fr', 'es'] : ['en'],
    rows: [{id: 'more-like', title: fixtureText('next.detail.fixture.moreLike'), entries: moreLikeDune},
      {id: 'viewers-also-watched', title: fixtureText('next.detail.fixture.viewersAlso'), entries: viewersAlso, note: {code: 'specimen.viewersAlsoWatchedNote', fallback: fixtureText('next.detail.fixture.viewersNote')}}],
  } as const;
}

export function createDetailSpecimen(frame: SpecimenFrame = '1') {
  const records: SpecimenEvent[] = [];
  const record = (kind: string, value?: unknown): void => {
    records.push(Object.freeze({kind, at: Date.now(), ...(value === undefined ? {} : {value})}));
  };
  const facts = fixtures(frame), {itemId} = facts;
  const progressMs = frame === '2' ? facts.progressMs : 0;
  let personal = {...basePersonal(), progressMs};
  const detail = () => detailDocument(facts, personal);
  const item = () => ({id: itemId, libraryId, kind: 'movie', title: facts.title, available: true, durationMs: facts.durationMs, progressMs: frame === '2' ? facts.progressMs : 0});
  const recommendations = () => ({itemId, revision: {...metric}, rows: facts.rows.map((row, index) => recommendationRow(index, row))});

  const request = async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    const route = path.split('?')[0];
    record('request', {path: route, method});
    if (method === 'GET' && route === `/v1/items/${itemId}`) return item() as T;
    if (method === 'GET' && route === `/v1/items/${itemId}/detail`) return detail() as T;
    if (method === 'GET' && route === `/v1/items/${itemId}/recommendations`) return recommendations() as T;
    if (method === 'PUT' && route === `/v1/items/${itemId}/personal-state`) {
      const intent = (body ?? {}) as {operationId?: unknown; watchlisted?: unknown; favorite?: unknown; watched?: unknown; rating?: unknown};
      personal = {
        ...personal,
        ...(typeof intent.watchlisted === 'boolean' ? {watchlisted: intent.watchlisted} : {}),
        ...(typeof intent.favorite === 'boolean' ? {favorite: intent.favorite} : {}),
        ...(typeof intent.watched === 'boolean' ? {watched: intent.watched} : {}),
        ...(typeof intent.rating === 'number' || intent.rating === null ? {rating: intent.rating as number | null} : {}),
        revision: personal.revision + 1,
      };
      record('personal-state', {watchlisted: personal.watchlisted, favorite: personal.favorite, watched: personal.watched, rating: personal.rating});
      return {operationId: intent.operationId, serverId, libraryId, itemId, viewerFence, current: {...personal}} as T;
    }
    throw Object.assign(new Error(`The detail specimen has no answer for ${method} ${route}.`), {code: 'not_found', status: 404});
  };

  const requestRaw = async (path: string) => {
    if (path.split('?')[0] === '/v1/admin/activity') return {status: 200, headers: {}, body: activityDocument(new Date().toISOString())};
    return {status: 404, headers: {}, body: {error: {code: 'not_found'}}};
  };

  // A ready public image slot: the production Artwork/AmbientArt components still own
  // markup, fit, masking and failure states; only the bytes and their arrival are fixture data.
  const readyImage = {src: image, bytes: 68, width: 1, height: 1, retain: () => () => {}};
  const readyScheduler = {peek: () => readyImage, load: async () => readyImage, subscribe: () => () => {}};
  const source = (key: string) => ({scheduler: readyScheduler, key, retain: () => () => {}}) as unknown as ArtworkSource;
  const artwork = {
    foreground: (url: string | undefined) => url ? source(`foreground:${url}`) : undefined,
    ambient: (url: string | undefined) => url ? ({...source(`ambient:${url}`), variant: 'ambient-v1', maxEdge: 64, version: 'a'.repeat(64)} as AmbientArtSource) : undefined,
    dispose: () => {},
  } as unknown as ShellArtwork;

  const events = {
    on: () => () => {}, onResync: () => () => {}, onAny: () => () => {},
    getStatus: () => ({connected: true}), subscribeStatus: () => () => {},
  } as unknown as EventsClient;

  const preferences = {
    deviceClass: 'web', registry: {fields: [], revision: 'detail-specimen'},
    documents: [], effective: {'appearance.showBackdrops': true}, effectiveSource: {},
    clampedFields: [], registryRevision: 'detail-specimen',
  } as unknown as PreferenceSnapshot;
  const subscribePreferences = () => () => {};

  const api = {
    request, requestRaw, getRouteSnapshot: () => ({phase: 'ready'}), subscribeRoute: () => () => {},
    reading: () => {
      const life = new AbortController();
      return {signal: life.signal, versions: async () => ({versions: []}), dispose: () => life.abort()};
    },
    dispose: () => {},
  } as unknown as HttpLocalApi;

  return Object.freeze({
    serverId, libraryId, itemId, viewerFence, personalFence, records, record,
    api, events, artwork, preferences, subscribePreferences,
    identity: {serverId, viewerId: personalFence}, detail, recommendations,
  });
}

type DetailsFixture = ReturnType<typeof fixtures>;
function detailDocument(facts: DetailsFixture, personal: ReturnType<typeof basePersonal>) {
  const {itemId} = facts;
  return {
    scope: {serverId, libraryId, itemId, viewerFence},
    revision: {...metric},
    item: {
      id: itemId, libraryId, kind: 'movie', title: facts.title, year: facts.year, durationMs: facts.durationMs,
      progressMs: facts.progressMs, available: true, addedAt: '2026-10-17T00:00:00Z', overview: facts.overview,
      posterUrl: artPath('poster', itemId), backdropUrl: artPath('backdrop', itemId),
    },
    personal: {...personal},
    actions: [
      {id: 'play', labelKey: 'entry.play', enabled: true, playback: {itemId, startMs: facts.progressMs}},
      ...(facts.progressMs > 0 ? [{id: 'start_over', labelKey: 'entry.startOver', enabled: true, playback: {itemId, startMs: 0}}] : []),
      {id: 'watchlist', labelKey: 'entry.addToWatchlist', enabled: true},
      {id: 'watched', labelKey: 'entry.markWatched', enabled: true},
      {id: 'favorite', labelKey: 'entry.addToFavorites', enabled: true},
      {id: 'rating', labelKey: 'entry.rate', enabled: true},
    ],
    facts: {
      contentRating: facts.rating, tagline: facts.tagline, releaseDate: facts.releaseDate,
      studio: facts.studio, ...('country' in facts ? {country: facts.country} : {}), originalLanguage: 'en',
    },
    metadata: {
      status: 'available', ratings: facts.itemId === 'inside-out' ? [{provider: 'TMDB', value: 8.1, max: 10, votes: 24810, sourceUrl: 'https://www.themoviedb.org/movie/150540', observedAt: '2026-10-17T00:00:00Z'}] : [], attributions: [], // lint-strings-allow specimen catalogue data, not interface copy
      genres: facts.genres.map(([id, name]) => ({id, name: fixtureText(name), provider: 'specimen'})),
      credits: [...facts.credits], creditTotals: facts.creditTotals,
    },
    languages: {audio: [...facts.audio], subtitles: [...facts.subtitles]},
    ...(facts.itemId === 'dune-part-two' ? {collections: [{id: 'dune-collection', name: fixtureText('next.detail.fixture.collection'), memberCount: 2, position: 2, yearFrom: 2021, yearTo: 2024, posterUrls: ['/specimen/poster/dune', '/specimen/poster/dune-part-two']}]} : {}),
    markers: [],
  };
}

function recommendationRow(index: number, row: DetailsFixture['rows'][number]) {
  return {
    id: row.id, viewerFence, title: row.title, kind: 'recommendations', artworkShape: 'poster', endpoint: 'item-recommendations',
    privacySensitivity: 'public', policyState: 'current', priority: index + 1, required: false,
    hideable: true, reorderable: false, critical: false, cursorCapable: false, titleText: {code: `specimen.${row.id}`, fallback: row.title},
    ...('note' in row && row.note ? {subtitleText: row.note} : {}), entries: row.entries, total: row.entries.length, start: 0, limit: row.entries.length,
    hasMore: false, nextCursor: '', revision: {...metric},
  };
}

function activityDocument(now: string) {
  const session = (id: string) => ({
    id, user: {id: 'specimen-account', username: 'demo'}, profile: {id: 'demo', name: fixtureText('next.detail.fixture.profile')},
    device: {id: 'specimen-device', name: fixtureText('next.detail.fixture.device')}, kind: 'vod', state: 'playing', startedAt: now,
    problem: false, converting: false, positionMs: 60_000, decision: {},
  });
  return {
    scope: {serverId, viewerFence: 'a'.repeat(64)}, observedAt: now, serverName: fixtureText('next.detail.fixture.server'),
    streams: {count: 2, convertingCount: 0, overflow: {count: 0, convertingCount: 0}, items: [session('stream-1'), session('stream-2')]},
    scans: [], tasks: {count: 0, items: []}, update: {available: false, state: 'unconfigured', current: ''},
    health: {degraded: false}, majorIssues: [],
  };
}

export type DetailSpecimen = ReturnType<typeof createDetailSpecimen>;
