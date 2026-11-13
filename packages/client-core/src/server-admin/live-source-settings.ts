import {throwIfAborted} from '../abort-lifetime.ts';
import type {LiveSource,LiveSourceSave} from '../../../contracts/src/next/api.ts';
import type {LibraryContentApi} from '../library-content.ts';
import {callServer,serverPath} from '../query/server-api.ts';
export type SourceSettingsDocument=Readonly<{source:LiveSource;etag:string;imagesEnabled:boolean}>;
export type SourceChoicePage=Readonly<{items:readonly {id:string;name:string}[];nextCursor?:string}>;
async function sourceDocument(api:LibraryContentApi,id:string,method:'GET'|'PATCH',signal?:AbortSignal,body?:LiveSourceSave,etag?:string){
 if(!api.requestRaw)throw {code:'invalid_response'};
 const answer=await api.requestRaw(serverPath('GET /v1/admin/live/sources/{id}',{id}),method,{signal,body,...(etag?{headers:{'If-Match':etag},singleAttempt:true}:{})});
 if(answer.status>=400){const error=(answer.body as {error?:{code?:string}})?.error;throw {status:answer.status,code:error?.code};}
 const source=answer.body as LiveSource,revision=answer.headers.etag??answer.headers.ETag;
 if(!source||source.id!==id||!source.config||typeof source.name!=='string'||!/^"[0-9]+"$/.test(revision??''))throw {code:'invalid_response'};
 return {source,etag:revision!};
}
export async function readSourceSettings(api:LibraryContentApi,id:string,signal?:AbortSignal):Promise<SourceSettingsDocument>{
 const document=await sourceDocument(api,id,'GET',signal);throwIfAborted(signal);
 const settings=await callServer(api,'GET /v1/admin/settings/{name}',{id:'live',signal});
 return {...document,imagesEnabled:(settings.values as {importImages?:boolean}).importImages!==false};
}
export function saveSourceSettings(api:LibraryContentApi,id:string,body:LiveSourceSave,etag:string,signal?:AbortSignal){return sourceDocument(api,id,'PATCH',signal,body,etag);}
export function importSourceLogos(api:LibraryContentApi,id:string,replaceExisting:boolean,signal?:AbortSignal){return callServer(api,'POST /v1/admin/live/sources/{id}/logos',{id,body:{replaceExisting},signal});}
export async function readSourceChoices(api:LibraryContentApi,id:string,kind:'groups'|'channels',query:string,cursor?:string,signal?:AbortSignal):Promise<SourceChoicePage>{
 if(kind==='groups'){
  const source=await callServer(api,'GET /v1/admin/live/sources/{id}',{id,signal});if(source.id!==id)throw {code:'invalid_response'};
  return {items:(source.report?.groups??[]).filter(name=>name.toLocaleLowerCase().includes(query.toLocaleLowerCase())).map(name=>({id:name,name}))};
 }
 const answer=await callServer(api,'GET /v1/admin/live/sources/{id}/channels',{id,query:{q:query,cursor},signal});
 if(answer.items.some(row=>typeof row.sourceKey!=='string'||typeof row.name!=='string'))throw {code:'invalid_response'};
 return {items:answer.items.map(row=>({id:row.sourceKey,name:row.name})),nextCursor:answer.nextCursor};
}
