import test from 'node:test';
import assert from 'node:assert/strict';
import {sameCastRemote} from '../../../packages/client-core/src/cast/remote-identity.ts';
import {castOrigin} from '../../../packages/client-core/src/connection-policy.ts';
import {screenModule} from './helpers/screen-harness.mjs';

/** Exercise the native wrapper and real occurrence comparison. The shared Cast
 * controller/SDK are held ports; this does not replace their protocol tests. */
async function fixture() {
  const sent: unknown[][] = [], errors: unknown[] = [];
  const attachmentId = '11111111-1111-4111-8111-111111111111';
  const remoteIdentity = {controller: {bindingId: 'binding', controllerGeneration: 1, connectionGeneration: 2},
    occurrence: {sessionId: 'receiver', generation: 3, itemId: 'episode'}};
  const report = {itemId: 'episode', positionSeconds: 12, durationSeconds: 100, paused: false};
  let active = true, shared: Controller;
  class Controller {
    private listeners = new Set<() => void>();
    private state: any = {phase: 'off', available: false, audioTracks: [], textTracks: []};
    constructor() {shared = this;}
    getSnapshot() {return this.state;}
    subscribe(listener: () => void) {this.listeners.add(listener); return () => this.listeners.delete(listener);}
    change(patch: any) {this.state = {...this.state, ...patch}; for (const listener of this.listeners) listener();}
    setAvailable(available: boolean) {this.change({available});}
    async adopt() {this.change({phase: 'casting', itemId: 'episode', remoteIdentity, reported: report,
      audioTracks: [{id: 1, name: 'English'}], textTracks: [{id: 2, name: 'English'}]}); return true;}
    setAudioTrack(id: number, expected: unknown) {sent.push(['audio', id, expected]);}
    setSubtitleTrack(id: number | null, expected: unknown) {sent.push(['subtitles', id, expected]);}
    dispose() {this.listeners.clear();}
  }
  const authority = {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'epoch', viewerFence: 'viewer'};
  const session = {sessionFamilyId: 'family', deviceId: 'phone', viewer: {...authority, authority: 'local', role: 'member'}};
  const adapter = {getSnapshot: () => ({authority}), subscribe: () => () => {}};
  const {NativeCastCaller} = await screenModule(new URL('../src-next/platform/play-on/cast.ts', import.meta.url), {
    react: {default: {}}, 'react-native': {Platform: {isTV: false}, NativeEventEmitter: class {addListener() {return {remove() {}};}},
      NativeModules: {PorticoCast: {applicationId: 'APP', available: async () => true, choose() {},
        current: async () => ({deviceId: 'TV', deviceName: 'Living room', attachmentId})}}},
    '../../../../../packages/client-core/src/connection-policy': {castOrigin},
    '../../../../../packages/client-core/src/cast': {CastController: Controller, sameCastRemote, parseCastEvent: () => undefined},
    '../../../../../packages/player-core/src': {sameFence: (a: unknown, b: unknown) => a === b},
    './cast-transport': {castTransport: {}, validCastAttachment: (id: unknown) => id === attachmentId},
    './cast-controller-storage': {castControllerStorage: {}, castControllerStorageReady: Promise.resolve()},
    './cast-offered': {castOffered: () => true},
  });
  const caller = new NativeCastCaller({api: {baseUrl: 'https://server.test', request: async () => ({applicationId: 'APP'})},
    current: () => active, session: () => session, playback: () => adapter, deviceName: () => 'Phone', reportError: (error: unknown) => errors.push(error)});
  await new Promise(setImmediate);
  assert.ok(caller.getSnapshot().control);
  return {caller, sent, errors, shared: shared!, report, remoteIdentity, retire: () => {active = false;}};
}

test('Cast track choices forward only offered ids and the exact admitted receiver occurrence', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  const control = f.caller.getSnapshot().control;
  f.caller.selectTrack('audio', 1, control);
  f.caller.selectTrack('subtitles', 2, control);
  f.caller.selectTrack('subtitles', null, control);
  assert.deepEqual(f.sent, [['audio', 1, control.remote], ['subtitles', 2, control.remote], ['subtitles', null, control.remote]]);
  for (const id of [0, 2, -1, 1.2, Number.NaN, Number.POSITIVE_INFINITY, null]) f.caller.selectTrack('audio', id, control);
  f.caller.selectTrack('subtitles', 1, control);
  assert.equal(f.sent.length, 3);
  f.shared.change({controlPending: true}); f.caller.selectTrack('audio', 1, control); assert.equal(f.sent.length, 3);
  f.shared.change({controlPending: false, textTracks: []}); f.caller.selectTrack('subtitles', null, control); assert.equal(f.sent.length, 3);
  assert.deepEqual(f.errors, []);
});

test('Cast track handlers reject successor occurrences, missing clocks, wrong attachments and retired viewers', async t => {
  const f = await fixture(); t.after(() => f.caller.dispose());
  const previous = f.caller.getSnapshot().control;
  f.caller.selectTrack('audio', 1, {...previous, attachmentId: 'stale'});
  f.shared.change({remoteIdentity: {...f.remoteIdentity, occurrence: {...f.remoteIdentity.occurrence, generation: 4}}, reported: undefined});
  const successor = f.caller.getSnapshot().control;
  f.caller.selectTrack('audio', 1, previous); f.caller.selectTrack('audio', 1, successor);
  assert.deepEqual(f.sent, [], 'no track dispatch before a current occurrence reports its media');
  f.shared.change({reported: {...f.report, itemId: 'other'}}); f.caller.selectTrack('audio', 1, successor); assert.equal(f.sent.length, 0);
  f.shared.change({reported: f.report}); f.caller.selectTrack('audio', 1, successor); assert.equal(f.sent.length, 1);
  f.retire(); f.caller.selectTrack('audio', 1, successor); assert.equal(f.sent.length, 1);
});
