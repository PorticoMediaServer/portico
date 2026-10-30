import {routePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
export type StorageStatus=Readonly<{databasePath:string;cacheDirectory:string;backupDirectory:string;reclaimableBytes:number;lastReclaimedAt?:string;lastReclaimedBytes:number}>;
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=8192):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f]/.test(v);
const count=(v:unknown):v is number=>Number.isSafeInteger(v)&&Number(v)>=0;
function invalid():never{throw new Error(unreadableServerResponse);}
export function parseStorageStatus(v:unknown):StorageStatus{
 if(!obj(v)||!text(v.databasePath)||!text(v.cacheDirectory)||!text(v.backupDirectory)||!count(v.reclaimableBytes)||!count(v.lastReclaimedBytes))invalid();

 return Object.freeze({databasePath:v.databasePath,cacheDirectory:v.cacheDirectory,backupDirectory:v.backupDirectory,reclaimableBytes:v.reclaimableBytes,lastReclaimedBytes:v.lastReclaimedBytes,...(text(v.lastReclaimedAt,64)&&v.lastReclaimedAt?{lastReclaimedAt:v.lastReclaimedAt}:{})});
}
export type FFmpegValidation=Readonly<{path:string;state:'accepted'|'refused'|'unchecked';inUse:boolean;reasonCode?:string;missingFeatures:readonly string[];checkedAt:string}>;
export function parseFFmpegValidation(v:unknown):FFmpegValidation{
 if(!obj(v)||!text(v.path)||!['accepted','refused','unchecked'].includes(String(v.state))||typeof v.inUse!=='boolean'||!text(v.checkedAt,64)||!Array.isArray(v.missingFeatures)||v.missingFeatures.length>128||!v.missingFeatures.every(x=>text(x,128))||v.reasonCode!==undefined&&!text(v.reasonCode,128))invalid();
 return Object.freeze({path:v.path,state:v.state as FFmpegValidation['state'],inUse:v.inUse,checkedAt:v.checkedAt,missingFeatures:Object.freeze(v.missingFeatures as string[]),...(typeof v.reasonCode==='string'?{reasonCode:v.reasonCode}:{})});
}
export function storageStatusService(request:<T>(path:string,method?:string,body?:unknown)=>Promise<T>){return Object.freeze({
 async read(){return parseStorageStatus(await request<unknown>(routePath("get_storage_status")));},

});}
