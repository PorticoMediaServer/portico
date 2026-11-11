import test from 'node:test';
import assert from 'node:assert/strict';
import {prepareSameTabReturn,consumeSameTabReturn,validateRegisteredReturnLocation,type ReturnPending,type ReturnPendingStore} from '../src/same-tab-return.ts';
import {routeBase64} from '../src/route-identity.ts';
import {ACCOUNT_STEP_WINDOW_MS} from '../../contracts/src/account-step.ts';
const pending:ReturnPending={serverId:'server',purpose:'setup',claimOperationId:'op',registrationId:'ret_x',state:'s'.repeat(43),verifier:'v'.repeat(43),serverChallenge:'c'.repeat(32),installationId:'i'.repeat(43),createdAt:Date.now()};
for(const kind of ['network','late','bad-state','duplicate','expired','wrong-server','bad-path','missing'] as const)test(`return fragment is scrubbed before ${kind} failure`,async()=>{
 let saved:ReturnPending|undefined=kind==='missing'?undefined:{...pending,...(kind==='expired'?{createdAt:Date.now()-ACCOUNT_STEP_WINDOW_MS-1_000}:kind==='late'?{createdAt:Date.now()-20*60_000}:{})},scrubbed=false,calls=0;
 const store:ReturnPendingStore={read:async()=>{assert.ok(scrubbed);return saved},change:async f=>{saved=f(saved)}};
 const api={getRouteConnection:()=>({pin:{serverId:kind==='wrong-server'?'wrong':'server'}}),request:async()=>{assert.ok(scrubbed);assert.equal(saved,undefined);calls++;throw new Error('offline')}};
 const fragment=`code=src_${'a'.repeat(43)}&state=${kind==='bad-state'?'wrong':pending.state}&transaction=srt_${'b'.repeat(43)}${kind==='duplicate'?'&code=extra':''}`;
 await assert.rejects(consumeSameTabReturn(api as never,store,{href:`https://server.example/${kind==='bad-path'?'other':'auth/portico-return'}#${fragment}`},{replaceState:(_a,_b,url)=>{assert.equal(url,kind==='bad-path'?'/other':'/auth/portico-return');scrubbed=true;}}));assert.equal(saved,undefined);assert.equal(calls,kind==='network'||kind==='late'?1:0);
});

test('registered return consumer refuses arbitrary URLs and altered immutable bindings',()=>{
 const origin='https://server.example:32500',fragment=`code=src_${'a'.repeat(43)}&state=saved&transaction=srt_fixture`,valid=origin+'/auth/portico-return#'+fragment;
 assert.equal(validateRegisteredReturnLocation(valid,{id:'ret_fixture',origin},'saved','srt_fixture'),valid);
 for(const bad of [valid.replace('server.example:32500','attacker.example'),valid.replace(':32500',':443'),valid.replace('server.example','server.examp1e'),valid.replace('server.example','user@server.example'),valid.replace('https:','http:'),'javascript:alert(1)',valid.replace('/auth/portico-return','/other'),valid.replace('#','?x=1#'),valid+'&state=extra',valid.replace('state=saved','state=wrong'),valid.replace('transaction=srt_fixture','transaction=srt_other')])assert.throws(()=>validateRegisteredReturnLocation(bad,{id:'ret_fixture',origin},'saved','srt_fixture'));
});
test('a member return prepares genuine key proof/PKCE without sending the server move secret to Hosted',async()=>{
 const pair=await crypto.subtle.generateKey('Ed25519',true,['sign','verify']) as CryptoKeyPair;
 const rawKey=new Uint8Array(await crypto.subtle.exportKey('raw',pair.publicKey)),key=routeBase64(rawKey),fingerprint=routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256',rawKey)));
 const prefix=new TextEncoder().encode('portico.server.identity.v1\0'),identity=new Uint8Array(prefix.length+rawKey.length);identity.set(prefix);identity.set(rawKey,prefix.length);
 const serverId='srv_'+routeBase64(new Uint8Array(await crypto.subtle.digest('SHA-256',identity)));
 let saved:ReturnPending|undefined,calls=0;const secret='M'.repeat(43);
 const store:ReturnPendingStore={read:async()=>saved,change:async fn=>{saved=fn(saved);}};
 const api={getRouteConnection:()=>({pin:{serverId}}),request:async(_path:string,_method:string,body:{nonce:string})=>{
  calls++;const payload=new TextEncoder().encode(JSON.stringify({kind:'portico.challenge-proof',version:'1',serverId,publicKey:key,fingerprint,challenge:'challenge_A',nonce:body.nonce,installationId:'i'.repeat(43)}));
  return {challenge:'challenge_A',proof:{payload:routeBase64(payload),signature:routeBase64(new Uint8Array(await crypto.subtle.sign('Ed25519',pair.privateKey,payload)))}};
 }};
 // The installed operation identifies the route tuple; it grants no owner outcome.
 const input={purpose:'sign-in' as const,claimOperationId:'installed_operation',registrationId:'ret_'+'r'.repeat(43),installationId:'i'.repeat(43),moveTicket:secret};
 const start=await prepareSameTabReturn(api as never,store,input);
 assert.equal(calls,1);assert.equal(start.purpose,'sign-in');assert.equal(start.claimOperationId,'installed_operation');assert.equal(saved?.moveTicket,secret);
 assert.equal(JSON.stringify(start).includes(secret),false);assert.equal('verifier' in start,false);assert.equal('moveTicket' in start,false);assert.equal(start.codeChallenge.length,43);
 for(const patch of [{claimOperationId:'invalid operation!'},{purpose:'switch' as const},{moveCode:secret}])await assert.rejects(prepareSameTabReturn(api as never,store,{...input,...patch}));
 assert.equal(calls,1);
});
