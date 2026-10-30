import test from 'node:test';
import assert from 'node:assert/strict';
import {LocalSessionRefresher, type PermissionReconciliation} from '../src/session-refresh.ts';
import {parseLocalSession} from '../src/local-session.ts';
import {SessionRefreshError, type RememberedServer} from '../src/server-connections.ts';
import type {LocalSession} from '../src/index.ts';
import {FreshnessHub,dependsOn} from '../src/freshness-hub.ts';

const original=()=>parseLocalSession({accessToken:'permission_test_access',deviceId:'device',installationId:'installation',
  sessionFamilyId:'family',tokenGeneration:'1',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2100-01-01T00:00:00Z',
  viewer:{authority:'local',accountId:'account',profileId:'profile',serverId:'server',role:'owner'}},{},{stored:true});
const remembered=(session:LocalSession):RememberedServer=>({version:'1',selectionId:'selection',logicalOrigin:'https://portico.test',session,
  pin:{serverId:'server',publicKey:'k'.repeat(43),fingerprint:'f'.repeat(43)},routes:{paired:[]}});
function deferred<T>() {let resolve!:(value:T)=>void;const promise=new Promise<T>(yes=>{resolve=yes;});return {promise,resolve};}
async function flush() {for(let i=0;i<40;i++)await Promise.resolve();}
function harness(t:import('node:test').TestContext,options:{onRotated?:(ref:LocalSessionRefresher)=>void;refresh?:(record:RememberedServer)=>LocalSession;refreshError?:unknown}={}) {
  let record:RememberedServer|undefined=remembered(original()),now=1000,role='owner',readCount=0,gets=0,changes=0,commits=0,signedOut=0;
  let reply:unknown,readError:unknown,writeError:unknown,getError:unknown,readHook:((n:number)=>void)|undefined,updateHook:(()=>void)|undefined;
  let network:Promise<unknown>|undefined,encryption:Promise<void>|undefined,wrapGuard=false;
  const timers=new Map<number,()=>void>();let timerId=0;
  const calls:{path:string;signal?:AbortSignal}[]=[],rotated:LocalSession[]=[];
  const store={read:async()=>{readCount++;readHook?.(readCount);if(readError)throw readError;return structuredClone(record);},
    change:async(update:(v:RememberedServer|undefined)=>RememberedServer|undefined,guard?:()=>void)=>{
      changes++;updateHook?.();const before=structuredClone(record),next=update(before);if(next===before)return;
      await encryption;
      try{guard?.();}catch(error){if(wrapGuard)throw new Error('wrapped transaction guard');throw error;}
      if(writeError)throw writeError;record=structuredClone(next);commits++;
    }};
  const ref=new LocalSessionRefresher({session:original(),env:{storage:store},now:()=>now,random:()=>0,
    setTimer:fn=>{timers.set(++timerId,fn);return timerId;},clearTimer:id=>{timers.delete(id as number);},
    api:{request:async<T>(path:string,_method?:string,_body?:unknown,signal?:AbortSignal)=>{
      gets++;calls.push({path,signal});if(getError)throw getError;
      return (network?await network:reply??{viewer:{...original().viewer,role}}) as T;
    },setAccessToken:()=>{},setAuthRecovery:()=>{}},
    refresh:async()=>{const next=options.refresh?.(record!);if(!next)throw options.refreshError??new SessionRefreshError('unavailable','try later',false);record={...record!,session:next};return next;},
    onRotated:session=>{rotated.push(session);options.onRotated?.(ref);},onSignedOut:()=>{signedOut++;}});
  t.after(()=>ref.stop());
  return {ref,calls,rotated,store,timers,get record(){return record;},set record(value:RememberedServer|undefined){record=value;},
    counts:()=>({reads:readCount,gets,changes,commits,signedOut}),role:(value:string)=>{role=value;},reply:(value:unknown)=>{reply=value;},
    errors:(which:'read'|'write'|'get',value:unknown)=>{if(which==='read')readError=value;else if(which==='write')writeError=value;else getError=value;},
    readHook:(fn:(n:number)=>void)=>{readHook=fn;},updateHook:(fn:()=>void)=>{updateHook=fn;},network:(p:Promise<unknown>)=>{network=p;},
    encryption:(p:Promise<void>)=>{encryption=p;},wrapGuard:()=>{wrapGuard=true;},advance:(ms:number)=>{now+=ms;},now:()=>now};
}
function failed(result:PermissionReconciliation,error:unknown,retryable=true) {
  assert.equal(result.kind,'failed');if(result.kind==='failed'){assert.equal(result.error,error);assert.equal(result.retryable,retryable);}
}
test('matching role confirmation proves the final protected selection and never writes or calls rotation',async t=>{
  const h=harness(t);assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'confirmed-unchanged'});
  assert.deepEqual(h.counts(),{reads:2,gets:1,changes:0,commits:0,signedOut:0});assert.equal(h.rotated.length,0);
  assert.equal(await h.ref.permissionDenied(),false);
});
test('a changed supported role commits once before handover and preserves exact bindings',async t=>{
  const h=harness(t,{onRotated:()=>{assert.equal(h.record!.session.viewer.role,'admin');assert.equal(h.counts().commits,1);}});h.role('admin');
  assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'adopted',role:'admin'});assert.equal(h.ref.current().viewer.role,'admin');
  assert.equal(h.record!.selectionId,'selection');assert.equal(h.record!.session.accessToken,original().accessToken);assert.equal(h.rotated.length,1);
});
test('legacy wrapper is true only for an actual committed role change',async t=>{
  const h=harness(t);h.role('member');assert.equal(await h.ref.permissionDenied(),true);assert.equal(h.rotated.length,1);
  assert.equal(await h.ref.permissionDenied(),false);assert.equal(h.counts().commits,1);
});
test('undefined original selectionId matches exactly but a later defined selection is not adopted',async t=>{
  const h=harness(t);h.record={...h.record!,selectionId:undefined};h.role('admin');assert.equal((await h.ref.reconcilePermissions()).kind,'adopted');
  const moved=harness(t);moved.record={...moved.record!,selectionId:undefined};moved.role('admin');moved.updateHook(()=>{moved.record={...moved.record!,selectionId:'new-selection'};});
  assert.deepEqual(await moved.ref.reconcilePermissions(),{kind:'superseded',reason:'selection-moved'});assert.equal(moved.counts().commits,0);assert.equal(moved.rotated.length,0);
});
test('a stored updater no-op cannot produce the historical false true wrapper result',async t=>{
  const h=harness(t);h.role('admin');h.updateHook(()=>{h.record={...h.record!,selectionId:'other'};});
  assert.equal(await h.ref.permissionDenied(),false);assert.equal(h.ref.current().viewer.role,'owner');assert.equal(h.rotated.length,0);assert.equal(h.counts().commits,0);
});
test('every protected principal/family/token/device/installation/pin change supersedes instead of adopting',async t=>{
  const changes:((r:RememberedServer)=>RememberedServer)[]=[
    r=>({...r,selectionId:'another'}),r=>({...r,pin:{...r.pin,serverId:'another'}}),
    ...(['serverId','accountId','profileId','authority','role'] as const).map(key=>(r:RememberedServer)=>({...r,session:{...r.session,viewer:{...r.session.viewer,[key]:key==='authority'?'hosted':'another'}}})),
    ...(['sessionFamilyId','accessToken','deviceId','installationId'] as const).map(key=>(r:RememberedServer)=>({...r,session:{...r.session,[key]:'another'}})),
  ];
  for(const change of changes){const h=harness(t),gate=deferred<unknown>();h.role('admin');h.network(gate.promise);const pending=h.ref.reconcilePermissions();await flush();assert.equal(h.counts().gets,1);
    h.record=change(h.record!);gate.resolve({viewer:{...original().viewer,role:'admin'}});assert.deepEqual(await pending,{kind:'superseded',reason:'selection-moved'});
    assert.equal(h.rotated.length,0);assert.equal(h.counts().commits,0);h.ref.stop();}
});
test('same-role success still refuses a selection that moved during its final protected read',async t=>{
  const h=harness(t);h.readHook(n=>{if(n===2)h.record={...h.record!,selectionId:'moved'};});
  assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'superseded',reason:'selection-moved'});assert.equal(h.counts().changes,0);
});
test('LOCAL WEB2: genuine protected read, encryption/write, transaction and AbortError failures retain the original error',async t=>{
  for(const which of ['read','write'] as const)for(const error of [new Error('busy store'),Object.assign(new Error('real transaction abort'),{name:'AbortError'})]){
    const h=harness(t);h.role('admin');h.errors(which,error);failed(await h.ref.reconcilePermissions(),error);assert.equal(h.rotated.length,0);assert.equal(h.counts().commits,0);
    if(which==='read')assert.equal(h.counts().gets,0);h.ref.stop();
  }
});
test('HTTP failures preserve error metadata without clearing credentials or inventing ended state',async t=>{
  for(const status of [401,403,429,503]){const h=harness(t),error=Object.assign(new Error('server response'),{status,retryAfterSeconds:7200});h.errors('get',error);
    failed(await h.ref.reconcilePermissions(),error,status===429||status===503);assert.equal(h.counts().signedOut,0);assert.equal(h.ref.getSnapshot().phase,'active');assert.ok(h.record);h.ref.stop();}
});
test('malformed/foreign viewer proof fails without role adoption; unsupported roles are terminal for their ticket',async t=>{
  const h=harness(t);h.reply({viewer:{...original().viewer,accountId:'foreign'}});const malformed=await h.ref.reconcilePermissions();assert.equal(malformed.kind,'failed');if(malformed.kind==='failed')assert.equal(malformed.retryable,true);
  h.reply({viewer:{...original().viewer,role:'account'}});const unsupported=await h.ref.reconcilePermissions();assert.equal(unsupported.kind,'failed');if(unsupported.kind==='failed')assert.equal(unsupported.retryable,false);
  assert.equal(await h.ref.permissionDenied(),false);assert.equal(h.counts().signedOut,0);assert.equal(h.rotated.length,0);
});
test('pre-aborted, stopped and absent protected admissions start no permission request',async t=>{
  const h=harness(t),c=new AbortController();c.abort();assert.deepEqual(await h.ref.reconcilePermissions({signal:c.signal}),{kind:'superseded',reason:'interest-cancelled'});assert.equal(h.counts().reads,0);
  h.ref.stop();assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'superseded',reason:'owner-stopped'});assert.equal(h.counts().gets,0);assert.equal(h.timers.size,0);
  const gone=harness(t);gone.record=undefined;assert.deepEqual(await gone.ref.reconcilePermissions(),{kind:'superseded',reason:'selection-moved'});assert.equal(gone.counts().gets,0);
});
test('a cancelled hint waiter cannot veto a surviving poll/403 borrower',async t=>{
  const h=harness(t),gate=deferred<unknown>(),c=new AbortController();h.network(gate.promise);h.role('admin');
  const hint=h.ref.reconcilePermissions({signal:c.signal}),legacy=h.ref.permissionDenied();await flush();assert.equal(h.counts().gets,1);
  c.abort();assert.deepEqual(await hint,{kind:'superseded',reason:'interest-cancelled'});assert.equal(h.calls[0].signal!.aborted,false);
  gate.resolve({viewer:{...original().viewer,role:'admin'}});assert.equal(await legacy,true);assert.equal(h.counts().commits,1);assert.equal(h.rotated.length,1);
});
test('abandoning every hint before commit vetoes the encrypted role write',async t=>{
  const h=harness(t),gate=deferred<void>(),c=new AbortController();h.role('admin');h.encryption(gate.promise);
  const pending=h.ref.reconcilePermissions({signal:c.signal});await flush();assert.equal(h.counts().changes,1);c.abort();
  assert.deepEqual(await pending,{kind:'superseded',reason:'interest-cancelled'});gate.resolve();await flush();assert.equal(h.counts().commits,0);assert.equal(h.record!.session.viewer.role,'owner');assert.equal(h.rotated.length,0);
});
test('owner stop during encryption vetoes late commit, including a storage adapter that wraps the guard sentinel',async t=>{
  for(const wrap of [false,true]){const h=harness(t),gate=deferred<void>();h.role('admin');h.encryption(gate.promise);if(wrap)h.wrapGuard();
    const pending=h.ref.reconcilePermissions();await flush();assert.equal(h.counts().changes,1);h.ref.stop();assert.deepEqual(await pending,{kind:'superseded',reason:'owner-stopped'});
    gate.resolve();await flush();assert.equal(h.counts().commits,0);assert.equal(h.rotated.length,0);assert.equal(h.record!.session.viewer.role,'owner');}
});
test('credential rotation supersedes the original permission proof without cancelling credential ownership',async t=>{
  const h=harness(t,{refresh:r=>parseLocalSession({...r.session,accessToken:'rotated_test_access',tokenGeneration:'2'},{},{stored:true})}),gate=deferred<unknown>();h.network(gate.promise);
  const pending=h.ref.reconcilePermissions();await flush();await h.ref.recover(original().accessToken);gate.resolve({viewer:{...original().viewer,role:'admin'}});
  assert.deepEqual(await pending,{kind:'superseded',reason:'session-moved'});assert.equal(h.ref.current().accessToken,'rotated_test_access');assert.equal(h.counts().commits,0);assert.equal(h.rotated.length,1);
});
test('synchronous handover retirement reports the committed adoption and installs no old-owner timer',async t=>{
  const h=harness(t,{onRotated:ref=>ref.stop()});h.role('admin');assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'adopted',role:'admin'});
  assert.equal(h.counts().commits,1);assert.equal(h.rotated.length,1);assert.equal(h.ref.getSnapshot().phase,'stopped');assert.equal(h.timers.size,0);
});
test('detailed deferred admission keeps a two-hour floor learned first by a legacy poll, without deadline inflation',async t=>{
  const h=harness(t),error=Object.assign(new Error('wait'),{status:429,retryAfterSeconds:7200});h.errors('get',error);const deadline=h.now()+7200000;
  assert.equal(await h.ref.permissionDenied(),false);h.errors('get',undefined);h.advance(3600001);
  assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'deferred',nextAllowedAt:deadline,reason:'server-floor'});assert.equal(h.counts().gets,1);
  assert.equal(await h.ref.permissionDenied(),false);assert.equal(h.counts().gets,2,'legacy admission retains its original one-hour cap');h.advance(1000);
  assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'deferred',nextAllowedAt:deadline,reason:'server-floor'});h.advance(3600000);
  assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'confirmed-unchanged'});assert.equal(h.counts().gets,3);
});
test('positive overflowing Retry-After saturates the detailed deadline instead of retrying immediately',async t=>{
  const h=harness(t),error=Object.assign(new Error('wait'),{status:429,retryAfterSeconds:Infinity});h.errors('get',error);failed(await h.ref.reconcilePermissions(),error);
  h.advance(1000);assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'deferred',nextAllowedAt:Number.MAX_VALUE,reason:'server-floor'});assert.equal(h.counts().gets,1);
});

test('a credential failure learned by the legacy owner retains its full floor for detailed permission admission',async t=>{
  const error=Object.assign(new SessionRefreshError('unavailable','try later',false),{retryAfterSeconds:7200});
  const h=harness(t,{refreshError:error}),deadline=h.now()+7200000;
  assert.equal(await h.ref.recover(original().accessToken),undefined);h.advance(3600001);
  assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'deferred',nextAllowedAt:deadline,reason:'server-floor'});
  assert.equal(h.counts().gets,0);h.advance(3600000);assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'confirmed-unchanged'});
});
test('only authoritative credential termination produces ended, including permission waiters in flight',async t=>{
  const error=new SessionRefreshError('revoked','ended',true),h=harness(t,{refreshError:error}),gate=deferred<unknown>();h.network(gate.promise);
  const pending=h.ref.reconcilePermissions();await flush();assert.equal(await h.ref.recover(original().accessToken),undefined);
  assert.deepEqual(await pending,{kind:'ended'});assert.deepEqual(await h.ref.reconcilePermissions(),{kind:'ended'});assert.equal(h.counts().signedOut,1);
  gate.resolve({viewer:{...original().viewer,role:'admin'}});await flush();assert.equal(h.counts().commits,0);assert.equal(h.rotated.length,0);
});
test('malformed protected state is failed rather than a confirmed no-op or invented sign-out',async t=>{
  const h=harness(t);h.record={...h.record!,selectionId:123 as any};
  const answer=await h.ref.reconcilePermissions();assert.equal(answer.kind,'failed');assert.equal(h.counts().gets,0);assert.equal(h.counts().signedOut,0);
});
test('LOCAL WEB2: a protected-write failure keeps interest debt, retries with backoff and clears only on real adoption',async t=>{
  const h=harness(t),error=Object.assign(new Error('original protected write failure'),{name:'AbortError'});h.role('admin');h.errors('write',error);
  const tasks=new Map<number,{at:number;run:()=>void}>();let next=0,attempts=0;const observed:unknown[]=[];
  const hub=new FreshnessHub({now:h.now,random:()=>0,setTimer:(run,ms)=>{tasks.set(++next,{at:h.now()+ms,run});return next;},clearTimer:id=>{tasks.delete(id as number);}});t.after(()=>hub.dispose());
  const interest=hub.interest({name:'permission-proof',dependencies:[dependsOn.settings('server')],read:async()=>{
    attempts++;const answer=await h.ref.reconcilePermissions();if(answer.kind==='failed'){observed.push(answer.error);return answer;}
    return answer.kind==='adopted'||answer.kind==='confirmed-unchanged'?{kind:'answered'}:{kind:'superseded'};
  }});
  const advance=async(ms:number)=>{const end=h.now()+ms;for(;;){const due=[...tasks].filter(([,v])=>v.at<=end).sort((a,b)=>a[1].at-b[1].at)[0];if(!due)break;tasks.delete(due[0]);h.advance(due[1].at-h.now());due[1].run();await flush();}h.advance(end-h.now());await flush();};
  interest.invalidate();await advance(400);assert.equal(attempts,1);assert.equal(interest.getState().owed,true);assert.equal(interest.getState().failures,1);assert.equal(observed[0],error);assert.equal(h.rotated.length,0);
  h.errors('write',undefined);await advance(1999);assert.equal(attempts,1);await advance(1);assert.equal(attempts,2);assert.equal(interest.getState().owed,false);assert.equal(h.rotated.length,1);assert.equal(h.counts().commits,1);
});
