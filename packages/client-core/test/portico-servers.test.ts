import test from 'node:test';import assert from 'node:assert/strict';
import {ApiError,ServerListWatcher,SERVER_LIST_INTERVAL_MS,SERVER_LIST_SPREAD_MS,hostedGate,leavePorticoServer,removePorticoServer,serverRemovalClaim,isAccessRefused,isSessionMigrated,isClockSkew,type ServerListRecord} from '../src/index.ts';

const clock=(start=1_800_000_000_000)=>{let now=start,random=0.5;return{now:()=>now,random:()=>random,advance:(ms:number)=>{now+=ms;},roll:(v:number)=>{random=v;}};};
const answer=(version:string,items:unknown[]=[])=>({items,version,watch:'d2F0Y2g.mac'});
const memory=()=>{let v:ServerListRecord|null=null;return{load:()=>v,save:(r:ServerListRecord|null)=>{v=r;}};};

let origins=0;const origin=()=>`https://hosted-${++origins}.example`;

test('a device checks on foreground only when due, and a 304 costs nothing more',async()=>{
 const c=clock(),o=origin(),calls:string[]=[];let lists=0;
 const w=new ServerListWatcher({hostedOrigin:o,clock:c,storage:memory(),listServers:async()=>{lists++;return answer('7');},onServers:()=>{},fetcher:async input=>{calls.push(String(input));return new Response(null,{status:304});}});
 assert.equal(await w.foreground(),'changed','no record yet: the list is fetched once');
 assert.equal(lists,1);
 assert.equal(await w.foreground(),'not-due');
 c.advance(SERVER_LIST_INTERVAL_MS-SERVER_LIST_SPREAD_MS-1);
 assert.equal(await w.foreground(),'not-due','nothing is checked within the interval');
 c.advance(2*SERVER_LIST_SPREAD_MS+2);
 assert.equal(await w.foreground(),'unchanged');
 assert.deepEqual(calls,[o+'/v1/server-list/d2F0Y2g.mac/7']);
 assert.equal(lists,1,'an unchanged list is not fetched');
});

test('a moved version fetches the list once and hands it over; Refresh Servers checks now',async()=>{
 const c=clock(),o=origin();let version='1',seen:unknown[]=[];
 const w=new ServerListWatcher({hostedOrigin:o,clock:c,listServers:async()=>answer(version,[{id:'srv_a'}]),onServers:a=>{seen=[...a.items];},fetcher:async input=>String(input).endsWith('/'+version)?new Response(null,{status:304}):new Response(JSON.stringify({version}),{status:200})});
 w.adopt(answer('1'));
 assert.equal(await w.refresh(),'unchanged','Refresh Servers ignores the schedule');
 version='2';
 assert.equal(await w.refresh(),'changed');
 assert.deepEqual(seen,[{id:'srv_a'}]);
 assert.equal(w.getRecord()?.version,'2');
});

test('a failing Hosted is met with backoff: automatic checks wait, nothing loops, nothing signs out',async()=>{
 const c=clock(),o=origin();let hits=0;
 const w=new ServerListWatcher({hostedOrigin:o,clock:c,listServers:async()=>answer('1'),onServers:()=>{},fetcher:async()=>{hits++;return new Response('{}',{status:503,headers:{'Retry-After':'120'}});}});
 w.adopt(answer('1'));c.advance(SERVER_LIST_INTERVAL_MS+SERVER_LIST_SPREAD_MS);
 assert.equal(await w.foreground(),'deferred');
 assert.ok(hostedGate(o).blockedFor()>=120000,'Retry-After is a floor');
 for(let i=0;i<5;i++)assert.equal(await w.foreground(),'not-due');
 assert.equal(hits,1,'no retry storm');
 // A person asking still gets an answer (the error), and the record is kept.
 await assert.rejects(w.refresh(),(e:unknown)=>e instanceof ApiError&&e.status===503);
 assert.equal(w.getRecord()?.version,'1');
});

test('a watch token that no longer verifies is replaced by the list answer',async()=>{
 const c=clock(),o=origin();let lists=0;
 const w=new ServerListWatcher({hostedOrigin:o,clock:c,listServers:async()=>{lists++;return {...answer('3'),watch:'bmV3.mac'};},onServers:()=>{},fetcher:async()=>new Response('{}',{status:404})});
 w.adopt(answer('3'));
 assert.equal(await w.refresh(),'changed');
 assert.equal(lists,1);assert.equal(w.getRecord()?.watch,'bmV3.mac');
});

test('a check that never answers is abandoned, so later checks are not left waiting on it',{timeout:5000},async()=>{
 const c=clock(),o=origin();let hits=0,aborted=0;
 const w=new ServerListWatcher({hostedOrigin:o,clock:c,timeoutMs:20,listServers:async()=>answer('1'),onServers:()=>{},fetcher:(_input,init)=>{
  // The first request stalls in the transport and does not even honour its signal.
  if(++hits===1){init?.signal?.addEventListener('abort',()=>{aborted++;});return new Promise<Response>(()=>{});}
  return Promise.resolve(new Response(null,{status:304}));
 }});
 w.adopt(answer('1'));c.advance(SERVER_LIST_INTERVAL_MS+SERVER_LIST_SPREAD_MS);
 const first=w.foreground(),joined=w.refresh();
 assert.equal(await first,'deferred');
 assert.equal(await joined,'deferred','a caller who joined the stalled check is released with it');
 assert.equal(aborted,1,'the stalled request is cancelled');
 assert.ok(hostedGate(o).blockedFor()>0,'the stall counts as a failure for automatic work');
 assert.equal(await w.refresh(),'unchanged','the next check makes its own request');
 assert.equal(hits,2);
});

test('a list fetch that never answers is abandoned too',{timeout:5000},async()=>{
 const c=clock(),o=origin();let lists=0;
 const w=new ServerListWatcher({hostedOrigin:o,clock:c,timeoutMs:20,listServers:()=>++lists===1?new Promise(()=>{}):Promise.resolve(answer('2')),onServers:()=>{},fetcher:async()=>new Response(null,{status:304})});
 await assert.rejects(w.refresh(),(e:unknown)=>e instanceof ApiError&&e.retryable);
 assert.equal(await w.refresh(),'changed');
 assert.equal(w.getRecord()?.version,'2');
});

test('a refused member and a migrated session are told apart',()=>{
 assert.ok(isAccessRefused(new ApiError(403,'access_refused','no',false)));
 assert.ok(!isAccessRefused(new ApiError(401,'unauthorized','no',false)));
 assert.ok(isSessionMigrated(new ApiError(401,'session_migrated','moved',false)));
 assert.ok(!isSessionMigrated(new ApiError(401,'unauthorized','no',false)));
 assert.ok(isClockSkew(new ApiError(401,'clock_skew','clock',false)));
});

test('a refresh refused as session_migrated is recognised for silent re-admission',async()=>{
 const {SessionRefreshError}=await import('../src/server-connections.ts');
 const {isSessionMigrated,isAccessRefused}=await import('../src/portico-servers.ts');
 assert.equal(isSessionMigrated(new SessionRefreshError('session_migrated','moved',true)),true);
 assert.equal(isSessionMigrated(new SessionRefreshError('refresh_refused','ended',true)),false);
 assert.equal(isAccessRefused(new SessionRefreshError('session_migrated','moved',true)),false);
});
