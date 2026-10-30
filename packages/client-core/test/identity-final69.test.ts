import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash, generateKeyPairSync, sign} from 'node:crypto';
import {identityCode, normaliseIdentityCode, probeRoute, assertFirstContactIdentity, FirstContactIdentityError, privateRouteHost} from '../src/route-identity.ts';
import {bonjourProvesHome, RouteConnection} from '../src/route-connection.ts';
import {parseJoinLink} from '../src/join-link.ts';

const origin='https://media.example';
const keys=generateKeyPairSync('ed25519');
const raw=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
// Independent Node crypto derives fixture identity and signs producer-shaped envelopes.
const pin={serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(raw).digest('base64url'),publicKey:raw.toString('base64url'),fingerprint:createHash('sha256').update(raw).digest('base64url')};
const leaf=Buffer.alloc(32,7).toString('base64url'),otherLeaf=Buffer.alloc(32,9).toString('base64url');
const at=Date.now();
const record={baseUrl:'https://192.168.1.8:32500',port:32500,path:'/',serverId:pin.serverId,fingerprint:pin.fingerprint,expiresAt:at+60000};
async function proof(options:{origin?:string;version?:string;metadata?:string;fields?:Record<string,unknown>;headers?:Record<string,string>}={}){
 const observations=new WeakMap<Response,string>();
 const fetcher=(async (_input:unknown,init?:RequestInit)=>{
  assert.equal(init?.method,'POST');assert.equal(init?.credentials,'omit');assert.equal(init?.redirect,'error');
  const request=JSON.parse(String(init?.body));
  assert.deepEqual(Object.keys(request).sort(),['baseUrl','nonce']);
  const version=options.version??'2';
  const payload=Buffer.from(JSON.stringify({kind:'portico.route-proof',version,...pin,baseUrl:request.baseUrl,nonce:request.nonce,issuedAt:new Date(at).toISOString(),expiresAt:new Date(at+30000).toISOString(),...(version==='2'?{tlsCertificateFingerprints:[leaf]}:{}),...options.fields}));
  const response=new Response(JSON.stringify({payload:payload.toString('base64url'),signature:sign(null,payload,keys.privateKey).toString('base64url')}),{headers:{'Content-Type':'application/json',...options.headers}});
  if(options.metadata!==undefined)observations.set(response,options.metadata);
  return response;
 }) as typeof fetch;
 return probeRoute(options.origin??origin,undefined,{fetcher,now:()=>at,observedTLSLeaf:response=>observations.get(response)});
}

test('identity code matches the independently supplied final ruling vector',()=>{
 assert.equal(identityCode('srv_Wl4E3ID18LNZFgw_Gjc3R0liIOV67Gd2vD54Ubw6OAY'),'JJWY-C8J8-J26F');
 for(const invalid of ['', 'srv_'+ 'A'.repeat(42),'srv_'+ 'A'.repeat(44),'srv_'+ 'A'.repeat(42)+'B'])assert.throws(()=>identityCode(invalid));
});
test('code input is complete, ASCII, case-insensitive and ignores only spaces/dashes',()=>{
 assert.equal(normaliseIdentityCode('jjwy c8j8-j26f'),'JJWY-C8J8-J26F');
 for(const invalid of ['JJWY-C8J8-J26','JJWY-C8J8-J26FF','IJWY-C8J8-J26F','0JWY-C8J8-J26F','JJWY_C8J8_J26F','ßJWY-C8J8-J26','JJWY-C8J8-J26😀'])assert.equal(normaliseIdentityCode(invalid),undefined,invalid);
});
test('Bonjour home requires independently key-derived TXT identity and exact fresh proof-response leaf',async()=>{
 const p=await proof({metadata:leaf});
 assert.equal(bonjourProvesHome(origin,p,leaf,[record],at),true);
 assert.equal(bonjourProvesHome(origin,p,leaf,[record],at+29999),true);
 for(const records of [[],[{...record,serverId:'srv_'+'A'.repeat(43)}],[{...record,fingerprint:leaf}],[{...record,expiresAt:at}],[record,{...record,serverId:'other'}],[{...record,path:'/not-proof'}]])assert.equal(bonjourProvesHome(origin,p,leaf,records,at),false);
 assert.equal(bonjourProvesHome(origin,p,otherLeaf,[record],at),false);
 assert.equal(bonjourProvesHome(origin,p,leaf,[record],at+30000),false);
 assert.equal(bonjourProvesHome(origin,p,leaf,[record],at-1),false);
 assert.equal(bonjourProvesHome('https://other.example',p,leaf,[record],at),false);
 assert.equal(bonjourProvesHome(origin,{...p},leaf,[record],at),false);
});
test('no metadata, a response header, another observed leaf, or v1 cannot supply Bonjour home trust',async()=>{
 assert.equal(bonjourProvesHome(origin,await proof(),leaf,[record],at),false);
 assert.equal(bonjourProvesHome(origin,await proof({headers:{'X-TLS-Leaf-Fingerprint':leaf}}),leaf,[record],at),false);
 assert.equal(bonjourProvesHome(origin,await proof({metadata:otherLeaf}),otherLeaf,[record],at),false);
 assert.equal(bonjourProvesHome(origin,await proof({version:'1',metadata:leaf}),leaf,[record],at),false);
});
test('proof verifier refuses signed wrong challenge/origin/key claims before any home decision',async()=>{
 for(const fields of [{nonce:'wrong'},{baseUrl:'https://other.example'},{fingerprint:leaf},{serverId:'srv_'+'A'.repeat(43)}])await assert.rejects(proof({metadata:leaf,fields}),{code:'identity_mismatch'});
});
test('CGNAT and Tailscale ranges remain away',async()=>{
 for(const host of ['100.64.0.1','100.100.100.100','100.127.255.254']){
  const address='https://'+host,p=await proof({origin:address,metadata:leaf});
  assert.equal(privateRouteHost(host),false);
  assert.equal(bonjourProvesHome(address,p,leaf,[record],at),false);
 }
});
test('typed and linked first contact cannot take over a saved Local origin',()=>{
 const other={...pin,serverId:'srv_'+'A'.repeat(43)};
 const knownRoutes=[{signIn:'local' as const,pin:other,origins:[origin]}];
 for(const context of [{knownRoutes},{knownRoutes,linkId:pin.serverId}])assert.throws(()=>assertFirstContactIdentity(origin,pin,context),(error:unknown)=>error instanceof FirstContactIdentityError&&error.savedServerId===other.serverId);
 assert.throws(()=>assertFirstContactIdentity(origin,pin,{knownRoutes,linkId:other.serverId}),(error:unknown)=>error instanceof FirstContactIdentityError&&error.savedServerId===undefined);
 assert.doesNotThrow(()=>assertFirstContactIdentity(origin,pin,{knownRoutes:[],linkId:pin.serverId}));
});
test('join links reject duplicates, extra fields and malformed identity expectations',()=>{
 const base='portico://join#code='+'x'.repeat(32)+'&server='+encodeURIComponent(origin);
 assert.deepEqual(parseJoinLink(base+'&id='+pin.serverId),{code:'x'.repeat(32),server:origin,id:pin.serverId});
 for(const extra of ['&id=bad','&id='+pin.serverId+'&id='+pin.serverId,'&other=x','&code='+'x'.repeat(32)])assert.equal(parseJoinLink(base+extra),undefined);
});
test('all-certificate route failure propagates unconfirmed; a mixed network failure stays offline',async()=>{
 for(const mixed of [false,true]){
  const connection=new RouteConnection({pin,logicalOrigin:origin,memory:{paired:[origin,'https://other.example']},probe:async url=>{throw Object.assign(new Error('synthetic transport failure'),{code:mixed&&url!==origin?'network':'certificate_untrusted'});}});
  try{await assert.rejects(connection.resolve(),{code:mixed?'route_unavailable':'certificate_unverified'});assert.equal(connection.getSnapshot().phase,mixed?'offline':'unconfirmed');}
  finally{connection.dispose();}
 }
});
