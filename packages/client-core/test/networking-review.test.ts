import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {ApiError,HttpLocalApi,type LocalSession} from '../src/index.ts';
import {RouteConnection,sessionRoute} from '../src/route-connection.ts';
import {type ServerPin} from '../src/route-identity.ts';
import {parseRememberedServer,rememberNativeSession,restoreNativeSession,restoreExactNativeSession,rememberRotatedSession,prepareRoutedSession,commitRoutedSession,type RememberedServer,type ServerConnectionStorage} from '../src/server-connections.ts';
import {SetupCodeService,type SetupCodeJournal} from '../src/setup-code.ts';
import {SessionSelectionService} from '../src/session-selection.ts';
const tick=()=>new Promise<void>(r=>setTimeout(r,5));
async function until(check:()=>boolean){for(let i=0;i<120&&!check();i++)await tick();assert.ok(check(),'condition did not settle');}
function deferred<T>(){let resolve!:(v:T)=>void,reject!:(e:unknown)=>void;const promise=new Promise<T>((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};}
const keys=generateKeyPairSync('ed25519'),rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const pin:ServerPin={publicKey:rawKey.toString('base64url'),fingerprint:createHash('sha256').update(rawKey).digest('base64url'),serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url')};
const lan='http://192.168.1.8:32500',other='http://192.168.1.9:32500';
const signed=(v:unknown)=>{const raw=Buffer.from(JSON.stringify(v));return{payload:raw.toString('base64url'),signature:sign(null,raw,keys.privateKey).toString('base64url')};};
function session(token='token_one',generation='1'):LocalSession {const until=new Date(Date.now()+600000).toISOString();return{accessToken:token,tokenGeneration:generation,sessionFamilyId:'same_family',expiresAt:until,authorizationHorizon:until,serverIdentity:{publicKey:pin.publicKey,fingerprint:pin.fingerprint},viewer:{serverId:pin.serverId,accountId:'owner',profileId:'profile',role:'owner',authority:'local'}};}
function storage(){let value:RememberedServer|undefined;return{read:async()=>structuredClone(value),change:async(update:(v:RememberedServer|undefined)=>RememberedServer|undefined)=>{value=structuredClone(update(structuredClone(value)));}} satisfies ServerConnectionStorage;}
async function ready(fetcher?:typeof fetch){const connection=new RouteConnection({pin,logicalOrigin:lan,memory:{paired:[lan]},probe:async()=>pin,fetcher});await connection.resolve();const api=new HttpLocalApi(lan,'',fetcher);api.useRoutes(connection);return{connection,api};}

test('delayed old mutation failure cannot invalidate a newer proved route or replay the mutation',async()=>{
 const failed=deferred<Response>();let calls=0,proofs=0;const connection=new RouteConnection({pin,logicalOrigin:lan,memory:{paired:[lan]},probe:async()=>{proofs++;return pin;},fetcher:async()=>{calls++;return failed.promise;}});
 try{await connection.resolve();const request=connection.fetch(lan+'/v1/change',{method:'POST'});const rejection=assert.rejects(request);await until(()=>calls===1);connection.networkChanged('wifi-2');await connection.resolve();const revision=connection.getSnapshot().revision,checks=proofs;failed.reject(new Error('old socket failed'));await rejection;assert.equal(calls,1);assert.equal(proofs,checks);assert.equal(connection.getSnapshot().revision,revision);assert.equal(connection.getSnapshot().phase,'ready');}finally{connection.dispose();}
});
test('read failure reuses newer proof without starting a third generation',async()=>{
 const failed=deferred<Response>();let calls=0,proofs=0;const connection=new RouteConnection({pin,logicalOrigin:lan,memory:{paired:[lan]},probe:async()=>{proofs++;return pin;},fetcher:async()=>++calls===1?failed.promise:new Response('ok')});
 try{await connection.resolve();const request=connection.fetch(lan+'/v1/me');await until(()=>calls===1);connection.networkChanged('wifi-2');await connection.resolve();const checks=proofs;failed.reject(new Error('lost read'));assert.equal(await(await request).text(),'ok');assert.equal(calls,2);assert.equal(proofs,checks);}finally{connection.dispose();}
});
test('network change between resolved promise and dispatch fences the credential-bearing request',async()=>{
 let proofGate:ReturnType<typeof deferred<ServerPin>>|undefined;const sent:string[]=[];const connection=new RouteConnection({pin,logicalOrigin:lan,memory:{paired:[lan]},probe:async()=>proofGate?proofGate.promise:pin,fetcher:async input=>{sent.push(String(input));return new Response('ok');}});
 try{await connection.resolve();proofGate=deferred<ServerPin>();const request=connection.fetch(lan+'/v1/me',{headers:{Authorization:'Bearer secret'}});connection.networkChanged('changed-synchronously');await tick();assert.deepEqual(sent,[]);proofGate.resolve(pin);await request;assert.equal(sent.length,1);}finally{connection.dispose();}
});
test('Hosted Retry-After survives recovery attempts without blocking cached LAN proofs',async()=>{
 let fresh=0,available=false,proofs=0;const connection=new RouteConnection({pin,logicalOrigin:lan,memory:{paired:[lan]},probe:async()=>{proofs++;if(!available)throw new Error('offline');return pin;},fresh:async()=>{fresh++;throw new ApiError(429,'rate_limited','wait',true,60);}});
 try{await assert.rejects(connection.resolve());assert.equal(fresh,1);await assert.rejects(connection.recover());assert.equal(fresh,1);available=true;await connection.recover();assert.equal(connection.getSnapshot().phase,'ready');assert.ok(proofs>=3);assert.equal(fresh,1);}finally{connection.dispose();}
});
test('same-family late selection rollback does not replace a newer saved selection',async()=>{
 const base=storage(),ack=deferred<void>();let first=true,committed=false,current=true;const delayed:ServerConnectionStorage={read:base.read,change:async update=>{await base.change(update);if(first){first=false;committed=true;await ack.promise;}}};const a=await ready(),b=await ready();
 try{const old=rememberNativeSession(a.api,session(),{storage:delayed},undefined,()=>current);const rejection=assert.rejects(old);await until(()=>committed);current=false;await rememberNativeSession(b.api,session(),{storage:base});const fresh=await base.read();ack.resolve();await rejection;assert.deepEqual(await base.read(),fresh);assert.equal(sessionRoute('token_one'),b.connection);}finally{a.connection.dispose();b.connection.dispose();}
});
test('old cache owner cannot write over a newer selection of the same family',async()=>{
 const store=storage(),a=await ready(),b=await ready();try{await rememberNativeSession(a.api,session(),{storage:store});await rememberNativeSession(b.api,session(),{storage:store});const fresh=await store.read();a.connection.rememberPaired(other);await a.connection.flush();assert.deepEqual(await store.read(),fresh);}finally{a.connection.dispose();b.connection.dispose();}
});
test('restore rechecks durable selection after proof, before sending a bearer',async()=>{
 const store=storage(),initial=await ready(),proof=deferred<void>();let requested=false,me=0;
 try{await rememberNativeSession(initial.api,session(),{storage:store});const fetcher:typeof fetch=async(input,init)=>{if(String(input).endsWith('/v1/networking/identity-proof')){requested=true;await proof.promise;const q=JSON.parse(String(init?.body));return new Response(JSON.stringify(signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()})),{headers:{'Content-Type':'application/json'}});}me++;return new Response(JSON.stringify({viewer:session().viewer}),{headers:{'Content-Type':'application/json'}});};const restored=restoreNativeSession({storage:store,fetcher});const rejection=assert.rejects(restored);await until(()=>requested);await store.change(()=>undefined);proof.resolve();await rejection;assert.equal(me,0);}finally{initial.connection.dispose();}
});
test('renewal projection keeps generation monotonic and retains exactly the routed family',async()=>{
 const store=storage(),a=await ready();try{await rememberNativeSession(a.api,session(),{storage:store});await rememberRotatedSession({storage:store},session('token_two','2'));assert.equal(sessionRoute('token_two'),a.connection);await rememberRotatedSession({storage:store},session('token_one','1'));assert.equal((await store.read())?.session.accessToken,'token_two');await assert.rejects(rememberRotatedSession({storage:store},session('conflicting','2')));}finally{a.connection.dispose();}
});
test('prepared approved grant remains retryable after atomic storage failure',async()=>{
 const store=storage(),a=await ready();let fail=true;const flaky:ServerConnectionStorage={read:store.read,change:async update=>{if(fail)throw new Error('keychain unavailable');await store.change(update);}};
 try{const grant=session('prepared');prepareRoutedSession(a.api,grant);await assert.rejects(commitRoutedSession(grant,{storage:flaky}));fail=false;assert.equal(await commitRoutedSession(grant,{storage:flaky}),a.api);assert.equal((await store.read())?.session.accessToken,'prepared');}finally{a.connection.dispose();}
});
const requestId='11111111-1111-4111-8111-111111111111';
const authorization=()=>({deviceCode:'s'.repeat(43),userCode:'ABCD-EFGH',verificationUri:'https://web.getportico.tv/device',verificationUriComplete:'https://web.getportico.tv/device#code=ABCD-EFGH',expiresAt:new Date(Date.now()+600000).toISOString(),expiresIn:600,interval:5});
function journal<T>(initial?:SetupCodeJournal<T>){let value=structuredClone(initial);return{read:()=>structuredClone(value),change:async(update:(v:SetupCodeJournal<T>|undefined)=>SetupCodeJournal<T>)=>{value=structuredClone(update(structuredClone(value)));return structuredClone(value);}};}
const opts={tickMs:5,requestTimeoutMs:50,replaceBeforeMs:60000};
test('pause and resume do not orphan an in-flight idempotent code creation',async()=>{
 const pending=deferred<ReturnType<typeof authorization>>(),store=journal<string>();let calls=0;const service=new SetupCodeService('local:one',{create:async()=>{calls++;return pending.promise;},poll:async()=>'',cancel:async()=>{}},store,async()=>requestId,{...opts,requestTimeoutMs:1000});
 try{await service.start();await until(()=>calls===1);service.pause();await service.start();pending.resolve(authorization());await until(()=>service.getSnapshot().phase==='waiting');assert.equal(calls,1);assert.equal(store.read()?.current?.requestId,requestId);assert.equal(store.read()?.retired.length,0);}finally{service.pause();}
});
test('late poll checkpoint cannot persist a grant after the bounded poll expired',async()=>{
 const store=journal<string>({version:1,authority:'local:one',revision:1,current:{requestId,authorization:authorization(),nextPollAt:0},retired:[]});let checkpoint:((g:string)=>Promise<void>)|undefined,signal:AbortSignal|undefined;
 const service=new SetupCodeService('local:one',{create:async()=>authorization(),poll:async(_entry,s,c)=>{signal=s;checkpoint=c;return new Promise(()=>{});},cancel:async()=>{}},store,async()=>requestId,opts);
 try{await service.start();await until(()=>!!signal?.aborted);await assert.rejects(checkpoint!('late-secret'));assert.equal(store.read()?.current?.grant,undefined);assert.notEqual(service.getSnapshot().phase,'approved');}finally{service.pause();}
});
test('approved adoption survives storage JSON copies and parent unmount acknowledgement',async()=>{
 const grant={family:'one'},store=journal<typeof grant>({version:1,authority:'local:one',revision:1,current:{requestId,authorization:authorization(),nextPollAt:0,grant},retired:[]});
 const service=new SetupCodeService('local:one',{create:async()=>authorization(),poll:async()=>grant,cancel:async()=>{}},store,async()=>requestId,opts);
 try{await service.start();const offered=service.getSnapshot().grant!;assert.ok(service.isCurrent(offered));service.pause();assert.equal(service.isCurrent(offered),false);assert.equal(await service.complete(offered),true);assert.equal(store.read()?.current,undefined);}finally{service.pause();}
});
test('cancelling a lost create reconciles the SAME request before retiring its secret',async()=>{
 const store=journal<string>({version:1,authority:'local:one',revision:1,creating:{requestId,startedAt:Date.now()},retired:[]});const created:string[]=[],cancelled:string[]=[];
 const service=new SetupCodeService('local:one',{create:async id=>{created.push(id);return authorization();},poll:async()=>'',cancel:async entry=>{cancelled.push(entry.requestId);}},store,async()=>{throw new Error('must not allocate another request');},opts);
 // Cancellation loads the protected journal even before a screen is started.
 await service.cancel();assert.deepEqual(created,[requestId]);assert.deepEqual(cancelled,[requestId]);assert.equal(store.read()?.creating,undefined);assert.equal(store.read()?.retired.length,0);
});
// Activation retries by its own clock: the test moves that clock and lets the service's short
// real ticks notice. `gaps` are the seconds between attempts, read to the step the clock moves in.
function activationClock(t:import('node:test').TestContext){
 let now=Date.UTC(2026,9,12,12);const mocked=t.mock.method(Date,'now',()=>now);
 const attempts:number[]=[];
 return {attempts,attempt:()=>{attempts.push(now);},restore:()=>mocked.mock.restore(),
  async run(count:number){for(let i=0;i<4000&&attempts.length<count;i++){now+=250;await new Promise<void>(r=>setTimeout(r,3));}assert.ok(attempts.length>=count,'attempts did not continue');},
  gaps:()=>attempts.slice(1).map((at,i)=>(at-attempts[i]!)/1000)};
}
const about=(actual:number[],expected:number[])=>{assert.equal(actual.length,expected.length);actual.forEach((gap,i)=>assert.ok(gap>=expected[i]!&&gap<=expected[i]!+1,`retry ${i+1} after ${gap} s, expected about ${expected[i]} s`));};
const fastTicks={tickMs:1,requestTimeoutMs:50,replaceBeforeMs:60000};
test('activation polls that cannot reach the service back off from about 5 s to a 30 s cap, with jitter',async t=>{
 const clock=activationClock(t),store=journal<string>();
 const service=new SetupCodeService('local:one',{create:async()=>authorization(),poll:async()=>{clock.attempt();throw new TypeError('network down');},cancel:async()=>{}},store,async()=>requestId,{...fastTicks,random:()=>0.5});
 await service.start();await clock.run(6);service.pause();
 // 5, 10, 20 stretched by the jitter (half of 20% here), then the cap, where the jitter falls
 // below 30 s instead of above it.
 about(clock.gaps(),[5.5,11,22,27,27]);
 assert.equal(service.getSnapshot().phase,'waiting','the code on screen stays valid throughout');
});
test('an answer from the service returns activation polling to its normal interval and first retry',async t=>{
 const clock=activationClock(t),store=journal<string>();let calls=0;
 const service=new SetupCodeService('local:one',{create:async()=>authorization(),poll:async()=>{clock.attempt();calls++;if(calls===3)throw new ApiError(400,'authorization_pending','Waiting for approval.',false);throw new TypeError('network down');},cancel:async()=>{}},store,async()=>requestId,{...fastTicks,random:()=>0});
 await service.start();await clock.run(5);service.pause();
 // Two failures (5 s, 10 s), "still pending" (the code's own 5 s interval), then failures start over at 5 s.
 about(clock.gaps(),[5,10,5,5]);
});
test('creating an activation code backs off the same way',async t=>{
 const clock=activationClock(t),store=journal<string>();
 const service=new SetupCodeService('local:one',{create:async()=>{clock.attempt();throw new TypeError('network down');},poll:async()=>'',cancel:async()=>{}},store,async()=>requestId,{...fastTicks,random:()=>0.5});
 await service.start();await clock.run(6);service.pause();
 about(clock.gaps(),[5.5,11,22,27,27]);
});
test('activation retries stay spread out once they reach the cap, and never pass it',async t=>{
 // Two devices that lost the service in the same moment, at opposite ends of the jitter.
 const waits=async(random:number)=>{
  const clock=activationClock(t),store=journal<string>();
  const service=new SetupCodeService('local:one',{create:async()=>authorization(),poll:async()=>{clock.attempt();throw new TypeError('network down');},cancel:async()=>{}},store,async()=>requestId,{...fastTicks,random:()=>random});
  await service.start();await clock.run(8);service.pause();clock.restore();
  return clock.gaps().slice(3);
 };
 const early=await waits(0.999),late=await waits(0);
 for(const gap of [...early,...late])assert.ok(gap>=24&&gap<=31,'at the cap: '+gap+' s');
 assert.ok(late.every((gap,i)=>gap-early[i]!>=4),'still several seconds apart on every retry: '+early.join(',')+' against '+late.join(','));
});
test('unobserved authorized server remains in the chooser without fabricated routes',async()=>{
 const selection=new SessionSelectionService({account:{accountId:'account',sessionId:'session'},api:{listServers:async()=>({items:[{id:pin.serverId,name:'Not observed yet',publicKey:pin.publicKey,policyRevision:1,baseUrl:''}]}),serverProfiles:async()=>({}),attach:async()=>session(),revokeLocal:async()=>{}},onSelected:()=>{}});
 try{await selection.loadServers();assert.equal(selection.getSnapshot().phase,'servers');assert.equal(selection.getSnapshot().servers.length,1);assert.equal(selection.getSnapshot().servers[0].routes,undefined);}finally{selection.dispose();}
});

test('route recovery cannot adopt a different tab or device selected profile',async()=>{
 const vault=storage(),{connection,api}=await ready();const saved=session();await rememberNativeSession(api,saved,{storage:vault});
 let probes=0;const env={storage:vault,fetcher:async()=>{probes++;throw new Error('must not send credentials for another selected viewer');}};
 const restored=await restoreNativeSession(env,undefined,{...saved,accessToken:'other_tab_token'});
 assert.equal(restored,undefined);assert.equal(probes,0);connection.dispose();
});

test('identity rejection expires without relaxing the server pin or sending a bearer',async t=>{
 const {RouteError}=await import('../src/route-identity.ts');
 let now=Date.now(),valid=false,proofs=0,requests=0;
 t.mock.method(Date,'now',()=>now);
 const connection=new RouteConnection({pin,logicalOrigin:lan,memory:{paired:[lan]},probe:async(_url,expected)=>{proofs++;assert.deepEqual(expected,pin);if(!valid)throw new RouteError('identity_mismatch','wrong server');return pin;},fetcher:async()=>{requests++;return new Response('ok');}});
 try {
  await assert.rejects(connection.fetch(lan+'/v1/me',{headers:{Authorization:'Bearer secret'}}));
  assert.equal(requests,0);assert.equal(proofs,1);
  valid=true;
  await assert.rejects(connection.recover());assert.equal(proofs,1);assert.equal(connection.getSnapshot().phase,'identity_mismatch');
  now+=5001;
  await connection.recover();await connection.fetch(lan+'/v1/me',{headers:{Authorization:'Bearer secret'}});
  assert.equal(proofs,2);assert.equal(requests,1);
 } finally {connection.dispose();}
});
test('a meaningful network transition allows a fresh pinned proof for rejected Bonjour routes',async()=>{
 const {RouteError}=await import('../src/route-identity.ts');
 let valid=false,proofs=0;
 // Nearby plain HTTP is used only once the server counts this device as home (P2-69 C-3).
 const connection=new RouteConnection({pin,logicalOrigin:lan,readConnection:async()=>({remote:false}),discover:async()=>[{baseUrl:lan,serverId:pin.serverId,fingerprint:pin.fingerprint,port:32500,path:'/',expiresAt:Date.now()+60000}],probe:async(_url,expected)=>{proofs++;assert.deepEqual(expected,pin);if(!valid)throw new RouteError('identity_mismatch','wrong server');return pin;}});
 try {await assert.rejects(connection.resolve());valid=true;connection.networkChanged('wifi-returned');assert.equal(await connection.resolve(),lan);assert.equal(proofs,2);}finally{connection.dispose();}
});

test('restore renews an access token the server no longer accepts instead of losing the sign-in',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');
 setInstallationIdentity({installationId:'inst_restore_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 const store=storage(),initial=await ready();
 const saved={...session('token_one','1'),refreshToken:'refresh_'+'a'.repeat(24)};
 const renewed={...session('token_two','2'),refreshToken:'refresh_'+'b'.repeat(24)};
 let refreshes=0;const bearers:string[]=[];
 try{
  await rememberNativeSession(initial.api,saved,{storage:store});
  const json=(status:number,body:unknown)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
  const fetcher:typeof fetch=async(input,init)=>{
   const url=String(input);
   if(url.endsWith('/v1/networking/identity-proof')){const q=JSON.parse(String(init?.body));return json(200,signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));}
   if(url.endsWith('/v1/auth/refresh')){refreshes++;return json(200,renewed);}
   const bearer=new Headers(init?.headers).get('authorization')??'';bearers.push(bearer);
   // The server restarted: the old access token is refused although it has not expired.
   return bearer==='Bearer token_two'?json(200,{viewer:saved.viewer}):json(401,{error:{code:'unauthorized'}});
  };
  const restored=await restoreNativeSession({storage:store,fetcher});
  assert.equal(restored?.session.accessToken,'token_two');assert.equal(refreshes,1);
  assert.deepEqual(bearers,['Bearer token_one','Bearer token_two']);
  assert.equal((await store.read())?.session.refreshToken,renewed.refreshToken);
  restored?.api.getRouteConnection()?.dispose();
 }finally{initial.connection.dispose();}
});

test('a malformed renewal (400) keeps the saved credential; only a refusal (401) ends it',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');const {refreshStoredSession}=await import('../src/server-connections.ts');
 setInstallationIdentity({installationId:'inst_renew_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 for(const [status,kept] of [[400,true],[401,false]] as const){
  // The saved token is bound to its route, so the server's answer comes through that route.
  const answer:typeof fetch=async()=>new Response(JSON.stringify({error:{code:'x'}}),{status,headers:{'Content-Type':'application/json'}});
  const store=storage(),initial=await ready(answer);
  try{
   await rememberNativeSession(initial.api,{...session('token_one','1'),refreshToken:'refresh_'+'a'.repeat(24)},{storage:store});
   // The access token has expired since, so renewal is due.
   await store.change(v=>v&&{...v,session:{...v.session,expiresAt:new Date(Date.now()-1000).toISOString()}});
   const api=new HttpLocalApi(lan,'token_one',answer);api.useRoutes(initial.connection);
   await assert.rejects(refreshStoredSession({env:{storage:store},api,family:{sessionFamilyId:'same_family',serverId:pin.serverId}}),(e:{status?:number})=>e.status===status);
   assert.equal(!!(await store.read()),kept,`status ${status}`);
  }finally{initial.connection.dispose();}
 }
});

async function restoreAgainst(t:import('node:test').TestContext,meAnswer:(bearer:string)=>number){
 const {setInstallationIdentity}=await import('../src/installation.ts');
 setInstallationIdentity({installationId:'inst_refused_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 const store=storage(),initial=await ready();
 const saved={...session('token_one','1'),refreshToken:'refresh_'+'a'.repeat(24)};
 const renewed={...session('token_two','2'),refreshToken:'refresh_'+'b'.repeat(24)};
 const calls={refresh:0,me:0};
 await rememberNativeSession(initial.api,saved,{storage:store});
 const json=(status:number,body:unknown)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
 const fetcher:typeof fetch=async(input,init)=>{
  const url=String(input);
  if(url.endsWith('/v1/networking/identity-proof')){const q=JSON.parse(String(init?.body));return json(200,signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));}
  if(url.endsWith('/v1/auth/refresh')){calls.refresh++;return json(200,renewed);}
  calls.me++;const status=meAnswer(new Headers(init?.headers).get('authorization')??'');
  return status===200?json(200,{viewer:saved.viewer}):json(status,{error:{code:'x'}});
 };
 const outcome=await restoreNativeSession({storage:store,fetcher}).then(r=>{r?.api.getRouteConnection()?.dispose();return 'restored';},(e:{code?:string})=>e.code);
 initial.connection.dispose();
 return {outcome,calls,stored:await store.read()};
}
test('a 403 from /me is access refused: no renewal, the credential stays',async t=>{
 const r=await restoreAgainst(t,()=>403);
 assert.equal(r.outcome,'access_refused');assert.equal(r.calls.refresh,0);assert.equal(r.calls.me,1);assert.equal(r.stored?.session.refreshToken,'refresh_'+'a'.repeat(24));
});
test('a second 401 after a successful renewal is access refused, not offline',async t=>{
 const r=await restoreAgainst(t,()=>401);
 assert.equal(r.outcome,'access_refused');assert.equal(r.calls.refresh,1);assert.equal(r.calls.me,2);assert.ok(r.stored?.session.refreshToken);
});

test('restore, Try again and the background refresher at once send one refresh and never reuse a token',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');const {LocalSessionRefresher}=await import('../src/session-refresh.ts');
 setInstallationIdentity({installationId:'inst_race_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 // The saved access token expired while the server was down; the refresh token is still good.
 const saved={...session('access_1','1'),refreshToken:'refresh_'+'1'.repeat(24)};
 // A fake server that rotates on the current refresh token and counts any predecessor as reuse.
 let current='refresh_'+'1'.repeat(24),generation=1,refreshes=0,reuse=0;const live=new Set<string>();
 const json=(status:number,body:unknown)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
 const fetcher:typeof fetch=async(input,init)=>{
  const url=String(input);
  if(url.endsWith('/v1/networking/identity-proof')){const q=JSON.parse(String(init?.body));return json(200,signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));}
  if(url.endsWith('/v1/auth/refresh')){
   // Slow enough that all three callers are waiting on a renewal at the same time.
   refreshes++;await new Promise(r=>setTimeout(r,150));
   const q=JSON.parse(String(init?.body)) as {refreshToken:string};
   if(q.refreshToken!==current){reuse++;return json(401,{error:{code:'unauthorized'}});}
   generation++;current='refresh_'+String(generation).repeat(24);const access='access_'+generation;live.add(access);
   return json(200,{...session(access,String(generation)),refreshToken:current});
  }
  const bearer=(new Headers(init?.headers).get('authorization')??'').replace('Bearer ','');
  return live.has(bearer)?json(200,{viewer:saved.viewer}):json(401,{error:{code:'unauthorized'}});
 };
 const store=storage(),initial=await ready(fetcher);
 await rememberNativeSession(initial.api,saved,{storage:store});
 await store.change(v=>v&&{...v,session:{...v.session,expiresAt:new Date(Date.now()-1000).toISOString()}});
 const env={storage:store,fetcher};
 // The background refresher on the transport the shell already holds, with the same stored sign-in.
 const shellApi=new HttpLocalApi(lan,'access_1',fetcher);shellApi.useRoutes(initial.connection);
 const {refreshToken:_secret,...visible}=saved;
 const refresher=new LocalSessionRefresher({env,api:shellApi,session:{...visible,expiresAt:new Date(Date.now()-1000).toISOString()},setTimer:()=>0,clearTimer:()=>{}});
 try{
  const [a,b,c]=await Promise.allSettled([restoreNativeSession(env),restoreNativeSession(env),refresher.recover('access_1')]);
  assert.equal(reuse,0,'no refresh token was presented twice');
  assert.equal(refreshes,1,'exactly one renewal');
  const stored=await store.read();
  assert.equal(stored?.session.refreshToken,current);assert.equal(stored?.pendingRefresh,undefined);
  for(const r of [a,b])if(r.status==='fulfilled')assert.equal(r.value?.session.accessToken,'access_2');
assert.equal(c.status==='fulfilled'&&c.value,'access_2');
  for(const r of [a,b])if(r.status==='fulfilled')r.value?.api.getRouteConnection()?.dispose();
 }finally{refresher.stop();initial.connection.dispose();}
});
test('a Portico Account member’s server-local session is remembered with its Portico route source; a legacy hosted viewer must still match',async()=>{
 const store=storage(),a=await ready();
 const hosted={origin:'https://hosted.example',accountId:'acc_portico',key:{} as never};
 try{
  const member={...session(),viewer:{authority:'local',accountId:'acct_server_local',profileId:'profile',serverId:pin.serverId,role:'member'}};
  await rememberNativeSession(a.api,member,{storage:store},hosted);
  const saved=await store.read();assert.equal(saved?.hosted?.accountId,'acc_portico');assert.equal(saved?.session.viewer.authority,'local');
  // …and read back at launch (the e2e restore failed on this before).
  const hostedKey={origin:'https://hosted.example',accountId:'acc_portico',key:{keyId:'k',publicKey:'p'.repeat(43)}};
  assert.equal(parseRememberedServer({...structuredClone(saved),hosted:hostedKey})?.session.viewer.authority,'local');
  assert.throws(()=>parseRememberedServer({...structuredClone(saved),hosted:hostedKey,session:{...structuredClone(saved)!.session,viewer:{...structuredClone(saved)!.session.viewer,authority:'hosted',accountId:'someone_else'}}}));
  const legacy={...session('token_two'),viewer:{authority:'hosted',accountId:'someone_else',profileId:'profile',serverId:pin.serverId,role:'member'}};
  await assert.rejects(rememberNativeSession(a.api,legacy,{storage:store},hosted),/another account/);
 }finally{a.connection.dispose();}
});

function accountRestoreSnapshot(){
 return {authority:'local',serverId:pin.serverId,account:{id:'owner',username:'owner',primaryProfileId:'profile',role:'owner',revision:1,disabled:false,allowedLibraries:[]},
  profiles:[{id:'profile',name:'Main',primary:true,position:0,art:'blue',revision:1,pinRevision:1,pinRequired:false},{id:'second',name:'Second',primary:false,position:1,art:'blue',revision:1,pinRevision:1,pinRequired:false}],
  canManage:false,capabilities:{manageAccount:false,managePeople:false,manageServer:false,manageSignIn:false}};
}
async function accountRestoreCase(answer:(snapshot:ReturnType<typeof accountRestoreSnapshot>)=>{status:number;body:unknown}){
 const store=storage(),initial=await ready(),saved={...session(),viewer:{...session().viewer,role:'account'}};
 const calls={proof:0,account:0,viewer:0,refresh:0,profile:0};
 const json=(status:number,body:unknown)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
 await rememberNativeSession(initial.api,saved,{storage:store});
 const fetcher:typeof fetch=async(input,init)=>{
  const path=new URL(String(input)).pathname;
  if(path==='/v1/networking/identity-proof'){
   calls.proof++;assert.equal(new Headers(init?.headers).has('Authorization'),false);
   const q=JSON.parse(String(init?.body));return json(200,signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));
  }
  assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer '+saved.accessToken);
  if(path==='/v1/direct'){calls.account++;const out=answer(accountRestoreSnapshot());return json(out.status,out.body);}
  if(path==='/v1/me'){calls.viewer++;return json(401,{error:{code:'unauthorized'}});}
  if(path==='/v1/auth/refresh')calls.refresh++;
  if(path.endsWith('/select'))calls.profile++;
  throw new Error('unexpected account restoration request');
 };
 return {calls,store,saved,initial,restore:()=>restoreNativeSession({storage:store,fetcher})};
}
test('restoration validates a live account grant without treating it as a selected viewer',async()=>{
 const f=await accountRestoreCase(snapshot=>({status:200,body:snapshot}));
 let restored:Awaited<ReturnType<typeof restoreNativeSession>>;
 try{
  restored=await f.restore();
  assert.equal(restored?.session.viewer.role,'account');
  assert.deepEqual(restored?.session.viewer,f.saved.viewer);
  assert.deepEqual(f.calls,{proof:1,account:1,viewer:0,refresh:0,profile:0});
  assert.equal((await f.store.read())?.session.sessionFamilyId,f.saved.sessionFamilyId);
 }finally{restored?.api.getRouteConnection()?.dispose();f.initial.connection.dispose();}
});
test('account restoration rejects different account/server/primary profile and disabled facts without viewer fallback',async()=>{
 for(const change of [
  (s:ReturnType<typeof accountRestoreSnapshot>)=>({...s,account:{...s.account,id:'another'}}),
  (s:ReturnType<typeof accountRestoreSnapshot>)=>({...s,serverId:'another'}),
  (s:ReturnType<typeof accountRestoreSnapshot>)=>({...s,account:{...s.account,primaryProfileId:'second'},profiles:s.profiles.map(p=>({...p,primary:p.id==='second'}))}),
  (s:ReturnType<typeof accountRestoreSnapshot>)=>({...s,account:{...s.account,disabled:true}}),
 ]){
  const f=await accountRestoreCase(s=>({status:200,body:change(s)}));
  try{await assert.rejects(f.restore());assert.equal(f.calls.viewer,0);assert.equal(f.calls.refresh,0);assert.equal(f.calls.profile,0);assert.equal((await f.store.read())?.session.viewer.role,'account');}
  finally{f.initial.connection.dispose();}
 }
});
test('live account denial is not a reason to probe viewer authority or select a profile',async()=>{
 const f=await accountRestoreCase(()=>({status:403,body:{error:{code:'not_permitted'}}}));
 try{await assert.rejects(f.restore(),{code:'access_refused'});assert.deepEqual(f.calls,{proof:1,account:1,viewer:0,refresh:0,profile:0});assert.equal((await f.store.read())?.session.viewer.role,'account');}
 finally{f.initial.connection.dispose();}
});

test('account-only restoration rotates once within account scope and refuses an implicit viewer upgrade',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');
 setInstallationIdentity({installationId:'account_restore_installation_00000001',name:'Test browser',platform:'web',app:'portico',appVersion:'1'});
 t.after(()=>setInstallationIdentity(undefined));
 for(const mode of ['account','upgrade','refused','revoked'] as const){
  const store=storage(),initial=await ready();
  const base=session(),saved={...base,refreshToken:'account_refresh_original',viewer:{...base.viewer,role:'account'}};
  const renewed={...saved,accessToken:'account_access_next',refreshToken:'account_refresh_next',tokenGeneration:'2',viewer:{...saved.viewer,role:mode==='upgrade'?'owner':'account'}};
  const calls={account:0,refresh:0,viewer:0,profile:0};
  const json=(status:number,body:unknown)=>new Response(JSON.stringify(body),{status,headers:{'Content-Type':'application/json'}});
  await rememberNativeSession(initial.api,saved,{storage:store});
  const fetcher:typeof fetch=async(input,init)=>{
   const path=new URL(String(input)).pathname;
   if(path==='/v1/networking/identity-proof'){
    const q=JSON.parse(String(init?.body));return json(200,signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));
   }
   if(path==='/v1/auth/refresh'){calls.refresh++;return mode==='revoked'?json(401,{error:{code:'unauthorized'}}):json(200,renewed);}
   if(path==='/v1/direct'){
    calls.account++;
    assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer '+(calls.account===1?saved.accessToken:renewed.accessToken));
    return calls.account===1||mode==='refused'?json(401,{error:{code:'unauthorized'}}):json(200,accountRestoreSnapshot());
   }
   if(path==='/v1/me')calls.viewer++;
   if(path.endsWith('/select'))calls.profile++;
   throw new Error('unexpected account rotation request');
  };
  let restored:Awaited<ReturnType<typeof restoreNativeSession>>;
  try{
   const pending=restoreNativeSession({storage:store,fetcher});
   if(mode==='account'){restored=await pending;assert.equal(restored?.session.viewer.role,'account');assert.equal(restored?.session.tokenGeneration,'2');}
   else await assert.rejects(pending,{code:mode==='upgrade'?'refresh_invalid':mode==='revoked'?'refresh_refused':'access_refused'});
   assert.equal(calls.refresh,1);assert.equal(calls.viewer,0);assert.equal(calls.profile,0);
   assert.equal(calls.account,mode==='upgrade'||mode==='revoked'?1:2);
   const current=await store.read();
   if(mode==='revoked')assert.equal(current,undefined);
   else assert.equal(current?.session.viewer.role,'account');
  }finally{restored?.api.getRouteConnection()?.dispose();initial.connection.dispose();}
 }
});


async function roleRestoreCase(from: string, to: string, nearExpiry = false) {
 const store=storage(),initial=await ready();
 const saved={...session('role_before'),refreshToken:'refresh_'+'a'.repeat(24),viewer:{...session().viewer,role:from},
  ...(nearExpiry?{expiresAt:new Date(Date.now()+30000).toISOString()}:{} )};
 await rememberNativeSession(initial.api,saved,{storage:store});
 let refreshes=0;
 const json=(value:unknown)=>new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}});
 const fetcher:typeof fetch=async(input,init)=>{
  if(String(input).endsWith('/v1/networking/identity-proof')){
   const q=JSON.parse(String(init?.body));return json(signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));
  }
  if(String(input).endsWith('/v1/auth/refresh')){
   refreshes++;return json({...session('role_after','2'),refreshToken:'refresh_'+'b'.repeat(24),viewer:{...saved.viewer,role:to}});
  }
  assert.ok(String(input).endsWith('/v1/me'));
  return json({viewer:{...saved.viewer,role:to}});
 };
 return {store,initial,saved,env:{storage:store,fetcher},refreshes:()=>refreshes};
}

test('ordinary restore reconciles promotions and demotions into the matching protected family',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');
 setInstallationIdentity({installationId:'inst_restore_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 for(const [from,to,nearExpiry] of [['owner','member',false],['member','admin',false],['admin','owner',true]] as const){
  const h=await roleRestoreCase(from,to,nearExpiry);let restored:Awaited<ReturnType<typeof restoreNativeSession>>;
  try{restored=await restoreNativeSession(h.env,undefined,h.saved);
   assert.equal(restored?.session.viewer.role,to);assert.equal((await h.store.read())?.session.viewer.role,to);
   assert.equal(restored?.session.sessionFamilyId,h.saved.sessionFamilyId);assert.equal(h.refreshes(),nearExpiry?1:0);
  }finally{restored?.api.getRouteConnection()?.dispose();h.initial.connection.dispose();}
 }
});

test('exact Security restore still rejects a changed role without reconciling the stored viewer',async()=>{
 const h=await roleRestoreCase('owner','member');
 try{await assert.rejects(restoreExactNativeSession(h.env,h.saved),{code:'identity_mismatch'});assert.equal((await h.store.read())?.session.viewer.role,'owner');assert.equal(h.refreshes(),0);}
 finally{h.initial.connection.dispose();}
});

test('exact Security restore refuses an old role after ordinary reconciliation before sending any request',async()=>{
 const h=await roleRestoreCase('owner','member');let restored:Awaited<ReturnType<typeof restoreNativeSession>>,requests=0;
 try{
  restored=await restoreNativeSession(h.env,undefined,h.saved);
  assert.equal(restored?.session.viewer.role,'member');
  assert.equal(restored?.session.accessToken,h.saved.accessToken);
  const exact=await restoreExactNativeSession({...h.env,fetcher:async(input,init)=>{requests++;return h.env.fetcher(input,init);}},h.saved);
  assert.equal(exact,undefined);assert.equal(requests,0);assert.equal(h.refreshes(),0);
 }finally{restored?.api.getRouteConnection()?.dispose();h.initial.connection.dispose();}
});

test('exact Security restore rejects a role reconciled while proof or live validation is in flight',async()=>{
 for(const during of ['proof','me'] as const){
  const h=await roleRestoreCase('owner','owner');let bearers=0;
  const fetcher:typeof fetch=async(input,init)=>{
   const result=await h.env.fetcher(input,init),isMe=String(input).endsWith('/v1/me');
   if(isMe)bearers++;
   if((during==='me')===isMe)await h.store.change(current=>({...current!,session:{...current!.session,viewer:{...current!.session.viewer,role:'member'}}}));
   return result;
  };
  try{await assert.rejects(restoreExactNativeSession({...h.env,fetcher},h.saved),{code:'cancelled'});assert.equal(bearers,during==='proof'?0:1);assert.equal((await h.store.read())?.session.viewer.role,'member');}
  finally{h.initial.connection.dispose();}
 }
});

test('ordinary role reconciliation preserves all stable viewer identity fences',async()=>{
 for(const field of ['accountId','profileId','serverId','authority'] as const){
  const h=await roleRestoreCase('owner','member'),fetcher:typeof fetch=async(input,init)=>{
   if(String(input).endsWith('/v1/me'))return new Response(JSON.stringify({viewer:{...h.saved.viewer,role:'member',[field]:field==='authority'?'hosted':'other'}}),{headers:{'Content-Type':'application/json'}});
   return h.env.fetcher(input,init);
  };
  try{await assert.rejects(restoreNativeSession({...h.env,fetcher}),{code:'identity_mismatch'});assert.equal((await h.store.read())?.session.viewer.role,'owner');}
  finally{h.initial.connection.dispose();}
 }
});

test('role reconciliation cannot overwrite a newer saved selection',async()=>{
 const h=await roleRestoreCase('owner','member');let replacement:RememberedServer|undefined;
 const fetcher:typeof fetch=async(input,init)=>{
  const result=await h.env.fetcher(input,init);
  if(String(input).endsWith('/v1/me'))await h.store.change(current=>{replacement={...current!,selectionId:'Z'.repeat(32)};return replacement;});
  return result;
 };
 try{await assert.rejects(restoreNativeSession({...h.env,fetcher}),{code:'cancelled'});assert.deepEqual(await h.store.read(),replacement);assert.equal(replacement?.session.viewer.role,'owner');}
 finally{h.initial.connection.dispose();}
});

test('BOOT-013: already-cancelled restore reads nothing, including exact Security restores',async()=>{
 const cancelled=new AbortController();cancelled.abort();let reads=0;
 const env={storage:{read:async()=>{reads++;return undefined;},change:async()=>{throw new Error('unexpected write');}}};
 await assert.rejects(restoreNativeSession(env,undefined,undefined,cancelled.signal),{code:'cancelled'});
 await assert.rejects(restoreExactNativeSession(env,session(),cancelled.signal),{code:'cancelled'});
 assert.equal(reads,0);
});

test('BOOT-013: cancelling the earliest saved read wins over a late malformed record',async()=>{
 const read=deferred<RememberedServer|undefined>(),cancelled=new AbortController();let writes=0,requests=0;
 const result=restoreNativeSession({storage:{read:()=>read.promise,change:async()=>{writes++;}},fetcher:async()=>{requests++;throw new Error('unexpected fetch');}},undefined,undefined,cancelled.signal);
 const rejected=assert.rejects(result,{code:'cancelled'});cancelled.abort();read.resolve({version:'bad'} as unknown as RememberedServer);
 await rejected;assert.equal(writes,0);assert.equal(requests,0);
});

test('BOOT-013: cancellation reaches proof, me, and exact validation without publishing or writing',async()=>{
 for(const exact of [false,true])for(const stage of ['proof','me'] as const){
  const h=await roleRestoreCase('owner','owner'),cancelled=new AbortController(),gate=deferred<void>();let requested=false,requestSignal:AbortSignal|undefined;
  const before=await h.store.read();
  const fetcher:typeof fetch=async(input,init)=>{
   if(String(input).endsWith(stage==='proof'?'/v1/networking/identity-proof':'/v1/me')){
    requested=true;requestSignal=init?.signal??undefined;await gate.promise;
   }
   return h.env.fetcher(input,init);
  };
  try{
   const result=exact?restoreExactNativeSession({...h.env,fetcher},h.saved,cancelled.signal):restoreNativeSession({...h.env,fetcher},undefined,h.saved,cancelled.signal);
   const rejected=assert.rejects(result,{code:'cancelled'});await until(()=>requested);cancelled.abort();
   gate.resolve();await rejected;assert.equal(requestSignal?.aborted,true);assert.deepEqual(await h.store.read(),before);assert.equal(h.refreshes(),0);
  }finally{gate.resolve();h.initial.connection.dispose();}
 }
});

test('BOOT-013: cancellation fences queued role reconciliation before the protected store commits',async()=>{
 const h=await roleRestoreCase('owner','member'),cancelled=new AbortController(),gate=deferred<void>();let changing=false;
 const before=await h.store.read();
 const delayed:ServerConnectionStorage={read:h.store.read,change:async(update,beforeCommit)=>{
  const next=update(await h.store.read());changing=true;await gate.promise;beforeCommit?.();await h.store.change(()=>next);
 }};
 try{
  const result=restoreNativeSession({...h.env,storage:delayed},undefined,h.saved,cancelled.signal);
  const rejected=assert.rejects(result,{code:'cancelled'});await until(()=>changing);cancelled.abort();gate.resolve();await rejected;
  assert.deepEqual(await h.store.read(),before);
 }finally{gate.resolve();h.initial.connection.dispose();}
});

test('BOOT-013: cancellation of refresh preserves the replay id and never adopts or erases a late receipt',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');
 setInstallationIdentity({installationId:'inst_restore_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 for(const response of ['success','refused'] as const){
  const h=await roleRestoreCase('owner','owner',true),cancelled=new AbortController(),gate=deferred<void>();let requested=false,requestSignal:AbortSignal|undefined;
  const fetcher:typeof fetch=async(input,init)=>{
   if(String(input).endsWith('/v1/auth/refresh')){
    requested=true;requestSignal=init?.signal??undefined;await gate.promise;
    if(response==='refused')return new Response(JSON.stringify({error:{code:'session_revoked',message:'revoked'}}),{status:401,headers:{'Content-Type':'application/json'}});
   }
   return h.env.fetcher(input,init);
  };
  try{
   const result=restoreNativeSession({...h.env,fetcher},undefined,h.saved,cancelled.signal),rejected=assert.rejects(result,{code:'cancelled'});
   await until(()=>requested);const before=await h.store.read();assert.ok(before?.pendingRefresh);
   cancelled.abort();assert.equal(requestSignal?.aborted,true);gate.resolve();await rejected;
   assert.deepEqual(await h.store.read(),before);
  }finally{gate.resolve();h.initial.connection.dispose();}
 }
});

test('BOOT-013: cancellation after final storage read cannot return a restored session',async()=>{
 const h=await roleRestoreCase('owner','owner'),cancelled=new AbortController();let reads=0;
 const storage:ServerConnectionStorage={change:h.store.change,read:async()=>{
  const record=await h.store.read();if(++reads===3)cancelled.abort();return record;
 }};
 try{await assert.rejects(restoreNativeSession({...h.env,storage},undefined,h.saved,cancelled.signal),{code:'cancelled'});assert.equal(reads,3);}
 finally{h.initial.connection.dispose();}
});

test('BOOT-013: refresh claim and receipt writes recheck cancellation under the storage commit guard',async t=>{
 const {setInstallationIdentity}=await import('../src/installation.ts');
 setInstallationIdentity({installationId:'inst_restore_'+'x'.repeat(24),name:'Test',platform:'ios',app:'portico-apple',appVersion:'1.0.0'});t.after(()=>setInstallationIdentity(undefined));
 for(const write of [1,2]){
  const h=await roleRestoreCase('owner','owner',true),cancelled=new AbortController(),gate=deferred<void>();let writes=0,waiting=false;
  const delayed:ServerConnectionStorage={read:h.store.read,change:async(update,beforeCommit)=>{
   const next=update(await h.store.read());
   if(++writes===write){waiting=true;await gate.promise;}
   beforeCommit?.();await h.store.change(()=>next);
  }};
  try{
   const result=restoreNativeSession({...h.env,storage:delayed},undefined,h.saved,cancelled.signal),rejected=assert.rejects(result,{code:'cancelled'});
   await until(()=>waiting);const before=await h.store.read();cancelled.abort();gate.resolve();await rejected;
   assert.deepEqual(await h.store.read(),before);assert.equal(h.refreshes(),write===1?0:1);
  }finally{gate.resolve();h.initial.connection.dispose();}
 }
});
