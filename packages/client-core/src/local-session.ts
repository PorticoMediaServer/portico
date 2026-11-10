import {serverNow} from './server-clock.ts';
import {unreadableServerResponse} from './server-messages.ts';
import type {LocalSession,Viewer} from './index.ts';

const object=(value:unknown):value is Record<string,unknown>=>!!value&&typeof value==='object'&&!Array.isArray(value);
const text=(value:unknown,max:number):value is string=>typeof value==='string'&&value.length>0&&value.length<=max&&!/[\x00-\x1f\x7f]/.test(value);
export function currentUTCTimestamp(value:unknown):value is string {
 if(typeof value!=='string')return false;
 const match=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|\+00:00)$/i.exec(value);
 if(!match)return false;
 const date=new Date(value);
 return Number.isFinite(date.getTime())&&date.getUTCFullYear()===Number(match[1])&&date.getUTCMonth()+1===Number(match[2])&&date.getUTCDate()===Number(match[3])&&date.getUTCHours()===Number(match[4])&&date.getUTCMinutes()===Number(match[5])&&date.getUTCSeconds()===Number(match[6]);
}
export function utcOrderKey(value:string):string {const [whole,fraction='']=value.toUpperCase().replace(/(?:Z|\+00:00)$/,'').split('.');return whole+'.'+fraction.padEnd(9,'0');}
/** Reads a sign-in as the server sends it (`Session` in the generated contract) or as this device
 * saved it. The fields this app needs are checked; a field it does not know is ignored, so a newer
 * server that adds one never locks this app out. Only the known fields are kept.
 * A sign-in that has not chosen a profile yet has the role `account` and an empty `profileId`.
 * `stored` reads a record this device saved earlier: it may have expired since, which is a
 * reason to sign in again and never a reason the record cannot be read (or replaced).
 * `refreshReceipt` leaves access expiry to the renewal, which saves and then renews an expired answer. */
export function parseLocalSession(raw:unknown,expected:Partial<Viewer>={},options:{stored?:boolean;refreshReceipt?:boolean}={}):LocalSession {
 function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_current_session'});}
 if(!object(raw)||!text(raw.accessToken,16384)||!currentUTCTimestamp(raw.expiresAt)||(!options.stored&&!options.refreshReceipt&&Date.parse(raw.expiresAt)<=serverNow())||!object(raw.viewer))invalid();
 const wire=raw.viewer,profileId=wire.profileId??'';
 if(!text(wire.accountId,256)||!text(wire.serverId,256)||!text(wire.role,256)||!(text(profileId,256)||profileId===''&&wire.role==='account'))invalid();
 // Every sign-in on a server is that server's own; `authority` is kept for the screens that show it.
 const viewer={accountId:wire.accountId,profileId:profileId as string,serverId:wire.serverId,authority:'local',role:wire.role};
 for(const [key,value]of Object.entries(expected)){if(viewer[key as keyof Viewer]!==value)throw new Error('The returned session does not match the selected account, profile and server.');}
 // The server calls these `sessionId` and `sessionExpiresAt`; a saved record keeps this app's own names.
 const family=raw.sessionFamilyId??raw.sessionId,horizon=raw.authorizationHorizon??raw.sessionExpiresAt;
 if(typeof family!=='string'||!/^[A-Za-z0-9_-]{1,128}$/.test(family)||typeof raw.tokenGeneration!=='string'||!/^[1-9][0-9]{0,18}$/.test(raw.tokenGeneration)||(raw.tokenGeneration.length===19&&raw.tokenGeneration>'9223372036854775807')||!currentUTCTimestamp(horizon)||utcOrderKey(raw.expiresAt)>utcOrderKey(horizon))invalid();
 if(raw.deviceId!==undefined&&!text(raw.deviceId,256))invalid();
 if(raw.installationId!==undefined&&!text(raw.installationId,256))invalid();
 if(raw.refreshToken!==undefined&&(typeof raw.refreshToken!=='string'||!/^[-_A-Za-z0-9]{16,512}$/.test(raw.refreshToken)))invalid();
 return Object.freeze({...(typeof raw.deviceId==='string'?{deviceId:raw.deviceId}:{}),...(typeof raw.installationId==='string'?{installationId:raw.installationId}:{}),...(typeof raw.refreshToken==='string'?{refreshToken:raw.refreshToken}:{}),accessToken:raw.accessToken,expiresAt:raw.expiresAt,sessionFamilyId:family,tokenGeneration:raw.tokenGeneration,authorizationHorizon:horizon,
  viewer:Object.freeze(viewer)});
}
