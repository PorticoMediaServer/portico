import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi,type LocalSession} from '../src/index.ts';
import {ConsoleClient} from '../src/console.ts';
import {PreferencesReader} from '../src/preferences-reader.ts';
import {emptyUserMutationState,type UserMutationState,USER_MUTATION_MAX_ENTRIES,newUserMutationKey} from '../src/user-mutations.ts';

const clone=<T>(value:T):T=>JSON.parse(JSON.stringify(value));
class Store {
 state=emptyUserMutationState();fail=false;gate?:Promise<void>;
 async read(){await this.gate;return clone(this.state);}
 async compareAndSwap(revision:number,next:UserMutationState){if(this.fail)throw new Error('full');if(revision!==this.state.revision)return false;this.state=clone(next);return true;}
}
const session:LocalSession={accessToken:'test-token',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-01-02T00:00:00Z',tokenGeneration:'1',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}};
const scope={serverId:'server',viewerId:JSON.stringify(['local','account','profile'])};
const envelope=(data:unknown)=>({scope:{serverId:'server',viewerFence:'a'.repeat(64)},data});
const snapshot=(deviceClass='tablet')=>({deviceClass,registryRevision:'p39.A1',registry:{revision:'p39.A1',fields:[{key:'search.rememberHistory',type:'boolean',default:true,scopes:['profile-server'],group:'search',labelKey:'preferences.search.rememberHistory'}]},documents:[{scope:'profile-server',revision:4,digest:'a'.repeat(64),activeRevision:4,values:{'search.rememberHistory':false}},{scope:'profile-device-class',revision:1,digest:'a'.repeat(64),activeRevision:1,values:{}}],effective:{'search.rememberHistory':false},effectiveSource:{'search.rememberHistory':'profile-server'},clampedFields:[]});
const runtimeValues={name:'My server',conversionMode:'all',outputFormat:'auto',downloadMaxQuality:'original',musicAwayQuality:'original',ownAddressEnabled:false,perAccountCap:null,serverCap:null,diagnosticDays:30,notificationDays:180,jobDays:30,
 hardwareBackend:'auto',hardwareDevice:'',hardwareSelection:'auto',hardwareDecode:true,hdrToneMapping:false,hdrToneMappingAlgorithm:'hable',x264Preset:'veryfast',directStreamRemux:true,
 analysisStorageGiB:32,analysisStageStorageMiB:256,subtitleTrackLimit:128,subtitleFileLimitMiB:128,planningPolicy:'maximum_fidelity',throttleBufferMs:60000,playedRetentionMs:180000,temporaryDirectory:'',ffmpegPath:'',sessionStorageMiB:4096,
 maxConcurrentSessions:0,maxHardwareSessions:0,maxSoftwareSessions:0,maxBackgroundSessions:0,
 playHistoryDays:0,'home.communityActivityEnabled':false,'updates.releaseChannel':'stable','updates.feedUrl':'','library.missingTitles':'after_days','library.missingTitlesDays':30,'library.segmentDetection':'standard',
 remoteSignInPolicy:'allow',remoteBitrateLimitKbps:0,lanNetworks:[],accessUrls:[],lanDiscoveryEnabled:true,dlnaEnabled:false,
 trustedProxies:[],uploadCapacityKbps:0,pausedSessionTimeoutMinutes:0,advertisedInterface:'',customCertificatePath:'',customCertificateKeyPath:'',customCertificateDomain:'',secureConnections:'preferred',connectionLimit:0,advertiseUrl:'',apiRequestsPerMinute:0,logLevel:'info',logRetention:[]};
const settingsDocument=(revision=1)=>({revision,digest:'b'.repeat(64),requested:runtimeValues,effective:runtimeValues,activeRevision:revision,restartFields:[],registryRevision:'p08.0'});

const draft={'search.rememberHistory':false};
async function until(predicate:()=>boolean){for(let n=0;n<500;n++){if(predicate())return;await new Promise(resolve=>setImmediate(resolve));}assert.ok(predicate(),'bounded asynchronous condition');}
function fixture(store=new Store()){
 const writes:{key:string;body:any}[]=[],reads:string[]=[],callerIds:string[]=[],acknowledged:string[]=[];
 let mode:'lost'|'capacity'|'conflict'|'expired'|'success'='lost',lookup:'unknown'|'applied'|'expired'='unknown',valid=true;
 let cancel=new AbortController(),document:unknown;
 const api=new HttpLocalApi('http://server.test',session.accessToken,async(input,init)=>{
  const url=new URL(String(input)),method=init?.method??'GET';
  if(url.pathname==='/v1/user-mutations/policy')return Response.json({serverTimeMs:Date.now(),retiredBeforeMs:0});
  if(url.pathname.startsWith('/v1/user-mutations/')){
   const key=url.pathname.split('/').pop()!,entry=store.state.entries.find(entry=>entry.key===key)!;
   if(lookup==='expired')return Response.json({error:{code:'receipt_expired'}},{status:410});
   if(lookup==='unknown')return Response.json({error:{code:'receipt_unknown'}},{status:404});
   return Response.json({key,operation:entry.operation,status:'applied',revision:4,committedMs:Date.now(),expiresMs:Date.now()+86400000});
  }
  if(method==='GET'){reads.push(url.pathname+url.search);return Response.json(envelope(url.pathname==='/v1/admin/console/settings'?document:valid?snapshot(url.searchParams.get('deviceClass')??'web'):{}));}
  const body=JSON.parse(String(init?.body)),key=new Headers(init?.headers).get('Idempotency-Key')!;writes.push({body:clone(body),key});
  assert.equal(store.state.entries.find(entry=>entry.key===key)?.state,'pending');
  if(mode==='lost')throw new Error('lost answer');
  if(mode==='capacity')return Response.json({error:{code:'writer_capacity'}},{status:429,headers:{'Retry-After':'120'}});
  if(mode==='conflict')return Response.json({error:{code:'revision_mismatch',currentRevision:9}},{status:409});
  if(mode==='expired')return Response.json({error:{code:'receipt_expired'}},{status:410});
  lookup='applied';return Response.json(envelope(valid?snapshot(body.deviceClass):{}));
 });
 api.setUserMutationSession(session);const coordinator=api.configureUserMutations(store);
 const mounted={awaitUserMutationsReady:()=>api.awaitUserMutationsReady(),getUserMutationCoordinator:()=>coordinator,getUserMutationScope:api.getUserMutationScope,
  request:api.request.bind(api),
  async requestConsole<T>(path:string,method:string,body:unknown,signal:AbortSignal){if(method!=='GET')callerIds.push((body as any).idempotencyKey);return api.requestConsole<T>(path,method,body,AbortSignal.any([signal,cancel.signal]));},
  async acknowledgeUserMutation(id:string){acknowledged.push(id);await api.acknowledgeUserMutation(id);},
 };
 return {api,store,coordinator,mounted,writes,reads,callerIds,acknowledged,set document(value:unknown){document=value;},set mode(value:typeof mode){mode=value;},set lookup(value:typeof lookup){lookup=value;},set valid(value:boolean){valid=value;},abort(){cancel.abort();},resetCancel(){cancel=new AbortController();},async ready(){await coordinator.reconcile();},async due(){await until(()=>!!store.state.entries[0]&&!store.state.entries[0].leaseOwner);},async advance(){await until(()=>!store.state.entries[0]?.leaseOwner);store.state.entries[0].dueMs=0;await coordinator.reconcile();},dispose(){cancel.abort();coordinator.dispose();}};
}

for(const owner of ['reader','console'] as const){
 test(`mounted ${owner}: ambiguous retries and replacement owners retain the same key and original body`,async()=>{
  const f=fixture();let client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);
  const apply=(values=draft,revision=3)=>client.applyPreferences('profile-server','tablet',revision,values);
  try{
   await f.ready();const pending=apply();pending.catch(()=>{});await until(()=>f.writes.length===1);await f.due();f.abort();await assert.rejects(pending);
   const original=clone(f.store.state.entries[0]);assert.equal(original.state,'pending');assert.equal((original.body as any).expectedRevision,3);
   await assert.rejects(apply({'search.rememberHistory':true},9),/unconfirmed/);assert.equal(f.writes.length,1);
   f.resetCancel();const retry=apply();retry.catch(()=>{});await until(()=>f.callerIds.length===2);f.abort();await assert.rejects(retry);
   assert.equal(f.store.state.entries.length,1);assert.deepEqual(f.store.state.entries[0].body,original.body);assert.equal(f.callerIds[0],f.callerIds[1]);
   client.dispose();client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);f.resetCancel();f.mode='success';f.lookup='unknown';
   const replacement=apply();replacement.catch(()=>{});await until(()=>f.callerIds.length===3);await f.advance();await replacement;
   assert.equal(f.writes.length,2);assert.deepEqual(f.writes[1],f.writes[0],'unknown receipt replays the identical wire key and body');assert.equal(f.callerIds[2],original.intentId);assert.equal(f.acknowledged.length,1);assert.equal(f.store.state.entries.length,0);
  }finally{client.dispose();f.dispose();}
 });
 test(`mounted ${owner}: immediate durable restart waits for custody before minting a caller ID`,async()=>{
  const original=fixture();const first=owner==='reader'?new PreferencesReader(original.mounted,scope):new ConsoleClient(original.mounted,scope);
  let restarted:ReturnType<typeof fixture>|undefined,replacement:PreferencesReader|ConsoleClient|undefined,release!:()=>void;
  try{
   await original.ready();const pending=first.applyPreferences('profile-server','tablet',3,draft);pending.catch(()=>{});await until(()=>original.writes.length===1);await original.due();original.abort();await assert.rejects(pending);
   const entry=clone(original.store.state.entries[0]),store=new Store();store.state=clone(original.store.state);store.gate=new Promise<void>(resolve=>{release=resolve;});original.dispose();
   restarted=fixture(store);replacement=owner==='reader'?new PreferencesReader(restarted.mounted,scope):new ConsoleClient(restarted.mounted,scope);
   const retry=replacement.applyPreferences('profile-server','tablet',3,draft);retry.catch(()=>{});await new Promise(resolve=>setImmediate(resolve));assert.equal(restarted.callerIds.length,0,'no fresh caller ID before durable startup');
   release();await until(()=>restarted!.callerIds.length===1);restarted.lookup='applied';await restarted.advance();await retry;assert.deepEqual(restarted.callerIds,[entry.intentId]);assert.equal(restarted.writes.length,0);assert.equal(store.state.entries.length,0);
  }finally{release?.();first.dispose();replacement?.dispose();original.dispose();restarted?.dispose();}
 });
 test(`mounted ${owner}: writer capacity keeps full Retry-After and retries never enqueue another key`,async()=>{
  const f=fixture();f.mode='capacity';const client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);
  const apply=()=>client.applyPreferences('profile-server','tablet',3,draft);
  try{
   await f.ready();const first=apply();first.catch(()=>{});await until(()=>f.writes.length===1);await f.due();
   const entry=clone(f.store.state.entries[0]);assert.ok(entry.dueMs>=Date.now()+119000);f.abort();await assert.rejects(first);
   f.resetCancel();const retry=apply();retry.catch(()=>{});await until(()=>f.callerIds.length===2);f.abort();await assert.rejects(retry);
   assert.equal(f.store.state.entries.length,1);assert.equal(f.store.state.entries[0].key,entry.key);assert.equal(f.store.state.entries[0].dueMs,entry.dueMs);assert.equal(f.callerIds[0],f.callerIds[1]);assert.equal(f.writes.length,1);
  }finally{client.dispose();f.dispose();}
 });
 for(const mode of ['conflict','expired'] as const)test(`mounted ${owner}: ${mode} retains the original revision and draft through reconciliation`,async()=>{
  const f=fixture();f.mode=mode;const client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);
  const apply=(revision=3,values=draft)=>client.applyPreferences('profile-server','tablet',revision,values);
  try{
   await f.ready();if(client instanceof ConsoleClient)client.setDraft('preferences:profile-server',{revision:3,values:draft});
   await assert.rejects(apply());const entry=clone(f.store.state.entries[0]);assert.equal(entry.state,'review');assert.equal((entry.body as any).expectedRevision,3);
   await assert.rejects(apply(9,{'search.rememberHistory':true}),/unconfirmed/);
   if(client instanceof ConsoleClient){await assert.rejects(client.reconcilePending());assert.equal(client.hasUnconfirmed(),true);assert.deepEqual(client.getDraft('preferences:profile-server'),{revision:3,values:draft});}
   else await assert.rejects(apply());
   assert.equal(f.store.state.entries.length,1);assert.equal(f.store.state.entries[0].key,entry.key);assert.deepEqual(f.store.state.entries[0].body,entry.body);assert.equal(f.writes.length,1);assert.equal(f.acknowledged.length,0);
  }finally{client.dispose();f.dispose();}
 });
 test(`mounted ${owner}: parsing failure retains the receipt; duplicate successful parses acknowledge once`,async()=>{
  const f=fixture();f.mode='success';f.lookup='applied';f.valid=false;
  const client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);
  const apply=()=>client.applyPreferences('profile-server','tablet',3,draft);
  try{
   await f.ready();await assert.rejects(apply());assert.equal(f.store.state.entries[0].state,'confirmed');assert.equal(f.acknowledged.length,0);
   f.valid=true;await Promise.all([apply(),apply()]);assert.equal(f.writes.length,1);assert.equal(f.store.state.entries.length,0);assert.equal(f.acknowledged.length,1);assert.ok(f.reads.includes('/v1/preferences?deviceClass=tablet'));
  }finally{client.dispose();f.dispose();}
 });
 test(`mounted ${owner}: confirmed restart recovers the exact device class after request erasure`,async()=>{
  const original=fixture();original.mode='success';original.valid=false;
  const first=owner==='reader'?new PreferencesReader(original.mounted,scope):new ConsoleClient(original.mounted,scope);
  let restarted:ReturnType<typeof fixture>|undefined,replacement:PreferencesReader|ConsoleClient|undefined;
  try{
   await original.ready();await assert.rejects(first.applyPreferences('profile-server','tablet',3,draft));
   const entry=clone(original.store.state.entries[0]);assert.equal(entry.state,'confirmed');assert.equal(entry.body,undefined);assert.equal(entry.recoveryDeviceClass,'tablet');
   original.dispose();const store=new Store();store.state=clone(original.store.state);restarted=fixture(store);
   replacement=owner==='reader'?new PreferencesReader(restarted.mounted,scope):new ConsoleClient(restarted.mounted,scope);
   await assert.rejects(replacement.applyPreferences('profile-server','tablet',9,{'search.rememberHistory':true}),/unconfirmed/);
   await replacement.reconcilePending();
   assert.deepEqual(restarted.callerIds,[entry.intentId]);assert.equal(restarted.writes.length,0);assert.equal(restarted.acknowledged.length,1);assert.equal(store.state.entries.length,0);assert.ok(restarted.reads.includes('/v1/preferences?deviceClass=tablet'));
  }finally{first.dispose();replacement?.dispose();original.dispose();restarted?.dispose();}
 });
 test(`mounted ${owner}: family changes never retarget the original pending action`,async()=>{
  const f=fixture();const client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);
  try{
   await f.ready();const pending=client.applyPreferences('profile-server','tablet',3,draft);pending.catch(()=>{});await until(()=>f.writes.length===1);await f.due();f.abort();await assert.rejects(pending);
   const original=clone(f.store.state.entries[0]);f.api.setUserMutationSession({...session,sessionFamilyId:'different-family'});f.resetCancel();
   await assert.rejects(client.applyPreferences('profile-server','tablet',3,draft),/selected viewer changed/);
   assert.equal(f.writes.length,1);assert.equal(f.store.state.entries.length,1);assert.equal(f.store.state.entries[0].key,original.key);assert.equal(f.store.state.entries[0].scope.familyId,'family');
  }finally{client.dispose();f.dispose();}
 });
 test(`mounted ${owner}: local storage quota sends nothing and preserves the retry ID`,async()=>{
  const f=fixture();const client=owner==='reader'?new PreferencesReader(f.mounted,scope):new ConsoleClient(f.mounted,scope);
  const apply=()=>client.applyPreferences('profile-server','tablet',3,draft);
  try{
   await f.ready();const binding=f.api.getUserMutationScope()!;
   f.store.state.entries=Array.from({length:USER_MUTATION_MAX_ENTRIES},(_,n)=>({key:newUserMutationKey(Date.now()),operation:'patch_preferences',method:'PATCH',path:'/v1/preferences',target:'other-'+n,intentId:'other-'+n,fingerprint:'frozen',scope:binding,body:{expectedRevision:1},headers:{},state:'review' as const,attempts:0,dueMs:0,errorCode:'receipt_expired'}));
   // Other targets fill the store without claiming this caller's pending action.
   for(const entry of f.store.state.entries){entry.path='/v1/admin/logs/settings';entry.operation='patch_admin_logs_settings';}
   await assert.rejects(apply());await assert.rejects(apply());assert.equal(f.writes.length,0);assert.equal(f.store.state.entries.length,USER_MUTATION_MAX_ENTRIES);assert.equal(f.callerIds[0],f.callerIds[1]);
  }finally{client.dispose();f.dispose();}
 });
}

test('mounted runtime settings keep the complete original document and revision after conflict and reread',async()=>{
 const f=fixture();f.mode='conflict';f.document=settingsDocument(3);const client=new ConsoleClient(f.mounted,scope);
 try{
  const initial=await client.settings();client.setDraft('runtime-settings',{revision:3,values:{name:'My edit'}});
  await assert.rejects(client.applySettings(initial.revision,{name:'My edit'}));
  const original=clone(f.store.state.entries[0]);assert.equal((original.body as any).expectedRevision,3);assert.deepEqual((original.body as any).values,{...runtimeValues,name:'My edit'});
  f.document={...settingsDocument(9),requested:{...runtimeValues,connectionLimit:123}};await client.settings();
  await assert.rejects(client.applySettings(9,{name:'My edit'}));await assert.rejects(client.reconcilePending());
  assert.equal(f.writes.length,1);assert.equal(f.store.state.entries[0].key,original.key);assert.deepEqual(f.store.state.entries[0].body,original.body);assert.deepEqual(client.getDraft('runtime-settings'),{revision:3,values:{name:'My edit'}});
 }finally{client.dispose();f.dispose();}
});
