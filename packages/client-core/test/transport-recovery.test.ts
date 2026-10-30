import test from 'node:test';
import assert from 'node:assert/strict';
import {HttpLocalApi, ApiError, RequestTimeoutError, type AuthRecovery} from '../src/index.ts';
import {RouteConnection} from '../src/route-connection.ts';

const tick = () => new Promise<void>(resolve => setImmediate(resolve));
function held<T>() {let resolve!: (value:T)=>void;const promise=new Promise<T>(r=>{resolve=r;});return {promise,resolve};}
const origin='http://192.168.1.20:32500';
const json=(status:number,body:unknown)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});

test('aborting an auth token waiter leaves shared renewal running and never dispatches the abandoned request',async()=>{
 const token=held<string>();let sends=0,tokenCalls=0;
 const api=new HttpLocalApi(origin,'old',async()=>{sends++;return json(200,{ok:true});});
 api.setAuthRecovery({token:()=>{tokenCalls++;return token.promise;},recover:async()=>undefined});
 const cancelled=new AbortController();let result:unknown;
 const first=api.request('/v1/items','GET',undefined,cancelled.signal).catch(e=>{result=e;});
 const second=api.request('/v1/items');
 try{
  cancelled.abort();await tick();
  assert.equal((result as Error)?.name,'AbortError');assert.equal(sends,0);assert.equal(tokenCalls,2);
  token.resolve('fresh');await Promise.all([first,second]);assert.equal(sends,1);
 }finally{token.resolve('fresh');await Promise.allSettled([first,second]);}
});

test('auth token, rejected-token recovery and permission waiters all obey the caller deadline',async t=>{
 t.mock.timers.enable({apis:['setTimeout']});
 for(const step of ['token','recover','permission'] as const){
  const pending=held<string|undefined|boolean>();let sends=0,entered=false,result:unknown;
  const api=new HttpLocalApi(origin,'old',async()=>{sends++;return json(step==='recover'?401:403,{error:{code:'refused'}});});
  const auth:AuthRecovery={token:async token=>{if(step==='token'){entered=true;return pending.promise as Promise<string>;}return token;},
   recover:async()=>{entered=true;return pending.promise as Promise<string|undefined>;},
   permissionDenied:async()=>{entered=true;return pending.promise as Promise<boolean>;}};
  api.setAuthRecovery(auth);
  const task=api.request('/v1/items').catch(e=>{result=e;});
  try{
   await tick();assert.equal(entered,true);t.mock.timers.tick(30000);await tick();
   assert.ok(result instanceof RequestTimeoutError,step);assert.equal(sends,step==='token'?0:1);
  }finally{pending.resolve(step==='permission'?false:'fresh');await task;}
  assert.equal(sends,step==='token'?0:1,'late shared completion cannot dispatch a retry');
 }
});

test('an explicitly cancelled auth recovery or permission waiter stays cancelled rather than returning HTTP failure',async()=>{
 for(const status of [401,403]){
  const blocked=held<string|boolean|undefined>();let entered=false,result:unknown,sends=0;
  const api=new HttpLocalApi(origin,'old',async()=>{sends++;return json(status,{error:{code:'refused'}});});
  api.setAuthRecovery({token:async t=>t,recover:async()=>{entered=true;return blocked.promise as Promise<string|undefined>;},permissionDenied:async()=>{entered=true;return blocked.promise as Promise<boolean>;}});
  const c=new AbortController(),task=api.request('/v1/items','GET',undefined,c.signal).catch(e=>{result=e;});
  try{await tick();assert.equal(entered,true);c.abort();await tick();assert.equal((result as Error)?.name,'AbortError');assert.equal(sends,1);}
  finally{blocked.resolve(status===401?'fresh':true);await task;}
  assert.equal(sends,1);
 }
});

test('explicit retry policy overrides HTTP 503, including never and other-request classes',async()=>{
 for(const [policy,retryable] of [[{retry:'never'},false],[{retry:'after_refresh'},false],[{retry:'after_sign_in'},false],[{retry:'same_request'},true],[{},true],[{retryable:false},false],[{retryable:true,retry:'never'},true],[{retry:null},false],[{retryable:null},false]] as const){
  const api=new HttpLocalApi(origin,'',async()=>json(503,{error:{code:'unavailable',...policy}}));
  await assert.rejects(api.request('/v1/items'),(e:unknown)=>e instanceof ApiError&&e.status===503&&e.retryable===retryable,JSON.stringify(policy));
 }
});

test('cancellation while reading an HTTP error body preserves abort and deadline classification',async t=>{
 t.mock.timers.enable({apis:['setTimeout']});
 for(const deadline of [false,true]){
  let parsing=false;
  const c=new AbortController();
  const api=new HttpLocalApi(origin,'',async(_input,init)=>({status:503,ok:false,headers:new Headers(),json:()=>new Promise((_resolve,reject)=>{
   parsing=true;init!.signal!.addEventListener('abort',()=>reject(Object.assign(new Error('body stopped'),{name:'AbortError'})),{once:true});
  })}) as Response);
  const task=api.request('/v1/items','GET',undefined,c.signal).then(()=>undefined,e=>e);
  await tick();assert.equal(parsing,true);
  if(deadline)t.mock.timers.tick(30000);else c.abort();
  const result=await task;
  if(deadline)assert.ok(result instanceof RequestTimeoutError);else assert.equal(result?.name,'AbortError');
  assert.equal(result instanceof ApiError,false);
 }
});

test('temporary HTTP wrappers subscribe upstream only while they have route listeners',async()=>{
 const pin={serverId:'srv',publicKey:'A'.repeat(43),fingerprint:'F'.repeat(43)};
 const a=new RouteConnection({pin,logicalOrigin:origin,memory:{paired:[origin]},probe:async()=>pin});
 const b=new RouteConnection({pin,logicalOrigin:origin,memory:{paired:[origin]},probe:async()=>pin});
 let activeA=0,activeB=0;
 const instrument=(connection:RouteConnection,change:(n:number)=>void)=>{
  const original=connection.subscribe;connection.subscribe=listener=>{change(1);const off=original(listener);return()=>{change(-1);off();};};
 };
 instrument(a,n=>{activeA+=n;});instrument(b,n=>{activeB+=n;});
 try{
  for(let i=0;i<12;i++)new HttpLocalApi(origin).useRoutes(a);
  assert.equal(activeA,0);
  const api=new HttpLocalApi(origin);api.useRoutes(a);let notifications=0;
  const off1=api.subscribeRoute(()=>{notifications++;}),off2=api.subscribeRoute(()=>{notifications++;});
  assert.equal(activeA,1);await a.resolve();assert.ok(notifications>=2);
  off1();assert.equal(activeA,1);api.useRoutes(b);assert.equal(activeA,0);assert.equal(activeB,1);
  const before=notifications;await b.resolve();assert.ok(notifications>before);
  off2();assert.equal(activeB,0);const off3=api.subscribeRoute(()=>{});assert.equal(activeB,1);off3();assert.equal(activeB,0);
 }finally{a.dispose();b.dispose();}
});

test('cancelled routed GETs detach from gateway and socket recovery without stopping another waiter',async()=>{
 for(const failure of ['gateway','socket'] as const){
  const proof=held<void>();let recovering=false,probes=0,sends=0,answer:unknown;
  const pin={serverId:'srv',publicKey:'A'.repeat(43),fingerprint:'F'.repeat(43)};
  const connection=new RouteConnection({pin,logicalOrigin:origin,memory:{paired:[origin]},probe:async()=>{if(++probes>1){recovering=true;await proof.promise;}return pin;},fetcher:async()=>{
   sends++;if(sends===1){if(failure==='socket')throw new TypeError('offline');return new Response('gateway',{status:503});}return new Response('ok');
  }});
  const c=new AbortController();let task:Promise<unknown>|undefined,other:Promise<Response>|undefined;
  try{
   await connection.resolve();task=connection.fetch(origin+'/v1/items',{signal:c.signal}).catch(e=>{answer=e;});
   await tick();assert.equal(recovering,true);other=connection.fetch(origin+'/v1/items');
   c.abort();await tick();assert.equal((answer as {code?:string})?.code,'cancelled');assert.equal(sends,1);
   proof.resolve();await task;assert.equal(await(await other).text(),'ok');assert.equal(sends,2);
  }finally{proof.resolve();await Promise.allSettled([task,other]);connection.dispose();}
 }
});
