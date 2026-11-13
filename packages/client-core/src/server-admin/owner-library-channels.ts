import type {LibraryContentApi} from '../library-content.ts';
import {callServer,serverPath} from '../query/server-api.ts';
import {randomId} from '../random-id.ts';
import {unreadableServerResponse} from '../server-messages.ts';
import type {LibraryChannel,LibraryChannelBlock,LibraryChannelSave,LibraryChannelBuilder,LibraryChannelSelection,LibraryChannelCondition,LibraryChannelBlockPreset,LibraryChannelBlockPresetSave} from '../../../contracts/src/next/api.ts';
export type {LibraryChannel,LibraryChannelBlock,LibraryChannelSave,LibraryChannelBuilder,LibraryChannelSelection,LibraryChannelCondition,LibraryChannelBlockPreset,LibraryChannelBlockPresetSave};
export const newChannelOperation=randomId;
function channel(value:LibraryChannel){if(!value||!value.id||typeof value.name!=='string'||!/^\d+$/.test(value.revision))throw new Error(unreadableServerResponse);return value;}
async function change<T>(api:LibraryContentApi,path:string,method:'POST'|'PATCH'|'PUT'|'DELETE',body:unknown,signal?:AbortSignal,headers?:Record<string,string>):Promise<T>{
 if(!api.requestRaw)throw new Error(unreadableServerResponse);
 const answer=await api.requestRaw(path,method,{body,headers,signal,singleAttempt:true});
 if(answer.status>=400){const error=(answer.body as {error?:{code?:string;message?:string;field?:string}})?.error;throw {status:answer.status,code:error?.code,message:error?.message,field:error?.field};}
 return answer.body as T;
}
export async function readOwnerChannels(api:LibraryContentApi,cursor?:string,signal?:AbortSignal){const page=await callServer(api,'GET /v1/admin/library-channels',{query:{cursor},signal});return {...page,items:page.items.map(channel)};}
export async function readOwnerChannel(api:LibraryContentApi,id:string,signal?:AbortSignal){const result=channel(await callServer(api,'GET /v1/admin/library-channels/{id}',{id,signal}));if(!result.builder||result.id!==id)throw new Error(unreadableServerResponse);return result;}
export async function readChannelChoices(api:LibraryContentApi,signal?:AbortSignal){const result=await callServer(api,'GET /v1/admin/library-channels/templates',{signal});if(!Array.isArray(result.items)||typeof result.timeZone!=='string'||!result.timeZone||typeof result.moodAvailable!=='boolean')throw new Error(unreadableServerResponse);return result;}
export async function saveOwnerChannel(api:LibraryContentApi,body:LibraryChannelSave,existing?:LibraryChannel,operation?:string,signal?:AbortSignal){return channel(await change<LibraryChannel>(api,existing?serverPath('PATCH /v1/admin/library-channels/{id}',{id:existing.id}):serverPath('POST /v1/admin/library-channels'),existing?'PATCH':'POST',body,signal,existing?{'If-Match':`"${existing.revision}"`}:{'Idempotency-Key':operation!}));}
export const previewOwnerChannel=(api:LibraryContentApi,body:LibraryChannelSave,id?:string,signal?:AbortSignal)=>callServer(api,'POST /v1/admin/library-channels/preview',{body:{channelId:id,timeZone:body.timeZone!,builder:body.builder!},signal});
export const removeOwnerChannel=(api:LibraryContentApi,row:LibraryChannel,signal?:AbortSignal)=>change(api,serverPath('DELETE /v1/admin/library-channels/{id}',{id:row.id}),'DELETE',undefined,signal,{'If-Match':`"${row.revision}"`});
export const regenerateOwnerChannel=(api:LibraryContentApi,row:LibraryChannel,signal?:AbortSignal)=>callServer(api,'POST /v1/admin/library-channels/{id}/regenerate',{id:row.id,signal});
export const orderOwnerChannels=(api:LibraryContentApi,ids:string[],signal?:AbortSignal)=>callServer(api,'PUT /v1/admin/library-channels/order',{body:{ids},signal});
export async function readOwnerBlockPresets(api:LibraryContentApi,cursor?:string,signal?:AbortSignal){const result=await callServer(api,'GET /v1/admin/block-presets',{query:{cursor},signal});if(!Array.isArray(result.items)||result.items.some(row=>!row.id||!row.recipe||!/^\d+$/.test(row.revision)))throw new Error(unreadableServerResponse);return result;}
export const saveOwnerBlockPreset=(api:LibraryContentApi,body:LibraryChannelBlockPresetSave,existing?:LibraryChannelBlockPreset,operation?:string,signal?:AbortSignal)=>change<LibraryChannelBlockPreset>(api,existing?serverPath('PATCH /v1/admin/block-presets/{id}',{id:existing.id}):serverPath('POST /v1/admin/block-presets'),existing?'PATCH':'POST',body,signal,existing?{'If-Match':`"${existing.revision}"`}:{'Idempotency-Key':operation!});
export const removeOwnerBlockPreset=(api:LibraryContentApi,row:LibraryChannelBlockPreset,signal?:AbortSignal)=>change(api,serverPath('DELETE /v1/admin/block-presets/{id}',{id:row.id}),'DELETE',undefined,signal,{'If-Match':`"${row.revision}"`});
export const removeOwnerChannelLogo=(api:LibraryContentApi,id:string,signal?:AbortSignal)=>callServer(api,'DELETE /v1/admin/library-channels/{id}/logo',{id,signal});
export const readChannelLibrary=(api:LibraryContentApi,id:string,signal?:AbortSignal)=>callServer(api,'GET /v1/libraries/{id}',{id,signal});
export const readChannelTitle=(api:LibraryContentApi,id:string,signal?:AbortSignal)=>callServer(api,'GET /v1/items/{id}',{id,signal});
export async function findChannelTitles(api:LibraryContentApi,library:string,q:string,kind:'movie'|'show',cursor?:string,signal?:AbortSignal){const page=await callServer(api,'GET /v1/search',{query:{library,q,kind,cursor,limit:30},signal});const group=page.groups.find(row=>row.kind===kind);return {items:group?.items??[],nextCursor:group?.nextCursor};}
export async function channelConditionValues(api:LibraryContentApi,field:string,library:string,cursor?:string,signal?:AbortSignal,moodLabels?:Readonly<Record<string,string>>){
 if(['actor','director','writer','producer','composer','person','author','narrator','artist'].includes(field)){const page=await callServer(api,'GET /v1/people',{query:{library,role:field==='person'?undefined:field,cursor,limit:100},signal});return {items:page.items.map(row=>({id:row.id,name:row.name})),nextCursor:page.nextCursor};}
 if(field==='collection'){const page=await callServer(api,'GET /v1/items',{query:{kind:'collection',library,cursor,limit:100},signal});return {items:page.items.map(row=>({id:row.id,name:row.title})),nextCursor:page.nextCursor};}
 const kind=field==='tag'?'':field;const page=await callServer(api,'GET /v1/tags',{query:{kind,cursor,limit:100},signal});return {items:page.items.filter(row=>field!=='tag'||row.kind!=='mood').map(row=>({id:row.id,name:field==='mood'?moodLabels?.[row.name]??'':row.name})).filter(row=>!!row.name),nextCursor:page.nextCursor};
}

export async function readOwnerBlockPreset(api:LibraryContentApi,id:string,signal?:AbortSignal){let cursor:string|undefined;const seen=new Set<string>();do{if(signal?.aborted)throw {code:'cancelled'};const page=await readOwnerBlockPresets(api,cursor,signal);const found=page.items.find(row=>row.id===id);if(found)return found;cursor=page.nextCursor;if(cursor&&seen.has(cursor))throw new Error(unreadableServerResponse);if(cursor)seen.add(cursor);}while(cursor);throw {status:404,code:'not_found'};}
export async function channelConditionNames(api:LibraryContentApi,field:string,ids:readonly string[],signal?:AbortSignal,moodLabels?:Readonly<Record<string,string>>){
 const names:Record<string,string>={};if(!ids.length)return names;
 if(['genre','studio','network','label','tag','country','mood'].includes(field)){for(const value of ids)if(!value.startsWith('tag_'))names[value]=field==='mood'?moodLabels?.[value]??'':value;const only=ids.filter(value=>value.startsWith('tag_'));if(!only.length)return names;const page=await callServer(api,'GET /v1/tags',{query:{ids:only.slice(0,200).join(','),limit:200},signal});for(const row of page.items)names[row.id]=field==='mood'?moodLabels?.[row.name]??'':row.name;return names;}
 let offset=0;await Promise.all(Array.from({length:Math.min(4,ids.length)},async()=>{for(;;){const id=ids[offset++];if(!id||signal?.aborted)return;try{if(field==='collection'){const row=await callServer(api,'GET /v1/items/{id}',{id,signal});names[id]=row.title;}else{const row=await callServer(api,'GET /v1/people/{id}',{id,signal});names[id]=row.name;}}catch(error){if((error as {status?:number}).status!==404)throw error;}}}));return names;
}

/** Raw picture upload; authentication is captured by the owner admission. */
export function uploadOwnerChannelLogo(api:Pick<import('../index.ts').HttpLocalApi,'routeFetch'|'baseUrl'>,id:string,token:string,file:File,signal:AbortSignal){return api.routeFetch(api.baseUrl+serverPath('PUT /v1/admin/library-channels/{id}/logo',{id}),{method:'PUT',body:file,signal,redirect:'error',credentials:'omit',referrerPolicy:'no-referrer',headers:{Authorization:'Bearer '+token,Accept:'application/json','Content-Type':file.type}});}
