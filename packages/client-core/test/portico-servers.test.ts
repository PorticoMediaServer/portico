import test from 'node:test';import assert from 'node:assert/strict';
import {ApiError,ServerListWatcher,SERVER_LIST_INTERVAL_MS,SERVER_LIST_SPREAD_MS,hostedGate,porticoSignIn,acceptPorticoInvitation,transferPorticoOwnership,acceptPorticoCustody,leavePorticoServer,removePorticoServer,serverRemovalClaim,isAccessRefused,isSessionMigrated,isClockSkew,routeBase64,type ServerListRecord} from '../src/index.ts';
import {parseDirectSnapshot} from '../src/profile-management.ts';
import {webRouteCrypto,type RouteCrypto} from '../src/route-identity.ts';
import {acceptShortPorticoInvitation} from '../src/portico-servers.ts';

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

const assertionBody={payload:'eyJraW5kIjoicG9ydGljby5pZGVudGl0eS52MSJ9',signature:'c2ln',keyId:'key'};

// A server identity: the id is the digest of the public key, and the server
// signs its challenge answer with the private key (INT M7).
async function serverIdentity(){
 const pair=await crypto.subtle.generateKey('Ed25519',true,['sign','verify']) as CryptoKeyPair;
 const pub=new Uint8Array(await crypto.subtle.exportKey('raw',pair.publicKey));
 const context=new TextEncoder().encode('portico.server.identity.v1\0');const bytes=new Uint8Array(context.length+pub.length);bytes.set(context);bytes.set(pub,context.length);
 const id='srv_'+routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)));
 const fingerprint=routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256',pub)));
 const prove=async(challenge:string,nonce:string,claimed=id)=>{
  const payload=new TextEncoder().encode(JSON.stringify({kind:'portico.challenge-proof',version:'1',serverId:claimed,publicKey:routeBase64(pub),fingerprint,challenge,nonce,installationId:'i',issuedAt:'2026-09-23T00:00:00Z',expiresAt:'2026-09-23T00:05:00Z'}));
  return {payload:routeBase64(payload),signature:routeBase64(new Uint8Array(await crypto.subtle.sign('Ed25519',pair.privateKey,payload)))};
 };
 return {id,prove};
}
const me=await serverIdentity(),impostor=await serverIdentity();

const signIn={authority:'local',serverId:me.id,account:{id:'acc-local',username:'member',primaryProfileId:'p1',role:'member',revision:1,disabled:false,allowedLibraries:[],hostedAccountId:'acc_hosted'},profiles:[{id:'p1',name:'Member',primary:true,position:0,art:'blue',revision:1,pinRevision:1,pinRequired:false}],canManage:true, capabilities: {manageAccount:true,managePeople:false,manageServer:false,manageSignIn:false},
 session:{accessToken:'a'.repeat(43),refreshToken:'r'.repeat(43),deviceId:'d',installationId:'i'.repeat(22),expiresAt:'2099-01-01T00:00:00Z',viewer:{accountId:'acc-local',profileId:'p1',serverId:me.id,authority:'local',role:'member'},sessionFamilyId:'f',tokenGeneration:'1',authorizationHorizon:'2099-01-01T00:00:00Z'}};

const challenge='k'.repeat(32);
type Prover=(challenge:string,nonce:string)=>Promise<{payload:string;signature:string}>;
const fakeServer=(calls:unknown[],answer:unknown=signIn,prove:Prover=me.prove)=>({request:async<T,>(path:string,method?:string,body?:unknown)=>{
 calls.push([method,path,body]);
 if(path==='/v1/direct/portico-challenge')return {challenge,expiresAt:'2099-01-01T00:00:00Z',proof:await prove(challenge,(body as {nonce:string}).nonce)} as T;
 return answer as T;
}});

test('short invitation is a distinct verified-server capability and rejects long links before any request',async()=>{
 const hostedCalls:unknown[]=[],serverCalls:unknown[]=[];
 const hosted={request:async<T,>(path:string,method?:string,body?:unknown)=>{hostedCalls.push([method,path,body]);return assertionBody as T;}};
 const server=fakeServer(serverCalls);
 const result=await acceptShortPorticoInvitation(hosted,origin(),server,me.id,'0123-4567-89ab-cdef');
 assert.equal(result.kind,'signed-in');
 assert.deepEqual(serverCalls.at(-1),['POST','/v1/access/invitations/accept',{shortCode:'0123456789ABCDEF',porticoIdentity:assertionBody}]);
 const before=[hostedCalls.length,serverCalls.length];
 for(const raw of ['x'.repeat(43),'https://server.example/join?code=0123456789ABCDEF','0123-4567-89ab-cdei',' 0123456789ABCDEF'])await assert.rejects(acceptShortPorticoInvitation(hosted,origin(),server,me.id,raw));
 assert.deepEqual([hostedCalls.length,serverCalls.length],before);
 await assert.rejects(acceptPorticoInvitation(hosted,origin(),server,me.id,'0123456789ABCDEF'));
 assert.deepEqual([hostedCalls.length,serverCalls.length],before);
 const otherCalls:unknown[]=[];
 await assert.rejects(acceptShortPorticoInvitation(hosted,origin(),fakeServer(otherCalls,signIn,impostor.prove),me.id,'0123456789ABCDEF'));
 assert.equal(otherCalls.length,1,'an unproven server never receives the capability');
 assert.equal(hostedCalls.length,before[0],'an unproven server never obtains a Hosted assertion');
});

test('Portico sign-in answers the server\'s challenge with an identity assertion from Hosted',async()=>{
 const o=origin(),hostedCalls:unknown[]=[],serverCalls:unknown[]=[];
 const hosted={request:async<T,>(path:string,method?:string,body?:unknown)=>{hostedCalls.push([method,path,body]);return assertionBody as T;}};
 const server=fakeServer(serverCalls);
 const result=await porticoSignIn(hosted,o,server,me.id);
 assert.deepEqual(hostedCalls,[['POST','/v1/servers/'+me.id+'/identity',{challenge}]]);
 const nonce=(serverCalls[0] as [string,string,{nonce:string}])[2].nonce;
 assert.equal(nonce.length,43,'a fresh 32-byte nonce');
 assert.deepEqual(serverCalls,[['POST','/v1/direct/portico-challenge',{nonce}],['POST','/v1/direct/sign-in',{porticoIdentity:assertionBody}]]);
 assert.equal(result.kind,'signed-in');
 if(result.kind==='signed-in')assert.equal(result.snapshot.account.hostedAccountId,'acc_hosted');
 const code='c'.repeat(43);
 await acceptPorticoInvitation(hosted,o,server,me.id,code);
 assert.deepEqual(serverCalls.at(-1),['POST','/v1/access/invitations/accept',{code,porticoIdentity:assertionBody}]);
 await transferPorticoOwnership(hosted,o,server,me.id,'acc-heir',4);
 assert.deepEqual(serverCalls.at(-1),['POST','/v1/direct/ownership',{accountId:'acc-heir',expectedRevision:4,porticoIdentity:assertionBody}]);
 await acceptPorticoCustody(hosted,o,server,me.id);
 assert.deepEqual(hostedCalls.at(-1),['POST','/v1/servers/'+me.id+'/identity',{challenge,purpose:'custody'}]);
 assert.deepEqual(serverCalls.at(-1),['POST','/v1/direct/ownership/custody',{porticoIdentity:assertionBody}]);
 await leavePorticoServer(hosted,o,me.id);
 assert.deepEqual(hostedCalls.at(-1),['POST','/v1/servers/'+me.id+'/leave',{}]);
 const claim=serverRemovalClaim({claimGeneration:'7',credentialGeneration:'3'})!,op='op-'+'a'.repeat(20);
 await removePorticoServer(hosted,o,me.id,claim,op);
 assert.deepEqual(hostedCalls.at(-1),['DELETE','/v1/servers/'+me.id,{operationId:op,claimGeneration:'7',credentialGeneration:'3'}]);
 // P2-66 D-3: the exact claim the owner saw, never a default; a fresh, bounded operation id.
 const before=hostedCalls.length;
 for(const row of [{},{claimGeneration:'7'},{claimGeneration:'0',credentialGeneration:'3'},{claimGeneration:'7',credentialGeneration:'-1'},{claimGeneration:'07',credentialGeneration:'3'},{claimGeneration:7,credentialGeneration:3},{claimGeneration:'9223372036854775808',credentialGeneration:'1'}])assert.equal(serverRemovalClaim(row as never),undefined,JSON.stringify(row));
 await assert.rejects(removePorticoServer(hosted,o,me.id,{claimGeneration:'',credentialGeneration:'3'},op),/claim/);
 await assert.rejects(removePorticoServer(hosted,o,me.id,claim,'short'),/removal/);
 assert.equal(hostedCalls.length,before,'nothing is sent without the exact claim and a valid operation id');
});

test('a platform without WebCrypto (Hermes on Apple) passes its own route crypto to every Portico call',async()=>{
 const o=origin(),used:string[]=[];
 const hosted={request:async<T,>()=>assertionBody as T};
 const counted:RouteCrypto={
  random:n=>{used.push('random');return webRouteCrypto.random(n);},
  sha256:b=>{used.push('sha256');return webRouteCrypto.sha256(b);},
  verify:(k,sig,msg)=>{used.push('verify');return webRouteCrypto.verify(k,sig,msg);},
 };
 await porticoSignIn(hosted,o,fakeServer([]),me.id,{crypto:counted});
 assert.ok(used.includes('random')&&used.includes('verify'),'the nonce and the proof use the crypto given');
 for(const call of [
  ()=>acceptPorticoInvitation(hosted,o,fakeServer([]),me.id,'c'.repeat(43),undefined,counted),
  ()=>transferPorticoOwnership(hosted,o,fakeServer([]),me.id,'acc-heir',4,undefined,counted),
  ()=>acceptPorticoCustody(hosted,o,fakeServer([]),me.id,undefined,counted),
 ]){used.length=0;await call();assert.ok(used.includes('random')&&used.includes('verify'));}
});

test('a server that cannot prove the chosen identity gets nothing from Hosted',async()=>{
 const o=origin();let asked=0;
 const hosted={request:async<T,>()=>{asked++;return assertionBody as T;}};
 // Another server's key, answering as itself or claiming the chosen id.
 await assert.rejects(porticoSignIn(hosted,o,fakeServer([],signIn,impostor.prove),me.id),(e:any)=>e.code==='challenge_not_proven'&&/could not prove/.test(e.message));
 await assert.rejects(porticoSignIn(hosted,o,fakeServer([],signIn,(c,n)=>impostor.prove(c,n,me.id)),me.id),/could not prove/);
 // The chosen server's own proof, but for someone else's nonce or challenge (a replay).
 await assert.rejects(porticoSignIn(hosted,o,fakeServer([],signIn,c=>me.prove(c,'n'.repeat(43))),me.id),/could not prove/);
 await assert.rejects(porticoSignIn(hosted,o,fakeServer([],signIn,(_c,n)=>me.prove('x'.repeat(32),n)),me.id),/could not prove/);
 assert.equal(asked,0,'no assertion was requested');
});

test('an answer from another server is refused',async()=>{
 const o=origin(),hosted={request:async<T,>()=>assertionBody as T};
 await assert.rejects(porticoSignIn(hosted,o,fakeServer([],{...signIn,serverId:impostor.id,session:{...signIn.session,viewer:{...signIn.session.viewer,serverId:impostor.id}}}),me.id),(e:any)=>e.code==='server_mismatch'&&/different server/.test(e.message));
});

test('the snapshot says when the owner still has to accept custody',()=>{
 const owner={...signIn,account:{...signIn.account,role:'owner'}};
 assert.equal(parseDirectSnapshot({...owner,custodyPending:true}).custodyPending,true);
 assert.equal(parseDirectSnapshot(owner).custodyPending,undefined);
 assert.throws(()=>parseDirectSnapshot({...owner,custodyPending:'yes'}));
});

test('a refused member and a migrated session are told apart',()=>{
 assert.ok(isAccessRefused(new ApiError(403,'access_refused','no',false)));
 assert.ok(!isAccessRefused(new ApiError(401,'unauthorized','no',false)));
 assert.ok(isSessionMigrated(new ApiError(401,'session_migrated','moved',false)));
 assert.ok(!isSessionMigrated(new ApiError(401,'unauthorized','no',false)));
 assert.ok(isClockSkew(new ApiError(401,'clock_skew','clock',false)));
});

test('a silent re-admission waits for a recovering Hosted; a person does not',async()=>{
 const o=origin();hostedGate(o).failed(600);let asked=0;
 const hosted={request:async<T,>()=>{asked++;return assertionBody as T;}};
 const server=fakeServer([]);
 await assert.rejects(porticoSignIn(hosted,o,server,me.id,{kind:'automatic'}),(e:unknown)=>e instanceof ApiError&&e.code==='account_service_recovering');
 assert.equal(asked,0);
 assert.equal((await porticoSignIn(hosted,o,server,me.id)).kind,'signed-in');
});

test('a refresh refused as session_migrated is recognised for silent re-admission',async()=>{
 const {SessionRefreshError}=await import('../src/server-connections.ts');
 const {isSessionMigrated,isAccessRefused}=await import('../src/portico-servers.ts');
 assert.equal(isSessionMigrated(new SessionRefreshError('session_migrated','moved',true)),true);
 assert.equal(isSessionMigrated(new SessionRefreshError('refresh_refused','ended',true)),false);
 assert.equal(isAccessRefused(new SessionRefreshError('session_migrated','moved',true)),false);
});
