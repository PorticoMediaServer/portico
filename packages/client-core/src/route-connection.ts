import {routePath} from '../../contracts/src/route-paths.ts';
import {candidateAllowed,DEFAULT_CONNECTION_POLICY,derivedLocalOrigin,insecureNeedsLocalConfirmation,type CandidateSource,type ConnectionPolicy} from './connection-policy.ts';
import {addressClass,hostAddress,isLocalHost,parseCIDR,type ServerNetworkPolicy} from './network-locality.ts';
import {RouteError,SECURE_CONNECTION_REQUIRED,probeRoute,routeJSON,routeOrigin,routeBytes,observedRouteTLSLeaf,privateRouteHost,sameServerPin,webRouteCrypto,type RouteCrypto,type RouteCandidate,type RouteProof,type ServerPin,type VerifiedRoutes} from './route-identity.ts';
export type DiscoveryRoute = Readonly<{baseUrl:string;serverId:string;fingerprint:string;port:number;path:string;expiresAt:number}>;
export type RouteSnapshot = Readonly<{phase:'idle'|'probing'|'ready'|'offline'|'identity_mismatch'|'unconfirmed';errorCode?:string;origin:string|null;
 /** The server's own answer for `origin`: whether it counts this device as away from home. */
 remote?:boolean;revision:number;network:string;message:string;discovery:'unknown'|'available'|'unsupported'|'denied'|'empty'}>;
/** The server's network policy last read from it (`/v1/networking/connection`), so the next launch
 * ranks and filters candidates before any request. */
export type RememberedNetworkPolicy = Readonly<ServerNetworkPolicy & {observedAt:string}>;
export type RouteMemory = Readonly<{current?:VerifiedRoutes;previous?:VerifiedRoutes;paired:readonly string[];lastGood?:string;policy?:RememberedNetworkPolicy}>;
const probeFailureCodes = ['invalid_address','invalid_identity','invalid_response','identity_mismatch','invalid_signature','invalid_routes','route_stale','clocks_disagree','insecure_context','authentication_required','access_denied','rate_limited','route_unavailable','certificate_unverified','certificate_untrusted','secure_connection_required','cancelled'] as const;
const probeFailureNames = ['RouteError','Error','TypeError','SyntaxError','ReferenceError','RangeError','AbortError'] as const;
/** Diagnostic categories only: never the error, message, stack, address or proof. */
export type RouteProbeFailure = Readonly<{code:typeof probeFailureCodes[number]|'other';name:typeof probeFailureNames[number]|'unknown'}>;
function probeFailure(error:unknown):RouteProbeFailure {
 let code:RouteProbeFailure['code']='other',name:RouteProbeFailure['name']='unknown';
 try {
  const value=error as {code?:unknown;name?:unknown}|null;
  if(typeof value?.code==='string'&&(probeFailureCodes as readonly string[]).includes(value.code))code=value.code as RouteProbeFailure['code'];
  if(typeof value?.name==='string'&&(probeFailureNames as readonly string[]).includes(value.name))name=value.name as RouteProbeFailure['name'];
 }catch{/* Hostile accessors do not enter diagnostics or change the probe outcome. */}
 return Object.freeze({code,name});
}
export interface RouteConnectionOptions {
 pin:ServerPin;
 logicalOrigin:string;
 memory?:RouteMemory;
 crypto?:RouteCrypto;
 fetcher?:typeof fetch;
 discover?:(signal:AbortSignal)=>Promise<readonly DiscoveryRoute[]>;
 fresh?:(signal:AbortSignal)=>Promise<VerifiedRoutes>;
 /** How long remembered routes may stay silent before `fresh` is asked. */
 freshAfterMs?:number;
 persist?:(memory:RouteMemory)=>Promise<void>;
 probe?:typeof probeRoute;
 /** Optional passive observation before a current candidate's failure is folded
  * into the race result. Aborted/retired candidates are not reported. */
 onProbeFailure?:(failure:RouteProbeFailure)=>void;
 /** This device's insecure-connection setting and platform, read at every race. */
 policy?:()=>ConnectionPolicy;
 /** Reads `/v1/networking/connection` at an origin without credentials. When supplied, the chosen
  * route's facts are read in the background (connection details, LAN networks, Secure connections);
  * an insecure route's confirmation falls back to `fetcher` without it. */
 readConnection?:(origin:string,signal:AbortSignal)=>Promise<unknown>;
}
/** The facts of `/v1/networking/connection` this connection uses. A missing or unreadable answer
 * leaves the remembered policy as it was; `remote` must be an explicit false to count as home. */
export function parseNetworkFacts(raw:unknown):{remote?:boolean;policy:ServerNetworkPolicy}{
 const v=raw&&typeof raw==='object'&&!Array.isArray(raw)?raw as Record<string,unknown>:{};
 const lanNetworks=Array.isArray(v.lanNetworks)&&v.lanNetworks.length<=64?v.lanNetworks.filter((x):x is string=>typeof x==='string'&&!!parseCIDR(x)):undefined;
 const secureConnections=v.secureConnections==='required'||v.secureConnections==='preferred'?v.secureConnections:undefined;
 return {remote:typeof v.remote==='boolean'?v.remote:undefined,policy:{...(lanNetworks?{lanNetworks}:{}),...(secureConnections?{secureConnections}:{})}};
}
type RaceEntry={candidate:RouteCandidate;source:CandidateSource;secure:boolean;local:boolean;family:4|6|0};
/** Plex-like order: secure before insecure, home before away, IPv6 and IPv4 taking turns (RFC 8305). */
function tier(e:RaceEntry){return (e.secure?0:2)+(e.local?0:1);}
function orderEntries(entries:readonly RaceEntry[],lastGood?:string):RaceEntry[]{
 const out:RaceEntry[]=[];const first=entries.find(e=>e.candidate.baseUrl===lastGood);if(first)out.push(first);
 for(let t=0;t<4;t++){
  const group=entries.filter(e=>e!==first&&tier(e)===t).map((e,i)=>({e,i})).sort((a,b)=>Number(a.e.candidate.quality!=='reachable')-Number(b.e.candidate.quality!=='reachable')||a.i-b.i).map(x=>x.e);
  const v6=group.filter(e=>e.family===6),rest=group.filter(e=>e.family!==6);
  for(let i=0;i<Math.max(v6.length,rest.length);i++){if(v6[i])out.push(v6[i]!);if(rest[i])out.push(rest[i]!);}
 }
 return out;
}
/** Attempts start 150 ms apart (at once after a failure), at most four at a time, three at home. */
const ATTEMPT_DELAY_MS=150,MAX_ACTIVE=4,MAX_ACTIVE_LOCAL=3,LOCAL_HEAD_START_MS=120;
/** Normalize a Bonjour result without making it trusted. A conflicting group
 * contributes no route, including otherwise plausible records in that group. */
export function discoveredRoutes(records:readonly DiscoveryRoute[],pin:ServerPin,now=Date.now()):RouteCandidate[] {
 if(records.length>64)return [];
 const groups=new Map<string,DiscoveryRoute[]>();for(const record of records){const group=groups.get(record.fingerprint)??[];group.push(record);groups.set(record.fingerprint,group);}
 const group=groups.get(pin.fingerprint)??[];if(group.some(r=>r.serverId!==pin.serverId||r.path!==''&&r.path!=='/'||!Number.isInteger(r.port)||r.port<1||r.port>65535)||new Set(group.map(r=>r.port+'|'+r.path)).size>1)return [];
 const out:RouteCandidate[]=[];for(const r of group){if(!Number.isFinite(r.expiresAt)||r.expiresAt<=now||r.expiresAt>now+120000)continue;try{const origin=routeOrigin(r.baseUrl),u=new URL(origin);if(!privateRouteHost(u.hostname)||Number(u.port||(u.protocol==='https:'?443:80))!==r.port)continue;if(!out.some(c=>c.baseUrl===origin))out.push({baseUrl:origin,class:'lan',insecure:u.protocol==='http:',quality:'probe_required',generation:'1'});}catch{}}
 return out.slice(0,8);
}
/** Final identity ruling (design 69f2d45): Bonjour's fp remains SHA256(identity public key),
 * not a TLS fingerprint. Current-network discovery alone never authorizes a certificate.
 * probeRoute has already derived serverId/fingerprint from the key and verified the signed
 * nonce for this origin. The SAME native proof exchange must supply its actual observed leaf.
 * This function sends nothing and pins nothing; any absent/invalid fact takes the away path. */
export function bonjourProvesHome(origin:string,proof:RouteProof,observedTLSLeaf:string,records:readonly DiscoveryRoute[],now=Date.now()):boolean {
 try{
  const url=new URL(routeOrigin(origin));
  // The binding ruling keeps overlays (100.64/10, Tailscale's IPv6 range) away.
  if(addressClass(url.hostname)==='overlay')return false;
  if(url.protocol!=='https:'||observedRouteTLSLeaf(origin,proof,now)!==observedTLSLeaf)return false;
  routeBytes(observedTLSLeaf,32);
  if(!proof.tlsCertificateFingerprints?.includes(observedTLSLeaf))return false;
  // Existing discovery grouping rejects conflicting id/fp or service records and accepts
  // only current private routes. It compares both TXT values with the cryptographically
  // derived proof values; it never treats the identity-key fp as the observed TLS leaf.
  return discoveredRoutes(records,proof,now).length>0;
 }catch{return false;}
}
const routeFailureCode=(error:unknown):string|undefined=>{const code=(error as {code?:unknown}|null)?.code;return typeof code==='string'?code:undefined;};
const certificateFailure=(code:string|undefined)=>code==='certificate_unverified'||code==='certificate_untrusted';
function newer(a:VerifiedRoutes,b:VerifiedRoutes):boolean {
 if(a.document.policyRevision<b.document.policyRevision||Date.parse(a.document.issuedAt)<Date.parse(b.document.issuedAt))return false;
 for(const key of ['claimGeneration','credentialGeneration','generation'] as const){const x=BigInt(a.document[key]),y=BigInt(b.document[key]);if(x!==y)return x>y;}
 return Date.parse(a.document.issuedAt)>=Date.parse(b.document.issuedAt)&&a.document.policyRevision>=b.document.policyRevision;
}
/** Remembered routes are asked first, and the Portico Account service only if they fail
 * (at once) or are still silent after this long. A server that is merely slow to answer,
 * on a mobile link or waking from sleep, must not cost the account service a request. */
const FRESH_AFTER_MS=3000;
const failureMessage='Your server is unavailable. Your sign-in is saved, and Portico will keep trying to connect.';
export class RouteConnection {
 readonly pin:ServerPin;
 readonly logicalOrigin:string;
 private options:RouteConnectionOptions;
 private memory:RouteMemory;
 private state:RouteSnapshot;
 private listeners=new Set<()=>void>();
 private serial=0;
 private run?:{controller:AbortController;promise:Promise<string>};
 private disposed=false;
 private retry?:ReturnType<typeof setTimeout>;
 private attempts=0;
 private foreground=true;
 private lastForegroundRetry=0;
 private freshNotBefore=0;
 private promotion?:AbortController;
 private promotionNotBefore=0;
 private recent=new Set<string>();
 private persistTail:Promise<void>=Promise.resolve();
 private rejected=new Map<string,{retryAt:number;attempts:number}>();
 constructor(options:RouteConnectionOptions){
  this.options=options;this.pin=options.pin;this.logicalOrigin=routeOrigin(options.logicalOrigin);
  this.memory=options.memory??{paired:[]};for(const doc of [this.memory.current,this.memory.previous])if(doc&&!sameServerPin(doc.document,this.pin))throw new RouteError('identity_mismatch','Remembered routes belong to a different server.');
  if(this.memory.paired.length>8)throw new RouteError('invalid_routes','Too many remembered addresses.');this.memory={...this.memory,paired:this.memory.paired.map(routeOrigin)};
  this.state=Object.freeze({phase:'idle',origin:null,revision:0,network:'launch',message:'Verifying the server connection.',discovery:options.discover?'unknown':'unsupported'});
 }
 getSnapshot=():RouteSnapshot=>this.state;
 getMemory=():RouteMemory=>this.memory;
 subscribe=(listener:()=>void):(()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private publish(value:Partial<RouteSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...value,revision:this.state.revision+1});for(const listener of this.listeners)listener();}
 private save(){const memory=this.memory,persist=this.options.persist;this.persistTail=this.persistTail.catch(()=>{}).then(()=>persist?.(memory));void this.persistTail.catch(()=>{});}
 /** Persistence failure must prevent the initial credential publication, but
  * cannot revoke an already selected viewer during later location changes. */
 flush():Promise<void>{return this.persistTail;}
 setPersistence(persist:(memory:RouteMemory)=>Promise<void>){this.options.persist=persist;}
 setFreshSource(fresh:(signal:AbortSignal)=>Promise<VerifiedRoutes>){this.options.fresh=fresh;}
 rememberPaired(origin:string){const value=routeOrigin(origin);this.memory={...this.memory,paired:[value,...this.memory.paired.filter(v=>v!==value)].slice(0,8)};this.save();}
 install(document:VerifiedRoutes){
  if(!sameServerPin(document.document,this.pin)||this.memory.current&&!newer(document,this.memory.current))throw new RouteError('identity_mismatch','Connection evidence moved backwards or changed identity.');
  this.promotion?.abort();this.promotion=undefined;
  const old=this.memory.current,allowed=new Set(document.document.candidates.map(c=>c.baseUrl));
  // Hosted includes its one eligible previous-verified set in the new signed
  // response. An omitted old candidate must not be resurrected by local cache.
  const previous=old&&old.document.keyId===document.document.keyId&&old.document.candidates.some(c=>allowed.has(c.baseUrl))?old:undefined;
  this.memory={...this.memory,current:document,previous,lastGood:this.memory.lastGood&&allowed.has(this.memory.lastGood)?this.memory.lastGood:undefined};
  if(!old||old.document.generation!==document.document.generation||old.document.credentialGeneration!==document.document.credentialGeneration||old.document.claimGeneration!==document.document.claimGeneration)this.rejected.clear();this.save();
 }
 setForeground(value:boolean){
  if(this.disposed)return;this.foreground=value;if(!value){this.promotion?.abort();this.promotion=undefined;}
  if(value)this.foregroundActivity();
  else if(this.retry){clearTimeout(this.retry);this.scheduleRecovery();}
 }
 foregroundActivity(){
  if(this.state.phase==='ready'){this.promoteLocal();return;}
  if(this.disposed||!this.foreground||this.run||Date.now()-this.lastForegroundRetry<5000)return;
  this.lastForegroundRetry=Date.now();this.attempts=0;if(this.retry)clearTimeout(this.retry);this.retry=undefined;void this.resolve().catch(()=>{});
 }
 /** The policy and server facts every decision reads. */
 private connectionPolicy():ConnectionPolicy{try{return this.options.policy?.()??DEFAULT_CONNECTION_POLICY;}catch{return DEFAULT_CONNECTION_POLICY;}}
 private serverPolicy():ServerNetworkPolicy|undefined{return this.memory.policy;}
 private learnPolicy(policy:ServerNetworkPolicy){
  const next:RememberedNetworkPolicy={...(policy.lanNetworks?{lanNetworks:[...policy.lanNetworks]}:this.memory.policy?.lanNetworks?{lanNetworks:this.memory.policy.lanNetworks}:{}),...(policy.secureConnections?{secureConnections:policy.secureConnections}:this.memory.policy?.secureConnections?{secureConnections:this.memory.policy.secureConnections}:{}),observedAt:new Date().toISOString()};
  this.memory={...this.memory,policy:next};this.save();
 }
 private readFacts(origin:string,signal:AbortSignal):Promise<unknown>{
  if(this.options.readConnection)return this.options.readConnection(origin,signal);
  return (this.options.fetcher??globalThis.fetch)(origin+routePath('get_connection'),{method:'GET',redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',signal,headers:{Accept:'application/json'}}).then(r=>routeJSON(r,signal,8192));
 }
 /** Before credentials go over plain HTTP under "On my network", the server itself must count this
  * device as home. Anything else (away, no answer, unreadable) drops the route. */
 private async confirmLocal(origin:string,signal:AbortSignal):Promise<void>{
  const controller=new AbortController(),cancel=()=>controller.abort();signal.addEventListener('abort',cancel,{once:true});const timer=setTimeout(cancel,3000);
  try{const facts=parseNetworkFacts(await this.readFacts(origin,controller.signal));this.learnPolicy(facts.policy);if(facts.remote!==false)throw new RouteError('route_unavailable','The server counts this device as away from home.');}
  finally{clearTimeout(timer);signal.removeEventListener('abort',cancel);}
 }
 private factsAt=0;
 private confirmedLocal?:string;
 /** After a route is chosen: the server's answer for it (shown in connection details) and its
  * current policy, at most every ten minutes per origin. Never delays or changes the route. */
 private refreshFacts(origin:string,known?:boolean){
  if(known!==undefined){this.factsAt=Date.now();this.publish({remote:known});return;}
  if(!this.options.readConnection)return;
  if(Date.now()-this.factsAt<600000&&this.state.remote!==undefined)return;this.factsAt=Date.now();
  const controller=new AbortController(),timer=setTimeout(()=>controller.abort(),5000),generation=this.serial;
  void this.readFacts(origin,controller.signal).then(raw=>{if(this.disposed||generation!==this.serial||this.state.origin!==origin)return;const facts=parseNetworkFacts(raw);this.learnPolicy(facts.policy);if(facts.remote!==undefined)this.publish({remote:facts.remote});}).catch(()=>{}).finally(()=>clearTimeout(timer));
 }
 private entry(candidate:RouteCandidate,source:CandidateSource):RaceEntry{
  let host='';try{host=new URL(candidate.baseUrl).hostname;}catch{}
  const ip=hostAddress(host);
  return {candidate,source,secure:candidate.baseUrl.startsWith('https:'),local:!!host&&isLocalHost(host,this.serverPolicy()),family:ip?ip.family:0};
 }
 private allowed(e:RaceEntry){return candidateAllowed(e.candidate.baseUrl,e.source,this.connectionPolicy(),this.serverPolicy());}
 /** The plain-HTTP twins of signed LAN names, for a router that refuses to resolve them. */
 private derived(documents:readonly (VerifiedRoutes|undefined)[]):RouteCandidate[]{
  const out:RouteCandidate[]=[];
  for(const doc of documents)for(const c of doc?.document.candidates??[]){
   if(out.length>=4)break;
   if(c.class!=='lan'||!c.address||!c.port||!isLocalHost(c.address,this.serverPolicy()))continue;
   const origin=derivedLocalOrigin(c.address,c.port);if(origin&&!out.some(v=>v.baseUrl===origin))out.push({baseUrl:origin,class:'lan',insecure:true,quality:'probe_required',generation:c.generation});
  }
  return out;
 }
 /** Ordinary foreground activity may move to a better route (insecure to secure, or secure away to
  * secure at home) without disturbing the working route or asking Hosted. At most once a minute. */
 private promoteLocal(){
  const origin=this.state.origin;
  if(this.disposed||!this.foreground||this.state.phase!=='ready'||!origin||this.promotion||Date.now()<this.promotionNotBefore)return;
  const currentEntry=this.entry({baseUrl:origin,class:'lan',insecure:origin.startsWith('http:'),quality:'reachable',generation:'1'},'typed');
  if(currentEntry.secure&&currentEntry.local)return;
  this.promotionNotBefore=Date.now()+60000;
  const known=[...(this.memory.current?.document.candidates??[]),...(this.memory.previous?.document.candidates??[])];
  const controller=new AbortController(),generation=this.serial;
  this.promotion=controller;
  const timer=setTimeout(()=>controller.abort(),5000);
  const promote=async()=>{
   const discovered=this.options.discover?await this.options.discover(controller.signal).catch(()=>[]):[];
   const better=(e:RaceEntry)=>e.secure&&(!currentEntry.secure||e.local);
   const candidates=orderEntries([...known.map(c=>this.entry(c,'listed')),...discoveredRoutes(discovered,this.pin).map(c=>this.entry(c,'nearby'))].filter(e=>better(e)&&this.allowed(e)));
   const seen=new Set<string>();
   for(const {candidate} of candidates){
    if(controller.signal.aborted||this.disposed||generation!==this.serial||this.state.origin!==origin)return;
    if(seen.has(candidate.baseUrl)||candidate.baseUrl===origin)continue;
    seen.add(candidate.baseUrl);if(seen.size>4)break;
    try{
     await (this.options.probe??probeRoute)(candidate.baseUrl,this.pin,{fetcher:this.options.fetcher,crypto:this.options.crypto??webRouteCrypto,signal:controller.signal});
     if(controller.signal.aborted||this.disposed||generation!==this.serial||this.state.origin!==origin)return;
     this.serial++;this.recent.add(candidate.baseUrl);while(this.recent.size>8)this.recent.delete(this.recent.values().next().value!);
     this.memory={...this.memory,lastGood:candidate.baseUrl};this.save();
     this.publish({origin:candidate.baseUrl,remote:undefined,message:''});this.factsAt=0;this.refreshFacts(candidate.baseUrl);return;
    }catch{ /* Keep using the verified working route. */ }
   }
  };
  void Promise.race([promote(),new Promise<void>(resolve=>controller.signal.addEventListener('abort',()=>resolve(),{once:true}))]).finally(()=>{clearTimeout(timer);controller.abort();if(this.promotion===controller)this.promotion=undefined;});
 }
 private scheduleRecovery(){
  if(this.retry)clearTimeout(this.retry);
  const delay=Math.min(this.foreground?300000:900000,5000*2**Math.min(8,this.attempts++))*(.9+Math.random()*.2);
  this.retry=setTimeout(()=>{this.retry=undefined;void this.resolve().catch(()=>{});},delay);
 }
 networkChanged(network:string){if(this.disposed||network===this.state.network)return;this.rejected.clear();this.invalidate(network);void this.resolve().catch(()=>{});}
 private invalidate(network=this.state.network){this.promotion?.abort();this.promotion=undefined;this.serial++;this.run?.controller.abort();this.run=undefined;if(this.retry)clearTimeout(this.retry);this.retry=undefined;this.publish({phase:'probing',errorCode:undefined,origin:null,network,message:'The network changed. Verifying the same server before reconnecting.'});}
 recover(signal?:AbortSignal):Promise<string>{if(!this.run)this.invalidate();return this.resolve(signal);}
 async resolve(signal?:AbortSignal):Promise<string>{
  if(this.disposed)throw new RouteError('cancelled','This server connection is closed.');if(signal?.aborted)throw new RouteError('cancelled','Connection request cancelled.');
  if(this.state.phase==='ready'&&this.state.origin)return this.state.origin;
  if(!this.run){const generation=++this.serial,controller=new AbortController();this.publish({phase:'probing',errorCode:undefined,origin:null,message:'Finding a verified connection to your server.'});
   const promise=this.race(controller.signal).then(async origin=>{if(this.disposed||generation!==this.serial)throw new RouteError('cancelled','A newer connection check replaced this one.');this.recent.add(origin);while(this.recent.size>8)this.recent.delete(this.recent.values().next().value!);this.memory={...this.memory,lastGood:origin};this.save();this.attempts=0;const confirmed=this.confirmedLocal===origin;this.confirmedLocal=undefined;this.factsAt=0;this.publish({phase:'ready',errorCode:undefined,origin,remote:undefined,message:''});this.refreshFacts(origin,confirmed?false:undefined);return origin;}).catch(error=>{if(!this.disposed&&generation===this.serial){const code=routeFailureCode(error),mismatch=code==='identity_mismatch',unconfirmed=certificateFailure(code);this.publish({phase:mismatch?'identity_mismatch':unconfirmed?'unconfirmed':'offline',errorCode:code,origin:null,message:mismatch?'An address answered with a different server identity. No credentials were sent.':failureMessage});this.scheduleRecovery();}throw error;}).finally(()=>{controller.abort();if(this.run?.controller===controller)this.run=undefined;});this.run={controller,promise};
  }
  if(!signal)return this.run.promise;
  return new Promise<string>((resolve,reject)=>{const abort=()=>reject(new RouteError('cancelled','Connection request cancelled.'));signal.addEventListener('abort',abort,{once:true});void this.run!.promise.then(resolve,reject).finally(()=>signal.removeEventListener('abort',abort));if(signal.aborted)abort();});
 }
 private race(signal:AbortSignal):Promise<string>{
  const now=Date.now(),queue:RaceEntry[]=[],seen=new Set<string>(),controllers=new Set<AbortController>();let active=0,activeLocal=0,lastStart=0,finished=false,freshStarted=false,freshDone=!this.options.fresh||Date.now()<this.freshNotBefore,discoveryDone=!this.options.discover,mismatch=false,certificateFailures=0,otherFailures=0,secureRequired=false;
  let freshAllowed:Set<string>|undefined,held:string|undefined;const independent=new Set(this.memory.paired),inflight=new Map<AbortController,RaceEntry>();
  let derivedAllowed=new Set<string>();
  const eligible=(baseUrl:string)=>!freshAllowed||freshAllowed.has(baseUrl)||independent.has(baseUrl)||derivedAllowed.has(baseUrl);
  const current=this.memory.current,eligiblePrevious=this.memory.previous?.document.candidates.filter(c=>!current||current.document.candidates.some(n=>n.baseUrl===c.baseUrl))??[];
  const listed=[...(current?.document.candidates??[]),...eligiblePrevious].map(c=>this.entry(c,'listed'));
  const derivedNow=this.derived([current,this.memory.previous]);derivedAllowed=new Set(derivedNow.map(c=>c.baseUrl));
  const initial=[...listed,...this.memory.paired.map(baseUrl=>this.entry({baseUrl,class:'lan',insecure:baseUrl.startsWith('http:'),quality:'probe_required',generation:'1'},'typed')),...derivedNow.map(c=>this.entry(c,'derived'))];
  const localAtStart=initial.some(e=>e.local&&this.allowed(e));
  return new Promise<string>((resolve,reject)=>{
   const failure=()=>new RouteError(mismatch?'identity_mismatch':secureRequired&&otherFailures===0?SECURE_CONNECTION_REQUIRED:certificateFailures>0&&otherFailures===0&&active===0&&queue.length===0?'certificate_unverified':'route_unavailable',secureRequired&&!mismatch&&otherFailures===0?'This server requires a secure connection.':failureMessage);
   let boundary:ReturnType<typeof setTimeout>|undefined,pacer:ReturnType<typeof setTimeout>|undefined;const deadline=setTimeout(()=>held?finish(held):finish(undefined,failure()),8500);
   const abort=()=>finish(undefined,new RouteError('cancelled','Connection check cancelled.'));
   const finish=(origin?:string,error?:Error)=>{if(finished)return;finished=true;clearTimeout(deadline);if(boundary)clearTimeout(boundary);if(pacer)clearTimeout(pacer);signal.removeEventListener('abort',abort);for(const c of controllers)c.abort();origin?resolve(origin):reject(error??new RouteError('route_unavailable',failureMessage));};
   const resort=()=>{const ordered=orderEntries(queue,this.memory.lastGood);queue.splice(0,queue.length,...ordered);};
   const add=(values:readonly RaceEntry[])=>{for(const e of values){const c=e.candidate;if(seen.size>=96)break;const key=c.baseUrl+'|'+c.generation;if(seen.has(c.baseUrl)||!this.allowed(e))continue;const rejected=this.rejected.get(key);if(rejected&&rejected.retryAt>Date.now()){mismatch=true;continue;}try{routeOrigin(c.baseUrl);}catch{continue;}seen.add(c.baseUrl);queue.push(e);}resort();pump();};
   const securePending=()=>queue.some(e=>e.secure)||[...inflight.values()].some(e=>e.secure);
   // "Secure connections: Required": no plain HTTP for this server, from now on.
   const requireSecure=()=>{secureRequired=true;this.learnPolicy({secureConnections:'required'});held=undefined;for(let i=queue.length-1;i>=0;i--)if(!queue[i]!.secure)queue.splice(i,1);for(const [probe,e] of inflight)if(!e.secure)probe.abort();};
   const fresh=()=>{if(finished||freshStarted||!this.options.fresh||Date.now()<this.freshNotBefore)return;freshStarted=true;this.freshNotBefore=Date.now()+300000;const c=new AbortController();controllers.add(c);void this.options.fresh(c.signal).then(doc=>{if(finished||signal.aborted)return;this.install(doc);freshAllowed=new Set(doc.document.candidates.map(v=>v.baseUrl));const derived=this.derived([doc]);derivedAllowed=new Set(derived.map(v=>v.baseUrl));for(let i=queue.length-1;i>=0;i--)if(!eligible(queue[i]!.candidate.baseUrl))queue.splice(i,1);for(const [probe,e] of inflight)if(!eligible(e.candidate.baseUrl))probe.abort();add([...doc.document.candidates.map(v=>this.entry(v,'listed')),...derived.map(v=>this.entry(v,'derived'))]);}).catch(error=>{if(finished||signal.aborted)return;const retry=(error as {retryAfterSeconds?:unknown})?.retryAfterSeconds;if(typeof retry==='number'&&Number.isFinite(retry)&&retry>0)this.freshNotBefore=Math.max(this.freshNotBefore,Date.now()+Math.min(86400,retry)*1000);}).finally(()=>{controllers.delete(c);freshDone=true;pump();});};
   const won=(e:RaceEntry)=>{
    // Never an insecure route over a working secure one: hold it while any secure attempt is pending.
    if(e.secure||!securePending()){finish(e.candidate.baseUrl);return;}
    held??=e.candidate.baseUrl;
   };
   const pump=()=>{
    if(finished||signal.aborted)return;
    while(active<MAX_ACTIVE&&queue.length){
     const sinceStart=Date.now()-lastStart;
     if(active>0&&sinceStart<ATTEMPT_DELAY_MS){if(!pacer)pacer=setTimeout(()=>{pacer=undefined;pump();},ATTEMPT_DELAY_MS-sinceStart);break;}
     const index=queue.findIndex(e=>e.local?activeLocal<MAX_ACTIVE_LOCAL:Date.now()-now>=LOCAL_HEAD_START_MS||!localAtStart);
     if(index<0){if(!pacer&&queue.some(e=>!e.local)&&Date.now()-now<LOCAL_HEAD_START_MS)pacer=setTimeout(()=>{pacer=undefined;pump();},Math.max(1,LOCAL_HEAD_START_MS-(Date.now()-now)));break;}
     const e=queue.splice(index,1)[0]!,candidate=e.candidate,controller=new AbortController();controllers.add(controller);inflight.set(controller,e);active++;if(e.local)activeLocal++;lastStart=Date.now();
     void (this.options.probe??probeRoute)(candidate.baseUrl,this.pin,{fetcher:this.options.fetcher,crypto:this.options.crypto??webRouteCrypto,signal:controller.signal}).then(async()=>{
      if(signal.aborted||controller.signal.aborted||!eligible(candidate.baseUrl))return;
      if(!e.secure&&insecureNeedsLocalConfirmation(candidate.baseUrl,e.source,this.connectionPolicy())){await this.confirmLocal(candidate.baseUrl,controller.signal);if(signal.aborted||controller.signal.aborted||finished)return;this.confirmedLocal=candidate.baseUrl;}
      this.rejected.delete(candidate.baseUrl+'|'+candidate.generation);won(e);
     }).catch(error=>{if(finished||signal.aborted||controller.signal.aborted)return;try{this.options.onProbeFailure?.(probeFailure(error));}catch{/* Diagnostics never change route admission or recovery. */}const code=routeFailureCode(error);if(code===SECURE_CONNECTION_REQUIRED){requireSecure();return;}if(certificateFailure(code))certificateFailures++;else otherFailures++;if(code==='identity_mismatch'){mismatch=true;const key=candidate.baseUrl+'|'+candidate.generation,attempts=Math.min(7,(this.rejected.get(key)?.attempts??0)+1);this.rejected.set(key,{attempts,retryAt:Date.now()+Math.min(300000,5000*2**(attempts-1))});while(this.rejected.size>64)this.rejected.delete(this.rejected.keys().next().value!);}lastStart=0;}).finally(()=>{active--;if(e.local)activeLocal--;controllers.delete(controller);inflight.delete(controller);pump();});
    }
    if(held&&!securePending()){finish(held);return;}
    if(!queue.length&&!active){if(!freshStarted)fresh();if(freshDone&&discoveryDone)finish(undefined,failure());}
   };
   signal.addEventListener('abort',abort,{once:true});if(signal.aborted){abort();return;}
   if(this.options.discover){const c=new AbortController();controllers.add(c);void this.options.discover(c.signal).then(records=>{if(finished)return;const candidates=discoveredRoutes(records,this.pin);for(const candidate of candidates)independent.add(candidate.baseUrl);this.publish({discovery:candidates.length?'available':'empty'});add(candidates.map(v=>this.entry(v,'nearby')));}).catch(e=>{if(!finished)this.publish({discovery:(e as {code?:string})?.code==='permission_denied'?'denied':'empty'});}).finally(()=>{controllers.delete(c);discoveryDone=true;pump();});}
   boundary=setTimeout(fresh,this.options.freshAfterMs??FRESH_AFTER_MS);add(initial);
  });
 }
 /** Logical origin is stable for all existing command/journal ownership. Only
  * transport changes. Mutations are NEVER automatically replayed after loss. */
 fetch:typeof fetch=async(input,init={})=>{
  if(typeof input!=='string')throw new RouteError('invalid_address','Only selected-server URLs may be routed.');
  const requested=new URL(input);if(requested.username||requested.password||requested.hash||!this.acceptsOrigin(requested.origin))throw new RouteError('invalid_address','Cross-server request rejected.');
  this.promoteLocal();
  const method=(init.method??'GET').toUpperCase(),read=method==='GET'||method==='HEAD';
  let sentGeneration:number|undefined;
  const perform=async()=>{
   let origin=await this.resolve(init.signal??undefined);
   // A network event can run while resolve's fulfilled promise is queued.
   // Never dispatch credentials using an origin that is no longer verified.
   if(this.state.phase!=='ready'||this.state.origin!==origin)origin=await this.resolve(init.signal??undefined);
   if(init.signal?.aborted||this.disposed||this.state.phase!=='ready'||this.state.origin!==origin)throw new RouteError('cancelled','Request cancelled or superseded.');
   sentGeneration=this.serial;
   return (this.options.fetcher??globalThis.fetch)(origin+requested.pathname+requested.search,{...init,redirect:'error',credentials:'omit',referrerPolicy:'no-referrer'});
  };
  // A gateway answering for the server (a 502/503/504 that isn't Portico's own JSON: the
  // server is down or restarting behind its proxy) is a lost route, not an answer. Probe
  // again so the shell can show one reconnecting state; reads retry once after recovery.
  try{const response=await perform();if(!gatewayFailure(response)||init.signal?.aborted||this.disposed||sentGeneration===undefined)return response;const recovery=sentGeneration===this.serial?this.recover(init.signal??undefined):this.resolve(init.signal??undefined);if(read){await recovery;return perform();}void recovery.catch(()=>{});return response;}catch(error){
   if(init.signal?.aborted||this.disposed||sentGeneration===undefined)throw error;
   // An old socket's delayed failure must not cancel the replacement race or
   // invalidate a newer proven transport. Read retry only; no mutation replay.
   const recovery=sentGeneration===this.serial?this.recover(init.signal??undefined):this.resolve(init.signal??undefined);
   if(read){await recovery;return perform();}void recovery.catch(()=>{});throw error;
  }
 };
 acceptsOrigin(origin:string):boolean{return origin===this.logicalOrigin||this.recent.has(origin)||origin===this.state.origin;}
 mediaURL(path:string):string{const url=new URL(path,this.logicalOrigin);if(!this.acceptsOrigin(url.origin)||url.username||url.password)throw new RouteError('invalid_address','Cross-server media capability rejected.');if(this.state.phase!=='ready'||!this.state.origin)return '';return this.state.origin+url.pathname+url.search+url.hash;}
 dispose(){this.promotion?.abort();this.promotion=undefined;this.disposed=true;this.serial++;this.run?.controller.abort();if(this.retry)clearTimeout(this.retry);this.listeners.clear();}
}
/** A proxy's answer for an unavailable upstream. Portico's own 502, 503 and 504 are plain
 * `application/json`; anything else with those statuses came from something in front of it: an
 * HTML or text error page, or a proxy's own JSON dialect (`application/problem+json`). */
export function gatewayFailure(response:Response):boolean{return (response.status===502||response.status===503||response.status===504)&&(response.headers.get('content-type')??'').split(';')[0]!.trim().toLowerCase()!=='application/json';}
// A bearer is bound in memory only after identity proof + durable credential
// storage. New APIs for the same selected family inherit that exact connection.
const bindings=new Map<string,RouteConnection>();
export function bindSessionRoute(token:string,connection:RouteConnection){if(!token)throw new Error('A native credential is required.');bindings.delete(token);bindings.set(token,connection);while(bindings.size>16)bindings.delete(bindings.keys().next().value!);}
export function sessionRoute(token:string):RouteConnection|undefined{return token?bindings.get(token):undefined;}
export function forgetSessionRoute(token:string){bindings.delete(token);}
