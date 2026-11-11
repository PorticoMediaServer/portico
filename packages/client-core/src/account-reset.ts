import {routeBody} from '../../contracts/src/routes.ts';
import {routePath} from '../../contracts/src/route-paths.ts';
import {parseTaggedSignIn,parseDirectSignIn,type DirectSignIn,type ProfileAPI} from './profile-management.ts';
import {serverPath} from './query/server-api.ts';
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

/** The reset file's public answer (O-169): its fixed name and how long until another can be
 * written. The server no longer publishes its operating system or data directory, so no
 * client claims an exact path it was not given. */
export type ResetFileLocation=Readonly<{fileName:'reset-code.txt';retryAfterSeconds:number}>;
export function parseResetFileLocation(raw:unknown):ResetFileLocation {
 const v=raw as Record<string,unknown>;
 if(!v||typeof v!=='object'||v.fileName!=='reset-code.txt'||!Number.isInteger(v.retryAfterSeconds)||Number(v.retryAfterSeconds)<0||Number(v.retryAfterSeconds)>60||'code' in v||'username' in v||'account' in v||'session' in v)throw new Error('The reset file response was invalid.');
 return Object.freeze({fileName:'reset-code.txt',retryAfterSeconds:Number(v.retryAfterSeconds)});
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
/** The rebuilt server's reset code, as the member types it: three groups of four, such as K7QP-2MXD-9HRT. */
const resetCode=(value:unknown):value is string=>typeof value==='string'&&/^[0-9A-Z]{4}-[0-9A-Z]{4}-[0-9A-Z]{4}$/.test(value);
function memberResetCode(raw:{code?:unknown;expiresAt?:unknown}):MemberResetCode {
 const code=raw?.code,expiresAt=raw?.expiresAt;
 if(!resetCode(code)||typeof expiresAt!=='string'||!Number.isFinite(Date.parse(expiresAt)))throw new Error('The member reset response was invalid.');
 return Object.freeze({code,expiresAt});
}
/** Makes a reset code for a member. On the rebuilt server this also stops their password at once and signs them out
 * everywhere, until they choose a new one with the code. Making another replaces the one before. */
export async function makeMemberResetCode(api:ProfileAPI,id:string,_expectedRevision:number,turnOffTwoStep=false,signal?:AbortSignal):Promise<MemberResetCode>{
 return memberResetCode(await api.request<{code?:unknown;expiresAt?:unknown}>(serverPath('POST /v1/admin/members/{id}/password-reset',{id}),'POST',{resetTwoFactor:turnOffTwoStep},signal));
}
/** Replaces a member's reset code when the two-step choice changes. The code shown is the proof that one was made: a
 * missing or malformed one, or an unstated two-step choice, sends nothing. The server replaces the old code. */
export async function replaceMemberResetCode(api:ProfileAPI,id:string,expectedRevision:number,originalResetCode:string,turnOffTwoStep:boolean,signal?:AbortSignal):Promise<MemberResetCode>{
 if(!resetCode(originalResetCode))throw new Error('The original reset code is invalid.');
 if(typeof turnOffTwoStep!=='boolean')throw new Error('The two-step choice is invalid.');
 return makeMemberResetCode(api,id,expectedRevision,turnOffTwoStep,signal);
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
