import test, {type TestContext} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {HttpLocalApi, ApiError, type LocalSession} from '@core/index.ts';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {setInstallationIdentity} from '@core/installation.ts';
import {rememberNativeSession} from '@core/server-connections.ts';
import {routeBase64, routeBytes, webRouteCrypto} from '@core/route-identity.ts';
import type {SetupDraft} from '@core/setup-onboarding.ts';
import {BrowserSetupComposition, type SetupTarget} from '../src/platform/setup-composition.ts';
import {SetupStageFailure} from '../src/platform/setup-failure.ts';

const vector = JSON.parse(readFileSync(new URL('../../server/internal/claimweb/testdata/owner_start.json', import.meta.url), 'utf8'));
const original = JSON.parse(vector.claimPayload);
async function fixture(t: TestContext) {
  const install='test-installation-123456789012345678901234';
  setInstallationIdentity({installationId:install,name:'Browser',platform:'web',app:'portico',appVersion:'1'});t.after(()=>setInstallationIdentity(undefined));
  const pin={serverId:original.serverId,publicKey:original.publicKey,fingerprint:routeBase64(await webRouteCrypto.sha256(routeBytes(original.publicKey,32)))};
  let record:AppSessionRecord={version:1,revision:0,servers:[]};
  const sessions=new AppSessionStore({read:async()=>record,change:async update=>{record=update(record)!;}});
  let draft:SetupDraft|undefined={version:1,serverId:pin.serverId,requestId:'11111111-2222-4333-8444-555555555555',setupToken:'private-original-setup',authMode:'local',name:'Actual server'};
  const store={read:async()=>draft,change:async(update:(v:SetupDraft|undefined)=>SetupDraft|undefined)=>{draft=update(draft);}};
  const session:LocalSession={accessToken:'actual-issued-owner',refreshToken:'actual-refresh-owner',sessionFamilyId:'actual-owner-family',tokenGeneration:'1',expiresAt:'2099-01-01T00:00:00Z',authorizationHorizon:'2099-02-01T00:00:00Z',
    serverIdentity:{publicKey:pin.publicKey,fingerprint:pin.fingerprint},viewer:{serverId:pin.serverId,authority:'local',accountId:'actual-account',profileId:'main',role:'owner'}};
  const snapshot={authority:'local',serverId:pin.serverId,canManage:true,capabilities:{manageAccount:true,managePeople:true,manageServer:true,manageSignIn:true},
    account:{id:'actual-account',username:'actual_owner',primaryProfileId:'main',role:'owner',revision:1,disabled:false,allowedLibraries:[]},
    profiles:[{id:'main',name:'Main',primary:true,art:'blue',position:0,revision:1,pinRevision:1,pinRequired:false}]};
  const calls:{path:string;body:unknown}[]=[];
  let secure=true,claimCalls=0,initializes=0;
  const api=new HttpLocalApi(original.address,'');
  const route={pin,logicalOrigin:original.address,getSnapshot:()=>({phase:'ready'}),getMemory:()=>({paired:new Set([original.address])}),setPersistence:()=>{},subscribe:()=>()=>{},fetch:async(input:string|URL|Request,init?:RequestInit)=>{calls.push({path:new URL(String(input)).pathname,body:init?.body});return new Response('{}',{headers:{'Content-Type':'application/json'}});},dispose:()=>{}};
  api.useRoutes(route as never);
  api.request=(async(path:string,_method:string,body:unknown)=>{
    calls.push({path,body});
    if(path==='/v1/direct')return snapshot;
    if(path==='/v1/sessions/current')return {};
    if(path==='/v1/libraries')return {id:'actual-library',name:'Movies',kind:'movie',defaultView:'grid',pinned:false};
    if(path==='/v1/libraries/actual-library/scans')return {};
    if(path==='/v1/setup/resume')throw new ApiError(404,'not_found','not_found',false);
    if(path==='/v1/setup'){initializes++;return {variant:'claim-required',serverId:pin.serverId,requestId:draft!.requestId,authMode:'hosted',claimRequired:true,revision:'1'};}
    throw new Error('unexpected setup request '+path);
  }) as HttpLocalApi['request'];
  const target:SetupTarget={server:{id:pin.serverId,name:'Actual server',version:'1',signIn:'local',move:null,setupRequired:true,hostedConfigured:true,hostedAttached:false, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []},api,pin,draft:store,dispose:()=>{}};
  type Ports=NonNullable<ConstructorParameters<typeof BrowserSetupComposition>[2]>;
  const ports:Ports={sessions:()=>sessions,environment:()=>({storage:sessions.connectionStorage(pin.serverId)}),restore:async()=>({api,session}),remember:rememberNativeSession,
    draft:()=>({...store,assertAvailable:()=>{if(!secure)throw new Error('secure storage required');}}),
    claim:()=>({prepare:async()=>{claimCalls++;assert.equal((await sessions.read()).servers.length,0);return 'https://accounts.example/claim#code=actual';},cancel:async()=>{}})};
  const navigated:string[]=[];
  const flow=new BrowserSetupComposition('https://accounts.example',url=>navigated.push(url),ports);
  const connect=()=>flow.connect(async()=>target,new AbortController().signal);
  return {flow,target,session,sessions,store,calls,navigated,ports,snapshot,connect,draft:()=>draft,
    hosted:()=>{draft={...draft!,authMode:'hosted'};},insecure:()=>{secure=false;},get claimCalls(){return claimCalls;},get initializes(){return initializes;},
    replaceSelection:()=>{record={...record,revision:record.revision+1};}};
}

test('an admitted setup owner alone can create and scan the real first library',async t=>{
 const f=await fixture(t);await f.connect();
 await assert.rejects(f.flow.addLibrary({kind:'movie',name:'Movies',path:'/media'}));assert.equal(f.calls.length,0);
 await f.flow.acceptLocal(f.target,f.session);assert.equal(f.draft(),undefined);assert.equal((await f.sessions.selected())?.connection.session.sessionFamilyId,f.session.sessionFamilyId);
 await f.flow.addLibrary({kind:'movie',name:'Movies',path:'/media'});
 assert.deepEqual(f.calls.filter(v=>v.path.startsWith('/v1/libraries')).map(v=>v.path),['/v1/libraries','/v1/libraries/actual-library/scans']);f.flow.dispose();
});
test('a changed selection refuses setup admission and preserves the private draft',async t=>{
 const f=await fixture(t);await f.connect();f.replaceSelection();await assert.rejects(f.flow.acceptLocal(f.target,f.session));
 assert.equal((await f.sessions.read()).servers.length,0);assert.ok(f.draft());f.flow.dispose();
});
test('Hosted setup produces only pending state and hands off after the protected claim',async t=>{
 const f=await fixture(t);f.hosted();await f.connect();await f.flow.continueHosted(f.target,{name:'Actual server'});
 assert.equal(f.initializes,1);assert.equal(f.claimCalls,1);assert.equal((await f.sessions.read()).servers.length,0);assert.deepEqual(f.navigated,['https://accounts.example/claim#code=actual']);f.flow.dispose();
});
test('an insecure Hosted context refuses before secret issuance, initialization or navigation',async t=>{
 const f=await fixture(t);f.hosted();await f.connect();f.insecure();await assert.rejects(f.flow.continueHosted(f.target,{name:'Actual server'}),/secure storage/);
 assert.equal(f.initializes,0);assert.equal(f.claimCalls,0);assert.equal(f.calls.length,0);assert.deepEqual(f.navigated,[]);f.flow.dispose();
});
test('disposal retires a late local admission rather than retaining an unexposed family',async t=>{
 const f=await fixture(t);
 const flow=new BrowserSetupComposition('https://accounts.example',()=>{}, {...f.ports,remember:async(...args)=>{await rememberNativeSession(...args);flow.dispose();}});
 await flow.connect(async()=>f.target,new AbortController().signal);await assert.rejects(flow.acceptLocal(f.target,f.session));
 assert.equal(await f.sessions.selected(),undefined);assert.ok(f.draft());assert.ok(f.calls.some(v=>v.path==='/v1/sessions/current'));
});

// Source regression vectors for the staged auth1d continuation. Execution belongs
// to the normal checkpoint; these do not qualify setup or return at runtime.
test('a staged retry keeps the original request and name, then spends its own failure', async t => {
 const f=await fixture(t);f.hosted();let claims=0;
 const navigated:string[]=[];
 const composition=new BrowserSetupComposition('https://accounts.example',url=>navigated.push(url),{...f.ports,claim:()=>({
  prepare:async()=>{if(++claims===1)throw new ApiError(503,'claim_unavailable','unavailable',true);return 'https://accounts.example/claim#code=owned';},cancel:async()=>{},
 })});t.after(()=>composition.dispose());
 await composition.connect(async()=>f.target,new AbortController().signal);
 let failure:unknown;
 try {await composition.continueHosted(f.target,{name:'Original name'});} catch(error) {failure=error;}
 assert.ok(failure instanceof SetupStageFailure);assert.equal(failure.stage,'claim');assert.ok(failure.retry);
 const original=f.draft(), first=f.calls.find(call=>call.path==='/v1/setup');
 await failure.retry();
 assert.equal(claims,2);assert.equal(navigated.length,1);assert.deepEqual(f.draft(),original);
 assert.deepEqual(f.calls.filter(call=>call.path==='/v1/setup').map(call=>call.body),[first!.body,first!.body]);
 const before=f.calls.length;
 await assert.rejects(failure.retry());assert.equal(f.calls.length,before);assert.equal(navigated.length,1);
});

for(const replacement of ['selection','opening'] as const) test('a staged '+replacement+' replacement refuses before retry I/O',async t=>{
 const f=await fixture(t);f.hosted();let active=true,claims=0;
 const composition=new BrowserSetupComposition('https://accounts.example',()=>assert.fail('late navigation'),{...f.ports,claim:()=>({
  prepare:async()=>{claims++;throw new ApiError(503,'claim_unavailable','unavailable',true);},cancel:async()=>{},
 })},()=>active);t.after(()=>composition.dispose());
 await composition.connect(async()=>f.target,new AbortController().signal);
 let failure:unknown;
 try {await composition.continueHosted(f.target,{name:'Original name'});} catch(error) {failure=error;}
 assert.ok(failure instanceof SetupStageFailure);assert.ok(failure.retry);
 const before=f.calls.length, draft=f.draft();
 if(replacement==='selection')f.replaceSelection();else active=false;
 await assert.rejects(failure.retry());
 assert.equal(f.calls.length,before);assert.equal(claims,1);assert.deepEqual(f.draft(),draft);
});

test('local recovery cannot borrow a replacement selection and receives the original abort',async t=>{
 const f=await fixture(t);f.hosted();let local=0;let capturedSignal:AbortSignal|undefined;
 const composition=new BrowserSetupComposition('https://accounts.example',()=>assert.fail('unexpected navigation'),{...f.ports,claim:()=>({
  prepare:async(_api,_origin,opening)=>{
   assert.ok(opening);capturedSignal=opening.signal;
   opening.capture(Object.freeze({retry:async()=>{throw new Error('not used');},useLocal:async()=>{local++;}}));
   throw new ApiError(503,'claim_unavailable','unavailable',true);
  },cancel:async()=>{},
 })});t.after(()=>composition.dispose());
 await composition.connect(async()=>f.target,new AbortController().signal);
 let failure:unknown;
 try {await composition.continueHosted(f.target,{name:'Original name'});} catch(error) {failure=error;}
 assert.ok(failure instanceof SetupStageFailure);assert.ok(failure.useLocal);assert.ok(capturedSignal);
 f.replaceSelection();const before=f.calls.length;
 await assert.rejects(failure.useLocal());assert.equal(local,0);assert.equal(f.calls.length,before);
 composition.dispose();assert.equal(capturedSignal.aborted,true);
});
