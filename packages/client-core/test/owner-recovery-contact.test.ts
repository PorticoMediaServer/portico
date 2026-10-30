import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {contactKnownServerForOwnerRecovery} from '../src/owner-recovery-contact.ts';
import {routePath} from '../../contracts/src/routes.ts';

const keys=generateKeyPairSync('ed25519');
const rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const captured={baseUrl:'https://server.example:32500',publicKey:rawKey.toString('base64url'),serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url')};
const system={id:captured.serverId,name:'Server',version:'1',signIn:'portico',move:null,setupRequired:false,hostedConfigured:true,hostedAttached:true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []};
const json=(value:unknown)=>new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}});
function transport(overrides:{proof?:Record<string,unknown>;system?:Record<string,unknown>;after?:(path:string)=>void;redirect?:boolean}={}) {
 const calls:string[]=[];
 const fetcher:typeof fetch=async(input,init)=>{
  const url=String(input);calls.push(url);
  assert.equal(init?.credentials,'omit');assert.equal(init?.redirect,'error');assert.equal(init?.referrerPolicy,'no-referrer');
  assert.equal(init?.cache,'no-store');
  assert.equal(new Headers(init?.headers).has('Authorization'),false);assert.equal(new Headers(init?.headers).has('Cookie'),false);
  assert.equal(new URL(url).origin,captured.baseUrl);
  let response:Response;
  if(url.endsWith(routePath('prove_network_identity'))){
   const asked=JSON.parse(String(init?.body)),now=Date.now();
   const payload=Buffer.from(JSON.stringify({kind:'portico.route-proof',version:'1',...captured,baseUrl:asked.baseUrl,nonce:asked.nonce,fingerprint:createHash('sha256').update(rawKey).digest('base64url'),issuedAt:new Date(now).toISOString(),expiresAt:new Date(now+30000).toISOString(),...overrides.proof}));
   response=json({payload:payload.toString('base64url'),signature:sign(null,payload,keys.privateKey).toString('base64url')});
  }else {assert.equal(url,captured.baseUrl+routePath('get_system'));response=json({...system,...overrides.system});}
  if(overrides.redirect)Object.defineProperty(response,'redirected',{value:true});
  overrides.after?.(url);return response;
 };
 return {fetcher,calls};
}
const lifetime=()=>({signal:new AbortController().signal,isCurrent:()=>true});
test('emergency contact proves only the captured origin and returns purpose-only same-origin navigation',async()=>{
 const net=transport();
 assert.deepEqual(await contactKnownServerForOwnerRecovery(captured,{...net,...lifetime()}),{purpose:'owner-recovery',origin:captured.baseUrl,serverId:captured.serverId,signInURL:captured.baseUrl+'/sign-in'});
 assert.deepEqual(net.calls,[captured.baseUrl+routePath('prove_network_identity'),captured.baseUrl+routePath('get_system')]);
});
test('pin, nonce, system identity and redirect failures never yield a recovery handoff',async()=>{
 for(const overrides of [{proof:{nonce:'wrong'}},{proof:{publicKey:'A'.repeat(43)}},{system:{id:'different'}},{redirect:true}]){
  const net=transport(overrides);
  await assert.rejects(contactKnownServerForOwnerRecovery(captured,{...net,...lifetime()}));
 }
});
test('retired or pre-aborted selection performs no contact, and retirement after proof cannot fetch system',async()=>{
 const net=transport(),controller=new AbortController();controller.abort();
 await assert.rejects(contactKnownServerForOwnerRecovery(captured,{...net,signal:controller.signal,isCurrent:()=>true}));assert.equal(net.calls.length,0);
 await assert.rejects(contactKnownServerForOwnerRecovery(captured,{...net,...lifetime(),isCurrent:()=>false}));assert.equal(net.calls.length,0);
 let current=true;
 const retiring=transport({after:()=>{current=false;}});
 await assert.rejects(contactKnownServerForOwnerRecovery(captured,{...retiring,...lifetime(),isCurrent:()=>current}));assert.equal(retiring.calls.length,1);
});
