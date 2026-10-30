import {routeBody} from "../../contracts/src/routes.ts";
import type {FileInfoActionProof} from './file-info-pages.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
/** Titles as things in their own right: titles whose files are gone from disk,
 * saying two titles are one, and removing a library. Owner only. These routes
 * are on the typed registry, so their answers are plain JSON (no envelope). */

import {firstOfEach,keepReadable,oneOfOr} from './tolerant.ts';

export type TitleIdentityApi=Readonly<{request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>}>;

/** When missing titles are removed: `immediately`, `after_days` (see `days`) or `never`. */
export type MissingRule='immediately'|'after_days'|'never';
export type MissingTitle=Readonly<{id:string;libraryId:string;kind:string;title:string;parentTitle?:string;year?:number;
 /** When the title's last file went (RFC 3339). */missingSince:string;
 /** When the owner's rule will remove it; absent when the rule is `never`. */removedAt?:string}>;
export type MissingTitlesPage=Readonly<{observedAt?:string;items:readonly MissingTitle[];nextCursor?:string;total:number;rule:MissingRule;days:number}>;
/** What one pass of emptying removed. Call again while `more` is true. */
export type MissingEmptied=Readonly<{titles:number;files:number;more:boolean}>;

const obj=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=1024):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&v.length>0;
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const rules:readonly MissingRule[]=['immediately','after_days','never'];
function invalid(what:string):never{throw new Error('Unreadable server response: '+what);}

/**
 * The page size, the cursor and the total stay exact: the total is what the owner types to remove
 * them all. A title this app cannot read is left out of the list alone and a repeated one keeps its
 * first row. A rule this app does not know reads as `never`; each row still says when it goes.
 */
export function parseMissingTitlesPage(v:unknown):MissingTitlesPage{
 if(!obj(v)||!Array.isArray(v.items)||v.items.length>200||!count(v.total)||!count(v.days))invalid('missing titles');
 if(v.observedAt!==undefined&&(!text(v.observedAt,64)||!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?Z$/.test(v.observedAt)||!Number.isFinite(Date.parse(v.observedAt))||Date.parse(v.observedAt)<=0))invalid('missing titles observation');
 if(v.nextCursor!==undefined&&!text(v.nextCursor,256))invalid('missing titles cursor');
 const items=firstOfEach('missing-titles','items',keepReadable('missing-titles','items',v.items,entry=>{
  if(!obj(entry)||!id(entry.id)||!id(entry.libraryId)||!id(entry.kind)||!text(entry.title)||!text(entry.missingSince,64)||!entry.missingSince)invalid('missing title');
  if(entry.parentTitle!==undefined&&!text(entry.parentTitle)||entry.year!==undefined&&!count(entry.year)||entry.removedAt!==undefined&&!text(entry.removedAt,64))invalid('missing title');
  return Object.freeze({id:entry.id,libraryId:entry.libraryId,kind:entry.kind,title:entry.title,...(entry.parentTitle?{parentTitle:entry.parentTitle as string}:{}),...(entry.year?{year:entry.year as number}:{}),missingSince:entry.missingSince,...(entry.removedAt?{removedAt:entry.removedAt as string}:{})});
 }),entry=>entry.id);
 return Object.freeze({...(typeof v.observedAt==='string'?{observedAt:v.observedAt}:{}),items:Object.freeze(items),...(v.nextCursor?{nextCursor:v.nextCursor as string}:{}),total:v.total,rule:oneOfOr('missing-titles','rule',v.rule,rules,'never'),days:v.days});
}
export function parseMissingEmptied(v:unknown):MissingEmptied{
 if(!obj(v)||!count(v.titles)||!count(v.files)||typeof v.more!=='boolean')invalid('emptied missing titles');
 return Object.freeze({titles:v.titles,files:v.files,more:v.more});
}

/** One page of missing titles, oldest first. `libraryId` omitted lists every library. */
export async function listMissingTitles(api:TitleIdentityApi,input:{libraryId?:string;cursor?:string;limit?:number}={},signal?:AbortSignal):Promise<MissingTitlesPage>{
 const params=new URLSearchParams();
 if(input.libraryId)params.set('libraryId',input.libraryId);
 if(input.cursor)params.set('cursor',input.cursor);
 if(input.limit)params.set('limit',String(input.limit));
 const query=params.toString();
 return parseMissingTitlesPage(await api.request<RouteResponse<"list_missing_titles">>(routePath("list_missing_titles") + (query?'?'+query:''),'GET',undefined,signal));
}
/** Removes missing titles and files now, whatever the owner's rule says. The server works in
 * short passes; this repeats until nothing is left and returns the totals. Watch state is kept. */
export async function emptyMissingTitles(api:TitleIdentityApi,libraryId?:string,signal?:AbortSignal):Promise<MissingEmptied>{
 let titles=0,files=0;
 for(let pass=0;pass<1000;pass++){
  const step=parseMissingEmptied(await api.request<RouteResponse<"empty_missing_titles">>(routePath("empty_missing_titles"),'POST',libraryId?{libraryId}:{},signal));
  titles+=step.titles;files+=step.files;
  if(!step.more)return Object.freeze({titles,files,more:false});
 }
 return Object.freeze({titles,files,more:true});
}
/** Says two titles of one library are the same title: `fromId` becomes part of `intoId`, with its
 * files as versions and everyone's state combined. Returns the id that remains. */
/** From File info, `fileInfo` proves the exact source file of the opening the owner acted on; the
 * server refuses a stale proof with 409 before changing anything. */
const proofToken=(v:unknown)=>typeof v==='string'&&v.length>0&&v.length<=1024&&!/[\s\x00-\x1f\x7f]/.test(v);
export async function mergeTitles(api:TitleIdentityApi,fromId:string,intoId:string,fileInfo?:FileInfoActionProof):Promise<string>{
 if(!id(fromId)||!id(intoId)||fromId===intoId)invalid('titles to merge');
 if(fileInfo&&(!proofToken(fileInfo.opening)||!proofToken(fileInfo.occurrence)))invalid('titles to merge');
 const out=await api.request<RouteResponse<"merge_titles">>(routePath("merge_titles"),'POST',routeBody("merge_titles", {fromId,intoId,...(fileInfo?{fileInfo:{opening:fileInfo.opening,occurrence:fileInfo.occurrence}}:{})}));
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
export async function removeLibrary(api:TitleIdentityApi,libraryId:string,confirmation:string,expectedRevision:number):Promise<void>{
 if(!id(libraryId)||!text(confirmation,200)||!count(expectedRevision))invalid('library to remove');
 const out=await api.request<RouteResponse<"remove_library">>(encodedRoutePath("remove_library", {"id": encodeURIComponent(libraryId)}),'DELETE',routeBody("remove_library", {confirmation,expectedRevision}));
 if(!obj(out)||out.removing!==true)invalid('library removal');
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
export async function separateLibraryFolder(api:TitleIdentityApi,libraryId:string,file:LibraryFileRecord):Promise<string>{
 if(!id(libraryId)||!id(file.objectId)||!id(file.revision))invalid('folder to separate');
 const out=await api.request<unknown>(libraryBase(libraryId)+'/inventory/'+encodeURIComponent(file.objectId)+'/separate-folder','POST',{revision:file.revision});
 if(!obj(out)||!id(out.itemId))invalid('separated album');
 return out.itemId;
}

/** Says one file is not a version of the title it was grouped with: it becomes a title of its own, and the choice is remembered across scans. Returns the new title's id. */
/** "Not this title" from File info: the server resolves the exact occurrence by point and applies
 * the same separate action. No library inventory lookup. Returns the new title's id. */
export async function separateFileInfoOccurrence(api:TitleIdentityApi,itemId:string,fileInfo:FileInfoActionProof):Promise<string>{
 if(!id(itemId)||!proofToken(fileInfo.opening)||!proofToken(fileInfo.occurrence))invalid('file to separate');
 const out=await api.request<RouteResponse<"separate_item_file_info">>(routePath("separate_item_file_info",{id:itemId}),'POST',routeBody("separate_item_file_info",{fileInfo:{opening:fileInfo.opening,occurrence:fileInfo.occurrence}}));
 if(!obj(out)||!id(out.itemId))invalid('separated title');
 return out.itemId;
}
export async function separateLibraryFile(api:TitleIdentityApi,libraryId:string,file:LibraryFileRecord):Promise<string>{
 if(!id(libraryId)||!id(file.objectId)||!id(file.revision))invalid('file to separate');
 const out=await api.request<unknown>(libraryBase(libraryId)+'/inventory/'+encodeURIComponent(file.objectId)+'/separate','POST',{revision:file.revision});
 if(!obj(out)||!id(out.itemId))invalid('separated title');
 return out.itemId;
}
