import {routeBody} from '../../contracts/src/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {parseTaggedSignIn,parseDirectSignIn,type DirectSignIn,type ProfileAPI} from './profile-management.ts';
import {identityCode} from './route-identity.ts';
import {parseTwoFactorState} from './identity-restrictions.ts';

/** Only the original owner-reset request may classify these closed outcomes.
 * Do not infer expiry or a usable reset from status, raw text or another route. */
export type OwnerResetFailurePurpose='request'|'verify';
export type OwnerResetFailure='invalid'|'expired'|'limited'|'write';
export function ownerResetFailure(error:unknown,purpose:OwnerResetFailurePurpose|undefined):OwnerResetFailure|undefined{
 const code=error&&typeof error==='object'?(error as {code?:unknown}).code:undefined;
 if(purpose==='request'&&code==='owner_reset_file_unavailable')return 'write';
 if(purpose==='verify'&&code==='owner_reset_expired')return 'expired';
 if(purpose&&code==='rate_limited')return 'limited';
 if(purpose==='verify'&&code==='unauthorized')return 'invalid';
 return undefined;
}

export type ResetFileLocation=Readonly<{systemKind:'linux'|'windows'|'macos'|'docker';dataDirectory:string;fileDirectory:string;fileName:'reset-code.txt';retryAfterSeconds:number}>;
export function parseResetFileLocation(raw:unknown):ResetFileLocation {
 const v=raw as Record<string,unknown>;
 if(!v||typeof v!=='object'||!['linux','windows','macos','docker'].includes(String(v.systemKind))||typeof v.dataDirectory!=='string'||!v.dataDirectory||typeof v.fileDirectory!=='string'||!v.fileDirectory||v.fileName!=='reset-code.txt'||!Number.isInteger(v.retryAfterSeconds)||Number(v.retryAfterSeconds)<0||Number(v.retryAfterSeconds)>60||'code' in v||'username' in v||'account' in v||'session' in v)throw new Error('The reset file response was invalid.');
 return Object.freeze({systemKind:v.systemKind as ResetFileLocation['systemKind'],dataDirectory:v.dataDirectory,fileDirectory:v.fileDirectory,fileName:'reset-code.txt',retryAfterSeconds:Number(v.retryAfterSeconds)});
}
export async function requestOwnerResetFile(api:ProfileAPI,username?:string,signal?:AbortSignal):Promise<ResetFileLocation>{
 return parseResetFileLocation(await api.request(routePath('post_auth_owner_reset_request'),'POST',routeBody('post_auth_owner_reset_request',username===undefined?{}:{username}),signal));
}
export async function verifyOwnerResetFile(api:ProfileAPI,code:string,turnOffTwoStep=false,signal?:AbortSignal):Promise<DirectSignIn>{
 const out=parseTaggedSignIn(await api.request(routePath('post_auth_owner_reset_verify'),'POST',routeBody('post_auth_owner_reset_verify',{code:code.trim(),turnOffTwoStep}),signal),['password-change','owner-session'],(tag,signedIn)=>signedIn.kind==='signed-in'&&(tag==='owner-session'?signedIn.resetFileOwnerSession===true:signedIn.passwordChangeRequired));
 if(out.kind!=='signed-in'||(!out.passwordChangeRequired&&!out.resetFileOwnerSession)||out.session.refreshToken)throw new Error('The reset file did not return the expected session.');
 return out;
}
export type MemberResetCode=Readonly<{code:string;expiresAt:string;serverId?:string}>;
/** The one-use reset code's exact opaque spelling: one ASCII base64url token,
 * never trimmed, coerced or replaced by a freshly made code. */
const resetCodePattern=/^[A-Za-z0-9_-]{43}$/;
/** `$` can match before a final line terminator, so the exact length is checked
 * separately; controls and separators are then outside the ASCII token. */
const resetCode=(value:unknown):value is string=>typeof value==='string'&&value.length===43&&resetCodePattern.test(value);
function memberResetCode(raw:{code?:unknown;expiresAt?:unknown;serverId?:unknown}):MemberResetCode {
 const code=raw?.code,expiresAt=raw?.expiresAt;
 if(!resetCode(code)||typeof expiresAt!=='string'||!Number.isFinite(Date.parse(expiresAt)))throw new Error('The member reset response was invalid.');
 if(raw.serverId!==undefined){if(typeof raw.serverId!=='string')throw new Error('The member reset response was invalid.');identityCode(raw.serverId);}
 return Object.freeze({code,expiresAt,...(raw.serverId===undefined?{}:{serverId:raw.serverId as string})});
}
export async function makeMemberResetCode(api:ProfileAPI,id:string,expectedRevision:number,turnOffTwoStep=false,signal?:AbortSignal):Promise<MemberResetCode>{
 return memberResetCode(await api.request<{code?:unknown;expiresAt?:unknown;serverId?:unknown}>(routePath('post_direct_members_id_reset_code',{id}),'POST',routeBody('post_direct_members_id_reset_code',{expectedRevision,turnOffTwoStep}),signal));
}
/** Replaces a member's reset code using the original one-use code as the proof.
 * A missing or malformed proof, or an unstated two-step choice, sends nothing:
 * the proof is never trimmed, coerced, replaced by a fresh code or retried. */
export async function replaceMemberResetCode(api:ProfileAPI,id:string,expectedRevision:number,originalResetCode:string,turnOffTwoStep:boolean,signal?:AbortSignal):Promise<MemberResetCode>{
 if(!resetCode(originalResetCode))throw new Error('The original reset code is invalid.');
 if(typeof turnOffTwoStep!=='boolean')throw new Error('The two-step choice is invalid.');
 const body:Readonly<{expectedRevision:number;turnOffTwoStep:boolean;originalResetCode:string}>={expectedRevision,turnOffTwoStep,originalResetCode};
 const raw=await api.request<{code?:unknown;expiresAt?:unknown;serverId?:unknown}>(routePath('post_direct_members_id_reset_code',{id}),'POST',routeBody('post_direct_members_id_reset_code',body),signal);
 return memberResetCode(raw);
}
export async function verifyMemberResetCode(api:ProfileAPI,username:string,code:string,signal?:AbortSignal):Promise<DirectSignIn>{
 const out=parseDirectSignIn(await api.request(routePath('post_direct_password_reset'),'POST',routeBody('post_direct_password_reset',{username,code:code.trim()}),signal));
 if(out.kind!=='signed-in'||!out.passwordChangeRequired||!out.accountScoped||out.session.refreshToken||out.resetFileOwnerSession)throw new Error('The member reset did not return a password-change session.');
 return out;
}
/** The one-hour account session from a reset code may only choose its password.
 * Keeping two-step verification requires its ordinary authenticator/recovery proof. */
export async function changeResetPassword(api:ProfileAPI,newPassword:string,secondFactorCode='',signal?:AbortSignal):Promise<void>{
 await api.request(routePath('post_direct_password'),'POST',routeBody('post_direct_password',{currentPassword:'',newPassword,code:secondFactorCode.trim()}),signal);
}

function ownerPasswordGrant(grant:DirectSignIn){
 if(grant.kind!=='signed-in'||!grant.passwordChangeRequired||!grant.accountScoped||grant.session.refreshToken||grant.snapshot.account.role!=='owner'||grant.resetFileOwnerSession)throw new Error('The owner reset proof is not current.');
 return grant;
}
/** Existing security read, after the one-use file was proved. The API must be
 * bound to that restricted grant; this helper never publishes or replaces it. */
export async function ownerResetSecurity(api:ProfileAPI,grant:DirectSignIn,signal?:AbortSignal):Promise<Readonly<{username:string;twoStepEnabled:boolean}>>{
 const current=ownerPasswordGrant(grant);
 const state=parseTwoFactorState(await api.request(routePath('get_direct_two_factor'),'GET',undefined,signal));
 return Object.freeze({username:current.snapshot.account.username,twoStepEnabled:state.enabled});
}
/** The existing password command commits this choice and the new password in
 * one writer. Fresh sign-in still needs the preserved factor when left on. */
export async function finishOwnerResetPassword(api:ProfileAPI,grant:DirectSignIn,newPassword:string,turnOffTwoStep:boolean,signal?:AbortSignal):Promise<void>{
 ownerPasswordGrant(grant);
 if(typeof turnOffTwoStep!=='boolean')throw new Error('The two-step choice is invalid.');
 const body={currentPassword:'',newPassword,code:'',turnOffTwoStep};
 await api.request(routePath('post_direct_password'),'POST',routeBody('post_direct_password',body),signal);
}
