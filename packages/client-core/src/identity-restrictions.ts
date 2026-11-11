import {routePrefix} from "../../contracts/src/routes.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Profile pictures, two-step state and the pre-sign-in descriptor. A profile's limits are in
 * profile-restrictions.ts; the server decides what a profile sees and a client never filters for it. */
export type {RatingValue,RatingSystem,ProfileRestrictions} from './profile-restrictions.ts';
export type ProfileAvatar=Readonly<{profileId:string;version:number;updatedAt:string;sourceMime:string;url:string}>;
export type TwoFactorState=Readonly<{enabled:boolean;pendingEnrolment:boolean;recoveryCodesRemaining:number}>;
export type AuthCapabilities=Readonly<{serverId:string;serverName:string;setupRequired:boolean;methods:readonly string[];selfRegistration:'off'|'invite-only'|'open';quickConnect:boolean;twoFactor:boolean;hostedAttach:boolean}>;

const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=256):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=128&&/^[A-Za-z0-9_-]+$/.test(v);
const bool=(v:unknown):v is boolean=>typeof v==='boolean';
const count=(v:unknown,min=0,max=1e6):v is number=>typeof v==='number'&&Number.isInteger(v)&&v>=min&&v<=max;
const instant=(v:unknown):v is string=>typeof v==='string'&&v.length<=40&&!Number.isNaN(Date.parse(v));
const only=(v:Record<string,unknown>,keys:readonly string[]):void=>{if(Object.keys(v).some(k=>!keys.includes(k)))fail('unexpected field');};
// What a person reads says what happened to them; `code` and `detail` keep the
// technical shape for diagnostics. "Invalid identity document" told a viewer
// nothing they could act on.
function fail(reason:string):never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_identity_document',detail:reason,retryable:false});}

export function parseProfileAvatar(raw:unknown):ProfileAvatar{
 if(!obj(raw))fail('avatar');only(raw,['profileId','version','updatedAt','sourceMime','url']);
 if(!id(raw.profileId)||!count(raw.version,1)||!instant(raw.updatedAt))fail('avatar fields');
 if(raw.sourceMime!=='image/jpeg'&&raw.sourceMime!=='image/png'&&raw.sourceMime!=='image/webp')fail('sourceMime');
 // The URL must be a server-relative path. An absolute URL here would let a
 // compromised field point a profile picture at another origin.
 if(typeof raw.url!=='string'||!raw.url.startsWith(routePrefix("get_profiles_id_avatar", 2))||raw.url.length>512)fail('url');
 return Object.freeze({profileId:raw.profileId as string,version:raw.version as number,updatedAt:raw.updatedAt as string,sourceMime:raw.sourceMime,url:raw.url});
}

/** `TwoFactorState` on the wire: `enabled` and `recoveryCodesLeft`. A setup started and not confirmed reads as off. */
export function parseTwoFactorState(raw:unknown):TwoFactorState{
 if(!obj(raw)||!bool(raw.enabled)||!count(raw.recoveryCodesLeft,0,64))fail('two-factor state');
 return Object.freeze({enabled:raw.enabled as boolean,pendingEnrolment:false,recoveryCodesRemaining:raw.recoveryCodesLeft as number});
}

const registrationModes=['off','invite-only','open'] as const;

/** The pre-authentication descriptor. A client shows only the methods listed
 * here, so a server that does not offer something never grows a dead button. */
export function parseAuthCapabilities(raw:unknown):AuthCapabilities{
 if(!obj(raw))fail('capabilities');only(raw,['serverId','serverName','setupRequired','methods','selfRegistration','quickConnect','twoFactor','hostedAttach']);
 if(!id(raw.serverId)||!text(raw.serverName,120)||!bool(raw.setupRequired))fail('capability fields');
 if(!Array.isArray(raw.methods)||raw.methods.length>8||raw.methods.some(m=>!text(m,32)||m===''))fail('methods');
 if(new Set(raw.methods as string[]).size!==raw.methods.length)fail('duplicate methods');
 if(typeof raw.selfRegistration!=='string'||!registrationModes.includes(raw.selfRegistration as typeof registrationModes[number]))fail('selfRegistration');
 for(const flag of ['quickConnect','twoFactor','hostedAttach'])if(!bool(raw[flag]))fail(flag);
 return Object.freeze({serverId:raw.serverId as string,serverName:raw.serverName as string,setupRequired:raw.setupRequired as boolean,methods:Object.freeze([...raw.methods] as string[]),selfRegistration:raw.selfRegistration as AuthCapabilities['selfRegistration'],quickConnect:raw.quickConnect as boolean,twoFactor:raw.twoFactor as boolean,hostedAttach:raw.hostedAttach as boolean});
}
