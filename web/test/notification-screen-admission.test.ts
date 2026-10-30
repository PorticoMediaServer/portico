import test from 'node:test';
import assert from 'node:assert/strict';
import {InboxService, type InboxApi} from '@core/inbox.ts';
import {NotificationScreenAdmission} from '../src/screens/account/notification-screen-admission.ts';

// Production screen admission + real SDK, authored UNRUN. No browser or server
// job is started here. These envelopes use the existing canonical payload shape.
const at = '2026-10-26T00:00:00Z';
function fixture() {
  const calls: {path: string; method: string; body?: unknown}[] = [];
  let failure: unknown, revision = 7, operation = 0;
  const api: InboxApi = {request: async <T,>(path: string, method = 'GET', body?: unknown): Promise<T> => {
    calls.push({path, method, body});
    const url = new URL(path, 'https://server.test'), audience = url.searchParams.get('audience') ?? 'profile';
    const counts = {unread: 1, read: 0, archived: 0, total: 1};
    if (url.pathname === '/v1/notifications/unread-count') return {data: {revision, audience, counts, observedAt: at}} as T;
    if (method === 'POST') {
      if (failure) throw failure;
      const intent = body as {audience: string};
      return {data: {revision: ++revision, audience: intent.audience, applied: 1, receipts: [], counts: {...counts, unread: 0, read: 1}}} as T;
    }
    const view = url.searchParams.get('state') ?? 'unread', archived = view === 'archived';
    return {data: {revision, audience, audiences: ['profile', 'account-admin'], state: view, counts: archived ? {...counts, unread: 0, archived: 1} : counts,
      items: [{id: audience + '-1', audience, severity: 'info', source: 'downloads', category: 'download.finished', title: 'Download ready', body: 'Ready', arguments: {}, actions: [],
        dedupeKey: 'download:1', revision, createdAt: at, updatedAt: at, readAt: null, archivedAt: archived ? at : null, read: false, archived, cursor: '1'}],
      nextCursor: 'next', observedAt: at, retentionDays: 180}} as T;
  }};
  const service = new InboxService(api, () => 'operation-' + ++operation);
  return {service, calls, fail: (value?: unknown) => {failure = value;}};
}

test('retained bulk/More/audience/view/retry/navigation callbacks never borrow successor B', async () => {
  const f = fixture(), opening = new NotificationScreenAdmission(f.service, () => true);
  try {
    await f.service.load();
    // Drain only already-issued fixture microtasks, so badge refresh is settled.
    await f.service.refreshUnread();
    const a = f.service.getSnapshot(); let navigations = 0;
    const callbacks = [
      () => opening.run(a, () => {void f.service.readAll();}),
      () => opening.run(a, () => {void f.service.more();}),
      () => opening.run(a, () => {void f.service.load('profile', a.view);}),
      () => opening.run(a, () => {void f.service.load(a.audience, 'unread');}),
      () => opening.run(a, () => {void f.service.load(a.audience, a.view);}),
      () => opening.leave(a, () => {navigations++;}),
    ];
    await f.service.load('account-admin', 'archived'); await f.service.refreshUnread();
    const b = f.service.getSnapshot(), count = f.calls.length;
    for (const retained of callbacks) assert.equal(retained(), false);
    assert.equal(f.calls.length, count);assert.equal(navigations, 0);assert.equal(f.service.getSnapshot(), b);
  } finally {opening.retire();f.service.dispose();}
});

test('navigation spends its opening synchronously and original retirement cancels its publication', async () => {
  const f = fixture(), opening = new NotificationScreenAdmission(f.service, () => true);
  try {
    await f.service.load();await f.service.refreshUnread();const drawn = f.service.getSnapshot();
    let navigations = 0;
    opening.leave(drawn, current => {
      assert.equal(opening.leave(drawn, () => {navigations++;}), false);
      opening.retire();
      if (current()) navigations++;
    });
    assert.equal(navigations, 0);
  } finally {opening.retire();f.service.dispose();}
});

test('route retirement and Strict replay do not revive old handlers or dispose the borrowed service', async () => {
  const f = fixture();let route = 'notifications';
  const a = new NotificationScreenAdmission(f.service, () => route === 'notifications');
  try {
    await f.service.load();await f.service.refreshUnread();const drawn = f.service.getSnapshot();
    route = 'settings';a.retire();
    assert.equal(a.run(drawn, () => {void f.service.readAll();}), false);
    route = 'notifications';
    const b = new NotificationScreenAdmission(f.service, () => route === 'notifications');
    assert.equal(a.run(drawn, () => {throw new Error('old handler ran');}), false);
    let ran = 0;assert.equal(b.run(drawn, () => {ran++;}), true);assert.equal(ran, 1);
    b.retire();await f.service.load('account-admin', 'all');
    assert.equal(f.service.getSnapshot().phase, 'ready');assert.equal(f.service.getSnapshot().audience, 'account-admin');
  } finally {a.retire();f.service.dispose();}
});

test('retired passive initializer makes zero I/O and an explicit view choice prevents a second initial load', async () => {
  const f = fixture(), a = new NotificationScreenAdmission(f.service, () => true);
  try {
    const idle = f.service.getSnapshot();a.retire();a.initialize(idle);assert.equal(f.calls.length, 0);
    const b = new NotificationScreenAdmission(f.service, () => true);
    assert.equal(b.run(idle, () => {void f.service.load('account-admin', 'all');}), true);
    const count = f.calls.length;b.initialize(idle);b.initialize(f.service.getSnapshot());assert.equal(f.calls.length, count);
    b.retire();
  } finally {f.service.dispose();}
});

test('freshly drawn retry retains the SDK uncertain operation id/body; old callback cannot retry it', async () => {
  const f = fixture(), opening = new NotificationScreenAdmission(f.service, () => true);
  try {
    await f.service.load();await f.service.refreshUnread();const a = f.service.getSnapshot();
    f.fail(new TypeError('response lost'));
    let command = Promise.resolve();
    opening.run(a, () => {command = f.service.readAll();});await command;
    const posts = () => f.calls.filter(value => value.method === 'POST');
    assert.equal(posts().length, 1);const original = structuredClone(posts()[0].body);
    assert.equal(opening.run(a, () => {void f.service.readAll();}), false);
    f.fail();const retry = f.service.getSnapshot();
    opening.run(retry, () => {command = f.service.readAll();});await command;
    assert.equal(posts().length, 2);assert.deepEqual(posts()[1].body, original);
  } finally {opening.retire();f.service.dispose();}
});
