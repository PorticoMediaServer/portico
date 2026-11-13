import {encodedRoutePath,routePath} from '../../../contracts/src/route-paths.ts';
import {type LibraryAdminDocument,type LibrarySettings} from '../administration.ts';
import {serverPath} from '../query/server-api.ts';
import {ownerLibrary, libraryDocument, libraryFolders} from './library-document.ts';
import {CodedError, unreadableServerResponse} from '../server-messages.ts';
import type {AdminLibrary, LibraryEdit} from '../../../contracts/src/next/api.ts';
import {type InventorySource,type SourceSettings} from '../library-inventory.ts';
import type {LibraryContentApi} from '../library-content.ts';
export {listMissingTitles,emptyMissingTitles,removeLibrary} from '../title-identity.ts';
export {readLibraryInventoryConfig} from '../library-inventory.ts';
export {LibraryManagementService,browseServerFilesystem} from '../library-management.ts';
export {setLibraryAnalysis,setLibraryScanDepth,libraryScanDepth,setLibrarySeekPreviews,setLibraryPreviewHours} from './library-policy.ts';

function libraryID(id:string){if(!id||id.length>256||/[\x00-\x1f\x7f]/.test(id))throw new Error('Invalid library identity.');return encodeURIComponent(id);}
/** A bounded configuration read. It never reads the library's catalogue items. */
export async function readLibrarySettings(api:LibraryContentApi,id:string,signal?:AbortSignal):Promise<LibraryAdminDocument>{
 libraryID(id);
 return libraryDocument(ownerLibrary(await api.request<unknown>(serverPath('GET /v1/admin/libraries/{id}',{id}),'GET',undefined,signal),id));
}
/** The version the owner edited goes in If-Match, never a revision in the server body. */
export async function patchOwnerLibrary(api:LibraryContentApi,id:string,revision:number,body:LibraryEdit,signal?:AbortSignal):Promise<AdminLibrary>{
 libraryID(id);
 if(!Number.isSafeInteger(revision)||revision<1)throw new Error(unreadableServerResponse);
 if(!api.requestRaw)throw new CodedError('unsupported',unreadableServerResponse);
 const response=await api.requestRaw(serverPath('PATCH /v1/admin/libraries/{id}',{id}),'PATCH',{headers:{'If-Match':`"${revision}"`},body,signal,singleAttempt:true});
 if(response.status>=400){const error=(response.body as {error?:{code?:string;message?:string}}|undefined)?.error;
  throw Object.assign(new Error(error?.message||unreadableServerResponse),{status:response.status,code:error?.code});}
 return ownerLibrary(response.body,id);
}
/** One explicit CAS write; consume only after the exact library document parses. */
export async function saveLibrarySettings(api:LibraryContentApi,current:LibraryAdminDocument,settings:LibrarySettings,operationId:string,signal?:AbortSignal):Promise<LibraryAdminDocument>{
 if(!operationId||operationId.length>128)throw new Error('Invalid library operation identity.');
 const original=current.serverLibrary;if(!original)throw new Error(unreadableServerResponse);
 const options={...original.options},body:LibraryEdit={};let changedOptions=false;
 const fields={scanDepth:'depth',groupCollections:'groupSeries',allowMediaDeletion:'allowDelete',monitorChanges:'watch',lyricsSource:'lyrics',segmentDetection:'segmentDetection'} as const;
 for(const key of Object.keys(settings) as (keyof LibrarySettings)[]){
  if(JSON.stringify(settings[key])===JSON.stringify(current.settings[key]))continue;
  if(key==='providers'){
   const provider=settings.providers[0],before=current.settings.providers[0];if(!provider||!before)throw new Error(unreadableServerResponse);
   if(provider.provider!==before.provider)body.metadataProvider=provider.provider;
   if(provider.language!==before.language)body.language=provider.language;
   if(provider.region!==before.region)body.region=provider.region;
  }else if(Object.hasOwn(fields,key)&&Object.hasOwn(current.settings,key)){
   const value=key==='scanDepth'&&settings.scanDepth==='file_list_only'?'files':key==='lyricsSource'&&settings.lyricsSource==='files'?'':settings[key];
   Object.assign(options,{[fields[key as keyof typeof fields]]:value});changedOptions=true;
  }else throw new CodedError('unsupported',unreadableServerResponse);
 }
 if(changedOptions)body.options=options;
 if(!Object.keys(body).length)return current;
 const doc=libraryDocument(await patchOwnerLibrary(api,current.libraryId,current.revision,body,signal));
 if(doc.revision<=current.revision)throw new Error('Library settings write could not be reconciled.');
 return doc;
}
/** Listed stable IDs come first; the server retains omitted IDs in their prior order. */
export async function reorderLibraries(api:LibraryContentApi,libraryIds:readonly string[],signal?:AbortSignal):Promise<void>{
 if(!libraryIds.length||libraryIds.length>1000||new Set(libraryIds).size!==libraryIds.length)throw new Error('Invalid library order.');
 libraryIds.forEach(libraryID);
 const raw=await api.request<{libraryIds:unknown}>(routePath('put_admin_libraries_order'),'PUT',{libraryIds},signal);
 if(!raw||!Array.isArray(raw.libraryIds)||raw.libraryIds.length!==libraryIds.length||raw.libraryIds.some((id,i)=>id!==libraryIds[i]))throw new Error('Library order could not be reconciled.');
}
/** Folder membership uses the same library version as the settings page. */
async function folderLibrary(api:LibraryContentApi,id:string,revision:number,signal?:AbortSignal){
 const library=ownerLibrary(await api.request<unknown>(serverPath('GET /v1/admin/libraries/{id}',{id}),'GET',undefined,signal),id);
 if(Number(library.revision)!==revision)throw Object.assign(new Error('The library changed elsewhere.'),{status:412,code:'changed_elsewhere'});
 return library;
}
export async function saveLibraryFolder(api:LibraryContentApi,libraryId:string,sourceId:string|undefined,input:SourceSettings,signal?:AbortSignal):Promise<InventorySource>{
 if(!input.path||!Number.isSafeInteger(input.expectedRevision)||input.expectedRevision<1)throw new Error('Invalid folder revision or path.');
 // No editor may silently discard a setting the server does not honour yet.
 if(['name','classification','followSymlinks','intervalSeconds','scanAfter'].some(key=>(input as unknown as Record<string,unknown>)[key]!==undefined)||input.acceptReplacement)throw new CodedError('unsupported',unreadableServerResponse);
 const library=await folderLibrary(api,libraryId,input.expectedRevision,signal);
 const prior=sourceId?library.folders.find(f=>f.id===sourceId):undefined;
 if(sourceId&&!prior)throw new Error('Library folder identity changed.');
 const remote=input.expectedRemoteSource?.sourceId??prior?.source;
 const kept=library.folders.filter(f=>f.id!==sourceId);
 const member={path:input.path,...(remote?{source:remote}:{})};
 const folders=[...kept,member];
 const saved=await patchOwnerLibrary(api,libraryId,input.expectedRevision,{folders:folders.filter(f=>!f.source).map(f=>f.path),
  remoteFolders:folders.flatMap(f=>f.source?[{source:f.source,path:f.path}]:[])},signal);
 const answer=libraryFolders(saved,{serverId:'',viewerId:''}).sources.find(f=>f.path===input.path&&f.remoteSource===remote);
 if(!answer)throw new Error('Library folder write could not be reconciled.');return answer;
}
export async function removeLibraryFolder(api:LibraryContentApi,libraryId:string,sourceId:string,expectedRevision:number,signal?:AbortSignal):Promise<void>{
 const library=await folderLibrary(api,libraryId,expectedRevision,signal),kept=library.folders.filter(f=>f.id!==sourceId);
 if(kept.length===library.folders.length)throw new Error('Library folder identity changed.');
 await patchOwnerLibrary(api,libraryId,expectedRevision,{folders:kept.filter(f=>!f.source).map(f=>f.path),
  remoteFolders:kept.flatMap(f=>f.source?[{source:f.source,path:f.path}]:[])},signal);
}
export async function scanLibraryFolder(api:LibraryContentApi,libraryId:string,folder:string,signal?:AbortSignal):Promise<void>{
 await api.request(serverPath('POST /v1/admin/libraries/{id}/scan',{id:libraryId}),'POST',{folder},signal);
}
export async function checkLibraryFolder(api:LibraryContentApi,libraryId:string,folder:string,signal?:AbortSignal):Promise<void>{
 const library=ownerLibrary(await api.request<unknown>(serverPath('GET /v1/admin/libraries/{id}',{id:libraryId}),'GET',undefined,signal),libraryId);
 const member=library.folders.find(f=>f.id===folder);if(!member)throw new Error('Library folder identity changed.');
 const path=member.source?serverPath('GET /v1/admin/remote-sources/{id}/folders',{id:member.source}):serverPath('GET /v1/admin/filesystem');
 await api.request(path+'?'+new URLSearchParams({path:member.path}),'GET',undefined,signal);
}
