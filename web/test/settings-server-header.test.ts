import {test} from 'node:test';
import assert from 'node:assert/strict';
import {storageServerHeader} from '../src/screens/settings/server-header.ts';

const storage = {dataFolder: '/data', sharedDrive: false, diskBytes: 1000e9, freeBytes: 273e9, level: 'ok', folders: [{name: 'database', bytes: 1_100_000}], measuredAt: '2026-11-12T02:00:00Z'};

test('the header says the server needs attention while any alert is major, whatever the free space (console rules 4 and 10)', () => {
  assert.equal(storageServerHeader(storage, []).health, 'normal');
  assert.equal(storageServerHeader(storage, [{major: false}]).health, 'normal', 'a warning alone is not a problem that stops playback or risks data');
  assert.equal(storageServerHeader(storage, [{major: false}, {major: true}]).health, 'attention', 'a failed backup or an unreachable source with plenty of space');
  assert.equal(storageServerHeader(storage).health, undefined, 'no word when the alerts could not be read');
  assert.deepEqual(storageServerHeader(storage, []).facts.map(f => f.id), ['free', 'database']);
  assert.equal(storageServerHeader(undefined, [{major: true}]).health, 'attention');
});
