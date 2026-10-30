import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {routeOrigin} from '../src/route-identity.ts';

const keys=generateKeyPairSync('ed25519');
const rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const publicKey=rawKey.toString('base64url');
const pin={publicKey,serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url'),fingerprint:createHash('sha256').update(rawKey).digest('base64url')};
const key={keyId:'routes-key',publicKey};
const expected={accountId:'account',serverId:pin.serverId};
function proofServer(serverNow:()=>number,signer=keys.privateKey){
 return (async(_url:RequestInfo|URL,init?:RequestInit)=>{
  const asked=JSON.parse(String(init?.body));const at=serverNow();
  const payload=Buffer.from(JSON.stringify({kind:'portico.route-proof',version:'1',...pin,baseUrl:asked.baseUrl,nonce:asked.nonce,issuedAt:new Date(at).toISOString(),expiresAt:new Date(at+30000).toISOString()}));
  return new Response(JSON.stringify({payload:payload.toString('base64url'),signature:sign(null,payload,signer).toString('base64url')}),{status:200,headers:{'Content-Type':'application/json'}});
 }) as typeof fetch;
}
test('a device clock up to two minutes off in either direction still accepts a fresh proof',async()=>{
 const {probeRoute}=await import('../src/route-identity.ts');
 const server=Date.parse('2026-10-13T12:00:00.000Z');
 for(const offset of [0,45_000,110_000,-110_000]){
  assert.deepEqual(await probeRoute('http://192.168.1.20:32500',undefined,{fetcher:proofServer(()=>server),now:()=>server+offset}),pin,String(offset));
 }
});
test('clocks further apart are named as a clock problem, not as a wrong server',async()=>{
 const {probeRoute}=await import('../src/route-identity.ts');
 const server=Date.parse('2026-10-13T12:00:00.000Z');
 for(const offset of [200_000,-200_000]){
  await assert.rejects(probeRoute('http://192.168.1.20:32500',undefined,{fetcher:proofServer(()=>server),now:()=>server+offset}),(e:{code?:string})=>e.code==='clocks_disagree',String(offset));
 }
});
test('a proof signed by another key is still a wrong server, whatever the clocks say',async()=>{
 const {probeRoute}=await import('../src/route-identity.ts');
 const other=generateKeyPairSync('ed25519').privateKey;
 const server=Date.parse('2026-10-13T12:00:00.000Z');
 await assert.rejects(probeRoute('http://192.168.1.20:32500',undefined,{fetcher:proofServer(()=>server,other),now:()=>server+200_000}),(e:{code?:string})=>e.code==='identity_mismatch');
});
test('explicit HTTP origins work on public, overlay and IPv6 addresses',()=>{
 for(const origin of ['http://media.example:32500','http://100.64.1.2:32500','http://203.0.113.9:32500','http://[2001:db8::9]:32500']) assert.equal(routeOrigin(origin),origin);
 for(const origin of ['http://user:secret@media.example','http://media.example/path','http://media.example?secret=x']) assert.throws(()=>routeOrigin(origin));
});
