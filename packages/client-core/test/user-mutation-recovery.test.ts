import test from 'node:test';
import assert from 'node:assert/strict';
import {setImmediate} from 'node:timers/promises';
import {EventEmitter, once} from 'node:events';
import {HttpLocalApi, type LocalSession} from '../src/index.ts';
import {UserMutationRecoveryModel, type UserMutationRecoveryApi} from '../src/user-mutation-recovery.ts';
import {UserMutationCoordinator, emptyUserMutationState, userMutationFingerprint, type UserMutationEntry, type UserMutationState, type UserMutationScope, type UserMutationResponse} from '../src/user-mutations.ts';
import {createI18n, resolveRegion, supportedLocales} from '../../i18n/src/index.ts';
const clone=<T>(value:T):T=>JSON.parse(JSON.stringify(value));
const scope:UserMutationScope={serverId:'server',origin:'http://server.test',authority:'local',accountId:'account',profileId:'profile',familyId:'family',deviceId:'device'};
const key=(n:number)=>'p251_1800000000000_'+n.toString(16).padStart(32,'0');
const receipt=(n:number,status:NonNullable<UserMutationEntry['receipt']>['status']='applied')=>({key:key(n),operation:'put_admin_backup_settings',status,committedMs:1800000000000,expiresMs:1802592000000});
const entry=(n:number,patch:Partial<UserMutationEntry>={}):UserMutationEntry=>{const value:UserMutationEntry={key:key(n),scope,operation:'put_admin_backup_settings',method:'PUT',path:'/v1/admin/backups/settings',target:'backup',intentId:'intent-'+n,body:{passphraseProtected:true},fingerprint:'{}',headers:{'Idempotency-Key':key(n)},state:'review',attempts:1,dueMs:0,...patch};if(value.fingerprint==='{}')value.fingerprint=userMutationFingerprint({...value,headers:{}});return value;};
class Store {
 state=emptyUserMutationState();
 async read(){return clone(this.state);}
 async compareAndSwap(revision:number,next:UserMutationState){if(revision!==this.state.revision)return false;this.state=clone(next);return true;}
}
const backup={backupFolder:'',includeArtwork:false,passphraseProtected:true,passphraseSet:true};
async function fixture(entries:UserMutationEntry[],read:()=>Promise<unknown>=async()=>backup,lookup?:(entry:Readonly<UserMutationEntry>)=>Promise<UserMutationResponse>) {
 const store=new Store();store.state.entries=clone(entries);let current:UserMutationScope|undefined=scope;
 const core=new UserMutationCoordinator({store,scope:()=>current,ownerId:'recovery-test',transport:{send:async()=>{throw new Error('No background send');},lookup:lookup??(async()=>{throw new Error('No background lookup');}),recover:async()=>({status:200,headers:{}})},timers:{setTimeout:(()=>0) as unknown as typeof setTimeout,clearTimeout:()=>{}}});
 const api:UserMutationRecoveryApi={request:async<T>()=>await read() as T,reenterUserMutationSecrets:async()=>{throw new Error('Not reentry fixture');},acknowledgeUserMutation:key=>core.acknowledge(key),dismissReviewedUserMutation:key=>core.dismissReviewed(key)};
 // Subscribe before startup loads durable entries, as the mounted shell does.
 const model=new UserMutationRecoveryModel({api,coordinator:core,scope:()=>current});
 assert.equal(model.snapshot().rows.length,0);await core.start();
 return {store,core,model,switchScope:(next:UserMutationScope|undefined)=>{current=next;},close:()=>{model.dispose();core.dispose();}};
}
test('startup publication discovers retained work with stable typed snapshots and exact scope',async()=>{
 const f=await fixture([entry(1),entry(2,{scope:{...scope,familyId:'other'}}),entry(3,{scope:{...scope,profileId:'other'}}),...(['serverId','origin','authority','accountId','deviceId'] as const).map((field,index)=>entry(index+4,{scope:{...scope,[field]:'other'}}))]);
 try {const initial=f.model.snapshot();assert.equal(initial.rows.length,1);assert.equal(initial.rows[0].category,'settings');assert.equal(initial.rows[0].status,'review');assert.equal(f.model.snapshot(),initial);assert.equal('body' in initial.rows[0],false);assert.equal('errorCode' in initial.rows[0],false);assert.equal('operation' in initial.rows[0],false);assert.equal(initial.rows[0].createdMs,1800000000000);
  f.switchScope({...scope,familyId:'other'});assert.equal(f.model.snapshot().rows.length,0);await assert.rejects(f.model.review(key(1)));assert.equal(f.store.state.entries.length,8);
 }finally{f.close();}
});
test('explicit consumption requires successful typed current-state read; closing preserves custody',async()=>{
 let raw:unknown={current:true};
 const f=await fixture([entry(1,{state:'confirmed',body:undefined,fingerprint:'',receipt:receipt(1)})],async()=>raw);
 try {await assert.rejects(f.model.keepAsIs(key(1)));await assert.rejects(f.model.review(key(1)));assert.equal(f.model.snapshot().rows[0].canKeep,false);assert.equal(f.store.state.entries.length,1);
  raw=backup;assert.deepEqual(await f.model.review(key(1)),{kind:'settings',server:true,section:'storage'});assert.equal(f.model.snapshot().rows[0].canKeep,true);
  // Sheet close performs no action; a restart must review again.
  f.model.dispose();assert.equal(f.store.state.entries.length,1);
  const reopened=new UserMutationRecoveryModel({api:{request:async<T>()=>backup as T,reenterUserMutationSecrets:async()=>{throw Error();},acknowledgeUserMutation:k=>f.core.acknowledge(k),dismissReviewedUserMutation:k=>f.core.dismissReviewed(k)},coordinator:f.core,scope:()=>scope});
  try{assert.equal(reopened.snapshot().rows[0].canKeep,false);await reopened.review(key(1));await reopened.keepAsIs(key(1));assert.equal(f.store.state.entries.length,0);}finally{reopened.dispose();}
 }finally{f.close();}
});
test('unknown, expired, pending and quarantined histories survive review and Keep as is',async()=>{
 const f=await fixture([entry(1,{errorCode:'receipt_unknown'}),entry(2,{errorCode:'receipt_expired',terminalProof:{kind:'refused',httpStatus:409,code:'receipt_expired'}}),entry(3,{state:'pending',dueMs:9_000_000_000_000_000}),entry(4,{state:'quarantined',terminalProof:{kind:'refused',httpStatus:409,code:'revision_mismatch'}})]);
 try{for(let n=1;n<=4;n++){await f.model.review(key(n));await assert.rejects(f.model.keepAsIs(key(n)));}assert.equal(f.store.state.entries.length,4);assert.ok(f.model.snapshot().rows.every(row=>!row.canKeep));}finally{f.close();}
});
test('guarded terminal Keep as is releases only after read, and a scope change during read cannot authorize it',async()=>{
 let release!:(value:unknown)=>void;
 const pending=new Promise<unknown>(resolve=>{release=resolve;});
 const f=await fixture([entry(1,{terminalProof:{kind:'refused',httpStatus:412,code:'revision_mismatch'}})],()=>pending);
 try{const reading=f.model.review(key(1));f.switchScope({...scope,accountId:'other'});release(backup);await assert.rejects(reading);assert.equal(f.store.state.entries.length,1);f.switchScope(scope);
  await f.model.review(key(1));await f.model.keepAsIs(key(1));assert.equal(f.store.state.entries.length,0);
 }finally{f.close();}
});
test('an unsupported secret field never becomes an arbitrary body or URL editor',async()=>{
 const f=await fixture([entry(1,{requiresReentry:true,errorCode:'secret_reentry_required',secretFields:['arbitrary.url.password']})]);
 try{assert.deepEqual(f.model.snapshot().rows[0].secretFields,[]);await assert.rejects(f.model.reenter(key(1),{'arbitrary.url.password':'credential'}));assert.equal(f.store.state.entries.length,1);}finally{f.close();}
});
test('restart secret form reentry uses frozen body, original revision and same key without storing secrets',async()=>{
 const store=new Store(),safe={passphraseProtected:true,includeArtwork:false},input={operation:'put_admin_backup_settings',method:'PUT',path:'/v1/admin/backups/settings',target:'backup',body:safe,requiresReentry:true,secretFields:['passphrase']};
 store.state.entries=[entry(1,{...input,state:'pending',fingerprint:userMutationFingerprint(input),attempts:0})];
 const writes:{key:string|null;body:unknown}[]=[];
 const session={accessToken:'test-token',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}} as LocalSession;
 const fetcher=async(url:RequestInfo|URL,init?:RequestInit)=>{
  const path=new URL(String(url)).pathname;
  if(path.startsWith('/v1/user-mutations/'))return writes.length?Response.json({...receipt(1),operation:input.operation}):Response.json({error:{code:'receipt_unknown'}},{status:404});
  if(init?.method==='PUT'){writes.push({key:new Headers(init.headers).get('Idempotency-Key'),body:JSON.parse(String(init.body))});return Response.json(backup);}
  return Response.json(backup);
 };
 const api=new HttpLocalApi(scope.origin,session.accessToken,fetcher);api.setUserMutationSession(session);const core=api.configureUserMutations(store),model=new UserMutationRecoveryModel({api,coordinator:core,scope:api.getUserMutationScope});
 try{
  for(let n=0;n<30&&model.snapshot().rows[0]?.secretFields.length!==1;n++)await setImmediate();
  const row=model.snapshot().rows[0];assert.equal(writes.length,0);assert.deepEqual(row.secretFields,[{path:'passphrase',labelKey:'next.settings.backup.passphrase',maxLength:1024}]);
  await assert.rejects(model.reenter(key(1),{passphrase:'correct secret',extra:'not allowed'}));
  await model.reenter(key(1),{passphrase:'correct secret'});
  assert.deepEqual(writes,[{key:key(1),body:{...safe,passphrase:'correct secret'}}]);assert.equal(store.state.entries[0].key,key(1));assert.equal(store.state.entries[0].state,'confirmed');assert.equal(JSON.stringify(store.state).includes('correct secret'),false);assert.equal(JSON.stringify(model.snapshot()).includes('correct secret'),false);assert.equal(model.snapshot().rows[0].status,'done');assert.equal(model.snapshot().rows[0].canKeep,false);
 }finally{model.dispose();core.dispose();}
});
test('approved copy resolves through the normal catalogue for every supported locale',()=>{
 const expected={'recovery.title':'Pending changes','recovery.status.waiting':'Waiting','recovery.status.review':'Needs review','recovery.status.done':'Done','recovery.reviewNote':'Check the current state before changing it again.','recovery.reentryNote':'Re-enter the details to finish this change.','recovery.checkAgain':'Check again','recovery.review':'Review','recovery.reenter':'Re-enter','recovery.keepAsIs':'Keep as is'} as const;
 for(const locale of supportedLocales){const i18n=createI18n(resolveRegion({locale},{locales:[locale]}));for(const [key,value] of Object.entries(expected))assert.equal(i18n.t(key as keyof typeof expected),value);}
});

test('metadata secret reentry merges only allowed fields while retaining the original expected revision',async()=>{
 const safe={expectedRevision:17,includeAdult:false,localArtworkLimitBytes:1234},original=entry(1,{operation:'put_metadata_settings',path:'/v1/admin/metadata/settings',body:safe,requiresReentry:true,errorCode:'secret_reentry_required',secretFields:['tmdbReadToken','openSubtitlesPassword']});
 const f=await fixture([original]);let captured:unknown;
 const api:UserMutationRecoveryApi={request:async<T>()=>backup as T,acknowledgeUserMutation:async()=>{},dismissReviewedUserMutation:async()=>{},reenterUserMutationSecrets:async(key,body)=>{assert.equal(key,original.key);captured=body;return {status:200,headers:{}};}};
 const model=new UserMutationRecoveryModel({api,coordinator:f.core,scope:()=>scope});
 try{await model.reenter(original.key,{tmdbReadToken:'new token',openSubtitlesPassword:'password'});assert.deepEqual(captured,{...safe,tmdbReadToken:'new token',openSubtitlesPassword:'password'});assert.deepEqual(f.store.state.entries[0].body,safe);assert.equal(JSON.stringify(model.snapshot()).includes('new token'),false);}finally{model.dispose();f.close();}
});
test('live recovery exposes only the retained approved password and whole credential-bearing guide URL fields',async()=>{
 const safe={operationId:key(1),expectedRevision:9,settings:{remote:{kind:'m3u',guideUrlRedacted:false,refreshMs:3600000}}};
 const f=await fixture([entry(1,{operation:'set_live_source_configuration',path:'/v1/admin/live-sources/source/configuration',body:safe,requiresReentry:true,errorCode:'secret_reentry_required',secretFields:['settings.remote.guideUrl','settings.remote.password']})]);
 let captured:unknown;
 const api:UserMutationRecoveryApi={request:async<T>()=>backup as T,acknowledgeUserMutation:async()=>{},dismissReviewedUserMutation:async()=>{},reenterUserMutationSecrets:async(received,body)=>{assert.equal(received,key(1));captured=body;return {status:200,headers:{}};}};
 const model=new UserMutationRecoveryModel({api,coordinator:f.core,scope:()=>scope});
 try{assert.deepEqual(model.snapshot().rows[0].secretFields.map(field=>[field.path,field.labelKey,field.maxLength]),[['settings.remote.guideUrl','web.liveTv.guideUrl',8192],['settings.remote.password','next.remoteSources.password',2048]]);await model.reenter(key(1),{'settings.remote.guideUrl':'https://guide.test/list?token=private','settings.remote.password':'private password'});assert.deepEqual(captured,{...safe,settings:{remote:{...safe.settings.remote,guideUrl:'https://guide.test/list?token=private',password:'private password'}}});assert.deepEqual(f.store.state.entries[0].body,safe);}finally{model.dispose();f.close();}
});

test('review waits for the mounted startup load and cannot read after scope retirement',async()=>{
 const f=await fixture([entry(1,{state:'confirmed',body:undefined,fingerprint:'',receipt:receipt(1)})]);
 let ready!:()=>void,reads=0;
 const loading=new Promise<void>(resolve=>{ready=resolve;});
 const api:UserMutationRecoveryApi={awaitUserMutationsReady:()=>loading,request:async<T>()=>{reads++;return backup as T;},acknowledgeUserMutation:k=>f.core.acknowledge(k),dismissReviewedUserMutation:k=>f.core.dismissReviewed(k),reenterUserMutationSecrets:async()=>{throw Error();}};
 const model=new UserMutationRecoveryModel({api,coordinator:f.core,scope:()=>scope});
 try{const reviewing=model.review(key(1));await setImmediate();assert.equal(reads,0);model.dispose();ready();await assert.rejects(reviewing);assert.equal(reads,0);assert.equal(f.store.state.entries.length,1);}finally{model.dispose();f.close();}
});

test('Check again performs real lookup only for the original review key and never sends or consumes',async()=>{
 const lookedUp:string[]=[];let result=200;
 const f=await fixture([entry(1,{errorCode:'secret_reentry_required',requiresReentry:true,secretFields:['passphrase']}),entry(2,{errorCode:'receipt_unknown'})],async()=>backup,async original=>{lookedUp.push(original.key);return result===200?{status:200,headers:{},body:receipt(1)}:{status:result,headers:{},body:{error:{code:result===410?'receipt_expired':'receipt_unknown'}}};});
 try{await f.model.checkAgain(key(1));assert.deepEqual(lookedUp,[key(1)]);assert.equal(f.store.state.entries.length,2);assert.equal(f.store.state.entries[0].key,key(1));assert.equal(f.store.state.entries[0].state,'confirmed');assert.equal(f.model.snapshot().rows[0].status,'done');assert.equal(f.model.snapshot().rows[0].canKeep,false);assert.equal(f.store.state.entries[1].state,'review');
  for(const status of [404,410,503]){result=status;const before=clone(f.store.state.entries[1]);await f.model.checkAgain(key(2));assert.deepEqual(f.store.state.entries[1],before);}assert.deepEqual(lookedUp,[key(1),key(2),key(2),key(2)]);
 }finally{f.close();}
});

test('restarted live credential URL form reenters the same key and revision without persisting the URL',async()=>{
 const store=new Store(),safe={operationId:key(1),expectedRevision:23,settings:{remote:{kind:'m3u',guideUrlRedacted:false,refreshMs:3600000}}},input={operation:'set_live_source_configuration',method:'PUT',path:'/v1/admin/live-sources/source/configuration',target:'source',body:safe,requiresReentry:true,secretFields:['settings.remote.guideUrl']};
 store.state.entries=[entry(1,{...input,state:'pending',fingerprint:userMutationFingerprint(input),attempts:0})];
 const writes:{key:string|null;body:unknown}[]=[],url='https://guide.test/list?token=private-guide-token';
 const session={accessToken:'test-token',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}} as LocalSession;
 const fetcher=async(received:RequestInfo|URL,init?:RequestInit)=>{
  const path=new URL(String(received)).pathname;
  if(path.startsWith('/v1/user-mutations/'))return writes.length?Response.json({...receipt(1),operation:input.operation}):Response.json({error:{code:'receipt_unknown'}},{status:404});
  if(init?.method==='PUT'){writes.push({key:new Headers(init.headers).get('Idempotency-Key'),body:JSON.parse(String(init.body))});return Response.json({saved:true});}
  throw new Error('Unexpected transport');
 };
 const api=new HttpLocalApi(scope.origin,session.accessToken,fetcher);api.setUserMutationSession(session);const core=api.configureUserMutations(store),model=new UserMutationRecoveryModel({api,coordinator:core,scope:api.getUserMutationScope});
 try{for(let n=0;n<30&&model.snapshot().rows[0]?.secretFields.length!==1;n++)await setImmediate();assert.equal(writes.length,0);assert.equal(model.snapshot().rows[0].secretFields[0].path,'settings.remote.guideUrl');await model.reenter(key(1),{'settings.remote.guideUrl':url});assert.deepEqual(writes,[{key:key(1),body:{...safe,settings:{remote:{...safe.settings.remote,guideUrl:url}}}}]);assert.equal(store.state.entries[0].state,'confirmed');assert.equal(store.state.entries[0].key,key(1));assert.equal(JSON.stringify(store.state).includes('private-guide-token'),false);assert.equal(JSON.stringify(model.snapshot()).includes('private-guide-token'),false);}finally{model.dispose();core.dispose();}
});

test('actual admission and restart re-entry retain the authored caller ID and original wire key',async()=>{
 const store=new Store(),url='https://guide.test/list?token=original-private-token';
 const body={operationId:'a'.repeat(48),expectedRevision:23,settings:{remote:{kind:'m3u',guideUrl:url,guideUrlRedacted:false,refreshMs:3600000}}};
 const path='/v1/admin/live-sources/'+ 'b'.repeat(48)+'/configuration';
 const writes:{key:string|null;body:typeof body}[]=[];
 const session={accessToken:'test-token',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}} as LocalSession;
 const dispatched=new EventEmitter();
 const fetcher=async(received:RequestInfo|URL,init?:RequestInit)=>{
  const endpoint=new URL(String(received)).pathname;
  if(endpoint==='/v1/user-mutations/policy')return Response.json({serverTimeMs:Date.now()});
  if(endpoint.startsWith('/v1/user-mutations/')){
   const entry=store.state.entries[0];return writes.length>1?Response.json({key:entry.key,operation:entry.operation,status:'applied',targetId:'source',revision:24,committedMs:Date.now(),expiresMs:Date.now()+2592000000}):Response.json({error:{code:'receipt_unknown'}},{status:404});
  }
  writes.push({key:new Headers(init?.headers).get('Idempotency-Key'),body:JSON.parse(String(init?.body))});
  if(writes.length===1){dispatched.emit('write');throw new Error('connection lost before acceptance');}
  return Response.json({saved:true});
 };
 const oldApi=new HttpLocalApi(scope.origin,session.accessToken,fetcher);oldApi.setUserMutationSession(session);const old=oldApi.configureUserMutations(store),abort=new AbortController();
 const firstWire=once(dispatched,'write',{signal:AbortSignal.timeout(5000)});
 try{
 const pending=oldApi.request(path,'PUT',body,abort.signal);const canceled=assert.rejects(pending);
 // Observe the actual dispatched write; event-loop turns are not elapsed time.
 await firstWire;assert.equal(writes.length,1);
 abort.abort();old.dispose();oldApi.setUserMutationSession(undefined);await canceled;
 const original=store.state.entries[0].key;
 assert.equal((store.state.entries[0].body as {operationId:string}).operationId,original);
 assert.equal(JSON.parse(store.state.entries[0].fingerprint).body.operationId,body.operationId);
 store.state.entries[0].dueMs=0;delete store.state.entries[0].leaseOwner;delete store.state.entries[0].leaseUntilMs;
 const api=new HttpLocalApi(scope.origin,session.accessToken,fetcher);api.setUserMutationSession(session);const core=api.configureUserMutations(store),model=new UserMutationRecoveryModel({api,coordinator:core,scope:api.getUserMutationScope});
 try{
  // Recovery publication follows startup reconciliation. Subscribe to that
  // real state transition rather than assuming a number of microtask turns.
  await new Promise<void>((resolve,reject)=>{
   const timeout=setTimeout(()=>{off();reject(new Error('Restart did not publish secret re-entry'));},5000);
   const ready=()=>{if(model.snapshot().rows[0]?.secretFields.length){clearTimeout(timeout);off();resolve();}};
   const off=model.subscribe(ready);ready();
  });
  assert.equal(model.snapshot().rows[0]?.secretFields.length,1);
  await model.reenter(original,{'settings.remote.guideUrl':url});
  assert.equal(writes.length,2);assert.equal(writes[1].key,original);assert.deepEqual(writes[1].body,{...body,operationId:original});
  assert.equal(store.state.entries[0].state,'confirmed');assert.equal(JSON.stringify(store.state).includes('original-private-token'),false);
 }finally{model.dispose();core.dispose();}
 }finally{abort.abort();old.dispose();oldApi.setUserMutationSession(undefined);}
});
