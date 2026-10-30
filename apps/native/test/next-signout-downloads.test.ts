import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineOwnerKey, type OfflineEntry} from '../platform/offline-downloads';
import {capturedViewerIsCurrent, removeCapturedDownloads, removeCapturedServerDownloads, type SignOutCapturedSession} from '../src-next/screens/accounts/sign-out-downloads';

const viewer = (profileId: string) => ({authority: 'local', serverId: 'server', accountId: 'account', profileId});
const session = (profileId: string, family = `family-${profileId}`, pin: {publicKey: string; fingerprint: string} = {publicKey: 'key', fingerprint: 'pin'}, authority = 'local') => ({
  signIn: 'local',
  connection: {pin: {serverId: 'server', ...pin}, session: {sessionFamilyId: family, viewer: {...viewer(profileId), authority}}},
}) as unknown as SignOutCapturedSession;
const entry = (id: string, profileId: string) => ({id, owner: offlineOwnerKey(viewer(profileId))}) as OfflineEntry;

test('a confirmation opened for A deletes none of B after the selection changes', async () => {
  const removed: string[] = [];
  const count = await removeCapturedServerDownloads({session: session('a'), name: 'A'}, {
    selected: async () => session('b'),
    entries: () => [entry('b-one', 'b')],
    remove: async value => {removed.push(value.id);},
  });
  assert.equal(count, 0);
  assert.deepEqual(removed, [], 'another selected profile is never touched');
});

test('a selection change during a multi-entry removal stops before the next entry', async () => {
  const removed: string[] = [];
  let selected = session('a');
  const count = await removeCapturedServerDownloads({session: session('a'), name: 'A'}, {
    selected: async () => selected,
    entries: () => [entry('a-one', 'a'), entry('a-two', 'a')],
    remove: async value => {removed.push(value.id); selected = session('b');},
  });
  assert.equal(count, 1);
  assert.deepEqual(removed, ['a-one'], 'the retired viewer stops the loop');
});

test('only the captured viewer entries are removed while it stays selected', async () => {
  const removed: string[] = [];
  const count = await removeCapturedDownloads(session('a'), {
    selected: async () => session('a'),
    entries: () => [entry('a-one', 'a'), entry('b-one', 'b'), entry('a-two', 'a')],
    remove: async value => {removed.push(value.id);},
  });
  assert.equal(count, 2);
  assert.deepEqual(removed, ['a-one', 'a-two']);
});

test('the exact family and viewer tuple defines currentness', () => {
  assert.equal(capturedViewerIsCurrent(session('a'), session('a')), true);
  assert.equal(capturedViewerIsCurrent(session('b'), session('a')), false);
  assert.equal(capturedViewerIsCurrent(session('a', 'replacement-family'), session('a')), false);
  assert.equal(capturedViewerIsCurrent(session('a', 'family-a', {publicKey: 'other', fingerprint: 'pin'}), session('a')), false, 'a different signing key is not the same server');
  assert.equal(capturedViewerIsCurrent(session('a', 'family-a', {publicKey: 'key', fingerprint: 'other'}), session('a')), false, 'a different pinned fingerprint is not the same server');
  assert.equal(capturedViewerIsCurrent(session('a', 'family-a', undefined, 'hosted'), session('a')), false, 'a different viewer authority is not the same viewer');
  assert.equal(capturedViewerIsCurrent(undefined, session('a')), false);
});
