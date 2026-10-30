import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {connectPairedPorticoServer,type ConnectionEnvironment} from '../src/server-connections.ts';
const keys=generateKeyPairSync('ed25519'),raw=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const pin={serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(raw).digest('base64url'),publicKey:raw.toString('base64url'),fingerprint:createHash('sha256').update(raw).digest('base64url')};
const signed=(value:unknown)=>{const payload=Buffer.from(JSON.stringify(value));return {payload:payload.toString('base64url'),signature:sign(null,payload,keys.privateKey).toString('base64url')};};
const source={origin:'https://account.example',accountId:'account_waiting',request:async<T>():Promise<T>=>{throw new Error('Waiting accounts have no directory row.');}};
const json=(body:unknown)=>new Response(JSON.stringify(body),{headers:{'Content-Type':'application/json'}});
function environment(options:{wrongProof?:boolean;key?:unknown;abort?:AbortController;signIn?:'local'|'portico'}={}){
 const calls:string[]=[];let writes=0;
 const env:ConnectionEnvironment={storage:{read:async()=>undefined,change:async()=>{writes++;}},fetcher:async(input,init)=>{
  const url=String(input);calls.push(url);
  if(url==='https://server.example/v1/system')return json({id:pin.serverId,name:'Server',version:'test',signIn:options.signIn??'portico',move:options.signIn==='local'?null:'to_portico',setupRequired:false,hostedConfigured:true,hostedAttached:true, apiLevel: 1, apiVersion: '1.0', apiMajors: [1], capabilities: []});
  if(url==='https://account.example/v1/keys'){
   assert.equal(init?.redirect,'error');assert.equal(init?.credentials,'omit');assert.equal(new Headers(init?.headers).get('Authorization'),null);
   if(options.abort)options.abort.abort();return json(options.key??{keyId:'route_key',publicKey:pin.publicKey});
  }
  assert.equal(url,'https://server.example/v1/networking/identity-proof');
  const q=JSON.parse(String(init?.body));return json(signed({kind:'portico.route-proof',version:'1',...pin,...q,...(options.wrongProof?{nonce:'wrong'}:{}),issuedAt:new Date().toISOString(),expiresAt:new Date(Date.now()+29000).toISOString()}));
 }};
 return {env,calls,writes:()=>writes};
}
test('a Waiting account pairs a proven Portico server without reading the directory or saving a credential',async()=>{
 const f=environment(),result=await connectPairedPorticoServer('https://server.example',pin,source,f.env,new AbortController().signal);
 try{assert.equal(result.api.getRouteConnection(),result.connection);assert.deepEqual(result.hosted,{origin:source.origin,accountId:source.accountId,key:{keyId:'route_key',publicKey:pin.publicKey}});assert.deepEqual(f.calls,['https://server.example/v1/networking/identity-proof','https://server.example/v1/system','https://account.example/v1/keys']);assert.equal(f.writes(),0);assert.equal(result.connection.getMemory().current,undefined);}finally{result.connection.dispose();}
});
test('failed server proof never contacts the account service',async()=>{
 const f=environment({wrongProof:true});await assert.rejects(connectPairedPorticoServer('https://server.example',pin,source,f.env,new AbortController().signal));assert.equal(f.calls.some(v=>v.startsWith(source.origin)),false);assert.equal(f.writes(),0);
});
test('a Local kind answer never contacts the account service',async()=>{
 const f=environment({signIn:'local'});await assert.rejects(connectPairedPorticoServer('https://server.example',pin,source,f.env,new AbortController().signal));assert.equal(f.calls.some(v=>v.startsWith(source.origin)),false);assert.equal(f.writes(),0);
});
test('invalid account origin, malformed TLS key response and cancellation cannot publish a candidate',async()=>{
 const f=environment();await assert.rejects(connectPairedPorticoServer('https://server.example',pin,{...source,origin:'http://account.example'},f.env,new AbortController().signal));assert.deepEqual(f.calls,[]);
 for(const key of [{keyId:'route_key',publicKey:'short'},{publicKey:pin.publicKey},[]]){const g=environment({key});await assert.rejects(connectPairedPorticoServer('https://server.example',pin,source,g.env,new AbortController().signal));assert.equal(g.writes(),0);}
 const controller=new AbortController(),g=environment({abort:controller});await assert.rejects(connectPairedPorticoServer('https://server.example',pin,source,g.env,controller.signal));assert.equal(g.writes(),0);
});
