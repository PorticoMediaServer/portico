import test from 'node:test';
import assert from 'node:assert/strict';
import {SourceSettingsController} from '../src/screens/settings/server-livetv/source-settings-controller.ts';
import {createOwnerSources} from '../src/screens/live/owner/OwnerSourcesPage.tsx';
import {UserMutationRecoveryModel} from '../../packages/client-core/src/user-mutation-recovery.ts';
const id = 'a'.repeat(48);
const filter = {mode: 'include', values: []};
const settings = {enabledGroups: [], enabledChannels: [], kind: 'playlist', streamBufferMs: null, retryWindowMs: null, userAgent: '', guideSourceId: '', importLimits: null,
  filters: {categories: filter, countries: filter, keywords: filter}, logoImport: {enabled: true, overwriteExisting: false}, channelNumbering: {mode: 'source', startAt: 1, step: 1}};
const doc = {sourceId: id, sourceName: 'Stored source', acquisition: '', revision: 7, digest: 'digest', tunerCount: 2, effectiveLogoImport: {enabled: true, overwriteExisting: false}, availableKinds: ['playlist', 'xmltv-guide'], settings,
  effective: {importLimits: {bytes: 268435456, channels: 10000, programmes: 1000000}, streamBufferMs: 8000, retryWindowMs: 60000, userAgent: '', guideDays: 7, logoImport: true, discoveryEnabled: false}};
const response = (result = doc) => ({protocolVersion: '1.0', serverId: 'server', result});
test('source configuration uses checked revision, stable retry id and receipt readback', async () => {
  const bodies: any[] = [];let attempt = 0;
  const controller = new SourceSettingsController({async request<T>(_path: string, method?: string, body?: any) {
    if (method === 'PUT') {bodies.push(body);if (++attempt === 1) throw {status: 503};return response({...doc, revision: 8, settings: body.settings}) as T;}return response() as T;
  }}, 'server', id, () => true, () => {}, () => 'b'.repeat(48));
  await controller.load();controller.edit({userAgent: 'Stored player'});
  assert.equal(await controller.save(), false);
  controller.edit({userAgent: 'Replacement'});assert.equal(await controller.save(), false);assert.equal(bodies.length, 1);
  controller.edit({userAgent: 'Stored player'});assert.equal(await controller.save(), true);
  assert.ok(Object.isFrozen(bodies[0]));assert.equal(bodies[0], bodies[1]);
  assert.deepEqual(bodies[0], bodies[1]);assert.equal(bodies[0].expectedRevision, 7);assert.equal(controller.getSnapshot().document?.revision, 8);controller.dispose();
});
test('retiring during load/save publication prevents API invocation', async () => {
  let calls = 0;const api = {async request<T>() {calls++;return response() as T;}};
  const loading = new SourceSettingsController(api, 'server', id, () => true, () => {});
  loading.subscribe(() => {if (loading.getSnapshot().loading) loading.dispose();});await loading.load();assert.equal(calls, 0);
  const saving = new SourceSettingsController(api, 'server', id, () => true, () => {});await saving.load();saving.edit({userAgent: 'Changed'});
  saving.subscribe(() => {if (saving.getSnapshot().saving) saving.dispose();});assert.equal(await saving.save(), false);assert.equal(calls, 1);assert.equal(saving.getSnapshot().document, undefined);
});
test('a configuration for another source never populates this source', async () => {
  const controller = new SourceSettingsController({async request<T>() {return response({...doc, sourceId: 'c'.repeat(48)}) as T;}}, 'server', id, () => true, () => {});
  await controller.load();assert.equal(controller.getSnapshot().document, undefined);assert.ok(controller.getSnapshot().error);controller.dispose();
});

test('persisted rows round-trip with revision and write-only credential changes', async()=>{
 const remote={kind:'xtream',guideUrl:'https://guide.example/guide.xml',guideUrlRedacted:true,refreshMs:21600000,ownerLimit:2,useDiscoveredCapacity:false};
 const initial={...doc,settings:{...settings,enabled:true,viewerAccess:'server-members',remote}};let body:any;
 const controller=new SourceSettingsController({async request<T>(_path:string,method?:string,payload?:any){if(method==='PUT'){body=payload;const {username,password,...publicRemote}=payload.settings.remote;return response({...initial,revision:9,settings:{...payload.settings,remote:publicRemote}}) as T;}return response(initial) as T;}},'server',id,()=>true,()=>{},()=> 'b'.repeat(48));
 await controller.load();assert.equal(controller.getSnapshot().draft?.remote?.password,undefined);
 controller.edit({enabled:false,viewerAccess:'owner-only',remote:{...remote,kind:'xtream',refreshMs:43200000,ownerLimit:3,password:'replace-me',username:''}});
 assert.equal(await controller.save(),true);assert.equal(body.expectedRevision,7);assert.equal(body.settings.enabled,false);assert.equal(body.settings.viewerAccess,'owner-only');assert.equal(body.settings.remote.password,'replace-me');assert.equal(body.settings.remote.username,'');assert.equal(controller.getSnapshot().draft?.remote?.password,undefined);controller.dispose();
});
test('a private acquisition edit never rebases silently onto a concurrent write',async()=>{
 const remote={kind:'xtream' as const,guideUrl:'',guideUrlRedacted:false,refreshMs:21600000,ownerLimit:0,useDiscoveredCapacity:false};let calls=0;
 const controller=new SourceSettingsController({async request<T>(_path:string,method?:string){calls++;if(method==='PUT')throw {status:409,code:'channel_revision_conflict'};return response({...doc,settings:{...settings,remote}}) as T;}},'server',id,()=>true,()=>{});
 await controller.load();controller.edit({remote:{...remote,password:'private'}});assert.equal(await controller.save(),false);assert.equal(calls,2);controller.dispose();
});
test('guide address policy confirmation remains explicit and never includes error text',async()=>{
 const remote={kind:'m3u' as const,guideUrl:'',guideUrlRedacted:false,refreshMs:21600000,ownerLimit:0,useDiscoveredCapacity:false};
 const controller=new SourceSettingsController({async request<T>(_path:string,method?:string){if(method==='PUT')throw {status:400,code:'source_lan_confirmation_required',message:'private-password',confirmRoots:['http://192.168.1.23:80/']};return response({...doc,settings:{...settings,remote}}) as T;}},'server',id,()=>true,()=>{});
 await controller.load();controller.edit({remote:{...remote,guideUrl:'http://192.168.1.23/guide.xml'}});assert.equal(await controller.save(),false);assert.deepEqual((controller.getSnapshot().error as any).confirmRoots,['http://192.168.1.23']);assert(!JSON.stringify(controller.getSnapshot().error).includes('private-password'));controller.dispose();
});

// Local protocol fixtures exercise the actual HTTP coordinator and CAS boundary.
import {HttpLocalApi, emptyUserMutationState, type UserMutationState, type LocalSession} from '../../packages/client-core/src/index.ts';
const clone = <T>(value: T): T => JSON.parse(JSON.stringify(value));
class MutationStore {
  state = emptyUserMutationState();failAcknowledgement = false;failAdmission = false;unknownAdmission = false;
  async read() {return clone(this.state);}
  async compareAndSwap(revision: number, next: UserMutationState) {
    if (this.state.revision !== revision) return false;
    if (this.failAdmission && next.entries.length > this.state.entries.length) throw new Error('local admission failed');
    if (this.failAcknowledgement && next.entries.length < this.state.entries.length) throw new Error('local acknowledgement failed');
    const admitted = next.entries.length > this.state.entries.length;
    this.state = clone(next);
    if (this.unknownAdmission && admitted) {this.unknownAdmission = false;throw new Error('admission committed but answer lost');}
    return true;
  }
}
const session: LocalSession = {accessToken: 'test-token', expiresAt: '2099-01-01T00:00:00Z', authorizationHorizon: '2099-01-02T00:00:00Z', tokenGeneration: '1', sessionFamilyId: 'family', deviceId: 'device', viewer: {serverId: 'server', authority: 'local', accountId: 'account', profileId: 'profile', role: 'owner'}};
function transportFixture(options: {conflict?: boolean; invalidResult?: boolean} = {}) {
  const store = new MutationStore(), calls: {path: string; method: string; body: any; key: string | null}[] = [], receipts = new Map<string, unknown>();
  let current = clone(doc), ids = 0;
  const fetcher: typeof fetch = async (input, init) => {
    const path = new URL(String(input)).pathname, method = init?.method ?? 'GET';
    const body = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined;
    const key = new Headers(init?.headers).get('Idempotency-Key');calls.push({path, method, body, key});
    if (path === '/v1/user-mutations/policy') return Response.json({serverTimeMs: Date.now(), retiredBeforeMs: 0});
    if (path.startsWith('/v1/user-mutations/')) return receipts.has(path.split('/').pop()!) ? Response.json(receipts.get(path.split('/').pop()!)) : Response.json({error: {code: 'receipt_unknown'}}, {status: 404});
    if (method === 'GET') return Response.json(response(current));
    const entry = store.state.entries.find(entry => entry.key === key);
    assert.ok(entry, 'CAS custody precedes every wire write');assert.equal(entry.state, 'pending');
    assert.equal((entry.body as any).expectedRevision, body.expectedRevision);
    if (options.conflict) {
      current = {...current, revision: 8, settings: {...current.settings, retryWindowMs: 1000}};
      return Response.json({error: {code: 'channel_revision_conflict'}}, {status: 409});
    }
    current = {...current, revision: current.revision + 1, settings: body.settings};
    receipts.set(key!, {key, operation: entry.operation, status: 'applied', targetId: id, revision: current.revision, committedMs: Date.now(), expiresMs: Date.now() + 2592000000});
    return Response.json(response(options.invalidResult ? {...current, sourceId: 'c'.repeat(48)} : current), {headers: {'Portico-Mutation-Key': key!}});
  };
  const api = new HttpLocalApi('http://server.test', session.accessToken, fetcher);api.setUserMutationSession(session);
  const coordinator = api.configureUserMutations(store);
  const makeId = () => (++ids).toString(16).padStart(48, '0');
  const owner = createOwnerSources({api, serverId: 'server', viewerId: 'account', isCurrent: () => api.getUserMutationScope()?.familyId === session.sessionFamilyId, reportError: () => {}});
  const controller = owner.track(new SourceSettingsController(owner.api, owner.serverId, id, owner.current, owner.reportError, makeId));
  return {store, calls, api, coordinator, owner, controller, makeId, ids: () => ids, close: () => {owner.dispose();coordinator.dispose();}};
}

test('public configuration conflict never reads/rebases or issues a replacement until reviewed release', async () => {
  const options = {conflict: true}, f = transportFixture(options);
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Original edit'});
    const draft = clone(f.controller.getSnapshot().draft);
    assert.equal(await f.controller.save(), false);
    const original = clone(f.store.state.entries[0]);
    assert.equal(original.state, 'review');assert.equal((original.body as any).expectedRevision, 7);
    assert.equal(await f.controller.save(), false);await f.controller.load();
    assert.equal(f.controller.getSnapshot().document?.revision, 7);assert.deepEqual(f.controller.getSnapshot().draft, draft);
    f.controller.edit({userAgent: 'Replacement edit'});assert.equal(await f.controller.save(), false);
    assert.equal(f.ids(), 1);assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
    assert.equal(f.calls.filter(call => call.method === 'GET' && call.path.includes('/configuration')).length, 1);
    assert.deepEqual(f.store.state.entries[0].body, original.body);assert.equal(f.store.state.entries[0].key, original.key);
    const write = f.calls.find(call => call.method === 'PUT')!;
    assert.equal(write.key, original.key);assert.equal(write.body.operationId, original.key);assert.equal(write.body.expectedRevision, 7);
    // Remount cannot bypass the retained original command.
    const reopened = new SourceSettingsController(f.api, 'server', id, () => true, () => {}, f.makeId);
    try {
      await reopened.load();reopened.edit({userAgent: 'Reviewed replacement'});
      assert.equal(await reopened.save(), false);assert.equal(f.ids(), 1);
      await f.api.dismissReviewedUserMutation(original.key);options.conflict = false;
      assert.equal(await reopened.save(), true);
      const writes = f.calls.filter(call => call.method === 'PUT');
      assert.equal(writes.length, 2);assert.notEqual(writes[1].key, original.key);assert.equal(writes[1].body.expectedRevision, 8);
      assert.equal(f.store.state.entries.length, 0);
    } finally {reopened.dispose();}
  } finally {f.close();}
});

test('parsed acknowledgement failure retries the exact frozen command and releases confirmed CAS quota', async () => {
  const f = transportFixture(), bodies: any[] = [], acknowledgements: string[] = [];
  const request = f.api.request.bind(f.api), acknowledge = f.api.acknowledgeUserMutation.bind(f.api);
  f.api.request = async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    if (method === 'PUT') bodies.push(body);return request<T>(path, method, body, signal);
  };
  f.api.acknowledgeUserMutation = async key => {acknowledgements.push(key);return acknowledge(key);};
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Original edit'});f.store.failAcknowledgement = true;
    assert.equal(await f.controller.save(), false);
    const original = clone(f.store.state.entries[0]);assert.equal(original.state, 'confirmed');
    assert.equal(f.controller.getSnapshot().document?.revision, 7);assert.equal(f.controller.getSnapshot().draft?.userAgent, 'Original edit');
    assert.equal(f.controller.getSnapshot().saved, false);
    assert.ok(Object.isFrozen(bodies[0]));assert.ok(Object.isFrozen(bodies[0].settings.filters.categories.values));
    f.controller.edit({userAgent: 'Changed edit'});assert.equal(await f.controller.save(), false);assert.equal(bodies.length, 1);
    f.controller.edit({userAgent: 'Original edit'});f.store.failAcknowledgement = false;
    assert.equal(await f.controller.save(), true);
    assert.equal(bodies[0], bodies[1]);assert.equal(bodies[1].expectedRevision, 7);
    assert.deepEqual(acknowledgements, [original.intentId, original.intentId]);assert.equal(f.ids(), 1);
    assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
    assert.equal(f.store.state.entries.length, 0);assert.equal(f.controller.getSnapshot().document?.revision, 8);
  } finally {f.close();}
});

test('wrong-source results remain unacknowledged until a checked current document is consumed', async () => {
  const f = transportFixture({invalidResult: true});let acks = 0;
  const acknowledge = f.api.acknowledgeUserMutation.bind(f.api);
  f.api.acknowledgeUserMutation = async key => {acks++;return acknowledge(key);};
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Original edit'});
    assert.equal(await f.controller.save(), false);assert.equal(acks, 0);
    assert.equal(f.store.state.entries[0].state, 'confirmed');assert.equal(f.controller.getSnapshot().document?.revision, 7);
    assert.equal(await f.controller.save(), true);assert.equal(acks, 1);assert.equal(f.ids(), 1);
    assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);assert.equal(f.store.state.entries.length, 0);
  } finally {f.close();}
});

test('257 mounted owner source saves forward acknowledgement and release the 256-entry capacity', async () => {
  const f = transportFixture();
  try {
    await f.controller.load();
    for (let index = 0; index < 257; index++) {
      f.controller.edit({userAgent: 'Player ' + index});assert.equal(await f.controller.save(), true, 'action ' + index);
      assert.equal(f.store.state.entries.length, 0);
    }
    const writes = f.calls.filter(call => call.method === 'PUT');assert.equal(writes.length, 257);
    assert.equal(new Set(writes.map(call => call.key)).size, 257);assert.equal(f.ids(), 257);
    assert.equal(f.controller.getSnapshot().document?.revision, 264);
  } finally {f.close();}
});

test('the actual owner wrapper guards acknowledgement before invocation and after completion', async () => {
  const retired = transportFixture();let calls = 0;
  retired.api.acknowledgeUserMutation = async () => {calls++;};
  try {
    retired.owner.dispose();
    await assert.rejects(retired.owner.api.acknowledgeUserMutation!('original'), {code: 'cancelled'});
    assert.equal(calls, 0);
  } finally {retired.close();}

  const f = transportFixture();let entered!: () => void, finish!: () => void;
  const started = new Promise<void>(resolve => {entered = resolve;}), held = new Promise<void>(resolve => {finish = resolve;});
  const acknowledge = f.api.acknowledgeUserMutation.bind(f.api);
  f.api.acknowledgeUserMutation = async key => {entered();await held;await acknowledge(key);};
  try {
    const pending = f.owner.api.acknowledgeUserMutation!('original');await started;
    f.owner.dispose();finish();
    await assert.rejects(pending, {code: 'cancelled'});
    assert.equal(f.store.state.entries.length, 0);
  } finally {finish();f.close();}
});

test('scope change after a checked response cannot acknowledge or replace the original family command', async () => {
  const f = transportFixture();let acknowledgements = 0;
  const request = f.api.request.bind(f.api);
  f.api.request = async <T>(path: string, method?: string, body?: unknown, signal?: AbortSignal) => {
    const result = await request<T>(path, method, body, signal);
    if (method === 'PUT') f.api.setUserMutationSession({...session, sessionFamilyId: 'successor-family'});
    return result;
  };
  f.api.acknowledgeUserMutation = async () => {acknowledgements++;};
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Original edit'});
    assert.equal(await f.controller.save(), false);assert.equal(acknowledgements, 0);
    assert.equal(f.controller.getSnapshot().document, undefined);
    assert.equal(f.store.state.entries[0].scope.familyId, 'family');
    f.controller.edit({userAgent: 'Replacement'});assert.equal(await f.controller.save(), false);
    assert.equal(f.ids(), 1);assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
  } finally {f.close();}
});

test('failed local admission does not freeze a later edited draft when authoritative custody is absent', async () => {
  const f = transportFixture();
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Not queued'});f.store.failAdmission = true;
    assert.equal(await f.controller.save(), false);assert.equal(f.store.state.entries.length, 0);assert.equal(f.calls.filter(call => call.method === 'PUT').length, 0);
    f.store.failAdmission = false;f.controller.edit({userAgent: 'Edited after storage recovery'});
    assert.equal(await f.controller.save(), true);assert.equal(f.calls.filter(call => call.method === 'PUT').length, 1);
    assert.equal(f.controller.getSnapshot().document?.settings.userAgent, 'Edited after storage recovery');
  } finally {f.close();}
});

test('an unknown admission result retains the actual durable command instead of trusting an empty snapshot', async () => {
  const f = transportFixture();
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Original admitted draft'});f.store.unknownAdmission = true;
    assert.equal(await f.controller.save(), false);const original = clone(f.store.state.entries[0]);assert.ok(original);
    f.controller.edit({userAgent: 'Replacement must wait'});assert.equal(await f.controller.save(), false);
    assert.equal(f.store.state.entries.length, 1);assert.equal(f.store.state.entries[0].key, original.key);assert.deepEqual(f.store.state.entries[0].body, original.body);
  } finally {f.close();}
});

test('the surviving controller reads the current revision after guarded review release', async () => {
  const options = {conflict: true},f = transportFixture(options);
  const model = new UserMutationRecoveryModel({api: f.api, coordinator: f.coordinator, scope: f.api.getUserMutationScope});
  try {
    await f.controller.load();f.controller.edit({userAgent: 'Original conflicted draft'});assert.equal(await f.controller.save(), false);
    const original = f.store.state.entries[0];await model.review(original.key);await model.keepAsIs(original.key);assert.equal(f.store.state.entries.length, 0);
    const reads = f.calls.filter(call => call.method === 'GET' && call.path.endsWith('/configuration')).length;
    await f.controller.load();assert.equal(f.calls.filter(call => call.method === 'GET' && call.path.endsWith('/configuration')).length, reads + 1);assert.equal(f.controller.getSnapshot().document?.revision, 8);
    options.conflict = false;f.controller.edit({userAgent: 'Explicit new edit'});assert.equal(await f.controller.save(), true);
    const writes = f.calls.filter(call => call.method === 'PUT');assert.equal(writes.length, 2);assert.equal(writes[0].body.expectedRevision, 7);assert.equal(writes[1].body.expectedRevision, 8);assert.notEqual(writes[0].key, writes[1].key);
  } finally {model.dispose();f.close();}
});

test('quiet configuration conflict preserves base, draft, acknowledgement and original revision for Save',async()=>{
 let fresh=false,put:any;const controller=new SourceSettingsController({async request<T>(_path:string,method?:string,body?:any){if(method==='PUT'){put=body;return response({...doc,revision:9,settings:body.settings}) as T;}return response(fresh?{...doc,revision:8,settings:{...settings,userAgent:'C'}}:doc) as T;}},'server',id,()=>true,()=>{},()=> 'b'.repeat(48));
 await controller.load();controller.edit({userAgent:'B'});const before=controller.getSnapshot();fresh=true;
 assert.deepEqual(await controller.refreshQuietly(),{kind:'conflict'});assert.equal(controller.getSnapshot(),before);assert.equal(await controller.save(),true);assert.equal(put.expectedRevision,7);assert.equal(put.settings.userAgent,'B');controller.dispose();
});

test('quiet configuration rebases current edits made during I/O',async()=>{
 let finish!:(value:unknown)=>void,fresh=false;const controller=new SourceSettingsController({async request<T>(){return fresh?new Promise(resolve=>{finish=resolve;}) as Promise<T>:response() as T;}},'server',id,()=>true,()=>{});
 await controller.load();fresh=true;const pending=controller.refreshQuietly();controller.edit({userAgent:'B'});const before=controller.getSnapshot();finish(response({...doc,revision:8,settings:{...settings,retryWindowMs:90000}}));assert.deepEqual(await pending,{kind:'adopted'});assert.equal(controller.getSnapshot().draft?.userAgent,'B');assert.equal(controller.getSnapshot().draft?.retryWindowMs,90000);assert.equal(controller.getSnapshot().document?.revision,8);assert.equal(controller.getSnapshot().saved,before.saved);assert.equal(controller.getSnapshot().error,before.error);controller.dispose();
});

test('Save and logo admission fence earlier quiet configuration reads even after completion',async()=>{
 for(const command of ['save','logo'] as const){let quiet=false,finish!:(value:unknown)=>void;const controller=new SourceSettingsController({async request<T>(path:string,method?:string,body?:any){if(method==='PUT')return response({...doc,revision:8,settings:body.settings}) as T;if(path.endsWith('/logos'))return {serverId:'server',protocolVersion:'1.0',result:{requested:1,imported:1,skipped:0}} as T;if(quiet)return new Promise(resolve=>{finish=resolve;}) as Promise<T>;return response() as T;}},'server',id,()=>true,()=>{});
 await controller.load();if(command==='save')controller.edit({userAgent:'B'});quiet=true;const pending=controller.refreshQuietly();if(command==='save')await controller.save();else await controller.importLogos();const before=controller.getSnapshot();finish(response({...doc,revision:99,settings:{...settings,userAgent:'stale'}}));assert.deepEqual(await pending,{kind:'deferred'});assert.equal(controller.getSnapshot(),before);controller.dispose();}
});

test('unreadable configuration keeps exact retry error; uncertain write cannot borrow a revision',async()=>{
 const error=Object.assign(new Error('original'),{status:503,retryAfterSeconds:7200});let fail=false;const controller=new SourceSettingsController({async request<T>(_p:string,method?:string){if(fail||method==='PUT')throw error;return response() as T;}},'server',id,()=>true,()=>{});
 await controller.load();const before=controller.getSnapshot();fail=true;const result=await controller.refreshQuietly();assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal(result.error,error);assert.equal(controller.getSnapshot(),before);fail=false;controller.edit({userAgent:'B'});await controller.save();const uncertain=controller.getSnapshot();assert.deepEqual(await controller.refreshQuietly(),{kind:'deferred'});assert.equal(controller.getSnapshot(),uncertain);controller.dispose();
});
