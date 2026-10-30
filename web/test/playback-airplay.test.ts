import test from 'node:test';
import assert from 'node:assert/strict';
import {WebMediaPlayer, type HlsModule} from '../src/platform/playback/web-media-player.ts';
import {airPlaySourceReady} from '../src/platform/playback/airplay.ts';
import {FakeVideo, fakeHls, manualClock} from './runtime-media-fixture.ts';

const settle = async () => {for (let i = 0; i < 12; i++) await Promise.resolve();};

class Source {
  src = ''; type = ''; parent?: SafariVideo;
  remove() {if (this.parent) this.parent.children = this.parent.children.filter(child => child !== this);this.parent = undefined;}
}
class SafariVideo extends FakeVideo {
  children: Source[] = [];disableRemotePlayback = false;webkitCurrentPlaybackTargetIsWireless = false;
  webkitShowPlaybackTargetPicker = () => {};
  ownerDocument = {createElement: () => new Source()};nativeHls = 'probably';
  appendChild(source: Source) {this.children.push(source);source.parent = this;return source;}
  querySelectorAll() {return this.children;}
  getAttribute(name: string) {return name === 'src' ? this.src || null : null;}
}
const source = (generation = 1) => ({generation, sessionId: 'session', mode: 'stream' as const, url: `/v1/media/grant-${generation}/master.m3u8`, startPositionMs: 42000, autoplay: true, subtitles: []});
function fixture(managed: boolean, video = new SafariVideo(), load?: (module: HlsModule) => Promise<HlsModule>) {
  const base = fakeHls();
  const Events = {...base.Events, MEDIA_ATTACHING: 'hlsMediaAttaching', MEDIA_DETACHING: 'hlsMediaDetaching'};
  const Hls = class extends base.Hls {
    static Events = Events;
    attachMedia() {
      // Vendored buffer-controller: classic MSE uses src; MMS removes children and appends blob.
      if (managed) {
        video.removeAttribute('src');video.disableRemotePlayback = true;
        for (const child of [...video.children]) child.remove();
        video.appendChild(Object.assign(new Source(), {src: 'blob:managed', type: 'video/mp4'}));video.load();
      } else video.src = 'blob:classic';
      base.instances.at(-1)!.emit(Events.MEDIA_ATTACHING);
    }
  } as HlsModule;
  let origin = 'https://server.test';
  const player = new WebMediaPlayer(video, {loadHls: () => load ? load(Hls) : Promise.resolve(Hls), nativeHls: 'fallback', resolveUrl: path => origin + path, timers: manualClock(), externalRoute: () => video.webkitCurrentPlaybackTargetIsWireless ? 'airplay' : undefined});
  return {player, video, instances: base.instances, Events, prepare: (generation = 1) => player.setAirPlaySource({sessionId: 'session', generation, url: `/v1/media/receiver-${generation}/master.m3u8`}), route: (next: string) => {origin = next;}};
}
for (const managed of [false, true]) test(`AirPlay ${managed ? 'MMS' : 'classic MSE'} owns blob-first absolute alternatives across generations and server routes`, async () => {
  const f = fixture(managed);
  try {
    f.player.load(source());await settle();f.prepare();
    assert.equal(f.video.src, '', 'src must not override source alternatives');
    assert.deepEqual(f.video.children.map(child => [child.src, child.type]), [[managed ? 'blob:managed' : 'blob:classic', 'video/mp4'], ['https://server.test/v1/media/receiver-1/master.m3u8', 'application/vnd.apple.mpegurl']]);
    assert.equal(f.instances[0]!.config.startPosition, 42);assert.equal(f.instances[0]!.config.preferManagedMediaSource, undefined);
    assert.equal(f.video.disableRemotePlayback, false);assert.equal(airPlaySourceReady(f.video, 1), true);
    f.video.ready(42);f.video.advance(51);f.player.setRate(1.25);f.player.pause();
    f.player.suspend();assert.equal(airPlaySourceReady(f.video, 1), false);
    f.route('https://new-route.test');f.player.resume();await settle();f.prepare();
    assert.equal(f.instances.at(-1)!.config.startPosition, 51);assert.equal(f.video.children.at(-1)!.src, 'https://new-route.test/v1/media/receiver-1/master.m3u8');
    f.video.ready(51);assert.equal(f.player.observe().state, 'paused');assert.equal(f.video.playbackRate, 1.25);
    f.player.load(source(2));await settle();f.prepare(2);f.instances[0]!.emit(f.Events.MEDIA_ATTACHING);
    assert.equal(airPlaySourceReady(f.video, 1), false);assert.equal(airPlaySourceReady(f.video, 2), true);
    assert.equal(f.video.children.at(-1)!.src, 'https://new-route.test/v1/media/receiver-2/master.m3u8');
    f.player.unload();assert.equal(airPlaySourceReady(f.video, 2), false);assert.equal(f.video.children.some(child => child.src.startsWith('https:')), false);
  } finally {f.player.dispose();}
});
test('AirPlay reattachment and refusal clear only their own generation', async () => {
  const f = fixture(true);
  try {
    f.player.load(source());await settle();f.prepare();f.instances[0]!.emit(f.Events.MEDIA_DETACHING);
    assert.equal(airPlaySourceReady(f.video, 1), false);f.instances[0]!.engine.attachMedia(f.video);
    assert.equal(airPlaySourceReady(f.video, 1), true);assert.equal(f.video.children.filter(child => child.src.startsWith('https:')).length, 1);
    f.player.load(source(2));await settle();f.prepare(2);f.instances[0]!.emit(f.Events.MEDIA_DETACHING);assert.equal(airPlaySourceReady(f.video, 2), true);
    f.instances.at(-1)!.error({fatal: true, type: 'networkError', response: {code: 403}});assert.equal(airPlaySourceReady(f.video, 2), false);
  } finally {f.player.dispose();}
});
test('AirPlay native source and system wireless events publish real route observations and retire listeners', () => {
  const video = new SafariVideo();
  const player = new WebMediaPlayer(video, {nativeHls: 'first', timers: manualClock(), resolveUrl: path => 'https://server.test' + path, externalRoute: () => video.webkitCurrentPlaybackTargetIsWireless ? 'airplay' : undefined});
  const routes: unknown[] = [];player.onEvent(event => routes.push(event.observation.externalRoute));
  player.load(source());assert.equal(airPlaySourceReady(video, 1), false);player.setAirPlaySource({sessionId:'session',generation:1,url:'/v1/media/receiver/master.m3u8'});assert.equal(airPlaySourceReady(video, 1), true);
  video.webkitCurrentPlaybackTargetIsWireless = true;video.fire('webkitcurrentplaybacktargetiswirelesschanged');assert.equal(routes.at(-1), 'airplay');
  video.webkitCurrentPlaybackTargetIsWireless = false;video.fire('webkitcurrentplaybacktargetiswirelesschanged');assert.equal(routes.at(-1), undefined);
  player.dispose();assert.equal(video.listenerCount(), 0);assert.equal(airPlaySourceReady(video, 1), false);
});
test('AirPlay rejects blob capabilities and caller restrictions; non-WebKit browsers keep their engine path', async () => {
  const blocked = fixture(true);blocked.video.disableRemotePlayback = true;
  blocked.player.load(source());await settle();blocked.prepare();assert.equal(airPlaySourceReady(blocked.video, 1), false);assert.equal(blocked.video.disableRemotePlayback, true);blocked.player.dispose();
  const video = new FakeVideo(), base = fakeHls();
  const player = new WebMediaPlayer(video, {loadHls: async () => base.Hls, resolveUrl: path => 'https://server.test' + path, timers: manualClock()});
  player.load(source());await settle();assert.equal(airPlaySourceReady(video, 1), false);assert.equal(base.instances.length, 1);player.dispose();
  const blob = new SafariVideo();const native = new WebMediaPlayer(blob, {nativeHls: 'first', timers: manualClock()});
  native.load({...source(), url: 'blob:unfetchable'});assert.equal(airPlaySourceReady(blob, 1), false);native.dispose();
});

for (const outcome of ['resolve','reject'] as const) test(`AirPlay native handoff fences a late local hls.js ${outcome} and preserves return-to-local`, async () => {
  let finish!: () => void, fail!: (error: Error) => void, calls = 0;
  const pending = new Promise<void>((resolve,reject) => {finish = resolve;fail = reject;});
  const f = fixture(false, new SafariVideo(), module => ++calls === 1 ? pending.then(() => module) : Promise.resolve(module));
  try {
    f.player.load(source());f.prepare();
    f.video.webkitCurrentPlaybackTargetIsWireless = true;f.video.fire('webkitcurrentplaybacktargetiswirelesschanged');
    assert.equal(f.video.src,'https://server.test/v1/media/receiver-1/master.m3u8');
    f.video.ready(42);f.video.advance(60);f.player.setRate(1.25);f.player.pause();
    if (outcome === 'resolve') finish();else fail(new Error('late import failure'));
    await settle();
    assert.equal(f.instances.length,0,'no local engine may attach over the active receiver');
    assert.equal(f.video.src,'https://server.test/v1/media/receiver-1/master.m3u8');
    assert.equal(f.player.observe().state,'paused');assert.equal(f.player.observe().positionMs,60000);assert.equal(f.player.observe().rate,1.25);
    f.video.webkitCurrentPlaybackTargetIsWireless = false;f.video.fire('webkitcurrentplaybacktargetiswirelesschanged');await settle();
    assert.equal(f.instances.length,1);assert.equal(f.instances[0]!.config.startPosition,60);
    f.video.ready(60);assert.equal(f.player.observe().state,'paused');assert.equal(f.player.observe().positionMs,60000);
  } finally {f.player.dispose();}
});
