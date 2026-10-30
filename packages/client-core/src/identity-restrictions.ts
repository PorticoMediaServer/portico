import {routePrefix} from "../../contracts/src/routes.ts";
import {unreadableServerResponse} from './server-messages.ts';
/** Per-profile content restrictions, device records and the Top Shelf feed.
 *
 * The server owns every visibility decision. A client renders these documents and
 * submits edits; it never decides whether a title is suitable, and it never uses
 * `maximumAge` to hide something the server sent — a title the server returned is
 * a title this profile may see, and one it filtered is simply absent. Enforcing
 * locally would drift from the server rule and would leak the titles it hid.
 *
 * Every parser rejects unknown keys. A field a newer server added must reach a
 * newer client, not be silently dropped into a stale restriction document that a
 * client then submits back as a full replacement. */
export type RatingValue=Readonly<{code:string;label:string;minimumAge:number}>;
export type RatingSystem=Readonly<{id:string;name:string;region:string;values:readonly RatingValue[];screens:readonly string[]}>;
export type ProfileRestrictions=Readonly<{profileId:string;ratingSystem:string;maximumAgeRating:string;maximumAge:number;allowUnrated:boolean;blockedLabels:readonly string[];allowDownloads:boolean;allowLiveTv:boolean;allowDvr:boolean;allowWatchTogether:boolean;revision:number}>;
export type PINRecoveryMethods=Readonly<{password:boolean;authenticator:boolean;recoveryCode:boolean}>;
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

const ratingSystemKeys=['id','name','region','values','screens'] as const;
const ratingValueKeys=['code','label','minimumAge'] as const;

/** Rating tables arrive ordered ascending by admission age. The order is the
 * server's, not the client's: a picker that re-sorted would present a different
 * ceiling from the one the server will enforce. */
export function parseRatingSystems(raw:unknown):readonly RatingSystem[]{
 if(!Array.isArray(raw)||raw.length>32)fail('rating systems');
 return Object.freeze(raw.map(system=>{
  if(!obj(system))fail('rating system');only(system,ratingSystemKeys);
  if(!id(system.id)||!text(system.name,120)||!text(system.region,8))fail('rating system fields');
  if(!Array.isArray(system.values)||system.values.length<1||system.values.length>32)fail('rating values');
  if(!Array.isArray(system.screens)||system.screens.length>16||system.screens.some(s=>!text(s,32)))fail('rating screens');
  let previous=-1;
  const values=system.values.map(value=>{
   if(!obj(value))fail('rating value');only(value,ratingValueKeys);
   if(!text(value.code,32)||value.code.length<1||!text(value.label,160)||!count(value.minimumAge,0,21))fail('rating value fields');
   if(value.minimumAge<previous)fail('rating values out of order');
   previous=value.minimumAge as number;
   return Object.freeze({code:value.code as string,label:value.label as string,minimumAge:value.minimumAge as number});
  });
  return Object.freeze({id:system.id as string,name:system.name as string,region:system.region as string,values:Object.freeze(values),screens:Object.freeze([...system.screens] as string[])});
 }));
}

const restrictionKeys=['profileId','ratingSystem','maximumAgeRating','maximumAge','allowUnrated','blockedLabels','allowDownloads','allowLiveTv','allowDvr','allowWatchTogether','revision'] as const;

/** `maximumAge` is -1 when the profile has no ceiling, and 0 is a real ceiling
 * that admits only all-ages content. A client that treated 0 as "unset" would
 * quietly widen a child profile. */
export function parseProfileRestrictions(raw:unknown):ProfileRestrictions{
 if(!obj(raw))fail('restrictions');only(raw,restrictionKeys);
 if(!id(raw.profileId))fail('profileId');
 if(!text(raw.ratingSystem,32)||!text(raw.maximumAgeRating,32))fail('rating selection');
 if(typeof raw.maximumAge!=='number'||!Number.isInteger(raw.maximumAge)||raw.maximumAge< -1||raw.maximumAge>21)fail('maximumAge');
 if((raw.maximumAgeRating==='')!==(raw.maximumAge===-1))fail('a rating code and its resolved age disagree');
 if(raw.maximumAgeRating!==''&&raw.ratingSystem==='')fail('a rating code without a system');
 if(!Array.isArray(raw.blockedLabels)||raw.blockedLabels.length>64||raw.blockedLabels.some(l=>!text(l,120)||l===''))fail('blockedLabels');
 if(new Set((raw.blockedLabels as string[]).map(l=>l.toLowerCase())).size!==raw.blockedLabels.length)fail('duplicate blockedLabels');
 for(const flag of ['allowUnrated','allowDownloads','allowLiveTv','allowDvr','allowWatchTogether'])if(!bool(raw[flag]))fail(flag);
 if(!count(raw.revision,1))fail('revision');
 return Object.freeze({profileId:raw.profileId as string,ratingSystem:raw.ratingSystem as string,maximumAgeRating:raw.maximumAgeRating as string,maximumAge:raw.maximumAge as number,allowUnrated:raw.allowUnrated as boolean,blockedLabels:Object.freeze([...raw.blockedLabels] as string[]),allowDownloads:raw.allowDownloads as boolean,allowLiveTv:raw.allowLiveTv as boolean,allowDvr:raw.allowDvr as boolean,allowWatchTogether:raw.allowWatchTogether as boolean,revision:raw.revision as number});
}

/** A restriction edit is a full replacement guarded by the revision that was
 * read. Building it from a parsed document is the only supported path: a patch
 * assembled from loose fields could drop a flag a newer server added. */
export function restrictionEdit(current:ProfileRestrictions,changes:Partial<Omit<ProfileRestrictions,'profileId'|'revision'|'maximumAge'>>):Record<string,unknown>{
 const next={...current,...changes};
 if(next.maximumAgeRating!==''&&next.ratingSystem==='')fail('a rating code without a system');
 return Object.freeze({expectedRevision:current.revision,ratingSystem:next.ratingSystem,maximumAgeRating:next.maximumAgeRating,allowUnrated:next.allowUnrated,blockedLabels:[...next.blockedLabels],allowDownloads:next.allowDownloads,allowLiveTv:next.allowLiveTv,allowDvr:next.allowDvr,allowWatchTogether:next.allowWatchTogether});
}

export function parsePINRecoveryMethods(raw:unknown):PINRecoveryMethods{
 if(!obj(raw))fail('pin recovery');only(raw,['password','authenticator','recoveryCode','emailedToken']);
 // C59: servers no longer offer emailed PIN recovery; an older server's emailedToken is accepted and ignored.
 for(const flag of ['password','authenticator','recoveryCode'])if(!bool(raw[flag]))fail(flag);
 if(raw.emailedToken!==undefined&&!bool(raw.emailedToken))fail('emailedToken');
 return Object.freeze({password:raw.password as boolean,authenticator:raw.authenticator as boolean,recoveryCode:raw.recoveryCode as boolean});
}

export function parseProfileAvatar(raw:unknown):ProfileAvatar{
 if(!obj(raw))fail('avatar');only(raw,['profileId','version','updatedAt','sourceMime','url']);
 if(!id(raw.profileId)||!count(raw.version,1)||!instant(raw.updatedAt))fail('avatar fields');
 if(raw.sourceMime!=='image/jpeg'&&raw.sourceMime!=='image/png'&&raw.sourceMime!=='image/webp')fail('sourceMime');
 // The URL must be a server-relative path. An absolute URL here would let a
 // compromised field point a profile picture at another origin.
 if(typeof raw.url!=='string'||!raw.url.startsWith(routePrefix("get_profiles_id_avatar", 2))||raw.url.length>512)fail('url');
 return Object.freeze({profileId:raw.profileId as string,version:raw.version as number,updatedAt:raw.updatedAt as string,sourceMime:raw.sourceMime,url:raw.url});
}

export function parseTwoFactorState(raw:unknown):TwoFactorState{
 if(!obj(raw))fail('two-factor state');only(raw,['enabled','pendingEnrolment','recoveryCodesRemaining']);
 if(!bool(raw.enabled)||!bool(raw.pendingEnrolment)||!count(raw.recoveryCodesRemaining,0,64))fail('two-factor fields');
 if(raw.enabled&&raw.pendingEnrolment)fail('a factor cannot be both in force and pending');
 return Object.freeze({enabled:raw.enabled as boolean,pendingEnrolment:raw.pendingEnrolment as boolean,recoveryCodesRemaining:raw.recoveryCodesRemaining as number});
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
