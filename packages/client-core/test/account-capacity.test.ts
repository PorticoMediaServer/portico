import assert from 'node:assert/strict';
import {test} from 'node:test';
import {capacityChangedEventType} from '../src/account-capacity.ts';
import {parseEvent} from '../src/playback-v1/types.ts';
import {parseRemotePlaybackControls} from '../src/playback-controls.ts';

const hint = {id: '123', type: capacityChangedEventType, at: '2026-10-25T20:00:00Z'};
test('capacity hints admit sequencing only, before generic event projection', () => {
 assert.deepEqual(parseEvent(hint), hint);
 for (const extra of [{resource: {kind: 'session', id: 'private'}}, {revision: '3'}, {data: {}}, {accountId: 'private'}, {playing: 0}]) {
  assert.equal(parseEvent({...hint, ...extra}), undefined);
 }
 assert.equal(parseEvent({...hint, at: 'private'}), undefined);
 assert.equal(parseEvent({id: '123', type: capacityChangedEventType}), undefined);
});
test('controls retain actual account counts independently of visible rows', () => {
 const document = parseRemotePlaybackControls({items: [], nextCursor: '', streamLimit: {scope: 'account', limit: 2, playing: 3}});
 assert.deepEqual(document.streamLimit, {scope: 'account', limit: 2, playing: 3});
 assert.equal(parseRemotePlaybackControls({items: [], nextCursor: ''}).streamLimit, undefined);
 for (const streamLimit of [null, {scope: 'account', limit: 0, playing: 0}, {scope: 'account', limit: 2, playing: -1}, {scope: 'server', limit: 2, playing: 0}]) {
  assert.throws(() => parseRemotePlaybackControls({items: [], nextCursor: '', streamLimit}));
 }
});
