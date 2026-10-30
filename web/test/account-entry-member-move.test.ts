import test from 'node:test';
import assert from 'node:assert/strict';
import {AppSessionStore, type AppSessionRecord} from '@core/app-sessions.ts';
import {HttpLocalApi} from '@core/index.ts';
import {ServerSignInProbe} from '@core/sign-in.ts';
import {BrowserAccountEntry} from '../src/platform/account-entry.ts';

const origin='https://member.example',hostedOrigin='https://account.example';
const serverId='srv_'+'A'.repeat(43),pin={serverId,publicKey:'p'.repeat(43),fingerprint:'f'.repeat(43)};
const code='C'.repeat(43),ticket='T'.repeat(43),installation='b909a633-f70f-470a-b096-b0f69d90cabe';
async function fixture(move:'to_portico'|null='to_portico'){
 let raw:AppSessionRecord|undefined,expiresAt=new Date(Date.now()+60000).toISOString(),passwordGate:Promise<void>|undefined,prepareGate:Promise<void>|undefined;
 let passwordStarted:()=>void=()=>{},prepareStarted:()=>void=()=>{},releasePassword:()=>void=()=>{},releasePrepare:()=>void=()=>{},cancels=0,remembers=0;
 const calls:{path:string;method?:string;body?:unknown}[]=[],prepares:unknown[][]=[];
 const store=new AppSessionStore({read:async()=>raw,change:async update=>{raw=update(raw);}});
 const system={id:serverId,name:'Actual server',version:'1',signIn:'portico',move,setupRequired:false,hostedConfigured:true,hostedAttached:true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
 const connection={pin,getSnapshot:()=>({phase:'ready'}),dispose:()=>{}};
 const api=new HttpLocalApi(origin,'');api.getRouteConnection=()=>connection as never;
 api.request=(async(path,method,body)=>{
  calls.push({path,method,body});
  if(path==='/v1/system')return system;
  if(path==='/v1/auth/move/code')return {username:'old-member',signIn:'portico',expiresAt};
  if(path==='/v1/auth/move/password'){passwordStarted();await passwordGate;return {ticket,expiresAt};}
  throw new Error('Unexpected account mutation: '+path);
 }) as HttpLocalApi['request'];
 const controller={prepare:async(...args:unknown[])=>{prepares.push(args);prepareStarted();await prepareGate;return hostedOrigin+'/#signIn=canonical';},finish:async()=>{throw new Error('No callback in initiation');},completeChallenge:async()=>{throw new Error('No factor in initiation');},cancel:async()=>{cancels++;}};
 const entry=new BrowserAccountEntry({store,actions:{} as never,environment:()=>({storage:store.connectionStorage(serverId)}),
  restore:async()=>{throw new Error('No generic restore for move proof');},inspect:async()=>pin,
  connect:(async()=>({api,connection})) as never,remember:async()=>{remembers++;},
  registered:()=>({controller,installationId:()=>installation}),
 },new ServerSignInProbe(async()=>Response.json(system)));
 await entry.inspect(origin);assert.equal(entry.getSnapshot().phase,'portico');
 return {entry,store,calls,prepares,system,get cancels(){return cancels;},get remembers(){return remembers;},
  expire:()=>{expiresAt='2000-01-01T00:00:00Z';},
  holdPassword:()=>{passwordGate=new Promise(resolve=>{releasePassword=resolve;});return new Promise<void>(resolve=>{passwordStarted=resolve;});},releasePassword:()=>releasePassword(),
  holdPrepare:()=>{prepareGate=new Promise(resolve=>{releasePrepare=resolve;});return new Promise<void>(resolve=>{prepareStarted=resolve;});},releasePrepare:()=>releasePrepare()};
}
test('a real move-code preview reaches protected member handoff with only that private code',async()=>{
 const f=await fixture();await f.entry.previewMoveCode(code);assert.equal(f.entry.getSnapshot().phase,'move-portico');
 assert.equal(await f.entry.preparePorticoSignIn(hostedOrigin),hostedOrigin+'/#signIn=canonical');
 assert.equal(f.prepares.length,1);assert.deepEqual(f.prepares[0].slice(1),[origin,hostedOrigin,installation,{moveCode:code}]);
 assert.equal(f.remembers,0);assert.equal(await f.store.selected(),undefined);
 assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()),new RegExp(code+'|'+ticket));f.entry.dispose();
});
test('the old password obtains only a ticket and forwards it through the existing protected return',async()=>{
 const f=await fixture();assert.equal(await f.entry.proveLocalMovePassword('old-member','old-password'),true);
 assert.equal(f.entry.getSnapshot().phase,'move-portico');
 assert.deepEqual(f.calls.find(c=>c.path==='/v1/auth/move/password')?.body,{username:'old-member',password:'old-password'});
 assert.equal(await f.entry.preparePorticoSignIn(hostedOrigin),hostedOrigin+'/#signIn=canonical');
 assert.deepEqual(f.prepares[0][4],{moveTicket:ticket});assert.equal(f.remembers,0);assert.equal(await f.store.selected(),undefined);
 assert.doesNotMatch(JSON.stringify(f.entry.getSnapshot()),/old-password|TTTT/);f.entry.dispose();
});
test('an ordinary Portico server never receives an old local password or a fabricated move proof',async()=>{
 const f=await fixture(null);assert.equal(await f.entry.proveLocalMovePassword('member','old-password'),false);
 assert.equal(f.calls.some(c=>c.path==='/v1/auth/move/password'),false);
 assert.equal(await f.entry.preparePorticoSignIn(hostedOrigin),hostedOrigin+'/#signIn=canonical');assert.deepEqual(f.prepares[0][4],{});f.entry.dispose();
});
test('an expired move proof refuses before any Hosted handoff',async()=>{
 const f=await fixture();f.expire();assert.equal(await f.entry.proveLocalMovePassword('member','old-password'),false);
 assert.equal(f.prepares.length,0);assert.equal(f.entry.getSnapshot().phase,'portico');f.entry.dispose();
});
test('a selection change while proving the old password cannot publish or hand off the late ticket',async()=>{
 const f=await fixture(),started=f.holdPassword();const pending=f.entry.proveLocalMovePassword('member','old-password');await started;
 await f.store.signOutServer(serverId,'unselected-family');f.releasePassword();assert.equal(await pending,false);
 assert.equal(f.entry.getSnapshot().phase,'portico');assert.equal(f.prepares.length,0);f.entry.dispose();
});
test('cancel during old-password proof cannot revive the member flow',async()=>{
 const f=await fixture(),started=f.holdPassword();const pending=f.entry.proveLocalMovePassword('member','old-password');await started;
 f.entry.cancel();f.releasePassword();assert.equal(await pending,false);assert.equal(f.entry.getSnapshot().phase,'welcome');assert.equal(f.prepares.length,0);f.entry.dispose();
});
test('a selected revision changed after code preview refuses before creating protected pending',async()=>{
 const f=await fixture();await f.entry.previewMoveCode(code);await f.store.signOutServer(serverId,'unselected-family');
 assert.equal(await f.entry.preparePorticoSignIn(hostedOrigin),undefined);assert.equal(f.prepares.length,0);f.entry.dispose();
});
test('a selection change during protected prepare cancels that attempt and returns no navigation',async()=>{
 const f=await fixture();await f.entry.previewMoveCode(code);const started=f.holdPrepare(),pending=f.entry.preparePorticoSignIn(hostedOrigin);await started;
 await f.store.signOutServer(serverId,'unselected-family');f.releasePrepare();assert.equal(await pending,undefined);assert.equal(f.cancels,1);assert.equal(f.remembers,0);f.entry.dispose();
});

test('a stored old-password ticket expires without falling back to ordinary sign-in',async t=>{
 const f=await fixture();assert.equal(await f.entry.proveLocalMovePassword('member','old-password'),true);
 const future=Date.now()+120000;t.mock.method(Date,'now',()=>future);
 assert.equal(await f.entry.preparePorticoSignIn(hostedOrigin),undefined);assert.equal(f.prepares.length,0);f.entry.dispose();
});
test('expiry during protected preparation cancels the attempt before returning a URL',async t=>{
 const f=await fixture();await f.entry.previewMoveCode(code);const started=f.holdPrepare(),pending=f.entry.preparePorticoSignIn(hostedOrigin);await started;
 const future=Date.now()+120000;t.mock.method(Date,'now',()=>future);f.releasePrepare();
 assert.equal(await pending,undefined);assert.equal(f.cancels,1);f.entry.dispose();
});

test('a captured move link keeps its expected identity through entry proof before code preview',async()=>{
 const f=await fixture();await f.entry.inspectMoveLink(origin,{code,id:serverId});
 assert.equal(f.entry.getSnapshot().phase,'move-portico');
 assert.equal(f.calls.filter(c=>c.path==='/v1/auth/move/code').length,1);assert.equal(f.remembers,0);f.entry.dispose();
});
test('a different proved server never receives a move link code for the expected identity',async()=>{
 const f=await fixture();await f.entry.inspectMoveLink(origin,{code,id:'srv_'+'C'.repeat(42)+'A'});
 assert.equal(f.entry.getSnapshot().phase,'address');
 assert.equal(f.calls.some(c=>c.path==='/v1/auth/move/code'),false);assert.equal(f.prepares.length,0);assert.equal(f.remembers,0);f.entry.dispose();
});
