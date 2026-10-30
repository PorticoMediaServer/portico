import {routeBody as hostedRouteBody} from "../../contracts/src/hosted/routes.ts";
import type {RouteResponse as HostedRouteResponse} from "../../contracts/src/hosted/routes.ts";
import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {encodedRoutePath as hostedEncodedRoutePath} from "../../contracts/src/hosted/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
/** Portico Account members of user servers (Spec — Hosted at Scale).
 *
 * Hosted proves who a person is and keeps an index of their servers; each user server decides who
 * may use it. So:
 * - Signing in to a server with a Portico Account is a direct sign-in whose credential is a short
 *   Hosted identity assertion (`porticoSignIn`): the server answers exactly as for a password, with
 *   its own profiles, PINs and device approval. A non-member gets `access_refused`.
 * - A device learns about new or removed servers by asking Hosted "has my list changed since
 *   version N?" — only while the app is in the foreground, at most every few hours, answered from
 *   Hosted's memory (`ServerListWatcher`). "Refresh Servers" asks once, now.
 * - Every automatic Hosted call goes through the shared gate: exponential backoff with jitter, a
 *   Retry-After never undercut, so a recovering Hosted meets a trickle. Everything already set up
 *   keeps working while Hosted is down; nothing here signs anyone out. */
import {ApiError,retryAfterSecondsFrom} from './index.ts';
import {hostedGate} from './hosted-gate.ts';
import {parseDirectSignIn,type DirectSignIn,type ProfileAPI} from './profile-management.ts';
import {routeBase64,routeBytes,serverPin,webRouteCrypto,type RouteCrypto} from './route-identity.ts';
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {parseShortInvitationCode} from './short-invitation-code.ts';

/** A Hosted-signed document, verified by the server against its pinned root. */
export type IdentityAssertion=Readonly<{payload:string;signature:string;keyId:string;certificate?:unknown}>;
/** The Hosted API with the account's bearer (`HttpHostedApi`'s transport, or any equivalent). */
export interface HostedRequestApi{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}

const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max:number):v is string=>typeof v==='string'&&v.length>0&&v.length<=max;
function invalid():never{throw new CodedError('invalid_response',unreadableServerResponse);}

function parseAssertion(raw:unknown):IdentityAssertion{
 if(!obj(raw)||!text(raw.payload,8192)||!text(raw.signature,128)||!text(raw.keyId,128))invalid();
 return Object.freeze({payload:raw.payload,signature:raw.signature,keyId:raw.keyId,...(raw.certificate===undefined?{}:{certificate:raw.certificate})});
}

/** `POST /v1/servers/{id}/identity {challenge}` on Hosted: a five-minute assertion for one server,
 * carrying the challenge that server gave this installation. Ask for it just before using it.
 * `automatic` (a silent re-admission) waits while the gate is open; a person's sign-in always goes
 * through. Only ever for a server the person acts on (signs in to, accepts, transfers) or already
 * belongs to: an assertion is the account's consent to be listed there. `purpose: 'custody'` asks
 * for a custody assertion instead: the owner accepting custody of the server's claim. */
export async function requestIdentityAssertion(hosted:HostedRequestApi,hostedOrigin:string,serverId:string,challenge:string,kind:'interactive'|'automatic'='interactive',signal?:AbortSignal,purpose?:'custody'):Promise<IdentityAssertion>{
 if(!text(serverId,128))throw new CodedError('not_portico_server','This server is not a Portico server.');
 if(!text(challenge,128))invalid();
 return hostedGate(hostedOrigin).run(kind,async()=>parseAssertion(await hosted.request<HostedRouteResponse<"server_identity_issue">>(hostedEncodedRoutePath("server_identity_issue", {"id": encodeURIComponent(serverId)}),'POST',purpose?{challenge,purpose}:{challenge},signal)));
}

/** The server's answer does not prove it is the server this device meant to reach. */
export const challengeNotProven='This server could not prove it is the server you chose. Nothing was sent to it.';

/** `POST /v1/direct/portico-challenge {nonce}` on the server: a single-use challenge for this
 * installation, with a proof signed by the server's identity key over the challenge and this
 * device's fresh nonce. The proof is checked against `serverId` (a server id is its key's own
 * digest) before anything is asked of Hosted, so a server or relay without that key cannot collect
 * an assertion for it (INT M7). */
export async function requestPorticoChallenge(server:ProfileAPI,serverId:string,signal?:AbortSignal,crypto:RouteCrypto=webRouteCrypto):Promise<string>{
 const nonce=routeBase64(crypto.random(32));
 const raw=await server.request<RouteResponse<"post_direct_portico_challenge">>(routePath("post_direct_portico_challenge"),'POST',routeBody("post_direct_portico_challenge", {nonce}),signal);
 if(!obj(raw)||!text(raw.challenge,128)||!obj(raw.proof)||!text(raw.proof.payload,8192)||!text(raw.proof.signature,128))invalid();
 let v:unknown;
 try{v=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(routeBytes(raw.proof.payload,undefined,6144)));}catch{throw new CodedError('challenge_not_proven',challengeNotProven);}
 if(!obj(v)||v.kind!=='portico.challenge-proof'||v.version!=='1'||v.serverId!==serverId||v.challenge!==raw.challenge||v.nonce!==nonce||typeof v.publicKey!=='string'||typeof v.fingerprint!=='string')throw new CodedError('challenge_not_proven',challengeNotProven);
 let pin;
 try{pin=await serverPin(serverId,v.publicKey,v.fingerprint,crypto);}catch{throw new CodedError('challenge_not_proven',challengeNotProven);}
 let signature:Uint8Array;
 try{signature=routeBytes(raw.proof.signature,64);}catch{throw new CodedError('challenge_not_proven',challengeNotProven);}
 if(!await crypto.verify(routeBytes(pin.publicKey,32),signature,routeBytes(raw.proof.payload,undefined,6144)))throw new CodedError('challenge_not_proven',challengeNotProven);
 return raw.challenge;
}

async function assertionFor(hosted:HostedRequestApi,hostedOrigin:string,server:ProfileAPI,serverId:string,kind:'interactive'|'automatic',signal?:AbortSignal,purpose?:'custody',crypto?:RouteCrypto):Promise<IdentityAssertion>{
 const challenge=await requestPorticoChallenge(server,serverId,signal,crypto);
 return requestIdentityAssertion(hosted,hostedOrigin,serverId,challenge,kind,signal,purpose);
}

/** The answer must come from the server the assertion was for. */
function sameServer(result:DirectSignIn,serverId:string):DirectSignIn{
 if(result.kind==='signed-in'&&result.snapshot.serverId!==serverId)throw new CodedError('server_mismatch','The server answered as a different server.');
 return result;
}

/** Signs a Portico Account in to a server: a challenge from the server, an identity assertion from
 * Hosted for it, then `POST /v1/direct/sign-in {porticoIdentity}` on the server. The answer is a
 * `DirectSignIn` (profile chooser, PIN, device approval, or the account's own second factor on
 * this server) exactly as for a password. A proven identity that is not a member throws
 * `ApiError(403,'access_refused')`; a server clock far off Hosted's, `ApiError(401,'clock_skew')`. */
export async function porticoSignIn(hosted:HostedRequestApi,hostedOrigin:string,server:ProfileAPI,serverId:string,options:{kind?:'interactive'|'automatic';signal?:AbortSignal;crypto?:RouteCrypto}={}):Promise<DirectSignIn>{
 const porticoIdentity=await assertionFor(hosted,hostedOrigin,server,serverId,options.kind??'interactive',options.signal,undefined,options.crypto);
 return sameServer(parseDirectSignIn(await server.request<RouteResponse<"post_direct_sign_in">>(routePath("post_direct_sign_in"),'POST',{porticoIdentity},options.signal)),serverId);
}

/** Accepts a server invitation as a Portico Account: `POST /v1/access/invitations/accept
 * {code, porticoIdentity}` on the inviting server, which creates the membership and answers with
 * the sign-in. The server tells Hosted; this device already has the server. */
export async function acceptPorticoInvitation(hosted:HostedRequestApi,hostedOrigin:string,server:ProfileAPI,serverId:string,code:string,signal?:AbortSignal,crypto?:RouteCrypto):Promise<DirectSignIn>{
 const trimmed=code.trim();if(trimmed.length<32||trimmed.length>128)throw new CodedError('invitation_incomplete','This invitation code is not complete.');
 const porticoIdentity=await assertionFor(hosted,hostedOrigin,server,serverId,'interactive',signal,undefined,crypto);
 return sameServer(parseDirectSignIn(await server.request<RouteResponse<"post_access_invitations_accept">>(routePath("post_access_invitations_accept"),'POST',{code:trimmed,porticoIdentity},signal)),serverId);
}

/** Accepts only the distinct human-entry capability, on an already verified
 * inviting server. It is never resolved by Hosted or interpreted as a link. */
export async function acceptShortPorticoInvitation(hosted:HostedRequestApi,hostedOrigin:string,server:ProfileAPI,serverId:string,raw:string,signal?:AbortSignal,crypto?:RouteCrypto):Promise<DirectSignIn>{
 const shortCode=parseShortInvitationCode(raw);
 if(!shortCode)throw new CodedError('invitation_incomplete','This invitation code is not complete.');
 const porticoIdentity=await assertionFor(hosted,hostedOrigin,server,serverId,'interactive',signal,undefined,crypto);
 return sameServer(parseDirectSignIn(await server.request<RouteResponse<"post_access_invitations_accept">>(routePath("post_access_invitations_accept"),'POST',{shortCode,porticoIdentity},signal)),serverId);
}

/** A Portico Account owner (it has no password on the server) transfers ownership:
 * `POST /v1/direct/ownership {accountId, expectedRevision, porticoIdentity}` with this device's
 * account session. The server tells Hosted; custody of the claim moves once the new owner accepts
 * it (`acceptPorticoCustody`, prompted by `custodyPending` in its snapshot). */
export async function transferPorticoOwnership(hosted:HostedRequestApi,hostedOrigin:string,server:ProfileAPI,serverId:string,accountId:string,expectedRevision:number,signal?:AbortSignal,crypto?:RouteCrypto):Promise<void>{
 const porticoIdentity=await assertionFor(hosted,hostedOrigin,server,serverId,'interactive',signal,undefined,crypto);
 await server.request(routePath("post_direct_ownership"),'POST',{accountId,expectedRevision,porticoIdentity},signal);
}

/** A public identity is one server family/account token: ASCII only, 1..128.
 * The characters are checked one by one, because a regular expression anchor
 * can match before a final newline. */
function publicId(value: unknown): value is string {
 if (typeof value !== 'string' || value.length < 1 || value.length > 128) return false;
 for (let index = 0; index < value.length; index++) {
  const code = value.charCodeAt(index);
  if (!(code >= 0x30 && code <= 0x39 || code >= 0x41 && code <= 0x5a || code >= 0x61 && code <= 0x7a || code === 0x5f || code === 0x2d)) return false;
 }
 return true;
}
/** Unpaired surrogates would be rewritten by UTF-8 encoding, so a password
 * carrying one is refused instead of being sent as different bytes. */
function wellFormed(value: string): boolean {
 for (let index = 0; index < value.length; index++) {
  const unit = value.charCodeAt(index);
  if (unit >= 0xd800 && unit <= 0xdbff) {
   const low = value.charCodeAt(++index);
   if (!(low >= 0xdc00 && low <= 0xdfff)) return false;
  } else if (unit >= 0xdc00 && unit <= 0xdfff) return false;
 }
 return true;
}
/** The local owner hands ownership to another member of this server:
 * `POST /v1/direct/ownership {accountId, currentPassword, expectedRevision}` and nothing else.
 * The caller's own revision is required, and the current password is re-proved by the server.
 * This helper never signs in, retiers, signs anyone out or falls back to a Hosted assertion. */
export async function transferLocalOwnership(server:ProfileAPI,accountId:string,currentPassword:string,expectedRevision:number,signal?:AbortSignal):Promise<void>{
 if(!publicId(accountId))throw new Error('The target account is invalid.');
 if(typeof expectedRevision!=='number'||!Number.isSafeInteger(expectedRevision)||expectedRevision<1)throw new Error('The owner revision is invalid.');
 if(typeof currentPassword!=='string'||currentPassword.length===0||!wellFormed(currentPassword)||new TextEncoder().encode(currentPassword).length>72)throw new Error('The current password is invalid.');
 if(signal?.aborted)throw Object.assign(new Error('The ownership transfer was cancelled.'),{name:'AbortError'});
 const body:Readonly<{accountId:string;currentPassword:string;expectedRevision:number}>={accountId,currentPassword,expectedRevision};
 await server.request<unknown>(routePath("post_direct_ownership"),'POST',routeBody("post_direct_ownership",body),signal);
}

/** Root68: a new local account is created in the SAME accepting owner transfer.
 * Never issue an ordinary member-create followed by a second mutation. */
export type LocalOwnerCreate = Readonly<{username:string;name:string;password:string}>;
export async function transferLocalOwnershipToNewAccount(server:ProfileAPI,newAccount:LocalOwnerCreate,currentPassword:string,expectedRevision:number,signal?:AbortSignal):Promise<void>{
 const username=newAccount?.username, name=newAccount?.name, password=newAccount?.password;
 if(typeof username!=='string'||!wellFormed(username)||new TextEncoder().encode(username.trim()).length<3||new TextEncoder().encode(username.trim()).length>64||/[ \t\r\n@]/.test(username.trim()))throw new Error('The new local username is invalid.');
 if(typeof name!=='string'||!wellFormed(name)||!name.trim()||new TextEncoder().encode(name.trim()).length>120||/[\x00-\x1f\x7f]/.test(name.trim()))throw new Error('The new local display name is invalid.');
 if(typeof password!=='string'||!wellFormed(password)||[...password].length<8||new TextEncoder().encode(password).length>72)throw new Error('The new local password is invalid.');
 if(typeof expectedRevision!=='number'||!Number.isSafeInteger(expectedRevision)||expectedRevision<1)throw new Error('The owner revision is invalid.');
 if(typeof currentPassword!=='string'||!wellFormed(currentPassword)||!currentPassword||new TextEncoder().encode(currentPassword).length>72)throw new Error('The current password is invalid.');
 if(signal?.aborted)throw Object.assign(new Error('The ownership transfer was cancelled.'),{name:'AbortError'});
 const body={accountId:'',currentPassword,expectedRevision,newAccount:{username,name,password}};
 await server.request<unknown>(routePath("post_direct_ownership"),'POST',routeBody("post_direct_ownership",body),signal);
}

/** A Portico Account owner accepts Hosted's custody of the server's claim (retiring it, its
 * certificates): `POST /v1/direct/ownership/custody {porticoIdentity}` with this device's account
 * session and a custody assertion. Ask the owner first: this is their consent. Needed when the
 * snapshot says `custodyPending` (after ownership was transferred to them). */
export async function acceptPorticoCustody(hosted:HostedRequestApi,hostedOrigin:string,server:ProfileAPI,serverId:string,signal?:AbortSignal,crypto?:RouteCrypto):Promise<void>{
 const porticoIdentity=await assertionFor(hosted,hostedOrigin,server,serverId,'interactive',signal,'custody',crypto);
 await server.request(routePath("post_direct_ownership_custody"),'POST',{porticoIdentity},signal);
}

/** Removes a server from this account's list at Hosted (`POST /v1/servers/{id}/leave`). The server
 * ends the membership at its next check-in; the owner cannot leave (409). */
export async function leavePorticoServer(hosted:HostedRequestApi,hostedOrigin:string,serverId:string,signal?:AbortSignal):Promise<void>{
 if(!text(serverId,128))throw new CodedError('not_portico_server','This server is not a Portico server.');
 await hostedGate(hostedOrigin).run('interactive',()=>hosted.request(hostedEncodedRoutePath("server_leave", {"id": encodeURIComponent(serverId)}),'POST',hostedRouteBody("server_leave", {}),signal));
}

/** The exact claim of an owned server, as the account's own server list showed it. */
export type ServerRemovalClaim = Readonly<{claimGeneration: string; credentialGeneration: string}>;
const generation = (value: unknown): value is string => typeof value === 'string' && /^[1-9][0-9]{0,18}$/.test(value) && (value.length < 19 || value <= '9223372036854775807');
/** The claim tuple of an owned list row, or undefined when it is absent or not a positive decimal.
 * Never defaulted: a guess could retire a re-claimed successor. */
export function serverRemovalClaim(row: object): ServerRemovalClaim | undefined {
 const {claimGeneration, credentialGeneration} = row as {claimGeneration?: unknown; credentialGeneration?: unknown};
 return generation(claimGeneration) && generation(credentialGeneration) ? Object.freeze({claimGeneration, credentialGeneration}) : undefined;
}
/** The owner retires an owned server at Hosted (`DELETE /v1/servers/{server}`): it leaves the
 * owner's list and every member's. Hosted retires it only while it still holds exactly the claim
 * the owner saw; a server re-claimed since is refused, never retired. `operationId` is fresh per
 * attempt; a retry of the same attempt passes the same one. A member uses leavePorticoServer. */
export async function removePorticoServer(hosted:HostedRequestApi,hostedOrigin:string,serverId:string,claim:ServerRemovalClaim,operationId:string,signal?:AbortSignal):Promise<void>{
 if(!text(serverId,128))throw new CodedError('not_portico_server','This server is not a Portico server.');
 if(!serverRemovalClaim(claim))throw new CodedError('server_claim_unknown','This server’s claim could not be confirmed. Refresh and try again.');
 if(typeof operationId!=='string'||operationId.length<16||operationId.length>128||/[\r\n\0]/.test(operationId))throw new CodedError('invalid_operation','The removal could not be started.');
 await hostedGate(hostedOrigin).run('interactive',()=>hosted.request(hostedEncodedRoutePath("server_remove", {"server": encodeURIComponent(serverId)}),'DELETE',hostedRouteBody("server_remove",{operationId,claimGeneration:claim.claimGeneration,credentialGeneration:claim.credentialGeneration}),signal));
}

/** The server no longer admits this account: drop the server or go to sign-in. */
export const isAccessRefused=(e:unknown):boolean=>e instanceof ApiError&&e.status===403&&e.code==='access_refused';
/** A session the server's move to its own membership ended (server migration 0120). Not a
 * sign-out: while the Portico Account session is valid, re-admit silently with `porticoSignIn`
 * (kind `automatic`) and keep the person where they were; only `access_refused`, or no
 * Portico Account session, shows the sign-in screen. */
export const isSessionMigrated=(e:unknown):boolean=>e instanceof ApiError&&e.status===401&&e.code==='session_migrated';
/** The server's clock is too far from Portico's for a Portico sign-in: tell the person to check it. */
export const isClockSkew=(e:unknown):boolean=>e instanceof ApiError&&e.status===401&&e.code==='clock_skew';

export type ServerListRecord=Readonly<{version:string;watch:string;checkedAt:number;nextCheckAt:number}>;
/** Where the watcher keeps its record between launches (per account). */
export interface ServerListStorage{load():ServerListRecord|null;save(record:ServerListRecord|null):void}
export type ServerListAnswer=Readonly<{items:readonly unknown[];version:string;watch:string}>;
export type ServerListOutcome='unchanged'|'changed'|'not-due'|'deferred';
type Clock={now:()=>number;random:()=>number};

/** At most one automatic check per this interval, on foreground only, spread by ±SPREAD so a
 * population of devices opened at the same hour does not check in step. */
export const SERVER_LIST_INTERVAL_MS=6*3600000,SERVER_LIST_SPREAD_MS=3600000;
/** A check or list request with no answer after this long is abandoned. Longer than the transports'
 * own deadlines, so it only acts where one has none (a browser's bare fetch) or does not answer. */
export const SERVER_LIST_TIMEOUT_MS=30000;

/** Keeps a device's server list current without polling. `adopt` records the version and watch
 * token of every `GET /v1/servers` answer; `foreground` checks when due; `refresh` is the
 * Settings "Refresh Servers" action. A changed list is fetched with `listServers` (the account's
 * `GET /v1/servers`) and handed to `onServers`. */
export class ServerListWatcher{
 private record:ServerListRecord|null;private flight:Promise<ServerListOutcome>|null=null;
 private readonly origin:string;private readonly fetcher:typeof fetch;private readonly storage?:ServerListStorage;
 private readonly listServers:(signal?:AbortSignal)=>Promise<ServerListAnswer>;private readonly onServers:(answer:ServerListAnswer)=>void;
 private readonly clock:Clock;private readonly timeoutMs:number;
 constructor(options:{hostedOrigin:string;listServers:(signal?:AbortSignal)=>Promise<ServerListAnswer>;onServers:(answer:ServerListAnswer)=>void;storage?:ServerListStorage;fetcher?:typeof fetch;clock?:Clock;timeoutMs?:number}){
  this.timeoutMs=options.timeoutMs??SERVER_LIST_TIMEOUT_MS;
  const fetcher=options.fetcher??((input,init)=>globalThis.fetch(input,init));
  this.origin=new URL(options.hostedOrigin).origin;this.fetcher=(input,init)=>fetcher(input,init);
  this.storage=options.storage;this.listServers=options.listServers;this.onServers=options.onServers;
  this.clock=options.clock??{now:()=>Date.now(),random:()=>Math.random()};
  let stored:ServerListRecord|null=null;try{stored=this.storage?.load()??null;}catch{}
  this.record=stored&&text(stored.version,32)&&text(stored.watch,400)?stored:null;
 }
 getRecord():ServerListRecord|null{return this.record;}
 /** Runs one request under the watcher's own deadline, so a stalled transport cannot hold the
  * shared check forever. React Native lacks AbortSignal.any/timeout: a plain controller carries
  * the caller's cancellation, and the race releases the check even if the transport ignores it. */
 private bounded<T>(caller:AbortSignal|undefined,request:(signal:AbortSignal)=>Promise<T>):Promise<T>{
  const bound=new AbortController(),abort=()=>bound.abort();let timer:ReturnType<typeof setTimeout>|undefined;
  caller?.addEventListener('abort',abort,{once:true});if(caller?.aborted)abort();
  // Rejected before aborting: the outcome is "no answer" (a failure the gate counts), not a cancellation.
  const deadline=new Promise<never>((_,reject)=>{timer=setTimeout(()=>{reject(new ApiError(504,'server_list_unavailable','Your server list could not be checked.',true));abort();},this.timeoutMs);});
  return Promise.race([(async()=>request(bound.signal))(),deadline]).finally(()=>{clearTimeout(timer);caller?.removeEventListener('abort',abort);});
 }
 private nextCheck(now:number){return now+SERVER_LIST_INTERVAL_MS+Math.floor((this.clock.random()*2-1)*SERVER_LIST_SPREAD_MS);}
 private save(record:ServerListRecord|null){this.record=record;try{this.storage?.save(record);}catch{}}
 /** Records a `GET /v1/servers` answer made anywhere in the app (sign-in, a chooser). */
 adopt(answer:ServerListAnswer){
  if(!text(answer.version,32)||!text(answer.watch,400))return;
  const now=this.clock.now();this.save(Object.freeze({version:answer.version,watch:answer.watch,checkedAt:now,nextCheckAt:this.nextCheck(now)}));
 }
 /** Forget the record (sign-out, account switch). */
 clear(){this.save(null);}
 /** The app came to the foreground. Checks only when due and while the gate is closed. */
 foreground(signal?:AbortSignal):Promise<ServerListOutcome>{
  const now=this.clock.now();
  if(this.record&&now<this.record.nextCheckAt)return Promise.resolve('not-due');
  if(hostedGate(this.origin).blockedFor()>0)return Promise.resolve('deferred');
  return this.check('automatic',signal);
 }
 /** Settings › Refresh Servers: one check now, then the list if it moved. */
 refresh(signal?:AbortSignal):Promise<ServerListOutcome>{return this.check('interactive',signal);}
 private check(kind:'interactive'|'automatic',signal?:AbortSignal):Promise<ServerListOutcome>{
  if(this.flight)return this.flight;
  const run=async():Promise<ServerListOutcome>=>{
   const gate=hostedGate(this.origin),record=this.record;
   if(!record)return this.fetchList(kind,signal);
   let moved:boolean;
   try{
    moved=await gate.run(kind,()=>this.bounded(signal,async bound=>{
     const response=await this.fetcher((this.origin) + hostedEncodedRoutePath("server_list_version", {"watch": encodeURIComponent(record.watch), "version": encodeURIComponent(record.version)}),{method:'GET',cache:'no-store',credentials:'omit',redirect:'error',referrerPolicy:'no-referrer',signal:bound});
     if(response.status===304)return false;
     if(response.status===200||response.status===404)return true; // 404: the watch token no longer verifies; the list answer brings a new one.
     throw new ApiError(response.status,'server_list_unavailable','Your server list could not be checked.',true,retryAfterSecondsFrom(response.headers.get('Retry-After')));
    }),'server-list-check');
   }catch(e){
    return this.defer(kind,e);
   }
   if(!moved){const now=this.clock.now();this.save(Object.freeze({...record,checkedAt:now,nextCheckAt:this.nextCheck(now)}));return 'unchanged';}
   return this.fetchList(kind,signal);
  };
  const flight=run().finally(()=>{if(this.flight===flight)this.flight=null;});
  this.flight=flight;return flight;
 }
 /** A failed check or fetch tries again at a foreground after the gate's deadline, never in a
  * loop; a person's refresh sees the error. */
 private defer(kind:'interactive'|'automatic',error:unknown):ServerListOutcome{
  const now=this.clock.now(),next=Math.max(now+hostedGate(this.origin).blockedFor(),now+60000);
  if(this.record)this.save(Object.freeze({...this.record,nextCheckAt:next}));
  if(kind==='interactive')throw error;
  return 'deferred';
 }
 private async fetchList(kind:'interactive'|'automatic',signal?:AbortSignal):Promise<ServerListOutcome>{
  let answer:ServerListAnswer;
  try{answer=await hostedGate(this.origin).run(kind,()=>this.bounded(signal,bound=>this.listServers(bound)),'server-list');}catch(e){return this.defer(kind,e);}
  if(!obj(answer)||!Array.isArray(answer.items))invalid();
  this.adopt(answer);this.onServers(answer);return 'changed';
 }
}
