import test from 'node:test';
import assert from 'node:assert/strict';
import {bindVideoSystemMedia, type NativeVideoSystemMediaPort} from '../src-next/screens/watch/system-media.ts';
import {playbackFence} from '../../../packages/player-core/src/identity.ts';

const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
const fence = playbackFence({kind: 'queue', playback: {scope: {serverId: 'server', accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane', authority: 'local'}, run: 1}, sessionId: 'session', itemId: 'item', sourceId: 'version', entryId: 'entry', generation: 1});

function fixture() {
  let live = true, state: any = {authority, fence, lease: 'lease', retired: false, dock: {local: {kind: 'video', fence, expanded: true}},
    entry: {id: 'item', title: 'Episode', kind: 'episode', show: {id: 'show', name: 'Show'}}, effectiveRate: 1,
    playback: {phase: 'ready', intent: 'paused', positionSeconds: 15, duration: 300}, capabilities: {play: true, pause: true, seek: true, next: true, previous: true}};
  const updates = new Set<() => void>(), handlers = new Set<(event: unknown) => void>(), captured: ((event: unknown) => void)[] = [];
  const metadata: any[] = [], commands: any[] = [], errors: unknown[] = []; let clears = 0;
  const preferences = {effective: {'playback.skipBackMs': 10000, 'playback.skipForwardMs': 30000}};
  const subscribe = (f: () => void) => {updates.add(f); return () => {updates.delete(f);};};
  const action = (name: string) => async (...args: unknown[]) => {commands.push([name, ...args]);};
  const playback: any = {getSnapshot: () => state, subscribe, toggle: action('toggle'), seek: action('seek'), skip: action('skip'), next: action('next'), previous: action('previous')};
  const context: any = {playback, current: () => live, authority: () => authority, subscribeAuthority: subscribe, preferences: () => preferences, subscribePreferences: subscribe};
  const port: NativeVideoSystemMediaPort = {setVideo: value => {metadata.push(value);}, clear: () => {clears++;}, subscribe: handler => {
    handlers.add(handler); captured.push(handler); return () => {handlers.delete(handler);};
  }};
  const stop = bindVideoSystemMedia(context, port, error => {errors.push(error);});
  const update = () => {for (const f of updates) f();};
  return {playback, stop, metadata, commands, errors, handlers, captured, update, preferences, clears: () => clears,
    emit: (event: unknown) => {for (const f of handlers) f(event && typeof event === 'object' ? {sourceToken: metadata.at(-1).sourceToken, ...event} : event);},
    state: () => state, replace: (next: any) => {state = next; update();}, retire: () => {live = false; update();}, subscriptions: () => updates.size};
}

test('video metadata and dedicated keys use the single existing playback adapter', async () => {
  const f = fixture();
  try {
    assert.equal(f.handlers.size, 1);
    const {sourceToken, ...published} = f.metadata[0];
    assert.match(sourceToken, /^video-\d+$/, 'the token is opaque and carries no identity');
    assert.deepEqual(published, {itemId: 'item', title: 'Episode', subtitle: 'Show', seriesTitle: 'Show',
      durationSeconds: 300, live: false, canNext: true, canPrevious: true, skipBackSeconds: 10, skipForwardSeconds: 30});
    f.update(); assert.equal(f.metadata.length, 1, 'unchanged metadata is not republished');
    const resting = f.state();
    f.replace({...resting, effectiveRate: 1.5, playback: {...resting.playback, positionSeconds: 16}});
    assert.equal(f.metadata.length, 1, 'progress and rate are read by the native player, not republished');
    f.replace(resting);
    f.emit({itemId: 'item', command: 'play'});
    f.emit({itemId: 'item', command: 'pause'});
    f.emit({itemId: 'item', command: 'seek', positionSeconds: 22});
    f.emit({itemId: 'item', command: 'skip', deltaSeconds: -10});
    f.emit({itemId: 'item', command: 'next'});
    f.emit({itemId: 'item', command: 'previous'});
    assert.deepEqual(f.commands, [['toggle', fence], ['seek', fence, 22], ['skip', fence, -10], ['next', fence], ['previous', fence]]);
    f.preferences.effective['playback.skipForwardMs'] = 45000; f.update();
    assert.equal(f.metadata.at(-1).skipForwardSeconds, 45);
    assert.equal(f.metadata.at(-1).skipBackSeconds, 10);
  } finally {f.stop();}
  assert.equal(f.handlers.size, 0); assert.equal(f.subscriptions(), 0); assert.equal(f.clears(), 1);
});

test('transport refuses malformed, unavailable, ended and retired-source commands', () => {
  const f = fixture();
  try {
    const old = f.captured[0]!, oldToken = f.metadata.at(-1).sourceToken;
    for (const command of [null, {itemId: 'other', command: 'play'}, {itemId: 'item', command: 'seek', positionSeconds: NaN}, {itemId: 'item', command: 'skip', deltaSeconds: Infinity}]) f.emit(command);
    const replacement = playbackFence({...fence, generation: 2});
    f.replace({...f.state(), fence: replacement, dock: {local: {kind: 'video', fence: replacement}}});
    old({itemId: 'item', sourceToken: oldToken, command: 'play'});
    f.emit({itemId: 'item', sourceToken: oldToken, command: 'play'});
    f.emit({itemId: 'item', sourceToken: undefined, command: 'play'});
    assert.equal(f.handlers.size, 1); assert.deepEqual(f.commands, []);
    f.replace({...f.state(), capabilities: {play: false, pause: false, seek: false, previous: false, next: false}});
    for (const command of ['play', 'toggle', 'next', 'previous']) f.emit({itemId: 'item', command});
    assert.deepEqual(f.commands, []);
    f.replace({...f.state(), capabilities: {play: true, seek: true}, playback: {...f.state().playback, phase: 'ended'}});
    f.emit({itemId: 'item', command: 'play'}); f.emit({itemId: 'item', command: 'seek', positionSeconds: 0});
    assert.deepEqual(f.commands, []);
    f.retire(); old({itemId: 'item', command: 'toggle'});
    assert.equal(f.handlers.size, 0); assert.equal(f.clears(), 1); assert.deepEqual(f.commands, []);
  } finally {f.stop();}
});

test('music and native listening never acquire a second media session', () => {
  const f = fixture();
  try {
    f.replace({...f.state(), dock: {local: {kind: 'music', fence}}});
    assert.equal(f.handlers.size, 0); assert.equal(f.clears(), 1);
    f.replace({...f.state(), dock: {local: {kind: 'video', fence}}, playback: {...f.state().playback, nativeListening: {}}});
    assert.equal(f.handlers.size, 0); assert.equal(f.metadata.length, 1);
  } finally {f.stop();}
});

test('current adapter command errors are reported and late retired errors are ignored', async () => {
  const f = fixture(), error = new Error('command refused');
  try {
    f.playback.toggle = async () => {throw error;};
    f.emit({itemId: 'item', command: 'play'}); await Promise.resolve();
    assert.deepEqual(f.errors, [error]);
    f.emit({itemId: 'item', command: 'play'}); f.retire(); await Promise.resolve();
    assert.deepEqual(f.errors, [error]);
  } finally {f.stop();}
});
