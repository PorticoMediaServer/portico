import test from 'node:test';
import assert from 'node:assert/strict';
import {SourceSettingsController} from '../src/screens/settings/server-livetv/source-settings-controller.ts';
import {createOwnerSources} from '../src/screens/live/owner/OwnerSourcesPage.tsx';
const id = 'a'.repeat(48);
const filter = {mode: 'include', values: []};
const settings = {enabledGroups: [], enabledChannels: [], kind: 'playlist', streamBufferMs: null, retryWindowMs: null, userAgent: '', guideSourceId: '', importLimits: null,
  filters: {categories: filter, countries: filter, keywords: filter}, logoImport: {enabled: true, overwriteExisting: false}, channelNumbering: {mode: 'source', startAt: 1, step: 1}};
const doc = {sourceId: id, sourceName: 'Stored source', acquisition: '', revision: 7, digest: 'digest', tunerCount: 2, effectiveLogoImport: {enabled: true, overwriteExisting: false}, availableKinds: ['playlist', 'xmltv-guide'], settings,
  effective: {importLimits: {bytes: 268435456, channels: 10000, programmes: 1000000}, streamBufferMs: 8000, retryWindowMs: 60000, userAgent: '', guideDays: 7, logoImport: true, discoveryEnabled: false}};
const response = (result = doc) => ({protocolVersion: '1.0', serverId: 'server', result});
test('a save that gets no answer shows an error and the form reads the source again; the next save is a new one', async () => {
  const bodies: any[] = [];let attempt = 0, ids = 0;
  const controller = new SourceSettingsController({async request<T>(_path: string, method?: string, body?: any) {
    if (method === 'PUT') {bodies.push(body);if (++attempt === 1) throw {status: 503};return response({...doc, revision: 8, settings: body.settings}) as T;}return response() as T;
  }}, 'server', id, () => true, () => {}, () => (++ids).toString(16).padStart(48, '0'));
  await controller.load();controller.edit({userAgent: 'Stored player'});
  assert.equal(await controller.save(), false);
  for (let n = 0; n < 10; n++) await Promise.resolve();
  assert.ok(controller.getSnapshot().error, 'the failure stays on the page');
  assert.equal(controller.getSnapshot().draft?.userAgent, '', 'the form shows what the server has');
  controller.edit({userAgent: 'Stored player'});assert.equal(await controller.save(), true);
  assert.equal(bodies.length, 2);assert.notEqual(bodies[0].operationId, bodies[1].operationId);
  assert.equal(bodies[1].expectedRevision, 7);assert.equal(controller.getSnapshot().document?.revision, 8);controller.dispose();
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

test('unreadable configuration keeps exact retry error; after a save with no answer the form holds what the server has',async()=>{
 const error=Object.assign(new Error('original'),{status:503,retryAfterSeconds:7200});let fail=false;const controller=new SourceSettingsController({async request<T>(_p:string,method?:string){if(fail||method==='PUT')throw error;return response() as T;}},'server',id,()=>true,()=>{});
 await controller.load();const before=controller.getSnapshot();fail=true;const result=await controller.refreshQuietly();assert.equal(result.kind,'unreadable');if(result.kind==='unreadable')assert.equal(result.error,error);assert.equal(controller.getSnapshot(),before);fail=false;controller.edit({userAgent:'B'});await controller.save();for(let n=0;n<10;n++)await Promise.resolve();assert.ok(controller.getSnapshot().error);assert.deepEqual(controller.getSnapshot().draft,controller.getSnapshot().document?.settings);controller.dispose();
});
