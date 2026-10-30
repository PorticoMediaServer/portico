import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {prepareRegisteredSignIn,type ReturnPending,type ReturnPendingStore} from '../src/same-tab-return.ts';
const keys=generateKeyPairSync('ed25519'),rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const pin={serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url'),publicKey:rawKey.toString('base64url'),fingerprint:createHash('sha256').update(rawKey).digest('base64url')};
const input={origin:'https://server.example:32500',hostedOrigin:'https://account.example',installationId:Buffer.alloc(32,1).toString('base64url'),moveTicket:Buffer.alloc(32,2).toString('base64url')};
const registrationId='ret_'+Buffer.alloc(32,5).toString('base64url');
const nano=(date:Date)=>date.toISOString().replace(/(\.\d*?)0+Z$/,'$1Z').replace('.Z','Z');
function fixture(patch:(out:any,proof:any)=>void=()=>{}){
 let pending:ReturnPending|undefined,calls=0;
 const store:ReturnPendingStore={read:async()=>pending,change:async f=>{pending=f(pending);}};
 const api={getRouteConnection:()=>({pin}),request:async(path:string,method:string,body:any)=>{
  calls++;assert.equal(path,'/v1/auth/portico-return/prepare');assert.equal(method,'POST');assert.equal(body.origin,input.origin);assert.deepEqual(Object.keys(body).sort(),['nonce','origin']);assert.equal(Buffer.from(body.nonce,'base64url').length,32);
  const issuedAt=nano(new Date()),expires=new Date(Date.now()+299000);
  const proof={kind:'portico.registered-sign-in-prepare',version:'1',...pin,serverName:'Actual <server> & TV\u2028Room',registrationId,registrationOrigin:input.origin,hostedOrigin:input.hostedOrigin,claimOperationId:'installed_operation',challenge:'C'.repeat(32),nonce:body.nonce,installationId:input.installationId,issuedAt,expiresAt:nano(expires)};
  const out={serverId:pin.serverId,hostedOrigin:input.hostedOrigin,claimOperationId:'installed_operation',registration:{id:registrationId,origin:input.origin},challenge:proof.challenge,expiresAt:nano(expires),proof:{} as unknown,context:undefined as unknown};
  patch(out,proof);const payload=Buffer.from(JSON.stringify(proof).replace(/[<>&\u2028\u2029]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0')));out.context={payload:payload.toString('base64url'),signature:sign(null,payload,keys.privateKey).toString('base64url')};return out;
 }};
 return {store,api,pending:()=>pending,calls:()=>calls};
}
test('checked member prepare retains the installed operation, actual registration and same signed challenge',async()=>{
 const f=fixture(),result=await prepareRegisteredSignIn(f.api as never,f.store,input);
 assert.equal(f.calls(),1);assert.equal(result.start.claimOperationId,'installed_operation');assert.equal(result.start.purpose,'sign-in');assert.equal(result.start.serverChallenge,'C'.repeat(32));assert.deepEqual(result.registration,{id:registrationId,origin:input.origin});
 assert.equal(JSON.parse(Buffer.from(result.context.payload,'base64url').toString()).serverName,'Actual <server> & TV\u2028Room');
 assert.equal(f.pending()?.moveTicket,input.moveTicket);assert.equal(f.pending()?.registrationOrigin,input.origin);assert.equal(result.start.codeChallenge,createHash('sha256').update(f.pending()!.verifier).digest('base64url'));
 for(const secret of ['verifier','moveTicket','moveCode','registrationOrigin'])assert.equal(Object.hasOwn(result.start,secret),false);
});
test('ordinary preparation binds the existing UUID installation rather than creating or decoding another identity',async()=>{
 const installationId='06be25db-f89c-497a-a817-d160d9e50761';
 const f=fixture((_out,proof)=>{proof.installationId=installationId;});
 const result=await prepareRegisteredSignIn(f.api as never,f.store,{...input,installationId});
 assert.equal(result.start.installationId,installationId);assert.equal(f.pending()?.installationId,installationId);assert.equal(f.calls(),1);
 const g=fixture();await assert.rejects(prepareRegisteredSignIn(g.api as never,g.store,{...input,installationId:'invalid/installation'}));assert.equal(g.calls(),0);assert.equal(g.pending(),undefined);
 const h=fixture();await assert.rejects(prepareRegisteredSignIn(h.api as never,h.store,{...input,installationId}));assert.equal(h.pending(),undefined,'a different signed installation never binds the UUID');
});
for(const [name,patch] of Object.entries({
 registration:(out:any)=>{out.registration.id='provisional';},
 origin:(out:any)=>{out.registration.origin='https://other.example';},
 service:(out:any)=>{out.hostedOrigin='https://other.example';},
 operation:(out:any)=>{out.claimOperationId='';},
 signedOperation:(_out:any,proof:any)=>{proof.claimOperationId='different_actual_operation';},
 signedOrigin:(_out:any,proof:any)=>{proof.registrationOrigin='https://other.example';},
 signedHosted:(_out:any,proof:any)=>{proof.hostedOrigin='https://other.example';},
 invalidName:(_out:any,proof:any)=>{proof.serverName='';},
 controlName:(_out:any,proof:any)=>{proof.serverName='forged\nname';},
 nonce:(_out:any,proof:any)=>{proof.nonce='wrong';},
 installation:(_out:any,proof:any)=>{proof.installationId=Buffer.alloc(32,3).toString('base64url');},
 challenge:(out:any)=>{out.challenge='D'.repeat(32);},
 expiry:(out:any)=>{out.expiresAt='2099-01-01T00:00:00Z';},
 expired:(out:any,proof:any)=>{proof.issuedAt='2020-01-01T00:00:00Z';proof.expiresAt='2020-01-01T00:05:00Z';out.expiresAt=proof.expiresAt;},
 longLifetime:(out:any,proof:any)=>{proof.expiresAt=new Date(Date.now()+600000).toISOString();out.expiresAt=proof.expiresAt.replace(/\.\d{3}Z$/,'Z');},
}))test(`member prepare refuses ${name} without saving pending authority`,async()=>{
 const f=fixture(patch);await assert.rejects(prepareRegisteredSignIn(f.api as never,f.store,input));assert.equal(f.pending(),undefined);
});
test('no Local/HTTP origin or cancelled preparation can save pending authority',async()=>{
 const f=fixture();await assert.rejects(prepareRegisteredSignIn(f.api as never,f.store,{...input,origin:'http://server.example'}));assert.equal(f.calls(),0);
 const g=fixture(),controller=new AbortController();controller.abort();await assert.rejects(prepareRegisteredSignIn(g.api as never,g.store,input,{signal:controller.signal}));assert.equal(g.calls(),0);assert.equal(g.pending(),undefined);
 let current=true;const h=fixture(()=>{current=false;});await assert.rejects(prepareRegisteredSignIn(h.api as never,h.store,input,{isCurrent:()=>current}));assert.equal(h.pending(),undefined);
});
