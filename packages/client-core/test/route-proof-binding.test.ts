import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {probeRoute,RouteError} from '../src/route-identity.ts';

const keys=generateKeyPairSync('ed25519');
const rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const pin={publicKey:rawKey.toString('base64url'),serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url'),fingerprint:createHash('sha256').update(rawKey).digest('base64url')};
const origin='https://media.example:32500';
const cert=createHash('sha256').update('leaf certificate DER').digest('base64url');
function proof(fields:Record<string,unknown>,tamper=false):typeof fetch {
 return async(_input,init)=>{
  assert.equal(new Headers(init?.headers).get('Accept'),'application/json; portico-route-proof=2');
  const asked=JSON.parse(String(init?.body));
  assert.deepEqual(Object.keys(asked).sort(),['baseUrl','nonce']); // old servers reject unknown body fields
  const now=Date.now();
  const raw=Buffer.from(JSON.stringify({kind:'portico.route-proof',version:'2',...pin,baseUrl:asked.baseUrl,nonce:asked.nonce,issuedAt:new Date(now).toISOString(),expiresAt:new Date(now+30000).toISOString(),...fields}));
  const signature=sign(null,raw,keys.privateKey);
  if(tamper)signature[0]=(signature[0]+1)%256;
  return new Response(JSON.stringify({payload:raw.toString('base64url'),signature:signature.toString('base64url')}),{headers:{'Content-Type':'application/json'}});
 };
}
test('v2 exposes only a verified immutable leaf certificate binding',async()=>{
 const other=createHash('sha256').update('rotated DER').digest('base64url');
 const result=await probeRoute(origin,pin,{fetcher:proof({tlsCertificateFingerprints:[cert,other]})});
 assert.deepEqual(result,{...pin,tlsCertificateFingerprints:[cert,other]});
 assert.ok(Object.isFrozen(result));assert.ok(Object.isFrozen(result.tlsCertificateFingerprints));
 await assert.rejects(probeRoute(origin,pin,{fetcher:proof({tlsCertificateFingerprints:[cert]},true)}));
});
test('old v1 and unbound v2 proofs preserve identity without authorizing a TLS certificate',async()=>{
 for(const version of ['1','2'])assert.deepEqual(await probeRoute(origin,pin,{fetcher:proof({version})}),pin);
});
test('proof versions and certificate bindings are strict and bounded',async()=>{
 for(const fields of [
  {version:'3'}, {version:2}, {version:'1',tlsCertificateFingerprints:[cert]},
  {unexpected:true}, {tlsCertificateFingerprints:null}, {tlsCertificateFingerprints:cert},
  {tlsCertificateFingerprints:[]}, {tlsCertificateFingerprints:[cert,cert]},
  {tlsCertificateFingerprints:[1]}, {tlsCertificateFingerprints:['B'.repeat(43)]},
  {tlsCertificateFingerprints:[cert+'=']}, {tlsCertificateFingerprints:['a'.repeat(44)]},
  {tlsCertificateFingerprints:Array.from({length:9},(_,i)=>createHash('sha256').update(String(i)).digest('base64url'))},
 ])await assert.rejects(probeRoute(origin,pin,{fetcher:proof(fields)}),RouteError,JSON.stringify(fields));
});
test('bindings do not weaken nonce, origin, derived identity or expected pin checks',async()=>{
 for(const fields of [{nonce:'A'.repeat(43)},{baseUrl:'https://relay.example'},{serverId:'srv_'+cert},{fingerprint:cert}]){
  await assert.rejects(probeRoute(origin,pin,{fetcher:proof({tlsCertificateFingerprints:[cert],...fields})}),RouteError);
 }
 await assert.rejects(probeRoute(origin,{...pin,serverId:'srv_'+cert},{fetcher:proof({tlsCertificateFingerprints:[cert]})}),RouteError);
});
