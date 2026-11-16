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
/** The rebuilt server's answer (`fileName`, `retryAfterMs`). It is the same whatever username was typed. */
export function parseResetFileLocation(raw:unknown):ResetFileLocation {
 const v=raw as Record<string,unknown>;
 if(!v||typeof v!=='object'||v.fileName!=='reset-code.txt'||!Number.isInteger(v.retryAfterMs)||Number(v.retryAfterMs)<0||Number(v.retryAfterMs)>60_000||'code' in v||'username' in v||'account' in v||'session' in v)throw new Error('The reset file response was invalid.');
 return Object.freeze({fileName:'reset-code.txt',retryAfterSeconds:Math.ceil(Number(v.retryAfterMs)/1000)});
}
/** Asks the server to write the owner's one-time code to `reset-code.txt` in its data folder (definition 01 rule 40,
 * `POST /v1/auth/owner-reset`). Anyone at the sign-in page may ask; the page is never shown the code. */
export async function requestOwnerResetFile(api:ProfileAPI,username?:string,signal?:AbortSignal):Promise<ResetFileLocation>{
 return parseResetFileLocation(await api.request(serverPath('POST /v1/auth/owner-reset'),'POST',username===undefined?{}:{username},signal));
}
/** Chooses the owner's new password with the code from the file (`POST /v1/auth/owner-reset/confirm`): once, within
 * 15 minutes, five wrong codes cancel it. `turnOffTwoStep` also turns the owner's two-step sign-in off. It signs
 * nobody in, and the server signs every device of the owner out. */
export async function finishOwnerReset(api:ProfileAPI,code:string,newPassword:string,turnOffTwoStep:boolean,signal?:AbortSignal):Promise<void>{
 await api.request(serverPath('POST /v1/auth/owner-reset/confirm'),'POST',{code:code.trim(),newPassword,...(turnOffTwoStep?{turnOffTwoStep:true}:{})},signal);
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
/** A member chooses a new password with the reset code the owner or an administrator gave them
 * (`POST /v1/auth/password-reset/confirm`). It signs nobody in: the person then signs in with the new password. A
 * wrong code, a used one and a username nobody has are the same refusal, and five wrong codes cancel the code. */
export async function redeemMemberResetCode(api:ProfileAPI,username:string,code:string,newPassword:string,signal?:AbortSignal):Promise<void>{
 await api.request(serverPath('POST /v1/auth/password-reset/confirm'),'POST',{username:username.trim(),code:code.trim(),newPassword},signal);
}
