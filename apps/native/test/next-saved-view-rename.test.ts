import test from 'node:test';
import assert from 'node:assert/strict';
import {PersonalSavedService} from '../../../packages/client-core/src/personal-saved.ts';
import type {SavedApi, SavedScope} from '../../../packages/client-core/src/saved.ts';
import {createNativeSavedViewRenameController} from '../src-next/screens/saved/rename-view.ts';

const scope: SavedScope = {serverId: 'server-a', viewerId: 'actual-viewer'};
const fence = 'resource-fence';
const viewId = 'view-7';
const definition = {libraryId: 'library-a', pivot: 'movies', query: null, sort: [{field: 'title', direction: 'asc'}], presentation: 'grid'};

type FixtureView = {id: string; name: string; revision: number; actions: readonly string[]; kind?: 'view' | 'collection'};
type Call = {path: string; method: string; body?: unknown};

const browse = () => ({pivot: 'movies', applied: {query: null, sort: [{field: 'title', direction: 'asc'}], seek: null}, entries: [],
  pageInfo: {start: 0, total: 0, totalIsLowerBound: false, revision: 'browse-1', hasMore: false, nextCursor: ''}});
const resourceOf = (view: FixtureView) => ({serverId: scope.serverId, viewerFence: fence, id: view.id, kind: view.kind ?? 'view', name: view.name,
  summary: '', visibility: 'private', revision: view.revision, role: 'owner', entryCount: 0, actions: [...view.actions],
  definition, status: 'ready', invalidComponents: [], pinned: false, pinRevision: 0, shares: []});

/** A synthetic canonical Saved server: the resource route a view actually loads,
 * and the PATCH receipt the canonical service actually validates. */
class SavedFixture {
  readonly calls: Call[] = [];
  readonly views = new Map<string, FixtureView>();
  readonly requestIds: string[] = [];
  /** Receipts already applied, so a byte-identical retry is idempotent. */
  readonly applied = new Map<string, number>();
  patch: (body: Readonly<Record<string, unknown>>) => Promise<unknown>;
  constructor(views: readonly FixtureView[]) {
    for (const view of views) this.views.set(view.id, view);
    this.patch = async body => this.succeed(body);
  }
  get patchCalls(): Call[] { return this.calls.filter(call => call.method === 'PATCH'); }
  get reads(): number { return this.calls.filter(call => call.method === 'GET').length; }
  succeed(body: Readonly<Record<string, unknown>>): unknown {
    const view = this.views.get(viewId)!;
    const operationId = String(body.operationId);
    const prior = this.applied.get(operationId);
    if (prior !== undefined) return {serverId: scope.serverId, viewerFence: fence,
      receipt: {operationId, revision: prior, resourceId: view.id, deleted: false}};
    if (typeof body.expectedRevision === 'number' && body.expectedRevision !== view.revision) {
      throw Object.assign(new Error('This saved view changed.'), {code: 'revision_mismatch'});
    }
    if (typeof body.name === 'string') view.name = body.name;
    view.revision += 1;
    this.applied.set(operationId, view.revision);
    return {serverId: scope.serverId, viewerFence: fence,
      receipt: {operationId, revision: view.revision, resourceId: view.id, deleted: false}};
  }
  readonly api: SavedApi = {request: async <T>(path: string, method = 'GET', body?: unknown): Promise<T> => this.route(path, method, body) as Promise<T>};
  private async route(path: string, method: string, body?: unknown): Promise<unknown> {
    this.calls.push({path, method, ...(method === 'PATCH' ? {body} : {})});
    if (method === 'GET') {
      const match = /\/v1\/saved-resources\/([^/]+)\/content$/.exec(path.split('?')[0]!);
      if (!match) throw new Error(`unexpected read ${path}`);
      const view = this.views.get(decodeURIComponent(match[1]!));
      if (!view) throw new Error(`unknown view ${match[1]}`);
      return {nextCursor: '', resource: resourceOf(view), browse: browse(), entries: [], query: {sort: 'position', direction: 'asc', category: 'all', q: '', limit: 40, searchMode: 'none'}, sorts: [], filters: [], totalCount: 0, start: 0};
    }
    if (method === 'PATCH') return await this.patch((body ?? {}) as Readonly<Record<string, unknown>>);
    throw new Error(`unexpected ${method} ${path}`);
  }
}

const uuid = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;

/** Load the real resource route through the service's public API before the
 * controller exists, exactly as the owning Saved view does. */
async function open(view: FixtureView, extra: readonly FixtureView[] = []) {
  const fixture = new SavedFixture([view, ...extra]);
  const service = new PersonalSavedService(fixture.api, scope, async () => {
    const id = uuid(fixture.requestIds.length + 1);
    fixture.requestIds.push(id);
    return id;
  });
  await service.select({view: 'resource', resourceId: view.id});
  assert.equal(service.getSnapshot().resource?.id, view.id);
  return {fixture, service, resource: service.getSnapshot().resource!};
}

test('constructing the rename controller reads nothing and starts from the resource name verbatim', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: '  Comfort movies  ', revision: 4, actions: ['update', 'pin']});
  const reads = fixture.reads;
  const requests = fixture.requestIds.length;
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.name, '  Comfort movies  ');
    assert.equal(snapshot.busy, false);
    assert.equal(snapshot.error, null);
    assert.equal(snapshot.retryPending, false);
    assert.equal(snapshot.closed, false);
    assert.equal(snapshot.retired, false);
    assert.equal(snapshot.canSave, true);
    assert.equal(fixture.reads, reads);
    assert.equal(fixture.patchCalls.length, 0);
    assert.equal(fixture.requestIds.length, requests);
  } finally {controller.dispose();}
});

test('Save sends only the trimmed name and the original CAS through the canonical update', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update', 'pin', 'delete']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    assert.equal(controller.setName('  Comfort movies HD  '), true);
    assert.equal(await controller.save(), true);
    const [patch] = fixture.patchCalls;
    assert.equal(patch!.method, 'PATCH');
    assert.equal(patch!.path, `/v1/saved-resources/${viewId}`);
    assert.deepEqual(patch!.body, {expectedRevision: 4, name: 'Comfort movies HD', operationId: fixture.requestIds[0]});
    assert.deepEqual(Object.keys(patch!.body as object).sort(), ['expectedRevision', 'name', 'operationId']);
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.name, '');
    assert.equal(snapshot.busy, false);
    assert.equal(snapshot.error, null);
    assert.equal(snapshot.retryPending, false);
    assert.equal(snapshot.closed, true);
    assert.equal(snapshot.canSave, false);
    assert.equal(service.getSnapshot().pending, false);
    assert.equal(service.getSnapshot().resource?.name, 'Comfort movies HD');
    assert.equal(service.getSnapshot().result?.resourceId, viewId);
  } finally {controller.dispose();}
});

test('a second Save while the first is in flight sends nothing and cannot edit the name', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    let release!: () => void;
    const held = new Promise<void>(resolve => {release = resolve;});
    let started!: () => void;
    const began = new Promise<void>(resolve => {started = resolve;});
    fixture.patch = async body => {started(); await held; return fixture.succeed(body);};
    const first = controller.save();
    assert.equal(controller.getSnapshot().busy, true);
    assert.equal(controller.setName('Second try'), false);
    await began;
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 1);
    assert.equal(fixture.requestIds.length, 1);
    release();
    assert.equal(await first, true);
    assert.equal(controller.getSnapshot().closed, true);
  } finally {controller.dispose();}
});

test('an empty trimmed name cannot be submitted', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    assert.equal(controller.setName('   '), true);
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 0);
    assert.equal(controller.getSnapshot().name, '   ');
    assert.equal(controller.getSnapshot().closed, false);
  } finally {controller.dispose();}
});

test('a controller constructed after the owner retired is inert and cannot revive', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => false});
  try {
    assert.equal(controller.getSnapshot().retired, true);
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(controller.setName('Renamed'), false);
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 0);
    assert.equal(controller.getSnapshot().name, 'Comfort movies');
  } finally {controller.dispose();}
});

test('a view that does not offer update cannot be renamed', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['pin']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 0);
    assert.equal(controller.getSnapshot().name, 'Renamed');
    assert.equal(controller.getSnapshot().closed, false);
    assert.equal(controller.getSnapshot().error, null);
  } finally {controller.dispose();}
});

test('a replaced resource is not renamed and does not lose the typed name', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']},
    [{id: 'view-8', name: 'Other view', revision: 2, actions: ['update']}]);
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    assert.equal(controller.setName('Renamed'), true);
    await service.select({view: 'resource', resourceId: 'view-8'});
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 0);
    assert.equal(controller.getSnapshot().name, 'Renamed');
    assert.equal(controller.getSnapshot().closed, false);
    assert.equal(controller.getSnapshot().retired, false);
  } finally {controller.dispose();}
});

test('a server validation refusal keeps the typed name and surfaces the canonical error', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    fixture.patch = async () => {throw Object.assign(new Error('A saved view name may hold at most 200 characters.'), {code: 'invalid_name'});};
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    const failure = service.getSnapshot().mutationError;
    const snapshot = controller.getSnapshot();
    assert.equal(failure?.code, 'invalid_name');
    assert.equal(snapshot.error, failure);
    assert.deepEqual(snapshot.error, {code: 'invalid_name', message: 'A saved view name may hold at most 200 characters.', conflict: false});
    assert.equal(snapshot.name, 'Renamed');
    assert.equal(snapshot.closed, false);
    assert.equal(snapshot.busy, false);
    assert.equal(snapshot.retryPending, true);
    assert.equal(snapshot.canSave, false);
    assert.equal(service.getSnapshot().retryPending, true);
    assert.equal(fixture.patchCalls.length, 1);
  } finally {controller.dispose();}
});

test('a conflict keeps the typed name, surfaces the canonical conflict and never rebases', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    fixture.patch = async () => {throw Object.assign(new Error('This saved view changed.'), {code: 'revision_mismatch'});};
    const readsBefore = fixture.reads;
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    const snapshot = controller.getSnapshot();
    assert.equal(snapshot.error?.conflict, true);
    assert.equal(snapshot.error, service.getSnapshot().mutationError);
    assert.equal(snapshot.name, 'Renamed');
    assert.equal(snapshot.retryPending, true);
    assert.equal(snapshot.closed, false);
    assert.equal(fixture.patchCalls.length, 1, 'a conflict is never automatically rebased');
    assert.equal(fixture.reads, readsBefore + 1, 'the canonical service re-read the conflicted resource once');
    assert.equal(service.getSnapshot().resource?.revision, 4);
  } finally {controller.dispose();}
});

test('a lost receipt retries the original operation byte for byte and refuses edits or a new Save', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    let attempt = 0;
    fixture.patch = async body => {
      attempt += 1;
      if (attempt === 1) {
        fixture.succeed(body);
        throw Object.assign(new Error('The answer was lost before it arrived.'), {code: 'timeout'});
      }
      return fixture.succeed(body);
    };
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    const failed = controller.getSnapshot();
    assert.equal(failed.error?.code, 'timeout');
    assert.equal(failed.retryPending, true);
    assert.equal(failed.name, 'Renamed');
    assert.equal(controller.setName('Different name'), false);
    assert.equal(controller.getSnapshot().name, 'Renamed');
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 1);
    assert.equal(fixture.requestIds.length, 1);
    assert.equal(await controller.retry(), true);
    assert.equal(fixture.patchCalls.length, 2);
    assert.deepEqual(fixture.patchCalls[1]!.body, fixture.patchCalls[0]!.body);
    assert.equal(fixture.requestIds.length, 1, 'retry reuses the canonical operation ID');
    assert.equal(controller.getSnapshot().name, '');
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(controller.getSnapshot().retryPending, false);
  } finally {controller.dispose();}
});

test('owner retirement refuses a late acknowledgement and never revives', async () => {
  let ownerCurrent = true;
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => ownerCurrent});
  try {
    let release!: () => void;
    const held = new Promise<void>(resolve => {release = resolve;});
    fixture.patch = async body => {await held; return fixture.succeed(body);};
    assert.equal(controller.setName('Comfort movies HD'), true);
    const saving = controller.save();
    assert.equal(controller.getSnapshot().busy, true);
    ownerCurrent = false;
    release();
    assert.equal(await saving, false);
    const retired = controller.getSnapshot();
    assert.equal(retired.retired, true);
    assert.equal(retired.closed, false, 'a late receipt cannot close a retired presentation');
    assert.equal(retired.name, 'Comfort movies HD');
    assert.equal(service.getSnapshot().result?.resourceId, viewId, 'the canonical owner still received and applied the receipt');
    ownerCurrent = true;
    assert.equal(controller.setName('Renamed'), false);
    assert.equal(await controller.save(), false);
    assert.equal(controller.getSnapshot().retired, true);
    assert.equal(fixture.patchCalls.length, 1);
  } finally {controller.dispose();}
});

test('an owner-dismissed command clears only the local error, and an unrelated result never closes the rename', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    fixture.patch = async () => {throw Object.assign(new Error('This saved view changed.'), {code: 'revision_mismatch'});};
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    assert.equal(controller.getSnapshot().retryPending, true);
    const discarded = fixture.requestIds[0];
    service.dismiss();
    const afterDismiss = controller.getSnapshot();
    assert.equal(afterDismiss.error, null);
    assert.equal(afterDismiss.retryPending, false);
    assert.equal(afterDismiss.name, 'Renamed');
    assert.equal(afterDismiss.closed, false);
    assert.equal(afterDismiss.canSave, true);
    fixture.patch = async body => fixture.succeed(body);
    assert.equal(await service.mutate({action: 'update', summary: 'An unrelated edit'}), true);
    assert.equal(controller.getSnapshot().closed, false, 'another mutation with the same resource ID is not this rename');
    assert.equal(controller.getSnapshot().name, 'Renamed');
    assert.equal(await controller.save(), true);
    assert.notEqual(fixture.requestIds[2], discarded);
    assert.equal(controller.getSnapshot().closed, true);
  } finally {controller.dispose();}
});

test('Cancel closes only this presentation and leaves the borrowed uncertain command retryable', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    fixture.patch = async () => {throw Object.assign(new Error('The answer was lost.'), {code: 'timeout'});};
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    const pending = service.getSnapshot().mutationError;
    assert.equal(service.getSnapshot().retryPending, true);
    assert.equal(controller.cancel(), true);
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(controller.getSnapshot().error, null);
    assert.equal(service.getSnapshot().retryPending, true, 'Cancel never dismisses the owner command');
    assert.equal(service.getSnapshot().mutationError, pending);
    fixture.patch = async body => fixture.succeed(body);
    assert.equal(await service.retry(), true);
    assert.equal(service.getSnapshot().resource?.name, 'Renamed');
    assert.equal(service.getSnapshot().retryPending, false);
    assert.equal(controller.getSnapshot().closed, true, 'the late owner receipt never reopens the cancelled presentation');
    assert.equal(controller.getSnapshot().name, 'Renamed');
  } finally {controller.dispose();}
});

test('Dispose during a Save only removes local state; the borrowed service finishes and stays usable', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    let release!: () => void;
    const held = new Promise<void>(resolve => {release = resolve;});
    fixture.patch = async body => {await held; return fixture.succeed(body);};
    assert.equal(controller.setName('Renamed'), true);
    const saving = controller.save();
    assert.equal(controller.cancel(), false, 'Cancel cannot dismiss an in-flight command');
    controller.dispose();
    controller.dispose();
    assert.equal(controller.getSnapshot().busy, false);
    assert.equal(controller.getSnapshot().closed, false);
    release();
    assert.equal(await saving, false);
    assert.equal(service.getSnapshot().result?.resourceId, viewId);
    assert.equal(service.getSnapshot().resource?.name, 'Renamed');
    const reads = fixture.reads;
    await service.refresh();
    assert.equal(fixture.reads, reads + 1, 'the owner service is still alive and readable');
  } finally {controller.dispose();}
});

test("two rename presentations sharing one service never consume each other's command or result", async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const first = createNativeSavedViewRenameController({service, resource, current: () => true});
  const second = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    let release!: () => void;
    const held = new Promise<void>(resolve => {release = resolve;});
    let started!: () => void;
    const began = new Promise<void>(resolve => {started = resolve;});
    fixture.patch = async body => {started(); await held; return fixture.succeed(body);};
    assert.equal(first.setName('First name'), true);
    assert.equal(second.setName('Second name'), true);
    const saving = first.save();
    await began;
    assert.equal(second.getSnapshot().canSave, false, 'the shared command keeps the other presentation from submitting');
    assert.equal(await second.save(), false);
    assert.equal(second.getSnapshot().closed, false);
    assert.equal(fixture.patchCalls.length, 1);
    release();
    assert.equal(await saving, true);
    assert.equal(first.getSnapshot().closed, true);
    assert.equal(second.getSnapshot().closed, false, "the first presentation's receipt is not the second acknowledgement");
    assert.equal(second.getSnapshot().name, 'Second name');
    assert.equal(second.getSnapshot().canSave, true);
    assert.equal(await second.save(), true);
    assert.equal(second.getSnapshot().closed, true);
    assert.equal(first.getSnapshot().closed, true);
    assert.equal(fixture.patchCalls.length, 2);
    assert.notEqual(fixture.requestIds[0], fixture.requestIds[1]);
  } finally {first.dispose(); second.dispose();}
});

test('an unknown outcome owns its original command; only the owner dismissal starts a new one after the canonical refresh', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    let attempt = 0;
    fixture.patch = async body => {
      attempt += 1;
      if (attempt === 1) {
        fixture.succeed(body);
        throw Object.assign(new Error('The answer was lost.'), {code: 'timeout'});
      }
      return fixture.succeed(body);
    };
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    const uncertain = controller.getSnapshot();
    assert.equal(uncertain.retryPending, true);
    assert.equal(uncertain.name, 'Renamed');
    assert.equal(controller.setName('New name'), false, 'an unknown outcome refuses a name edit');
    const original = fixture.patchCalls[0]!.body as Record<string, unknown>;
    assert.equal(await controller.save(), false, 'an unknown outcome refuses a new Save');
    assert.equal(fixture.patchCalls.length, 1);
    assert.equal(fixture.requestIds.length, 1);
    service.dismiss();
    assert.equal(controller.getSnapshot().retryPending, false);
    assert.equal(controller.getSnapshot().name, 'Renamed');
    assert.equal(controller.getSnapshot().canSave, true);
    assert.equal(await controller.retry(), false, 'a dismissed operation cannot be re-sent');
    assert.equal(fixture.patchCalls.length, 1);
    assert.equal(await controller.save(), false, 'the stale CAS after an applied-but-unknown rename is a canonical conflict');
    const conflicted = controller.getSnapshot();
    assert.equal(conflicted.error?.code, 'revision_mismatch');
    assert.equal(conflicted.error?.conflict, true);
    assert.equal(conflicted.retryPending, true);
    assert.equal(conflicted.name, 'Renamed');
    assert.equal(service.getSnapshot().resource?.revision, 5);
    assert.equal(service.getSnapshot().resource?.name, 'Renamed');
    const second = fixture.patchCalls[1]!.body as Record<string, unknown>;
    assert.equal(second.name, 'Renamed');
    assert.equal(second.expectedRevision, 4);
    assert.notEqual(second.operationId, original.operationId);
    service.dismiss();
    assert.equal(await controller.save(), true, 'the canonical refreshed CAS makes the next Save a new command');
    const third = fixture.patchCalls[2]!.body as Record<string, unknown>;
    assert.equal(third.expectedRevision, 5);
    assert.notEqual(third.operationId, original.operationId);
    assert.notEqual(third.operationId, second.operationId);
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(controller.getSnapshot().name, '');
    assert.equal(service.getSnapshot().resource?.name, 'Renamed');
  } finally {controller.dispose();}
});

test('Dispose after an unknown outcome leaves the canonical retry to the owner and never closes later', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    fixture.patch = async () => {throw Object.assign(new Error('The answer was lost.'), {code: 'timeout'});};
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(await controller.save(), false);
    assert.equal(service.getSnapshot().retryPending, true);
    const pending = service.getSnapshot().mutationError;
    controller.dispose();
    controller.dispose();
    assert.equal(controller.getSnapshot().closed, false);
    assert.equal(controller.getSnapshot().error, pending);
    assert.equal(service.getSnapshot().retryPending, true);
    assert.equal(service.getSnapshot().mutationError, pending);
    fixture.patch = async body => fixture.succeed(body);
    assert.equal(await service.retry(), true);
    assert.equal(service.getSnapshot().resource?.name, 'Renamed');
    assert.equal(service.getSnapshot().retryPending, false);
    assert.equal(controller.getSnapshot().closed, false, 'a disposed presentation never observes the late receipt');
    assert.equal(controller.getSnapshot().name, 'Renamed');
  } finally {controller.dispose();}
});

test('a resource replacement during a Save discards the late acknowledgement without closing or leaking', async () => {
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']},
    [{id: 'view-8', name: 'Other view', revision: 2, actions: ['update']}]);
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    let release!: () => void;
    const held = new Promise<void>(resolve => {release = resolve;});
    let started!: () => void;
    const began = new Promise<void>(resolve => {started = resolve;});
    fixture.patch = async body => {started(); await held; return fixture.succeed(body);};
    assert.equal(controller.setName('Renamed'), true);
    const saving = controller.save();
    await began;
    await service.select({view: 'resource', resourceId: 'view-8'});
    assert.equal(await saving, false);
    assert.equal(controller.getSnapshot().closed, false, 'the replaced route cannot close on the old request');
    assert.equal(controller.getSnapshot().name, 'Renamed');
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(service.getSnapshot().resource?.id, 'view-8');
    assert.equal(service.getSnapshot().result, null, 'the replaced route consumed no late receipt');
    release();
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(controller.getSnapshot().closed, false);
    assert.equal(service.getSnapshot().result, null);
    await service.select({view: 'resource', resourceId: viewId});
    assert.equal(service.getSnapshot().resource?.name, 'Renamed', 'the late PATCH reached the server, but only a fresh read shows it');
    assert.equal(controller.getSnapshot().canSave, true);
    assert.equal(await controller.save(), true);
    assert.equal(controller.getSnapshot().closed, true);
    assert.equal(fixture.patchCalls.length, 2);
  } finally {controller.dispose();}
});

test('a loaded collection that offers update cannot use the Saved-view rename controller', async () => {
  const {fixture, service, resource} = await open({id: 'collection-3', name: 'Weekend picks', revision: 2, actions: ['update', 'pin'], kind: 'collection'});
  const controller = createNativeSavedViewRenameController({service, resource, current: () => true});
  try {
    assert.equal(resource.kind, 'collection');
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(controller.setName('Renamed'), true);
    assert.equal(controller.getSnapshot().canSave, false);
    assert.equal(await controller.save(), false);
    assert.equal(fixture.patchCalls.length, 0, 'a non-view resource never reaches a PATCH');
    assert.equal(controller.getSnapshot().name, 'Renamed', 'the refused draft survives');
    assert.equal(controller.getSnapshot().closed, false);
    assert.equal(controller.getSnapshot().error, null);
    assert.equal(service.getSnapshot().resource?.name, 'Weekend picks');
    assert.equal(service.getSnapshot().resource?.revision, 2);
    assert.equal(service.getSnapshot().pending, false);
  } finally {controller.dispose();}
});

test('retirement found by a read is latched silently and cannot revive, while actions and service callbacks still publish', async () => {
  let ownerCurrent = true;
  const {fixture, service, resource} = await open({id: viewId, name: 'Comfort movies', revision: 4, actions: ['update']});
  const readOnly = createNativeSavedViewRenameController({service, resource, current: () => ownerCurrent});
  const active = createNativeSavedViewRenameController({service, resource, current: () => true});
  const silent: number[] = [];
  const notified: number[] = [];
  const stopSilent = readOnly.subscribe(() => silent.push(silent.length + 1));
  const stopActive = active.subscribe(() => notified.push(notified.length + 1));
  try {
    const before = readOnly.getSnapshot();
    assert.equal(readOnly.getSnapshot(), before, 'an unchanged read returns the cached snapshot');
    ownerCurrent = false;
    const retired = readOnly.getSnapshot();
    assert.equal(retired.retired, true);
    assert.equal(silent.length, 0, 'a read must not notify its subscribers');
    assert.equal(readOnly.getSnapshot(), retired, 'the latched retired snapshot stays stable');
    ownerCurrent = true;
    assert.equal(readOnly.getSnapshot(), retired, 'owner false-to-true cannot revive retirement');
    assert.equal(readOnly.setName('Renamed'), false);
    assert.equal(await readOnly.save(), false);
    assert.equal(fixture.patchCalls.length, 0);
    assert.equal(active.setName('Active name'), true);
    assert.equal(notified.length, 1, 'an explicit action still publishes normally');
    assert.equal(await service.mutate({action: 'update', summary: 'An unrelated edit'}), true);
    assert.ok(notified.length > 1, 'a real service callback still publishes normally');
    assert.equal(silent.length, 0, 'the retired controller stopped its local subscription');
    assert.equal(readOnly.getSnapshot(), retired, 'the retired snapshot is unchanged by the service callback');
    assert.equal(active.getSnapshot().closed, false);
  } finally {
    stopSilent(); stopActive(); readOnly.dispose(); active.dispose();
  }
});
