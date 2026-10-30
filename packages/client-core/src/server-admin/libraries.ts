import {expectedRemoteSource} from '../remote-source-selection.ts';
import {encodedRoutePath,routePath} from '../../../contracts/src/route-paths.ts';
import {parseLibraryDocument,type LibraryAdminDocument,type LibrarySettings} from '../administration.ts';
import {parseInventorySource,type InventorySource,type SourceSettings} from '../library-inventory.ts';
import {durationIntent} from '../wire-units.ts';
import type {LibraryContentApi} from '../library-content.ts';
export {listMissingTitles,emptyMissingTitles,removeLibrary} from '../title-identity.ts';
export {readLibraryInventoryConfig} from '../library-inventory.ts';
export {LibraryManagementService,browseServerFilesystem} from '../library-management.ts';
export {setLibraryAnalysis,setLibraryScanDepth,libraryScanDepth,setLibrarySeekPreviews,setLibraryPreviewHours} from './library-policy.ts';

function libraryID(id:string){if(!id||id.length>256||/[\x00-\x1f\x7f]/.test(id))throw new Error('Invalid library identity.');return encodeURIComponent(id);}
/** A bounded configuration read. It never reads the library's catalogue items. */
export async function readLibrarySettings(api:LibraryContentApi,id:string,signal?:AbortSignal):Promise<LibraryAdminDocument>{
 const doc=parseLibraryDocument(await api.request<unknown>(encodedRoutePath('get_library_settings',{id:libraryID(id)}),'GET',undefined,signal));
 if(doc.libraryId!==id)throw new Error('Library settings identity changed.');
 return doc;
}
/** One explicit CAS write; consume only after the exact library document parses. */
export async function saveLibrarySettings(api:LibraryContentApi,current:LibraryAdminDocument,settings:LibrarySettings,operationId:string,signal?:AbortSignal):Promise<LibraryAdminDocument>{
 if(!operationId||operationId.length>128)throw new Error('Invalid library operation identity.');
 const doc=parseLibraryDocument(await api.request<unknown>(encodedRoutePath('put_library_settings',{id:libraryID(current.libraryId)}),'PUT',{expectedRevision:current.revision,operationId,settings},signal));
 if(doc.libraryId!==current.libraryId||doc.revision<=current.revision)throw new Error('Library settings write could not be reconciled.');
 await api.acknowledgeUserMutation?.(operationId);
 return doc;
}
/** Listed stable IDs come first; the server retains omitted IDs in their prior order. */
export async function reorderLibraries(api:LibraryContentApi,libraryIds:readonly string[],signal?:AbortSignal):Promise<void>{
 if(!libraryIds.length||libraryIds.length>1000||new Set(libraryIds).size!==libraryIds.length)throw new Error('Invalid library order.');
 libraryIds.forEach(libraryID);
 const raw=await api.request<{libraryIds:unknown}>(routePath('put_admin_libraries_order'),'PUT',{libraryIds},signal);
 if(!raw||!Array.isArray(raw.libraryIds)||raw.libraryIds.length!==libraryIds.length||raw.libraryIds.some((id,i)=>id!==libraryIds[i]))throw new Error('Library order could not be reconciled.');
}
/** expectedRevision is inventory-config.revision, never settings.revision. */
export async function saveLibraryFolder(api:LibraryContentApi,libraryId:string,sourceId:string|undefined,input:SourceSettings,signal?:AbortSignal):Promise<InventorySource>{
 if(!Number.isSafeInteger(input.expectedRevision)||input.expectedRevision<1||!Number.isSafeInteger(input.intervalSeconds)||input.intervalSeconds<0)throw new Error('Invalid folder revision or interval.');
 if(input.scanAfter!==undefined&&typeof input.scanAfter!=='boolean')throw new Error('Invalid folder scan choice.');
 const path=sourceId?encodedRoutePath('patch_admin_libraries_id_sources_source',{id:libraryID(libraryId),source:libraryID(sourceId)}):encodedRoutePath('post_admin_libraries_id_sources',{id:libraryID(libraryId)});
 const source=parseInventorySource(await api.request<unknown>(path,sourceId?'PATCH':'POST',durationIntent({...input,...(input.expectedRemoteSource!==undefined?{expectedRemoteSource:expectedRemoteSource(input.expectedRemoteSource)}:{})},{intervalSeconds:'intervalMs'}),signal),libraryId);
 if(sourceId&&source.id!==sourceId)throw new Error('Library folder identity changed.');
 return source;
}
export async function removeLibraryFolder(api:LibraryContentApi,libraryId:string,sourceId:string,expectedRevision:number,signal?:AbortSignal):Promise<void>{
 if(!Number.isSafeInteger(expectedRevision)||expectedRevision<1)throw new Error('Invalid folder revision.');
 await api.request(encodedRoutePath('delete_admin_libraries_id_sources_source',{id:libraryID(libraryId),source:libraryID(sourceId)}),'DELETE',{expectedRevision},signal);
}
