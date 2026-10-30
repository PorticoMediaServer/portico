import {useEffect, useMemo} from 'react';
import {defaultI18n} from '@i18n';
import {createRoot} from 'react-dom/client';
import type {ContentEntryKind} from '@core/library-content.ts';
import type {HomeActions, HomeChannel, HomeDocument, HomeEntry, HomeRow, HomeSpotlight} from '@core/home.ts';
import type {GuideChannel, GuideProgramme, GuideSource} from '@core/channel-guide.ts';
import {IconSprite} from '../../kit/Icon';
import {createArtworkRuntime} from '../../kit/artwork';
import type {ShellArtwork} from '../../shell/artwork';
import type {HomeEffects, HomeSelection} from './Home';
import {HomeHeroArtwork, HomeView} from './Home';
import {homeSpotlights} from './model';
import {HeroFacts} from '../../kit/layout';
import extras from './ds-web-home-r46.extras.module.css';
import '../../kit/tokens.css';
import '../../kit/fonts.css';
import '../../bootstrap.css';

/**
 * DEV-only Home specimen for a same-size/same-state capture pair.
 * Drawn Home content lives here only; the genuine HomeView and kit render it.
 * Production cannot import this entry and no server read or session is involved.
 */
if (!import.meta.env.DEV) throw new Error('Home specimens require development.');

const minute = 60_000, nowMs = Date.now(), now = new Date(Math.floor(nowMs / minute) * minute).toISOString().replace(/\.\d{3}Z$/, 'Z');
const instant = (value: number) => new Date(value).toISOString().replace(/\.\d{3}Z$/, 'Z');
const photo = (name: string) => `assets/${name}`;

const actions = (state: Partial<HomeActions['state']> = {}): HomeActions => ({
  play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, played: true, rating: true,
  editMetadata: false, refreshMetadata: false, delete: false, download: true, details: true,
  state: {watchlisted: false, favorite: false, watched: false, rating: null, personalRevision: 1, containerRevision: 1, ...state},
  selection: {play: true, queue: true, playlist: true, collection: true, myList: true, favorite: true, watched: true, download: true, edit: false, refresh: false, delete: false},
});
const entry = (id: string, kind: ContentEntryKind, title: string, extra: Partial<HomeEntry> = {}): HomeEntry =>
  ({id, kind, title, playback: {itemId: `play-${id}`}, homeActions: actions(), ...extra});
const movie = (id: string, title: string, year: number, poster: string, extra: Partial<HomeEntry> = {}) =>
  entry(id, 'movie', title, {year, posterUrl: photo(poster), backdropUrl: photo(`pool/movies/${poster.slice(2)}`), ...extra});
const show = (id: string, title: string, seasonCount: number, poster: string, extra: Partial<HomeEntry> = {}) =>
  entry(id, 'show', title, {seasonCount, posterUrl: photo(poster), ...extra});
const episode = (id: string, title: string, showTitle: string, showId: string, seasonNumber: number, episodeNumber: number, duration: number, progressSeconds: number, backdrop: string) =>
  entry(id, 'episode', title, {seasonNumber, episodeNumber, duration, progressSeconds, backdropUrl: photo(backdrop),
    show: {id: showId, name: showTitle}, homeTitleContext: {kind: 'show', year: 2022, genres: ['Drama'], seasonCount: 1}});
const book = (id: string, title: string, author: string, poster: string, extra: Partial<HomeEntry> = {}) =>
  entry(id, 'audiobook_file', title, {author, posterUrl: photo(poster), ...extra});
const album = (id: string, title: string, artist: string, poster: string) =>
  entry(id, 'album', title, {artist: {id: `artist-${id}`, name: artist}, posterUrl: photo(poster)});

const row = (id: string, code: string, title: string, artworkShape: HomeRow['artworkShape'], entries: readonly HomeEntry[], extra: Partial<HomeRow> = {}): HomeRow => ({
  id, viewerFence: 'ds-web-home-r46', title, titleText: {code, fallback: title}, kind: id, artworkShape,
  endpoint: `/v1/home/rows/${id}`, privacySensitivity: 'personal', policyState: 'available', priority: 10,
  required: false, hideable: true, reorderable: true, critical: false, cursorCapable: true,
  entries, total: entries.length, start: 0, limit: 20, hasMore: false, nextCursor: '', revision: {catalog: 1, viewer: 1}, ...extra,
});

const lastOfUs = episode('ep-last-of-us', "When You're Lost in the Darkness", 'The Last of Us', 'show-last-of-us', 1, 1, 4920, 1080, 'bd-tlou.jpg');
const insideOut = movie('mv-inside-out', 'Inside Out', 2015, 'pool/movies/inside-out.jpg', {duration: 5700, progressSeconds: 540, backdropUrl: photo('bd-insideout.jpg')});
const rick = episode('ep-rick', 'Lawnmower Dog', 'Rick and Morty', 'show-rick', 1, 2, 1260, 769, 'bd-rick.jpg');
const solo = episode('ep-solo', "I'm Used to It", 'Solo Leveling', 'show-solo', 1, 1, 1440, 634, 'bd-solo.jpg');

const newOnServer = [
  movie('mv-toy-story-5', 'Toy Story 5', 2026, 'pool/movies/toy-story-5.jpg', {new: true}),
  show('show-stranger-things-85', "Stranger Things: Tales from '85", 1, 'p-st85.jpg', {new: true}),
  movie('mv-odyssey', 'The Odyssey', 2026, 'pool/movies/the-odyssey.jpg', {new: true}),
  show('show-severance', 'Severance', 1, 'pool/tv/severance.jpg'),
  movie('mv-amziah-king', 'The Rivals of Amziah King', 2026, 'pool/movies/the-rivals-of-amziah-king.jpg'),
  show('show-monster-lizzie', 'Monster: The Lizzie Borden Story', 1, 'pool/tv/monster-the-lizzie-borden-story.jpg'),
  show('show-jujutsu', 'JUJUTSU KAISEN', 1, 'p-jjk.jpg', {genres: ['Anime']}),
  movie('mv-practical-magic-2', 'Practical Magic 2', 2026, 'pool/movies/practical-magic-2.jpg'),
  show('show-neagley', 'Neagley', 1, 'pool/tv/neagley.jpg'),
];
const because = [
  movie('mv-spirited', 'Spirited Away', 2001, 'pool/movies/spirited-away.jpg'),
  movie('mv-walle', 'WALL·E', 2008, 'pool/movies/wall-e.jpg'),
  movie('mv-toy-story-3', 'Toy Story 3', 2010, 'pool/movies/toy-story-3.jpg'),
  movie('mv-incredibles', 'The Incredibles', 2004, 'pool/movies/the-incredibles.jpg'),
  movie('mv-spiderverse', 'Spider-Man: Into the Spider-Verse', 2018, 'pool/movies/spider-man-into-the-spider-verse.jpg'),
  movie('mv-your-name', 'Your Name.', 2016, 'pool/movies/your-name.jpg'),
  movie('mv-across-spiderverse', 'Spider-Man: Across the Spider-Verse', 2023, 'pool/movies/spider-man-across-the-spider-verse.jpg'),
  movie('mv-toy-story-2', 'Toy Story 2', 1999, 'pool/movies/toy-story-2.jpg'),
];
const collectionMembers = [
  movie('mv-toy-story', 'Toy Story', 1995, 'pool/movies/toy-story.jpg'),
  movie('mv-toy-story-2-c', 'Toy Story 2', 1999, 'pool/movies/toy-story-2.jpg'),
  movie('mv-toy-story-3-c', 'Toy Story 3', 2010, 'pool/movies/toy-story-3.jpg'),
  movie('mv-toy-story-5-c', 'Toy Story 5', 2026, 'pool/movies/toy-story-5.jpg'),
];
const mostWatched = [
  show('show-last-of-us', 'The Last of Us', 1, 'pool/tv/the-last-of-us.jpg'),
  show('show-stranger-things', 'Stranger Things', 4, 'pool/tv/stranger-things.jpg'),
  movie('mv-matrix', 'The Matrix', 1999, 'pool/movies/the-matrix.jpg'),
  show('show-mandalorian', 'The Mandalorian', 3, 'pool/tv/the-mandalorian.jpg'),
  movie('mv-shawshank', 'The Shawshank Redemption', 1994, 'pool/movies/the-shawshank-redemption.jpg'),
  show('show-rick-ranked', 'Rick and Morty', 7, 'pool/tv/rick-and-morty.jpg'),
];
const listen = [
  book('bk-martian', 'The Martian', 'Andy Weir', 'sq-martian.jpg', {duration: 34_320, progressSeconds: 4800}),
  book('bk-hail-mary', 'Project Hail Mary', 'Andy Weir', 'sq-hailmary.jpg'),
  album('al-after-hours', 'After Hours', 'The Weeknd', 'sq-afterhours.jpg'),
  book('bk-hobbit', 'The Hobbit', 'J. R. R. Tolkien', 'sq-hobbit.jpg'),
  album('al-abbey-road', 'Abbey Road', 'The Beatles', 'sq-abbey.jpg'),
  book('bk-hunger-games', 'The Hunger Games: Special Edition', 'Suzanne Collins', 'sq-hunger.jpg'),
  album('al-1989', "1989 (Taylor's Version)", 'Taylor Swift', 'sq-1989.jpg'),
  book('bk-pride', 'Pride and Prejudice', 'Jane Austen', 'sq-pride.jpg'),
];

const source: GuideSource = {id: 'src-synthetic', name: 'Synthetic Live TV', generation: '1', publishedAt: now, availableStart: now, availableEnd: instant(Date.parse(now) + 86_400_000), provenance: 'live-source', refreshState: 'ready'};
/** Offsets in minutes from the specimen instant: negative is earlier, positive is later. */
const programme = (id: string, channelId: string, title: string, startMinutes: number, endMinutes: number, extra: Partial<GuideProgramme> = {}): GuideProgramme => ({
  id, channelId, title, start: instant(Date.parse(now) + startMinutes * minute), end: instant(Date.parse(now) + endMinutes * minute),
  lineage: 'provider-id', seriesId: id, episodeId: id, newEvidence: 'unknown', description: title, ...extra,
});
const channel = (id: string, name: string, number: string, mark: string, group: string, programmeTitle: string, startMinutes: number, endMinutes: number, extra: Partial<GuideChannel> = {}): HomeChannel => ({
  source: {...source, id: 'src-synthetic'},
  actions: {play: true, favorite: true},
  channel: {
    id, sourceId: 'src-synthetic', provenance: 'live-source', name, number, group, generation: '1',
    mark: {text: mark, seed: `src-synthetic:${id}`}, programmes: [programme(`${id}-now`, id, programmeTitle, startMinutes, endMinutes)],
    tuneAvailable: true, recordAvailable: true, tuneUnavailableReason: '', recordUnavailableReason: '',
    favorite: false, hidden: false, preferenceRevision: 1, ...extra,
  },
});
const live = {
  revision: 'live-1', observedAt: instant(Date.parse(now) - minute), expiresAt: instant(Date.parse(now) + 30 * minute),
  sort: 'favorites' as const, group: 'all', groups: [{name: 'Synthetic Live TV', count: 5}],
  channels: [
    channel('ch-northwind', 'Northwind News', '101', 'NN', 'News', 'World Tonight', -32, 28),
    channel('ch-summit', 'Summit Sports', '201', 'SS', 'Sports', 'Cycling: Mountain Stage', -128, 122),
    channel('ch-lantern', 'Lantern Movies', '301', 'LM', 'Movies', 'Beneath the Glass Sky', -71, 9),
    channel('ch-pebble', 'Pebble Kids', '401', 'PK', 'Kids', 'Story Time Island', -110, 24, {programmes: [programme('ch-pebble-now', 'ch-pebble', 'Story Time Island', -3, 27, {flags: {new: true}})]}),
    channel('ch-atlas', 'Atlas Documentary', '501', 'AD', 'Documentary', 'How Bridges Stand', -57, 13),
  ],
};

const rows: readonly HomeRow[] = [
  row('continue', 'next.home.continueWatching', 'Continue Watching', 'landscape', [lastOfUs, insideOut, rick, solo], {
    required: true, priority: 10, privacySensitivity: 'personal',
    resumes: [{entryId: insideOut.id, target: {...insideOut, playback: {itemId: 'resume-inside-out', startSeconds: 540}}},
      {entryId: lastOfUs.id, target: {...lastOfUs, playback: {itemId: 'resume-last-of-us', startSeconds: 1080}}},
      {entryId: rick.id, target: {...rick, playback: {itemId: 'resume-rick', startSeconds: 769}}},
      {entryId: solo.id, target: {...solo, playback: {itemId: 'resume-solo', startSeconds: 634}}}],
  }),
  row('new_on_server', 'next.home.newOnServer', 'New on your server', 'poster', newOnServer, {
    subtitleText: {code: 'next.home.videoKinds', fallback: 'Movies, shows and anime'}, priority: 20, privacySensitivity: 'catalog',
    seeAll: {type: 'row', rowId: 'new_on_server'},
  }),
  row('because_you_watched', 'next.home.becauseYouWatched', 'Because you watched Inside Out', 'poster', because, {
    priority: 30, privacySensitivity: 'personal', seeAll: {type: 'row', rowId: 'because_you_watched'},
    titleText: {code: 'next.home.becauseYouWatched', params: {title: 'Inside Out'}, fallback: 'Because you watched Inside Out'}, // lint-strings-allow fixture data
  }),
  row('featured_collection', 'next.home.collection', 'Collection', 'poster', [], {
    priority: 40, privacySensitivity: 'catalog', seeAll: {type: 'collection', resourceId: 'col-toy-story', libraryId: 'lib-movies'},
    collection: {collection: {id: 'col-toy-story', libraryId: 'lib-movies', name: 'Toy Story', itemCount: 4, revision: 1}, mediaKind: 'movie',
      members: collectionMembers, backdropUrl: photo('bd-toystory.jpg'), firstYear: 1995, lastTitle: 'Toy Story 5'},
  }),
  row('on_now', 'next.home.onNow', 'On now', 'landscape', [], {
    subtitleText: {code: 'next.home.sourceName', params: {source: 'Synthetic Live TV'}, fallback: 'Synthetic Live TV'}, priority: 50, privacySensitivity: 'personal',
    seeAll: {type: 'guide'}, live,
  }),
  row('community_watching', 'next.home.mostWatched', 'Most watched on this server', 'poster', mostWatched, {
    priority: 60, privacySensitivity: 'aggregated',
    period: {days: 30, start: instant(Date.parse(now) - 30 * 86_400_000), end: now, metric: 'distinct_eligible_viewers',
      text: {code: 'next.home.last30Days', fallback: 'Last 30 days'}},
  }),
  row('listen_next', 'next.home.listenNext', 'Listen next', 'square', listen, {
    subtitleText: {code: 'next.home.audioKinds', fallback: 'Audiobooks and music'}, priority: 70, privacySensitivity: 'personal',
    seeAll: {type: 'row', rowId: 'listen_next'},
    resumes: [{entryId: 'bk-martian', target: {...listen[0], playback: {itemId: 'resume-martian', startSeconds: 4800}}}],
  }),
];

const spotlight: readonly HomeSpotlight[] = [
  {entry: entry('sp-severance', 'show', 'Severance', {year: 2022, genres: ['Drama', 'Mystery', 'Sci-Fi'], seasonCount: 1, posterUrl: photo('p-sev.jpg'), backdropUrl: photo('bd-sev.jpg'),
    overview: 'Mark leads a team of office workers whose memories have been surgically divided between their work and personal lives. When a mysterious colleague appears outside of work, it begins a journey to discover the truth about their jobs.',
    playback: {itemId: 'play-severance', startSeconds: 0}}),
    reason: {kind: 'new', text: {code: 'next.home.reason.newIn', params: {library: 'TV Shows'}, fallback: 'New in TV Shows'}},
    resumeTarget: entry('ep-severance-1', 'episode', 'Good News About Hell', {seasonNumber: 1, episodeNumber: 1, duration: 3420, progressSeconds: 0,
      show: {id: 'sp-severance', name: 'Severance'}, backdropUrl: photo('bd-sev.jpg'), playback: {itemId: 'play-severance-1', startSeconds: 0}})},

  {entry: entry('sp-spirited', 'movie', 'Spirited Away', {year: 2001, genres: ['Animation', 'Family'], duration: 7500, posterUrl: photo('p-spirited.jpg'), backdropUrl: photo('bd-spirited.jpg'),
    overview: 'A young girl, Chihiro, becomes trapped in a strange new world of spirits. When her parents undergo a mysterious transformation, she must call upon the courage she never knew she had to free her family.'}),
    reason: {kind: 'because_you_watched', text: {code: 'next.home.becauseYouWatched', params: {title: 'Inside Out'}, fallback: 'Because you watched Inside Out'}}}, // lint-strings-allow fixture data
  {entry: entry('sp-spiderverse', 'movie', 'Spider-Man: Into the Spider-Verse', {year: 2018, genres: ['Animation', 'Action'], duration: 7020, posterUrl: photo('p-spiderverse.jpg'), backdropUrl: photo('bd-spiderverse.jpg'),
    overview: 'Brooklyn teenager Miles Morales is bitten by a radioactive spider, and discovers a super collider that pulls Spider-People from across the Spider-Verse into his dimension.'}),
    reason: {kind: 'recommended', text: {code: 'next.home.reason.recommended', fallback: 'Recommended for you'}}},
  {entry: entry('sp-fellowship', 'movie', 'The Fellowship of the Ring', {year: 2001, genres: ['Adventure', 'Fantasy'], duration: 10_680, posterUrl: photo('p-lotr.jpg'), backdropUrl: photo('bd-lotr.jpg'),
    overview: 'Young hobbit Frodo Baggins inherits a mysterious ring from his uncle Bilbo, and a fellowship is formed to carry it to the only place it can be destroyed.'}),
    reason: {kind: 'recent', text: {code: 'next.home.reason.recentTo', params: {library: 'Movies'}, fallback: 'Recently added to Movies'}}},
];

const homeDocument: HomeDocument = {
  serverId: 'specimen', viewerFence: 'ds-web-home-r46', rows, spotlight: spotlight.map(slide => slide),
  layout: {revision: 1, rowOrder: rows.map(value => value.id), hiddenRowIds: []}, revision: {catalog: 1, viewer: 1}, generatedAt: now,
};

/** mock.html#extras uses exactly these three artwork states; the entries carry only what they have. */
const extrasReason = {kind: 'recommended', text: {code: 'next.home.reason.recommended', fallback: 'Recommended for you'}} as const;
const extrasSpotlights: readonly HomeSpotlight[] = [
  {entry: entry('sp-severance-extras', 'show', 'Severance', {year: 2022, genres: ['Drama', 'Mystery'], posterUrl: photo('p-sev.jpg'), backdropUrl: photo('bd-sev.jpg')}), reason: extrasReason},
  {entry: entry('sp-jjk-extras', 'show', 'JUJUTSU KAISEN', {year: 2020, genres: ['Anime'], seasonCount: 1, posterUrl: photo('p-jjk.jpg')}), reason: extrasReason},
  {entry: entry('sp-lanterns-extras', 'show', 'Lanterns', {year: 2026, genres: ['TV Shows'], seasonCount: 1}), reason: extrasReason},
];
const extrasDocument: HomeDocument = {...homeDocument, spotlight: extrasSpotlights};

const assetOrigin = new URLSearchParams(location.search).get('assets') ?? '/';
function useArtwork() {
  const runtime = useMemo(() => createArtworkRuntime({
    scopeKey: 'ds-web-home-r46-specimen',
    load: async ({url}, signal) => {
      const response = await fetch(assetOrigin + url, {signal, credentials: 'omit', redirect: 'error'});
      if (!response.ok) throw Object.assign(new Error('Specimen artwork could not be read.'), {status: response.status});
      const blob = await response.blob(), src = URL.createObjectURL(blob), image = new Image();
      image.src = src;
      try { await image.decode(); if (signal.aborted) throw signal.reason; return {src, width: image.naturalWidth, height: image.naturalHeight, bytes: blob.size}; }
      catch (error) { URL.revokeObjectURL(src); throw error; }
    },
    classify: error => ({retryable: error instanceof TypeError}),
    release: image => URL.revokeObjectURL(image.src),
  }), []);
  useEffect(() => () => runtime.dispose(), [runtime]);
  return useMemo(() => ({
    ...runtime,
    foreground: (url: string | undefined, _pixels: number, _aspect: number, request?: Parameters<typeof runtime.source>[1]) =>
      url ? runtime.source({url, cacheKey: url}, request) : undefined,
    ambient: (url: string | undefined, request?: Parameters<typeof runtime.source>[1]) => url
      ? {...runtime.source({url, cacheKey: url}, request), version: '0'.repeat(64), variant: 'ambient-v1' as const, maxEdge: 64 as const}
      : undefined,
  } as unknown as ShellArtwork), [runtime]);
}

const effects: HomeEffects = {
  play: async () => {},
  playChannel: async () => {},
  openEntry: () => {},
  openSeeAll: () => {},
  openMore: () => {},
  setMyList: async () => {},
  reportError: () => {},
};
const selection: HomeSelection = {
  toolbar: null,
  card: () => undefined,
  channel: () => undefined,
  collection: () => undefined,
  registerWindow: () => undefined,
  registerSpotlight: () => undefined,
  moreAvailable: () => false,
  moreExpanded: () => false,
};
const service = {getSnapshot: () => ({phase: 'ready'}), subscribe: () => () => {}} as unknown as Parameters<typeof HomeView>[0]['service'];

function Specimen() {
  const artwork = useArtwork();
  return <HomeView service={service} document={homeDocument} nowMs={nowMs} onMore={() => {}} refreshLive={() => {}}
    artwork={artwork} effects={effects} selection={selection} time={{t: defaultI18n.t, locale: 'en-US', timeZone: 'America/Halifax', hourCycle: 'h12'}} reducedMotion />;
}

const extrasLabels = ['next.artwork.native.backdrop', 'next.artwork.native.posterOnly', 'next.artwork.native.noArtwork'] as const;
function ExtrasGallery() {
  const artwork = useArtwork(), slides = homeSpotlights(extrasDocument, {t: defaultI18n.t, locale: 'en-US'});
  return <main data-home-extras className={extras.gallery}>
    <h3>{defaultI18n.t('next.artwork.native.galleryTitle')}</h3>
    <p className={extras.lead}>{defaultI18n.t('next.artwork.native.lead')}</p>
    {slides.map((slide, index) => <div key={slide.id} className={extras.fb}>
      <HomeHeroArtwork slide={slide} context={{index, visible: true}} platform="desktop" artwork={artwork} heroPixels={1440} variant="panel"/>
      <span className={extras.label}>{defaultI18n.t(extrasLabels[index]!)}</span>
      <div className={extras.copy}><h4 className={extras.title}>{slide.title}</h4><HeroFacts parts={slide.facts} platform="desktop"/></div>
    </div>)}
  </main>;
}

const extrasMode = new URLSearchParams(location.search).get('mode') === 'extras';
createRoot(document.getElementById('root')!).render(<><IconSprite />{extrasMode ? <ExtrasGallery/> : <main data-home-specimen><Specimen /></main>}</>);
