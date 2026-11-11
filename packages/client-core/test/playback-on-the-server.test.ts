import assert from 'node:assert/strict';
import test from 'node:test';
import type {V1Http, V1Request, V1Response} from '../src/playback-v1/http.ts';
import type {MediaPlayer, PlayerEvent, PlayerObservation, PlayerSource} from '../src/playback-v1/port.ts';
import {PlaybackSessionController, SessionsClient} from '../src/playback-v1/sessions.ts';
import {QueueClient} from '../src/playback-v1/queue.ts';
import {parseWebVtt} from '../src/playback-v1/webvtt.ts';
import {lyricsView} from '../src/playback-v1/track-lists.ts';
import {activeLyricLine} from '../src/lyrics.ts';

/** A session as the server sends it. */
const session = (id: string, startPositionMs = 0, generation = 1) => ({id, kind: 'vod', state: 'playing', itemId: 'itm_film', versionId: 'med_film',
  lease: {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 1000},
  presentation: {generation, mode: 'direct', url: `/v1/media/${id}/original`, startPositionMs, engine: 'native', route: 'original', quality: {mode: 'original'}, subtitles: [], decision: {}}});

/** Answers each request with the next of `answer`'s replies for its method and path; records every request. */
function server(answer: (request: V1Request) => V1Response | Promise<V1Response>): V1Http & {sent: V1Request[]} {
  const sent: V1Request[] = [];
  return {sent, async send(request) { sent.push(request); return answer(request); }};
}
const json = (status: number, body?: unknown): V1Response => ({status, headers: {}, ...(body === undefined ? {} : {body})});
const timers = {setTimer: (run: () => void, ms: number) => setTimeout(run, Math.min(ms, 5)), clearTimer: (t: unknown) => clearTimeout(t as ReturnType<typeof setTimeout>)};

test('a start the server answers 202 is the session: the same start is sent again, and one given up on is stopped', async () => {
  let starts = 0;
  const http = server(request => request.method === 'POST' ? (++starts < 3 ? json(202, {...session('pbs_1'), retryAfterMs: 300}) : json(201, session('pbs_1'))) : json(204));
  const started = await new SessionsClient(http, timers).start({itemId: 'itm_film'}, {state: 'playing'}, 'key-0000000000000001');
  assert.equal(started.id, 'pbs_1');
  const posts = http.sent.filter(r => r.method === 'POST');
  assert.equal(posts.length, 3);
  for (const post of posts) {
    assert.equal(post.headers?.['Idempotency-Key'], 'key-0000000000000001');
    assert.deepEqual(post.body, {itemId: 'itm_film', state: 'playing'});
  }

  // The viewer moves on while the stream gets ready: the session the 202 named is stopped.
  const abandoned = server(request => request.method === 'POST' ? json(202, {...session('pbs_2'), retryAfterMs: 300}) : json(204));
  const leave = new AbortController();
  const start = new SessionsClient(abandoned, {setTimer: (run, ms) => setTimeout(run, ms), clearTimer: t => clearTimeout(t as ReturnType<typeof setTimeout>)})
    .start({itemId: 'itm_film'}, {}, 'key-0000000000000002', {signal: leave.signal});
  await new Promise(resolve => setTimeout(resolve, 20));
  leave.abort();
  await assert.rejects(start, {name: 'AbortError'});
  await new Promise(resolve => setTimeout(resolve, 0));
  assert.ok(abandoned.sent.some(r => r.method === 'DELETE' && r.path === '/v1/playback/sessions/pbs_2'));
});

/** A player whose position the test sets. */
function player(): MediaPlayer & {loads: PlayerSource[]; at(positionMs: number): void} {
  const listeners = new Set<(event: PlayerEvent) => void>(), loads: PlayerSource[] = [];
  let reading: PlayerObservation = {generation: 0, state: 'idle', positionMs: 0, rate: 1};
  return {loads, load(source) { loads.push(source); reading = {generation: source.generation, state: 'playing', positionMs: source.startPositionMs, rate: 1}; },
    at(positionMs) { reading = {...reading, positionMs}; for (const l of listeners) l({type: 'time', observation: reading}); },
    play() {}, pause() {}, seek() {}, setRate() {}, selectSidecarSubtitle() {}, observe: () => reading,
    onEvent(listener) { listeners.add(listener); return () => listeners.delete(listener); }, dispose() {}};
}

test('after the server restarts and forgets the session, the title starts again where the player was', async () => {
  let forgotten = false, starts = 0;
  const http = server(request => {
    if (request.method === 'POST') { starts++; return json(201, session('pbs_' + starts, (request.body as {startPositionMs?: number}).startPositionMs ?? 0)); }
    if (request.method === 'PUT') return forgotten && request.path.includes('pbs_1') ? json(404, {error: {code: 'not_found', retry: false}}) : json(200, {expiresAt: '2099-01-01T00:00:00Z', reportEveryMs: 1000, generation: 1});
    if (request.method === 'GET') return json(404, {error: {code: 'not_found', retry: false}});
    return json(204);
  });
  const screen = player();
  const controller = new PlaybackSessionController({http, player: screen, timers: {...timers, now: () => Date.now()}});
  await controller.start({itemId: 'itm_film'}, {state: 'playing'});
  screen.at(42_000);
  forgotten = true;
  await controller.flush();
  for (let n = 0; n < 50 && starts < 2; n++) await new Promise(resolve => setTimeout(resolve, 5));
  assert.equal(starts, 2, 'the title was started again');
  const again = http.sent.filter(r => r.method === 'POST')[1]!;
  assert.equal((again.body as {startPositionMs?: number}).startPositionMs, 42_000);
  assert.equal(screen.loads.at(-1)?.startPositionMs, 42_000);
  assert.equal(controller.getSnapshot().phase, 'active');
  controller.dispose();
});

test('a queue made from a list carries the list’s filters, and moving on names the entry the device is on and the play that ended', async () => {
  const http = server(request => request.path === '/v1/queues'
    ? json(201, {queue: {id: 'que_1', revision: '1', medium: 'video', total: 2, current: {entryId: 'e1', position: 0}, remainingCount: 1, remainingMs: 1, repeat: 'off', shuffle: false, nextEntryId: 'e2', automaticAdvances: 0}, window: []})
    : json(200, {id: 'que_1', revision: '2', medium: 'video', total: 2, current: {entryId: 'e2', position: 1}, remainingCount: 0, remainingMs: 0, repeat: 'off', shuffle: false, automaticAdvances: 1}));
  const queues = new QueueClient(http, () => 'key-0000000000000003');
  const made = await queues.create([{source: {query: {libraryId: 'lib_films', pivot: 'movies', query: {variant: 'predicate', field: 'genre', operator: 'in', value: ['Drama']}, sort: [{field: 'year', direction: 'desc'}]}}, anchor: {itemId: 'itm_film'}}]);
  assert.deepEqual(http.sent[0]!.body, {source: {view: {library: 'lib_films', kinds: ['movie'], filter: [{field: 'genre', values: ['Drama']}], sort: 'year', desc: true}}, startAt: 'itm_film'});
  assert.equal(made.queue.next?.entryId, 'e2');
  const moved = await queues.advance('que_1', 'e1', 'completion', undefined, undefined, 'pbs_1');
  assert.deepEqual(http.sent[1]!.body, {reason: 'completion', fromEntryId: 'e1', sessionId: 'pbs_1'}, 'the end names the play that ended, so a resend under repeat one counts once');
  await queues.advance('que_1', 'e2', 'next', undefined, undefined, 'pbs_2');
  assert.deepEqual(http.sent[2]!.body, {reason: 'next', fromEntryId: 'e2'});
  assert.equal(moved.queue.current?.entryId, 'e2');
});

test('subtitles the server serves as WebVTT and lyrics it lists are read for the screens', () => {
  const cues = parseWebVtt('WEBVTT\n\n00:00.500 --> 00:59.000\nHello from the <b>subtitle</b> file\n\n01:00:01.000 --> 01:00:02.000 line:0\n<i>Top</i>\n');
  assert.deepEqual(cues.cues.map(c => [c.startUs, c.endUs, c.text, !!c.italic, !!c.top]), [['500000', '59000000', 'Hello from the subtitle file', false, false], ['3601000000', '3602000000', 'Top', true, true]]);
  const view = lyricsView({items: [{id: 'lyr_1', origin: 'sidecar', format: 'lrc', text: '[00:01.00]One\n[00:03.00]Two', offsetMs: 0, lines: [{atMs: 1000, text: 'One'}, {atMs: 3000, text: 'Two'}]}]},
    {serverId: 'srv', viewerId: 'viewer'}, {itemId: 'itm_song', libraryId: 'lib_music'});
  const shown = view.selection.resource!.document!;
  assert.equal(activeLyricLine(shown, 3.5, 0), 1);
});
