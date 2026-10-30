import {throwIfAborted} from './abort-lifetime.ts';
import type {RouteResponse as HostedRouteResponse} from "../../contracts/src/hosted/routes.ts";
import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath as hostedRoutePath, encodedRoutePath as hostedEncodedRoutePath} from "../../contracts/src/hosted/routes.ts";
import {routePath} from "../../contracts/src/route-paths.ts";
import {hostedGate} from './hosted-gate.ts';
import {ApiError,HttpLocalApi,type HostedServer,type LocalSession} from './index.ts';
import {credentialLock,installationIdentity} from './installation.ts';
import {parseLocalSession} from './local-session.ts';
import {parseServerSignIn} from './sign-in.ts';
import {parseDirectSnapshot} from './profile-management.ts';
import {RouteConnection,bindSessionRoute,forgetSessionRoute,sessionRoute,sessionRoute as sessionRouteForRotation,type DiscoveryRoute,type RememberedNetworkPolicy,type RouteMemory,type RouteProbeFailure} from './route-connection.ts';
import type {ConnectionPolicy} from './connection-policy.ts';
import {RouteError,routeBase64,probeRoute,routeOrigin,routeJSON,serverPin,sameServerPin,verifyRoutes,webRouteCrypto,type RouteCrypto,type RouteKey,type ServerPin,type SignedRoutes,type VerifiedRoutes} from './route-identity.ts';
export type RememberedServer = {
 version:'1';selectionId?:string;logicalOrigin:string;session:LocalSession;pin:ServerPin;
 hosted?:{origin:string;accountId:string;key:RouteKey};
 routes:{current?:SignedRoutes;previous?:SignedRoutes;paired:string[];lastGood?:string;policy?:RememberedNetworkPolicy};
 /** A refresh sent but not yet stored: retried with this same request id, so the server replays its answer. */
 pendingRefresh?:string;
};
/** Implementations protect and atomically replace credentials + raw key + route
 * evidence together. A cache update cannot overwrite another selected family.
 *
 * A read can fail in two ways, and the store must say which. A record that is there but can never
 * be read again (not valid, missing its key, failing authentication) is reported with the code
 * `invalid_saved_connection`; `change` then treats it as absent, so a new sign-in replaces it.
 * Anything else (locked, busy, timed out) is thrown as it is and means "not now". */
export interface ServerConnectionStorage {
 read():Promise<RememberedServer|undefined>;
 /** A supplied guard runs under the storage lock immediately before mutation,
  * including after asynchronous encryption; a failed guard writes nothing. */
 change(update:(current:RememberedServer|undefined)=>RememberedServer|undefined,beforeCommit?:()=>void):Promise<void>;
}
export type ConnectionEnvironment={storage:ServerConnectionStorage;fetcher?:typeof fetch;crypto?:RouteCrypto;discover?:(signal:AbortSignal)=>Promise<readonly DiscoveryRoute[]>;onProbeFailure?:(failure:RouteProbeFailure)=>void;
 /** This device's insecure-connection setting and platform (connection-policy.ts). */
 policy?:()=>ConnectionPolicy;
 /** Reads `/v1/networking/connection` without credentials (route-connection.ts `readConnection`). */
 readConnection?:(origin:string,signal:AbortSignal)=>Promise<unknown>};
export type HostedConnectionSource={origin:string;accountId:string;request:<T>(path:string,signal:AbortSignal)=>Promise<T>};
export {parseRememberedServer,savedConnectionDamaged} from './remembered-server.ts';
import {parseRememberedServer,savedConnectionDamaged,hostedOrigin} from './remembered-server.ts';
const object=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
function memoryRecord(memory:RouteMemory):RememberedServer['routes']{return {current:memory.current?.envelope,previous:memory.previous?.envelope,paired:[...memory.paired],lastGood:memory.lastGood,...(memory.policy?{policy:memory.policy}:{})};}
async function routeKey(source:HostedConnectionSource,env:ConnectionEnvironment,signal:AbortSignal):Promise<RouteKey>{
 const origin=hostedOrigin(source.origin),response=await (env.fetcher??globalThis.fetch)((origin) + hostedRoutePath("keys_get"),{signal,redirect:'error',credentials:'omit',headers:{Accept:'application/json'}}),key=await routeJSON(response,signal,2048);
 if(!object(key)||typeof key.keyId!=='string'||typeof key.publicKey!=='string'||key.keyId.length>256||key.publicKey.length!==43)throw new RouteError('invalid_account_key','Account route verification is unavailable.');return {keyId:key.keyId,publicKey:key.publicKey};
}
function sameSelection(a:RememberedServer|undefined,b:RememberedServer):boolean {
 return !!a&&a.selectionId===b.selectionId&&a.pin.serverId===b.pin.serverId&&a.session.sessionFamilyId===b.session.sessionFamilyId&&
  (b.selectionId!==undefined||a.session.accessToken===b.session.accessToken);
}
function attachPersistence(connection:RouteConnection,env:ConnectionEnvironment,record:RememberedServer,hosted?:RememberedServer['hosted']){
 connection.setPersistence(memory=>env.storage.change(current=>{
  if(!sameSelection(current,record))return current;
  return {...current!,routes:memoryRecord(memory),...(hosted?{hosted}:{})};
 }));
}
async function currentDocument(source:HostedConnectionSource,env:ConnectionEnvironment,pin:ServerPin,key:RouteKey,signal:AbortSignal):Promise<{routes:VerifiedRoutes;key:RouteKey}>{
 // Asking for fresh routes is the app's own idea, never a tap: it waits for the shared circuit,
 // and connections to the same server share one request.
 const signed=await hostedGate(source.origin).run('automatic',()=>source.request<HostedRouteResponse<"server_routes_get">>(hostedEncodedRoutePath("server_routes_get", {"id": encodeURIComponent(pin.serverId)}),signal),'routes:'+pin.serverId);
 try{return {routes:await verifyRoutes(signed,key,{accountId:source.accountId,serverId:pin.serverId,pin},env.crypto),key};}catch(e){if(!(e instanceof RouteError)||e.code!=='invalid_signature')throw e;const freshKey=await routeKey(source,env,signal);return {routes:await verifyRoutes(signed,freshKey,{accountId:source.accountId,serverId:pin.serverId,pin},env.crypto),key:freshKey};}
}
export async function connectHostedServer(server:HostedServer,source:HostedConnectionSource,env:ConnectionEnvironment,signal:AbortSignal):Promise<{api:HttpLocalApi;connection:RouteConnection;hosted:NonNullable<RememberedServer['hosted']>}> {
 const key=await routeKey(source,env,signal),signed=server.routes??await source.request<HostedRouteResponse<"server_routes_get">>(hostedEncodedRoutePath("server_routes_get", {"id": encodeURIComponent(server.id)}),signal);
 const verified=await verifyRoutes(signed,key,{accountId:source.accountId,serverId:server.id},env.crypto);
 if(verified.document.publicKey!==server.publicKey||verified.document.policyRevision<server.policyRevision)throw new RouteError('identity_mismatch','The server directory and signed identity disagree.');
 const origin=verified.document.candidates.find(c=>c.baseUrl===server.baseUrl)?.baseUrl??verified.document.candidates[0]?.baseUrl;
 if(!origin)throw new RouteError('server_unavailable','This server has not published a usable address. Its owner can repair remote access from the local network.');
 const hosted={origin:hostedOrigin(source.origin),accountId:source.accountId,key};
 const connection=new RouteConnection({pin:verified.document,logicalOrigin:origin,memory:{current:verified,paired:[]},fetcher:env.fetcher,crypto:env.crypto,discover:env.discover,onProbeFailure:env.onProbeFailure,policy:env.policy,readConnection:env.readConnection,fresh:async signal=>{const next=await currentDocument(source,env,verified.document,hosted.key,signal);hosted.key=next.key;return next.routes;}});
 try{await connection.resolve(signal);const api=new HttpLocalApi(origin,'',env.fetcher);api.useRoutes(connection);return {api,connection,hosted};}catch(e){connection.dispose();throw e;}
}
/** Anonymous proof precedes credential entry. Native decides certificate trust first;
 * browsers use their system trust store. No explicit fingerprint confirmation is shown.
 * First-contact callers must also apply assertFirstContactIdentity to saved route owners. */
export async function inspectDirectServer(origin:string,env:ConnectionEnvironment,signal:AbortSignal):Promise<ServerPin>{return probeRoute(routeOrigin(origin),undefined,{fetcher:env.fetcher,crypto:env.crypto,signal});}
export async function connectPairedServer(origin:string,pin:ServerPin,env:ConnectionEnvironment,signal:AbortSignal):Promise<{api:HttpLocalApi;connection:RouteConnection}>{
 const checked=await serverPin(pin.serverId,pin.publicKey,pin.fingerprint,env.crypto);
 const connection=new RouteConnection({pin:checked,logicalOrigin:routeOrigin(origin),memory:{paired:[routeOrigin(origin)]},fetcher:env.fetcher,crypto:env.crypto,discover:env.discover,onProbeFailure:env.onProbeFailure,policy:env.policy,readConnection:env.readConnection});
 try{await connection.resolve(signal);const api=new HttpLocalApi(connection.logicalOrigin,'',env.fetcher);api.useRoutes(connection);return{api,connection};}catch(e){connection.dispose();throw e;}
}
/** A Waiting member may prove this server before it has a Hosted directory row.
 * Pairing proves the exact server; the authenticated account source supplies only
 * its route-key binding. This reads no member routes and saves no credential. */
export async function connectPairedPorticoServer(origin:string,pin:ServerPin,source:HostedConnectionSource,env:ConnectionEnvironment,signal:AbortSignal):Promise<{api:HttpLocalApi;connection:RouteConnection;hosted:NonNullable<RememberedServer['hosted']>}>{
 const accountOrigin=hostedOrigin(source.origin);
 if(!/^[A-Za-z0-9_-]{1,128}$/.test(source.accountId))throw new RouteError('identity_mismatch','Choose the Portico Account before signing in to this server.');
 throwIfAborted(signal);
 const paired=await connectPairedServer(origin,pin,env,signal);
 try{
  const system=parseServerSignIn(await paired.api.request(routePath('get_system'),'GET',undefined,signal));
  if(system.id!==pin.serverId||system.signIn!=='portico')throw new RouteError('identity_mismatch','This server does not use Portico Accounts.');
  const key=await routeKey(source,env,signal);throwIfAborted(signal);
  return {...paired,hosted:{origin:accountOrigin,accountId:source.accountId,key}};
 }catch(error){paired.connection.dispose();throw error;}
}
export async function rememberNativeSession(api:HttpLocalApi,session:LocalSession,env:ConnectionEnvironment,hosted?:RememberedServer['hosted'],isCurrent:()=>boolean=()=>true):Promise<void>{
 const parsed=parseLocalSession(session),connection=api.getRouteConnection();if(!connection||connection.getSnapshot().phase!=='ready'||connection.pin.serverId!==parsed.viewer.serverId)throw new RouteError('identity_mismatch','Verify this server before saving its sign-in.');
 // Local pairing alone cannot supply the offline raw-key credential pin. The
 // issuing server must return it with the native session, in its transaction.
 if(!hosted&&!parsed.serverIdentity)throw new RouteError('missing_server_key','This server must include its identity key in the sign-in response.');
 if(parsed.serverIdentity){const actual=await serverPin(parsed.viewer.serverId,parsed.serverIdentity.publicKey,parsed.serverIdentity.fingerprint,env.crypto);if(!sameServerPin(actual,connection.pin))throw new RouteError('identity_mismatch','The native session returned a different server identity.');}
 // A Portico Account member (Spec — Hosted at Scale) holds a server-local session whose account is the
 // server's own; the Portico Account only discovers routes for it. A legacy hosted viewer must match.
 if(hosted&&parsed.viewer.authority==='hosted'&&parsed.viewer.accountId!==hosted.accountId)throw new RouteError('identity_mismatch','The native session belongs to another account.');
 const record:RememberedServer={version:'1',selectionId:routeBase64((env.crypto??webRouteCrypto).random(24)),logicalOrigin:connection.logicalOrigin,session:parsed,pin:{serverId:connection.pin.serverId,publicKey:connection.pin.publicKey,fingerprint:connection.pin.fingerprint},routes:memoryRecord(connection.getMemory()),...(hosted?{hosted}: {})};
 let previous:RememberedServer|undefined;
 const beforeCommit=()=>{if(!isCurrent())throw new RouteError('cancelled','The selected account or server changed.');};
 await env.storage.change(current=>{beforeCommit();previous=current;return record;},beforeCommit);
 if(!isCurrent()){await env.storage.change(current=>sameSelection(current,record)?previous:current);throw new RouteError('cancelled','The selected account or server changed.');}
 // The storage acknowledgement itself may be delayed behind a newer selection.
 const saved=await env.storage.read();
 if(!isCurrent()||!sameSelection(saved,record)){if(!isCurrent())await env.storage.change(current=>sameSelection(current,record)?previous:current);throw new RouteError('cancelled','The selected account or server changed.');}
 attachPersistence(connection,env,record,hosted);bindSessionRoute(parsed.accessToken,connection);api.setAccessToken(parsed.accessToken);
}
/** Restore outcome: the server knows this sign-in but refuses it access (403, or 401 right after a
 * renewal). Not offline and not a reset: say so once, keep the credential, don't retry. */
export const ACCESS_REFUSED='access_refused';
export async function restoreNativeSession(env:ConnectionEnvironment,source?:(record:RememberedServer)=>HostedConnectionSource|undefined,expected?:LocalSession,signal?:AbortSignal):Promise<{api:HttpLocalApi;session:LocalSession}|undefined>{
 return restoreStoredSession(env,source,expected,false,signal);
}
/** Closed Security continuations may inspect only their exact original saved
 * family/token generation and viewer. They never renew, adopt a newer token, contact Hosted
 * for routes, or publish route persistence into another opening. */
export async function restoreExactNativeSession(env:ConnectionEnvironment,expected:LocalSession,signal?:AbortSignal):Promise<{api:HttpLocalApi;session:LocalSession}|undefined>{
 return restoreStoredSession(env,undefined,expected,true,signal);
}
function checkRestoreCancellation(signal:AbortSignal|undefined){if(signal?.aborted)throw new RouteError('cancelled','The session restore was cancelled.');}
async function restoreStoredSession(env:ConnectionEnvironment,source:((record:RememberedServer)=>HostedConnectionSource|undefined)|undefined,expected:LocalSession|undefined,exact:boolean,signal?:AbortSignal):Promise<{api:HttpLocalApi;session:LocalSession}|undefined>{
 const check=()=>checkRestoreCancellation(signal);check();
 try{
 const stored=await env.storage.read();check();
 const record=parseRememberedServer(stored);if(!record)return;
 const exactViewerMatches=(saved:RememberedServer|undefined)=>!!saved&&!!expected&&(['authority','accountId','profileId','serverId','role'] as const).every(field=>saved.session.viewer[field]===expected.viewer[field]);
 if(exact&&(!expected||!exactViewerMatches(record)||record.pendingRefresh||record.session.tokenGeneration!==expected.tokenGeneration||record.session.accessToken!==expected.accessToken||Date.parse(record.session.expiresAt)<=Date.now()))return;
 // The protected store may hold a newer rotation of the same family than the caller's copy.
 if(expected&&(record.session.sessionFamilyId!==expected.sessionFamilyId||BigInt(record.session.tokenGeneration)<BigInt(expected.tokenGeneration)||(record.session.tokenGeneration===expected.tokenGeneration&&record.session.accessToken!==expected.accessToken)||record.session.viewer.serverId!==expected.viewer.serverId||record.session.viewer.accountId!==expected.viewer.accountId||record.session.viewer.profileId!==expected.viewer.profileId))return;
 const pin=await serverPin(record.pin.serverId,record.pin.publicKey,record.pin.fingerprint,env.crypto??webRouteCrypto);check();
 if(record.session.serverIdentity){const actual=await serverPin(pin.serverId,record.session.serverIdentity.publicKey,record.session.serverIdentity.fingerprint,env.crypto);check();if(!sameServerPin(pin,actual))throw new RouteError('identity_mismatch','Saved credentials and server identity disagree.');}
 const memory:RouteMemory={paired:record.routes.paired,lastGood:record.routes.lastGood,...(record.routes.policy?{policy:record.routes.policy}:{})};let current:VerifiedRoutes|undefined,previous:VerifiedRoutes|undefined;
 if(record.hosted){for(const field of ['current','previous'] as const){const signed=record.routes[field];if(signed){const verified=await verifyRoutes(signed,record.hosted.key,{accountId:record.hosted.accountId,serverId:pin.serverId,pin,allowExpiredHint:true},env.crypto);check();if(field==='current')current=verified;else previous=verified;}}}
 const freshSource=source?.(record),hosted=record.hosted;
 const connection=new RouteConnection({pin,logicalOrigin:record.logicalOrigin,memory:{...memory,current,previous},fetcher:env.fetcher,crypto:env.crypto,discover:env.discover,onProbeFailure:env.onProbeFailure,policy:env.policy,readConnection:env.readConnection,fresh:freshSource&&hosted?async signal=>{const next=await currentDocument(freshSource,env,pin,hosted.key,signal);hosted.key=next.key;return next.routes;}:undefined});
 try{
  await connection.resolve(signal);check();
  const selected=await env.storage.read();check();
  if(!sameSelection(selected,record)||selected!.session.accessToken!==record.session.accessToken||(exact&&!exactViewerMatches(selected)))throw new RouteError('cancelled','The saved selection changed during reconnect.');
  const api=new HttpLocalApi(record.logicalOrigin,'',env.fetcher);api.useRoutes(connection);api.setAccessToken(record.session.accessToken);let renewedNow=false;
  // A sign-in saved more than a few minutes ago has an expired access token: renew it first.
  if(!exact&&record.session.refreshToken&&(record.pendingRefresh||Date.parse(record.session.expiresAt)-Date.now()<REFRESH_MIN_VALID_MS)){
   bindSessionRoute(record.session.accessToken,connection);
   const renewed=await refreshStoredSession({env,api,family:{sessionFamilyId:record.session.sessionFamilyId,serverId:record.pin.serverId},signal});check();
   record.session=renewed;record.pendingRefresh=undefined;api.setAccessToken(renewed.accessToken);renewedNow=true;
  }
  // Fresh private-key possession is not membership. The server must accept the
  // same native family under its current local or signed cached policy.
  // An access token the server no longer accepts (expired early, or the server restarted) is not
  // a lost sign-in: renew it with the refresh token once, then ask again.
  // Only a 401 is a credential problem. A 403, or a 401 for a token that was just renewed, means
  // the server knows this sign-in and refuses it access: `access_refused`, reported once and never
  // retried (each retry would rotate a refresh token for nothing). The credential stays.
  const refusedAccess=()=>new RouteError(ACCESS_REFUSED,'This profile no longer has access to this server.');
  const statusOf=(e:unknown)=>(e as {status?:unknown}|null)?.status;
  const validateLiveScope=async()=>{
   const viewer=record.session.viewer;
   if(viewer.authority==='local'&&viewer.role==='account'){
    // Account sign-in has not selected a viewer. Validate this exact live
    // account through its canonical endpoint; /me correctly refuses it.
    const raw=await api.request(routePath('get_direct_account'),'GET',undefined,signal);check();
    const snapshot=parseDirectSnapshot(raw,
     {authority:'local',accountId:viewer.accountId,serverId:viewer.serverId});
    if(snapshot.account.disabled||snapshot.account.primaryProfileId!==viewer.profileId)throw new RouteError('identity_mismatch','The saved account no longer matches this server sign-in.');
    return;
   }
   const me=await api.me(signal);check();
   for(const field of ['authority','accountId','profileId','serverId'] as const)if(me.viewer[field]!==viewer[field])throw new RouteError('identity_mismatch','The saved viewer no longer matches this server session.');
   if(me.viewer.role!==viewer.role){
    // Ordinary restoration adopts current permissions; captured Security continuations stay exact.
    if(exact||!['owner','admin','member'].includes(me.viewer.role))throw new RouteError('identity_mismatch','The saved viewer no longer matches this server session.');
    const next=parseLocalSession({...record.session,viewer:me.viewer},{authority:viewer.authority,accountId:viewer.accountId,profileId:viewer.profileId,serverId:viewer.serverId},{stored:true});
    check();await env.storage.change(current=>{check();return sameSelection(current,record)&&current!.session.accessToken===record.session.accessToken?{...current!,session:{...current!.session,viewer:next.viewer}}:current;},check);check();
    record.session=next;
   }
  };
  try{await validateLiveScope();check();}
  catch(e){
   check();const status=statusOf(e);
   if(status===403||(status===401&&renewedNow))throw refusedAccess();
   if(exact||!record.session.refreshToken||status!==401)throw e;
   bindSessionRoute(record.session.accessToken,connection);
   const renewed=await refreshStoredSession({env,api,family:{sessionFamilyId:record.session.sessionFamilyId,serverId:record.pin.serverId},rejected:record.session.accessToken,signal});check();
   record.session=renewed;record.pendingRefresh=undefined;api.setAccessToken(renewed.accessToken);
   try{await validateLiveScope();check();}
   catch(again){check();const s=statusOf(again);if(s===401||s===403)throw refusedAccess();throw again;}
  }
  const selectedAfter=await env.storage.read();check();
  if(!sameSelection(selectedAfter,record)||selectedAfter!.session.accessToken!==record.session.accessToken||(exact&&!exactViewerMatches(selectedAfter)))throw new RouteError('cancelled','The saved selection changed during reconnect.');
  if(!exact){attachPersistence(connection,env,record,hosted);bindSessionRoute(record.session.accessToken,connection);}return {api,session:record.session};
 }catch(e){connection.dispose();throw e;}
 }catch(e){check();throw e;}
}
export async function forgetNativeSession(env:ConnectionEnvironment,session:LocalSession):Promise<void>{
 // The family is the sign-in: a rotated access token in the store is still this sign-in.
 let stored:string|undefined;
 await env.storage.change(current=>{if(current?.session.sessionFamilyId===session.sessionFamilyId&&current.session.viewer.serverId===session.viewer.serverId){stored=current.session.accessToken;return undefined;}return current;});
 forgetSessionRoute(session.accessToken);if(stored)forgetSessionRoute(stored);
}
/** Existing authorization renewal owns token rotation. This only updates the
 * atomic saved native envelope without replacing route or playback authority. */
export async function rememberRotatedSession(env:ConnectionEnvironment,session:LocalSession):Promise<void>{
 const parsed=parseLocalSession(session);let saved:RememberedServer|undefined,connection:RouteConnection|undefined;
 await env.storage.change(current=>{
  if(!current||current.session.sessionFamilyId!==parsed.sessionFamilyId||current.session.viewer.serverId!==parsed.viewer.serverId)return current;
  for(const field of ['authority','accountId','profileId'] as const)if(current.session.viewer[field]!==parsed.viewer[field])throw new RouteError('identity_mismatch','Renewal changed the selected viewer.');
  if(parsed.serverIdentity&&(parsed.serverIdentity.publicKey!==current.pin.publicKey||parsed.serverIdentity.fingerprint!==current.pin.fingerprint))throw new RouteError('identity_mismatch','Renewal changed the pinned server.');
  const generation=BigInt(parsed.tokenGeneration),old=BigInt(current.session.tokenGeneration);
  if(generation<old)return current;
  if(generation===old&&parsed.accessToken!==current.session.accessToken)throw new RouteError('identity_mismatch','Conflicting credentials for the same generation.');
  connection=current.session.accessToken?sessionRouteForRotation(current.session.accessToken):undefined;
  saved={...current,session:parsed};return saved;
 });
 if(saved&&connection){const current=await env.storage.read();if(sameSelection(current,saved)&&current!.session.accessToken===parsed.accessToken)bindSessionRoute(parsed.accessToken,connection);}
}

const pendingConnections=new Map<string,{api:HttpLocalApi;hosted?:RememberedServer['hosted']}>();
export function prepareRoutedSession(api:HttpLocalApi,session:LocalSession,hosted?:RememberedServer['hosted']) {
 const connection=api.getRouteConnection();if(!connection||connection.pin.serverId!==session.viewer.serverId)throw new RouteError('identity_mismatch','This session does not match the verified server.');
 api.setAccessToken(session.accessToken);bindSessionRoute(session.accessToken,connection);pendingConnections.set(session.accessToken,{api,hosted});
 while(pendingConnections.size>8){const key=pendingConnections.keys().next().value!;pendingConnections.get(key)?.api.getRouteConnection()?.dispose();pendingConnections.delete(key);forgetSessionRoute(key);}
}
export async function commitRoutedSession(session:LocalSession,env:ConnectionEnvironment,isCurrent:()=>boolean=()=>true):Promise<HttpLocalApi>{
 const prepared=pendingConnections.get(session.accessToken);if(!prepared)throw new RouteError('missing_identity','The verified connection is no longer available. Select the server again.');
 try{await rememberNativeSession(prepared.api,session,env,prepared.hosted,isCurrent);pendingConnections.delete(session.accessToken);return prepared.api;}
 catch(error){if(!isCurrent()){pendingConnections.delete(session.accessToken);forgetSessionRoute(session.accessToken);}throw error;}
}

/** Trusted profile exchange is an explicit selection. Prove the cached server
 * before sending the offline proof; an old UI URL is not an identity check. */
export async function connectTrustedProfileServer(env:ConnectionEnvironment,scope:{accountId:string;serverId:string},signal:AbortSignal):Promise<HttpLocalApi>{
 const record=parseRememberedServer(await env.storage.read());
 if(!record||record.session.viewer.authority!=='hosted'||record.session.viewer.accountId!==scope.accountId||record.pin.serverId!==scope.serverId)throw new RouteError('identity_mismatch','Open the saved connection for this account and server first.');
 const pin=await serverPin(record.pin.serverId,record.pin.publicKey,record.pin.fingerprint,env.crypto);
 const memory:RouteMemory={paired:record.routes.paired,lastGood:record.routes.lastGood,...(record.routes.policy?{policy:record.routes.policy}:{})};
 let current:VerifiedRoutes|undefined,previous:VerifiedRoutes|undefined;
 if(record.hosted)for(const field of ['current','previous']as const){const signed=record.routes[field];if(signed){const v=await verifyRoutes(signed,record.hosted.key,{accountId:scope.accountId,serverId:scope.serverId,pin,allowExpiredHint:true},env.crypto);if(field==='current')current=v;else previous=v;}}
 const connection=new RouteConnection({pin,logicalOrigin:record.logicalOrigin,memory:{...memory,current,previous},fetcher:env.fetcher,crypto:env.crypto,discover:env.discover,onProbeFailure:env.onProbeFailure,policy:env.policy,readConnection:env.readConnection});
 try{await connection.resolve(signal);const selected=await env.storage.read();if(!sameSelection(selected,record)||signal.aborted)throw new RouteError('cancelled','The saved server selection changed.');const api=new HttpLocalApi(record.logicalOrigin,'',env.fetcher);api.useRoutes(connection);return api;}catch(e){connection.dispose();throw e;}
}

/** Renew when less than this much access time remains. */
export const REFRESH_MIN_VALID_MS = 60_000;
/**
 * The 403 a renewal answers outside the hours the owner allows this account. The server ends
 * nothing: the same credential renews again once the hours return, so the sign-in is kept.
 */
export const REFRESH_OUTSIDE_HOURS = 'outside_allowed_hours';

/**
 * A refresh that could not complete. `signOut` means the sign-in is over on this device. Either
 * the server refused the credential (reused, revoked, expired, device no longer approved) and the
 * stored record has been removed, or the stored record is replaced, gone or damaged
 * (`saved_sign_in_damaged`, status 0: the server was not asked, and the record is left for a new
 * sign-in to replace). Otherwise the credential is kept and the refresh can be retried (same
 * request id).
 */
export class SessionRefreshError extends ApiError {
 readonly signOut:boolean;
 constructor(code:string,message:string,signOut:boolean,status=signOut?401:0){super(status,code,message,!signOut);this.signOut=signOut;}
}

export type RefreshStoredOptions=Readonly<{
 env:ConnectionEnvironment;
 /** The selected server's transport (routed); only its `request` is used. */
 api:Pick<HttpLocalApi,'request'>;
 family:Readonly<{sessionFamilyId:string;serverId:string}>;
 /** The access token a server just refused: refresh even if the stored one still looks fresh. */
 rejected?:string;
 now?:()=>number;
 minValidMs?:number;
 timeoutMs?:number;
 signal?:AbortSignal;
}>;

/**
 * One refresh against the protected connection store (lane C `POST /v1/auth/refresh`):
 * under the credential lock, re-read the store (another tab or a restore may already have
 * rotated), persist the request id before sending, send, then store the next access and refresh
 * credential in one atomic change. A crash after sending replays the same request id and the
 * server returns its stored answer until the successor is used or the family horizon ends.
 * An expired replay is persisted and renewed once more within this same lock. Refusals end the sign-in.
 */
export function refreshStoredSession(o:RefreshStoredOptions):Promise<LocalSession>{
 const check=()=>checkRestoreCancellation(o.signal);
 const now=o.now??Date.now,minValid=o.minValidMs??REFRESH_MIN_VALID_MS;
 const same=(r:RememberedServer|undefined)=>!!r&&r.session.sessionFamilyId===o.family.sessionFamilyId&&r.pin.serverId===o.family.serverId;
 return credentialLock()('portico.session-refresh:'+o.family.serverId,async()=>{
  // A receipt plus one successor rotation, never an unbounded chain or a nested credential lock.
  for(let attempt=0;attempt<2;attempt++){
  check();
  let record:RememberedServer|undefined,stored:unknown;
  // Three different things can be wrong with the saved sign-in, and only the store can tell the
  // first two apart. It cannot be read right now (locked, busy, timed out): nothing is known about
  // the family, so nothing ends and the renewal is tried again. It is there but damaged for good:
  // waiting will not mend it, so the sign-in ends on this device, by this device's account and not
  // the server's, and the record stays where it is for a new sign-in to replace.
  const damaged=()=>new SessionRefreshError('saved_sign_in_damaged','The sign-in saved on this device can’t be read. Sign in again.',true,0);
  try{stored=await o.env.storage.read();check();}
  catch(e){check();throw savedConnectionDamaged(e)?damaged():new SessionRefreshError('store_unavailable','This device couldn’t read its saved sign-in right now.',false);}
  try{record=parseRememberedServer(stored);}catch{throw damaged();}
  // Or it reads, and is no longer this sign-in.
  if(!record||!same(record))throw new SessionRefreshError('session_replaced','This sign-in was replaced or ended on this device.',true);
  const session=record.session;
  const fresh=Date.parse(session.expiresAt)-now()>minValid;
  if(fresh&&!record.pendingRefresh&&(o.rejected===undefined||session.accessToken!==o.rejected))return session;
  const identity=installationIdentity();
  if(!session.refreshToken||!identity)throw new SessionRefreshError('refresh_unavailable','This sign-in can’t be renewed. Sign in again when it expires.',false);
  let requestId=record.pendingRefresh;
  if(!requestId){
   const id=routeBase64((o.env.crypto??webRouteCrypto).random(24));requestId=id;
   // The first context to save a request id owns this renewal. The lock above does not reach
   // other tabs where the browser has no Web Locks (a page on plain HTTP), so two tabs can get
   // here together: the store's own atomic change lets exactly one id in, and the other tab
   // reads it back and sends the very same request, which the server answers once for both.
   // Sending two different ids for one refresh credential would look like theft and end the sign-in.
   await o.env.storage.change(current=>{check();return sameSelection(current,record!)&&current!.session.refreshToken===session.refreshToken&&!current!.pendingRefresh?{...current!,pendingRefresh:id}:current;},check);check();
   let after:RememberedServer|undefined;
   try{const stored=await o.env.storage.read();check();after=parseRememberedServer(stored);}
   catch{check();throw new SessionRefreshError('refresh_unavailable_now','This device couldn’t verify its saved renewal right now.',false);}
   if(!after||!same(after)||after.selectionId!==record.selectionId)throw new SessionRefreshError('session_replaced','This sign-in was replaced or ended on this device.',true);
   if(after.session.refreshToken!==session.refreshToken){
    // Another context finished the renewal in the meantime: use what it stored, send nothing.
    if(Date.parse(after.session.expiresAt)-now()>minValid&&!after.pendingRefresh)return after.session;
    throw new SessionRefreshError('refresh_unavailable_now','The server couldn’t renew your sign-in right now.',false);
   }
   if(!after.pendingRefresh)throw new SessionRefreshError('refresh_unavailable_now','This device couldn’t verify its saved renewal right now.',false);
   requestId=after.pendingRefresh;
  }
  // React Native lacks AbortSignal.any/timeout: a plain controller bounds the request.
  const bound=new AbortController(),abort=()=>bound.abort(),timer=setTimeout(abort,o.timeoutMs??15_000);
  o.signal?.addEventListener('abort',abort,{once:true});if(o.signal?.aborted)abort();
  let raw:unknown;
  try{raw=await o.api.request<RouteResponse<"post_auth_refresh">>(routePath("post_auth_refresh"),'POST',routeBody("post_auth_refresh", {refreshToken:session.refreshToken,installationId:identity.installationId,requestId}),bound.signal);check();}
  catch(e){
   check();const status=(e as {status?:unknown}|null)?.status;
   // Only a 401 ends the sign-in (revoked, unknown family, or a reused refresh token). No 403 does:
   // the server's own (outside the account's allowed hours), a gateway's page and a rejected origin
   // all leave the family as it was, so the credential is kept. A malformed request (400) is this
   // client's problem and keeps it too.
   if(status===403&&(e as {code?:unknown}|null)?.code===REFRESH_OUTSIDE_HOURS)throw new SessionRefreshError(REFRESH_OUTSIDE_HOURS,'This account can’t be used at this time of day.',false,403);
   if(status===401){
    await o.env.storage.change(current=>{check();return same(current)&&current!.session.refreshToken===session.refreshToken?undefined:current;},check).catch(()=>{check();});check();
    forgetSessionRoute(session.accessToken);
    // The server's move to its own membership (migration 0120) ended this family: not a sign-out.
    // The code survives so the app can re-admit silently (`isSessionMigrated`).
    if(status===401&&(e as {code?:unknown}|null)?.code==='session_migrated')throw new SessionRefreshError('session_migrated','This server moved your sign-in to its own membership.',true);
    throw new SessionRefreshError('refresh_refused','Your sign-in has ended. Sign in again.',true);
   }
   if(status===404||status===405)throw new SessionRefreshError('refresh_unsupported','This server doesn’t renew sign-ins.',false,status as number);
   // Transport failures keep their own shape so restore policies still see "offline, retry".
   if(typeof status!=='number')throw e;
   // The server's own wait (a 429 or 503 with Retry-After) travels with the failure, so the next try respects it.
   const busy=new SessionRefreshError('refresh_unavailable_now','The server couldn’t renew your sign-in right now.',false,status);
   const wait=(e as {retryAfterSeconds?:unknown}|null)?.retryAfterSeconds;if(typeof wait==='number'&&wait>0)busy.retryAfterSeconds=wait;
   throw busy;
  }finally{clearTimeout(timer);o.signal?.removeEventListener('abort',abort);}
  let next:LocalSession;
  try{next=parseLocalSession(raw,{authority:session.viewer.authority,accountId:session.viewer.accountId,profileId:session.viewer.profileId,serverId:session.viewer.serverId},{refreshReceipt:true});}
  catch{throw new SessionRefreshError('refresh_invalid','The renewed sign-in didn’t match this one.',false);}
  if(next.sessionFamilyId!==session.sessionFamilyId||BigInt(next.tokenGeneration)<=BigInt(session.tokenGeneration)||Date.parse(next.authorizationHorizon)<=now()||
   !next.refreshToken||next.refreshToken===session.refreshToken||
   // An expired receipt is adopted and renewed here with no caller in between, so its bindings
   // are checked here. A live successor is returned, and the caller's own binding gate decides.
   (Date.parse(next.expiresAt)<=now()&&((session.deviceId!==undefined&&next.deviceId!==session.deviceId)||
    (next.installationId!==undefined&&next.installationId!==identity.installationId)||(session.installationId!==undefined&&next.installationId!==session.installationId)))||
   (session.viewer.role==='account'&&next.viewer.role!=='account')||(next.serverIdentity&&(next.serverIdentity.publicKey!==record.pin.publicKey||next.serverIdentity.fingerprint!==record.pin.fingerprint)))
   throw new SessionRefreshError('refresh_invalid','The renewed sign-in didn’t match this one.',false);
  const connection=sessionRoute(session.accessToken);let saved=false;
  try{
   await o.env.storage.change(current=>{check();if(!sameSelection(current,record!)||current!.session.refreshToken!==session.refreshToken||current!.pendingRefresh!==requestId)return current;const {pendingRefresh:_done,...rest}=current!;saved=true;return {...rest,session:next};},check);check();
  }catch{
   check();
   // Never spend an expired successor before its durable adoption: the predecessor receipt
   // must remain replayable after a failed write or process exit.
   if(Date.parse(next.expiresAt)<=now())throw new SessionRefreshError('store_unavailable','This device couldn’t save its renewed sign-in right now.',false);
  }
  if(Date.parse(next.expiresAt)<=now()&&!saved)throw new SessionRefreshError('refresh_unavailable_now','The saved sign-in changed during renewal. Try again.',false);
  if(connection)bindSessionRoute(next.accessToken,connection);
  if(Date.parse(next.expiresAt)>now())return next;
  }
  throw new SessionRefreshError('refresh_unavailable_now','The server couldn’t renew your sign-in right now.',false);
 }).catch(error=>{check();throw error;});
}
