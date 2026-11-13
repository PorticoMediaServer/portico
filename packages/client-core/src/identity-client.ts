import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {callServer,serverPath} from './query/server-api.ts';
/** Typed calls for the identity workstream on a direct server: profile limits and
 * devices, two-step sign-in. These functions add no state. */
import {parseDevice,parseDevices,parseRememberedAccounts,type Device,type RememberedAccount} from './identity-devices.ts';
import {parseProfileAvatar,profileAvatarFromProfile,parseTwoFactorState,type ProfileAvatar,type TwoFactorState} from './identity-restrictions.ts';
import {RATING_LIMITS,readRestrictions,saveRestrictions,tagChoices,type ProfileRestrictions,type RatingSystem,type RestrictionChanges,type TagChoice} from './profile-restrictions.ts';

export type IdentityApi={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
export type DeviceDescription=Readonly<{installationId:string;name:string;platform:string;app:string;appVersion:string}>;
/** One sign-in of the account on one device. `network` is where it was last used from, never an address. */
export type SignInRecord=Readonly<{id:string;deviceId:string;deviceName:string;profileId?:string;createdAt:string;lastUsedAt:string;expiresAt:string;endedAt?:string;endReason?:string;network?:'home'|'away';current:boolean}>;
/** What an authenticator app is given. The recovery codes come when the first code is confirmed. */
export type TwoFactorEnrolment=Readonly<{secret:string;uri:string}>;
const id=(v:string)=>encodeURIComponent(v);

export function parseTwoFactorEnrolment(raw:unknown):TwoFactorEnrolment{
 const v=raw as Record<string,unknown>|null;
 if(!v||typeof v.secret!=='string'||!v.secret||v.secret.length>256||typeof v.uri!=='string'||!v.uri.startsWith('otpauth://totp/')||v.uri.length>1024)throw new Error(unreadableServerResponse);
 return Object.freeze({secret:v.secret,uri:v.uri});
}
/** The ten single-use recovery codes, shown once (`RecoveryCodes`). */
function recoveryCodes(raw:unknown):readonly string[]{
 const codes=(raw as {recoveryCodes?:unknown}|null)?.recoveryCodes;
 if(!Array.isArray(codes)||codes.length<1||codes.length>20||codes.some(value=>typeof value!=='string'||value.length<6||value.length>64||/[\x00-\x20\x7f]/.test(value))||new Set(codes).size!==codes.length)throw new Error(unreadableServerResponse);
 return Object.freeze([...codes as string[]]);
}

/** Sends bytes rather than JSON; only a picture upload needs it. The app supplies it because
 * only the app holds the bearer and the platform's way of naming a file's bytes. */
export type IdentityUpload=(path:string,body:Blob|ArrayBuffer|Uint8Array,contentType:string)=>Promise<unknown>;
export const AVATAR_MAX_BYTES=4*1024*1024;

/** Both own-account and managed-member uploads use the same binary profile route. */
export async function uploadProfilePicture(upload:IdentityUpload|undefined,profileId:string,picture:Blob|ArrayBuffer|Uint8Array,contentType:string,member?:string):Promise<ProfileAvatar>{
 if(!upload)throw new Error('This device can’t send pictures.');
 const size=picture instanceof Blob?picture.size:picture.byteLength;
 if(!size)throw new Error('That file is empty.');if(size>AVATAR_MAX_BYTES)throw Object.assign(new CodedError('body_too_large','A profile picture can be at most 4 MB.'),{status:413});
 const raw=await upload(serverPath('PUT /v1/profiles/{id}/avatar',{id:profileId,query:{member}}),picture,/^image\/(jpeg|png|gif|webp)$/.test(contentType)?contentType:'application/octet-stream');
 const avatar=profileAvatarFromProfile(raw,member);
 if(!avatar||avatar.profileId!==profileId)throw new CodedError('invalid_response',unreadableServerResponse);
 return avatar;
}

export class IdentityClient{
 private api:IdentityApi;private upload?:IdentityUpload;
 constructor(api:IdentityApi,upload?:IdentityUpload){this.api=api;this.upload=upload;}
 /** The highest-rating choices. The server keeps a limit as an age, so these are fixed and need no request. */
 async ratingSystems(_signal?:AbortSignal):Promise<readonly RatingSystem[]>{return Object.freeze([RATING_LIMITS]);}
 async restrictions(profileId:string,signal?:AbortSignal):Promise<ProfileRestrictions>{return readRestrictions(this.api,profileId,undefined,signal);}
 /** The Tags a profile can have blocked, by name. */
 async tags(signal?:AbortSignal):Promise<readonly TagChoice[]>{return tagChoices(this.api,signal);}
 /** Saves the limits as read with these changes (`PUT /v1/profiles/{id}/restrictions`); the later save wins. */
 async saveRestrictions(current:ProfileRestrictions,changes:RestrictionChanges):Promise<ProfileRestrictions>{return saveRestrictions(this.api,current,changes);}
 /** Forgot PIN: the account's password (and two-step code when that is on) sets a new PIN from any profile
  * (`PUT /v1/profiles/{id}/pin`). Without them, only the main profile may change a PIN. */
 async resetPIN(profileId:string,pin:string,proof?:Readonly<{currentPassword:string;code?:string}>,signal?:AbortSignal):Promise<void>{
  await callServer(this.api,'PUT /v1/profiles/{id}/pin',{id:profileId,body:{pin,...(proof?{currentPassword:proof.currentPassword,...(proof.code?{code:proof.code}:{})}:{})},signal});
 }
 /** A Portico PIN recovery uses only Services' password-checked note, never the password itself. */
 async resetPorticoPIN(profileId:string,pin:string,proof:Readonly<{challenge:string;identity:unknown}>,signal?:AbortSignal):Promise<void>{
  await callServer(this.api,'PUT /v1/profiles/{id}/pin',{id:profileId,body:{pin,challenge:proof.challenge,identity:proof.identity},signal});
 }
 async removeAvatar(profileId:string):Promise<void>{await callServer(this.api,'DELETE /v1/profiles/{id}/avatar',{id:profileId});}
 avatar(raw:unknown):ProfileAvatar{return parseProfileAvatar(raw);}
 /** Pictures already named by the account's profile list; absent pictures stay absent.
  * The rebuilt server gives a versioned URL, not the old upload receipt's metadata. */
 async avatars(signal?:AbortSignal):Promise<readonly ProfileAvatar[]>{
  const raw=await callServer(this.api,'GET /v1/profiles',{signal});
  if(!Array.isArray(raw?.items)||raw.items.length>64)throw new CodedError('invalid_response',unreadableServerResponse);
  return Object.freeze(raw.items.flatMap((profile:unknown)=>{const avatar=profileAvatarFromProfile(profile);return avatar?[avatar]:[];}));
 }
 /** The server sniffs, crops and re-encodes the picture itself; the type sent here is only a courtesy. */
 async uploadAvatar(profileId:string,picture:Blob|ArrayBuffer|Uint8Array,contentType:string):Promise<ProfileAvatar>{
  return uploadProfilePicture(this.upload,profileId,picture,contentType);
 }

 /** The account's signed-in devices (`GET /v1/devices`), most recently used first. */
 async devices(_installationId:string,signal?:AbortSignal):Promise<readonly Device[]>{
  return parseDevices(await callServer(this.api,'GET /v1/devices',{signal}));
 }
 /** The account's sign-ins, ended ones included for as long as the server lists them (`GET /v1/me/sessions`). */
 async sessions(signal?:AbortSignal):Promise<readonly SignInRecord[]>{
  const raw=await callServer(this.api,'GET /v1/me/sessions',{signal});
  if(!Array.isArray(raw?.items))throw new Error(unreadableServerResponse);
  return Object.freeze(raw.items.filter(row=>row&&typeof row.id==='string'&&typeof row.deviceName==='string').map(row=>Object.freeze({id:row.id,deviceId:row.deviceId,deviceName:row.deviceName,createdAt:row.createdAt,lastUsedAt:row.lastUsedAt,expiresAt:row.expiresAt,current:row.current===true,
   ...(row.profileId?{profileId:row.profileId}:{}),...(row.endedAt?{endedAt:row.endedAt}:{}),...(row.endReason?{endReason:row.endReason}:{}),...(row.network==='home'||row.network==='away'?{network:row.network}:{})})));
 }
 /** Ends one of the account's sign-ins (`DELETE /v1/me/sessions/{id}`). */
 async endSession(sessionId:string,signal?:AbortSignal):Promise<void>{await callServer(this.api,'DELETE /v1/me/sessions/{id}',{id:sessionId,signal});}
 /** Who has used this installation. Asked before anyone is signed in; nothing in it is a credential. */
 /** The installation id goes in the body, never the address: addresses end up in proxy and access logs. */
 async rememberedAccounts(installationId:string,signal?:AbortSignal):Promise<readonly RememberedAccount[]>{return parseRememberedAccounts(await this.api.request<RouteResponse<"post_auth_remembered_accounts">>(routePath("post_auth_remembered_accounts"),'POST',routeBody("post_auth_remembered_accounts", {installationId}),signal));}
 async rememberAccount(installationId:string,automaticSignIn:boolean):Promise<readonly RememberedAccount[]>{return parseRememberedAccounts(await this.api.request<RouteResponse<"post_direct_remembered_accounts">>(routePath("post_direct_remembered_accounts"),'POST',routeBody("post_direct_remembered_accounts", {installationId,automaticSignIn})));}
 async forgetAccount(accountId:string,installationId:string):Promise<void>{await this.api.request(encodedRoutePath("delete_direct_remembered_account", {"accountId": id(accountId)}),'DELETE',routeBody("delete_direct_remembered_account", {installationId}));}
 /** Renames a device (`PATCH /v1/devices/{id}`). Remembering an account on a device is not on the new server yet. */
 async editDevice(deviceId:string,edit:{name?:string;rememberAccount?:boolean}):Promise<Device>{
  if(edit.name===undefined)throw new CodedError('not_available','This is not available on this server yet.');
  return parseDevice(await callServer(this.api,'PATCH /v1/devices/{id}',{id:deviceId,body:{name:edit.name}}));
 }
 /** Signs a device out and takes it off the list (`DELETE /v1/devices/{id}`): the server does both in one step. */
 async signOutDevice(deviceId:string):Promise<void>{await callServer(this.api,'DELETE /v1/devices/{id}',{id:deviceId});}
 async removeDevice(deviceId:string):Promise<void>{await callServer(this.api,'DELETE /v1/devices/{id}',{id:deviceId});}
 /** Signs the account out everywhere except on this device (`DELETE /v1/me/sessions`). */
 async signOutEverywhere():Promise<void>{await callServer(this.api,'DELETE /v1/me/sessions');}
 /** Changes the account's password: the current password always, plus `code` (authenticator or
  * recovery code) when two-step sign-in is on. The account's other devices are signed out. */
 async changePassword(currentPassword:string,newPassword:string,code?:string):Promise<void>{await callServer(this.api,'POST /v1/auth/password',{body:{currentPassword,newPassword,...(code?{code}:{})}});}

 /** Whether two-step sign-in is on and how many recovery codes are left (`GET /v1/me/two-factor`). */
 async twoFactor(signal?:AbortSignal):Promise<TwoFactorState>{return parseTwoFactorState(await callServer(this.api,'GET /v1/me/two-factor',{signal}));}
 /** Starts turning two-step on with the current password (`POST /v1/me/two-factor`). */
 async enrolTwoFactor(password:string):Promise<TwoFactorEnrolment>{return parseTwoFactorEnrolment(await callServer(this.api,'POST /v1/me/two-factor',{body:{currentPassword:password}}));}
 /** Turns two-step on with the first code from the authenticator (`POST /v1/me/two-factor/confirm`). The answer
  * holds the recovery codes, shown once; callers keep them in memory only. `password` was proved when it started. */
 async verifyTwoFactor(_password:string,code:string):Promise<TwoFactorState&{recoveryCodes:readonly string[]}>{
  const codes=recoveryCodes(await callServer(this.api,'POST /v1/me/two-factor/confirm',{body:{code:code.trim()}}));
  return Object.freeze({enabled:true,pendingEnrolment:false,recoveryCodesRemaining:codes.length,recoveryCodes:codes});
 }
 /** Replaces the recovery codes with the current password and a current code (`POST /v1/me/two-factor/recovery-codes`).
  * Only this answer holds the new codes; callers keep them in memory only. */
 async replaceRecoveryCodes(password:string,code:string,signal?:AbortSignal):Promise<readonly string[]>{
  return recoveryCodes(await callServer(this.api,'POST /v1/me/two-factor/recovery-codes',{body:{currentPassword:password,code:code.trim()},signal}));
 }
 /** Turns two-step off with the current password and a current code (`DELETE /v1/me/two-factor`). */
 async disableTwoFactor(password:string,code:string):Promise<void>{await callServer(this.api,'DELETE /v1/me/two-factor',{body:{currentPassword:password,code:code.trim()}});}
}
