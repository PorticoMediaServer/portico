import test from 'node:test';
import assert from 'node:assert/strict';
import {PlaybackService, type PageVisibility, type PlaybackSnapshot} from '../src/index.ts';
import {toPlaybackSession} from '../src/playback-v1/legacy-bridge.ts';
import {parseSession} from '../src/playback-v1/types.ts';

const api = {createPlayback: async () => ({id: 's1', generation: 1, streamUrl: '/v1/media/g', mode: 'direct' as const, duration: 120, resumeSeconds: 0}), stopPlayback: async () => {}, progressPlayback: async () => {}};
function page(hidden: boolean) {
  const listeners = new Set<() => void>();
  const v: PageVisibility & {hiddenNow: boolean; show(): void} = {
    hiddenNow: hidden,
    hidden: () => v.hiddenNow,
    onVisible(l) { listeners.add(l); return () => { listeners.delete(l); }; },
    show() { v.hiddenNow = false; for (const l of [...listeners]) l(); },
  };
  return v;
}

// O4, 24 Sep: browsers defer loading media in a hidden page, so a start there can't become ready.
test('a start in a hidden page waits for the page instead of failing, then gets a full window', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const visibility = page(true);
  const s = new PlaybackService(api, () => 'k', {startupTimeoutMs: 15, pageVisibility: visibility});
  await s.play('movie', 0);
  t.mock.timers.tick(40);
  assert.notEqual(s.getSnapshot().phase, 'error', 'no failure while hidden');
  visibility.show();
  t.mock.timers.tick(14);
  assert.notEqual(s.getSnapshot().phase, 'error', 'shown: a fresh window starts, not an immediate failure');
  t.mock.timers.tick(1);
  assert.equal(s.getSnapshot().phase, 'error', 'still not ready a full window after being shown');
  s.leave();
});

test('a visible page keeps the startup window as before; becoming ready clears a pending wait', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const visible = new PlaybackService(api, () => 'k', {startupTimeoutMs: 15, pageVisibility: page(false)});
  await visible.play('movie', 0);
  t.mock.timers.tick(14);
  assert.equal(visible.getSnapshot().phase, 'starting');
  t.mock.timers.tick(1);
  assert.equal(visible.getSnapshot().phase, 'error');
  visible.leave();
  const visibility = page(true);
  const s = new PlaybackService(api, () => 'k', {startupTimeoutMs: 15, pageVisibility: visibility});
  await s.play('movie', 0);
  t.mock.timers.tick(25);
  s.ready(s.getSnapshot().intentId);
  visibility.show();
  t.mock.timers.tick(30);
  assert.notEqual(s.getSnapshot().phase, 'error');
  s.leave();
});

// The lead's session from the demo (Dune: Part Two, 24 Sep 02:52 UTC): a v1 direct presentation
// resuming at 33 s reaches the adapter as the direct media URL with a pending seek to 33 s.
test('a v1 direct presentation reaches the adapter with its URL and the resume seek', async () => {
  const session = parseSession({id: 'ps_1', revision: '1', kind: 'vod', role: 'local', state: 'playing', itemId: 'item1', queue: {queueId: 'q1', entryId: 'e1'},
    lease: {reportEveryMs: 10000}, presentation: {generation: 1, mode: 'direct', url: '/v1/media/grantABC_-1', startPositionMs: 33000, subtitles: [], decision: {video: {action: 'direct'}, audio: {action: 'direct'}}}});
  assert.ok(session, 'the demo response parses');
  const s = new PlaybackService(api, () => 'k', {queueRequired: true, startupTimeoutMs: 1000});
  const applied: PlaybackSnapshot[] = [];
  s.attachAdapter({apply: next => { applied.push(next); }});
  const intent = s.beginQueueIntent('item1');
  assert.equal(await s.adoptQueueSession('item1', toPlaybackSession(session!, 9960), intent), true);
  const last = applied.at(-1)!;
  assert.equal(last.session?.streamUrl, '/v1/media/grantABC_-1');
  assert.equal(last.session?.mode, 'direct');
  assert.equal(last.pendingSeek?.positionSeconds, 33);
  s.ready(last.intentId);
  assert.ok(s.seekApplied(last.intentId, last.pendingSeek!.revision, 33, 'playing'));
  assert.equal(s.getSnapshot().positionSeconds, 33);
  s.leave();
});

function changingPage() {
  let hidden = false;
  const listeners = new Set<() => void>();
  const visibility: PageVisibility & {hide(): void; show(): void; observers(): number} = {
    hidden: () => hidden,
    onVisible: listener => {const change=()=>{if(!hidden)listener();};listeners.add(change);return()=>{listeners.delete(change);};},
    onChange: listener => {listeners.add(listener);return()=>{listeners.delete(listener);};},
    hide: () => {hidden=true;for(const listener of [...listeners])listener();},
    show: () => {hidden=false;for(const listener of [...listeners])listener();},
    observers: () => listeners.size,
  };
  return visibility;
}

test('backgrounding pauses the remaining startup deadline before suspended timers can fire', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const visibility=changingPage();
  const s=new PlaybackService(api,()=> 'k',{startupTimeoutMs:20000,seekTimeoutMs:60000,pageVisibility:visibility});
  try {
    await s.play('movie',0);t.mock.timers.tick(5000);visibility.hide();
    t.mock.timers.tick(120000);assert.equal(s.getSnapshot().phase,'starting');
    visibility.show();t.mock.timers.tick(14999);assert.equal(s.getSnapshot().phase,'starting');
    t.mock.timers.tick(1);assert.equal(s.getSnapshot().phase,'error');
  } finally {s.leave();}
  assert.equal(visibility.observers(),0);
});

test('a pending seek keeps its target and remaining deadline across a long background interval', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const visibility=changingPage();
  const s=new PlaybackService(api,()=> 'k',{pageVisibility:visibility,seekTimeoutMs:15000});
  try {
    await s.play('movie',0);let snapshot=s.getSnapshot();s.ready(snapshot.intentId);
    assert.equal(s.seekApplied(snapshot.intentId,snapshot.pendingSeek!.revision,0,'playing'),true);
    s.seek(90);t.mock.timers.tick(4000);visibility.hide();t.mock.timers.tick(120000);
    assert.equal(s.getSnapshot().pendingSeek?.positionSeconds,90);assert.equal(s.getSnapshot().seekError,undefined);
    visibility.show();t.mock.timers.tick(10999);assert.equal(s.getSnapshot().pendingSeek?.positionSeconds,90);
    t.mock.timers.tick(1);snapshot=s.getSnapshot();assert.equal(snapshot.pendingSeek,undefined);assert.equal(snapshot.failedSeek?.positionSeconds,90);
    assert.equal(snapshot.intent,'paused');assert.equal(snapshot.phase,'ready');
  } finally {s.leave();}
  assert.equal(visibility.observers(),0);
});

test('retiring hidden playback removes both deadline listeners and foreground cannot revive it', async t => {
  t.mock.timers.enable({apis: ['setTimeout', 'Date']});
  const visibility=changingPage();
  const s=new PlaybackService(api,()=> 'k',{pageVisibility:visibility});
  await s.play('movie',0);visibility.hide();assert.equal(visibility.observers(),2);
  s.leave();assert.equal(visibility.observers(),0);visibility.show();t.mock.timers.tick(60000);
  assert.equal(s.getSnapshot().phase,'idle');assert.equal(s.getSnapshot().pendingSeek,undefined);
});
