import {unreadableServerResponse} from './server-messages.ts';
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=8192):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f]/.test(v);
function invalid():never{throw new Error(unreadableServerResponse);}
export type FFmpegValidation=Readonly<{path:string;state:'accepted'|'refused'|'unchecked';inUse:boolean;reasonCode?:string;missingFeatures:readonly string[];checkedAt:string}>;
export function parseFFmpegValidation(v:unknown):FFmpegValidation{
 if(!obj(v)||!text(v.path)||!['accepted','refused','unchecked'].includes(String(v.state))||typeof v.inUse!=='boolean'||!text(v.checkedAt,64)||!Array.isArray(v.missingFeatures)||v.missingFeatures.length>128||!v.missingFeatures.every(x=>text(x,128))||v.reasonCode!==undefined&&!text(v.reasonCode,128))invalid();
 return Object.freeze({path:v.path,state:v.state as FFmpegValidation['state'],inUse:v.inUse,checkedAt:v.checkedAt,missingFeatures:Object.freeze(v.missingFeatures as string[]),...(typeof v.reasonCode==='string'?{reasonCode:v.reasonCode}:{})});
}
