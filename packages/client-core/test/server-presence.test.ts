import test from 'node:test';import assert from 'node:assert/strict';
import {SERVER_RECENTLY_SEEN_MS,needsOfflineServerDialog,parseServerPresence,presenceLabel,sortServersByPresence,wasRecentlySeen,type HostedServer,type ServerPresence} from '../src/index.ts';

// X-13 (client): choosers order servers online-first by last seen, label stale
// ones, and reserve the keep-trying copy for servers seen within the hour.
const now = 1_800_000_000_000;
const iso = (ms: number) => new Date(ms).toISOString();
const server = (id: string, presence?: ServerPresence): HostedServer =>
  ({id, name: id, baseUrl: 'https://' + id + '.example', publicKey: 'k', policyRevision: 1, ...(presence ? {presence} : {})});

test('online servers first, then the rest by last seen descending, unknown last', () => {
  const online = server('online', {online: true, lastSeenAt: iso(now - 10 * 60_000)});
  const stale = server('stale', {online: false, lastSeenAt: iso(now - 5 * 3_600_000)});
  const older = server('older', {online: false, lastSeenAt: iso(now - 8 * 86_400_000)});
  const never = server('never', {online: false, lastSeenAt: null});
  const unknown = server('unknown');
  assert.deepEqual(sortServersByPresence([stale, unknown, never, older, online]).map(s => s.id), ['online', 'stale', 'older', 'never', 'unknown']);
  assert.deepEqual(sortServersByPresence([]), []);
});

test('the label tells last seen from never seen; unknown and online say nothing', () => {
  assert.deepEqual(presenceLabel(undefined, now), {kind: 'unknown'});
  assert.deepEqual(presenceLabel({online: true, lastSeenAt: iso(now - 60_000)}, now), {kind: 'online'});
  assert.deepEqual(presenceLabel({online: false, lastSeenAt: null}, now), {kind: 'neverSeen'});
  const seen = iso(now - 5 * 86_400_000);
  assert.deepEqual(presenceLabel({online: false, lastSeenAt: seen}, now), {kind: 'lastSeen', lastSeenAt: seen, recent: false});
});

test('the one-hour keep-trying rule: recent stays waiting, stale gets the dialog', () => {
  assert.equal(SERVER_RECENTLY_SEEN_MS, 3_600_000);
  const recent: ServerPresence = {online: false, lastSeenAt: iso(now - 30 * 60_000)};
  const stale: ServerPresence = {online: false, lastSeenAt: iso(now - 2 * 3_600_000)};
  assert.equal(wasRecentlySeen(recent, now), true);
  assert.equal(wasRecentlySeen(stale, now), false);
  assert.equal(wasRecentlySeen({online: false, lastSeenAt: null}, now), false);
  assert.equal(wasRecentlySeen(undefined, now), false);
  assert.deepEqual(presenceLabel(recent, now), {kind: 'lastSeen', lastSeenAt: recent.lastSeenAt, recent: true});
  assert.equal(needsOfflineServerDialog(recent, true, now), false, 'seen half an hour ago: the waiting copy');
  assert.equal(needsOfflineServerDialog(stale, true, now), true);
  assert.equal(needsOfflineServerDialog({online: false, lastSeenAt: null}, true, now), true, 'never seen: the dialog');
  assert.equal(needsOfflineServerDialog(undefined, true, now), false, 'unknown presence renders normally');
  assert.equal(needsOfflineServerDialog({online: true, lastSeenAt: iso(now - 30 * 60_000)}, true, now), false);
  assert.equal(needsOfflineServerDialog(stale, false, now), false, 'no failure: no dialog');
});

test('malformed presence stays unknown and never breaks the helpers', () => {
  assert.equal(parseServerPresence({online: 'yes'}), undefined);
  assert.equal(parseServerPresence(null), undefined);
  assert.deepEqual(presenceLabel(parseServerPresence({online: false, lastSeenAt: 'not-a-date'}), now), {kind: 'neverSeen'});
  const odd = [server('x', parseServerPresence('junk')), server('y', {online: true, lastSeenAt: iso(now)})];
  assert.deepEqual(sortServersByPresence(odd).map(s => s.id), ['y', 'x']);
});
