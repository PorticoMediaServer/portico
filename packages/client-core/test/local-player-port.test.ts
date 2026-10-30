import test from 'node:test';
import assert from 'node:assert/strict';
import {localPlayerSource, localPlayerLoadReceipt, type LocalPlayerLoadReceipt} from '../src/playback-v1/local-port.ts';
import {localPlaybackFence} from '../../player-core/src/index.ts';

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', sessionFamilyId: 'family'};
const source = localPlayerSource({kind: 'local', scope, preparationId: 'preparation', localSourceId: 'opaque', entryId: 'entry', itemId: 'item', generation: 1, startPositionMs: 3000, autoplay: true});
const receipt = (): LocalPlayerLoadReceipt => ({kind: 'local', scope, preparationId: 'preparation', localSourceId: 'opaque', entryId: 'entry', itemId: 'item', generation: 1,
  sessionId: 'local:entry:17', playId: 'actual_journal', artifact: {sha256: 'a'.repeat(64), bytes: 4096}});

test('opaque load source and actual receipt preserve full scope and bind directly to the canonical local fence', () => {
  assert.ok(Object.isFrozen(source) && Object.isFrozen(source.scope));
  const actual = localPlayerLoadReceipt(receipt(), source);
  const fence = localPlaybackFence({kind: 'local', playback: {kind: 'local', scope: actual.scope, run: 1}, generation: actual.generation,
    sessionId: actual.sessionId, playId: actual.playId, entryId: actual.entryId, itemId: actual.itemId, artifact: actual.artifact});
  assert.equal(fence.sessionId, 'local:entry:17');assert.equal(fence.playId, 'actual_journal');assert.deepEqual(fence.artifact, actual.artifact);
  assert.equal('url' in source, false);assert.equal('url' in actual, false);
  const second = localPlayerLoadReceipt({...receipt(), sessionId: 'local:entry:18', playId: 'different_journal'}, source);
  assert.equal(second.generation, actual.generation, 'a repeated public generation is not a native load/session identity');
  assert.notEqual(second.sessionId, actual.sessionId);assert.notEqual(second.playId, actual.playId);
});

test('receipt validation refuses every changed scope/member/load and never supplies missing facts', () => {
  for (const field of ['serverId', 'authority', 'accountId', 'profileId', 'sessionFamilyId'] as const) {
    assert.throws(() => localPlayerLoadReceipt({...receipt(), scope: {...scope, [field]: field === 'authority' ? 'hosted' : 'foreign'}}, source));
  }
  for (const patch of [{preparationId: 'other'}, {localSourceId: 'other'}, {entryId: 'other'}, {itemId: 'other'}, {generation: 2},
    {sessionId: 'v1_session'}, {sessionId: 'local:entry:0'}, {sessionId: 'local:entry:9007199254740992'}, {playId: ''}, {artifact: {sha256: 'bad', bytes: 4096}},
    {artifact: {sha256: 'a'.repeat(64), bytes: 0}}, {url: '/private/file'}, {path: '/private/file'}, {privateGeneration: 17}]) {
    assert.throws(() => localPlayerLoadReceipt({...receipt(), ...patch} as LocalPlayerLoadReceipt, source));
  }
  for (const value of [undefined, null, {}, {kind: 'local'}]) assert.throws(() => localPlayerLoadReceipt(value as LocalPlayerLoadReceipt, source));
});

test('a local source cannot carry a URL, credentials, private generation or an inferred scope', () => {
  for (const patch of [{url: '/media'}, {path: '/private/file'}, {authorization: 'credential'}, {privateGeneration: 1}, {generation: 0}, {startPositionMs: -1}, {startPositionMs: .5}]) {
    assert.throws(() => localPlayerSource({...source, ...patch}));
  }
  const {sessionFamilyId: ignored, ...incomplete} = scope;void ignored;
  assert.throws(() => localPlayerSource({...source, scope: incomplete as typeof scope}));
});
