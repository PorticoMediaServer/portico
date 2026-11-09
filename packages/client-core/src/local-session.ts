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
/** Current envelope only. There is no missing-family format, metadata synthesis, or migration. */
/** The fields this app needs are checked; a field it does not know is ignored, so a newer server
 * that adds one never locks this app out. Only the known fields are kept.
 * `stored` reads a record this device saved earlier: it may have expired since, which is a
 * reason to sign in again and never a reason the record cannot be read (or replaced).
 * `refreshReceipt` defers access expiry to the refresh owner,
 * which validates the family/bindings/horizon and persists then renews an expired successor. */
export function parseLocalSession(raw:unknown,expected:Partial<Viewer>={},options:{stored?:boolean;refreshReceipt?:boolean}={}):LocalSession {
 function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_current_session'});}
 if(!object(raw)||!text(raw.accessToken,16384)||!currentUTCTimestamp(raw.expiresAt)||(!options.stored&&!options.refreshReceipt&&Date.parse(raw.expiresAt)<=serverNow())||!object(raw.viewer))invalid();
 const viewer=raw.viewer;
 if(!text(viewer.accountId,256)||!text(viewer.profileId,256)||!text(viewer.serverId,256)||!text(viewer.role,256)||!['local','hosted'].includes(viewer.authority as string))invalid();
 for(const [key,value]of Object.entries(expected)){if(viewer[key]!==value)throw new Error('The returned session does not match the selected account, profile and server.');}
 if(typeof raw.sessionFamilyId!=='string'||!/^[A-Za-z0-9_-]{1,128}$/.test(raw.sessionFamilyId)||typeof raw.tokenGeneration!=='string'||!/^[1-9][0-9]{0,18}$/.test(raw.tokenGeneration)||(raw.tokenGeneration.length===19&&raw.tokenGeneration>'9223372036854775807')||!currentUTCTimestamp(raw.authorizationHorizon)||utcOrderKey(raw.expiresAt)>utcOrderKey(raw.authorizationHorizon))invalid();
 // Device-bound sessions (lane C, 939a746): the device record the family is bound to, and over
 // HTTPS a rotating refresh credential (43 base64url characters) the device keeps in protected storage.
 if(raw.deviceId!==undefined&&!text(raw.deviceId,256))invalid();
 // C45: every issued session names the installation it is bound to (persist it for refresh).
 if(raw.installationId!==undefined&&!text(raw.installationId,256))invalid();
 if(raw.refreshToken!==undefined&&(typeof raw.refreshToken!=='string'||!/^[-_A-Za-z0-9]{16,512}$/.test(raw.refreshToken)))invalid();
 if(raw.serverIdentity!==undefined&&(!object(raw.serverIdentity)||!/^[-_A-Za-z0-9]{43}$/.test(String(raw.serverIdentity.publicKey))||!/^[-_A-Za-z0-9]{43}$/.test(String(raw.serverIdentity.fingerprint))))invalid();
 return Object.freeze({...(typeof raw.deviceId==='string'?{deviceId:raw.deviceId}:{}),...(typeof raw.installationId==='string'?{installationId:raw.installationId}:{}),...(typeof raw.refreshToken==='string'?{refreshToken:raw.refreshToken}:{}),...(raw.serverIdentity?{serverIdentity:Object.freeze({publicKey:String((raw.serverIdentity as Record<string,unknown>).publicKey),fingerprint:String((raw.serverIdentity as Record<string,unknown>).fingerprint)})}:{}),accessToken:raw.accessToken,expiresAt:raw.expiresAt,sessionFamilyId:raw.sessionFamilyId,tokenGeneration:raw.tokenGeneration,authorizationHorizon:raw.authorizationHorizon,
  viewer:Object.freeze({accountId:viewer.accountId,profileId:viewer.profileId,serverId:viewer.serverId,authority:viewer.authority as string,role:viewer.role})});
}
