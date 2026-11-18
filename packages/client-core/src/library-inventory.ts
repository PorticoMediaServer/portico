import type {ExpectedRemoteSource} from './remote-source-selection.ts';
import {serverPath} from './query/server-api.ts';
import {ownerLibrary,libraryFolders} from './server-admin/library-document.ts';
import type {ContentScope, LibraryContentApi} from './library-content.ts';
import {wireDurations} from './wire-units.ts';
import {dependsOn,type FreshnessDependency} from './freshness-hub.ts';

export type ScanTier='file_list_only'|'basic'|'complete'|'custom';
/** The server's public scan operations this client knows by name (catalog BasicScanOperations + DeepScanOperations). */
export const scanOperations=['probe','local_metadata','subtitles','playback_frames','checksum','chapter_images','trickplay','waveform','loudness','fingerprint','sonic','episode_thumbnail'] as const;
export type ScanOperation=typeof scanOperations[number];
/** `operations` are the server's own ids, each once, in its order: one this client has no name for is kept as given,
 * so a policy read and written back never loses an operation a newer server added. */
export type ScanPolicy=Readonly<{tier:ScanTier;revision:number;operations:readonly string[]}>;
export type InventorySource=Readonly<{eligibleControls?:readonly string[];remoteSource?:string;id:string;libraryId:string;kind:string;name:string;path:string;resolvedPath:string;classification:'local'|'network';generation:number;health:string;enabled:boolean;followSymlinks:boolean;identityConfirmed:boolean;intervalSeconds:number;nextScanAt:string;lastCompleteAt:string;lastProgressAt:string}>;
export type SourceSettings=Readonly<{expectedRemoteSource?:ExpectedRemoteSource;name?:string;path:string;classification?:'local'|'network';followSymlinks?:boolean;intervalSeconds?:number;expectedRevision:number;acceptReplacement?:boolean;/** false: attach without the automatic scan. */scanAfter?:boolean}>;
export type InventoryConfig=Readonly<{eligibleSourceControls?:readonly string[];scope:Readonly<{serverId:string;viewerFence:string}>;libraryId:string;revision:number;sources:readonly InventorySource[];policy:ScanPolicy}>;
const record=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const text=(v:unknown,max=8192):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>text(v,256)&&!!v;
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
function assert(condition:unknown):asserts condition {if(!condition)throw new Error('Invalid inventory response.');}
export function parseInventorySource(wire:unknown,libraryId:string):InventorySource{const v:unknown=wireDurations(wire,{intervalSeconds:'intervalMs'});assert(record(v)&&id(v.id)&&v.libraryId===libraryId&&text(v.kind,64)&&text(v.name,512)&&text(v.path)&&text(v.resolvedPath)&&['local','network'].includes(String(v.classification))&&integer(v.generation)&&text(v.health,128)&&typeof v.enabled==='boolean'&&typeof v.followSymlinks==='boolean'&&typeof v.identityConfirmed==='boolean'&&integer(v.intervalSeconds)&&text(v.nextScanAt,128)&&text(v.lastCompleteAt,128)&&text(v.lastProgressAt,128));return Object.freeze({...v}) as InventorySource;}
/** What an owner's documents about one library are built from: its scans, the server's settings, and the list of libraries (a rename or removal). */
export function libraryAdminDependencies(libraryId:string):readonly FreshnessDependency[]{return [dependsOn.event('library.scan.updated',libraryId),dependsOn.settings('server'),dependsOn.libraries];}
/** The native owner page reads folder members from the rebuilt library. */
export async function readOwnerLibraryInventoryConfig(api:LibraryContentApi,bound:ContentScope,libraryId:string,signal?:AbortSignal):Promise<InventoryConfig>{
 assert(id(libraryId));
 return libraryFolders(ownerLibrary(await api.request<unknown>(serverPath('GET /v1/admin/libraries/{id}',{id:libraryId}),'GET',undefined,signal),libraryId),bound);
}
