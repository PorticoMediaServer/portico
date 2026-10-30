import * as userMutations from '../../../packages/client-core/src/user-mutations.ts';
import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import * as notifications from '../../../packages/client-core/src/notifications.ts';
import * as preferenceValues from '../../../packages/client-core/src/preferences.ts';
import * as tolerant from '../../../packages/client-core/src/tolerant.ts';
import * as messages from '../../../packages/client-core/src/server-messages.ts';
import {screenModule} from './helpers/screen-harness.mjs';
import * as installation from '../../../packages/client-core/src/installation.ts';

const routePath = (name: string) => '/test/' + name;
const unsupported = () => {throw new Error('Unexpected bootstrap-resource test path');};
const react = {default: {}};
const routes = {routePath, encodedRoutePath: unsupported, routeBody: (_name: string, body: unknown) => body};
// Run the actual core coalescing and forgiving-inbox boundaries. Only omitted
// generated path resolution and display-only error copy are replaced.
const inbox = await screenModule(new URL('../../../packages/client-core/src/inbox.ts', import.meta.url), {
  react,
  '../../contracts/src/routes.ts': routes, '../../contracts/src/route-paths.ts': routes, './random-id.ts': {randomId: () => 'operation'}, './installation.ts': installation, './notifications.ts': notifications,
  './presentation/service-text.ts': {serviceProblem: () => 'Request failed', serviceText: (key: string) => key},
});
const preferences = await screenModule(new URL('../../../packages/client-core/src/preferences-reader.ts', import.meta.url), {
  react,
  '../../contracts/src/routes.ts': routes, '../../contracts/src/route-paths.ts': routes, './preferences.ts': preferenceValues, './user-mutations.ts': userMutations,
});
const guide = await screenModule(new URL('../../../packages/client-core/src/guide/directory.ts', import.meta.url), {
  react,
  '../../../contracts/src/routes.ts': routes, '../../../contracts/src/route-paths.ts': routes, '../server-messages.ts': messages, '../tolerant.ts': tolerant,
  // These tests use an empty, genuinely parsed source directory; detailed
  // channel/programme parsing is outside the resource lifetime under test.
  '../channel-guide.ts': {GUIDE_PROTOCOL: '1.0', parseGuideSources: unsupported, parseGuideChannels: unsupported, parseGuideProgramme: unsupported},
});
class HomePort {
  private api: {request: (path: string) => Promise<any>};
  private state: any = {phase: 'idle'};
  constructor(api: {request: (path: string) => Promise<any>}) {this.api = api;}
  getSnapshot = () => this.state;
  async load() {const document = await this.api.request(routePath('get_home')); this.state = {phase: 'ready', document};}
  dispose() {}
}
const {NativeShellBootstrapServices} = await screenModule(new URL('../src-next/shell/bootstrap-services.ts', import.meta.url), {
  react, './shared-read': await import('../src-next/shell/shared-read.ts'), './account-binding': await import('../src-next/shell/account-binding.ts'), './home-read-log': await import('../src-next/shell/home-read-log.ts'),
  '../../../../packages/client-core/src/home-service': {HomeService: HomePort},
  '../../../../packages/client-core/src/inbox': inbox, '../../../../packages/client-core/src/notifications': notifications,
  '../../../../packages/client-core/src/preferences-reader': preferences, '../../../../packages/client-core/src/guide/directory': guide,
  '../../../../packages/client-core/src/profile-management': {parseDirectSnapshot: unsupported}, '../../../../packages/contracts/src/routes': routes,
});

type Resource = 'unread' | 'guide' | 'preferences' | 'home';
type Request = {resource: Resource; method: string; path: string; signal: AbortSignal; startedAborted: boolean; body?: any};
function deferred<T>() {
  let resolve!: (value: T) => void, reject!: (error: unknown) => void;
  const promise = new Promise<T>((yes, no) => {resolve = yes; reject = no;});
  return {promise, resolve, reject};
}
const counts = {unread: 2, read: 0, archived: 0, total: 2};
const abortError = (error: any) => error?.name === 'AbortError';
const preferenceData = (speed = 1, revision = 1) => ({deviceClass: 'mobile', registryRevision: 'registry',
  registry: {revision: 'registry', fields: [{key: 'playback.defaultSpeed', type: 'number', default: 1, group: 'playback', labelKey: 'preferences.playback.defaultSpeed', scopes: ['profile-server']}]},
  documents: [{scope: 'profile-server', revision, activeRevision: revision, digest: 'd'.repeat(64), values: {'playback.defaultSpeed': speed}},
    {scope: 'profile-device-class', revision: 1, activeRevision: 1, digest: 'e'.repeat(64), values: {}}],
  effective: {'playback.defaultSpeed': speed}, effectiveSource: {'playback.defaultSpeed': 'profile-server'}, clampedFields: []});
function fixture(context: TestContext) {
  const calls: Request[] = [], owner = new AbortController(); let current = true, ended = 0;
  const held: {resource: Resource; method: string; pending: ReturnType<typeof deferred<unknown>>; request?: Request}[] = [];
  const reply = (request: Request): unknown => {
    if (request.resource === 'preferences') return {scope: {serverId: 'server', viewerFence: 'a'.repeat(64)},
      data: preferenceData(request.method === 'PATCH' ? 1.5 : 1, request.method === 'PATCH' ? 2 : 1)};
    if (request.resource === 'guide') return {protocolVersion: '1.0', serverId: 'server', viewerFence: 'guide-fence', revision: 'guide-1', sources: []};
    if (request.resource === 'home') return {serverId: 'server', viewerFence: 'home-fence', rows: []};
    return {scope: {serverId: 'server', viewerFence: 'b'.repeat(64)}, data: {
      revision: 7, audience: 'profile', counts, observedAt: '2026-10-22T12:00:00Z',
      ...(request.path.startsWith(routePath('get_notifications_inbox')) ? {audiences: ['profile'], state: 'unread', items: [], nextCursor: '', retentionDays: 180} : {}),
    }};
  };
  const api = {request: async (path: string, method = 'GET', body?: unknown, signal?: AbortSignal) => {
    assert.ok(signal, 'native services must supply their owned cancellation signal');
    const resource: Resource = path.includes('notifications_') ? 'unread' : path.includes('guide_') ? 'guide' : path.includes('preferences') ? 'preferences' : path.includes('get_home') ? 'home' : unsupported();
    const request = {resource, method, path, signal, startedAborted: signal.aborted, body}; calls.push(request);
    const hold = held.find(value => value.resource === resource && value.method === method && !value.request);
    if (hold) {hold.request = request; return hold.pending.promise;}
    return reply(request);
  }};
  const services = new NativeShellBootstrapServices(api, {viewer: {authority: 'local', role: 'member', serverId: 'server', accountId: 'account', profileId: 'profile'},
    deviceClass: 'mobile', signal: owner.signal, current: () => current, onAuthorityEnded: () => {ended++;},
    accountBinding: {assertAccount() {}, assertCurrent: async () => {assert.equal(current, true);}}});
  context.after(() => services.dispose());
  return {services, calls, ended: () => ended, retire: () => {current = false;},
    hold(resource: Resource, method = 'GET') {
      const value = {resource, method, pending: deferred<unknown>(), request: undefined as Request | undefined}; held.push(value);
      return {get request() {return value.request;}, finish: () => {assert.ok(value.request); value.pending.resolve(reply(value.request));},
        fail: (error: unknown) => value.pending.reject(error)};
    }};
}
const read = (services: any, resource: 'unread' | 'guide', signal?: AbortSignal, afterCurrent = false): Promise<unknown> =>
  resource === 'unread' ? services.readUnread(signal, afterCurrent) : services.readGuideAvailability(signal, afterCurrent);

for (const resource of ['unread', 'guide'] as const) {
  test(`${resource}: an event arriving during a live full-refresh read waits for a fresh successor`, async context => {
    const f = fixture(context);
    await read(f.services, resource);
    const old = f.hold(resource), first = read(f.services, resource), successor = f.hold(resource);
    const before = f.calls.length; let eventSettled = false;
    const eventRead = read(f.services, resource, new AbortController().signal, true).then(value => {eventSettled = true; return value;});
    assert.equal(f.calls.length, before); old.finish(); await first;
    for (let i = 0; i < 12; i++) await Promise.resolve();
    assert.ok(successor.request, 'the event issues a new physical read after the older answer');
    assert.equal(eventSettled, false, 'the old answer cannot discharge the new invalidation');
    assert.equal(f.calls.length, before + 1);
    successor.finish(); assert.equal(await eventRead, resource === 'unread' ? 2 : false);
  });

  test(`${resource}: a fresh caller queues behind an aborted flight, then reads again without overlap`, async context => {
    const f = fixture(context), held = f.hold(resource), firstOwner = new AbortController(), nextOwner = new AbortController();
    const first = read(f.services, resource, firstOwner.signal); void first.catch(() => {});
    assert.ok(held.request); firstOwner.abort();
    const next = read(f.services, resource, nextOwner.signal); void next.catch(() => {});
    assert.equal(f.calls.length, 1, 'even an abort-ignoring transport keeps its slot until it settles');
    assert.equal(held.request.signal.aborted, true);
    held.finish(); await assert.rejects(first, abortError);
    assert.equal(await next, resource === 'unread' ? 2 : false);
    assert.ok(f.calls.length > 1, 'the resumed caller receives a newly issued read');
    assert.equal(f.calls[1]!.startedAborted, false, 'the successor does not inherit the retired request signal');
    assert.equal(f.ended(), 0);
  });

  test(`${resource}: a caller cancelled while queued starts no new I/O`, async context => {
    const f = fixture(context), held = f.hold(resource), firstOwner = new AbortController(), nextOwner = new AbortController();
    const first = read(f.services, resource, firstOwner.signal); void first.catch(() => {}); firstOwner.abort();
    const next = read(f.services, resource, nextOwner.signal); void next.catch(() => {}); nextOwner.abort();
    held.finish(); await assert.rejects(first, abortError); await assert.rejects(next, abortError);
    assert.equal(f.calls.length, 1); assert.equal(f.ended(), 0);
  });

  test(`${resource}: authority retirement while queued prevents successor I/O and publication`, async context => {
    const f = fixture(context), held = f.hold(resource), firstOwner = new AbortController();
    const first = read(f.services, resource, firstOwner.signal); void first.catch(() => {}); firstOwner.abort();
    const next = read(f.services, resource, new AbortController().signal); void next.catch(() => {});
    f.retire(); held.finish(); await assert.rejects(first, abortError); await assert.rejects(next, abortError);
    assert.equal(f.calls.length, 1); assert.equal(f.ended(), 0, 'supersession is not a refusal by the captured authority');
    assert.equal(f.services.home.getSnapshot().document, undefined);
  });

  test(`${resource}: a late live permission denial retires an already-admitted Home`, async context => {
    const f = fixture(context);
    await read(f.services, resource); await f.services.home.load();
    assert.equal(f.services.home.getSnapshot().phase, 'ready');
    const held = f.hold(resource), pending = read(f.services, resource); void pending.catch(() => {});
    assert.ok(held.request); held.fail(Object.assign(new Error('denied'), {status: 403, code: 'forbidden'}));
    await assert.rejects(pending); assert.equal(f.ended(), 1); assert.equal(f.services.home.getSnapshot().document, undefined);
    const count = f.calls.length; await assert.rejects(read(f.services, resource), abortError); assert.equal(f.calls.length, count);
  });
}

test('pending unread and guide leave required preferences and the admitted Home transport independent', async context => {
  const f = fixture(context), inboxHold = f.hold('unread'), guideHold = f.hold('guide'); let settled = false;
  const auxiliary = Promise.all([f.services.readUnread(), f.services.readGuideAvailability()]).then(value => {settled = true; return value;});
  const [preferences] = await Promise.all([f.services.readPreferences('mobile'), f.services.home.load()]);
  assert.equal(preferences.deviceClass, 'mobile'); assert.equal(f.services.home.getSnapshot().phase, 'ready'); assert.equal(settled, false);
  inboxHold.finish(); guideHold.finish(); assert.deepEqual(await auxiliary, [2, false]); assert.equal(f.ended(), 0);
});

test('cancelling an event preference GET leaves a concurrent Settings PATCH independent', async context => {
  const f = fixture(context), get = f.hold('preferences'), patch = f.hold('preferences', 'PATCH'), owner = new AbortController();
  const refresh = f.services.readPreferences('mobile', owner.signal); void refresh.catch(() => {});
  const save = f.services.applyPreferences('profile-server', 'mobile', 1, {'playback.defaultSpeed': 1.5});
  assert.ok(get.request); assert.ok(patch.request); owner.abort();
  assert.equal(get.request.signal.aborted, true); assert.equal(patch.request.signal.aborted, false);
  get.finish(); patch.finish(); await assert.rejects(refresh, abortError);
  assert.equal((await save).effective['playback.defaultSpeed'], 1.5);
  assert.equal(f.ended(), 0); assert.deepEqual(f.calls.map(request => request.method), ['GET', 'PATCH']);
});
