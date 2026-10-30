/**
 * The Vega player engine (`PorticoPlaybackModule`) against the engine contract every platform's
 * engines keep (`apps/native/Native/ENGINE-CONTRACT.md`): what it says of itself, the lease, the
 * commands it refuses, and the facts it answers with.
 */
import test, {afterEach} from 'node:test';
import assert from 'node:assert/strict';
import {resetServer, segment, server} from './stubs/server.mjs';
import {resetWorld, world} from './stubs/w3cmedia.mjs';
import {AppState} from './stubs/kepler.mjs';
import {PorticoPlaybackModule as engine, playbackEvents} from '../src/bridge/playback.ts';
import {subtitleDisplay} from '../src/bridge/subtitles.ts';
import {requestCredentials, verifiedCredentialOrigin} from '../src/bridge/credential-boundary.ts';
// The real shared route owner has verified these same-server routes before engine commands.
for (const origin of ['http://server.test', 'http://other.test']) {
  requestCredentials(origin + '/v1/networking/identity-proof', {}, JSON.stringify({baseUrl: origin, nonce: 'N'.repeat(43)}));
  verifiedCredentialOrigin({kind: 'portico.route-proof', version: '1', baseUrl: origin, nonce: 'N'.repeat(43), fingerprint: 'test-server'});
}

const base = 'http://server.test/v1/media/GRANT/';
const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));
async function until(what: () => boolean, ms = 4000): Promise<void> {
  const end = Date.now() + ms;
  while (!what()) { if (Date.now() > end) throw new Error('timed out waiting'); await sleep(10); }
}

type Fact = Record<string, any>;
let facts: Fact[] = [];
playbackEvents.addListener('PorticoPlaybackFact', (fact: Fact) => facts.push(fact));
const of = (type: string) => facts.filter(fact => fact.type === type);
const player = () => world.players[world.players.length - 1] as any;

let lease = '';
let revision = 0;
afterEach(async () => { if (lease) await engine.release(lease); lease = ''; revision = 0; facts = []; resetServer(); resetWorld(); });

async function acquire(): Promise<string> {
  lease = (await engine.acquire('test-owner')).lease;
  return lease;
}
const binding = (more: object = {}) => ({lease, intentId: 1, sessionId: 'session', generation: 1, itemId: 'item', ...more});
const command = (more: Record<string, unknown> = {}) => ({binding: binding(), source: base + 'index.m3u8', paused: false, rate: 1, commandRevision: ++revision, connectionState: 'ready', routeRevision: 0, pendingSeek: null, timelineOriginMs: null, audio: null, subtitles: null, ...more}) as never;

function film(count = 40, bytes = 0, base = 'http://server.test/v1/media/GRANT/') {
  let text = '#EXTM3U\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-TARGETDURATION:7\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-MAP:URI="init.mp4"\n';
  server.routes.set(base + 'init.mp4', segment({init: true}));
  for (let n = 0; n < count; n++) { text += `#EXTINF:6.000,\ns${n}.m4s\n`; server.routes.set(`${base}s${n}.m4s`, segment({pts: 1 + n * 6, duration: 6, bytes})); }
  server.routes.set(base + 'index.m3u8', text + '#EXT-X-ENDLIST\n');
}

test('the engine says what it is and what it does not do', () => {
  assert.equal(engine.engine, 'native');
  assert.equal(engine.available, true);
  // No Picture in Picture, no AirPlay, plain-text subtitles only: the player screen hides the rest
  // and the server burns styled and image subtitles in.
  assert.deepEqual(engine.features, {pictureInPicture: false, airPlayVideo: false, liveChannels: true, musicSessions: false, audioTrackSwitching: false, subtitleRendering: ['text'], chapters: false});
});

test('one lease, to one owner', async () => {
  const first = await engine.acquire('test-owner');
  lease = first.lease;
  assert.equal(first.engine, 'native');
  assert.deepEqual(await engine.acquire('test-owner'), first);
  await assert.rejects(engine.acquire('someone-else'), (error: any) => error.code === 'owner_busy');
  assert.equal(await engine.release('not-the-lease'), true);
  await assert.rejects(engine.acquire('someone-else'), (error: any) => error.code === 'owner_busy');
});

test('a command is taken whole or refused with the contract\'s words', async () => {
  await acquire();
  const refused = async (value: Record<string, unknown>, message: string) => { revision--; await assert.rejects(engine.command(command(value)), (error: any) => error.code === 'command_rejected' && error.message === message); };
  revision = 1;
  await refused({binding: binding({lease: 'old'})}, 'Playback lease is no longer active.');
  await refused({paused: 0}, 'Invalid paused flag.');
  await refused({rate: NaN}, 'Invalid playback command.');
  await refused({binding: binding({intentId: 0})}, 'Invalid playback binding.');
  await refused({timelineOriginMs: -5}, 'Invalid channel clock.');
  await refused({connectionState: 'elsewhere'}, 'Invalid route transition.');
  revision = 0;
  await refused({}, 'Invalid playback revision.');
});

test('a title: ready, the seek it opened with, then progress with what was measured of the connection', async () => {
  film(40, 100000);
  server.delay = 5;
  await acquire();
  await engine.command(command({pendingSeek: {revision: 1, positionSeconds: 62}}));
  await until(() => of('seekApplied').length === 1);
  // Filled from where it starts, not from the beginning.
  assert.equal(server.log.includes(base + 's0.m4s'), false);
  assert.equal(of('ready').length, 1);
  assert.equal(of('seekApplied')[0].seekRevision, 1);
  assert.ok(Math.abs(of('seekApplied')[0].position - 62) < 0.01);
  // JavaScript retires the seek; the engine then plays.
  await engine.command(command());
  await until(() => of('firstFrame').length === 1 && of('progress').length >= 1);
  const fact = of('progress')[0];
  assert.equal(fact.instanceId, (await engine.acquire('test-owner')).instanceId);
  assert.deepEqual(fact.binding, binding());
  assert.equal(fact.lastCommandRevision, revision);
  assert.deepEqual(fact.supportedRates, [1]);
  assert.equal(fact.paused, false);
  assert.ok(fact.bufferedSeconds > 20, `buffered ${fact.bufferedSeconds}`);
  assert.ok(fact.throughputKbps > 1000, `throughput ${fact.throughputKbps}`);
  // Sequences only ever rise.
  assert.deepEqual(facts.map(f => f.factSequence), [...facts.map(f => f.factSequence)].sort((a, b) => a - b));
});

test('progress is said while the picture moves and once when it stops', async () => {
  film();
  await acquire();
  await engine.command(command({paused: true}));
  await until(() => of('ready').length === 1);
  await sleep(1700);
  assert.equal(of('progress').length, 1);
  assert.equal(of('progress')[0].paused, true);
  // Playing: time moves, and each half second says so.
  await engine.command(command());
  await until(() => of('firstFrame').length === 1);
  const before = of('progress').length;
  for (let i = 0; i < 4; i++) { player().advance(0.5); await sleep(510); }
  assert.ok(of('progress').length >= before + 3, `${of('progress').length - before} more`);
  // Paused again: one more, then nothing.
  await engine.command(command({paused: true}));
  await sleep(700);
  const paused = of('progress').length;
  await sleep(1200);
  assert.equal(of('progress').length, paused);
});

test('while the route is in doubt the engine is silent, and it opens again where it was', async () => {
  film();
  await acquire();
  await engine.command(command({pendingSeek: {revision: 1, positionSeconds: 30}}));
  await until(() => of('seekApplied').length === 1);
  await engine.command(command());
  await until(() => of('firstFrame').length === 1);
  player().advance(100);
  const closedPlayer = player();
  await engine.command(command({connectionState: 'verifying', routeRevision: 1, paused: true}));
  await sleep(50);
  assert.equal(closedPlayer.initialized, false);
  facts = [];
  server.log = [];
  await sleep(700);
  assert.deepEqual(facts, []);
  // Another route to the same server: another address for the same title.
  const other = 'http://other.test/v1/media/GRANT/';
  for (const [url, answer] of [...server.routes]) server.routes.set(url.replace(base, other), answer);
  await engine.command(command({source: other + 'index.m3u8', routeRevision: 2}));
  await until(() => of('ready').length === 1 && of('firstFrame').length === 1);
  assert.notEqual(player(), closedPlayer);
  // 130 s in is segment 21: the stream is opened there, and that is where the element is sent.
  assert.equal(server.log.find(url => url.endsWith('.m4s')), other + 's21.m4s');
  await until(() => of('progress').length >= 1);
  assert.ok(Math.abs(of('progress')[0].position - 130) < 0.01, `at ${of('progress')[0].position}`);
});

test('leaving the app pauses a video and says so; coming back leaves it for the viewer to continue', async () => {
  film();
  await acquire();
  await engine.command(command());
  await until(() => of('firstFrame').length === 1);
  assert.equal(player().paused, false);
  AppState.emit('background');
  assert.equal(player().paused, true);
  assert.equal(of('systemPaused').length, 1);
  assert.equal(of('systemPaused')[0].reason, 'background');
  // Still wanted playing as far as the engine was told, but it stays paused while away.
  await engine.command(command({rate: 1, subtitles: null, audio: null, routeRevision: 0, paused: false, pendingSeek: null, timelineOriginMs: null, connectionState: 'ready'}));
  assert.equal(player().paused, true);
  // JavaScript follows (its intent becomes paused), and the app returns: paused until Play.
  await engine.command(command({paused: true}));
  AppState.emit('active');
  assert.equal(player().paused, true);
  await engine.command(command());
  assert.equal(player().paused, false);
  // A video that was already paused says nothing when the app leaves.
  await engine.command(command({paused: true}));
  AppState.emit('background');
  assert.equal(of('systemPaused').length, 1);
  AppState.emit('active');
});

test('a video that becomes ready while the app is away does not start behind its back', async () => {
  film();
  await acquire();
  AppState.emit('background');
  await engine.command(command());
  await until(() => of('ready').length === 1 && of('systemPaused').length === 1);
  assert.equal(player().paused, true);
  AppState.emit('active');
  assert.equal(player().paused, false);
});

test('a title that stops says why, in the contract\'s terms', async () => {
  // The server refuses a segment.
  film();
  server.routes.set(base + 's1.m4s', {status: 410});
  await acquire();
  await engine.command(command());
  await until(() => of('error').length === 1);
  let error = of('error')[0];
  assert.deepEqual([error.httpStatus, error.deliveryError, error.networkError, error.engineCode, error.uncertain, error.paused], [410, true, false, '', false, true]);
  assert.equal(typeof error.hasPlayed, 'boolean');
  await engine.release(lease);

  // The connection gives no answer.
  facts = []; revision = 0;
  server.offline = true;
  await acquire();
  await engine.command(command());
  await until(() => of('error').length === 1);
  error = of('error')[0];
  assert.deepEqual([error.httpStatus, error.deliveryError, error.networkError, error.engineCode, error.uncertain, error.hasPlayed], [0, false, true, '', false, false]);
  assert.equal(error.error, 'The media connection was interrupted. Verifying another route.');
  server.offline = false;
  await engine.release(lease);

  // The media element will not take the stream: the server is told, and delivers it another way.
  facts = []; revision = 0;
  film();
  world.refuse = /video\/mp4/;
  await acquire();
  await engine.command(command());
  await until(() => of('error').length === 1);
  error = of('error')[0];
  assert.deepEqual([error.networkError, error.deliveryError, error.engineCode, error.uncertain], [false, false, 'source_not_supported', false]);
  world.refuse = null;
  await engine.release(lease);

  // A file from a plain-HTTP address is one the element cannot open.
  facts = []; revision = 0;
  await acquire();
  await engine.command(command({source: 'http://server.test/v1/media/GRANT/file.mp4'}));
  await until(() => of('error').length === 1);
  assert.equal(of('error')[0].engineCode, 'source_not_supported');
  await engine.release(lease);

  // Not an address at all.
  facts = []; revision = 0;
  await acquire();
  await engine.command(command({source: 'ftp://server.test/file.mp4'}));
  await until(() => of('error').length === 1);
  assert.equal(of('error')[0].error, 'Invalid media address.');
});

test('an opaque HTTPS direct file is declined so the server can deliver an observable remux', async () => {
  await acquire();
  await engine.command(command({source: 'https://server.test/v1/media/GRANT/file.mp4', paused: true}));
  await until(() => of('error').length === 1);
  assert.equal(of('error')[0].engineCode, 'source_not_supported');
  assert.notEqual(player().src, 'https://server.test/v1/media/GRANT/file.mp4');
});

test('a live channel reports its place on the channel\'s timeline and nothing of the connection', async () => {
  const origin = Date.parse('2026-10-13T18:00:00Z');
  let text = '#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:100\n';
  for (let n = 100; n < 112; n++) { text += `#EXT-X-PROGRAM-DATE-TIME:${new Date(origin + n * 4000).toISOString()}\n#EXTINF:4.000000,\ns${n}.ts\n`; server.routes.set(`${base}s${n}.ts`, segment({pts: 9000 + n * 4, duration: 4, bytes: 100000})); }
  server.routes.set(base + 'index.m3u8', text);
  await acquire();
  await engine.command(command({timelineOriginMs: origin}));
  await until(() => of('ready').length === 1 && of('progress').length >= 1);
  // Where live viewing begins: three segments behind the newest.
  assert.ok(Math.abs(of('progress')[0].position - 109 * 4) < 0.01, `at ${of('progress')[0].position}`);
  assert.equal(of('progress')[0].throughputKbps, undefined);
  assert.equal(of('progress')[0].bufferedSeconds, undefined);
  // Back to a moment still in the channel's window.
  await engine.command(command({timelineOriginMs: origin, pendingSeek: {revision: 1, positionSeconds: 104 * 4 + 2}}));
  await until(() => of('seekApplied').length === 1);
  assert.ok(Math.abs(of('seekApplied')[0].position - (104 * 4 + 2)) < 0.01, `at ${of('seekApplied')[0].position}`);
});

test('subtitles: the session\'s own cue document is drawn in the profile\'s caption style, with its offset', async () => {
  film();
  const document = base.replace('/v1/media/GRANT/', '/v1/media/subtitles/GRANT/res/3');
  let asked = 0;
  server.routes.set(document, ({headers}: {headers: Record<string, string>}) => {
    asked++;
    return {status: 200, headers: {'Content-Type': 'application/json; charset=utf-8', ETag: '"v3"'}, text: JSON.stringify({version: 1, timeDomain: 'source-relative', cues: [{startUs: '10000000', endUs: '14000000', text: 'Hello there'}]}), sent: headers};
  });
  const subtitles = (more: object = {}) => ({binding: {intentId: 1, sessionId: 'session', generation: 1}, mode: 'track', revision: 1, resourceId: 'res', resourceRevision: 3, refreshRevision: 0, offsetUs: '2000000', documentUrl: document, style: {scale: 1.3, background: 'none'}, ...more});
  await acquire();
  await engine.command(command({paused: true, subtitles: subtitles()}));
  await until(() => of('subtitleStatus').some(fact => fact.error === ''));
  assert.equal(asked, 1);
  // Ten seconds in, with the track shown two seconds later: nothing yet at 11 s, the line at 12.5 s.
  player().currentTime = 1 + 11;
  await sleep(250);
  assert.equal(subtitleDisplay.current().display.text, '');
  player().currentTime = 1 + 12.5;
  await until(() => subtitleDisplay.current().display.text === 'Hello there');
  assert.deepEqual(subtitleDisplay.current().style, {scale: 1.3, background: 'none', color: 'white', position: 'bottom', font: 'default'});
  // The viewer changes the size: the same document, drawn anew, not fetched again.
  await engine.command(command({paused: true, subtitles: subtitles({style: {scale: 1.6, background: 'opaque'}})}));
  await until(() => subtitleDisplay.current().style.scale === 1.6);
  assert.equal(asked, 1);
  // Off.
  await engine.command(command({paused: true, subtitles: {binding: {intentId: 1, sessionId: 'session', generation: 1}, mode: 'off', revision: 2}}));
  assert.equal(subtitleDisplay.current().display.text, '');
});

test('subtitles: an address that is not the session\'s own is refused', async () => {
  film();
  await acquire();
  await engine.command(command({paused: true, subtitles: {binding: {intentId: 1, sessionId: 'session', generation: 1}, mode: 'track', revision: 1, resourceId: 'res', resourceRevision: 3, refreshRevision: 0, offsetUs: '0', documentUrl: 'http://elsewhere.test/v1/media/subtitles/GRANT/res/3'}}));
  await until(() => of('subtitleStatus').length >= 1);
  assert.equal(of('subtitleStatus')[0].error, 'The subtitle address does not belong to this playback session.');
});

test('the element stays paused until JavaScript retires the seek command', async () => {
  film();
  await acquire();
  await engine.command(command({pendingSeek: {revision: 1, positionSeconds: 62}}));
  await until(() => of('seekApplied').length === 1);
  assert.equal(player().paused, true, 'seekApplied does not retire the command');
  assert.equal(of('firstFrame').length, 0);
  await engine.command(command());
  await until(() => of('firstFrame').length === 1);
  assert.equal(player().paused, false);
});

test('an unsupported rate holds actual playback paused', async () => {
  film();
  await acquire();
  await engine.command(command({rate: 1.5}));
  await until(() => of('ready').length === 1);
  assert.equal(player().paused, true);
  assert.equal(of('firstFrame').length, 0);
  await engine.command(command());
  await until(() => of('firstFrame').length === 1);
});

test('stale intents and changed bindings under the same intent are refused atomically', async () => {
  film();
  await acquire();
  await engine.command(command({paused: true, binding: binding({intentId: 2})}));
  await until(() => of('ready').length === 1);
  const current = player();
  for (const change of [{intentId: 1}, {intentId: 2, itemId: 'other'}, {intentId: 2, sessionId: 'other', generation: 2}]) {
    await assert.rejects(engine.command(command({binding: binding(change)})), (e: any) => e.code === 'command_rejected' && e.message === 'Stale playback binding.');
    assert.equal(player(), current);
    assert.equal(player().paused, true);
  }
});

test('a newer generation of the same session under the same intent is a re-plan in place: its source opens', async () => {
  film();
  film(40, 0, 'http://server.test/v1/media/REPLAN/');
  const replanned = 'http://server.test/v1/media/REPLAN/index.m3u8';
  await acquire();
  await engine.command(command({paused: true}));
  await until(() => of('ready').length === 1);
  const first = player();
  // A subtitle or quality change: the server's new presentation of the same session.
  await engine.command(command({paused: true, binding: binding({generation: 2}), source: replanned}));
  assert.notEqual(player(), first);
  await until(() => of('ready').some(fact => fact.binding.generation === 2));
  // The presentation it replaced is not taken back.
  await assert.rejects(engine.command(command({paused: true})), (e: any) => e.code === 'command_rejected' && e.message === 'Stale playback binding.');
});

test('invalid, conflicting and retired seeks keep the accepted state intact', async () => {
  film();
  await acquire();
  for (const pendingSeek of [{revision: 1, positionSeconds: NaN}, {revision: 1, positionSeconds: -1}]) {
    await assert.rejects(engine.command(command({pendingSeek})), (e: any) => e.message === 'Invalid seek command.');
  }
  await assert.rejects(engine.command(command({pendingSeek: {revision: 1.5, positionSeconds: 10}})), (e: any) => e.message === 'Invalid seek revision.');
  await engine.command(command({paused: true, pendingSeek: {revision: 2, positionSeconds: 62}}));
  await until(() => of('seekApplied').length === 1);
  await assert.rejects(engine.command(command({pendingSeek: {revision: 1, positionSeconds: 62}})), (e: any) => e.message === 'Stale seek command.');
  await assert.rejects(engine.command(command({pendingSeek: {revision: 2, positionSeconds: 63}})), (e: any) => e.message === 'Conflicting or retired seek command.');
  await engine.command(command({paused: true}));
  await assert.rejects(engine.command(command({pendingSeek: {revision: 2, positionSeconds: 62}})), (e: any) => e.message === 'Conflicting or retired seek command.');
});

test('a seeked event at the wrong position reports failure and keeps the element paused', async () => {
  film();
  await acquire();
  await engine.command(command({paused: true}));
  await until(() => of('ready').length === 1);
  world.seekClamp = 0;
  await engine.command(command({pendingSeek: {revision: 1, positionSeconds: 62}}));
  await until(() => of('seekApplied').length + of('seekFailed').length > 0);
  assert.equal(of('seekApplied').length, 0);
  assert.equal(of('seekFailed').length, 1);
  assert.equal(player().paused, true);
});

test('a route that cannot restore the retained position never announces ready or starts over', async () => {
  film();
  for (const [url, answer] of [...server.routes]) server.routes.set(url.replace(base, 'http://other.test/v1/media/GRANT/'), answer);
  await acquire();
  await engine.command(command());
  await until(() => of('firstFrame').length === 1);
  player().advance(130);
  await engine.command(command({connectionState: 'verifying', routeRevision: 1}));
  facts = [];
  world.seekClamp = 0;
  await engine.command(command({source: 'http://other.test/v1/media/GRANT/index.m3u8', routeRevision: 2}));
  await until(() => of('ready').length + of('error').length > 0);
  assert.equal(of('ready').length, 0);
  assert.equal(of('firstFrame').length, 0);
  assert.equal(of('error')[0]?.error, 'The previous playback position is unavailable on this connection. Your position has been retained.');
  assert.equal(player().paused, true);
});
