import test from 'node:test';
import assert from 'node:assert/strict';
import {offlineFixture, viewer, owner, digest, title, settle} from './helpers/offline-fixture.mjs';
import {offlineModule} from './helpers/offline-module.mjs';
import {claimNativeDownloadsDevice} from '../src-next/shell/downloads-device-custody';
import {NativeOfflinePlayback} from '../src-next/platform/playback/offline-playback';
import type {OfflineEntry} from '../platform/offline-downloads';

const day = 86400000, contact = Date.parse('2026-10-01T12:00:00Z');
async function opening(floor = contact) {
  const f = await offlineFixture({state: JSON.stringify({version: 3, permissions: {[owner]: contact + 30 * day}, clockFloor: floor})});
  const otherOwner = JSON.stringify([viewer.authority, viewer.accountId, 'other_profile', viewer.serverId]);
  f.seed('copy', 'film', {sha256: digest, meta: {downloadId: 'dl_mine', owner, title, bundle: 4}});
  f.seed('other', 'other_film', {sha256: digest, meta: {downloadId: 'dl_theirs', owner: otherOwner, title, bundle: 4}});
  const identity = {owner: {...viewer, sessionFamilyId: 'family'}, current: () => true, subscribe: () => () => {}};
  const {createRememberedDownloadsDevice} = await offlineModule(new URL('../src-next/shell/remembered-downloads-device.ts', import.meta.url), {
    '../platform/offline-downloads': {offlineDownloads: f.offline}, './downloads-device-custody': {claimNativeDownloadsDevice},
  });
  const remembered = createRememberedDownloadsDevice(identity, () => true);
  await remembered.open();
  const player = new NativeOfflinePlayback(identity as never, remembered.device);
  return {f, remembered, player, entry: remembered.device.getSnapshot().entries[0]!,
    dispose() {player.dispose(); remembered.dispose(); f.offline.dispose();}};
}

test('cold offline opening admits a rebuilt local artifact only for the remembered signed-in profile', async t => {
  t.mock.method(Date, 'now', () => contact + day);
  const x = await opening(); t.after(() => x.dispose());
  assert.equal(x.entry.id, 'copy'); assert.equal(x.entry.receipt, undefined);
  assert.deepEqual(x.remembered.device.getSnapshot().entries.map((entry: OfflineEntry) => entry.id), ['copy']);
  const before = x.f.calls.length, source = await x.player.prepare(x.entry, () => true);
  assert.ok(source.session().streamUrl.startsWith('file:'));
  assert.equal(x.f.calls.length, before, 'launch and playback need no server');
  const other = {...x.entry, id: 'other', itemId: 'other_film', downloadId: 'dl_theirs', owner: JSON.stringify([viewer.authority, viewer.accountId, 'other_profile', viewer.serverId])};
  assert.equal(x.remembered.device.playabilityReason(other), 'other_profile');
  await assert.rejects(x.remembered.device.localMedia(other));
  await assert.rejects(x.player.prepare(other, () => true));
  await assert.rejects(x.player.prepare({...x.entry, artifact: {...x.entry.artifact!, sha256: 'b'.repeat(64)}}, () => true));
});

test('a rebuilt download past 30 days cannot open through the cold-launch binding', async t => {
  t.mock.method(Date, 'now', () => contact + 31 * day);
  const x = await opening(); t.after(() => x.dispose());
  assert.equal(x.remembered.device.playabilityReason(x.entry), 'needs_check_in');
  await assert.rejects(x.remembered.device.localMedia(x.entry));
  await assert.rejects(x.player.prepare(x.entry, () => true));
  assert.equal(x.f.entries.has('copy'), true, 'expiry keeps the file');
});

test('turning the clock back after expiry cannot reopen a rebuilt download on cold launch', async t => {
  let now = contact + 31 * day; t.mock.method(Date, 'now', () => now);
  const first = await opening();
  assert.equal(first.remembered.device.playabilityReason(first.entry), 'needs_check_in');
  await settle();
  const floor = JSON.parse(first.f.disk.state).clockFloor;
  assert.ok(floor >= now); first.dispose();
  now = contact + day;
  const relaunched = await opening(floor); t.after(() => relaunched.dispose());
  assert.equal(relaunched.remembered.device.playabilityReason(relaunched.entry), 'needs_check_in');
  await assert.rejects(relaunched.remembered.device.localMedia(relaunched.entry));
  await assert.rejects(relaunched.player.prepare(relaunched.entry, () => true));
});
