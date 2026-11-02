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
function proofServer(_serverNow:()=>number,signer=keys.privateKey,extra:Record<string,unknown>={},nonce?:(asked:string)=>string){
 return (async(_url:RequestInfo|URL,init?:RequestInit)=>{
  const asked=JSON.parse(String(init?.body));
  const payload=Buffer.from(JSON.stringify({kind:'portico.route-proof',version:'1',...pin,baseUrl:asked.baseUrl,nonce:nonce?nonce(asked.nonce):asked.nonce,...extra}));
  return new Response(JSON.stringify({payload:payload.toString('base64url'),signature:sign(null,payload,signer).toString('base64url')}),{status:200,headers:{'Content-Type':'application/json'}});
 }) as typeof fetch;
}
const YEAR=365*86400000;
test('No client-clock requirements: a device a year ahead or behind still accepts a fresh proof',async()=>{
 const {probeRoute}=await import('../src/route-identity.ts');
 const server=Date.parse('2026-10-13T12:00:00.000Z');
 for(const offset of [0,YEAR,-YEAR,10*YEAR]){
  assert.deepEqual(await probeRoute('http://192.168.1.20:32500',undefined,{fetcher:proofServer(()=>server),now:()=>server+offset}),pin,String(offset));
 }
});
test('the nonce, not a clock, keeps a proof fresh: an answer over another nonce is refused',async()=>{
 const {probeRoute}=await import('../src/route-identity.ts');
 await assert.rejects(probeRoute('http://192.168.1.20:32500',undefined,{fetcher:proofServer(()=>0,keys.privateKey,{},()=>Buffer.alloc(32,7).toString('base64url'))}),(e:{code?:string})=>e.code==='identity_mismatch');
});
test('a proof carrying times is not the current contract and is refused',async()=>{
 const {probeRoute}=await import('../src/route-identity.ts');
 await assert.rejects(probeRoute('http://192.168.1.20:32500',undefined,{fetcher:proofServer(()=>0,keys.privateKey,{issuedAt:'2026-10-13T12:00:00Z',expiresAt:'2026-10-13T12:00:30Z'})}),(e:{code?:string})=>e.code==='identity_mismatch');
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
