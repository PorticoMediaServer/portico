import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {ApiError,HttpLocalApi} from '../src/index.ts';
import {routeOrigin,probeRoute,serverPin,verifyRoutes,type ServerPin,type VerifiedRoutes} from '../src/route-identity.ts';
import {RouteConnection,discoveredRoutes} from '../src/route-connection.ts';
import {SetupCodeService,normalizeSetupCode,type SetupCodeJournal} from '../src/setup-code.ts';
import {initializeServer,type SetupDraft} from '../src/setup-onboarding.ts';
import {setupQR} from '../src/setup-qr.ts';
import {addressClass,formatIP,parseDirectName} from '../src/network-locality.ts';
const delay=(ms=10)=>new Promise<void>(r=>setTimeout(r,ms));
const keys=generateKeyPairSync('ed25519');
const rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const publicKey=rawKey.toString('base64url');
const pin:ServerPin={publicKey,serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url'),fingerprint:createHash('sha256').update(rawKey).digest('base64url')};
const key={keyId:'routes-key',publicKey};
const signed=(value:unknown)=>{const raw=Buffer.from(JSON.stringify(value));return {payload:raw.toString('base64url'),signature:sign(null,raw,keys.privateKey).toString('base64url')};};
// The signed LAN route for 192.168.1.8 (P2-67 Gate 2: an HTTPS IP name with its literal and port).
const lan='https://192-168-1-8.ptc-aaaaaaaaaaaaaaaaaaaa.portico.direct:32500';
/** A Gate 2 candidate: IP names are automatic routes; any other address is the owner's own (manual). */
function candidate(baseUrl:string,generation:string){
 const name=parseDirectName(new URL(baseUrl).hostname);
 if(name){const address=formatIP(name.address),cls=addressClass(address)==='public'?'public':'lan';return {baseUrl,generation,class:cls,insecure:false,address,port:Number(new URL(baseUrl).port||443),quality:cls==='lan'?'probe_required':'reachable'};}
 return {baseUrl,generation,class:'manual',insecure:baseUrl.startsWith('http:'),quality:'reachable'};
}
function document(urls=[lan],generation='1',issued=Date.now()){
 return {kind:'portico.routes',version:'1',algorithm:'Ed25519',keyId:key.keyId,audience:'account',...pin,name:'Media server',policyRevision:1,claimGeneration:'1',credentialGeneration:'1',generation,issuedAt:new Date(issued).toISOString(),expiresAt:new Date(issued+45*60000).toISOString(),candidates:urls.map(baseUrl=>candidate(baseUrl,generation))};
}
async function routes(urls?:string[],generation?:string){return verifyRoutes({...signed(document(urls,generation)),keyId:key.keyId},key,{accountId:'account',serverId:pin.serverId});}
test('route origins accept plaintext at any address and require canonical IP syntax',()=>{
 assert.equal(routeOrigin('https://media.example:443/'),'https://media.example');
 assert.equal(routeOrigin('http://192.168.1.8:80'),'http://192.168.1.8');
 assert.equal(routeOrigin('http://public.example'),'http://public.example');
 assert.equal(routeOrigin('http://100.64.2.1'),'http://100.64.2.1');
 for(const value of ['http://0x7f000001','http://2130706433','https://x.example/a','https://x.example?token=secret','https://a:b@x.example'])assert.throws(()=>routeOrigin(value),value);
});
test('actual Hosted signed-policy wrapper keyId is accepted; authoritative pin is derived',async()=>{
 assert.deepEqual(await serverPin(pin.serverId,publicKey,pin.fingerprint),pin);
 const value=await routes();assert.equal(value.document.serverId,pin.serverId);assert.equal(value.document.candidates.length,1);
 await assert.rejects(verifyRoutes({...signed(document()),keyId:'wrong'},key,{accountId:'account',serverId:pin.serverId}));
});
test('route evidence rejects wrong audience, signature, identity and expiry',async()=>{
 const expected={accountId:'account',serverId:pin.serverId};
 await assert.rejects(verifyRoutes(signed({...document(),audience:'other'}),key,expected));
 await assert.rejects(verifyRoutes(signed({...document(),fingerprint:'A'.repeat(43)}),key,expected));
 const tampered=signed(document());tampered.signature='A'.repeat(86);await assert.rejects(verifyRoutes(tampered,key,expected));
 const expired=signed(document(undefined,'1',Date.now()-46*60000));await assert.rejects(verifyRoutes(expired,key,expected));
 assert.equal((await verifyRoutes(expired,key,{...expected,allowExpiredHint:true})).document.serverId,pin.serverId);
});
test('fresh nonce proof uses no credentials and binds the actual requested origin',async()=>{
 const origin='http://192.168.1.8:32500';let sent=0;
 const fetcher=(async(url,init)=>{sent++;assert.equal(url,origin+'/v1/networking/identity-proof');assert.equal(init?.credentials,'omit');assert.equal(init?.redirect,'error');assert.equal(new Headers(init?.headers).get('Authorization'),null);const q=JSON.parse(String(init?.body));assert.equal(Buffer.from(q.nonce,'base64url').length,32);return new Response(JSON.stringify(signed({kind:'portico.route-proof',version:'1',...pin,...q,issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()})),{headers:{'Content-Type':'application/json'}});}) as typeof fetch;
 assert.deepEqual(await probeRoute(origin,pin,{fetcher}),pin);assert.equal(sent,1);
 const wrong=(async()=>new Response(JSON.stringify(signed({kind:'portico.route-proof',version:'1',...pin,baseUrl:origin,nonce:'replayed',issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()})),{headers:{'Content-Type':'application/json'}})) as typeof fetch;
 await assert.rejects(probeRoute(origin,pin,{fetcher:wrong}));
});
test('healthy cached LAN wins without a Hosted request',async()=>{
 let fresh=0;const memory={current:await routes(),paired:[]};const connection=new RouteConnection({pin,logicalOrigin:'https://old.example',memory,probe:async()=>pin,fresh:async()=>{fresh++;return memory.current;}});
 try{assert.equal(await connection.resolve(),lan);assert.equal(fresh,0);assert.equal(connection.logicalOrigin,'https://old.example');}finally{connection.dispose();}
});
test('Hosted discovery is hedged behind a hung cached route, not serial, and waits its full delay',async()=>{
 const old=await routes(),next=await routes(['https://new.example'],'2');let freshAt=0;const start=Date.now();
 const connection=new RouteConnection({pin,logicalOrigin:'https://old.example',memory:{current:old,paired:[]},freshAfterMs:500,probe:async origin=>origin==='https://new.example'?pin:new Promise(()=>{}),fresh:async()=>{freshAt=Date.now();return next;}});
 try{assert.equal(await connection.resolve(),'https://new.example');assert.ok(freshAt-start>=450);assert.ok(freshAt-start<2000);}finally{connection.dispose();}
});
test('a remembered route that answers within the delay never asks the account service',async()=>{
 const old=await routes(['https://old.example']);let asked=0;
 const connection=new RouteConnection({pin,logicalOrigin:'https://old.example',memory:{current:old,paired:[]},fresh:async()=>{asked++;return old;},probe:async()=>{await delay(900);return pin;}});
 try{assert.equal(await connection.resolve(),'https://old.example');await delay(50);assert.equal(asked,0);}finally{connection.dispose();}
});
test('late proof from a route omitted by newer Hosted truth cannot win',async()=>{
 const old=await routes(['https://old.example']),next=await routes(['https://new.example'],'2');
 const connection=new RouteConnection({pin,logicalOrigin:'https://old.example',memory:{current:old,paired:[]},freshAfterMs:500,fresh:async()=>next,probe:async origin=>{await delay(origin==='https://old.example'?620:250);return pin;}});
 try{assert.equal(await connection.resolve(),'https://new.example');assert.equal(connection.getMemory().current?.document.generation,'2');}finally{connection.dispose();}
});
test('policy rollback is rejected even with a newer topology generation',async()=>{
 const current=await routes(),connection=new RouteConnection({pin,logicalOrigin:'https://old.example',memory:{current,paired:[]}});
 const rollback=await verifyRoutes(signed({...document(undefined,'2'),policyRevision:0}),key,{accountId:'account',serverId:pin.serverId});
 try{assert.throws(()=>connection.install(rollback));}finally{connection.dispose();}
});
test('Bonjour hints cannot introduce public, expired or conflicting identities',()=>{
 const good={baseUrl:'http://192.168.1.8:32500',...pin,port:32500,path:'/',expiresAt:Date.now()+5000};
 assert.equal(discoveredRoutes([good],pin).length,1);
 for(const bad of [{...good,expiresAt:Date.now()-1},{...good,baseUrl:'https://public.example:32500'},{...good,port:443},{...good,path:'/other'}])assert.equal(discoveredRoutes([bad],pin).length,0);
 assert.equal(discoveredRoutes([good,{...good,serverId:'other'}],pin).length,0);
});
test('transport remaps only the verified server and never blind-replays a mutation',async()=>{
 let calls=0;const connection=new RouteConnection({pin,logicalOrigin:'https://logical.example',memory:{paired:['http://192.168.1.8:32500']},probe:async()=>pin,fetcher:async input=>{if(!String(input).endsWith('/v1/networking/connection'))calls++;throw new Error('lost response');}});
 try{assert.equal(connection.mediaURL('/media'),'');await connection.resolve();assert.equal(connection.mediaURL('/media?ticket=opaque'),'http://192.168.1.8:32500/media?ticket=opaque');await assert.rejects(connection.fetch('https://logical.example/v1/action',{method:'POST'}));assert.equal(calls,1);await assert.rejects(connection.fetch('https://other.example/v1/action',{method:'POST'}));assert.equal(calls,1);}finally{connection.dispose();}
});
function journalStore<T>(initial?:SetupCodeJournal<T>){let value=initial;return {read:()=>value,change:async(update:(v:SetupCodeJournal<T>|undefined)=>SetupCodeJournal<T>)=>{value=update(value);return value;}};}
const authorization=(code='ABCD-EFGH',expires=Date.now()+600000)=>({deviceCode:'s'.repeat(43),userCode:code,verificationUri:'https://web.getportico.tv/device',verificationUriComplete:'https://web.getportico.tv/device#code='+code,expiresAt:new Date(expires).toISOString(),expiresIn:600,interval:5});
const requestId='11111111-1111-4111-8111-111111111111';
test('human setup code normalization has no ambiguous characters',()=>{
 assert.equal(normalizeSetupCode(' abcd efgh '),'ABCD-EFGH');assert.equal(normalizeSetupCode('aBcd-efgh'),'ABCD-EFGH');for(const s of ['ABCI-EFGH','ABCL-EFGH','ABCO-EFGH','ABCD-0FGH'])assert.throws(()=>normalizeSetupCode(s));
});
test('activation create intent is persisted before mutation; pause does not cancel',async()=>{
 const store=journalStore<string>();let creates=0,cancels=0;
 const service=new SetupCodeService('local:one',{create:async id=>{creates++;assert.equal(store.read()?.creating?.requestId,id);return authorization();},poll:async()=>{throw new ApiError(400,'authorization_pending','pending');},cancel:async()=>{cancels++;}},store,async()=>requestId,{requestTimeoutMs:500,tickMs:5,replaceBeforeMs:60000});
 await service.start();await delay(25);service.pause();assert.equal(creates,1);assert.equal(cancels,0);assert.equal(service.getSnapshot().userCode,'ABCD-EFGH');assert.ok(!JSON.stringify(service.getSnapshot()).includes('s'.repeat(43)));
 await service.start();await delay(10);service.pause();assert.equal(creates,1);
});
test('proactive replacement failure preserves the current code and QR as one snapshot',async()=>{
 const store=journalStore<string>({version:1,authority:'local:one',revision:1,current:{requestId,authorization:authorization('ABCD-EFGH',Date.now()+30000),nextPollAt:Date.now()+5000},retired:[]});
 const service=new SetupCodeService('local:one',{create:async()=>{throw new Error('offline');},poll:async()=>'',cancel:async()=>{}},store,async()=>requestId,{requestTimeoutMs:500,tickMs:5,replaceBeforeMs:60000});
 await service.start();await delay(25);service.pause();const state=service.getSnapshot();assert.equal(state.phase,'waiting');assert.equal(state.userCode,'ABCD-EFGH');assert.equal(state.verificationUriComplete,authorization().verificationUriComplete);assert.equal(state.message,undefined);assert.equal(store.read()?.creating?.requestId,requestId);
});
test('a committed private grant resumes after restart and is acknowledged only after adoption',async()=>{
 const grant={family:'exact-one'},store=journalStore<typeof grant>({version:1,authority:'local:one',revision:1,current:{requestId,authorization:authorization(),nextPollAt:0},retired:[]});let polls=0,forgotten=0;
 const api={create:async()=>authorization(),poll:async()=>{polls++;return grant;},cancel:async()=>{},forgotten:()=>{forgotten++;}};
 const first=new SetupCodeService('local:one',api,store);await first.start();await delay(25);first.pause();assert.equal(first.getSnapshot().phase,'approved');assert.equal(store.read()?.current?.grant,grant);
 const second=new SetupCodeService('local:one',api,store);await second.start();assert.equal(second.getSnapshot().grant,grant);assert.equal(polls,1);assert.equal(forgotten,0);assert.equal(await second.complete(grant),true);assert.equal(store.read()?.current,undefined);assert.equal(forgotten,1);second.pause();
});
test('failed intent persistence performs no create call',async()=>{
 let creates=0;const service=new SetupCodeService('local:one',{create:async()=>{creates++;return authorization();},poll:async()=>'',cancel:async()=>{}},{change:async()=>{throw new Error('storage unavailable');}});
 await service.start();assert.equal(service.getSnapshot().phase,'error');assert.equal(creates,0);service.pause();
});
test('initialization always reconciles the saved operation before creating; password is never drafted',async()=>{
 let draft:SetupDraft|undefined;const paths:string[]=[];let committed=false;
 const session={accessToken:'private',expiresAt:new Date(Date.now()+60000).toISOString(),authorizationHorizon:new Date(Date.now()+60000).toISOString(),sessionFamilyId:'family',tokenGeneration:'1',viewer:{serverId:pin.serverId,accountId:'local-owner',profileId:'profile',role:'owner',authority:'local'}};
 const api={getRouteConnection:()=>({pin}),request:async(path:string,_method:string,body:any)=>{paths.push(path);assert.equal(draft?.requestId,requestId);assert.ok(!Object.hasOwn(draft!,'password'));if(path.endsWith('/resume')){if(!committed)throw new ApiError(404,'not_found','not found');return session;}committed=true;assert.equal(body.requestId,requestId);return session;}} as unknown as HttpLocalApi;
 const store={read:async()=>draft,change:async(update:(v:SetupDraft|undefined)=>SetupDraft|undefined)=>{draft=update(draft);}};
 const input={setupToken:'s'.repeat(43),name:'Media',username:'owner',password:'a strong private password 123!',authMode:'local' as const};
 await initializeServer(api,store,input,new AbortController().signal,async()=>requestId);await initializeServer(api,store,input,new AbortController().signal,async()=>requestId);
 assert.deepEqual(paths,['/v1/setup/resume','/v1/setup','/v1/setup/resume']);
});
test('local QR code matches a fixed independent byte-mode version-5-L vector',()=>{
 const matrix=setupQR('https://web.getportico.tv/device#code=ABCD-EFGH');assert.equal(matrix.length,37);assert.ok(matrix.every(row=>row.length===37));
 const digest=createHash('sha256').update(matrix.map(row=>row.map(b=>b?'1':'0').join('')).join('\n')).digest('hex');
 assert.equal(digest,'d92dac9c374ae275b21b212a1f6c8dadce0fc5fbf47f623f3bd508b58ab0b318');assert.throws(()=>setupQR('a'.repeat(107)));
});
test('signed routes may carry the signing key’s certificate (Hosted 784d3de5); anything else unknown is refused',async()=>{
 const expected={accountId:'account',serverId:pin.serverId};
 const withCertificate=await verifyRoutes({...signed(document()),keyId:key.keyId,certificate:{keyId:key.keyId,publicKey,signature:'x'}},key,expected);
 assert.equal(withCertificate.document.serverId,pin.serverId);
 assert.equal('certificate' in withCertificate.envelope,false,'not kept');
 await assert.rejects(verifyRoutes({...signed(document()),keyId:key.keyId,certificate:'not-an-object'},key,expected));
 await assert.rejects(verifyRoutes({...signed(document()),keyId:key.keyId,extra:true},key,expected));
});
