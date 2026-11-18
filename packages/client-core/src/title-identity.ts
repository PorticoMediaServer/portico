import {routeBody} from "../../contracts/src/routes.ts";
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {callServer} from './query/server-api.ts';
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
/** Title identity changes and library removal. */
export type TitleIdentityApi=Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;

const obj=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=1024):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&v.length>0;
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
function invalid(what:string):never{throw new Error('Unreadable server response: '+what);}

/** Says two titles of one library are the same title: `fromId` becomes part of `intoId`, with its
 * files as versions and everyone's state combined. Returns the id that remains. */
/** Merge the source title into the one whose details are kept. Rights and scope are checked by the server. */
export async function mergeTitles(api:TitleIdentityApi,fromId:string,intoId:string):Promise<string>{
 if(!id(fromId)||!id(intoId)||fromId===intoId)invalid('titles to merge');
 const out=await callServer(api, 'POST /v1/admin/items/{id}/merge', {id:intoId, body:{itemId:fromId}});
 if(!obj(out)||!id(out.itemId))invalid('merged title');
 return out.itemId;
}
/** Says these albums, all filed under one artist, are by someone else of the same name ("Not
 * this artist"): they move, with their songs' credits, to a new artist of their own that is never
 * joined back automatically. At least one album must stay with the artist. Returns the new artist's id. */
export async function separateArtist(api:TitleIdentityApi,albumIds:readonly string[]):Promise<string>{
 if(!albumIds.length||albumIds.length>100||!albumIds.every(id))invalid('albums to separate');
 const out=await api.request<RouteResponse<"separate_artist">>(routePath("separate_artist"),'POST',routeBody("separate_artist", {albumIds:[...albumIds]}));
 if(!obj(out)||!id(out.artistId))invalid('separated artist');
 return out.artistId;
}
/** Removes a library. `confirmation` is the library's name typed back; `expectedRevision` its
 * configuration revision. The library disappears at once; the rest happens in the background.
 * Files on disk are not touched and watch state is kept in case its titles return. */
/** Removes a library on the rebuilt server (`DELETE /v1/admin/libraries/{id}?confirm=`): the server checks the name the owner typed
 * and removes it in a background job, which this waits for (`GET /v1/jobs/{id}`, up to a minute) so the library list read after it
 * no longer holds the library. A job that fails is an error. The server keeps no revision of a library to compare. */
export async function removeLibrary(api:TitleIdentityApi,libraryId:string,confirmation:string,expectedRevision:number,pollMs=500):Promise<void>{
 if(!id(libraryId)||!text(confirmation,200)||!count(expectedRevision))invalid('library to remove');
 let job=await callServer(api,'DELETE /v1/admin/libraries/{id}',{id:libraryId,query:{confirm:confirmation}});
 if(!obj(job)||!id(job.id))invalid('library removal');
 const until=Date.now()+60_000,jobId=job.id;
 while(job.state==='waiting'||job.state==='running'){
  if(Date.now()>until)return;
  await new Promise(resolve=>setTimeout(resolve,pollMs));
  job=await callServer(api,'GET /v1/jobs/{id}',{id:jobId});
  if(!obj(job))invalid('library removal');
 }
 if(job.state==='failed')throw Object.assign(new Error(typeof job.error==='string'&&job.error?job.error:'The library could not be removed.'),{code:'library_removal_failed'});
}

// ── Merging and splitting ───────────────────────────────────────────────────

/** The kinds of title two of which the server will make one: a show takes the other's seasons and episodes; the rest take the other's files as versions. */
export const MERGEABLE_KINDS:readonly string[]=Object.freeze(['movie','show','episode','song','audiobook_file','extra','album','book','artist','author']);
/** The kinds whose merge moves children (an album's songs, a book's files, an artist's albums, an author's books) rather than files. */
export const CONTAINER_MERGE_KINDS:readonly string[]=Object.freeze(['album','book','artist','author']);
/** What a merge of this kind does, in words: the message id of the explanation shown before it. */
export const mergeExplanation=(kind:string):'web.merge.body'|'web.merge.containerBody'=>CONTAINER_MERGE_KINDS.includes(kind)?'web.merge.containerBody':'web.merge.body';
/** Whether two titles can be offered for merging: different titles of one kind, and not known to be in different libraries (a list inside one library need not name it on every entry; the server refuses a pair that is not in one library). */
export function canMergeTitles(a:Readonly<{id:string;kind:string;libraryId?:string}>,b:Readonly<{id:string;kind:string;libraryId?:string}>):boolean{
 return a.id!==b.id&&a.kind===b.kind&&MERGEABLE_KINDS.includes(a.kind)&&(!a.libraryId||!b.libraryId||a.libraryId===b.libraryId);
}
/** The kinds of title a file can be split off: a film, a track or a book's file. An episode is placed by show, season and number instead. */
export const SPLITTABLE_KINDS:readonly string[]=Object.freeze(['movie','song','audiobook_file']);

/** The library's own record of one file: what a split is asked with. */
export type LibraryFileRecord=Readonly<{objectId:string;revision:string}>;
/** A library folder as far as finding a file needs it. */
export type LibraryFolder=Readonly<{id:string;path:string;resolvedPath:string}>;

/** The folder a file's path lies in: the folder with the longest path that leads to it, by either of the folder's paths. */
export function folderOfPath(folders:readonly LibraryFolder[],filePath:string):string|undefined{
 const under=(folder:string)=>!!folder&&(filePath===folder||filePath.startsWith(folder.replace(/[\\/]+$/,'')+'/')||filePath.startsWith(folder.replace(/[\\/]+$/,'')+'\\'));
 let best:LibraryFolder|undefined,length=-1;
 for(const folder of folders)for(const path of [folder.path,folder.resolvedPath])if(under(path)&&path.length>length){best=folder;length=path.length;}
 return best?.id;
}

const libraryBase=(libraryId:string)=>encodedRoutePath("get_admin_libraries_id", {"id": encodeURIComponent(libraryId)});

/** A library's folders. An unreadable folder is left out alone: it only narrows the search, and a file is still found without it. */
export async function libraryFolders(api:TitleIdentityApi,libraryId:string,signal?:AbortSignal):Promise<readonly LibraryFolder[]>{
 const raw=await api.request<unknown>(libraryBase(libraryId)+'/inventory-config','GET',undefined,signal);
 if(!obj(raw)||!Array.isArray(raw.sources))invalid('library folders');
 return Object.freeze(keepReadable('title-identity','folders',raw.sources.slice(0,64),v=>{if(!obj(v)||!id(v.id)||!text(v.path,8192)||!text(v.resolvedPath,8192))invalid('library folder');return Object.freeze({id:v.id,path:v.path,resolvedPath:v.resolvedPath});}));
}

/**
 * Finds the library's record of one of a title's files by asking the server for that file
 * (within one folder when the folder is known). `onProgress` says how many rows came back;
 * `signal` stops it. Nothing found returns undefined.
 */
export async function findLibraryFile(api:TitleIdentityApi,libraryId:string,fileId:string,options:Readonly<{folderId?:string;signal?:AbortSignal;onProgress?:(looked:number)=>void}>={}):Promise<LibraryFileRecord|undefined>{
 if(!id(libraryId)||!id(fileId))invalid('file to find');

 const params=new URLSearchParams({limit:'40',assetId:fileId,state:'available'});
 if(options.folderId)params.set('sourceId',options.folderId);
 const raw=await api.request<unknown>(libraryBase(libraryId)+'/inventory?'+params,'GET',undefined,options.signal);
 if(!obj(raw)||!Array.isArray(raw.items)||raw.items.length>40)invalid('library files');
 options.onProgress?.(raw.items.length);
 for(const row of raw.items){if(!obj(row)||row.assetId!==fileId)continue;if(!id(row.id)||!id(row.revision))invalid('library file');return Object.freeze({objectId:row.id,revision:row.revision});}
 return undefined;
}

/** Kinds whose files sit in an album or book, which an owner can separate by folder. */
export const FOLDER_SPLITTABLE_KINDS:readonly string[]=Object.freeze(['song','audiobook_file']);

/** Says the folder holding this file is an album (or book) of its own, not part of the one it was joined with. Returns the new album's or book's id. */
export async function separateLibraryFolder(api:TitleIdentityApi,libraryId:string,file:LibraryFileRecord,signal?:AbortSignal):Promise<string>{
 const objectId=file.objectId,revision=file.revision;
 if(!id(libraryId)||!id(objectId)||!id(revision))invalid('folder to separate');
 const path=encodedRoutePath("post_admin_libraries_id_inventory_object_separate_folder", {"id": encodeURIComponent(libraryId), "object": encodeURIComponent(objectId)});
 const body=Object.freeze({revision}),started=Date.now();
 let pending:unknown;
 for(let attempt=0;attempt<32;attempt++){
  if(signal?.aborted)throw folderCancelled();
  if(attempt>0&&Date.now()-started>=120_000)throw pending;
  let out:unknown;
  try{out=await folderRequest(api,path,body,signal);}
  catch(error){
   if(signal?.aborted)throw folderCancelled();
   // Match the transport's ApiError fields; same_request is not transport auto-retry.
   if(!obj(error)||!((error.status===409&&error.code==='request_in_progress')||(error.status===503&&error.code==='writer_capacity')))throw error;
   pending=error;
   const seconds=typeof error.retryAfterSeconds==='number'&&error.retryAfterSeconds>0?error.retryAfterSeconds:2;
   const delay=seconds*1000,remaining=120_000-(Date.now()-started);
   if(attempt===31||delay>=remaining)throw error;
   await folderWait(delay,signal);
   continue;
  }
  if(signal?.aborted)throw folderCancelled();
  if(!obj(out)||!id(out.itemId))invalid('separated album');
  return out.itemId;
 }
 throw pending;
}
function folderCancelled():Error{return Object.assign(new Error('cancelled'),{name:'AbortError'});}
/** Race even a transport that ignores cancellation; its late result cannot publish an id. */
function folderRequest(api:TitleIdentityApi,path:string,body:Readonly<{revision:string}>,signal?:AbortSignal):Promise<unknown>{
 return new Promise((resolve,reject)=>{
  const abort=()=>{signal?.removeEventListener('abort',abort);reject(folderCancelled());};
  if(signal?.aborted){abort();return;}
  signal?.addEventListener('abort',abort,{once:true});
  Promise.resolve().then(()=>{
   if(signal?.aborted)throw folderCancelled();
   return api.request<unknown>(path,'POST',body,signal);
  }).then(resolve,reject).finally(()=>signal?.removeEventListener('abort',abort));
 });
}
function folderWait(delay:number,signal?:AbortSignal):Promise<void>{
 return new Promise((resolve,reject)=>{
  const cleanup=()=>{clearTimeout(timer);signal?.removeEventListener('abort',abort);};
  const abort=()=>{cleanup();reject(folderCancelled());};
  const timer=setTimeout(()=>{cleanup();resolve();},delay);
  if(signal?.aborted){abort();return;}
  signal?.addEventListener('abort',abort,{once:true});
 });
}

/** Split one whole version into a new title. There is no occurrence/part split on server-next. */
export async function splitTitleVersion(api:TitleIdentityApi,versionId:string):Promise<string>{
 if(!id(versionId))invalid('version to split');
 const out=await callServer(api, 'POST /v1/admin/versions/{id}/split', {id:versionId});
 if(!obj(out)||!id(out.itemId))invalid('split title');
 return out.itemId;
}
export async function separateLibraryFile(api:TitleIdentityApi,libraryId:string,file:LibraryFileRecord):Promise<string>{
 if(!id(libraryId)||!id(file.objectId)||!id(file.revision))invalid('file to separate');
 const out=await api.request<unknown>(libraryBase(libraryId)+'/inventory/'+encodeURIComponent(file.objectId)+'/separate','POST',{revision:file.revision});
 if(!obj(out)||!id(out.itemId))invalid('separated title');
 return out.itemId;
}
