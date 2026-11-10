import type {DeviceInfo} from '../../contracts/src/next/api.ts';
import {callServer} from './server-api.ts';
import {installationIdentity} from './installation.ts';
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {parseLocalSession,currentUTCTimestamp} from './local-session.ts';
import type {LocalSession} from './index.ts';
import {keepReadable,oneOfOr} from './tolerant.ts';
import {requireTag} from './needs-newer-app.ts';

/** Account credentials administer profiles; a selected profile is only a viewer. */
export interface ProfileAPI {request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;
 /** The same server, sending another sign-in's token (`HttpLocalApi.withAccessToken`). */
 withAccessToken?(token:string):ProfileAPI}
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
/** `rememberProfiles`: the PIN screen may offer Remember this profile. `managePinRequired`: this remembered
 * main profile enters its PIN again before it manages the account (profile_pin_manage). */
/** PR-1: the server refused a management request until this remembered main profile enters its PIN again
 * (`profile_pin_manage`). It acted on nothing, so what the screen holds is still current; the app's shell asks for the PIN. */
export const managementPinRefused = (error: unknown): boolean => (error as {code?: unknown} | null)?.code === 'profile_pin_manage';
export type DirectSnapshot=Readonly<{capabilities:ManagementCapabilities;authority:'local';serverId:string;account:DirectAccount;profiles:readonly ManagedProfile[];canManage:boolean;custodyPending?:boolean;twoStepSetupRequired?:true;rememberProfiles?:boolean;managePinRequired?:true}>;
/**
 * The answer to a sign-in:
 * - `signed-in`: one `session` for this device. Until a profile is chosen it has the role `account`
 *   (`accountScoped`); `directSignIn` chooses an account's only profile itself when it has no PIN.
 * - `challenge`: the account has two-step sign-in; finish with `completeDirectSignInChallenge`.
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
/** `remember` (the PIN screen's Remember this profile) is not on the new server yet and is not sent. */
export type ProfileSelectionInput=Readonly<{pin?:string;installationId?:string;remember?:boolean}>;
/** `remembered`: the new sign-in remembers its profile until `expiresAt`. */
export type DirectSelection=Readonly<{session:LocalSession;remembered?:Readonly<{expiresAt:string}>}>;
/** What the confirmation shows before a profile is deleted. `revision` and `lifetime` are kept for
 * the screens that pass a preview back; the server deletes a profile in one step and reads neither. */
export type ProfileDeletion=Readonly<{id?:string;profileId:string;revision:number;lifetime:string;name?:string;primary:boolean;status?:string;playlists?:number;savedResources?:number;servers?:readonly {id:string;name:string;acknowledged:boolean}[]}>;
/** A deleted profile is `removed` as soon as the server answers. */
export type ProfileRetirementState='removing'|'removed'|'blocked';
export type ProfileRetirement=Readonly<{profileId:string;lifetime:string;state:ProfileRetirementState}>;
export type ProfileDeletionOptions=Readonly<{signal?:AbortSignal;onStatus?:(retirement:ProfileRetirement)=>void}>;
export type ProfilePolicy=Readonly<{serverId:string;allowedLibraries:readonly string[]|null}>;
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length>0&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
function invalid():never{throw new CodedError('invalid_response',unreadableServerResponse);}
/** Not on the new server yet. The screen shows the refusal it already shows for any other. */
function notYet():never{throw new CodedError('not_available','This is not available on this server yet.');}

/**
 * Reads the account's profiles (`Profile` in the generated contract). A picture colour this app does not know reads as
 * `blue`, the colour a new profile starts with. Where the whole list is relied on (sign-in, the account snapshot) one
 * unreadable profile refuses the list; with `readable`, a list read for display leaves that profile out instead. Either
 * way there is exactly one main profile, no id appears twice and the list is no longer than an account may hold.
 * `position` is the server's order. `revision` and `pinRevision` are always 1: the later save wins on the new server.
 */
export function parseManagedProfiles(raw:unknown,readable=false):readonly ManagedProfile[]{
 if(!Array.isArray(raw)||raw.length<1||raw.length>8)invalid();
 const positions=new Map<unknown,number>(raw.map((value,index)=>[value,index]));
 const row=(v:unknown):ManagedProfile=>{if(!obj(v)||!text(v.id,128)||!text(v.name,120)||typeof v.isPrimary!=='boolean'||!text(v.color,64)||typeof v.pinRequired!=='boolean')invalid();
  return Object.freeze({id:v.id,name:v.name,primary:v.isPrimary,position:positions.get(v)??0,art:oneOfOr('profiles','profile.art',v.color,PROFILE_ART,'blue'),revision:1,pinRevision:1,pinRequired:v.pinRequired});};
 const result:ManagedProfile[]=readable?keepReadable('profiles','rows',raw,row):raw.map(row);
 if(new Set(result.map(v=>v.id)).size!==result.length||result.filter(v=>v.primary).length!==1)invalid();
 return Object.freeze(result);
}
/** Builds what the screens know about an account from the server's `Account` and `Profile` shapes.
 * `role` and `canManage` are this sign-in's: only the account's main profile manages anything. */
function accountSnapshot(serverId:string,account:unknown,profileRows:unknown,role:string,canManage:boolean,readable=false):DirectSnapshot{
 if(!text(serverId,128)||!obj(account)||!text(account.id,128)||!text(account.username,64)||!['owner','admin','member'].includes(account.role as string))invalid();
 const profiles=parseManagedProfiles(profileRows,readable),primary=profiles.find(p=>p.primary);if(!primary)invalid();
 const staff=canManage&&(role==='owner'||role==='admin');
 return Object.freeze({authority:'local',serverId,account:Object.freeze({id:account.id,username:account.username,primaryProfileId:primary.id,role:account.role as DirectAccount['role'],revision:1,disabled:false,allowedLibraries:Object.freeze([])}),
  profiles,capabilities:Object.freeze({manageAccount:canManage,managePeople:staff,manageServer:staff,manageSignIn:canManage&&role==='owner'}),canManage,rememberProfiles:false});
}
/** Reads what `readDirectAccount` fetched (`GET /v1/me` and `GET /v1/profiles`) for the server the caller is signed in to. */
export function parseDirectSnapshot(raw:unknown,expected:Partial<ProfileScope>={},readableProfiles=false):DirectSnapshot{
 if(!obj(raw)||!obj(raw.me)||!obj(raw.profiles)||typeof raw.me.canManageAccount!=='boolean'||!text(raw.me.role,64)||!expected.serverId||(expected.authority&&expected.authority!=='local'))invalid();
 const snapshot=accountSnapshot(expected.serverId,raw.me.account,raw.profiles.items,raw.me.role,raw.me.canManageAccount,readableProfiles);
 if(expected.accountId&&snapshot.account.id!==expected.accountId)invalid();
 return snapshot;
}
/** Reads a sign-in answer (`SignInResponse` in the generated contract). */
export function parseDirectSignIn(raw:unknown):DirectSignIn{
 if(!obj(raw))invalid();
 if(raw.challenge!==undefined){
  const c=raw.challenge;if(!obj(c)||!text(c.token,256)||!currentUTCTimestamp(c.expiresAt))invalid();
  return Object.freeze({kind:'challenge',token:c.token,expiresAt:c.expiresAt});
 }
 if(!obj(raw.session)||!obj(raw.session.viewer)||!obj(raw.account))invalid();
 const session=parseLocalSession(raw.session,{accountId:raw.account.id as string});
 const accountScoped=session.viewer.role==='account';
 const snapshot=accountSnapshot(session.viewer.serverId,raw.account,raw.profiles,session.viewer.role,!accountScoped&&session.viewer.profileId===parseManagedProfiles(raw.profiles).find(p=>p.primary)?.id);
 return Object.freeze({kind:'signed-in',snapshot,session,accountScoped,passwordChangeRequired:false});
}
/** A sign-in answered by a route whose response union is tagged: the server always sends `variant`, one of
 * `known`. A tag outside it is a newer server (`NeedsNewerAppError`); a known tag the sign-in does not match is unreadable. */
export function parseTaggedSignIn(raw:unknown,known:readonly string[],matches:(tag:string,out:DirectSignIn)=>boolean):DirectSignIn{
 const tag=requireTag(raw,'variant',known,invalid);
 const out=parseDirectSignIn(raw);
 if(!matches(tag,out))invalid();
 return out;
}
/** What this install says about itself when it asks for a sign-in (`DeviceInfo`). */
export function signInDevice():DeviceInfo{
 const identity=installationIdentity();
 if(!identity)throw new CodedError('installation_unavailable','This device is not ready to sign in. Reopen the app and try again.');
 return {installationId:identity.installationId,name:identity.name||identity.platform||'Portico',platform:identity.platform,app:identity.app,appVersion:identity.appVersion};
}
/** An account's only profile, when it has no PIN, opens without asking Who's watching. */
async function openOnlyProfile(api:ProfileAPI,result:DirectSignIn,signal?:AbortSignal):Promise<DirectSignIn>{
 if(result.kind!=='signed-in'||!result.accountScoped||result.snapshot.profiles.length!==1||result.snapshot.profiles[0]!.pinRequired)return result;
 const account=result.session,only=result.snapshot.profiles[0]!,scoped=api.withAccessToken?.(account.accessToken);
 if(!scoped)return result;
 const chosen=await selectDirectProfile(scoped,{authority:'local',accountId:result.snapshot.account.id,serverId:result.snapshot.serverId},only.id,{},signal);
 const session=Object.freeze({...chosen.session,...(account.refreshToken?{refreshToken:account.refreshToken}:{})});
 const role=session.viewer.role,snapshot=result.snapshot;
 return Object.freeze({...result,session,accountScoped:false,snapshot:Object.freeze({...snapshot,canManage:true,
  capabilities:Object.freeze({manageAccount:true,managePeople:role==='owner'||role==='admin',manageServer:role==='owner'||role==='admin',manageSignIn:role==='owner'})})});
}
/** `POST /v1/auth/sign-in`. */
export async function directSignIn(api:ProfileAPI,username:string,password:string,signal?:AbortSignal):Promise<DirectSignIn>{
 return openOnlyProfile(api,parseDirectSignIn(await callServer(api,'POST /v1/auth/sign-in',{body:{username,password,device:signInDevice()},signal})),signal);
}
/** Creates the owner of a new server (`POST /v1/setup/owner`) with the setup code the server wrote
 * to its log and to setup-code.txt in its data folder, and opens the owner's main profile. */
export async function completeOwnerSetup(api:ProfileAPI,owner:Readonly<{setupCode:string;username:string;password:string}>,signal?:AbortSignal):Promise<DirectSignIn>{
 return openOnlyProfile(api,parseDirectSignIn(await callServer(api,'POST /v1/setup/owner',{body:{...owner,device:signInDevice()},signal})),signal);
}
/** Finishes a two-step sign-in: the challenge token and an authenticator or recovery code (`POST /v1/auth/two-factor`). */
export async function completeDirectSignInChallenge(api:ProfileAPI,token:string,code:string,signal?:AbortSignal):Promise<DirectSignIn>{
 if(!text(token,256)||!text(code.trim(),64))throw new CodedError('code_required','Enter the code from your authenticator app or a recovery code.');
 const result=parseDirectSignIn(await callServer(api,'POST /v1/auth/two-factor',{body:{challenge:token,code:code.trim()},signal}));
 if(result.kind==='challenge')invalid();
 return openOnlyProfile(api,result,signal);
}
/** Changes the signed-in account's password (`POST /v1/auth/password`). */
export async function changeRequiredPassword(api:ProfileAPI,currentPassword:string,newPassword:string,signal?:AbortSignal):Promise<void>{
 await callServer(api,'POST /v1/auth/password',{body:{currentPassword,newPassword},signal});
}
/**
 * Chooses the profile this sign-in acts for (`POST /v1/profiles/{id}/select`). The answer is the same sign-in with a new
 * access token; the server leaves the refresh token as it was and does not send it again, so whoever saves the answer
 * keeps the one already saved (`rememberNativeSession` does).
 */
export async function selectDirectProfile(api:ProfileAPI,scope:ProfileScope&{serverId:string},profileId:string,input:ProfileSelectionInput,signal?:AbortSignal):Promise<DirectSelection>{
 if(scope.authority!=='local'||!text(profileId,128)||(input.pin!==undefined&&input.pin!==''&&!/^\d{4}$/.test(input.pin)))throw new CodedError('pin_format','Enter the four-digit profile PIN.');
 const raw=await callServer(api,'POST /v1/profiles/{id}/select',{id:profileId,body:input.pin?{pin:input.pin}:{},signal});
 return Object.freeze({session:parseLocalSession(raw,{accountId:scope.accountId,serverId:scope.serverId,profileId})});
}
/** The profiles of an account, managed on its own server from its main profile. */
export class ManagedProfilesClient {
 readonly api:ProfileAPI;readonly scope:ProfileScope;
 constructor(api:ProfileAPI,scope:ProfileScope){if(scope.authority!=='local'||!text(scope.accountId,128)||!text(scope.serverId,128))throw new Error('An account profile scope is required.');this.api=api;this.scope=Object.freeze({...scope});}
 async list(signal?:AbortSignal):Promise<readonly ManagedProfile[]>{return parseManagedProfiles((await callServer(this.api,'GET /v1/profiles',{signal}))?.items,true);}
 async create(name:string,art:ProfileArt='blue',signal?:AbortSignal):Promise<void>{if(!text(name.trim(),120)||!PROFILE_ART.includes(art))throw new Error('Enter a profile name.');await callServer(this.api,'POST /v1/profiles',{body:{name:name.trim(),color:art},signal});}
 /** The later save wins. An empty `pin` removes the PIN. Library access for a profile is not on the new server yet. */
 async edit(profile:ManagedProfile,change:{name?:string;art?:ProfileArt;pin?:string;policy?:ProfilePolicy},signal?:AbortSignal):Promise<void>{
  if(change.pin!==undefined&&change.pin!==''&&!/^\d{4}$/.test(change.pin))throw new Error('Use exactly four numeric digits for the profile PIN.');
  if(change.name!==undefined&&!text(change.name.trim(),120))throw new Error('Enter a profile name.');
  if(change.policy)notYet();
  if(change.name!==undefined||change.art!==undefined)await callServer(this.api,'PATCH /v1/profiles/{id}',{id:profile.id,body:{...(change.name===undefined?{}:{name:change.name.trim()}),...(change.art===undefined?{}:{color:change.art})},signal});
  if(change.pin==='')await callServer(this.api,'DELETE /v1/profiles/{id}/pin',{id:profile.id,signal});
  else if(change.pin!==undefined)await callServer(this.api,'PUT /v1/profiles/{id}/pin',{id:profile.id,body:{pin:change.pin},signal});
 }
 async order(_profiles:readonly ManagedProfile[],_signal?:AbortSignal):Promise<void>{notYet();}
 async forgetTrust(_profileId:string,_signal?:AbortSignal):Promise<void>{notYet();}
 /** The server has no preview of what a deletion removes yet, so the confirmation names the profile only. */
 async previewDelete(profile:ManagedProfile,_signal?:AbortSignal):Promise<ProfileDeletion>{
  if(profile.primary)throw new Error('The primary profile is managed through account settings.');
  return Object.freeze({profileId:profile.id,name:profile.name,revision:1,lifetime:'',primary:false});
 }
 /** Deletes the profile for good and returns its devices to Who's watching (`DELETE /v1/profiles/{id}`). */
 async delete(preview:ProfileDeletion,options:ProfileDeletionOptions={}):Promise<ProfileRetirement>{
  if(preview.primary)throw new Error('Preview this profile deletion first.');
  await callServer(this.api,'DELETE /v1/profiles/{id}',{id:preview.profileId,signal:options.signal});
  const removed:ProfileRetirement=Object.freeze({profileId:preview.profileId,lifetime:preview.lifetime,state:'removed'});
  options.onStatus?.(removed);
  return removed;
 }
}

/** Choosing a profile keeps the same sign-in, so there is never a second one to end. Kept for the
 * areas that still ask (a Portico Account sign-in, a move between sign-in kinds). */
export function shouldRetireAccountSession(account:Pick<LocalSession,'accessToken'|'sessionFamilyId'>|undefined,viewer:Pick<LocalSession,'accessToken'|'sessionFamilyId'>):boolean {
 return !!account && !!account.accessToken && !!account.sessionFamilyId && !!viewer.sessionFamilyId && account.accessToken!==viewer.accessToken && account.sessionFamilyId!==viewer.sessionFamilyId;
}
