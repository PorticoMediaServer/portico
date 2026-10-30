import {routePath as developmentRoutePath} from "../../contracts/src/development/routes.ts";
import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {parseLocalSession,currentUTCTimestamp} from './local-session.ts';
import type {LocalSession} from './index.ts';
import {keepReadable,oneOfOr} from './tolerant.ts';
import {requireTag} from './needs-newer-app.ts';

/** Account credentials administer profiles; a selected profile is only a viewer. */
export interface ProfileAPI {request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}
export type ProfileAuthority='local'|'hosted';
export const PROFILE_ART=['blue','violet','mint','coral','gold','slate','rose','sky'] as const;
export type ProfileArt=typeof PROFILE_ART[number];
export type ManagedProfile=Readonly<{id:string;name:string;primary:boolean;position:number;art:ProfileArt;revision:number;pinRevision:number;pinRequired:boolean;trustRevision?:number;allowedLibraries?:readonly string[]|null}>;
/** `hostedAccountId`: a Portico Account member (Spec — Hosted at Scale). Its password, email and
 * two-step verification are managed at the Portico Account; profiles, PINs, sessions and devices
 * here, exactly as for a direct account. */
export type DirectAccount=Readonly<{id:string;username:string;primaryProfileId:string;role:'owner'|'admin'|'member';revision:number;disabled:boolean;allowedLibraries:readonly string[];hostedAccountId?:string}>;
/** `custodyPending`: the owner is a Portico Account, but Hosted's custody of the server's claim
 * (retiring it, its certificates) is not with it yet: after a transfer to it, or after a direct
 * owner. Ask the owner to accept it (`acceptPorticoCustody`). */
export type ManagementCapabilities=Readonly<{manageAccount:boolean;managePeople:boolean;manageServer:boolean;manageSignIn:boolean}>;
export type DirectSnapshot=Readonly<{capabilities:ManagementCapabilities;authority:'local';serverId:string;account:DirectAccount;profiles:readonly ManagedProfile[];canManage:boolean;custodyPending?:boolean;twoStepSetupRequired?:true}>;
/**
 * C45 direct sign-in (Justin's account decisions). There is no separate account token any more:
 * - `signed-in`: one device-bound `session`. With several profiles or a PIN it has role `account`
 *   (`accountScoped`) and a profile must be chosen; with one unprotected profile it is the viewer.
 *   `passwordChangeRequired` means: `changeRequiredPassword` with this session, then sign in again.
 * - `challenge`: the account has two-step verification; finish with `completeDirectSignInChallenge`.
 */
export type DirectSignIn=
 Readonly<{kind:'signed-in';snapshot:DirectSnapshot;session:LocalSession;accountScoped:boolean;passwordChangeRequired:boolean;resetFileOwnerSession?:true}>
 |Readonly<{kind:'challenge';token:string;expiresAt:string}>;
/** A required setup remains an account-scoped credential, never a viewer or
 * management grant. Clients without factor setup retain their restricted
 * sign-in and use their existing supported web journey. No role is inferred. */
export function directSignInContinuation(result:DirectSignIn):'challenge'|'password-change'|'two-step-setup'|'profile-selection'|'viewer'{
 if(result.kind==='challenge')return 'challenge';
 if(result.passwordChangeRequired)return 'password-change';
 if(result.snapshot.twoStepSetupRequired)return 'two-step-setup';
 return result.accountScoped?'profile-selection':'viewer';
}
export type ProfileScope=Readonly<{authority:ProfileAuthority;accountId:string;serverId?:string}>;
export type ProfileSelectionInput=Readonly<{pin?:string;installationId?:string;trust?:boolean;trustToken?:string}>;
export type TrustedProfile=Readonly<{token:string;authority:ProfileAuthority;accountId:string;profileId:string;serverId:string;installationId:string;pinRevision:number;profileRevision:number;policyRevision?:number;membershipRevision?:number;trustRevision?:number;expiresAt:string}>;
export type SignedProfileProof=Readonly<{payload:string;signature:string;keyId:string}>;
export type DirectSelection=Readonly<{session:LocalSession;trustedSelection?:TrustedProfile}>;
/** A deletion preview: the revision and the profile lifetime the server will retire. The lifetime is
 * generated once by the server and never reused, so a replayed delete names the same retirement. */
export type ProfileDeletion=Readonly<{id?:string;profileId:string;revision:number;lifetime:string;name?:string;primary:boolean;status?:string;playlists?:number;savedResources?:number;servers?:readonly {id:string;name:string;acknowledged:boolean}[]}>;
/** Retirement is logical first, then a purge: only `removed` is finished. `blocked` is not success. */
export type ProfileRetirementState='removing'|'removed'|'blocked';
export type ProfileRetirement=Readonly<{profileId:string;lifetime:string;state:ProfileRetirementState}>;
export type ProfileDeletionOptions=Readonly<{
 signal?:AbortSignal;
 /** Every checked state, in order: `removing` and `blocked` keep the UI's pending wording. */
 onStatus?:(retirement:ProfileRetirement)=>void;
 /** Focused tests shorten the check interval. */
 intervalMs?:number;
 /** Focused tests bound the whole wait. The UI omits it: a private purge may legally take
  * longer than any fixed wait, so it keeps one repeated timer until `removed`, an error or the
  * viewer's abort instead of giving up on a retirement that is still running. */
 maxWaitMs?:number;
}>;
export type ProfilePolicy=Readonly<{serverId:string;allowedLibraries:readonly string[]|null}>;
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const natural=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const revision=(v:unknown):v is number=>natural(v)&&v>0;
/** A profile lifetime is 32 lowercase hexadecimal characters; it is never guessed or reused. */
const profileLifetime=(v:unknown):v is string=>typeof v==='string'&&/^[0-9a-f]{32}$/.test(v);
/** A retirement names the exact profile and lifetime that was previewed. Anything else, a lost
 * response included, is unreadable: it is never taken as `removed`. */
export function parseProfileRetirement(raw:unknown,expected:Readonly<{profileId:string;lifetime:string}>):ProfileRetirement{
 if(!obj(raw)||!text(raw.profileId,128)||!profileLifetime(raw.lifetime)||raw.profileId!==expected.profileId||raw.lifetime!==expected.lifetime||!['removing','removed','blocked'].includes(raw.state as string))invalid();
 return Object.freeze({profileId:raw.profileId,lifetime:raw.lifetime,state:raw.state as ProfileRetirementState});
}
const retirementStopped=()=>Object.assign(new Error('Profile removal stopped'),{name:'AbortError'});
const throwIfRetirementStopped=(signal?:AbortSignal):void=>{if(signal?.aborted)throw retirementStopped();};
/** One timer at a time, cleared with its listener when the removal is retired. */
function retirementSleep(signal?:AbortSignal):(ms:number)=>Promise<void>{
 return ms=>new Promise<void>((resolve,reject)=>{
  if(signal?.aborted){reject(retirementStopped());return;}
  let timer:ReturnType<typeof setTimeout>|undefined;
  const stop=()=>{if(timer!==undefined)clearTimeout(timer);signal?.removeEventListener('abort',stop);reject(retirementStopped());};
  timer=setTimeout(()=>{signal?.removeEventListener('abort',stop);resolve();},ms);
  signal?.addEventListener('abort',stop,{once:true});
 });
}
/** A checked `Retry-After` hint, never an unbounded pause. */
function retryAfterMs(error:unknown):number|undefined{
 const seconds=(error as {retryAfterSeconds?:unknown}|null|undefined)?.retryAfterSeconds;
 return typeof seconds==='number'&&Number.isSafeInteger(seconds)&&seconds>0&&seconds<=60?seconds*1000:undefined;
}
function invalid():never{throw new CodedError('invalid_response',unreadableServerResponse);}
function ids(v:unknown):readonly string[]{if(!Array.isArray(v)||v.length>1000||v.some(x=>!text(x,128))||new Set(v).size!==v.length)invalid();return Object.freeze([...v] as string[]);}
/**
 * A picture colour this app does not know reads as `blue`, the colour a new profile starts with; the name sent is never kept.
 * Everything else about a profile is exact, its PIN requirement and allowed libraries above all. Where the whole list is relied
 * on (sign-in, the account snapshot) one unreadable profile refuses the list. With `readable`, a list read for display leaves
 * that profile out instead: it is never shown with part of it missing. Either way there is exactly one primary profile, no id
 * appears twice and the list is no longer than an account may hold.
 */
export function parseManagedProfiles(raw:unknown,readable=false):readonly ManagedProfile[]{
 if(!Array.isArray(raw)||raw.length<1||raw.length>8)invalid();
 const row=(v:unknown)=>{if(!obj(v)||!text(v.id,128)||!text(v.name,120)||typeof v.primary!=='boolean'||!natural(v.position)||!text(v.art,64)||!revision(v.revision)||!revision(v.pinRevision)||typeof v.pinRequired!=='boolean'||(v.trustRevision!==undefined&&!revision(v.trustRevision)))invalid();
  return Object.freeze({id:v.id,name:v.name,primary:v.primary,position:v.position,art:oneOfOr('profiles','profile.art',v.art,PROFILE_ART,'blue'),revision:v.revision,pinRevision:v.pinRevision,pinRequired:v.pinRequired,...(v.trustRevision===undefined?{}:{trustRevision:v.trustRevision as number}),...(v.allowedLibraries===undefined?{}:{allowedLibraries:v.allowedLibraries===null?null:ids(v.allowedLibraries)})});};
 const result:ManagedProfile[]=readable?keepReadable('profiles','rows',raw,row):raw.map(row);
 if(new Set(result.map(v=>v.id)).size!==result.length||result.filter(v=>v.primary).length!==1)invalid();
 return Object.freeze(result.sort((a,b)=>a.position-b.position||a.id.localeCompare(b.id)));
}
export function parseDirectSnapshot(raw:unknown,expected:Partial<ProfileScope>={},readableProfiles=false):DirectSnapshot{
 if(!obj(raw)||raw.authority!=='local'||!text(raw.serverId,128)||!obj(raw.account)||typeof raw.canManage!=='boolean'||(raw.custodyPending!==undefined&&typeof raw.custodyPending!=='boolean')||(raw.twoStepSetupRequired!==undefined&&typeof raw.twoStepSetupRequired!=='boolean'))invalid();const a=raw.account;
 if(!text(a.id,128)||!text(a.username,64)||!text(a.primaryProfileId,128)||!['owner','admin','member'].includes(a.role as string)||(a.hostedAccountId!==undefined&&!text(a.hostedAccountId,128))||!revision(a.revision)||typeof a.disabled!=='boolean'||(expected.accountId&&a.id!==expected.accountId)||(expected.serverId&&raw.serverId!==expected.serverId)||(expected.authority&&expected.authority!=='local'))invalid();
 const c=raw.capabilities;
 if(!obj(c)||!['manageAccount','managePeople','manageServer','manageSignIn'].every(key=>typeof c[key]==='boolean')||c.manageAccount!==raw.canManage||c.manageSignIn===true&&a.role!=='owner'||(c.managePeople===true||c.manageServer===true)&&!['owner','admin'].includes(a.role as string)||c.manageAccount===false&&(c.managePeople===true||c.manageServer===true||c.manageSignIn===true))invalid();
 const capabilities:ManagementCapabilities=Object.freeze({manageAccount:c.manageAccount as boolean,managePeople:c.managePeople as boolean,manageServer:c.manageServer as boolean,manageSignIn:c.manageSignIn as boolean});
 if(raw.twoStepSetupRequired===true&&Object.values(capabilities).some(Boolean))invalid();
 const profiles=parseManagedProfiles(raw.profiles,readableProfiles);if(!profiles.some(p=>p.primary&&p.id===a.primaryProfileId))invalid();
 return Object.freeze({authority:'local',serverId:raw.serverId,account:Object.freeze({id:a.id,username:a.username,primaryProfileId:a.primaryProfileId,role:a.role as DirectAccount['role'],revision:a.revision,disabled:a.disabled,allowedLibraries:ids(a.allowedLibraries),...(a.hostedAccountId===undefined?{}:{hostedAccountId:a.hostedAccountId as string})}),profiles,capabilities,canManage:raw.canManage,...(raw.custodyPending===true?{custodyPending:true}:{}),...(raw.twoStepSetupRequired===true?{twoStepSetupRequired:true as const}:{})});
}
export function parseDirectSignIn(raw:unknown):DirectSignIn{
 if(!obj(raw))invalid();
 if(raw.challenge!==undefined){
  const c=raw.challenge;if(!obj(c)||!text(c.token,256)||!currentUTCTimestamp(c.expiresAt))invalid();
  return Object.freeze({kind:'challenge',token:c.token,expiresAt:c.expiresAt});
 }
 if(Object.hasOwn(raw,'deviceApprovalPending'))invalid();
 const snapshot=parseDirectSnapshot(raw);
 // A sign-in can never admit a disabled membership, even if its snapshot is
 // otherwise valid for management/display reads.
 if(snapshot.account.disabled)invalid();
 if(raw.passwordChangeRequired!==undefined&&typeof raw.passwordChangeRequired!=='boolean')invalid();
 const passwordChangeRequired=raw.passwordChangeRequired===true;
 const session=parseLocalSession(raw.session,{authority:'local',accountId:snapshot.account.id,serverId:snapshot.serverId,profileId:snapshot.account.primaryProfileId});
 const accountScoped=session.viewer.role==='account';
 // Direct sign-in names the primary profile. Its viewer role must be the
 // actual membership role; an account-only grant remains account-only.
 if(!accountScoped&&session.viewer.role!==snapshot.account.role)invalid();
 if(snapshot.twoStepSetupRequired&&!accountScoped)invalid();
 if(raw.resetFileOwnerSession!==undefined&&raw.resetFileOwnerSession!==true)invalid();
 const resetFileOwnerSession=raw.resetFileOwnerSession===true;
 if(resetFileOwnerSession&&(accountScoped||passwordChangeRequired||snapshot.account.role!=='owner'||session.viewer.role!=='owner'||session.refreshToken))invalid();

 // A viewer session straight from sign-in only for one unprotected profile and no forced password change.
 if(!accountScoped&&!resetFileOwnerSession&&(passwordChangeRequired||snapshot.profiles.length!==1||snapshot.profiles[0]!.pinRequired))invalid();
 return Object.freeze({kind:'signed-in',snapshot,session,accountScoped,passwordChangeRequired,...(resetFileOwnerSession?{resetFileOwnerSession:true as const}:{})});
}
/** A sign-in answered by a route whose response union is tagged (R-09b): the server always sends `variant`, one of
 * `known`. A tag outside it is a newer server (`NeedsNewerAppError`); a known tag the sign-in does not match is unreadable. */
export function parseTaggedSignIn(raw:unknown,known:readonly string[],matches:(tag:string,out:DirectSignIn)=>boolean):DirectSignIn{
 const tag=requireTag(raw,'variant',known,invalid);
 const out=parseDirectSignIn(raw);
 if(!matches(tag,out))invalid();
 return out;
}
/** `POST /v1/direct/sign-in`. */
export async function directSignIn(api:ProfileAPI,username:string,password:string,signal?:AbortSignal):Promise<DirectSignIn>{
 return parseDirectSignIn(await api.request<RouteResponse<"post_direct_sign_in">>(routePath("post_direct_sign_in"),'POST',routeBody("post_direct_sign_in", {username,password}),signal));
}
/** Development builds only: the server's seeded e2e owner, if it has one (`GET /v1/dev/e2e`; 404 otherwise). */
export async function devE2EUsername(api:ProfileAPI,signal?:AbortSignal):Promise<string|undefined>{
 try{const raw=await api.request<unknown>(developmentRoutePath("get_dev_e2e"),'GET',undefined,signal);return obj(raw)&&text(raw.username,64)?raw.username:undefined;}catch{return undefined;}
}
/** Development builds only: signs in as the seeded e2e owner without a password (`POST /v1/dev/e2e/sign-in`, loopback servers built with devtrust). */
export async function devE2ESignIn(api:ProfileAPI,username:string,signal?:AbortSignal):Promise<DirectSignIn>{
 return parseDirectSignIn(await api.request<unknown>(developmentRoutePath("post_dev_e2e_sign_in"),'POST',{username},signal));
}
/** Finishes a two-step sign-in: the challenge token and an authenticator or recovery code (`POST /v1/auth/two-factor/challenge`). */
export async function completeDirectSignInChallenge(api:ProfileAPI,token:string,code:string,signal?:AbortSignal):Promise<DirectSignIn>{
 if(!text(token,256)||!text(code.trim(),64))throw new CodedError('code_required','Enter the code from your authenticator app or a recovery code.');
 const result=parseDirectSignIn(await api.request<RouteResponse<"post_auth_two_factor_challenge">>(routePath("post_auth_two_factor_challenge"),'POST',routeBody("post_auth_two_factor_challenge", {token,code:code.trim()}),signal));
 if(result.kind==='challenge')invalid();
 return result;
}
/**
 * A required password change (sign-in said `passwordChangeRequired`): with that account session's
 * bearer, `POST /v1/direct/password {currentPassword, newPassword}`; then sign in again.
 */
export async function changeRequiredPassword(api:ProfileAPI,currentPassword:string,newPassword:string,signal?:AbortSignal):Promise<void>{
 await api.request(routePath("post_direct_password"),'POST',routeBody("post_direct_password", {currentPassword,newPassword}),signal);
}
export function parseTrustedProfile(raw:unknown,scope:ProfileScope&{profileId:string;installationId:string},now=Date.now()):TrustedProfile{
 if(!obj(raw)||raw.authority!==scope.authority||raw.accountId!==scope.accountId||raw.profileId!==scope.profileId||raw.serverId!==scope.serverId||raw.installationId!==scope.installationId||!text(raw.token,4096)||!revision(raw.pinRevision)||!revision(raw.profileRevision)||!currentUTCTimestamp(raw.expiresAt)||Date.parse(raw.expiresAt)<=now||Date.parse(raw.expiresAt)>now+30*86400000+60000)invalid();
 for(const key of ['policyRevision','membershipRevision','trustRevision'])if(raw[key]!==undefined&&!revision(raw[key]))invalid();
 if(scope.authority==='hosted'&&(!revision(raw.policyRevision)||!revision(raw.trustRevision)))invalid();if(scope.authority==='local'&&!revision(raw.membershipRevision))invalid();
 return Object.freeze({token:raw.token,authority:scope.authority,accountId:scope.accountId,profileId:scope.profileId,serverId:scope.serverId!,installationId:scope.installationId,pinRevision:raw.pinRevision,profileRevision:raw.profileRevision,expiresAt:raw.expiresAt,...(raw.policyRevision===undefined?{}:{policyRevision:raw.policyRevision as number}),...(raw.membershipRevision===undefined?{}:{membershipRevision:raw.membershipRevision as number}),...(raw.trustRevision===undefined?{}:{trustRevision:raw.trustRevision as number})});
}
export async function selectDirectProfile(api:ProfileAPI,scope:ProfileScope&{serverId:string},profileId:string,input:ProfileSelectionInput,signal?:AbortSignal):Promise<DirectSelection>{
 if(scope.authority!=='local'||!text(profileId,128)||(input.pin!==undefined&&input.pin!==''&&!/^\d{4}$/.test(input.pin)))throw new CodedError('pin_format','Enter the four-digit profile PIN.');
 const raw=await api.request<RouteResponse<"post_direct_profiles_id_select">>(encodedRoutePath("post_direct_profiles_id_select", {"id": encodeURIComponent(profileId)}),'POST',input,signal);if(!obj(raw))invalid();
 const session=parseLocalSession(raw.session,{authority:'local',accountId:scope.accountId,serverId:scope.serverId,profileId});
 const trustedSelection=raw.trustedSelection===undefined?undefined:parseTrustedProfile(raw.trustedSelection,{...scope,profileId,installationId:input.installationId??''});
 return Object.freeze({session,...(trustedSelection?{trustedSelection}:{})});
}
export function trustScopeKey(scope:ProfileScope&{profileId:string;installationId:string}):string{
 if(!['hosted','local'].includes(scope.authority)||![scope.accountId,scope.serverId,scope.profileId,scope.installationId].every(v=>text(v,128)))throw new CodedError('invalid_scope','A complete installation and viewer scope is required.');
 return JSON.stringify(['portico-profile-trust-v1',scope.authority,scope.accountId,scope.serverId,scope.profileId,scope.installationId]);
}
/** Validation and revision-bound mutations are shared by web, iOS and tvOS. */
/**
 * The profiles of a local account, managed on its own server. (Hosted Portico Account profiles
 * have no API of their own: an account's profiles are each server's, so they are managed here.)
 */
export class ManagedProfilesClient {
 readonly api:ProfileAPI;readonly scope:ProfileScope;
 constructor(api:ProfileAPI,scope:ProfileScope){if(scope.authority!=='local'||!text(scope.accountId,128)||!text(scope.serverId,128))throw new Error('An account profile scope is required.');this.api=api;this.scope=Object.freeze({...scope});}
 private path(profileId:string|undefined,action:'create'|'edit'|'delete'|'order'|'preview'):string{
  if(action==='create')return routePath("post_direct_profiles");
  if(action==='order')return routePath("put_direct_profiles_order");
  if(!profileId)throw new Error('A profile is required.');
  if(action==='edit')return routePath("patch_direct_profiles_id",{id:profileId});
  if(action==='delete')return routePath("delete_direct_profiles_id",{id:profileId});
  return routePath("get_direct_profiles_id_deletion_preview",{id:profileId});
 }
 async list(signal?:AbortSignal):Promise<readonly ManagedProfile[]>{const raw=await this.api.request<RouteResponse<"get_direct_account">>(routePath("get_direct_account"),'GET',undefined,signal);return parseDirectSnapshot(raw,this.scope,true).profiles;}
 async create(name:string,art:ProfileArt='blue',signal?:AbortSignal):Promise<void>{if(!text(name.trim(),120)||!PROFILE_ART.includes(art))throw new Error('Enter a profile name.');await this.api.request(this.path(undefined,'create'),'POST',{name:name.trim(),art},signal);}
 async edit(profile:ManagedProfile,change:{name?:string;art?:ProfileArt;pin?:string;policy?:ProfilePolicy},signal?:AbortSignal):Promise<void>{
  if(change.pin!==undefined&&change.pin!==''&&!/^\d{4}$/.test(change.pin))throw new Error('Use exactly four numeric digits for the profile PIN.');
  if(change.name!==undefined&&!text(change.name.trim(),120))throw new Error('Enter a profile name.');
  if(change.policy){if(change.policy.serverId!==this.scope.serverId)throw new Error('This profile belongs to a different server.');if(change.policy.allowedLibraries!==null)ids(change.policy.allowedLibraries);}
  const policy=change.policy?{allowedLibraries:change.policy.allowedLibraries}:undefined;
  await this.api.request(this.path(profile.id,'edit'),'PATCH',{...change,...(policy?{policy}:{}),expectedRevision:profile.revision},signal);
 }
 async order(profiles:readonly ManagedProfile[],signal?:AbortSignal):Promise<void>{await this.api.request(this.path(undefined,'order'),'PUT',{profiles:profiles.map(p=>({id:p.id,revision:p.revision}))},signal);}
 async forgetTrust(profileId:string,signal?:AbortSignal):Promise<void>{await this.api.request(routePath("delete_direct_trust"),'DELETE',{profileId},signal);}
 async previewDelete(profile:ManagedProfile,signal?:AbortSignal):Promise<ProfileDeletion>{
  if(profile.primary)throw new Error('The primary profile is managed through account settings.');const raw=await this.api.request<unknown>(this.path(profile.id,'preview'),'GET',undefined,signal);
  if(!obj(raw)||raw.profileId!==profile.id||!revision(raw.revision)||raw.primary!==false||!profileLifetime(raw.lifetime))invalid();return Object.freeze({...raw}) as ProfileDeletion;
 }
 private retirementPath(profileId:string,lifetime:string):string{
  return routePath('get_direct_profiles_id_retirement',{id:profileId})+'?lifetime='+encodeURIComponent(lifetime);
 }
 /**
  * Retire a profile for every client at once, then report the purge truthfully: the accepted body and
  * lifetime are sent once and replayed unchanged if a response is lost, `removed` is reported only
  * when the server says so, and `blocked`, an unreadable state or an abort is never success. The wait
  * has no fixed end: one check timer at a time, until removed, an error, or the caller's abort.
  */
 async delete(preview:ProfileDeletion,options:ProfileDeletionOptions={}):Promise<ProfileRetirement>{
  if(preview.primary||!revision(preview.revision)||!profileLifetime(preview.lifetime))throw new Error('Preview this profile deletion first.');
  const {signal}=options,body=Object.freeze({expectedRevision:preview.revision,lifetime:preview.lifetime,deleteSavedResources:true});
  throwIfRetirementStopped(signal);
  const accepted=await this.api.request<RouteResponse<"delete_direct_profiles_id">>(this.path(preview.profileId,'delete'),'DELETE',body,signal);
  // An answer that arrives after the viewer retired is not published as progress.
  throwIfRetirementStopped(signal);
  let status=parseProfileRetirement(accepted,{profileId:preview.profileId,lifetime:preview.lifetime});
  options.onStatus?.(status);
  const finish=(state:ProfileRetirement):ProfileRetirement=>{
   if(state.state==='removed')return state;
   if(state.state==='blocked')throw new CodedError('retirement_blocked','This profile could not be finished removing.');
   return state;
  };
  if(status.state!=='removing')return finish(status);
  const sleep=retirementSleep(signal),interval=Math.max(1,Math.floor(options.intervalMs??500));
  const budget=options.maxWaitMs===undefined?undefined:Math.max(interval,Math.floor(options.maxWaitMs));
  let waited=0;
  while(budget===undefined||waited<budget){
   const step=budget===undefined?interval:Math.min(interval,budget-waited);
   await sleep(step);waited+=step;
   throwIfRetirementStopped(signal);
   try{
    status=parseProfileRetirement(await this.api.request<unknown>(this.retirementPath(preview.profileId,preview.lifetime),'GET',undefined,signal),{profileId:preview.profileId,lifetime:preview.lifetime});
   }catch(error){
    // A bounded, server-asked pause is the only retry: no busy loop on an error.
    const pause=retryAfterMs(error);if(pause===undefined||budget!==undefined&&waited+pause>=budget)throw error;
    await sleep(pause);waited+=pause;continue;
   }
   throwIfRetirementStopped(signal);
   options.onStatus?.(status);
   if(status.state!=='removing')return finish(status);
  }
  throw new CodedError('retirement_pending','This profile is still being removed.');
 }
}

/** Retire a temporary account sign-in only after a distinct viewer family was accepted.
 * A direct sign-in may already be the viewer session, and deleting that bearer signs the viewer out. */
export function shouldRetireAccountSession(account:Pick<LocalSession,'accessToken'|'sessionFamilyId'>|undefined,viewer:Pick<LocalSession,'accessToken'|'sessionFamilyId'>):boolean {
 return !!account && !!account.accessToken && !!account.sessionFamilyId && !!viewer.sessionFamilyId && account.accessToken!==viewer.accessToken && account.sessionFamilyId!==viewer.sessionFamilyId;
}

/** A fresh sign-in proves Forgot PIN without replacing the installed viewer session.
 * `authenticate` completes local/Portico sign-in and any configured second factor.
 * The temporary transport is used only for this reset and then signed out.
 */
export async function resetProfilePINWithFreshSignIn(authenticate:()=>Promise<DirectSignIn>,temporaryAPI:(session:LocalSession)=>ProfileAPI,scope:ProfileScope&{serverId:string},profileId:string,pin:string,signal?:AbortSignal):Promise<void>{
 if(scope.authority!=='local'||!text(profileId,128)||pin!==''&&!/^\d{4}$/.test(pin))throw new CodedError('pin_format','Enter the four-digit profile PIN.');
 const signIn=await authenticate();if(signIn.kind!=='signed-in')throw new CodedError('authentication_required','Complete account sign-in first.');
 const api=temporaryAPI(signIn.session);
 try{
  if(signIn.snapshot.account.id!==scope.accountId||signIn.snapshot.serverId!==scope.serverId||!signIn.snapshot.profiles.some(p=>p.id===profileId))throw new CodedError('invalid_scope','Sign in to the account that owns this profile.');
  await api.request(routePath('post_direct_profile_pin_reset',{id:profileId}),'POST',routeBody('post_direct_profile_pin_reset',{pin}),signal);
 }finally{
  try{await api.request(routePath('delete_sessions_current'),'DELETE');}catch(e){if(!(e instanceof CodedError)||!['unauthorized','authentication_required','session_ended'].includes(e.code))throw e;}
 }
}
