import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, UserMutationError, emptyUserMutationState, type UserMutationState, type LocalSession} from '../src/index.ts';
import {userMutationRoutes,userMutationRoute} from '../src/user-mutation-routes.ts';
const copy=<T>(value:T):T=>JSON.parse(JSON.stringify(value));
class Store {
 state=emptyUserMutationState();fail=false;
 async read(){return copy(this.state);}
 async compareAndSwap(revision:number,next:UserMutationState){if(this.fail)throw new Error('disk full');if(this.state.revision!==revision)return false;this.state=copy(next);return true;}
}
const session:LocalSession={accessToken:'token',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-01-02T00:00:00Z',tokenGeneration:'1',sessionFamilyId:'family',deviceId:'device',viewer:{serverId:'server',authority:'local',accountId:'account',profileId:'profile',role:'owner'}};
function fixture(options:{refuse?:boolean;lookup?:boolean;retryDate?:boolean;wait?:Promise<void>;malformed503?:boolean;lostResponse?:boolean;acceptedBody?:'html'|'truncated'|'stream'|'oversized';malformedLookup?:'html'|'typed';refuse400?:boolean}={}){
 const store=new Store(),calls:{path:string;query:string;method:string;headers:Headers;body:any}[]=[];
 const fetcher=async(input:RequestInfo|URL,init?:RequestInit)=>{
  const path=new URL(String(input)).pathname,method=init?.method??'GET',headers=new Headers(init?.headers),body=typeof init?.body==='string'?JSON.parse(init.body):undefined;
  calls.push({path,query:new URL(String(input)).search,method,headers,body});
  if(path==='/v1/user-mutations/policy')return Response.json({serverTimeMs:Date.now()+86400000,retiredBeforeMs:0});
  if(path.startsWith('/v1/user-mutations/')){
   const key=path.split('/').pop()!,entry=store.state.entries.find(entry=>entry.key===key)!;
   if(options.malformedLookup)return options.malformedLookup==='html'?new Response('<html>receipt unavailable</html>',{status:200,headers:{'Retry-After':'120'}}):Response.json({key,operation:entry.operation,status:'applied',committedMs:'malformed'},{headers:{'Retry-After':'120'}});
   if(options.lookup===false)return Response.json({error:{code:'receipt_unknown'}},{status:404});
   return Response.json({key,operation:entry.operation,status:'applied',targetId:'target',revision:2,generation:1,committedMs:Date.now(),expiresMs:Date.now()+2592000000});
  }
  if(method==='GET')return Response.json({current:true});
  const entry=store.state.entries.find(entry=>entry.key===headers.get('Idempotency-Key'));
  assert.ok(entry,'durable record precedes the HTTP write');assert.equal(entry.state,'pending');
  if(options.wait)await options.wait;
  if(options.acceptedBody){if(options.acceptedBody==='stream')return new Response(new ReadableStream({start(controller){controller.error(new Error('truncated accepted response'));}}),{status:200});return new Response(options.acceptedBody==='html'?'<html>accepted by upstream</html>':'{\"revision\":',{status:200,headers:options.acceptedBody==='oversized'?{'Content-Length':'1048577'}:{}});}
  if(options.refuse400)return Response.json({error:{code:'invalid_request'}},{status:400});
  if(options.malformed503)return new Response('<html>upstream unavailable</html>',{status:503,headers:{'Retry-After':'120'}});
  if(options.lostResponse)throw new Error('response lost after commit');
  if(options.refuse)return Response.json({error:{code:'revision_mismatch',current:{revision:9}}},{status:412,headers:{'Retry-After':options.retryDate?new Date(Date.now()+86400000+120000).toUTCString():'120'}});
  if(method==='DELETE')return new Response(null,{status:204});
  return Response.json({operationId:body?.operationId,idempotencyKey:body?.idempotencyKey,revision:2},{headers:{'Portico-Mutation-Key':headers.get('Idempotency-Key')!}});
 };
 const api=new HttpLocalApi('http://server.test',session.accessToken,fetcher);api.setUserMutationSession(session);const coordinator=api.configureUserMutations(store);
 return {api,store,calls,coordinator};
}
test('exact generated cohort is 56 and excludes auth, jobs, reread and observations',()=>{
 assert.equal(userMutationRoutes.length,56);
 for(const route of userMutationRoutes){const path=route.path.replace(/\{[^}]+\}/g,'identifier');assert.equal(userMutationRoute(path,route.method)?.id,route.id);}
 for(const[path,method]of [['/v1/auth/refresh','POST'],['/v1/jobs','POST'],['/v1/items/id/reading/reread','POST'],['/v1/playback/sessions/id/observation','PUT'],['/v1/admin/libraries/id','DELETE'],['/v1/admin/backups/run','POST']])assert.equal(userMutationRoute(path,method),undefined);
});
test('request persists protocol key with original revision and correlates the caller intent response',async()=>{
 const f=fixture();try{
  const body={operationId:'original-uuid',expectedRevision:7,read:true};
  const value=await f.api.request<any>('/v1/items/item/reading/mark','PUT',body);
  const write=f.calls.find(call=>call.method==='PUT')!,key=write.headers.get('Idempotency-Key')!;
  assert.match(key,/^p251_[0-9]{13}_[a-f0-9]{32}$/);assert.ok(Number(key.split('_')[1])>Date.now()+86000000);
  assert.equal(write.body.operationId,key);assert.equal(write.body.expectedRevision,7);assert.equal(body.operationId,'original-uuid');assert.equal(value.operationId,'original-uuid');
  await f.api.acknowledgeUserMutation('original-uuid');assert.equal(f.store.state.entries.length,0);
 }finally{f.api.getUserMutationCoordinator()?.dispose();f.coordinator.dispose();}
});
test('raw Stop retains original session generation and first final position under retry',async()=>{
 const f=fixture();try{
  await f.api.requestRaw('/v1/playback/sessions/ps','DELETE',{body:{generation:3,positionMs:1500}});
  await f.api.requestRaw('/v1/playback/sessions/ps','DELETE',{body:{generation:3,positionMs:1900}});
  const writes=f.calls.filter(call=>call.method==='DELETE');assert.equal(writes.length,1);assert.deepEqual(writes[0].body,{generation:3,positionMs:1500});assert.equal(f.store.state.entries[0].intentId,'stop:ps:3');
 }finally{f.api.getUserMutationCoordinator()?.dispose();f.coordinator.dispose();}
});
test('underlying raw 412 and original conflict current are preserved',async()=>{
 const f=fixture({refuse:true});try{
  const response=await f.api.requestRaw('/v1/items/item/personal-state','PUT',{body:{operationId:'original',expectedRevision:2}});
  assert.equal(response.status,412);assert.deepEqual(response.body,{error:{code:'revision_mismatch',current:{revision:9}}});assert.equal(response.headers['retry-after'],'120');
  assert.equal(f.store.state.entries[0].state,'review');assert.equal((f.store.state.entries[0].body as any).expectedRevision,2);
 }finally{f.api.getUserMutationCoordinator()?.dispose();f.coordinator.dispose();}
});
test('local storage refusal sends no mutation',async()=>{
 const f=fixture();f.store.fail=true;try{await assert.rejects(f.api.request('/v1/preferences','PATCH',{values:{theme:'dark'}}));assert.equal(f.calls.filter(call=>call.method==='PATCH').length,0);}finally{f.coordinator.dispose();}
});
test('same-family auth refresh works and a family switch quarantines original intent',async()=>{
 let release!:()=>void;const wait=new Promise<void>(resolve=>{release=resolve;}),f=fixture({wait});
 try{const pending=f.api.requestRaw('/v1/playback/sessions/ps','DELETE',{body:{generation:1,positionMs:12}});pending.catch(()=>{});
  for(let n=0;n<30&&!f.calls.some(call=>call.method==='DELETE');n++)await new Promise(resolve=>setTimeout(resolve,2));
  f.api.setUserMutationSession({...session,sessionFamilyId:'other',accessToken:'other-token'});release();await assert.rejects(pending,UserMutationError);await f.coordinator.reconcile();assert.equal(f.store.state.entries[0].state,'quarantined');
 }finally{release();f.coordinator.dispose();}
});
test('metadata settings secrets stay ephemeral and lookup precedes the first send',async()=>{
 const f=fixture({lookup:false});try{
  const controller=new AbortController();const pending=f.api.request('/v1/admin/metadata/settings','PUT',{expectedRevision:1,values:{tmdbReadToken:'top-secret-value',openSubtitlesPassword:'secret-password',enabled:true}},controller.signal);pending.catch(()=>{});
  for(let n=0;n<30&&!f.calls.some(call=>call.method==='PUT');n++)await new Promise(resolve=>setTimeout(resolve,2));
  const entry=f.store.state.entries[0];assert.ok(entry.requiresReentry);assert.deepEqual(entry.secretFields,['values.tmdbReadToken','values.openSubtitlesPassword']);assert.ok(!JSON.stringify(f.store.state).includes('top-secret-value'));assert.ok(!JSON.stringify(f.store.state).includes('secret-password'));
  const write=f.calls.find(call=>call.method==='PUT')!;assert.equal(write.body.values.tmdbReadToken,'top-secret-value');assert.ok(f.calls.findIndex(call=>call.path.startsWith('/v1/user-mutations/')&&!call.path.endsWith('policy'))<f.calls.indexOf(write));
  controller.abort();await assert.rejects(pending);
 }finally{f.api.getUserMutationCoordinator()?.dispose();f.coordinator.dispose();}
});

test('the own-account Stop alias has exact suffix matching and durable custody without renderer evidence',async()=>{
 const f=fixture();try{await f.api.request('/v1/playback/controls/ps:stop','POST',{generation:1});const write=f.calls.find(call=>call.path==='/v1/playback/controls/ps:stop')!;assert.ok(write.headers.get('Idempotency-Key'));assert.deepEqual(write.body,{generation:1});assert.equal(userMutationRoute('/v1/playback/controls/ps:pause','POST'),undefined);assert.equal(userMutationRoute('/v1/playback/controls/ps','POST'),undefined);}finally{f.coordinator.dispose();}
});

test('HTTP-date Retry-After uses the calibrated server clock and preserves the floor',async()=>{
 const f=fixture({refuse:true,retryDate:true});try{const response=await f.api.requestRaw('/v1/items/item/personal-state','PUT',{body:{operationId:'original',expectedRevision:2}});assert.ok(Number(response.headers['retry-after'])>=119);assert.ok(Number(response.headers['retry-after'])<=120);}finally{f.coordinator.dispose();}
});
test('a restarted secret intent looks up first, requires explicit re-entry and keeps its original key and body',async()=>{
 const f=fixture({lookup:false});let restarted:ReturnType<HttpLocalApi['configureUserMutations']>|undefined;
 const raw={expectedRevision:1,values:{tmdbReadToken:'original-secret',enabled:true}};
 try{
  const cancel=new AbortController(),pending=f.api.request('/v1/admin/metadata/settings','PUT',raw,cancel.signal);pending.catch(()=>{});
  for(let n=0;n<50&&!f.calls.some(call=>call.method==='PUT');n++)await new Promise(resolve=>setTimeout(resolve,2));
  cancel.abort();await assert.rejects(pending);f.coordinator.dispose();await new Promise(resolve=>setTimeout(resolve,5));
  const key=f.store.state.entries[0].key,writes=f.calls.filter(call=>call.method==='PUT').length;
  f.api.setUserMutationSession(undefined);f.api.setUserMutationSession(session);
  f.store.state.entries[0].dueMs=0;delete f.store.state.entries[0].leaseOwner;delete f.store.state.entries[0].leaseUntilMs;
  restarted=f.api.configureUserMutations(f.store);
  for(let n=0;n<50&&f.store.state.entries[0].state!=='review';n++)await new Promise(resolve=>setTimeout(resolve,2));
  assert.equal(f.store.state.entries[0].errorCode,'secret_reentry_required');assert.equal(f.calls.filter(call=>call.method==='PUT').length,writes);
  await assert.rejects(f.api.reenterUserMutationSecrets(key,{...raw,expectedRevision:2}),/idempotency_conflict/);
  const againCancel=new AbortController(),again=f.api.reenterUserMutationSecrets(key,raw,againCancel.signal);again.catch(()=>{});
  for(let n=0;n<50&&f.calls.filter(call=>call.method==='PUT').length===writes;n++)await new Promise(resolve=>setTimeout(resolve,2));
  const write=f.calls.filter(call=>call.method==='PUT').at(-1)!;assert.equal(write.headers.get('Idempotency-Key'),key);assert.deepEqual(write.body,raw);assert.ok(!JSON.stringify(f.store.state).includes('original-secret'));
  againCancel.abort();await assert.rejects(again);
 }finally{restarted?.dispose();f.coordinator.dispose();}
});

test('mounted console retains its bounded validator and original conflict revision',async()=>{
 const f=fixture({refuse:true});try{await assert.rejects(f.api.requestConsole('/v1/admin/console/settings','PATCH',{idempotencyKey:'original',expectedRevision:3,values:{}},new AbortController().signal),error=>{assert.equal((error as any).status,412);assert.equal((error as any).currentRevision,9);assert.equal((error as any).code,'revision_mismatch');return true;});}finally{f.coordinator.dispose();}
});

const until=async(check:()=>boolean)=>{for(let n=0;n<100&&!check();n++)await new Promise(resolve=>setTimeout(resolve,2));assert.ok(check(),'expected coordinator transition');};
for(const path of ['/v1/items/item/personal-state','/v1/items/personal-state:batch','/v1/containers/show/show-1/personal-state','/v1/containers/show/show-1/personal-state/undo','/v1/playlists/list/entries','/v1/libraries/library/reading/mark-all'])test('lost applied response recovers only a compact marker for '+path,async()=>{
 const f=fixture({lostResponse:true});const cancel=new AbortController();
 try{
  const method=path.endsWith('/undo')||path.endsWith('/entries')||path.endsWith('/mark-all')?'POST':'PUT';
  const body=path.endsWith(':batch')?{operationId:'original',items:[{itemId:'item',expectedRevision:7,favorite:true}]}:path.endsWith('/undo')?{expectedRevision:7,undoToken:'original-undo'}:path.includes('/containers/')?{expectedRevision:7,favorite:true}:path.endsWith('/entries')?{operationId:'original',expectedRevision:7,itemIds:['item']}:path.endsWith('/mark-all')?{operationId:'original',expectedRevision:{catalog:7,viewer:3},viewerFence:'fence'}:{operationId:'original',expectedRevision:7,favorite:true};
  const pending=f.api.requestRaw(path,method,{body,signal:cancel.signal});pending.catch(()=>{});
  await until(()=>f.store.state.entries[0]?.attempts===1&&!f.store.state.entries[0]?.leaseOwner);
  const original=copy(f.store.state.entries[0]);
  f.store.state.entries[0].dueMs=0;await f.coordinator.reconcile();
  const response=await pending;
  assert.equal(response.status,200);assert.equal(response.headers['portico-mutation-recovered'],'true');
  assert.equal((response.body as any).mutationReceipt.key,original.key);assert.equal((response.body as any).mutationReceipt.operation,original.operation);
  assert.deepEqual(Object.keys(response.body as object),['mutationReceipt']);
  assert.equal(f.calls.filter(call=>call.method===method).length,1);assert.equal(f.calls.filter(call=>call.method==='GET'&&!call.path.startsWith('/v1/user-mutations/')).length,0);
  assert.equal(f.store.state.entries[0].state,'confirmed');assert.equal(f.store.state.entries[0].body,undefined);
 }finally{cancel.abort();f.coordinator.dispose();}
});
test('malformed 503 keeps immutable pending custody, Retry-After floor and looks up before retry',async()=>{
 const f=fixture({malformed503:true}),cancel=new AbortController();
 try{
  const pending=f.api.requestRaw('/v1/items/item/personal-state','PUT',{body:{operationId:'original',expectedRevision:7},signal:cancel.signal});pending.catch(()=>{});
  await until(()=>f.store.state.entries[0]?.attempts===1&&!f.store.state.entries[0]?.leaseOwner);
  const entry=copy(f.store.state.entries[0]);assert.equal(entry.state,'pending');assert.ok(entry.dueMs>=Date.now()+119000);assert.equal((entry.body as any).expectedRevision,7);
  assert.equal(f.calls.filter(call=>call.path.startsWith('/v1/user-mutations/')&&!call.path.endsWith('/policy')).length,0);
  f.store.state.entries[0].dueMs=0;await f.coordinator.reconcile();
  const response=await pending;assert.equal((response.body as any).mutationReceipt.key,entry.key);
  assert.equal(f.calls.filter(call=>call.method==='PUT').length,1);assert.equal(f.calls.filter(call=>call.path==='/v1/user-mutations/'+entry.key).length,1);
 }finally{cancel.abort();f.coordinator.dispose();}
});
test('preference recovery reads the captured device class after erasing the confirmed request',async()=>{
 const f=fixture();try{
  const body={idempotencyKey:'original',scope:'profile',deviceClass:'television',expectedRevision:7,values:{}};
  await f.api.request('/v1/preferences','PATCH',body);
  assert.equal(f.store.state.entries[0].body,undefined);
  f.api.configureUserMutations(f.store);
  await f.api.awaitUserMutationsReady();
  assert.deepEqual(await f.api.request('/v1/preferences','PATCH',body),{current:true});
  assert.equal(f.calls.find(call=>call.path==='/v1/preferences'&&call.method==='GET')?.query,'?deviceClass=television');
  assert.equal(f.calls.filter(call=>call.method==='PATCH').length,1);
  assert.ok(f.api.getUserMutationCoordinator());
 }finally{f.api.getUserMutationCoordinator()?.dispose();f.coordinator.dispose();}
});

test('public startup readiness waits for the durable snapshot before restore owners inspect it',async()=>{
 const store=new Store(),read=store.read.bind(store);let release!:()=>void;
 const gate=new Promise<void>(resolve=>{release=resolve;});store.read=async()=>{await gate;return read();};
 const api=new HttpLocalApi('http://server.test',session.accessToken,async()=>{throw new Error('startup must not send');});api.setUserMutationSession(session);const coordinator=api.configureUserMutations(store);
 try{
  let ready=false;const waiting=api.awaitUserMutationsReady().then(()=>{ready=true;});
  await new Promise(resolve=>setImmediate(resolve));assert.equal(ready,false);
  release();await waiting;assert.equal(ready,true);assert.equal(api.getUserMutationCoordinator(),coordinator);
 }finally{release();coordinator.dispose();}
});

for(const acceptedBody of ['html','truncated','stream','oversized'] as const)test('accepted '+acceptedBody+' response reconciles same key through current typed read, never undefined',async()=>{
 const f=fixture({acceptedBody});try{
  const response=await f.api.requestRaw('/v1/preferences','PATCH',{body:{idempotencyKey:'original',scope:'profile',deviceClass:'television',expectedRevision:7,values:{}}});
  assert.deepEqual(response.body,{current:true});assert.equal(response.headers['portico-mutation-recovered'],'true');assert.equal(f.store.state.entries[0].state,'confirmed');
  assert.equal(f.calls.filter(call=>call.method==='PATCH').length,1);assert.equal(f.calls.filter(call=>call.path==='/v1/user-mutations/'+f.store.state.entries[0].key).length,1);assert.equal(f.calls.find(call=>call.method==='GET'&&call.path==='/v1/preferences')?.query,'?deviceClass=television');
 }finally{f.coordinator.dispose();}
});
for(const malformedLookup of ['html','typed'] as const)test('malformed accepted '+malformedLookup+' receipt stays pending until a valid same-key lookup',async()=>{
 const options={acceptedBody:'html' as const,malformedLookup:malformedLookup as 'html'|'typed'|undefined},f=fixture(options),cancel=new AbortController();
 try{
  const pending=f.api.requestRaw('/v1/items/item/personal-state','PUT',{body:{operationId:'original',expectedRevision:7,favorite:true},signal:cancel.signal});pending.catch(()=>{});
  await until(()=>f.store.state.entries[0]?.attempts===1&&!f.store.state.entries[0]?.leaseOwner);
  const original=copy(f.store.state.entries[0]);assert.equal(original.state,'pending');assert.ok(original.dueMs>=Date.now()+119000);assert.ok(original.body);assert.equal(original.terminalProof,undefined);
  options.malformedLookup=undefined;f.store.state.entries[0].dueMs=0;await f.coordinator.reconcile();const response=await pending;
  assert.equal((response.body as any).mutationReceipt.key,original.key);assert.equal(response.headers['portico-mutation-recovered'],'true');assert.equal(f.calls.filter(call=>call.method==='PUT').length,1);assert.equal(f.calls.filter(call=>call.path==='/v1/user-mutations/'+original.key).length,2);
 }finally{cancel.abort();f.coordinator.dispose();}
});
test('typed HTTP 400 refusal keeps its terminal proof and immutable draft',async()=>{
 const f=fixture({refuse400:true});try{
  const response=await f.api.requestRaw('/v1/items/item/personal-state','PUT',{body:{operationId:'original',expectedRevision:7,favorite:true}});
  assert.equal(response.status,400);assert.deepEqual(response.body,{error:{code:'invalid_request'}});assert.equal(f.store.state.entries[0].state,'review');assert.equal(f.store.state.entries[0].terminalProof?.httpStatus,400);assert.equal((f.store.state.entries[0].body as any).expectedRevision,7);
 }finally{f.coordinator.dispose();}
});
test('own-account Stop alias retains generation one receipt while a new explicit generation two sends independently',async()=>{
 const f=fixture();try{
  await f.api.requestRaw('/v1/playback/controls/ps:stop','POST',{body:{generation:1}});assert.equal(f.store.state.entries[0].body,undefined);
  await f.api.requestRaw('/v1/playback/controls/ps:stop','POST',{body:{generation:2}});
  const writes=f.calls.filter(call=>call.path==='/v1/playback/controls/ps:stop');assert.equal(writes.length,2);assert.notEqual(writes[0].headers.get('Idempotency-Key'),writes[1].headers.get('Idempotency-Key'));assert.deepEqual(writes.map(call=>call.body),[{generation:1},{generation:2}]);assert.deepEqual(f.store.state.entries.map(entry=>entry.intentId),['stop:ps:1','stop:ps:2']);
  for(const generation of [0,-1,2147483648])await assert.rejects(f.api.requestRaw('/v1/playback/controls/ps:stop','POST',{body:{generation}}),UserMutationError);
 }finally{f.coordinator.dispose();}
});
for(const guideUrl of ['https://name:credential-value@guide.test/guide.xml','https://guide.test/guide.xml?password=credential-value','https://guide.test/guide.xml?%74oken=credential-value'])test('live guide credential URL never persists and restarts with original key: '+new URL(guideUrl).hostname,async()=>{
 const f=fixture({lookup:false}),raw={operationId:'guide-original',expectedRevision:1,settings:{remote:{guideUrl,kind:'m3u',refreshMs:3600000}}};let restarted:ReturnType<HttpLocalApi['configureUserMutations']>|undefined;
 try{
  const cancel=new AbortController(),pending=f.api.request('/v1/admin/live-sources/source/configuration','PUT',raw,cancel.signal);pending.catch(()=>{});
  await until(()=>f.calls.some(call=>call.method==='PUT'));cancel.abort();await assert.rejects(pending);f.coordinator.dispose();await new Promise(resolve=>setTimeout(resolve,2));
  const key=f.store.state.entries[0].key;assert.deepEqual(f.store.state.entries[0].secretFields,['settings.remote.guideUrl']);assert.equal((f.store.state.entries[0].body as any).settings.remote.guideUrl,undefined);assert.equal(JSON.stringify(f.store.state).includes('credential-value'),false);assert.equal(JSON.stringify(f.store.state).includes(guideUrl),false);
  await assert.rejects(f.api.request('/v1/admin/live-sources/source/configuration','PUT',{...raw,settings:{remote:{...raw.settings.remote,guideUrl:guideUrl.replace('credential-value','changed')}}}),/idempotency_conflict/);
  f.api.setUserMutationSession(undefined);f.api.setUserMutationSession(session);f.store.state.entries[0].dueMs=0;delete f.store.state.entries[0].leaseOwner;delete f.store.state.entries[0].leaseUntilMs;restarted=f.api.configureUserMutations(f.store);
  await until(()=>f.store.state.entries[0].errorCode==='secret_reentry_required');const before=f.calls.filter(call=>call.method==='PUT').length;
  const againCancel=new AbortController(),again=f.api.reenterUserMutationSecrets(key,raw,againCancel.signal);again.catch(()=>{});await until(()=>f.calls.filter(call=>call.method==='PUT').length>before);
  const write=f.calls.filter(call=>call.method==='PUT').at(-1)!;assert.equal(write.headers.get('Idempotency-Key'),key);assert.deepEqual(write.body,{...raw,operationId:key});assert.equal(JSON.stringify(f.store.state).includes('credential-value'),false);againCancel.abort();await assert.rejects(again);
 }finally{restarted?.dispose();f.coordinator.dispose();}
});
test('guide URLs stay ephemeral even when provider credentials cannot be recognized',async()=>{
 const f=fixture({lookup:false}),cancel=new AbortController();try{
  const guideUrl='https://guide.test/private-provider-path/guide.xml?language=en';
  const pending=f.api.requestRaw('/v1/admin/live-sources/source/configuration','PUT',{body:{operationId:'plain-guide',expectedRevision:1,settings:{remote:{guideUrl}}},signal:cancel.signal});pending.catch(()=>{});
  await until(()=>f.calls.some(call=>call.method==='PUT'));cancel.abort();await assert.rejects(pending);
  const write=f.calls.find(call=>call.method==='PUT')!;assert.equal(write.body.settings.remote.guideUrl,guideUrl);assert.equal(f.store.state.entries[0].requiresReentry,true);assert.equal(JSON.stringify(f.store.state).includes(guideUrl),false);
 }finally{cancel.abort();f.coordinator.dispose();}
});

for(const url of ['https://guide.test/guide.xml?password=never-store','https://guide.test/guide.xml?token=never-store','https://name:never-store@guide.test/guide.xml'])test('unhandled credential URL is explicitly not queued',async()=>{
 const f=fixture(),body={expectedRevision:1,downloadUrl:url};try{await assert.rejects(f.api.request('/v1/admin/metadata/settings','PUT',body),/mutation_not_queued/);assert.equal(f.store.state.entries.length,0);assert.equal(f.calls.filter(call=>call.method==='PUT').length,0);assert.equal(body.downloadUrl,url);}finally{f.coordinator.dispose();}
});
