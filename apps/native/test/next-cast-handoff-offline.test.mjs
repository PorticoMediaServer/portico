import test from 'node:test';
import assert from 'node:assert/strict';
import {sourceModule} from './helpers/offline-source-module.mjs';
import {sameFence} from '../../../packages/player-core/src/identity.ts';
import {castOffered} from '../src-next/platform/play-on/cast-offered.ts';
import {castOrigin} from '../../../packages/client-core/src/connection-policy.ts';

const deferred = () => {let resolve;const promise = new Promise(done => {resolve = done;});return {promise, resolve};};
const settle = () => new Promise(setImmediate);
async function fixture(storage = Promise.resolve()) {
  const listeners = new Map(), starts = [], errors = [];
  const emit = (name, value) => {for (const fn of [...(listeners.get(name) ?? [])]) fn(value);};
  class Emitter {
    addListener(name, fn) {const group = listeners.get(name) ?? new Set();group.add(fn);listeners.set(name, group);return {remove: () => group.delete(fn)};}
  }
  class Controller {
    state = {available: false, phase: 'off', positionSeconds: 0, durationSeconds: 0, paused: false, audioTracks: [], textTracks: []};
    listeners = new Set();
    subscribe = fn => {this.listeners.add(fn);return () => this.listeners.delete(fn);};
    getSnapshot = () => this.state;
    publish = patch => {this.state = {...this.state, ...patch};for (const fn of [...this.listeners]) fn();};
    setAvailable = available => this.publish({available});
    connecting = title => this.publish({phase: 'connecting', title});
    cancelConnecting = () => {if (this.state.phase === 'connecting') this.publish({phase: 'off'});};
    start = async (...args) => {starts.push(args);return true;};
    adopt = async () => false;
    stop = () => this.publish({phase: 'off'});
    dispose() {this.listeners.clear();}
  }
  const attachmentId = '11111111-1111-4111-8111-111111111111';
  let selected = null, chooserCalls = 0, current = true;
  const bridge = {applicationId: 'APP', available: async () => true, current: async () => selected, choose: () => {chooserCalls++;}};
  const scope = {serverId: 'server', accountId: 'account', profileId: 'profile', authority: 'local', controllerId: 'device', controllerEpoch: 'epoch', commandLaneId: 'lane'};
  const fence = {kind: 'queue', playback: {scope, run: 1}, sessionId: 'source', itemId: 'movie', sourceId: 'version', entryId: 'entry', generation: 1};
  let snapshot = {authority: {serverId: 'server', accountId: 'account', profileId: 'profile', epoch: 'viewer', viewerFence: 'viewer'},
    fence, dock: {local: {kind: 'video', fence, queue: null, positionSeconds: 12, playing: true}},
    playback: {phase: 'ready', positionSeconds: 12}, entry: {id: 'movie', title: 'Movie', kind: 'movie'}, subject: {kind: 'video'},
    session: {id: 'source', itemId: 'movie', presentation: {generation: 1}, queue: {queueId: 'queue', entryId: 'entry'}},
    queue: {view: {queue: {id: 'queue', revision: '10', currentEntryId: 'entry', currentPlaybackId: 'source'}}}};
  const playbackListeners = new Set();
  const adapter = {getSnapshot: () => snapshot, subscribe: fn => {playbackListeners.add(fn);return () => playbackListeners.delete(fn);},
    beginRemote: async () => {}, updateRemote: async () => {}, dismissRemote: async () => {}};
  const {NativeCastCaller} = await sourceModule(new URL('../src-next/platform/play-on/cast.ts', import.meta.url), {
    'react-native': {NativeEventEmitter: Emitter, NativeModules: {PorticoCast: bridge}, Platform: {isTV: false}},
    '../../../../../packages/client-core/src/connection-policy': {castOrigin},
    '../../../../../packages/client-core/src/cast': {CastController: Controller, sameCastRemote: (a, b) => a === b, parseCastEvent: value => value},
    '../../../../../packages/player-core/src': {sameFence},
    './cast-transport': {castTransport: {}, validCastAttachment: value => value === attachmentId},
    './cast-controller-storage': {castControllerStorage: {}, castControllerStorageReady: storage}, './cast-offered': {castOffered},
  });
  const session = {sessionFamilyId: 'family', viewer: {...scope, role: 'member'}};
  const caller = new NativeCastCaller({api: {baseUrl: 'https://server.invalid', request: async path => {assert.equal(path, '/v1/cast/configuration');return {applicationId: 'APP'};}},
    session: () => session, playback: () => adapter, current: () => current, deviceName: () => 'Phone', reportError: error => errors.push(error)});
  await settle(); assert.equal(caller.getSnapshot().cast.available, true);
  const change = patch => {snapshot = {...snapshot, ...patch};for (const fn of [...playbackListeners]) fn();};
  const connect = () => {selected = {deviceName: 'TV', deviceId: 'receiver', attachmentId};};
  return {caller, starts, errors, connect, choose: () => {connect();emit('PorticoCastSession', {state: 'started', ...selected});},
    snapshot: () => snapshot, change, chooserCalls: () => chooserCalls, retire: () => {current = false;}};
}

test('actual caller dispatches the source clock after chooser and storage wait', async t => {
  const gate = deferred(), f = await fixture(gate.promise);t.after(() => f.caller.dispose());
  f.caller.start();await settle();assert.equal(f.chooserCalls(), 1);f.choose();await settle();
  assert.equal(f.starts.length, 0);
  f.change({playback: {...f.snapshot().playback, positionSeconds: 81}});gate.resolve();await settle();
  assert.equal(f.starts.length, 1);assert.equal(f.starts[0][3], 81);assert.deepEqual(f.errors, []);
});

test('actual caller admits an existing attachment once without a started event or picker', async t => {
  const f = await fixture();t.after(() => f.caller.dispose());f.connect();f.caller.start();await settle();
  assert.equal(f.starts.length, 1);assert.equal(f.chooserCalls(), 0);
  f.choose();await settle();assert.equal(f.starts.length, 1);assert.deepEqual(f.errors, []);
});

test('queue replacement during storage wait rejects the existing attachment', async t => {
  const gate = deferred(), f = await fixture(gate.promise);t.after(() => f.caller.dispose());f.connect();f.caller.start();await settle();
  f.change({queue: {view: {queue: {...f.snapshot().queue.view.queue, revision: '11'}}}});gate.resolve();await settle();
  assert.equal(f.starts.length, 0);assert.deepEqual(f.errors, []);
});

test('a retired viewer cannot dispatch a late connected attachment', async t => {
  const gate = deferred(), f = await fixture(gate.promise);t.after(() => f.caller.dispose());f.connect();f.caller.start();await settle();
  f.retire();gate.resolve();await settle();assert.equal(f.starts.length, 0);
});
