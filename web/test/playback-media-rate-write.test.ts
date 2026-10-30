import test from 'node:test';
import assert from 'node:assert/strict';
import {WebMediaPlayer, type VideoLike} from '../src/platform/playback/web-media-player.ts';
import type {PlayerSource} from '@core/playback-v1/index.ts';

/**
 * A `<video>` stub that counts every assignment made to `playbackRate`, whether this port or the
 * browser's own engine/native control makes it. It is private to this file on purpose: the shared
 * fake lives in another test module, and importing that would register its whole suite here.
 */
class CountingVideo implements VideoLike {
  src = '';
  volume = 1;
  muted = false;
  paused = true;
  ended = false;
  seeking = false;
  readyState = 0;
  duration = NaN;
  error: {readonly code: number; readonly message?: string} | null = null;
  readonly buffered = {length: 0, start: () => 0, end: () => 0};
  readonly seekable = {length: 0, start: () => 0, end: () => 0};
  /** Every assignment to `playbackRate`, including one equal to the value already applied. */
  rateWrites = 0;
  private appliedRate = 1;
  private time = 0;
  private readonly handlers = new Map<string, Set<() => void>>();

  get currentTime() { return this.time; }
  set currentTime(value: number) { this.time = value; }
  get playbackRate() { return this.appliedRate; }
  set playbackRate(value: number) { this.appliedRate = value; this.rateWrites++; }

  play() { this.paused = false; return Promise.resolve(); }
  pause() { this.paused = true; }
  load() { this.readyState = 0; this.time = 0; this.paused = true; }
  removeAttribute(_name: string) {}
  canPlayType(_type: string) { return ''; }
  addEventListener(type: string, listener: () => void) {
    let set = this.handlers.get(type);
    if (!set) this.handlers.set(type, set = new Set());
    set.add(listener);
  }
  removeEventListener(type: string, listener: () => void) { this.handlers.get(type)?.delete(listener); }
  getVideoPlaybackQuality() { return {droppedVideoFrames: 0}; }

  /** What a browser does when the media is loaded and can start at `at`. */
  ready(at = 0) {
    this.time = at;
    this.duration = 3600;
    this.readyState = 4;
    this.seeking = false;
    this.emit('loadedmetadata');
    this.emit('canplay');
  }

  private emit(type: string) { for (const listener of [...(this.handlers.get(type) ?? [])]) listener(); }
}

/** The 250 ms housekeeping tick is not part of these cases; it is not started. */
const quietTimers = {setTicker: () => 0, clearTicker: () => {}};
const source: PlayerSource = {url: '/v1/media/item', generation: 7, mode: 'direct', startPositionMs: 0, subtitles: [], autoplay: true};

function port(video: VideoLike) { return new WebMediaPlayer(video, {timers: quietTimers}); }

test('a tick asking for the rate the element already applies writes nothing to the element', () => {
  const video = new CountingVideo();
  const player = port(video);
  for (let tick = 0; tick < 40; tick++) player.setRate(1);
  assert.equal(video.rateWrites, 0);
  assert.equal(video.playbackRate, 1);
  // The port's own rate is still set on every call, whatever the element is doing.
  assert.equal(player.observe().rate, 1);
  player.dispose();
});

test('a genuine rate change writes once, and later ticks for the same rate write nothing', () => {
  const video = new CountingVideo();
  const player = port(video);
  player.setRate(1.5);
  assert.equal(video.rateWrites, 1);
  assert.equal(video.playbackRate, 1.5);
  player.setRate(1.25);
  assert.equal(video.rateWrites, 2);
  assert.equal(video.playbackRate, 1.25);
  for (let tick = 0; tick < 40; tick++) player.setRate(1.25);
  assert.equal(video.rateWrites, 2);
  assert.equal(player.observe().rate, 1.25);
  player.dispose();
});

test('an element rate reset by the engine is restored while the port already holds the wanted rate', () => {
  const video = new CountingVideo();
  const player = port(video);
  player.setRate(2);
  assert.equal(player.observe().rate, 2);
  // An engine rebuild or a native control resets what the element actually applies. The port's own
  // cached rate still says 2, so comparing against it would decide there is nothing to do.
  video.playbackRate = 1;
  const writesBeforeRestore = video.rateWrites;
  assert.equal(player.observe().rate, 2);
  player.setRate(2);
  assert.equal(video.rateWrites - writesBeforeRestore, 1);
  assert.equal(video.playbackRate, 2);
  // Once restored, the following ticks are silent again.
  for (let tick = 0; tick < 10; tick++) player.setRate(2);
  assert.equal(video.rateWrites - writesBeforeRestore, 1);
  player.dispose();
});

test('a new load still applies the chosen rate to the element', () => {
  const video = new CountingVideo();
  const player = port(video);
  player.setRate(1.75);
  // A fresh presentation comes back at the element's default rate.
  video.playbackRate = 1;
  const writesBeforeLoad = video.rateWrites;
  player.load(source);
  video.ready(0);
  assert.equal(video.rateWrites - writesBeforeLoad, 1);
  assert.equal(video.playbackRate, 1.75);
  assert.equal(player.observe().rate, 1.75);
  player.dispose();
});
