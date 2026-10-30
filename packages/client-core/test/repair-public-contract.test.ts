import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn,execFileSync} from 'node:child_process';
import {mkdtempSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createInterface} from 'node:readline';
import {fileURLToPath} from 'node:url';
import {HttpLocalApi} from '../src/index.ts';
import {routeBody} from '../../contracts/src/routes.ts';
import {decode} from '../../contracts/src/runtime.ts';

async function bridge(){
 const folder=mkdtempSync(join(tmpdir(),'portico-public-bridge-'));const binary=join(folder,'http-producer.test');
 execFileSync('go',['test','-c','-o',binary,'./internal/httpapi'],{cwd:fileURLToPath(new URL('../../../server/',import.meta.url)),stdio:'pipe',timeout:120_000});
 const proc=spawn(binary,['-test.run=^TestRepairPublicHTTPBridge$','-test.v','-test.timeout=90s'],{cwd:fileURLToPath(new URL('../../../server/',import.meta.url)),env:{...process.env,PORTICO_REPAIR_PUBLIC_BRIDGE:'1'},stdio:['pipe','pipe','pipe']});
 const pending:((v:any)=>void)[]=[];const ready:any[]=[];let diagnostics='';
 proc.stderr.on('data',v=>{diagnostics+=v.toString()});
 createInterface({input:proc.stdout}).on('line',line=>{if(line.startsWith('PORTICO_REPAIR_JSON:')){const v=JSON.parse(line.slice('PORTICO_REPAIR_JSON:'.length));const next=pending.shift();if(next)next(v);else ready.push(v)}else diagnostics+=line+'\n'});
 const receive=()=>ready.length?Promise.resolve(ready.shift()):new Promise<any>((resolve,reject)=>{pending.push(resolve);proc.once('exit',code=>{if(code!==0)reject(new Error(`actual HTTP bridge failed (${code}): ${diagnostics}`))})});
 const initial=await receive();
 return {initial,request:async(path:string,method:string,body?:unknown,token?:string)=>{proc.stdin.write(JSON.stringify({Path:path,Method:method,Body:body,Token:token})+'\n');return receive()},close:async()=>{proc.stdin.end(JSON.stringify({Path:'stop'})+'\n');await new Promise<void>((resolve,reject)=>proc.once('exit',code=>code===0?resolve():reject(new Error('actual HTTP bridge failed'))));rmSync(folder,{recursive:true,force:true})}};
}
test('exported local setup intent and generated finish body reach actual strict route and writer',{timeout:120_000},async()=>{
 const b=await bridge();try{
  let token='';let request:any;
  const api=Object.create(HttpLocalApi.prototype) as HttpLocalApi;
  Object.assign(api,{request:async(path:string,method:string,body:unknown)=>{request=body;const r=await b.request(path,method,body);assert.equal(r.status,201);token=r.body.accessToken;return r.body}});
  const session=await api.setup(b.initial.setupToken,'owner','Local-long-password','Fixture');assert.equal(request.authMode,'local');assert.equal(session.viewer.authority,'local');assert.equal('recoverySaved' in request,false);
  const state=await b.request('/v1/setup/state','GET',undefined,token);assert.equal(state.status,200);
  const body=routeBody('post_setup_finish',{revision:state.body.revision});assert.deepEqual(Object.keys(body),['revision']);decode('PostSetupFinishRequest',body);
  assert.equal((await b.request('/v1/setup/finish','POST',body,token)).status,200);
  assert.equal((await b.request('/v1/setup/finish','POST',{...body,enabled:true,warningConfirmed:true},token)).status,400);
  assert.notEqual((await b.request('/v1/setup/finish','POST',{revision:'999'},token)).status,200);
  assert.notEqual((await b.request('/v1/setup/finish','POST',body)).status,200);
  const caps=await b.request('/v1/auth/capabilities','GET');assert.equal(caps.status,200);assert.ok(caps.body.methods.includes('password'));assert.equal(caps.body.methods.includes('hosted'),false);
 }finally{await b.close()}
});
test('hosted omission keeps genuine pending setup and never silently creates local owner',{timeout:120_000},async()=>{
 const b=await bridge();try{const result=await b.request('/v1/setup','POST',{setupToken:b.initial.setupToken,name:'Fixture'});assert.equal(result.status,201);assert.equal(result.body.authMode,'hosted');assert.equal(result.body.claimRequired,true);assert.equal(result.body.accessToken,undefined);const caps=await b.request('/v1/auth/capabilities','GET');assert.equal(caps.body.methods.includes('password'),false);assert.equal(caps.body.methods.includes('hosted'),false)}finally{await b.close()}
});
test('generated claim prepare represents bounded v2 controls and refuses malformed purpose/role/id',()=>{
 for(const purpose of ['setup','switch','change-owner'] as const){const body=routeBody('post_networking_claim_prepare',{purpose,...(purpose==='change-owner'?{previousOwnerRole:'admin' as const}:{}),returnRegistrationId:'ret_1',expected:{serverId:'srv_fixture',localGeneration:'0'}});assert.equal(body.purpose,purpose);assert.deepEqual(JSON.parse(JSON.stringify(decode('PostNetworkingClaimPrepareRequest',body))),body)}
 for(const bad of [{purpose:'other'},{purpose:'change-owner',previousOwnerRole:'owner'},{returnRegistrationId:'https://evil.example'}])assert.throws(()=>decode('PostNetworkingClaimPrepareRequest',{...bad,expected:{serverId:'srv_fixture',localGeneration:'0'}}));
});
