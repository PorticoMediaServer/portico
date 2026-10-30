import test from 'node:test';
import assert from 'node:assert/strict';
import {loadListeningSelection, listeningItemInput} from '../src/listening.ts';

const scope = {serverId: 'server', authority: 'local' as const, accountId: 'account', profileId: 'profile', controllerId: 'controller', controllerEpoch: 'epoch', commandLaneId: 'lane'};
const target = {libraryId: 'library', kind: 'album' as const, id: 'album'};
const wrapped = (data: unknown) => ({scope: {...scope}, data});

test('an exact selection above queue capacity names its total', async () => {
  const items = [listeningItemInput('song-0', target)];
  const data = {target, entries: items, totalCount: 1001, unavailableCount: 0, nextCursor: 'cursor', seed: 'seed'};
  await assert.rejects(loadListeningSelection(async () => wrapped(data), scope, target), /1,001 available items/);
});

test('a selection keeps the exact checks', async () => {
  const items = [listeningItemInput('song-0', target)];
  const exact = {target, entries: items, totalCount: 1, unavailableCount: 0, nextCursor: '', seed: 'seed'};
  const result = await loadListeningSelection(async () => wrapped(exact), scope, target);
  assert.equal(result.entries.length, 1);
  const mismatch = {target, entries: items, totalCount: 2, unavailableCount: 0, nextCursor: '', seed: 'seed'};
  await assert.rejects(loadListeningSelection(async () => wrapped(mismatch), scope, target));
});
