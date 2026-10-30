import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash,generateKeyPairSync,sign} from 'node:crypto';
import {addressClass,decodeAddressLabel,encodeAddressLabel,formatIP,isLocalHost,parseDirectName,parseIP} from '../src/network-locality.ts';
import {candidateAllowed,castOrigin,connectionDetails,connectionStatusKey,derivedLocalOrigin,effectiveInsecure,insecureChoicesFor,type ConnectionPolicy} from '../src/connection-policy.ts';
import {MAX_ROUTE_CANDIDATES,RouteError,privateRouteHost,verifyRoutes,type RouteCandidate} from '../src/route-identity.ts';
import {RouteConnection,parseNetworkFacts} from '../src/route-connection.ts';
import {serverAddressCandidates} from '../src/server-address.ts';
import {parseConnectionDocument} from '../src/server-administration.ts';

const keys=generateKeyPairSync('ed25519');
const rawKey=keys.publicKey.export({format:'der',type:'spki'}).subarray(-32);
const publicKey=rawKey.toString('base64url');
const pin={publicKey,serverId:'srv_'+createHash('sha256').update('portico.server.identity.v1\0').update(rawKey).digest('base64url'),fingerprint:createHash('sha256').update(rawKey).digest('base64url')};
const key={keyId:'routes-key',publicKey};
const expected={accountId:'account',serverId:pin.serverId};
const ns='ptc-aaaaaaaaaaaaaaaaaaaa.portico.direct';
const named=(address:string,port=32500)=>'https://'+encodeAddressLabel(parseIP(address)!)+'.'+ns+(port===443?'':':'+port);
const auto=(cls:'lan'|'public',address:string,port=32500,extra:Record<string,unknown>={})=>({baseUrl:named(address,port),class:cls,insecure:false,address,port,quality:'probe_required',generation:'1',...extra});
function signed(candidates:unknown[]){
 const now=Date.now();
 const raw=Buffer.from(JSON.stringify({kind:'portico.routes',version:'1',algorithm:'Ed25519',keyId:key.keyId,audience:expected.accountId,...pin,name:'Media server',policyRevision:1,claimGeneration:'1',credentialGeneration:'1',generation:'1',issuedAt:new Date(now).toISOString(),expiresAt:new Date(now+45*60000).toISOString(),candidates}));
 return {payload:raw.toString('base64url'),signature:sign(null,raw,keys.privateKey).toString('base64url')};
}
const verify=(candidates:unknown[])=>verifyRoutes(signed(candidates),key,expected);

test('IP names: IPv4 dashed, IPv6 RFC 5952 dashed, eight full groups only when :: would lead or trail',()=>{
 assert.equal(encodeAddressLabel(parseIP('66.94.102.39')!),'66-94-102-39');
 assert.equal(encodeAddressLabel(parseIP('2605:a144:2327:3137::1')!),'2605-a144-2327-3137--1');
 assert.equal(encodeAddressLabel(parseIP('fd00::')!),'fd00-0-0-0-0-0-0-0');
 assert.equal(encodeAddressLabel(parseIP('::ffff:1')!),'0-0-0-0-0-0-ffff-1');
 for(const [label,text] of [['66-94-102-39','66.94.102.39'],['2605-a144-2327-3137--1','2605:a144:2327:3137::1'],['fd00-0-0-0-0-0-0-0','fd00::']])assert.equal(formatIP(decodeAddressLabel(label)!),text,label);
 // Not canonical, ambiguous, or not a DNS label.
 for(const label of ['fd00-0000-0000-0000-0000-0000-0000-0000','fd00-0-0-0-0-0-0-1','066-94-102-39','66-94-102-256','66-94-102','2605-A144--1','2605-a144-0-0-0-0-0-1','--1','fd00--','2605-a144-2327-3137-0-0-0-1','1-2-3-4-5'])assert.equal(decodeAddressLabel(label),undefined,label);
 assert.equal(parseDirectName('66-94-102-39.'+ns)?.suffix,ns);
 assert.equal(parseDirectName('66-94-102-39.ptc-short.portico.direct'),undefined);
 assert.equal(parseDirectName('l-0123456789abcdef0123456789abcdef.'+ns),undefined);
});

test('one locality definition: home ranges local, overlays and public remote, LAN networks authoritative',()=>{
 const table:[string,string][]=[['127.0.0.1','loopback'],['::1','loopback'],['localhost','loopback'],['10.1.2.3','lan'],['172.16.0.1','lan'],['172.17.0.1','lan'],['192.168.1.10','lan'],['169.254.1.1','lan'],['fe80::1','lan'],['fd12:3456::1','lan'],
  ['100.64.0.1','overlay'],['100.127.255.254','overlay'],['fd7a:115c:a1e0::1','overlay'],['nas.tailnet.ts.net','overlay'],['8.8.8.8','public'],['2001:db8::1','public'],['media.example.com','public'],['nas','lan'],['nas.local','lan'],['nas.home.arpa','lan'],
  ['192-168-1-10.'+ns,'lan'],['100-101-102-103.'+ns,'overlay'],['66-94-102-39.'+ns,'public']];
 for(const [host,cls] of table)assert.equal(addressClass(host),cls,host);
 // Before the server's list is known.
 assert.equal(isLocalHost('192.168.1.10'),true);
 assert.equal(isLocalHost('100.101.102.103'),false);
 assert.equal(isLocalHost('fd7a:115c:a1e0::5'),false);
 // The server's list is the whole answer; empty means everything is remote but loopback.
 const home={lanNetworks:['192.168.1.0/24']};
 assert.equal(isLocalHost('192.168.1.10',home),true);
 assert.equal(isLocalHost('10.0.0.5',home),false);
 assert.equal(isLocalHost('192.168.1.10',{lanNetworks:[]}),false);
 assert.equal(isLocalHost('127.0.0.1',{lanNetworks:[]}),true);
 // Tailscale counts as home once the owner lists it.
 const tailnet={lanNetworks:['192.168.1.0/24','100.64.0.0/10','fd7a:115c:a1e0::/48']};
 assert.equal(isLocalHost('100.101.102.103',tailnet),true);
 assert.equal(isLocalHost('100-101-102-103.'+ns,tailnet),true);
 assert.equal(isLocalHost('fd7a:115c:a1e0::5',tailnet),true);
 // privateRouteHost no longer counts Tailscale's IPv6 range as private (P2-68 B5).
 assert.equal(privateRouteHost('fd7a:115c:a1e0::5'),false);
 assert.equal(privateRouteHost('fd12::5'),true);
});

const apple:ConnectionPolicy={insecure:'always',platform:'apple'};
test('insecure policy matrix, the Apple clamp, typed addresses and Secure connections: Required',()=>{
 const p=(insecure:'never'|'local'|'always',platform:ConnectionPolicy['platform']='native'):ConnectionPolicy=>({insecure,platform});
 const localHttp='http://192.168.1.10:32500',remoteHttp='http://203.0.113.9:32500',tailnetHttp='http://100.101.102.103:32500';
 assert.equal(candidateAllowed('https://anything.example',"listed",p('never')),true);
 assert.equal(candidateAllowed(localHttp,'listed',p('never')),false);
 assert.equal(candidateAllowed(localHttp,'listed',p('local')),true);
 assert.equal(candidateAllowed(remoteHttp,'listed',p('local')),false);
 assert.equal(candidateAllowed(remoteHttp,'listed',p('always')),true);
 assert.equal(candidateAllowed(tailnetHttp,'listed',p('local')),false);
 assert.equal(candidateAllowed(tailnetHttp,'listed',p('local'),{lanNetworks:['100.64.0.0/10']}),true);
 // Typed http:// is the person's choice; Never does not remove it.
 assert.equal(candidateAllowed(remoteHttp,'typed',p('never')),true);
 // Apple: no remote plain HTTP whatever the setting or source; Always reads as On my network.
 assert.equal(effectiveInsecure(apple),'local');
 assert.deepEqual(insecureChoicesFor('apple'),['never','local']);
 assert.equal(candidateAllowed(remoteHttp,'listed',apple),false);
 assert.equal(candidateAllowed(remoteHttp,'typed',apple),false);
 assert.equal(candidateAllowed('http://media.example.com:32500','typed',apple),false);
 assert.equal(candidateAllowed('http://nas.tailnet.ts.net:32500','typed',apple),false);
 assert.equal(candidateAllowed(localHttp,'listed',apple),true);
 assert.equal(candidateAllowed('http://nas.local:32500','typed',apple),true);
 // Web on its HTTPS origin: private literals only, and only with Local Network Access.
 assert.equal(candidateAllowed(localHttp,'derived',{insecure:'local',platform:'web-hosted'}),false);
 assert.equal(candidateAllowed(localHttp,'derived',{insecure:'local',platform:'web-hosted',webPrivateLiteralHttp:true}),true);
 assert.equal(candidateAllowed('http://nas.local:32500','typed',{insecure:'always',platform:'web-hosted',webPrivateLiteralHttp:true}),false);
 assert.equal(candidateAllowed(localHttp,'listed',{insecure:'always',platform:'cast'}),false);
 // Required refuses every plain route, typed included.
 for(const source of ['listed','typed','derived','nearby'] as const)assert.equal(candidateAllowed(localHttp,source,p('always'),{secureConnections:'required'}),false,source);
 assert.equal(derivedLocalOrigin('fd12::5',32500),'http://[fd12::5]:32500');
});

test('a scheme-less address is HTTPS only under Never; a typed http:// stays',()=>{
 assert.deepEqual(serverAddressCandidates('192.168.1.20',{insecure:'never'}),['https://192.168.1.20:32500','https://192.168.1.20']);
 assert.deepEqual(serverAddressCandidates('http://192.168.1.20:32500',{insecure:'never'}),['http://192.168.1.20:32500']);
 assert.equal(serverAddressCandidates('192.168.1.20')[0],'http://192.168.1.20:32500');
});

test('signed candidates follow P2-67 Gate 2 exactly',async()=>{
 const good=[auto('lan','192.168.1.10'),auto('lan','fd12:3456::10'),auto('public','66.94.102.39',42424),auto('public','2605:a144:2327:3137::1'),
  {baseUrl:'https://media.example.com',class:'manual',insecure:false,quality:'probe_required',generation:'1'},
  {baseUrl:'http://100.101.102.103:32500',class:'manual',insecure:true,quality:'probe_required',generation:'1'}];
 const verified=await verify(good);
 assert.deepEqual(verified.document.candidates.map(c=>[c.class,c.insecure,c.port]),[['lan',false,32500],['lan',false,32500],['public',false,42424],['public',false,32500],['manual',false,undefined],['manual',true,undefined]]);
 // quality and generation default when absent.
 const {quality:_q,generation:_g,...bare}=auto('lan','192.168.1.11');
 assert.deepEqual([...(await verify([bare])).document.candidates].map(c=>[c.quality,c.generation]),[['probe_required','1']]);
 const rejects:[string,unknown][]=[
  ['http lan',{...auto('lan','192.168.1.10'),baseUrl:'http://192.168.1.10:32500',insecure:true}],
  ['insecure mismatch',{...auto('public','66.94.102.39'),insecure:true}],
  ['manual http not flagged',{baseUrl:'http://media.example.com',class:'manual',insecure:false,quality:'probe_required',generation:'1'}],
  ['missing insecure',(({insecure:_,...c})=>c)(auto('lan','192.168.1.10'))],
  ['address mismatch',{...auto('lan','192.168.1.10'),address:'192.168.1.11'}],
  ['port mismatch',{...auto('lan','192.168.1.10'),port:32501}],
  ['non-canonical address',{...auto('public','2605:a144:2327:3137::1'),address:'2605:A144:2327:3137:0:0:0:1'}],
  ['no address',(({address:_,...c})=>c)(auto('lan','192.168.1.10'))],
  ['old l- alias',{baseUrl:'https://l-0123456789abcdef0123456789abcdef.'+ns+':32500',class:'lan',insecure:false,address:'192.168.1.10',port:32500,quality:'probe_required',generation:'1'}],
  ['public lan',auto('lan','66.94.102.39')],
  ['private public',auto('public','192.168.1.10')],
  ['overlay public',auto('public','100.101.102.103')],
  ['loopback lan',auto('lan','127.0.0.1')],
  ['manual with address',{baseUrl:'https://media.example.com',class:'manual',insecure:false,address:'66.94.102.39',quality:'probe_required',generation:'1'}],
  ['unknown key',{...auto('lan','192.168.1.10'),family:'ipv4'}],
  ['relay class',{...auto('public','66.94.102.39'),class:'relay'}],
 ];
 for(const [why,candidate] of rejects)await assert.rejects(verify([candidate]),(e:RouteError)=>e.code==='invalid_routes',why);
 // Another server's namespace in the same document.
 await assert.rejects(verify([auto('lan','192.168.1.10'),{...auto('public','66.94.102.39'),baseUrl:named('66.94.102.39').replace('ptc-aaaaaaaaaaaaaaaaaaaa','ptc-bbbbbbbbbbbbbbbbbbbb')}]),(e:RouteError)=>e.code==='invalid_routes');
 // The Gate 2 budget is 80.
 const many=Array.from({length:MAX_ROUTE_CANDIDATES},(_,i)=>auto('lan','10.1.'+Math.floor(i/200)+'.'+(i%200+1)));
 assert.equal((await verify(many)).document.candidates.length,80);
 await assert.rejects(verify([...many,auto('lan','10.9.9.9')]),(e:RouteError)=>e.code==='invalid_routes');
});

test('ConnectionDocument and network facts carry LAN networks and Secure connections',()=>{
 const doc=parseConnectionDocument({secure:true,remote:false,plainHttpOutsideHome:false,clientAddress:'192.168.1.5',accessUrls:[],lanNetworks:['192.168.1.0/24','nonsense','100.64.0.0/10'],secureConnections:'required'});
 assert.deepEqual(doc.lanNetworks,['192.168.1.0/24','100.64.0.0/10']);
 assert.equal(doc.secureConnections,'required');
 assert.equal(parseConnectionDocument({secure:false,remote:true,accessUrls:[]}).lanNetworks,undefined);
 assert.deepEqual(parseNetworkFacts({remote:false,lanNetworks:[],secureConnections:'preferred'}),{remote:false,policy:{lanNetworks:[],secureConnections:'preferred'}});
 assert.equal(parseNetworkFacts({}).remote,undefined);
});

test('connection details and Cast origin',()=>{
 const d=connectionDetails('http://192.168.1.10:32500',undefined,false)!;
 assert.deepEqual([d.secure,d.local,d.confirmed,d.family,connectionStatusKey(d)],[false,true,true,'ipv4','insecureHome']);
 assert.equal(connectionStatusKey(connectionDetails(named('66.94.102.39'))!),'secureAway');
 assert.equal(connectionDetails(named('2605:a144:2327:3137::1'))!.family,'ipv6');
 assert.equal(connectionStatusKey(connectionDetails(named('66.94.102.39'),undefined,false)!),'secureHome');
 const listed=[{baseUrl:named('66.94.102.39'),class:'public'},{baseUrl:named('192.168.1.10'),class:'lan'},{baseUrl:'http://192.168.1.10:32500',class:'manual'}];
 assert.equal(castOrigin('https://current.example',listed),'https://current.example');
 assert.equal(castOrigin('http://192.168.1.10:32500',listed),named('192.168.1.10'));
 assert.equal(castOrigin('http://192.168.1.10:32500',[{baseUrl:'http://x.example',class:'manual'}]),undefined);
});

// Races. Probes are fakes; timings are real but short.
type Probe=(origin:string)=>Promise<unknown>;
async function race(candidates:RouteCandidate[]|unknown[],probe:Probe,options:{policy?:ConnectionPolicy;memory?:Record<string,unknown>;readConnection?:(origin:string)=>Promise<unknown>;paired?:string[];settle?:boolean}={}){
 const verified=await verify(candidates as unknown[]);
 const probes:string[]=[];
 const connection=new RouteConnection({pin:verified.document,logicalOrigin:verified.document.candidates[0]?.baseUrl??'https://x.example',memory:{current:verified,paired:options.paired??[],...options.memory},
  policy:()=>options.policy??{insecure:'local',platform:'native'},
  readConnection:async origin=>options.readConnection?options.readConnection(origin):{remote:false},
  probe:(async(origin:string)=>{probes.push(origin);await probe(origin);return verified.document;}) as never});
 try{const origin=await connection.resolve();if(options.settle)await wait(20);return {origin,probes,connection,snapshot:connection.getSnapshot(),memory:connection.getMemory()};}finally{connection.dispose();}
}
const fail=()=>Promise.reject(new RouteError('route_unavailable','down'));
const wait=(ms:number)=>new Promise(r=>setTimeout(r,ms));

test('a secure LAN route beats an insecure one even when the insecure proof answers first',async()=>{
 const lanName=named('192.168.1.10');
 const r=await race([auto('lan','192.168.1.10')],async origin=>{if(origin===lanName)await wait(400);});
 assert.equal(r.origin,lanName);
 // The twin proved itself (and was confirmed local) while the secure name was still pending.
 assert(r.probes.includes('http://192.168.1.10:32500'));
});

test('the derived HTTP twin wins only once every secure route has failed (router rebinding)',async()=>{
 const r=await race([auto('lan','192.168.1.10'),auto('public','66.94.102.39')],async origin=>{if(origin.startsWith('https:'))await fail();});
 assert.equal(r.origin,'http://192.168.1.10:32500');
 assert.deepEqual(r.probes.slice(0,1),[named('192.168.1.10')]);
 // Never: no twin, so nothing works.
 await assert.rejects(race([auto('lan','192.168.1.10')],async origin=>{if(origin.startsWith('https:'))await fail();},{policy:{insecure:'never',platform:'native'}}));
});

test('an insecure route the server counts as away is dropped before credentials',async()=>{
 await assert.rejects(race([auto('lan','192.168.1.10')],async origin=>{if(origin.startsWith('https:'))await fail();},{readConnection:async()=>({remote:true})}));
 await assert.rejects(race([auto('lan','192.168.1.10')],async origin=>{if(origin.startsWith('https:'))await fail();},{readConnection:async()=>{throw new Error('no answer');}}));
});

test('426 secure_connection_required is learned and plain routes stop',async()=>{
 const listed=[auto('lan','192.168.1.10'),{baseUrl:'http://192.168.1.10:32501',class:'manual',insecure:true,quality:'reachable',generation:'1'}];
 const err=await race(listed,async origin=>{if(origin.startsWith('https:'))await fail();throw new RouteError('secure_connection_required','required');}).catch(e=>e);
 assert.equal((err as RouteError).code,'route_unavailable');
 const remembered=await race(listed,async origin=>{if(origin.startsWith('http:'))throw new Error('must not be tried');},{memory:{policy:{secureConnections:'required',observedAt:new Date().toISOString()}}});
 assert.equal(remembered.origin,named('192.168.1.10'));
 assert(!remembered.probes.some(o=>o.startsWith('http:')));
});

test('IPv6 and IPv4 take turns, home before away, and attempts are paced',async()=>{
 const starts:[string,number][]=[];const t0=Date.now();
 const r=await race([auto('public','66.94.102.39'),auto('public','2605:a144:2327:3137::1'),auto('lan','192.168.1.10'),auto('lan','fd12:3456::10')],async origin=>{starts.push([origin,Date.now()-t0]);await fail();},{policy:{insecure:'never',platform:'native'}}).catch(e=>e);
 assert(r instanceof RouteError);
 assert.deepEqual(starts.map(s=>s[0]),[named('fd12:3456::10'),named('192.168.1.10'),named('2605:a144:2327:3137::1'),named('66.94.102.39')]);
});

test('Tailscale listed as a LAN network is raced as home; unlisted it waits behind home routes',async()=>{
 const tail=auto('lan','100.101.102.103'),home=auto('lan','192.168.1.10');
 const tailnet={lanNetworks:['192.168.1.0/24','100.64.0.0/10'],observedAt:new Date().toISOString()};
 const order:string[]=[];
 await race([tail,home],async origin=>{order.push(origin);await fail();},{memory:{policy:tailnet},policy:{insecure:'never',platform:'native'}}).catch(()=>{});
 assert.deepEqual(order,[tail.baseUrl,home.baseUrl]);
 const unlisted:string[]=[];
 await race([tail,home],async origin=>{unlisted.push(origin);await fail();},{memory:{policy:{lanNetworks:['192.168.1.0/24'],observedAt:new Date().toISOString()}},policy:{insecure:'never',platform:'native'}}).catch(()=>{});
 assert.deepEqual(unlisted,[home.baseUrl,tail.baseUrl]);
 // Owner-listed tailnet: its HTTP twin counts as home too.
 const twin=await race([tail],async origin=>{if(origin.startsWith('https:'))await fail();},{memory:{policy:tailnet}});
 assert.equal(twin.origin,'http://100.101.102.103:32500');
});

test('a Docker bridge address is never chosen over the real LAN address, and never gets an HTTP twin',async()=>{
 const docker=auto('lan','172.17.0.1'),real=auto('lan','192.168.1.10');
 const policy={lanNetworks:['192.168.1.0/24'],observedAt:new Date().toISOString()};
 // Docker's bridge answers nothing from another machine; the real address wins.
 const r=await race([docker,real],async origin=>{if(origin===docker.baseUrl)await wait(500);if(origin.includes('172-17'))await fail();},{memory:{policy}});
 assert.equal(r.origin,real.baseUrl);
 assert.equal(r.probes[0],real.baseUrl);
 // Rebinding: neither name resolves. Only the real address's twin is tried.
 const tried:string[]=[];
 const twin=await race([docker,real],async origin=>{tried.push(origin);if(origin.startsWith('https:'))await fail();},{memory:{policy}});
 assert.equal(twin.origin,'http://192.168.1.10:32500');
 assert(!tried.includes('http://172.17.0.1:32500'));
});

test('the chosen route records the server policy and its answer for connection details',async()=>{
 const r=await race([auto('lan','192.168.1.10')],async()=>{},{settle:true,readConnection:async()=>({remote:false,lanNetworks:['192.168.1.0/24'],secureConnections:'preferred'})});
 assert.equal(r.origin,named('192.168.1.10'));
 assert.deepEqual(r.memory.policy?.lanNetworks,['192.168.1.0/24']);
 assert.equal(r.connection.getSnapshot().remote,false);
});

test('browsers skip restricted ports; native keeps them; Cast never gets one',()=>{
 const web={insecure:'local' as const,platform:'web-hosted' as const};
 assert.equal(candidateAllowed('https://media.example.com:6000','listed',web),false);
 assert.equal(candidateAllowed('https://media.example.com:6000','typed',{insecure:'always',platform:'web-server'}),false);
 assert.equal(candidateAllowed('https://media.example.com:32500','listed',web),true);
 assert.equal(candidateAllowed('https://media.example.com:6000','listed',{insecure:'local',platform:'native'}),true);
 assert.equal(candidateAllowed('https://media.example.com:6000','listed',apple),true);
 assert.equal(castOrigin('https://current.example:6000',[{baseUrl:named('66.94.102.39',10080),class:'public'},{baseUrl:named('66.94.102.39'),class:'public'}]),named('66.94.102.39'));
});

test('remote-access warnings are read, worded from code and port, and owner pages accept 80 routes',async()=>{
 const {parseRemoteAccess,remoteWarningLines}=await import('../src/remote-access.ts');
 const base={authorityId:'a',generation:'1',state:'reachable',config:{revision:'1',enabled:true,mapping:true,pcp:true,natpmp:true,upnp:true,publicPort:6000,gateway:''},topology:{lan:[],public:[],bind:''},mappings:[],
  candidates:Array.from({length:80},(_,i)=>({baseUrl:named('10.1.0.'+(i+1)),class:'lan',state:'listed'}))};
 const parsed=parseRemoteAccess({...base,warnings:[{code:'browser_restricted_port',port:6000,message:"Web browsers can't use port 6000. The apps still can."},{code:'future_code',port:1,message:'Server words.'}]});
 assert.equal(parsed.candidates.length,80);
 const t=((key:string,values?:{port?:number})=>key==='remoteAccess.warning.browserRestrictedPort'?`Web browsers can’t use port ${values?.port}. The apps still can.`:key) as never;
 assert.deepEqual(remoteWarningLines(parsed,t),['Web browsers can’t use port 6000. The apps still can.','Server words.']);
 assert.deepEqual(parseRemoteAccess(base).warnings,[]);
 assert.throws(()=>parseRemoteAccess({...base,warnings:'nope'}));
});

test('Secure connections: read from the policy (Preferred by default), bound to its row, and Required asks first',async()=>{
 const {parseConnectivityPolicy}=await import('../src/server-administration.ts');
 const {settingsBinding}=await import('../src/presentation/settings-bindings.ts');
 const {settingsChangeConfirmation}=await import('../src/presentation/settings-change-confirm.ts');
 const base={revision:1,remoteSignInPolicy:'allow',connectionLimit:0,advertiseUrl:'',lanNetworks:[],accessUrls:[]};
 assert.equal(parseConnectivityPolicy(base).secureConnections,'preferred');
 assert.equal(parseConnectivityPolicy({...base,secureConnections:'required'}).secureConnections,'required');
 assert.throws(()=>parseConnectivityPolicy({...base,secureConnections:'disabled'}));
 assert.deepEqual(settingsBinding('remote.secureConnections'),{form:'connectivity',key:'secureConnections'});
 assert.equal(settingsChangeConfirmation('remote.secureConnections','required','preferred'),'settings.confirm.remoteAccess');
 assert.equal(settingsChangeConfirmation('remote.secureConnections','preferred','required'),undefined);
});
