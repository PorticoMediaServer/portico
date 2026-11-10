import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {CodedError,unreadableServerResponse} from './server-messages.ts';
import {callServer} from './server-api.ts';
/** Typed calls for the identity workstream on a direct server: profile restrictions and
 * devices, two-factor. Parsing is strict; these functions add no state. */
import {parseDevice,parseDevices,parseRememberedAccounts,type Device,type RememberedAccount} from './identity-devices.ts';
import {parsePINRecoveryMethods,parseProfileAvatar,parseProfileRestrictions,parseRatingSystems,parseTwoFactorState,restrictionEdit,type PINRecoveryMethods,type ProfileAvatar,type ProfileRestrictions,type RatingSystem,type TwoFactorState} from './identity-restrictions.ts';

export type IdentityApi={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
export type DeviceDescription=Readonly<{installationId:string;name:string;platform:string;app:string;appVersion:string}>;
/** One sign-in of the account on one device. `network` is where it was last used from, never an address. */
export type SignInRecord=Readonly<{id:string;deviceId:string;deviceName:string;profileId?:string;createdAt:string;lastUsedAt:string;expiresAt:string;endedAt?:string;endReason?:string;network?:'home'|'away';current:boolean}>;
export type TwoFactorEnrolment=Readonly<{secret:string;uri:string;recoveryCodes:readonly string[]}>;
const id=(v:string)=>encodeURIComponent(v);

export function parseTwoFactorEnrolment(raw:unknown):TwoFactorEnrolment{
 const v=raw as Record<string,unknown>|null;
 if(!v||typeof v.secret!=='string'||!/^[A-Z2-7]{16,128}$/.test(v.secret)||typeof v.uri!=='string'||!v.uri.startsWith('otpauth://totp/')||v.uri.length>1024)throw new Error(unreadableServerResponse);
 if(!Array.isArray(v.recoveryCodes)||v.recoveryCodes.length>20||v.recoveryCodes.some(c=>typeof c!=='string'||c.length<6||c.length>64))throw new Error(unreadableServerResponse);
 return Object.freeze({secret:v.secret,uri:v.uri,recoveryCodes:Object.freeze([...v.recoveryCodes as string[]])});
}

/** Sends bytes rather than JSON; only a picture upload needs it. The app supplies it because
 * only the app holds the bearer and the platform's way of naming a file's bytes. */
export type IdentityUpload=(path:string,body:Blob|ArrayBuffer|Uint8Array,contentType:string)=>Promise<unknown>;
export const AVATAR_MAX_BYTES=4*1024*1024;

export class IdentityClient{
 private api:IdentityApi;private upload?:IdentityUpload;
 constructor(api:IdentityApi,upload?:IdentityUpload){this.api=api;this.upload=upload;}
 /** The server sends `{items}`; the parser takes the list. */
 async ratingSystems(signal?:AbortSignal):Promise<readonly RatingSystem[]>{const raw=await this.api.request<{items?:unknown}>(routePath("get_rating_systems"),'GET',undefined,signal);return parseRatingSystems(raw?.items);}
 async restrictions(profileId:string,signal?:AbortSignal):Promise<ProfileRestrictions>{return parseProfileRestrictions(await this.api.request<RouteResponse<"get_direct_profile_restrictions">>(encodedRoutePath("get_direct_profile_restrictions", {"id": id(profileId)}),'GET',undefined,signal));}
 /** C45: the signed-in account session manages restrictions and PINs; there is no step-up proof. */
 async saveRestrictions(current:ProfileRestrictions,changes:Parameters<typeof restrictionEdit>[1]):Promise<ProfileRestrictions>{
  return parseProfileRestrictions(await this.api.request<RouteResponse<"put_direct_profile_restrictions">>(encodedRoutePath("put_direct_profile_restrictions", {"id": id(current.profileId)}),'PUT',restrictionEdit(current,changes)));
 }
 async pinRecovery():Promise<PINRecoveryMethods>{return parsePINRecoveryMethods(await this.api.request<RouteResponse<"get_direct_pin_recovery">>(routePath("get_direct_pin_recovery")));}
 async resetPIN(profileId:string,pin:string):Promise<void>{await this.api.request(encodedRoutePath("post_direct_profile_pin_reset", {"id": id(profileId)}),'POST',routeBody("post_direct_profile_pin_reset", {pin}));}
 async removeAvatar(profileId:string):Promise<void>{await this.api.request(encodedRoutePath("delete_direct_profiles_id_avatar", {"id": id(profileId)}),'DELETE');}
 avatar(raw:unknown):ProfileAvatar{return parseProfileAvatar(raw);}
 /** Every profile's current picture version in one read; a profile with none is absent. */
 async avatars(signal?:AbortSignal):Promise<readonly ProfileAvatar[]>{const raw=await this.api.request<{items?:unknown}>(routePath("get_direct_profile_avatars"),'GET',undefined,signal);if(!Array.isArray(raw?.items)||raw.items.length>64)throw new Error(unreadableServerResponse);return Object.freeze(raw.items.map(parseProfileAvatar));}
 /** The server sniffs, crops and re-encodes the picture itself; the type sent here is only a courtesy. */
 async uploadAvatar(profileId:string,picture:Blob|ArrayBuffer|Uint8Array,contentType:string):Promise<ProfileAvatar>{
  if(!this.upload)throw new Error('This device can’t send pictures.');
  const size=picture instanceof Blob?picture.size:picture.byteLength;
  if(!size)throw new Error('That file is empty.');if(size>AVATAR_MAX_BYTES)throw new Error('Choose a picture smaller than 4 MB.');
  return parseProfileAvatar(await this.upload(encodedRoutePath("post_direct_profiles_id_avatar", {"id": id(profileId)}),picture,/^image\/(jpeg|png|webp)$/.test(contentType)?contentType:'application/octet-stream'));
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

 async twoFactor(signal?:AbortSignal):Promise<TwoFactorState>{return parseTwoFactorState(await this.api.request<RouteResponse<"get_direct_two_factor">>(routePath("get_direct_two_factor"),'GET',undefined,signal));}
 async enrolTwoFactor(password:string):Promise<TwoFactorEnrolment>{return parseTwoFactorEnrolment(await this.api.request<RouteResponse<"post_direct_two_factor_enrol">>(routePath("post_direct_two_factor_enrol"),'POST',routeBody("post_direct_two_factor_enrol", {password})));}
 /** C45: confirming enrolment sends the current password with the first code. */
 async verifyTwoFactor(password:string,code:string):Promise<TwoFactorState>{return parseTwoFactorState(await this.api.request<RouteResponse<"post_direct_two_factor_verify">>(routePath("post_direct_two_factor_verify"),'POST',routeBody("post_direct_two_factor_verify", {password,code})));}
 /** Replaces the local account's codes under a current password and factor proof.
  * Only this response contains the new plaintext codes; callers must keep them memory-only. */
 async replaceRecoveryCodes(password:string,code:string,signal?:AbortSignal):Promise<readonly string[]>{
  const raw=await this.api.request<unknown>(routePath("post_direct_two_factor_recovery_codes"),'POST',routeBody("post_direct_two_factor_recovery_codes",{password,code}),signal);
  if(!raw||typeof raw!=='object'||Array.isArray(raw)||Object.keys(raw).some(key=>key!=='codes'))throw new Error(unreadableServerResponse);
  const codes=(raw as {codes?:unknown}).codes;
  if(!Array.isArray(codes)||codes.length<1||codes.length>20||codes.some(value=>typeof value!=='string'||value.length<6||value.length>64||/[\x00-\x20\x7f]/.test(value))||new Set(codes).size!==codes.length)throw new Error(unreadableServerResponse);
  return Object.freeze([...codes]);
 }

 /** C45: turning it off sends the current password and a current code. */
 async disableTwoFactor(password:string,code:string):Promise<void>{await this.api.request(routePath("delete_direct_two_factor"),'DELETE',routeBody("delete_direct_two_factor", {password,code}));}
}

