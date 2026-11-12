import test from 'node:test';
import assert from 'node:assert/strict';
import {screenModule} from './helpers/screen-harness.mjs';

/** The real NativePlayOn logic runs against held transport doubles, with no
 * network or wall-clock polling wait. Server admission and native IO are not run. */
async function fixture() {
  const commands: any[] = [], errors: unknown[] = [];
  let permitted = true;
  const receiver = {id: 'tv', displayName: 'Living room', platform: 'tvos', allowed: false};
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch'};
  const fence = {kind: 'queue', itemId: 'episode-1', sessionId: 'original', entryId: 'entry-1'};
  const entry = {id: 'episode-1', title: 'Episode one', kind: 'episode', stillUrl: '/v1/art/one', show: {id: 'show', name: 'Show'}, seasonNumber: 1, episodeNumber: 1};
  const local = {fence, playing: true, kind: 'video'};
  let snapshot: any = {authority, fence, entry, playback: {phase: 'ready', positionSeconds: 12, duration: 100}, dock: {local}};
  // What the account's playback list says the TV plays (client-core RemoteStream).
  let report: any = {sessionId: 'receiver-1', itemId: 'episode-1', title: 'Episode one', deviceName: 'Living room',
    state: 'playing', positionMs: 45_000, durationMs: 100_000, next: {itemId: 'episode-2', title: 'Episode two'}};
  const playbackListeners = new Set<() => void>();
  const playback = {getSnapshot: () => snapshot, subscribe: (listener: () => void) => {playbackListeners.add(listener); return () => playbackListeners.delete(listener);},
    beginRemote: async (value: any) => {snapshot = {...snapshot, dock: {...snapshot.dock, remote: value}};},
    updateRemote: async (value: any) => {snapshot = {...snapshot, dock: {...snapshot.dock, remote: value}};}};
  class Handoff {
    private listeners = new Set<() => void>();
    private state: any = {directoryPhase: 'ready', phase: 'idle', receivers: [receiver]};
    subscribe(listener: () => void) {this.listeners.add(listener); return () => this.listeners.delete(listener);}
    getSnapshot() {return this.state;}
    async refresh() {for (const listener of this.listeners) listener();}
    async send() {this.state = {...this.state, phase: 'done', receiverPlaybackId: 'receiver-1'}; for (const listener of this.listeners) listener(); return true;}
    async refreshRemote() {this.state = {...this.state, remote: report};}
    async pause() {commands.push(['pause']); return true;}
    async play() {commands.push(['play']); return true;}
    async seek(position: number) {commands.push(['seek', position]); return true;}
    async stop() {commands.push(['stop']); return true;}
    cancel() {commands.push(['cancel']);}
    dispose() {}
  }
  class Cast {
    subscribe() {return () => {};}
    getSnapshot() {return {cast: {phase: 'off', available: false, audioTracks: [], textTracks: []}};}
    controls() {}
    dispose() {}
  }
  const {NativePlayOn} = await screenModule(new URL('../src-next/platform/play-on/controller.ts', import.meta.url), {
    react: {default: {}},
    '../../../../../packages/client-core/src/remote-playback': {HandoffController: Handoff},
    '../../../../../packages/player-core/src': {sameFence: (a: unknown, b: unknown) => a === b},
    './cast': {NativeCastCaller: Cast},
  });
  const session = {deviceId: 'phone'};
  const controller = new NativePlayOn({api: {request: async () => ({})}, session: () => session, deviceName: () => 'Phone', playback: () => playback,
    current: () => permitted, reportError: (error: unknown) => errors.push(error)});
  const settle = () => new Promise<void>(resolve => setImmediate(resolve));
  return {controller, receiver, entry, commands, errors, settle, report: () => report,
    setReport: (value: any) => {report = value;}, revoke: () => {permitted = false;},
    replaceLocal: () => {snapshot = {...snapshot, entry: {id: 'other', title: 'Other title', stillUrl: '/v1/art/other'}};}};
}

test('a move keeps the exact chooser anchor and shows only the clock the server reads for the TV', async t => {
  const f = await fixture(); t.after(() => f.controller.dispose());
  const anchor = {current: {measureInWindow() {}}};
  assert.equal(f.controller.open(anchor), true); assert.equal(f.controller.getSnapshot().anchorRef, anchor);
  let beforeReport: any;
  f.controller.subscribe(() => {const remote = f.controller.getSnapshot().remote; if (remote && !remote.positionKnown) beforeReport ??= remote;});
  await f.controller.send(f.receiver); await f.settle();
  assert.equal(f.controller.getSnapshot().anchorRef, anchor);
  assert.equal(beforeReport.positionKnown, false); assert.deepEqual(beforeReport.allows, {pause: false, resume: false, seek: false, stop: false});
  const remote = f.controller.getSnapshot().remote;
  assert.equal(remote.positionSeconds, 45); assert.equal(remote.durationSeconds, 100); assert.equal(remote.positionKnown, true);
  assert.equal(remote.entry, f.entry);
  assert.deepEqual(remote.next, {itemId: 'episode-2', title: 'Episode two', available: true}, 'the TV\'s next entry, as the server reads it');
  assert.deepEqual(remote.allows, {pause: true, resume: true, seek: true, stop: true}, 'every control while the TV plays');
  f.replaceLocal(); assert.equal(f.controller.getSnapshot().remote.entry, f.entry, 'another local title never supplies remote display facts');
  f.controller.openRemote(); assert.equal(f.controller.getSnapshot().remoteOpen, true); assert.equal(f.controller.getSnapshot().open, false);
  assert.deepEqual(f.errors, []);
});

test('remote seeks use the current receiver position, clamp the title, and stop once the TV has stopped', async t => {
  const f = await fixture(); t.after(() => f.controller.dispose());
  await f.controller.send(f.receiver); await f.settle();
  const id = f.controller.getSnapshot().remote.id;
  await f.controller.skip(-90, id); await f.controller.skip(500, id); await f.controller.command('seek', 500, id);
  assert.deepEqual(f.commands, [['seek', 0], ['seek', 100], ['seek', 100]]);
  f.setReport({...f.report(), state: 'ended'}); await f.controller.skip(1, id);
  assert.equal(f.commands.length, 4);
  await f.controller.pause(id); await f.controller.resume(id); await f.controller.skip(10, id); await f.controller.startOver(id);
  await f.controller.command('seek', 30, id); await f.controller.stop(id);
  assert.equal(f.commands.length, 4, 'retained controls cannot write once the TV stopped playing it');
  assert.deepEqual(f.errors, []);
});

test('changed receiver items clear captured episode art and unknown duration; next follows the same read', async t => {
  const f = await fixture(); t.after(() => f.controller.dispose());
  await f.controller.send(f.receiver); await f.settle(); const id = f.controller.getSnapshot().remote.id;
  f.setReport({...f.report(), itemId: 'episode-2', title: 'Episode two', durationMs: undefined, next: {itemId: 'episode-3', title: 'Episode three'}});
  await f.controller.skip(0, id);
  const remote = f.controller.getSnapshot().remote;
  assert.equal(remote.itemId, 'episode-2'); assert.equal(remote.title, 'Episode two'); assert.equal(remote.entry, undefined);
  assert.equal(remote.durationSeconds, 0); assert.equal(remote.next.itemId, 'episode-3');
  f.setReport({...f.report(), next: undefined}); await f.controller.skip(0, id);
  assert.equal(f.controller.getSnapshot().remote.next, undefined, 'at the last entry there is no next-up line');
  f.setReport(undefined); await f.controller.skip(0, id);
  assert.equal(f.controller.getSnapshot().remote.positionKnown, false); assert.equal(f.controller.getSnapshot().remote.next, undefined);
  assert.equal(f.controller.getSnapshot().remote.allows.seek, false);
  assert.deepEqual(f.errors, []);
});

test('retired authority and other remote ids cannot issue a command', async t => {
  const f = await fixture(); t.after(() => f.controller.dispose());
  await f.controller.send(f.receiver); await f.settle(); const id = f.controller.getSnapshot().remote.id;
  await f.controller.pause('not-this-remote'); await f.controller.skip(10, 'not-this-remote');
  f.revoke(); await f.controller.pause(id); await f.controller.skip(10, id); await f.controller.command('seek', 10, id);
  assert.deepEqual(f.commands, []); assert.deepEqual(f.errors, []);
});
