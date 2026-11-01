import test from 'node:test';
import assert from 'node:assert/strict';
import {ApiError} from '../src/index.ts';
import {BoundedReadError} from '../src/bounded-json.ts';
import {captionFor, compatContentApi, formatBytes, formatClock, formatDuration, iconFor, preferenceGroupTitle, preferenceLabel, preferenceValueLabel, presentError, sanitizeContentProjection, sentence, shapeFor} from '../src/presentation/index.ts';

test('shapeFor: posters for film, TV and anime everywhere; audiobooks square; landscape episodes only in the show workspace', () => {
  for (const kind of ['movie', 'show', 'season', 'episode', 'collection', 'category'] as const) {
    assert.equal(shapeFor(kind, 'shelf'), 'poster', kind);
    assert.equal(shapeFor(kind, 'grid'), 'poster', kind);
  }
  // Audiobook covers are square (2 Oct 2026), as the publishers make them.
  for (const kind of ['book', 'audiobook_file', 'book_series', 'chapter'] as const) assert.equal(shapeFor(kind, 'shelf'), 'square', kind);
  assert.equal(shapeFor('collection', 'shelf', 'audiobook'), 'square');
  assert.equal(shapeFor('episode', 'showWorkspace'), 'landscape');
  assert.equal(shapeFor('extra', 'detailExtras'), 'landscape');
  assert.equal(shapeFor('extra', 'shelf'), 'poster');
});

test('shapeFor: music is square; the artist hero is a circle; people are circles', () => {
  for (const kind of ['artist', 'album', 'song', 'disc'] as const) assert.equal(shapeFor(kind), 'square', kind);
  assert.equal(shapeFor('artist', 'hero'), 'circle');
  assert.equal(shapeFor('author'), 'circle');
  assert.equal(shapeFor('collection', 'shelf', 'music'), 'square');
});

test('presentError never echoes raw text and maps by code, status and type', () => {
  const secret = 'SELECT * FROM users; stack at foo.go:12 https://10.0.0.2/private';
  const cases: [unknown, string, string][] = [
    [new TypeError('Failed to fetch'), 'unreachable', 'try-again'],
    [new ApiError(401, 'unauthorized', secret), 'authentication', 'sign-in'],
    [new ApiError(403, 'forbidden', secret), 'permission', 'none'],
    [new ApiError(404, 'not_found', secret), 'not-found', 'none'],
    [new ApiError(409, 'administration_conflict', secret), 'changed', 'refresh'],
    [new ApiError(503, 'timeout', 'The operation timed out. Retry shortly.'), 'timeout', 'try-again'],
    [new ApiError(503, 'search_busy', secret), 'busy', 'try-again'],
    [new ApiError(500, 'internal_error', secret), 'unknown', 'try-again'],
    [new ApiError(418, 'something_new', secret), 'unknown', 'try-again'],
    [new BoundedReadError('response_too_large', secret), 'invalid-response', 'try-again'],
    [{code: 'invalid_home', message: secret}, 'invalid-response', 'try-again'],
    [new Error(secret), 'unknown', 'try-again'],
  ];
  for (const [error, category, action] of cases) {
    const p = presentError(error, 'library');
    assert.equal(p.category, category, String((error as {code?: string}).code ?? error));
    assert.equal(p.action, action);
    assert.ok(!p.body.includes('SELECT') && !p.body.includes('Retry shortly') && !p.body.includes('https://'), p.body);
    assert.equal(p.title, 'This library couldn’t load');
    assert.equal(p.silent, false);
  }
});

test('presentError: aborts are silent; offline is distinguished from an unreachable server', () => {
  const abort = new Error('The user aborted a request.');
  abort.name = 'AbortError';
  assert.equal(presentError(abort).silent, true);
  assert.equal(presentError('cancelled').silent, true);
  assert.equal(presentError(new TypeError('Load failed'), 'home', {deviceOnline: false}).category, 'offline');
});

test('presentError: when Portico is already retrying there is no Try again (PC-VISUAL §15.5)', () => {
  const p = presentError(new TypeError('Failed to fetch'), 'home', {retrying: true});
  assert.equal(p.action, 'automatic');
  assert.equal(p.actionLabel, undefined);
  assert.match(p.body, /keep trying/);
});

test('presentError: rate limits use the server hint; saves get save titles; playback keeps its own copy', () => {
  assert.match(presentError(new ApiError(429, 'rate_limited', 'x', true, 30)).body, /30 seconds/);
  // A source blocked after failures across many accounts is the same rate limit:
  // the server's own wait is shown, never the code.
  const blocked = presentError(new ApiError(429, 'source_blocked', 'x', true, 300), 'sign-in');
  assert.equal(blocked.category, 'rate-limited');
  assert.match(blocked.body, /300 seconds/);
  assert.equal(presentError(new ApiError(409, 'x_conflict', 'x'), 'preferences', {operation: 'save'}).title, 'The preference couldn’t be saved');
  const playback = presentError({code: 'transcoding_disabled'}, 'playback');
  assert.equal(playback.title, 'Playback stopped');
  assert.match(playback.body, /conversion/);
});

test('preference copy is platform-aware and never says browser or hover off the web', () => {
  for (const key of ['delivery.directPlay', 'delivery.transcode', 'appearance.reduceMotion']) {
    for (const platform of ['phone', 'tv'] as const) {
      const {label, help} = preferenceLabel(key, platform);
      assert.ok(!/browser|hover/i.test(`${label} ${help ?? ''}`), `${key} on ${platform}: ${help}`);
    }
  }
  assert.match(preferenceLabel('delivery.directPlay', 'web').help ?? '', /browser/);
  assert.equal(preferenceGroupTitle('appearance', 'tv').description, 'Applies to this TV.');
  assert.equal(preferenceLabel('music.audioNormalization', 'web').label, 'Volume normalization');
  assert.equal(preferenceLabel('playback.someNewSetting', 'web').label, 'Some new setting');
  assert.equal(preferenceValueLabel('playback.skipBackSeconds', 10), '10 seconds');
  assert.equal(preferenceValueLabel('playback.introSkip', 'auto'), 'Skip automatically');
});

test('shared content helpers', () => {
  assert.equal(iconFor('album'), 'music');
  assert.equal(iconFor('anime'), 'category');
  assert.equal(captionFor({kind: 'episode', seasonNumber: 2, episodeNumber: 5}), 'S2 · E5');
  assert.equal(formatDuration(6120), '1h 42m');
  assert.equal(formatClock(3723), '1:02:03');
  assert.equal(formatBytes(1_500_000_000), '1.4 GB');
  assert.equal(sentence('lastPlayedAt'), 'Last played at');
  assert.equal(sentence('in_progress'), 'In progress');
});

test('content compat drops unknown navigation views only, and no longer folds artwork objects', async () => {
  const projection = {navigation: [{view: 'browse'}, {view: 'episodes'}], sections: [{entries: [{id: 'a', artwork: {poster: {large: {url: '/p'}}}}]}]};
  const clean = sanitizeContentProjection(projection) as typeof projection;
  assert.deepEqual(clean.navigation, [{view: 'browse'}]);
  assert.equal((clean.sections[0].entries[0] as {posterUrl?: string}).posterUrl, undefined);
  const api = compatContentApi({request: async <T>() => projection as T});
  const read = await api.request<typeof projection>('/v1/libraries/x/content?view=browse');
  assert.equal(read.navigation.length, 1);
});

// Home's hero (2 Oct 2026): what the viewer was last in the middle of, named by the server and
// worded here for every client. These replace the per-client heroMeta tests.
test('homeHero: the server\'s entry, worded once for every client', async () => {
  const {homeHero} = await import('../src/presentation/home-hero.ts');
  const {createI18n} = await import('../../i18n/src/index.ts');
  const t = createI18n().t;
  const duration = (seconds: number) => `${Math.round(seconds / 60)}m`;
  const row = (id: string, entries: unknown[]) => ({id, title: id, kind: 'rail', entries});
  const episode = {id: 'e1', kind: 'episode', title: 'Ozymandias', subtitle: 'Breaking Bad · Season 5', seasonNumber: 5, episodeNumber: 14, year: 2013, contentRating: 'TV-MA', duration: 2820, progressSeconds: 1410, genres: ['Crime', 'Drama', 'Thriller'], backdropUrl: '/b', playback: {itemId: 'e1'}};
  const song = {id: 's1', kind: 'song', title: 'Time', artist: {id: 'a', name: 'Pink Floyd'}, album: {id: 'al', name: 'The Dark Side of the Moon'}, duration: 413, posterUrl: '/p'};
  const doc = (hero: unknown, rows: unknown[]) => ({rows, hero}) as never;

  const watching = homeHero(doc({rowId: 'continue', entryId: 'e1'}, [row('continue', [episode]), row('continue_listening', [song])]), t, {duration})!;
  assert.equal(watching.title, 'Breaking Bad');
  assert.equal(watching.line, 'S5 E14 · Ozymandias');
  // The show is the title, so the meta line does not say it again.
  assert.equal(watching.meta, '2013 · TV-MA · 47m · Crime · Drama');
  assert.equal(watching.progress, 0.5);
  assert.equal(watching.playLabel, t('entry.resumeRemaining', {remaining: '24m'}));
  assert.equal(watching.art, 'backdrop');

  // A client may have nothing to say about a short stretch (Apple writes no length under two
  // minutes): the button is then plain "Resume", never "Resume ·  left".
  const terse = (seconds: number) => (seconds >= 120 ? duration(seconds) : '');
  const nearlyDone = (left: number) => homeHero(doc({rowId: 'continue', entryId: 'e1'}, [row('continue', [{...episode, progressSeconds: episode.duration - left}])]), t, {duration: terse})!;
  assert.equal(nearlyDone(90).playLabel, t('entry.resume'));
  assert.equal(nearlyDone(60).playLabel, t('entry.resume'));
  assert.equal(nearlyDone(120).playLabel, t('entry.resumeRemaining', {remaining: '2m'}));
  assert.equal(nearlyDone(30).playLabel, t('entry.resume'));

  const listening = homeHero(doc({rowId: 'continue_listening', entryId: 's1'}, [row('continue_listening', [song])]), t, {duration})!;
  assert.equal(listening.title, 'Time');
  assert.equal(listening.line, 'Pink Floyd · The Dark Side of the Moon');
  assert.equal(listening.playLabel, t('action.play'));
  assert.deepEqual([listening.art, listening.coverShape], ['cover', 'square']);

  // No hero without the server naming one, when its entry was just removed, or when the row is gone.
  assert.equal(homeHero(doc(undefined, [row('continue', [episode])]), t, {duration}), undefined);
  assert.equal(homeHero(doc({rowId: 'continue', entryId: 'e1'}, [row('continue', [episode])]), t, {duration, hidden: new Set(['e1'])}), undefined);
  assert.equal(homeHero(doc({rowId: 'continue', entryId: 'e1'}, []), t, {duration}), undefined);
});

test('cardLines: TV in Plex\'s shape — an episode reads show, episode, "S1 · E3"; a season show, "Season 1"; a show added whole its seasons', async () => {
  const {cardLines, continueLines} = await import('../src/presentation/card-caption.ts');
  const {createI18n} = await import('../../i18n/src/index.ts');
  const {t} = createI18n();
  assert.deepEqual(cardLines({kind: 'episode', title: 'Grilled', subtitle: 'Breaking Bad', seasonNumber: 2, episodeNumber: 2}, t), {title: 'Breaking Bad', caption: 'Grilled', detail: 'S2 · E2'});
  // Numbered across seasons, or without seasons, the third line says so as the show does.
  assert.equal(cardLines({kind: 'episode', title: 'Romance Dawn', subtitle: 'One Piece', absoluteNumber: 104, episodeNumber: 1}, t).detail, 'Episode 104');
  assert.equal(cardLines({kind: 'episode', title: 'Pilot', subtitle: 'Miniseries', episodeNumber: 3}, t).detail, 'E3');
  // Without its show, an episode keeps its own title over its number (never an empty first line).
  assert.deepEqual(cardLines({kind: 'episode', title: 'Pilot', seasonNumber: 1, episodeNumber: 1}, t), {title: 'Pilot', caption: 'S1 · E1'});
  assert.deepEqual(cardLines({kind: 'season', title: 'Season 3', subtitle: 'Stranger Things', seasonNumber: 3}, t), {title: 'Stranger Things', caption: 'Season 3'});
  assert.deepEqual(cardLines({kind: 'season', title: 'Specials', subtitle: 'Doctor Who', seasonNumber: 0}, t), {title: 'Doctor Who', caption: 'Specials'});
  assert.deepEqual(cardLines({kind: 'show', title: 'Rizzoli & Isles', seasonCount: 6, year: 2010}, t), {title: 'Rizzoli & Isles', caption: '6 seasons'});
  // A show anywhere else, and every other kind, keeps its usual caption.
  assert.deepEqual(cardLines({kind: 'show', title: 'Silo', year: 2023}, t), {title: 'Silo', caption: '2023'});
  assert.deepEqual(cardLines({kind: 'movie', title: 'Inside Out', year: 2015}, t), {title: 'Inside Out', caption: '2015'});
  // Continue Watching: an episode as everywhere else; a film says what is left.
  assert.deepEqual(continueLines({kind: 'episode', title: 'Grilled', subtitle: 'Breaking Bad', seasonNumber: 2, episodeNumber: 2, duration: 2700, progressSeconds: 1260}, t), {title: 'Breaking Bad', caption: 'Grilled', detail: 'S2 · E2'});
  assert.deepEqual(continueLines({kind: 'movie', title: 'Inside Out', duration: 5700, progressSeconds: 4260}, t), {title: 'Inside Out', caption: '24 min left'});
});

test('cardCaption: a group counted only up to a threshold reads "10,000+ titles"; an exact count is unchanged', async () => {
  const {cardCaption} = await import('../src/presentation/card-caption.ts');
  const {createI18n} = await import('../../i18n/src/index.ts');
  const {t} = createI18n();
  assert.equal(cardCaption({kind: 'category', count: 10000, countIsLowerBound: true}, t), '10,000+ titles');
  assert.equal(cardCaption({kind: 'category', count: 10000}, t), t('title.itemCount', {count: 10000}));
  assert.equal(cardCaption({kind: 'collection', count: 12}, t), t('title.itemCount', {count: 12}));
  // A lower bound of nothing says nothing, as an exact zero does.
  assert.equal(cardCaption({kind: 'category', count: 0, countIsLowerBound: true}, t), undefined);
});
