import test from 'node:test';
import assert from 'node:assert/strict';
import {SessionSelectionService,type SessionSelectionApi,type SelectedSession} from '../src/session-selection.ts';
import type {LocalSession} from '../src/index.ts';
const account={accountId:'account',sessionId:'family'};
const server={id:'server',name:'Server',baseUrl:'https://server.example',publicKey:'key',policyRevision:2};
const directory={items:[server]};
const admission=()=>({accountId:'account',serverId:'server',policyRevision:2,localRevision:1,items:[{id:'profile',name:'Profile',eligible:true},{id:'denied',name:'Other',eligible:false,unavailableReason:'membership_revoked'}]});
const session=():LocalSession=>({accessToken:'private-test-token',expiresAt:'2099-01-01T00:00:00Z',sessionFamilyId:'local-family',tokenGeneration:'1',authorizationHorizon:'2099-01-02T00:00:00Z',viewer:{authority:'hosted',accountId:'account',profileId:'profile',serverId:'server',role:'member'}});
function setup(override:Partial<SessionSelectionApi>={},timeoutMs=1000){const offered:SelectedSession[]=[],revoked:LocalSession[]=[];const api:SessionSelectionApi={listServers:async()=>directory,serverProfiles:async()=>admission(),attach:async()=>session(),revokeLocal:async(s)=>{revoked.push(s);},...override};const service=new SessionSelectionService({api,account,onSelected:r=>{offered.push(r);},timeoutMs});return {service,api,offered,revoked};}
async function ready(s:SessionSelectionService){await s.loadServers();await s.selectServer('server');}
function deferred<T=any>(){let resolve!:(v:T)=>void;let reject!:(e:unknown)=>void;const promise=new Promise<T>((a,b)=>{resolve=a;reject=b;});return {promise,resolve,reject};}
const tick=()=>new Promise(r=>setTimeout(r,0));
test('server-authored eligibility and exact hosted tuple yield one offered session without credential snapshot',async()=>{const {service:s,offered,revoked}=setup();await ready(s);assert.equal(s.getSnapshot().profiles!.items[1].unavailableReason,'membership_revoked');assert.throws(()=>s.switchProfile('denied'));await s.switchProfile('profile');assert.equal(offered.length,1);assert.deepEqual(offered[0].context,{...account,serverId:'server',profileId:'profile'});assert.equal(s.getSnapshot().phase,'selected');assert.equal(JSON.stringify(s.getSnapshot()).includes('private-test-token'),false);assert.equal(revoked.length,0);});
test('all mismatched returned identity dimensions reject and revoke captured new session',async()=>{for(const patch of [{authority:'local'},{accountId:'other'},{profileId:'other'},{serverId:'other'}]){const created={...session(),viewer:{...session().viewer,...patch}};const {service:s,offered,revoked}=setup({attach:async()=>created});await ready(s);await s.switchProfile('profile');assert.equal(s.getSnapshot().error!.code,'session_identity_mismatch');assert.equal(offered.length,0);assert.equal(revoked[0],created);}});
test('cancel fences ignored-abort attach and revokes its late result once',async()=>{const d=deferred<LocalSession>();let signal:AbortSignal|undefined;const {service:s,offered,revoked}=setup({attach:async(_i,a)=>{signal=a;return d.promise;}});await ready(s);const task=s.switchProfile('profile');await tick();s.cancel();await task;assert.equal(signal!.aborted,true);d.resolve(session());await tick();assert.equal(offered.length,0);assert.equal(revoked.length,1);assert.equal(s.getSnapshot().phase,'idle');});
test('late profile list cannot enter newly scoped account',async()=>{const d=deferred();const {service:s,api}=setup({serverProfiles:async()=>d.promise});await s.loadServers();const old=s.selectServer('server');s.setAccount({accountId:'other',sessionId:'other-family'},api);await old;d.resolve(admission());await tick();assert.equal(s.getSnapshot().profiles,null);assert.equal(s.getSnapshot().account.accountId,'other');});
test('account-family change revokes late new session without callback',async()=>{const d=deferred<LocalSession>();const {service:s,api,revoked,offered}=setup({attach:async()=>d.promise});await ready(s);const old=s.switchProfile('profile');await tick();s.setAccount({...account,sessionId:'new-family'},api);await old;d.resolve(session());await tick();assert.equal(revoked.length,1);assert.equal(offered.length,0);});
test('duplicate server lists, profile loads and attaches are single flight',async()=>{const dl=deferred(),dp=deferred(),da=deferred<LocalSession>();let lists=0,reads=0,writes=0;const {service:s,offered}=setup({listServers:async()=>{lists++;return dl.promise;},serverProfiles:async()=>{reads++;return dp.promise;},attach:async()=>{writes++;return da.promise;}});const a=s.loadServers();assert.equal(a,s.loadServers());dl.resolve(directory);await a;const b=s.selectServer('server');assert.equal(b,s.selectServer('server'));dp.resolve(admission());await b;const c=s.switchProfile('profile');assert.equal(c,s.switchProfile('profile'));da.resolve(session());await c;assert.deepEqual([lists,reads,writes,offered.length],[1,1,1,1]);});
test('lost attach response requires read-only reconciliation; never automatic replay',async()=>{let calls=0;const {service:s,offered}=setup({attach:async()=>{calls++;if(calls===1)throw new Error('connection lost');return session();}});await ready(s);await s.switchProfile('profile');assert.equal(s.getSnapshot().error!.code,'attach_ambiguous');assert.equal(s.getSnapshot().profiles,null);assert.throws(()=>s.switchProfile('profile'));await s.reconcile();assert.equal(calls,1);assert.equal(offered.length,0);assert.equal(s.getSnapshot().phase,'profiles');await s.switchProfile('profile');assert.equal(calls,2);assert.equal(offered.length,1);s.dispose();});
test('revoked eligibility on reconciliation cannot be overridden by old selected profile',async()=>{let data=admission();const {service:s}=setup({serverProfiles:async()=>data});await ready(s);data={...data,policyRevision:3,items:data.items.map(p=>({...p,eligible:false,unavailableReason:'membership_revoked'}))};await s.reconcile();assert.throws(()=>s.switchProfile('profile'));assert.equal(s.getSnapshot().profiles!.policyRevision,3);s.dispose();});
test('malicious, duplicate, oversized or inconsistent admission is rejected',async()=>{for(const patch of [{accountId:'wrong'},{serverId:'wrong'},{policyRevision:1},{localRevision:-1},{items:[{id:'profile',name:'P',eligible:true,unavailableReason:'membership_revoked'}]},{items:[{id:'profile',name:'P',eligible:false}]},{items:Array(21).fill({id:'p',name:'P',eligible:true})}]){const {service:s}=setup({serverProfiles:async()=>({...admission(),...patch})});await ready(s);assert.equal(s.getSnapshot().error!.code,'invalid_selection');assert.equal(s.getSnapshot().profiles,null);}});
test('denial and failed server selection do not offer a new viewer',async()=>{const {service:s,offered}=setup({attach:async()=>{throw Object.assign(new Error('denied'),{code:'membership_revoked'});}});await ready(s);await s.switchProfile('profile');assert.equal(s.getSnapshot().error!.code,'membership_revoked');assert.equal(offered.length,0);assert.equal(s.getSnapshot().selected,null);});
test('timeout resolves even for ignored abort, late session revoked, no attach loop',async()=>{const d=deferred<LocalSession>();let writes=0;const {service:s,revoked}=setup({attach:async()=>{writes++;return d.promise;}},5);await ready(s);await s.switchProfile('profile');assert.equal(s.getSnapshot().error!.code,'attach_ambiguous');d.resolve(session());await tick();assert.equal(revoked.length,1);assert.equal(writes,1);});
test('dispose revokes late session at captured server rather than a new selection',async()=>{const d=deferred<LocalSession>();const targets:string[]=[];const {service:s,offered}=setup({attach:async()=>d.promise,revokeLocal:async(_s,target)=>{targets.push(target.id);}});await ready(s);const old=s.switchProfile('profile');await tick();s.dispose();await old;d.resolve(session());await tick();assert.deepEqual(targets,['server']);assert.equal(offered.length,0);});
test('cancellation before dispatch never invokes non-idempotent attach',async()=>{let writes=0;const {service:s}=setup({attach:async()=>{writes++;return session();}});await ready(s);const old=s.switchProfile('profile');s.cancel();await old;assert.equal(writes,0);});
test('expired created session is rejected and revoked, never persisted or offered',async()=>{const {service:s,revoked,offered}=setup({attach:async()=>({...session(),expiresAt:'2000-01-01T00:00:00Z'})});await ready(s);await s.switchProfile('profile');assert.equal(revoked.length,1);assert.equal(offered.length,0);assert.equal(s.getSnapshot().error!.code,'invalid_selection');});

test('asynchronous handoff receives a generation fence that stays false after cancel and reopen',async()=>{
 const gate=deferred(),entered=deferred();let oldCurrent:(()=>boolean)|undefined,delivered=0,revoked=0;
 const s=new SessionSelectionService({account,api:{listServers:async()=>directory,serverProfiles:async()=>admission(),attach:async()=>session(),revokeLocal:async()=>{revoked++;}},onSelected:async result=>{oldCurrent=result.isCurrent;entered.resolve();await gate.promise;if(!result.isCurrent?.())throw new Error('selection changed during persistence');delivered++;}});
 await ready(s);const selection=s.switchProfile('profile');await entered.promise;assert.equal(oldCurrent?.(),true);s.cancel();await s.loadServers();assert.equal(oldCurrent?.(),false);gate.resolve();await selection;assert.equal(delivered,0);assert.equal(revoked,1);assert.equal(s.getSnapshot().phase,'servers');s.dispose();
});
test('selection awaits the durable asynchronous handoff, and rejects its failure',async()=>{
 const gate=deferred<void>(),began=deferred<void>();const f=setup();
 const s=new SessionSelectionService({api:f.api,account,onSelected:async()=>{began.resolve();await gate.promise;}});
 await ready(s);const selected=s.switchProfile('profile');await began.promise;assert.equal(s.getSnapshot().phase,'switching');
 gate.reject(new Error('secure credential save failed'));await selected;
 assert.equal(s.getSnapshot().phase,'error');assert.equal(s.getSnapshot().selected,null);assert.equal(f.revoked.length,1);
});
test('unmount during the accepting handoff does not revoke a successfully published family',async()=>{
 const gate=deferred<void>(),began=deferred<void>();const f=setup();
 const s=new SessionSelectionService({api:f.api,account,onSelected:async()=>{began.resolve();await gate.promise;}});
 await ready(s);const selected=s.switchProfile('profile');await began.promise;s.dispose();gate.resolve();await selected;
 assert.equal(f.revoked.length,0);
});
test('P09 signed route envelope and verified logical origin survive the P10 PIN handoff',async()=>{
 const routed={...server,baseUrl:'',routes:{payload:'signed-routes',signature:'signature'}};let request:any;
 const f=setup({listServers:async()=>({items:[routed]}),attach:async input=>{request=input;return session();},selectedServer:(_session,selected)=>({...selected,baseUrl:'https://verified.example'})});
 await ready(f.service);await f.service.switchProfile('profile',{pin:'0123',trust:true});
 assert.deepEqual(request.server.routes,routed.routes);assert.equal(request.selection.pin,'0123');assert.equal(request.selection.trust,true);
 assert.equal(f.offered[0].server.baseUrl,'https://verified.example');assert.deepEqual(f.offered[0].server.routes,routed.routes);
});
test('route adapter cannot replace the selected server cryptographic identity',async()=>{
 const f=setup({selectedServer:(_session,selected)=>({...selected,id:'wrong-server'})});await ready(f.service);await f.service.switchProfile('profile');
 assert.equal(f.service.getSnapshot().error?.code,'session_identity_mismatch');assert.equal(f.offered.length,0);assert.equal(f.revoked.length,1);
});
test('malformed route envelopes are rejected instead of silently dropping remote-only routes',async()=>{
 for(const routes of [null,{}, {payload:'',signature:'sig'},{payload:'data',signature:''},{payload:'data',signature:3}]){
  const f=setup({listServers:async()=>({items:[{...server,baseUrl:'',routes}]}) as any});await f.service.loadServers();assert.equal(f.service.getSnapshot().error?.code,'invalid_selection');
 }
});

test('reversible chooser effect cleanup preserves replay and fences old directory results',async()=>{
 const old=deferred<{items:typeof server[]}>();let calls=0;
 const f=setup({listServers:async()=>++calls===1?old.promise:directory});
 const first=f.service.loadServers();f.service.cancel();await first;
 await f.service.loadServers();old.resolve({items:[{...server,id:'stale-server'}]});await tick();
 assert.equal(f.service.getSnapshot().phase,'servers');assert.equal(f.service.getSnapshot().servers[0].id,'server');
 await f.service.selectServer('server');await f.service.switchProfile('profile');assert.equal(f.offered.length,1);
});

test('late route-capture rejection still revokes the unoffered candidate after cancellation',async()=>{
 const late=deferred<LocalSession>();const f=setup({attach:async()=>late.promise,selectedServer:()=>{throw new Error('route binding invalid');}});
 await ready(f.service);const selected=f.service.switchProfile('profile');await tick();f.service.cancel();await selected;
 late.resolve(session());await tick();assert.equal(f.offered.length,0);assert.equal(f.revoked.length,1);
});


test('ready profile notification can attach immediately without an old read clearing its flight',async()=>{
 const gate=deferred<LocalSession>();let writes=0,selected:Promise<void>|undefined;
 const f=setup({attach:async()=>{writes++;return gate.promise;}});
 f.service.subscribe(()=>{if(f.service.getSnapshot().phase==='profiles')selected=f.service.switchProfile('profile');});
 await f.service.loadServers();await f.service.selectServer('server');
 assert.ok(selected);assert.equal(f.service.getSnapshot().phase,'switching');
 assert.equal(f.service.switchProfile('profile'),selected);
 gate.resolve(session());await selected;assert.equal(writes,1);assert.equal(f.offered.length,1);
});

test('ready directory subscriber can begin profile selection before the list call returns',async()=>{
 const f=setup();let profiles:Promise<void>|undefined;
 f.service.subscribe(()=>{if(f.service.getSnapshot().phase==='servers')profiles=f.service.selectServer('server');});
 await f.service.loadServers();await profiles;
 assert.equal(f.service.getSnapshot().phase,'profiles');await f.service.switchProfile('profile');assert.equal(f.offered.length,1);
});

test('sole eligible unprotected profile auto-enters from the ready snapshot exactly once without directory re-reads',async()=>{
 let lists=0,reads=0,writes=0;
 const sole=()=>({accountId:'account',serverId:'server',policyRevision:2,localRevision:1,items:[{id:'profile',name:'Profile',eligible:true}]});
 const f=setup({listServers:async()=>{lists++;return directory;},serverProfiles:async()=>{reads++;return sole();},attach:async()=>{writes++;return session();}});
 f.service.subscribe(()=>{
  const snap=f.service.getSnapshot();
  if(snap.phase!=='profiles'||!snap.profiles)return;
  const items=snap.profiles.items;
  if(items.length===1&&items[0]!.eligible&&!items[0]!.pinRequired)void f.service.switchProfile(items[0]!.id);
 });
 await f.service.loadServers();
 await f.service.selectServer('server');
 await tick();
 assert.equal(f.service.getSnapshot().phase,'selected');
 assert.deepEqual([lists,reads,writes,f.offered.length],[1,1,1,1]);
 f.service.dispose();
});

test('sole PIN-protected or revoked profile never auto-enters; the PIN still gates the manual path',async()=>{
 let writes=0;
 const f=setup({serverProfiles:async()=>({accountId:'account',serverId:'server',policyRevision:2,localRevision:1,items:[{id:'profile',name:'Profile',eligible:true,pinRequired:true}]}),attach:async()=>{writes++;return session();}});
 f.service.subscribe(()=>{
  const snap=f.service.getSnapshot();
  if(snap.phase!=='profiles'||!snap.profiles)return;
  const items=snap.profiles.items;
  if(items.length===1&&items[0]!.eligible&&!items[0]!.pinRequired)void f.service.switchProfile(items[0]!.id);
 });
 await f.service.loadServers();
 await f.service.selectServer('server');
 await tick();
 assert.equal(writes,0);
 assert.equal(f.offered.length,0);
 await f.service.switchProfile('profile',{pin:'0000'});
 assert.equal(f.offered.length,1);
 f.service.dispose();
});

test('sole ineligible profile never auto-enters and a fresh reconcile re-reads its admission',async()=>{
 let data=admission();
 const f=setup({serverProfiles:async()=>data,attach:async()=>session()});
 let attempts=0;
 f.service.subscribe(()=>{
  const snap=f.service.getSnapshot();
  if(snap.phase!=='profiles'||!snap.profiles)return;
  const items=snap.profiles.items;
  if(items.length===1&&items[0]!.eligible&&!items[0]!.pinRequired){attempts++;void f.service.switchProfile(items[0]!.id);}
 });
 await f.service.loadServers();
 await f.service.selectServer('server');
 await tick();
 assert.equal(attempts,0);
 assert.equal(f.offered.length,0);
 data={...data,policyRevision:3,items:[{id:'profile',name:'Profile',eligible:true}]};
 await f.service.reconcile();
 await tick();
 assert.equal(attempts,1);
 assert.equal(f.offered.length,1);
 assert.equal(f.service.getSnapshot().profiles!.policyRevision,3);
 f.service.dispose();
});
