import {routePath} from "../../contracts/src/route-paths.ts";
import {addressClass,formatIP,parseDirectName,parseIP,sameIP} from './network-locality.ts';
/** P09 connection evidence. None of these values grants a viewer or media lease. */
export type ServerPin = Readonly<{serverId:string;publicKey:string;fingerprint:string}>;
/** Verified route identity plus optional authorization of exact TLS leaf certificates. */
export type RouteProof = Readonly<ServerPin & {tlsCertificateFingerprints?:readonly string[]}>;
// A plain/cached pin or a deserialized proof must never stand for this connection's nonce
// exchange. Only probeRoute can register a result, after all cryptographic checks pass.
const proofObservations=new WeakMap<RouteProof,Readonly<{origin:string;receivedAt:number;tlsLeaf?:string}>>();
/** This exact object was proved fresh for this origin recently. Native additionally supplies
 * the TLS leaf observed by that SAME proof-only exchange, never a TXT or response-header claim. */
export function isCurrentRouteProof(origin:string,proof:RouteProof,now=Date.now()):boolean {
 const observation=proofObservations.get(proof);
 if(!observation||!Number.isFinite(now))return false;
 try{return observation.origin===routeOrigin(origin)&&now>=observation.receivedAt&&now-observation.receivedAt<30000;}catch{return false;}
}
/** Native transport metadata for the exact Response probeRoute consumed. Never populated
 * from HTTP headers, Bonjour TXT, a remembered pin, or a separate certificate observation. */
export function observedRouteTLSLeaf(origin:string,proof:RouteProof,now=Date.now()):string|undefined {
 return isCurrentRouteProof(origin,proof,now)?proofObservations.get(proof)?.tlsLeaf:undefined;
}
export type SignedRoutes = Readonly<{payload:string;signature:string}>;
export type RouteKey = Readonly<{keyId:string;publicKey:string}>;
/** A signed route (P2-67 Gate 2). Automatic `lan`/`public` routes are HTTPS IP names carrying their
 * literal `address` and effective `port`; `manual` routes are the owner's addresses as entered, and
 * `insecure` is exactly "this is plain HTTP". */
export type RouteCandidate = Readonly<{baseUrl:string;class:'lan'|'public'|'manual';insecure:boolean;address?:string;port?:number;quality:'reachable'|'probe_required';generation:string}>;
export const MAX_ROUTE_CANDIDATES=80;
export type RouteDocument = Readonly<ServerPin & {kind:'portico.routes';version:'1';audience:string;name:string;policyRevision:number;claimGeneration:string;credentialGeneration:string;generation:string;candidates:readonly RouteCandidate[];issuedAt:string;expiresAt:string;algorithm:'Ed25519';keyId:string}>;
export type VerifiedRoutes = Readonly<{envelope:SignedRoutes;document:RouteDocument}>;
export interface RouteCrypto {
 random(length:number):Uint8Array;
 sha256(value:Uint8Array):Promise<Uint8Array>;
 verify(publicKey:Uint8Array,signature:Uint8Array,payload:Uint8Array):Promise<boolean>;
}
export class RouteError extends Error {
 readonly code:string;
 constructor(code:string,message:string){super(message);this.name='RouteError';this.code=code;}
}
const alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';
export function routeBase64(bytes:Uint8Array):string {
 let out='',buffer=0,bits=0;for(const byte of bytes){buffer=(buffer<<8)|byte;bits+=8;while(bits>=6){bits-=6;out+=alphabet[(buffer>>>bits)&63];}}if(bits)out+=alphabet[(buffer<<(6-bits))&63];return out;
}
export function routeBytes(value:unknown,length?:number,max=65536):Uint8Array {
 if(typeof value!=='string'||!value.length||value.length>max*4/3+4||!/^[A-Za-z0-9_-]+$/.test(value)||value.length%4===1)throw new RouteError('invalid_identity','Invalid identity encoding.');
 const out=new Uint8Array(Math.floor(value.length*6/8));let bits=0,buffer=0,index=0;
 for(const c of value){buffer=(buffer<<6)|alphabet.indexOf(c);bits+=6;if(bits>=8){bits-=8;out[index++]=(buffer>>>bits)&255;}}
 if((length!==undefined&&out.length!==length)||out.length>max||routeBase64(out)!==value)throw new RouteError('invalid_identity','Invalid identity encoding.');return out;
}
const identityAlphabet='ABCDEFGHJKMNPQRSTUVWXYZ23456789';
/** A readable comparison of a server identity, never an authentication credential.
 * Callers must not display, log or transmit the expected value in a code challenge. */
export function identityCode(serverId:string):string {
 if(typeof serverId!=='string'||!/^srv_[A-Za-z0-9_-]{43}$/.test(serverId))throw new RouteError('invalid_identity','Invalid server identity.');
 const digest=routeBytes(serverId.slice(4),32);
 let n=0n;for(const byte of digest.subarray(0,8))n=(n<<8n)|BigInt(byte);
 n%=31n**12n;
 let code='';for(let i=0;i<12;i++){code=identityAlphabet[Number(n%31n)]+code;n/=31n;}
 return code.slice(0,4)+'-'+code.slice(4,8)+'-'+code.slice(8);
}
/** Only a complete code is comparable; invalid symbols are never silently removed. */
export function normaliseIdentityCode(input:string):string|undefined {
 if(typeof input!=='string'||input.length>128||/[^A-Za-z2-9\s-]/.test(input))return;
 const code=input.toUpperCase().replace(/[\s-]/g,'');
 if(code.length!==12||[...code].some(c=>!identityAlphabet.includes(c)))return;
 return code.slice(0,4)+'-'+code.slice(4,8)+'-'+code.slice(8);
}
/** Public identity and route ownership captured from the device's protected session record. */
export type KnownServerRoute=Readonly<{signIn:'local'|'portico';pin:ServerPin;origins:readonly string[]}>;
/** An address remains owned by every saved Local identity that remembers it. A link or a
 * freshly typed address cannot replace that ownership; only an explicit Forget removes it. */
export function knownRouteOwners(origin:string,known:readonly KnownServerRoute[]):readonly KnownServerRoute[] {
 const checked=routeOrigin(origin);
 return known.filter(value=>value.signIn==='local'&&value.origins.some(raw=>routeOrigin(raw)===checked));
}
export class FirstContactIdentityError extends RouteError {
 readonly savedServerId?:string;
 constructor(savedServerId?:string){super('identity_mismatch','The address did not prove the expected server identity. No credentials were sent.');this.savedServerId=savedServerId;}
}
/** Link expectation is checked first. A matching link still cannot take over a saved Local
 * server's address. The proof must already have passed probeRoute's cryptographic checks. */
export function assertFirstContactIdentity(origin:string,proof:ServerPin,context:Readonly<{linkId?:string;knownRoutes?:readonly KnownServerRoute[]}>={}):void {
 if(context.linkId!==undefined&&(!/^srv_[A-Za-z0-9_-]{43}$/.test(context.linkId)||proof.serverId!==context.linkId))throw new FirstContactIdentityError();
 const changed=knownRouteOwners(origin,context.knownRoutes??[]).find(value=>!sameServerPin(value.pin,proof));
 if(changed)throw new FirstContactIdentityError(changed.pin.serverId);
}
/** Route crypto for a page without `crypto.subtle` (plain HTTP on the LAN is not a secure
 * context). The web app registers an audited JavaScript SHA-256 and Ed25519 here at startup;
 * the checks themselves are the same, only the implementation differs. */
let routeCryptoFallback:Pick<RouteCrypto,'sha256'|'verify'>|undefined;
export function setRouteCryptoFallback(fallback:Pick<RouteCrypto,'sha256'|'verify'>):void{routeCryptoFallback=fallback;}
const subtle=()=>globalThis.crypto?.subtle;
const noRouteCrypto=()=>new RouteError('insecure_context','This browser can\'t verify the server here. Open Portico over HTTPS or update the browser.');
export const webRouteCrypto:RouteCrypto={
 random:n=>globalThis.crypto.getRandomValues(new Uint8Array(n)),
 sha256:async b=>{if(subtle())return new Uint8Array(await subtle()!.digest('SHA-256',new Uint8Array(b)));if(routeCryptoFallback)return routeCryptoFallback.sha256(b);throw noRouteCrypto();},
 verify:async(key,sig,raw)=>{if(!subtle()){if(routeCryptoFallback)return routeCryptoFallback.verify(key,sig,raw);throw noRouteCrypto();}try{const k=await globalThis.crypto.subtle.importKey('raw',new Uint8Array(key),'Ed25519',false,['verify']);return await globalThis.crypto.subtle.verify('Ed25519',k,new Uint8Array(sig),new Uint8Array(raw));}catch{return false;}},
};
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const generation=(v:unknown):v is string=>typeof v==='string'&&/^[1-9][0-9]{0,18}$/.test(v)&&BigInt(v)<=9223372036854775807n;
/** A loopback, private or link-local IP literal, `localhost`, or a `.local` name (one definition: network-locality.ts). */
export function privateRouteHost(host:string):boolean {
 const h=host.replace(/^\[|\]$/g,'').toLowerCase();if(h==='localhost'||h.endsWith('.local'))return true;
 const c=addressClass(h);return !!parseIP(h)&&(c==='lan'||c==='loopback');
}
const candidateKeys=['baseUrl','class','insecure','address','port','quality','generation'];
const invalidCandidate=()=>new RouteError('invalid_routes','Invalid signed route candidate.');
function routeCandidate(c:unknown,documentGeneration:string,suffixes:Set<string>):RouteCandidate {
 if(!object(c)||Object.keys(c).some(k=>!candidateKeys.includes(k))||!['lan','public','manual'].includes(String(c.class))||typeof c.insecure!=='boolean'||typeof c.baseUrl!=='string'||routeOrigin(c.baseUrl)!==c.baseUrl)throw invalidCandidate();
 const quality=c.quality===undefined?'probe_required':c.quality,gen=c.generation===undefined?documentGeneration:c.generation;
 if(!['reachable','probe_required'].includes(String(quality))||!generation(gen)||BigInt(gen)>BigInt(documentGeneration))throw invalidCandidate();
 const url=new URL(c.baseUrl),insecure=url.protocol==='http:';
 if(c.insecure!==insecure||(insecure&&c.class!=='manual'))throw invalidCandidate();
 const cls=c.class as RouteCandidate['class'],base={baseUrl:c.baseUrl,class:cls,insecure,quality:quality as RouteCandidate['quality'],generation:gen};
 if(cls==='manual'){if(c.address!==undefined||c.port!==undefined)throw invalidCandidate();return Object.freeze(base);}
 // Automatic: the IP name must decode to exactly the signed literal, on the URL's own port.
 const name=parseDirectName(url.hostname),address=typeof c.address==='string'?parseIP(c.address):undefined,port=Number(url.port||443);
 if(!name||!address||!sameIP(name.address,address)||formatIP(address)!==c.address||c.port!==port)throw invalidCandidate();
 const kind=addressClass(c.address);
 if(cls==='lan'?(kind==='public'||kind==='loopback'||isLinkLocal(c.address)):kind!=='public')throw invalidCandidate();
 suffixes.add(name.suffix);
 return Object.freeze({...base,address:c.address,port});
}
const isLinkLocal=(address:string)=>{const ip=parseIP(address);return !!ip&&(ip.family===4?ip.bytes[0]===169&&ip.bytes[1]===254:ip.bytes[0]===0xfe&&(ip.bytes[1]!&0xc0)===0x80);};
/** Origin only. No userinfo, path trick, query, IDNA ambiguity, or ambiguous host spellings. */
export function routeOrigin(raw:string):string {
 if(typeof raw!=='string'||raw.length>300||/[\\\s\x00-\x1f\x7f]/.test(raw)||!/^https?:\/\//i.test(raw))throw new RouteError('invalid_address','Enter an HTTP or HTTPS server address.');
 const u=new URL(raw);if(u.username||u.password||u.search||u.hash||u.pathname!=='/'||!u.hostname||u.hostname.endsWith('.')||u.hostname.includes('%')||!/^https?:$/.test(u.protocol))throw new RouteError('invalid_address','Enter a server address with no path.');
 // URL accepts legacy decimal/octal/hex IPv4. Do not silently reinterpret it.
 const authority=raw.slice(raw.indexOf('://')+3).replace(/\/$/,'');const host=authority.startsWith('[')?authority.slice(0,authority.indexOf(']')+1):authority.split(':')[0];
 if(host.toLowerCase()!==u.hostname)throw new RouteError('invalid_address','Use a canonical server address.');return u.origin;
}
export async function serverPin(serverId:string,publicKey:string,fingerprint:string|undefined,crypto:RouteCrypto=webRouteCrypto):Promise<ServerPin> {
 const raw=routeBytes(publicKey,32);const digest=routeBase64(await crypto.sha256(raw));
 const context=new TextEncoder().encode('portico.server.identity.v1\0');const bytes=new Uint8Array(context.length+raw.length);bytes.set(context);bytes.set(raw,context.length);
 if(serverId!=='srv_'+routeBase64(await crypto.sha256(bytes))||(fingerprint!==undefined&&fingerprint!==digest))throw new RouteError('identity_mismatch','This address does not match the selected server identity.');
 return Object.freeze({serverId,publicKey,fingerprint:digest});
}
export function sameServerPin(a:ServerPin,b:ServerPin):boolean{return a.serverId===b.serverId&&a.publicKey===b.publicKey&&a.fingerprint===b.fingerprint;}
function envelope(value:unknown,keyId?:string):SignedRoutes {
 // Hosted may attach the signing key's certificate from its offline root (784d3de5); the key itself
 // is checked against the account service's current key, so the certificate is accepted and not kept.
 if(!object(value)||Object.keys(value).some(k=>!['payload','signature',...(keyId===undefined?[]:['keyId','certificate'])].includes(k))||(value.certificate!==undefined&&!object(value.certificate))||(value.keyId!==undefined&&value.keyId!==keyId)||typeof value.payload!=='string'||typeof value.signature!=='string')throw new RouteError('invalid_identity','Invalid signed connection evidence.');
 routeBytes(value.payload,undefined,32768);routeBytes(value.signature,64);return Object.freeze({payload:value.payload,signature:value.signature});
}
function parseSignedJSON(value:SignedRoutes):Record<string,unknown>{try{const v:unknown=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(routeBytes(value.payload,undefined,32768)));if(object(v))return v;}catch{}throw new RouteError('invalid_identity','Invalid signed connection evidence.');}
function times(v:Record<string,unknown>,maxAge:number,now:number,allowExpired:boolean):void {
 const issued=typeof v.issuedAt==='string'?Date.parse(v.issuedAt):NaN,expires=typeof v.expiresAt==='string'?Date.parse(v.expiresAt):NaN;
 if(!Number.isFinite(issued)||!Number.isFinite(expires)||issued>now+120000||expires<=issued||expires-issued>maxAge||(!allowExpired&&expires<=now))throw new RouteError('route_stale','The connection evidence is expired or has an invalid clock.');
}
/**
 * The time check for a proof the server signed over this client's own nonce. The nonce already
 * proves the answer is fresh, so a failure here can only mean the two clocks disagree: this
 * device's is allowed to be up to two minutes off in either direction (the same allowance the
 * issue time always had), and beyond that the failure says so plainly instead of reading as a
 * wrong server. Call only after the signature has been verified.
 */
const PROOF_CLOCK_ALLOWANCE=120000;
function proofTimes(v:Record<string,unknown>,maxAge:number,now:number):void {
 const issued=typeof v.issuedAt==='string'?Date.parse(v.issuedAt):NaN,expires=typeof v.expiresAt==='string'?Date.parse(v.expiresAt):NaN;
 if(!Number.isFinite(issued)||!Number.isFinite(expires)||expires<=issued||expires-issued>maxAge)throw new RouteError('route_stale','The connection evidence is expired or has an invalid clock.');
 if(issued>now+PROOF_CLOCK_ALLOWANCE||expires+PROOF_CLOCK_ALLOWANCE<=now)throw new RouteError('clocks_disagree','This device and the server do not agree on the time. No credentials were sent.');
}
export async function verifyRoutes(value:unknown,key:RouteKey,expected:{accountId:string;serverId:string;pin?:ServerPin;allowExpiredHint?:boolean;now?:number},crypto:RouteCrypto=webRouteCrypto):Promise<VerifiedRoutes> {
 // A rotated transport key ID must take the same bounded fresh-key path as a payload/signature mismatch.
 if(object(value)&&typeof value.keyId==='string'&&value.keyId!==key.keyId)throw new RouteError('invalid_signature','The account service signing key changed.');
 const signed=envelope(value,key.keyId),v=parseSignedJSON(signed);
 if(!text(key.keyId,256)||v.keyId!==key.keyId||!await crypto.verify(routeBytes(key.publicKey,32),routeBytes(signed.signature,64),routeBytes(signed.payload)))throw new RouteError('invalid_signature','The account service did not sign these server routes.');
 if(v.kind!=='portico.routes'||v.version!=='1'||v.algorithm!=='Ed25519'||v.audience!==expected.accountId||v.serverId!==expected.serverId||!text(v.name,2048)||typeof v.publicKey!=='string'||typeof v.fingerprint!=='string'||!generation(v.generation)||!generation(v.claimGeneration)||!generation(v.credentialGeneration)||!Number.isSafeInteger(v.policyRevision)||Number(v.policyRevision)<0||!Array.isArray(v.candidates)||v.candidates.length>MAX_ROUTE_CANDIDATES)throw new RouteError('invalid_routes','The account service returned incompatible connection information.');
 const pin=await serverPin(v.serverId as string,v.publicKey,v.fingerprint,crypto);if(expected.pin&&!sameServerPin(pin,expected.pin))throw new RouteError('identity_mismatch','The selected server identity changed.');
 times(v,45*60000,expected.now??Date.now(),expected.allowExpiredHint===true);
 const suffixes=new Set<string>(),candidates=v.candidates.map(c=>routeCandidate(c,v.generation as string,suffixes));
 // One server, one namespace: every automatic name in a document shares its `ptc-<ns>.<domain>`.
 if(suffixes.size>1)throw new RouteError('invalid_routes','Signed route candidates name more than one server.');
 if(new Set(candidates.map(c=>c.baseUrl)).size!==candidates.length)throw new RouteError('invalid_routes','Duplicate signed route candidates.');
 return Object.freeze({envelope:signed,document:Object.freeze({...v,...pin,candidates:Object.freeze(candidates)}) as RouteDocument});
}
/** The server's "Secure connections: Required" answer to plain HTTP (HTTP 426). */
export const SECURE_CONNECTION_REQUIRED='secure_connection_required';
/** Bounded JSON reader with neutral, non-sensitive diagnostics (also works with RN Response). */
export async function routeJSON(response:Response,signal:AbortSignal,max=32768):Promise<unknown> {
 if(!response.ok){void response.body?.cancel().catch(()=>{});const e=new RouteError(response.status===401?'authentication_required':response.status===403?'access_denied':response.status===426?SECURE_CONNECTION_REQUIRED:response.status===429?'rate_limited':'route_unavailable',response.status===426?'This server requires a secure connection.':'The connection could not be verified.');throw e;}
 if(response.headers.get('Content-Type')?.split(';')[0].toLowerCase().trim()!=='application/json'||Number(response.headers.get('Content-Length')??0)>max)throw new RouteError('invalid_response','Invalid connection response.');
 // Native's bounded NSURLSession transport has already limited bytes. Fetch
 // streams are consumed incrementally so a hostile endpoint cannot allocate arbitrarily.
 if(!response.body?.getReader){const s=await response.text();if(signal.aborted||new TextEncoder().encode(s).length>max)throw new RouteError('invalid_response','Invalid connection response.');return JSON.parse(s);}
 const reader=response.body.getReader(),chunks:Uint8Array[]=[];let length=0;const abort=()=>{void reader.cancel().catch(()=>{});};signal.addEventListener('abort',abort,{once:true});
 try{for(;;){if(signal.aborted)throw new RouteError('cancelled','Connection check cancelled.');const next=await reader.read();if(next.done)break;length+=next.value.byteLength;if(length>max)throw new RouteError('invalid_response','Connection response exceeded its limit.');chunks.push(next.value);}if(signal.aborted)throw new RouteError('cancelled','Connection check cancelled.');const bytes=new Uint8Array(length);let n=0;for(const b of chunks){bytes.set(b,n);n+=b.length;}return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(bytes));}finally{signal.removeEventListener('abort',abort);void reader.cancel().catch(()=>{});try{reader.releaseLock();}catch{}}
}
export async function probeRoute(origin:string,pin:ServerPin|undefined,options:{fetcher?:typeof fetch;crypto?:RouteCrypto;signal?:AbortSignal;now?:()=>number;
 /** A trusted native bridge reads its actual TLS observation keyed by this exact proof
  * Response. Not an HTTP header. Absence cannot establish Bonjour-based home trust. */
 observedTLSLeaf?:(response:Response)=>string|undefined;
}={}):Promise<RouteProof> {
 const baseUrl=routeOrigin(origin),crypto=options.crypto??webRouteCrypto,nonce=routeBase64(crypto.random(32));
 const controller=new AbortController(),cancel=()=>controller.abort();options.signal?.addEventListener('abort',cancel,{once:true});const timer=setTimeout(cancel,3000);
 try{
  if(options.signal?.aborted)cancel();if(controller.signal.aborted)throw new RouteError('cancelled','Connection check cancelled.');
  const response=await (options.fetcher??globalThis.fetch)((baseUrl) + routePath("prove_network_identity"),{method:'POST',redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',signal:controller.signal,headers:{Accept:'application/json; portico-route-proof=2','Content-Type':'application/json'},body:JSON.stringify({baseUrl,nonce})});
  const tlsLeaf=options.observedTLSLeaf?.(response);if(tlsLeaf!==undefined)routeBytes(tlsLeaf,32);
  const signed=envelope(await routeJSON(response,controller.signal,8192)),v=parseSignedJSON(signed);
  if(Object.keys(v).some(k=>!['kind','version','baseUrl','nonce','serverId','publicKey','fingerprint','issuedAt','expiresAt',...(v.version==='2'?['tlsCertificateFingerprints']:[])].includes(k))||v.kind!=='portico.route-proof'||(v.version!=='1'&&v.version!=='2')||v.baseUrl!==baseUrl||v.nonce!==nonce||typeof v.serverId!=='string'||typeof v.publicKey!=='string'||typeof v.fingerprint!=='string')throw new RouteError('identity_mismatch','The server did not prove this connection.');
  let fingerprints:readonly string[]|undefined;
  if(v.tlsCertificateFingerprints!==undefined){
   const values=v.tlsCertificateFingerprints;
   if(!Array.isArray(values)||values.length<1||values.length>8||new Set(values).size!==values.length)throw new RouteError('invalid_identity','Invalid TLS certificate binding.');
   for(const value of values)routeBytes(value,32);
   fingerprints=Object.freeze([...values] as string[]);
  }
  const actual=await serverPin(v.serverId,v.publicKey,v.fingerprint,crypto);if(pin&&!sameServerPin(actual,pin))throw new RouteError('identity_mismatch','This address belongs to a different server. No credentials were sent.');
  if(!await crypto.verify(routeBytes(actual.publicKey,32),routeBytes(signed.signature,64),routeBytes(signed.payload)))throw new RouteError('identity_mismatch','The server could not prove its identity. No credentials were sent.');
  // After the signature: only a proof this server really signed over our nonce can blame the clocks.
  const receivedAt=options.now?.()??Date.now();proofTimes(v,30000,receivedAt);
  if(controller.signal.aborted)throw new RouteError('cancelled','Connection check cancelled.');
  const proof:RouteProof=fingerprints?Object.freeze({...actual,tlsCertificateFingerprints:fingerprints}):actual;
  proofObservations.set(proof,Object.freeze({origin:baseUrl,receivedAt,...(tlsLeaf===undefined?{}:{tlsLeaf})}));return proof;
 }finally{clearTimeout(timer);options.signal?.removeEventListener('abort',cancel);controller.abort();}
}
