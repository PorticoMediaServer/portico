import type {GuideChannelMark} from './channel-guide.ts';
import type {FileInfoActionProof} from './file-info-pages.ts';
import {parsePersistedRemoteConfiguration, type PersistedRemoteConfiguration} from './linear-api.ts';
import {parseInventorySource,type InventorySource} from './library-inventory.ts';
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import type {Alert} from './console.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOf,oneOfOr} from './tolerant.ts';
/** Owner administration wire shapes: media deletion and trash, library configuration, live source configuration, DVR defaults, library channel presets and maintenance. Parsers only; no transport policy and no stored credentials. */
export type AdminEnvelope<T>=Readonly<{protocolVersion:string;serverId:string;result:T}>;
export type AdminFailure=Readonly<{code:string;message:string;retryable:boolean;fields:readonly string[]}>;
export type CostClassId='metadata-only'|'single-pass-read'|'decode'|'network';
export type CostClass=Readonly<{id:CostClassId;name:string;description:string;order:number}>;
export type LibraryAnalysisOperation=Readonly<{id:string;name:string;costClass:CostClassId;description:string;mediaKinds:readonly string[];defaultEnabled:boolean;requires:readonly string[];producesArtifacts:boolean;network:boolean}>;
export type AnalysisMatrix=Readonly<{costClasses:readonly CostClass[];operations:readonly LibraryAnalysisOperation[]}>;
export type MetadataProviderId='tmdb'|'tvdb'|'musicbrainz'|'anilist'|'local';
export type MetadataProvider=Readonly<{id:MetadataProviderId;name:string;mediaKinds:readonly string[];supportsApiKey:boolean;supportsLocale:boolean;attributionNote:string}>;
export type ProviderSelection=Readonly<{mediaKind:string;provider:MetadataProviderId;apiKeySet:boolean;language:string;region:string}>;
export type ChapterThumbnailMode='none'|'embedded'|'generated';
export type GeneratedNavigation=Readonly<{trickplayIntervalMs:number;trickplayTileWidth:number;trickplayMaxTiles:number;chapterThumbnailMode:ChapterThumbnailMode;videoPreviewEnabled:boolean;videoPreviewMs:number}>;
/** A library's intro and credits detection: follow the server's mode, or choose for this library. */
export type SegmentDetectionChoice='server'|'off'|'standard'|'thorough';
export const segmentDetectionChoices:readonly SegmentDetectionChoice[]=['server','off','standard','thorough'];
export type LibrarySettings=Readonly<{scanDepth:'file_list_only'|'basic'|'complete'|'custom';groupCollections:boolean;localTags:'prefer'|'supplement'|'off';lyricsSource:'files'|'lrclib';titleSource:'tags'|'names';episodeNumbering:'season'|'absolute';findExtras:boolean;sortIgnoreArticles:boolean;refreshMode:'replace_unlocked'|'fill_missing';acoustidEnabled:boolean;continueWatching:Readonly<{weeks:number;maximumItems:number;includeSeasonPremieres:boolean;videoPlayedThreshold:number;videoCompletion:'earliest'|'threshold'|'credits'}>;allowMediaDeletion:boolean;monitorChanges:boolean;providers:readonly ProviderSelection[];analysis:readonly string[];navigation:GeneratedNavigation;segmentDetection:SegmentDetectionChoice}>;
export type LibrarySource=InventorySource;
export type LibraryAdminDocument=Readonly<{libraryId:string;libraryName:string;libraryKind:string;revision:number;digest:string;eligibleControls:readonly string[];audience:Readonly<{everyone:boolean;accounts:number}>;actions:readonly ('rename'|'scan'|'remove')[];settings:LibrarySettings;defaults:LibrarySettings;analysisMatrix:AnalysisMatrix;availableProviders:readonly MetadataProvider[];enumerations:Readonly<Record<string,readonly string[]>>;sources:readonly LibrarySource[]}>;
export type DeleteFile=Readonly<{assetId:string;path:string;bytes:number;present:boolean;shared:boolean}>;
export type DeleteDependent=Readonly<{kind:string;count:number;description:string;networkTarget?:string;driveType?:string}>;
export type DeleteTarget=Readonly<{itemId:string;title:string;kind:string;libraryId:string;libraryName:string;allowMediaDeletion:boolean;/** How many playable titles go with this item: 1 for a film, a show's episodes. */titles:number;bytes:number;files:readonly DeleteFile[];dependents:readonly DeleteDependent[]}>;
export type DeletePreview=Readonly<{revision:number;confirmation:string;confirmationKind:'title'|'count';totalBytes:number;totalFiles:number;totalBytesText:string;blocked:readonly string[];targets:readonly DeleteTarget[]}>;
/** One item's outcome. `code` says why an item was not removed: `file_changed` (the file at that path is no longer the one in the library) or `file_not_removed` (storage refused). */
export type DeleteReceipt=Readonly<{itemId:string;title:string;removed:boolean;filesDeleted:number;filesKept:number;bytes:number;code:string}>;
export type DeleteResult=Readonly<{revision:number;removed:number;failed:number;bytesDeleted:number;filesRetained:boolean;receipts:readonly DeleteReceipt[]}>;
export type FilesystemRoot=Readonly<{path:string;name:string;kind:string;description:string;classification?:'local'|'network'}>;
export type FilesystemEntry=Readonly<{name:string;path:string;kind:'directory'|'file'|'unknown';readable:boolean;symlink:boolean;bytes:number;hidden:boolean;reason?:string}>;
export type FilesystemPage=Readonly<{path:string;parent:string;separator:string;platform:string;writable:boolean;writableChecked:boolean;runsAs?:string;reason?:string;suggestions:readonly string[];truncated:boolean;roots:readonly FilesystemRoot[];entries:readonly FilesystemEntry[];nextCursor:string}>;
export type LiveTimeshiftMinutes=10|30|60|120;
export type LiveDefaults=Readonly<{timeshiftMinutes?:LiveTimeshiftMinutes;importLimits:Readonly<{bytes:number;channels:number;programmes:number}>;streamBufferMs:number;retryWindowMs:number;userAgent:string;guideDays:number;logoImport:boolean;discoveryEnabled:boolean}>;
export type FilterRule=Readonly<{mode:'include'|'exclude';values:readonly string[]}>;
export type LiveSourceKind='playlist'|'xmltv-guide'|'hdhomerun';
/** How a source gets its next version: an owner upload, or a remote refresh loop. Optional; an older server sends neither and this client claims neither. */
export type LiveSourceAcquisition='upload'|'remote';
/** `importLimits`: this source's own, lower than the server's; null keeps the server's. */
export type LiveSourceSettings=Readonly<{enabled?:boolean;viewerAccess?:'owner-only'|'server-members';remote?:PersistedRemoteConfiguration;enabledGroups:readonly string[];enabledChannels:readonly string[];kind:LiveSourceKind;streamBufferMs:number|null;retryWindowMs:number|null;userAgent:string;guideSourceId:string;importLimits:Readonly<{bytes:number;channels:number;programmes:number}>|null;filters:Readonly<{categories:FilterRule;countries:FilterRule;keywords:FilterRule}>;logoImport:Readonly<{enabled:boolean;overwriteExisting:boolean;/** true: this source uses the server-wide logo switch. */followServer?:boolean}>;channelNumbering:Readonly<{mode:'source'|'sequential';startAt:number;step:number}>}>;
export type LiveSourceDocument=Readonly<{sourceId:string;sourceName:string;revision:number;digest:string;tunerCount:number;availableKinds:readonly LiveSourceKind[];settings:LiveSourceSettings;effective:LiveDefaults;/** What the logo worker uses, after the server-wide switch and any following choice. */effectiveLogoImport:Readonly<{enabled:boolean;overwriteExisting:boolean}>;
 /** The authoritative update mode when the server sends it; absent means unknown, not remote or upload. */
 acquisition?:LiveSourceAcquisition}>;
export type ChannelMapEntry=Readonly<{mark?:GuideChannelMark;channelId:string;name:string;sourceNumber:string;number:string;group:string;guideChannelId:string;hidden:boolean;position:number;overridden:boolean;overrideNumber?:string}>;
export type ChannelMapPage=Readonly<{sourceId:string;generation:string;revision?:number;items:readonly ChannelMapEntry[];nextCursor:string}>;
export type FolderToken=Readonly<{token:string;description:string;example:string;supportsPadding:boolean}>;
export type KeepPolicy=Readonly<{mode:'keep-all'|'keep-count'|'keep-days';keepCount:number;keepDays:number;keepUntilWatched:boolean}>;
export type DVRDefaults=Readonly<{prePaddingMs:number;postPaddingMs:number;keepPolicy:KeepPolicy;conversion:Readonly<{mode:'none'|'remux'|'ladder';ladderId:string;deleteOriginal:boolean}>;folderTemplate:string;movieFolderTemplate:string;recordingProfile:string;sidecars:Readonly<{nfo:boolean;poster:boolean;fanart:boolean;thumbnail:boolean}>;tunerPreference:string;
 /** When a recording needs a tuner someone is watching live: keep watching (the default; the recording waits) or stop watching (the viewer gets a countdown). */
 liveTunerConflict:LiveTunerConflict}>;
export type LiveTunerConflict='keep-watching'|'stop-watching';
export const liveTunerConflicts:readonly LiveTunerConflict[]=Object.freeze(['keep-watching','stop-watching']);
export type DVRDocument=Readonly<{revision:number;digest:string;settings:DVRDefaults;folderTokens:readonly FolderToken[];enumerations:Readonly<Record<string,readonly string[]>>}>;
/** `accountName`/`profileName`: whose it is, empty for an account that is not this server's own. */
export type TunerAssignment=Readonly<{kind:string;accountId:string;profileId:string;accountName:string;profileName:string;recordingId:string;channelId:string;title:string;state:string;startsAt:string;endsAt:string;allocationId:string}>;
export type TunerAllocation=Readonly<{sourceId:string;sourceName:string;tunerCount:number;inUse:number;conflicts:number;active:readonly TunerAssignment[];upcoming:readonly TunerAssignment[]}>;
export type TunerView=Readonly<{observedAt:string;upcomingWindowHours:number;sources:readonly TunerAllocation[]}>;
export type StorageCategory=Readonly<{id:string;name:string;description:string;directories:readonly string[];cleanable:boolean;regenerable:boolean;retentionKey:string}>;
export type StorageUsage=StorageCategory&Readonly<{bytes:number;bytesText:string;fileCount:number;paths:readonly string[];present:boolean;oldestDays:number}>;
export type StorageReport=Readonly<{stateDirectory:string;categories:readonly StorageUsage[];totalBytes:number;totalBytesText:string;observedAt:string;truncated:boolean}>;
export type UpdateBuild=Readonly<{version:string;buildId:string;sourceDigest:string;builtAt:string;channel:string;notes:string;url:string}>;
export type UpdateStatus='current'|'update-available'|'no-feed'|'no-release'|'unavailable';
export type UpdateReport=Readonly<{current:UpdateBuild;channel:string;feedConfigured:boolean;latest:UpdateBuild|null;updateAvailable:boolean;checkedAt:string;status:UpdateStatus;message:string;autoInstall:false}>;
export type BackupKind='scheduled'|'manual'|'pre-restore';
// TODO(be backups): switch to generated types once apigen publishes the spec §4 routes.
// The shapes below are hand-written from `Spec — Backups (Plex model).md` §4 and §6.2.
export type BackupSummary=Readonly<{id:string;createdAt:string;kind:BackupKind;schemaVersion:number;serverVersion:string;bytes:number;path:string;encrypted?:boolean}>;
export type BackupRunning=Readonly<{jobId:string;kind?:'manual'|'scheduled';phase:string;bytesDone:number;bytesTotal:number}>;
export type RestoreOutcome='restored'|'rolled_back';
export const MANIFEST_DAMAGE_REASONS=Object.freeze(['record_too_large','unreadable_record','invalid_file_entry','entry_hash_mismatch','entry_chain_gap','manifest_end_mismatch','record_after_end','missing_end_record','missing_file','payload_hash_mismatch','duplicate_file','database_hash_mismatch','database_entry_missing'] as const);
export type ManifestDamageReason=typeof MANIFEST_DAMAGE_REASONS[number];
/** Lines are one-based; byte offsets are zero-based, with an exclusive end. */
export type RestoreManifestRegion=Readonly<{firstLine:number;lastLine:number;startByte:number;endByte:number;reason:ManifestDamageReason}>;
export type RestoreManifestDamage=Readonly<{count:number;regions:readonly RestoreManifestRegion[];regionsTruncated?:boolean;missingFiles:number;unverifiedDatabase?:boolean}>;
export type RestoreRecovery=Readonly<{indexesRebuilt?:boolean;salvaged?:boolean;damagedTables?:readonly string[];removedRows?:number;manifestDamage?:RestoreManifestDamage}>;
export type LastRestore=Readonly<{at:string;outcome:RestoreOutcome;reason:string;jobId?:string;schemaVersion?:number;recovery?:RestoreRecovery}>;
export type BackupResult=Readonly<{jobId:string;backupId?:string;outcome:'completed'|'failed';errorCode?:string}>;
export type BackupsDocument=Readonly<{lastBackup?:BackupResult;backups:readonly BackupSummary[];running?:BackupRunning;restore?:RestoreProgress;lastRestore?:LastRestore}>;
export type RestoreSource=Readonly<{backupId:string}|{path:string}>;
export type RestoreProgress=Readonly<{jobId:string;phase:string;bytesDone:number;bytesTotal:number;result?:RestoreStaged;errorCode?:string}>;
export type RestoreStaged=Readonly<{staged:true;restartRequired:true;validation:Readonly<{schemaVersion:number;integrity:'ok'|'recovered';recovery?:RestoreRecovery}>}>;
/** The 4xx codes POST /v1/admin/backups/restore answers with (spec §4); `insufficient_disk` is shared with start. */
export const RESTORE_REFUSAL_CODES=Object.freeze(['restore_lock_unavailable','restore_source_invalid','restore_pre_release','restore_integrity_failed','restore_not_portico','insufficient_disk'] as const);
export type RestoreRefusalCode=typeof RESTORE_REFUSAL_CODES[number];
/** isRestoreRefusalCode answers whether a failure code is one of the catalogue-mapped restore refusals. */
export function isRestoreRefusalCode(code:string):code is RestoreRefusalCode{
 return (RESTORE_REFUSAL_CODES as readonly string[]).includes(code);
}
/** backupProgressFraction reads bytesDone/bytesTotal clamped to [0,1]; undefined while the total is unknown. */
export function backupProgressFraction(running:BackupRunning):number|undefined{
 if(!(running.bytesTotal>0))return undefined;
 return Math.max(0,Math.min(1,running.bytesDone/running.bytesTotal));
}
/** The console alert code for the §6.2 state-permissions warning, as be/backups raises it. */
export const STATE_PERMISSIONS_CODE='state-permissions';
export type StatePermissionsItem=Readonly<{id:string;revision:number;severity:string;status:string;firstAt:number;lastAt:number;occurrences:number}>;

class AdministrationError extends Error{
 code:string;retryable:boolean;fields:readonly string[];
 constructor(code:string,message:string,retryable=false,fields:readonly string[]=[]){super(message);this.name='AdministrationError';this.code=code;this.retryable=retryable;this.fields=Object.freeze([...fields]);}
}
export {AdministrationError};
const object=(v:unknown):v is Record<string,unknown>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const filled=(v:unknown,max=512):v is string=>text(v,max)&&v.length>0;
const count=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const flag=(v:unknown):v is boolean=>typeof v==='boolean';
function invalid(what:string):never{throw Object.assign(new AdministrationError('invalid_administration_response',unreadableServerResponse),{detail:what});}
function list(v:unknown,max:number,what:string):unknown[]{if(!Array.isArray(v)||v.length>max)invalid(what);return v;}
function strings(v:unknown,max:number,what:string):readonly string[]{const values=list(v,max,what);if(!values.every(value=>text(value,512)))invalid(what);return Object.freeze(values as string[]);}
function nullableCount(v:unknown,what:string):number|null{if(v===null)return null;if(!count(v))invalid(what);return v;}
function choice<T extends string>(v:unknown,allowed:readonly T[],what:string):T{if(!allowed.includes(v as T))invalid(what);return v as T;}
/** A list of choices this app cannot read is left out alone; one that is too long still refuses the document. */
function vocabulary(v:unknown,what:string):Readonly<Record<string,readonly string[]>>{if(!object(v))invalid(what);const out:Record<string,readonly string[]>={};for(const[key,value]of Object.entries(v)){if(Array.isArray(value)&&value.length>256)invalid(what);if(!filled(key,64)||!Array.isArray(value)||!value.every(entry=>text(entry,512))){noteSkipped('administration','enumeration');continue;}out[key]=Object.freeze(value as string[]);}return Object.freeze(out);}
/** Reads the rows of a bounded list, leaving out the ones this app cannot read. A list inside a row that is too long still refuses the document. */
function readable<T>(v:unknown,max:number,what:string,read:(row:unknown)=>T,inner:readonly (readonly [string,number])[]=[]):T[]{const rows=list(v,max,what);for(const row of rows)if(object(row))for(const[key,limit]of inner)if(Array.isArray(row[key])&&(row[key] as unknown[]).length>limit)invalid(what);return keepReadable('administration',what,rows,read);}

/** parseFailure reads the server's error body, keeping the named fields so a form can mark them. */
export function parseFailure(v:unknown):AdminFailure{
 const body=object(v)&&object(v.error)?v.error:{};
 return Object.freeze({code:filled(body.code,128)?body.code:'request_failed',message:text(body.message)?body.message:'The administration request failed.',retryable:body.retryable===true,fields:Array.isArray(body.fields)?strings(body.fields,64,'error.fields'):Object.freeze([])});
}

/** parseEnvelope checks the shared envelope and confirms the response came from the expected server.
 * Routes published through the API registry (apikit: maintenance settings, backups, restore…)
 * answer the bare document with no envelope; that body is read as the result (§4a: a shape the
 * server really sends is never "couldn't read"). The server check applies whenever an envelope is present. */
export function parseEnvelope<T>(v:unknown,serverId:string,read:(result:unknown)=>T):AdminEnvelope<T>{
 if(object(v)&&v.protocolVersion===undefined&&v.serverId===undefined&&v.result===undefined)return Object.freeze({protocolVersion:'',serverId,result:read(v)});
 if(!object(v)||!filled(v.protocolVersion,32)||!filled(v.serverId,256)||v.result===undefined)invalid('envelope');
 if(v.serverId!==serverId)throw new AdministrationError('wrong_server','This response came from a different server.');
 return Object.freeze({protocolVersion:v.protocolVersion,serverId:v.serverId,result:read(v.result)});
}

const costClasses:readonly CostClassId[]=['metadata-only','single-pass-read','decode','network'];
function parseOperation(v:unknown):LibraryAnalysisOperation{
 if(!object(v)||!filled(v.id,64)||!filled(v.name,128)||!text(v.description,1024)||!flag(v.defaultEnabled)||!flag(v.producesArtifacts)||!flag(v.network))invalid('analysis operation');
 return Object.freeze({id:v.id,name:v.name,costClass:choice(v.costClass,costClasses,'analysis operation cost class'),description:v.description,mediaKinds:strings(v.mediaKinds,32,'analysis operation media kinds'),defaultEnabled:v.defaultEnabled,requires:v.requires===null||v.requires===undefined?Object.freeze([]):strings(v.requires,16,'analysis operation requires'),producesArtifacts:v.producesArtifacts,network:v.network});
}
/** parseAnalysisMatrix reads the operation matrix. Every operation's cost class must be one the document also publishes. */
export function parseAnalysisMatrix(v:unknown):AnalysisMatrix{
 if(!object(v))invalid('analysis matrix');
 const classes=firstOfEach('administration','cost classes',readable(v.costClasses,16,'cost classes',entry=>{if(!object(entry)||!filled(entry.name,128)||!text(entry.description,1024)||!count(entry.order))invalid('cost class');return Object.freeze({id:choice(entry.id,costClasses,'cost class id'),name:entry.name,description:entry.description,order:entry.order});}),entry=>entry.id);
 const known=new Set(classes.map(entry=>entry.id));
 // An operation this app cannot read, or one whose cost class the document does not publish, is left out alone.
 const operations=firstOfEach('administration','analysis operations',readable(v.operations,128,'analysis operations',entry=>{const operation=parseOperation(entry);if(!known.has(operation.costClass))invalid('analysis operation cost class');return operation;},[['mediaKinds',32],['requires',16]]),operation=>operation.id);
 return Object.freeze({costClasses:Object.freeze(classes),operations:Object.freeze(operations)});
}

const providerIds:readonly MetadataProviderId[]=['tmdb','tvdb','musicbrainz','anilist','local'];
function parseProvider(v:unknown):MetadataProvider{
 if(!object(v)||!filled(v.name,128)||!flag(v.supportsApiKey)||!flag(v.supportsLocale)||!text(v.attributionNote,512))invalid('metadata provider');
 return Object.freeze({id:choice(v.id,providerIds,'metadata provider id'),name:v.name,mediaKinds:strings(v.mediaKinds,32,'metadata provider media kinds'),supportsApiKey:v.supportsApiKey,supportsLocale:v.supportsLocale,attributionNote:v.attributionNote});
}
/** parseMetadataProviders reads the published provider list. */
export function parseMetadataProviders(v:unknown):readonly MetadataProvider[]{
 if(!object(v))invalid('metadata providers');
 return Object.freeze(firstOfEach('administration','metadata providers',readable(v.providers,32,'metadata providers',parseProvider,[['mediaKinds',32]]),provider=>provider.id));
}

function parseSelection(v:unknown):ProviderSelection{
 if(!object(v)||!filled(v.mediaKind,64)||!flag(v.apiKeySet)||!text(v.language,16)||!text(v.region,8))invalid('provider selection');
 // A key is never part of a read. A response that carries one is refused rather
 // than quietly held in client memory.
 if(v.apiKey!==undefined&&v.apiKey!=='')throw new AdministrationError('provider_key_disclosed',unreadableServerResponse);
 return Object.freeze({mediaKind:v.mediaKind,provider:choice(v.provider,providerIds,'provider selection provider'),apiKeySet:v.apiKeySet,language:v.language,region:v.region});
}
function parseNavigation(v:unknown):GeneratedNavigation{
 if(!object(v)||!count(v.trickplayIntervalMs)||!count(v.trickplayTileWidth)||!count(v.trickplayMaxTiles)||!flag(v.videoPreviewEnabled)||!count(v.videoPreviewMs))invalid('generated navigation');
 return Object.freeze({trickplayIntervalMs:v.trickplayIntervalMs,trickplayTileWidth:v.trickplayTileWidth,trickplayMaxTiles:v.trickplayMaxTiles,chapterThumbnailMode:choice(v.chapterThumbnailMode,['none','embedded','generated'] as const,'chapter thumbnail mode'),videoPreviewEnabled:v.videoPreviewEnabled,videoPreviewMs:v.videoPreviewMs});
}
function parseLibrarySettings(v:unknown):LibrarySettings{
 if(!object(v)||!flag(v.allowMediaDeletion)||!flag(v.monitorChanges)||!flag(v.groupCollections)||!flag(v.findExtras)||!flag(v.sortIgnoreArticles)||!flag(v.acoustidEnabled))invalid('library settings');
 const c=v.continueWatching;
 if(!object(c)||!count(c.weeks)||c.weeks<1||c.weeks>104||!count(c.maximumItems)||c.maximumItems<1||c.maximumItems>200||!flag(c.includeSeasonPremieres)||!count(c.videoPlayedThreshold)||c.videoPlayedThreshold<50||c.videoPlayedThreshold>100)invalid('continue watching');
 return Object.freeze({scanDepth:choice(v.scanDepth,['file_list_only','basic','complete','custom'] as const,'scan depth'),groupCollections:v.groupCollections,localTags:choice(v.localTags,['prefer','supplement','off'] as const,'local tags'),lyricsSource:choice(v.lyricsSource,['files','lrclib'] as const,'lyrics source'),titleSource:choice(v.titleSource,['tags','names'] as const,'title source'),episodeNumbering:choice(v.episodeNumbering,['season','absolute'] as const,'episode numbering'),findExtras:v.findExtras,sortIgnoreArticles:v.sortIgnoreArticles,refreshMode:choice(v.refreshMode,['replace_unlocked','fill_missing'] as const,'refresh mode'),acoustidEnabled:v.acoustidEnabled,continueWatching:Object.freeze({weeks:c.weeks,maximumItems:c.maximumItems,includeSeasonPremieres:c.includeSeasonPremieres,videoPlayedThreshold:c.videoPlayedThreshold,videoCompletion:choice(c.videoCompletion,['earliest','threshold','credits'] as const,'video completion')}),allowMediaDeletion:v.allowMediaDeletion,monitorChanges:v.monitorChanges,providers:Object.freeze(list(v.providers,16,'library providers').map(parseSelection)),analysis:strings(v.analysis,128,'library analysis'),navigation:parseNavigation(v.navigation),
  // A server from before the choice existed follows its own mode.
  segmentDetection:v.segmentDetection===undefined?'server':choice(v.segmentDetection,segmentDetectionChoices,'library segment detection')});
}
/** parseLibraryDocument reads one library's administration document. */
export function parseLibraryDocument(v:unknown):LibraryAdminDocument{
 if(!object(v)||!object(v.audience)||!flag(v.audience.everyone)||!count(v.audience.accounts)||!Array.isArray(v.actions)||!v.actions.every(a=>['rename','scan','remove'].includes(a))||!filled(v.libraryId,256)||!text(v.libraryName,512)||!filled(v.libraryKind,64)||!count(v.revision)||v.revision<1||!filled(v.digest,128))invalid('library document');
 // The settings a save sends back stay exact. A folder or an offered provider this app cannot read is left out alone.
 const sources=firstOfEach('administration','library sources',readable(v.sources,256,'library sources',entry=>parseInventorySource(entry,v.libraryId as string)),entry=>entry.id);
 return Object.freeze({libraryId:v.libraryId,libraryName:v.libraryName,libraryKind:v.libraryKind,audience:Object.freeze({everyone:v.audience.everyone,accounts:v.audience.accounts}),actions:Object.freeze(v.actions as ('rename'|'scan'|'remove')[]),revision:v.revision,digest:v.digest,eligibleControls:strings(v.eligibleControls,128,'eligible controls'),settings:parseLibrarySettings(v.settings),defaults:parseLibrarySettings(v.defaults),analysisMatrix:parseAnalysisMatrix(v.analysisMatrix),availableProviders:Object.freeze(firstOfEach('administration','available providers',readable(v.availableProviders,32,'available providers',parseProvider,[['mediaKinds',32]]),provider=>provider.id)),enumerations:vocabulary(v.enumerations,'library enumerations'),sources:Object.freeze(sources)});
}

/** analysisSelectionComplete answers whether every enabled operation's dependencies are also enabled, which is what the server requires of a write. */
export function analysisSelectionComplete(matrix:AnalysisMatrix,selected:readonly string[]):readonly string[]{
 const enabled=new Set(selected);
 const missing=new Set<string>();
 for(const operation of matrix.operations)if(enabled.has(operation.id))for(const need of operation.requires)if(!enabled.has(need))missing.add(need);
 return Object.freeze([...missing].sort());
}

function parseDeleteFile(v:unknown):DeleteFile{
 if(!object(v)||!filled(v.assetId,256)||!text(v.path,8192)||!count(v.bytes)||!flag(v.present)||!flag(v.shared))invalid('delete file');
 return Object.freeze({assetId:v.assetId,path:v.path,bytes:v.bytes,present:v.present,shared:v.shared});
}
function parseTarget(v:unknown):DeleteTarget{
 if(!object(v)||!filled(v.itemId,256)||!text(v.title,1024)||!filled(v.kind,64)||!filled(v.libraryId,256)||!text(v.libraryName,512)||!flag(v.allowMediaDeletion)||!count(v.titles)||!count(v.bytes))invalid('delete target');
 const dependents=list(v.dependents,32,'delete dependents').map(entry=>{if(!object(entry)||!filled(entry.kind,64)||!count(entry.count)||!text(entry.description,512))invalid('delete dependent');return Object.freeze({kind:entry.kind,count:entry.count,description:entry.description,...(typeof entry.networkTarget==='string'?{networkTarget:entry.networkTarget}:{}),...(typeof entry.driveType==='string'?{driveType:entry.driveType}:{})});});
 return Object.freeze({itemId:v.itemId,title:v.title,kind:v.kind,libraryId:v.libraryId,libraryName:v.libraryName,allowMediaDeletion:v.allowMediaDeletion,titles:v.titles,bytes:v.bytes,files:Object.freeze(list(v.files,10000,'delete files').map(parseDeleteFile)),dependents:Object.freeze(dependents)});
}
/** parseDeletePreview reads a delete preview. The confirmation it carries is the only text the delete will accept. */
export function parseDeletePreview(v:unknown):DeletePreview{
 if(!object(v)||!count(v.revision)||!text(v.confirmation,1024)||!count(v.totalBytes)||!count(v.totalFiles)||!text(v.totalBytesText,64))invalid('delete preview');
 const targets=list(v.targets,200,'delete targets').map(parseTarget);
 const kind=choice(v.confirmationKind,['title','count'] as const,'confirmation kind');
 // A single-target preview confirms by title and a multi-target one by count;
 // anything else would let a client prompt for the wrong text.
 if(kind==='title'&&targets.length!==1||kind==='count'&&v.confirmation!==String(targets.length))invalid('confirmation');
 return Object.freeze({revision:v.revision,confirmation:v.confirmation,confirmationKind:kind,totalBytes:v.totalBytes,totalFiles:v.totalFiles,totalBytesText:v.totalBytesText,blocked:strings(v.blocked,200,'blocked libraries'),targets:Object.freeze(targets)});
}
/** deletionAllowed answers whether every target in a preview sits in a library that has enabled deletion. */
export function deletionAllowed(preview:DeletePreview):boolean{
 return preview.blocked.length===0&&preview.targets.every(target=>target.allowMediaDeletion);
}
/** parseDeleteResult reads a delete outcome, one receipt per target. */
export function parseDeleteResult(v:unknown):DeleteResult{
 if(!object(v)||!count(v.revision)||!count(v.removed)||!count(v.failed)||!count(v.bytesDeleted)||!flag(v.filesRetained))invalid('delete result');
 const receipts=list(v.receipts,200,'delete receipts').map(entry=>{
  if(!object(entry)||!filled(entry.itemId,256)||!text(entry.title,1024)||!flag(entry.removed)||!count(entry.filesDeleted)||!count(entry.filesKept)||!count(entry.bytes))invalid('delete receipt');
  return Object.freeze({itemId:entry.itemId,title:entry.title,removed:entry.removed,filesDeleted:entry.filesDeleted,filesKept:entry.filesKept,bytes:entry.bytes,code:text(entry.code,64)?entry.code:''});
 });
 // The receipts say what was removed and what was not. Totals that disagree with them are read from the receipts.
 const removed=receipts.filter(receipt=>receipt.removed).length,failed=receipts.length-removed;
 if(removed!==v.removed||failed!==v.failed)noteSkipped('administration','delete result counts');
 return Object.freeze({revision:v.revision,removed,failed,bytesDeleted:v.bytesDeleted,filesRetained:v.filesRetained,receipts:Object.freeze(receipts)});
}
/** parseFilesystemPage reads the folder picker's roots or one directory page. */
export function parseFilesystemPage(v:unknown):FilesystemPage{
 if(!object(v)||!text(v.path,8192)||!text(v.parent,8192)||!filled(v.separator,4)||!filled(v.platform,32)||!flag(v.writable)||!flag(v.truncated)||!text(v.nextCursor,1024))invalid('filesystem page');
 // A root or an entry this app cannot read is left out alone; an entry of a kind it does not know is listed as unknown.
 const roots=readable(v.roots,64,'filesystem roots',entry=>{if(!object(entry)||!filled(entry.path,8192)||!filled(entry.name,256)||!filled(entry.kind,32)||!text(entry.description,512))invalid('filesystem root');return Object.freeze({path:entry.path,name:entry.name,kind:entry.kind,description:entry.description,...(entry.classification==='local'||entry.classification==='network'?{classification:entry.classification}:{})});});
 const entries=readable(v.entries,200,'filesystem entries',entry=>{
  if(!object(entry)||!filled(entry.name,512)||!filled(entry.path,8192)||!flag(entry.readable)||!flag(entry.symlink))invalid('filesystem entry');
  return Object.freeze({name:entry.name,path:entry.path,kind:oneOfOr('administration','filesystem entry kind',entry.kind,['directory','file','unknown'] as const,'unknown'),readable:entry.readable,symlink:entry.symlink,bytes:count(entry.bytes)?entry.bytes:0,hidden:entry.hidden===true,...(typeof entry.reason==='string'&&['permission','offline','missing','broken-link'].includes(entry.reason)?{reason:entry.reason}:{})});
 });
 return Object.freeze({path:v.path,parent:v.parent,separator:v.separator,platform:v.platform,writable:v.writable,writableChecked:v.writableChecked===true,...(typeof v.runsAs==='string'?{runsAs:v.runsAs}:{}),...(typeof v.reason==='string'?{reason:v.reason}:{}),suggestions:Object.freeze(Array.isArray(v.suggestions)?v.suggestions.filter((x):x is string=>text(x,8192)).slice(0,8):[]),truncated:v.truncated,roots:Object.freeze(roots),entries:Object.freeze(entries),nextCursor:v.nextCursor});
}

function parseLiveDefaults(v:unknown):LiveDefaults{
 if(!object(v)||!count(v.streamBufferMs)||!count(v.retryWindowMs)||!text(v.userAgent,200)||!count(v.guideDays)||!flag(v.logoImport)||!flag(v.discoveryEnabled))invalid('live defaults');
 if(!object(v.importLimits)||!count(v.importLimits.bytes)||v.importLimits.bytes>1073741824||!count(v.importLimits.channels)||v.importLimits.channels>100000||!count(v.importLimits.programmes)||v.importLimits.programmes>10000000)invalid('live import limits');
 const timeshiftMinutes=v.timeshiftMinutes;
 if(timeshiftMinutes!==undefined&&timeshiftMinutes!==10&&timeshiftMinutes!==30&&timeshiftMinutes!==60&&timeshiftMinutes!==120)invalid('live rewind');
 return Object.freeze({...(timeshiftMinutes===undefined?{}:{timeshiftMinutes}),importLimits:Object.freeze({bytes:v.importLimits.bytes,channels:v.importLimits.channels,programmes:v.importLimits.programmes}),streamBufferMs:v.streamBufferMs,retryWindowMs:v.retryWindowMs,userAgent:v.userAgent,guideDays:v.guideDays,logoImport:v.logoImport,discoveryEnabled:v.discoveryEnabled});
}
/** parseLiveDefaultsDocument reads the server-wide live source settings. */
export function parseLiveDefaultsDocument(v:unknown):Readonly<{scope:string;revision:number;digest:string;settings:LiveDefaults}>{
 if(!object(v)||!filled(v.scope,64)||!count(v.revision)||v.revision<1||!filled(v.digest,128))invalid('live defaults document');
 return Object.freeze({scope:v.scope,revision:v.revision,digest:v.digest,settings:parseLiveDefaults(v.settings)});
}
function parseFilter(v:unknown,what:string):FilterRule{
 if(!object(v))invalid(what);
 return Object.freeze({mode:choice(v.mode,['include','exclude'] as const,what),values:strings(v.values,200,what)});
}
const liveKinds:readonly LiveSourceKind[]=['playlist','xmltv-guide','hdhomerun'];
/** The two acquisition values the server may send. Anything else, or nothing, is unknown; refresh-row absence is never read as manual. */
export function liveSourceAcquisition(v:unknown):LiveSourceAcquisition|undefined{return v==='upload'||v==='remote'?v:undefined;}
/** parseLiveSourceDocument reads one source's configuration and the effective values behind it. */
export function parseLiveSourceDocument(v:unknown):LiveSourceDocument{
 if(!object(v)||!filled(v.sourceId,256)||!text(v.sourceName,512)||!count(v.revision)||v.revision<1||!filled(v.digest,128)||!count(v.tunerCount)||!object(v.settings))invalid('live source document');
 const s=v.settings;
 if(!object(s.filters)||!object(s.logoImport)||!object(s.channelNumbering)||!text(s.userAgent,200)||!text(s.guideSourceId,256))invalid('live source settings');
 const numbering=s.channelNumbering;
 if(!count(numbering.startAt)||!count(numbering.step))invalid('live source numbering');
 const logos=s.logoImport;
 if(!flag(logos.enabled)||!flag(logos.overwriteExisting)||logos.followServer!==undefined&&!flag(logos.followServer))invalid('live source logo import');
 const kind=choice(s.kind,liveKinds,'live source kind');
 const buffer=nullableCount(s.streamBufferMs,'live source stream buffer');
 // A guide-only source has nothing to stream, so buffered stream tuning on one
 // would mean the client and the server disagree about what the source is.
 if(kind==='xmltv-guide'&&(buffer!==null||s.guideSourceId!==''))invalid('guide-only source');
 if(s.enabled!==undefined&&!flag(s.enabled)||s.viewerAccess!==undefined&&!['owner-only','server-members'].includes(String(s.viewerAccess)))invalid('live source access');
 const settings=Object.freeze({...s.enabled===undefined?{}:{enabled:s.enabled as boolean},...s.viewerAccess===undefined?{}:{viewerAccess:s.viewerAccess as 'owner-only'|'server-members'},...s.remote===undefined?{}:{remote:parsePersistedRemoteConfiguration(s.remote)},enabledGroups:Object.freeze(strings(s.enabledGroups,100000,"enabled groups")),enabledChannels:Object.freeze(strings(s.enabledChannels,100000,"enabled channels")),kind,streamBufferMs:buffer,retryWindowMs:nullableCount(s.retryWindowMs,'live source retry window'),userAgent:s.userAgent,guideSourceId:s.guideSourceId,importLimits:object(s.importLimits)&&count(s.importLimits.bytes)&&count(s.importLimits.channels)&&count(s.importLimits.programmes)?Object.freeze({bytes:s.importLimits.bytes,channels:s.importLimits.channels,programmes:s.importLimits.programmes}):null,filters:Object.freeze({categories:parseFilter(s.filters.categories,'live source category filter'),countries:parseFilter(s.filters.countries,'live source country filter'),keywords:parseFilter(s.filters.keywords,'live source keyword filter')}),logoImport:Object.freeze({enabled:logos.enabled,overwriteExisting:logos.overwriteExisting,...(logos.followServer===undefined?{}:{followServer:logos.followServer})}),channelNumbering:Object.freeze({mode:choice(numbering.mode,['source','sequential'] as const,'live source numbering mode'),startAt:numbering.startAt,step:numbering.step})});
 // A kind of source this app does not know is not offered. The stored settings stay exact.
 const kinds=keepReadable('administration','live source kinds',list(v.availableKinds,8,'live source kinds'),value=>oneOf(value,liveKinds));
 const acquisition=liveSourceAcquisition(v.acquisition);
 const logoEffective=v.effectiveLogoImport;
 if(!object(logoEffective)||!flag(logoEffective.enabled)||!flag(logoEffective.overwriteExisting))invalid('live source effective logo import');
 return Object.freeze({sourceId:v.sourceId,sourceName:v.sourceName,revision:v.revision,digest:v.digest,tunerCount:v.tunerCount,availableKinds:Object.freeze(kinds),settings,effective:parseLiveDefaults(v.effective),effectiveLogoImport:Object.freeze({enabled:logoEffective.enabled,overwriteExisting:logoEffective.overwriteExisting}),...(acquisition?{acquisition}:{})});
}
/** parseChannelMapPage reads one page of a source's channel map. */
export function parseChannelMapPage(v:unknown):ChannelMapPage{
 if(!object(v)||!filled(v.sourceId,256)||!text(v.generation,256)||!text(v.nextCursor,1024))invalid('channel map page');
 const items=firstOfEach('administration','channel map entries',readable(v.items,200,'channel map entries',entry=>{
  if(!object(entry)||!filled(entry.channelId,256)||!text(entry.name,512)||!text(entry.sourceNumber,16)||!text(entry.number,16)||!text(entry.group,256)||!text(entry.guideChannelId,256)||!flag(entry.hidden)||!count(entry.position)||!flag(entry.overridden))invalid('channel map entry');
  let mark:GuideChannelMark|undefined;
  if(entry.mark!==undefined){
   if(object(entry.mark)&&filled(entry.mark.text,1024)&&entry.mark.text.trim()&&filled(entry.mark.seed,513)&&entry.mark.seed.trim())mark=Object.freeze({text:entry.mark.text,seed:entry.mark.seed});
   else noteSkipped('administration','channel mark');
  }
  return Object.freeze({...(mark?{mark}:{}),channelId:entry.channelId,name:entry.name,sourceNumber:entry.sourceNumber,number:entry.number,group:entry.group,guideChannelId:entry.guideChannelId,hidden:entry.hidden,position:entry.position,overridden:entry.overridden,...(text(entry.overrideNumber,16)?{overrideNumber:entry.overrideNumber}:{})});
 }),entry=>entry.channelId);
 // Two channels served on the same number make the lineup ambiguous. Both are listed, so the owner can see the clash and fix it here.
 const served=items.filter(entry=>!entry.hidden&&entry.number!=='').map(entry=>entry.number);
 if(new Set(served).size!==served.length)noteSkipped('administration','duplicate channel number');
 return Object.freeze({sourceId:v.sourceId,generation:v.generation,...(count(v.revision)?{revision:v.revision}:{}),items:Object.freeze(items),nextCursor:v.nextCursor});
}

/** parseDVRDocument reads the recording defaults and the folder token vocabulary. */
export function parseDVRDocument(v:unknown):DVRDocument{
 if(!object(v)||!count(v.revision)||v.revision<1||!filled(v.digest,128)||!object(v.settings))invalid('dvr document');
 const s=v.settings;
 if(!count(s.prePaddingMs)||!count(s.postPaddingMs)||!object(s.keepPolicy)||!object(s.conversion)||!object(s.sidecars)||!text(s.folderTemplate,400)||!text(s.movieFolderTemplate,400)||!filled(s.recordingProfile,64)||!filled(s.tunerPreference,64))invalid('dvr settings');
 const keep=s.keepPolicy,conversion=s.conversion,sidecars=s.sidecars;
 if(!count(keep.keepCount)||!count(keep.keepDays)||!flag(keep.keepUntilWatched)||!text(conversion.ladderId,128)||!flag(conversion.deleteOriginal)||![sidecars.nfo,sidecars.poster,sidecars.fanart,sidecars.thumbnail].every(flag))invalid('dvr policy');
 const mode=choice(conversion.mode,['none','remux','ladder'] as const,'dvr conversion mode');
 // Removing the original with no converted copy would delete the recording.
 if(mode==='none'&&conversion.deleteOriginal)invalid('dvr conversion');
 const tokens=firstOfEach('administration','folder tokens',readable(v.folderTokens,64,'folder tokens',entry=>{if(!object(entry)||!filled(entry.token,64)||!text(entry.description,512)||!text(entry.example,128)||!flag(entry.supportsPadding))invalid('folder token');return Object.freeze({token:entry.token,description:entry.description,example:entry.example,supportsPadding:entry.supportsPadding});}),entry=>entry.token);
 const settings=Object.freeze({prePaddingMs:s.prePaddingMs,postPaddingMs:s.postPaddingMs,keepPolicy:Object.freeze({mode:choice(keep.mode,['keep-all','keep-count','keep-days'] as const,'dvr keep mode'),keepCount:keep.keepCount,keepDays:keep.keepDays,keepUntilWatched:keep.keepUntilWatched}),conversion:Object.freeze({mode,ladderId:conversion.ladderId,deleteOriginal:conversion.deleteOriginal}),folderTemplate:s.folderTemplate,movieFolderTemplate:s.movieFolderTemplate,recordingProfile:s.recordingProfile,sidecars:Object.freeze({nfo:sidecars.nfo as boolean,poster:sidecars.poster as boolean,fanart:sidecars.fanart as boolean,thumbnail:sidecars.thumbnail as boolean}),tunerPreference:s.tunerPreference,liveTunerConflict:choice(s.liveTunerConflict,liveTunerConflicts,'dvr live tuner conflict')});
 return Object.freeze({revision:v.revision,digest:v.digest,settings,folderTokens:Object.freeze(tokens),enumerations:vocabulary(v.enumerations,'dvr enumerations')});
}
/** folderTemplateTokens lists the tokens a template uses, so a client can check one against the published vocabulary before it saves. */
export function folderTemplateTokens(template:string):readonly string[]{
 const found=new Set<string>();
 for(const match of template.matchAll(/\{([A-Za-z]+)(?::0?2)?\}/g))found.add('{'+match[1]+'}');
 return Object.freeze([...found].sort());
}
/** folderTemplateProblems names what the server would refuse about a template, so a form can say so before the write. */
export function folderTemplateProblems(template:string,tokens:readonly FolderToken[]):readonly string[]{
 const problems:string[]=[];
 const known=new Map(tokens.map(token=>[token.token,token.supportsPadding]));
 if(template==='')problems.push('A template is required.');
 if(/^[\\/]/.test(template)||template.includes('..')||template.includes('//'))problems.push('A template may not escape the recordings folder.');
 for(const name of folderTemplateTokens(template))if(!known.has(name))problems.push('Unknown token '+name+'.');
 for(const match of template.matchAll(/\{([A-Za-z]+):0?2\}/g))if(known.get('{'+match[1]+'}')===false)problems.push('Token {'+match[1]+'} cannot be padded.');
 if(!/\{(title|episode|date)\}/.test(template))problems.push('A template must include {title}, {episode} or {date} so recordings do not collide.');
 return Object.freeze(problems);
}

function parseAssignment(v:unknown):TunerAssignment{
 if(!object(v)||!filled(v.recordingId,256)||!text(v.channelId,256)||!text(v.title,1024)||!filled(v.state,64)||!text(v.startsAt,64)||!text(v.endsAt,64))invalid('tuner assignment');
 if(!text(v.kind,64)||!text(v.accountId,256)||!text(v.profileId,256))invalid("tuner owner");
 return Object.freeze({kind:v.kind,accountId:v.accountId,profileId:v.profileId,accountName:text(v.accountName,256)?v.accountName:'',profileName:text(v.profileName,256)?v.profileName:'',recordingId:v.recordingId,channelId:v.channelId,title:v.title,state:v.state,startsAt:v.startsAt,endsAt:v.endsAt,allocationId:text(v.allocationId,256)?v.allocationId:''});
}
/** parseTunerView reads the allocation page. */
export function parseTunerView(v:unknown):TunerView{
 if(!object(v)||!text(v.observedAt,64)||!count(v.upcomingWindowHours))invalid('tuner view');
 const sources=firstOfEach('administration','tuner sources',readable(v.sources,64,'tuner sources',entry=>{
  if(!object(entry)||!filled(entry.sourceId,256)||!text(entry.sourceName,512)||!count(entry.tunerCount)||!count(entry.inUse)||!count(entry.conflicts))invalid('tuner allocation');
  const active=keepReadable('administration','active streams',list(entry.active,100000,'active streams'),parseAssignment);
  // Shared readers consume one tuner per channel. Unreadable rows cannot invalidate the server's count.
  if(new Set(active.map(v=>v.channelId||v.allocationId||v.recordingId)).size!==entry.inUse)noteSkipped('administration','tuner allocation counts');
  return Object.freeze({sourceId:entry.sourceId,sourceName:entry.sourceName,tunerCount:entry.tunerCount,inUse:entry.inUse,conflicts:entry.conflicts,active:Object.freeze(active),upcoming:Object.freeze(keepReadable('administration','upcoming recordings',list(entry.upcoming,100000,'upcoming recordings'),parseAssignment))});
 },[['active',100000],['upcoming',100000]]),entry=>entry.sourceId);
 return Object.freeze({observedAt:v.observedAt,upcomingWindowHours:v.upcomingWindowHours,sources:Object.freeze(sources)});
}

/** parseStorageReport reads the storage care measurement. */
export function parseStorageReport(v:unknown):StorageReport{
 if(!object(v)||!text(v.stateDirectory,8192)||!count(v.totalBytes)||!text(v.totalBytesText,64)||!text(v.observedAt,64)||!flag(v.truncated))invalid('storage report');
 const categories=firstOfEach('administration','storage categories',readable(v.categories,64,'storage categories',entry=>{
  if(!object(entry)||!filled(entry.id,64)||!filled(entry.name,128)||!text(entry.description,1024)||!flag(entry.cleanable)||!flag(entry.regenerable)||!text(entry.retentionKey,64)||!count(entry.bytes)||!text(entry.bytesText,64)||!count(entry.fileCount)||!flag(entry.present)||!count(entry.oldestDays))invalid('storage category');
  return Object.freeze({id:entry.id,name:entry.name,description:entry.description,directories:strings(entry.directories,16,'storage directories'),cleanable:entry.cleanable,regenerable:entry.regenerable,retentionKey:entry.retentionKey,bytes:entry.bytes,bytesText:entry.bytesText,fileCount:entry.fileCount,paths:strings(entry.paths,16,'storage paths'),present:entry.present,oldestDays:entry.oldestDays,entries:count(entry.entries)?entry.entries:0});
 },[['directories',16],['paths',16]]),entry=>entry.id);
 return Object.freeze({stateDirectory:v.stateDirectory,categories:Object.freeze(categories),totalBytes:v.totalBytes,totalBytesText:v.totalBytesText,observedAt:v.observedAt,truncated:v.truncated});
}

function parseBuild(v:unknown):UpdateBuild{
 if(!object(v)||!text(v.version,64)||!text(v.buildId,128)||!text(v.sourceDigest,128)||!text(v.builtAt,64))invalid('update build');
 return Object.freeze({version:v.version,buildId:v.buildId,sourceDigest:v.sourceDigest,builtAt:v.builtAt,channel:text(v.channel,32)?v.channel:'',notes:text(v.notes,4096)?v.notes:'',url:text(v.url,2048)?v.url:''});
}
/** parseUpdateReport reads the updates page. A report claiming an automatic install is refused: this server never installs anything. */
export function parseUpdateReport(v:unknown):UpdateReport{
 if(!object(v)||!filled(v.channel,32)||!flag(v.feedConfigured)||!flag(v.updateAvailable)||!text(v.checkedAt,64)||!text(v.message,1024))invalid('update report');
 if(v.autoInstall!==false)throw new AdministrationError('unexpected_auto_install','This server reported that it installs updates on its own, which it must never do.');
 const latest=v.latest===null||v.latest===undefined?null:parseBuild(v.latest);
 // A status this app does not know reads as an update when the report describes one, and otherwise as unavailable.
 const status=oneOfOr('administration','update status',v.status,['current','update-available','no-feed','no-release','unavailable'] as const,v.updateAvailable&&latest!==null?'update-available':'unavailable');
 if(v.updateAvailable&&(latest===null||status!=='update-available'))invalid('update report');
 return Object.freeze({current:parseBuild(v.current),channel:v.channel,feedConfigured:v.feedConfigured,latest,updateAvailable:v.updateAvailable,checkedAt:v.checkedAt,status,message:v.message,autoInstall:false});
}

const backupKinds:readonly BackupKind[]=['scheduled','manual','pre-restore'];
const restoreOutcomes:readonly RestoreOutcome[]=['restored','rolled_back'];
/** parseBackupsDocument reads GET /v1/admin/backups (spec §4). A backup with an unknown kind or one
 * that cannot be read is skipped, a repeated one keeps its first row, and a malformed optional
 * `running`/`lastRestore` block is dropped — each degrades to a rendered list instead of failing
 * the whole screen (§4a). */
export function parseBackupsDocument(v:unknown):BackupsDocument{
 if(!object(v))invalid('backups document');
 const backups=firstOfEach('administration','backups',keepReadable('administration','backups',list(v.backups,1000,'backups'),(entry):BackupSummary|undefined=>{
  if(!object(entry)||!filled(entry.id,128)||!text(entry.createdAt,64))invalid('backup');
  if(!backupKinds.includes(entry.kind as BackupKind))return undefined;
  if(entry.encrypted!==undefined&&typeof entry.encrypted!=='boolean')invalid('backup encryption');
  if(!count(entry.schemaVersion)||!text(entry.serverVersion,64)||!count(entry.bytes)||!text(entry.path,8192))invalid('backup');
  return Object.freeze({id:entry.id,createdAt:entry.createdAt,kind:entry.kind as BackupKind,schemaVersion:entry.schemaVersion,serverVersion:entry.serverVersion,bytes:entry.bytes,path:entry.path,...(entry.encrypted!==undefined?{encrypted:entry.encrypted as boolean}:{})});
 }),entry=>entry.id);
 const doc:{lastBackup?:BackupResult;backups:readonly BackupSummary[];running?:BackupRunning;restore?:RestoreProgress;lastRestore?:LastRestore}={backups:Object.freeze(backups)};
 if(v.lastBackup!==undefined&&v.lastBackup!==null){
  const r=v.lastBackup;
  if(object(r)&&filled(r.jobId,128)&&(r.outcome==='completed'||r.outcome==='failed'))doc.lastBackup=Object.freeze({jobId:r.jobId,outcome:r.outcome,...(filled(r.backupId,128)?{backupId:r.backupId as string}:{}),...(filled(r.errorCode,128)?{errorCode:r.errorCode as string}:{})});
 }
 if(v.running!==undefined&&v.running!==null){
  const r=v.running;
  if(object(r)&&filled(r.jobId,128)&&text(r.phase,128)&&count(r.bytesDone)&&count(r.bytesTotal))doc.running=Object.freeze({jobId:r.jobId,...(r.kind==='manual'||r.kind==='scheduled'?{kind:r.kind}:{}),phase:r.phase,bytesDone:r.bytesDone,bytesTotal:r.bytesTotal});
 }
 if(v.restore!==undefined&&v.restore!==null){
  const r=v.restore;
  if(object(r)&&filled(r.jobId,128)&&text(r.phase,128)&&count(r.bytesDone)&&count(r.bytesTotal))doc.restore=Object.freeze({jobId:r.jobId,phase:r.phase,bytesDone:r.bytesDone,bytesTotal:r.bytesTotal,...(r.result?{result:parseRestoreStaged(r.result)}:{}),...(text(r.errorCode,128)?{errorCode:r.errorCode as string}:{})});
 }
 if(v.lastRestore!==undefined&&v.lastRestore!==null){
  const r=v.lastRestore;
  if(object(r)&&text(r.at,64)&&restoreOutcomes.includes(r.outcome as RestoreOutcome))doc.lastRestore=Object.freeze({at:r.at,outcome:r.outcome as RestoreOutcome,reason:text(r.reason,1024)?r.reason as string:'',...(filled(r.jobId,128)?{jobId:r.jobId as string}:{}),...(count(r.schemaVersion)?{schemaVersion:r.schemaVersion as number}:{}),...(r.recovery!==undefined?{recovery:parseRestoreRecovery(r.recovery)}:{})});
 }
 return Object.freeze(doc);
}

function parseRestoreManifestDamage(v:unknown):RestoreManifestDamage{
 if(!object(v)||!count(v.count)||v.count===0||!count(v.missingFiles)||(v.regionsTruncated!==undefined&&!flag(v.regionsTruncated))||(v.unverifiedDatabase!==undefined&&!flag(v.unverifiedDatabase)))invalid('manifest damage');
 const regions=list(v.regions,20,'manifest damage regions').map(region=>{
  if(!object(region)||!count(region.firstLine)||region.firstLine<1||!count(region.lastLine)||region.lastLine<region.firstLine||!count(region.startByte)||!count(region.endByte)||region.endByte<region.startByte)invalid('manifest damage region');
  return Object.freeze({firstLine:region.firstLine,lastLine:region.lastLine,startByte:region.startByte,endByte:region.endByte,reason:choice(region.reason,MANIFEST_DAMAGE_REASONS,'manifest damage reason')});
 });
 if(regions.length!==Math.min(v.count,20)||(v.regionsTruncated===true)!==(v.count>regions.length))invalid('manifest damage count');
 return Object.freeze({count:v.count,regions:Object.freeze(regions),missingFiles:v.missingFiles,...(v.regionsTruncated!==undefined?{regionsTruncated:v.regionsTruncated}:{}),...(v.unverifiedDatabase!==undefined?{unverifiedDatabase:v.unverifiedDatabase}:{})});
}

function parseRestoreRecovery(v:unknown):RestoreRecovery{
 if(!object(v)||(v.indexesRebuilt!==undefined&&typeof v.indexesRebuilt!=='boolean')||(v.salvaged!==undefined&&typeof v.salvaged!=='boolean')||(v.removedRows!==undefined&&!count(v.removedRows)))invalid('restore recovery');
 const damagedTables=v.damagedTables===undefined?undefined:list(v.damagedTables,1000,'damaged tables').map(table=>{if(!filled(table))invalid('damaged table');return table;});
 return Object.freeze({...(v.indexesRebuilt!==undefined?{indexesRebuilt:v.indexesRebuilt as boolean}:{}),...(v.salvaged!==undefined?{salvaged:v.salvaged as boolean}:{}),...(damagedTables?{damagedTables:Object.freeze(damagedTables)}:{}),...(v.removedRows!==undefined?{removedRows:v.removedRows as number}:{}),...(v.manifestDamage!==undefined?{manifestDamage:parseRestoreManifestDamage(v.manifestDamage)}:{})});
}

/** parseRestoreStaged accepts only a checked or recovered database staged for restart. */
export function parseRestoreStaged(v:unknown):RestoreStaged{
 if(!object(v)||v.staged!==true||v.restartRequired!==true||!object(v.validation)||!count(v.validation.schemaVersion)||(v.validation.integrity!=='ok'&&v.validation.integrity!=='recovered'))invalid('restore receipt');
 return Object.freeze({staged:true,restartRequired:true,validation:Object.freeze({schemaVersion:v.validation.schemaVersion as number,integrity:v.validation.integrity as 'ok'|'recovered',...(v.validation.recovery!==undefined?{recovery:parseRestoreRecovery(v.validation.recovery)}:{})})});
}

/** parseStatePermissionsItem reads the §6.2 warning out of the health/alerts contract. */
export function parseStatePermissionsItem(v:unknown):StatePermissionsItem{
 if(!object(v)||v.code!==STATE_PERMISSIONS_CODE||!filled(v.id,160)||!count(v.revision)||(v.revision as number)<1||!text(v.severity,32)||!filled(v.status,32)||!count(v.firstAt)||!count(v.lastAt)||!count(v.occurrences))invalid('state permissions item');
 return Object.freeze({id:v.id as string,revision:v.revision as number,severity:v.severity as string,status:v.status as string,firstAt:v.firstAt as number,lastAt:v.lastAt as number,occurrences:v.occurrences as number});
}

/** isStatePermissionsAlert answers whether an alert is a live (unresolved) permissions warning. */
export function isStatePermissionsAlert(alert:Pick<Alert,'code'|'status'>):boolean{
 return alert.code===STATE_PERMISSIONS_CODE&&alert.status!=='resolved';
}

/**
 * The providers one selection can choose from: the ones that serve its media kind. "Local files
 * only" is the library's Metadata source choice, so it is listed here only while it is the stored value.
 */
export function providerChoices(available:readonly MetadataProvider[],selection:Readonly<{mediaKind:string;provider:string}>):readonly MetadataProvider[]{
 return available.filter(a=>a.mediaKinds.includes(selection.mediaKind)&&(a.id!=='local'||selection.provider==='local'));
}

export type VersionDeletePreview=DeletePreview&Readonly<{assetId:string;previewKey:string}>;
export function parseVersionDeletePreview(raw:unknown):VersionDeletePreview{
 const preview=parseDeletePreview(raw);
 if(!object(raw)||!filled(raw.assetId,128)||!text(raw.previewKey,64)||!/^[0-9a-f]{64}$/.test(raw.previewKey)||preview.targets.length!==1||preview.totalFiles!==1||preview.targets[0].files.length!==1||preview.targets[0].files[0].assetId!==raw.assetId)invalid('version delete preview');
 return Object.freeze({...preview,assetId:raw.assetId,previewKey:raw.previewKey});
}
export function versionDeletionService(request:<T>(path:string,method?:string,body?:unknown)=>Promise<T>){
 const path=(item:string,asset:string)=>encodedRoutePath("delete_title_version", {"id": encodeURIComponent(item), "fileId": encodeURIComponent(asset)});
 // From File info, `fileInfo` names the exact occurrence of the accepted opening; the route's
 // item and file must be that occurrence's. The server refuses a stale opening before any change.
 const proof=(fileInfo?:FileInfoActionProof)=>fileInfo?{fileInfo:{opening:fileInfo.opening,occurrence:fileInfo.occurrence}}:{};
 return Object.freeze({
  async preview(item:string,asset:string,fileInfo?:FileInfoActionProof){return parseVersionDeletePreview(await request<unknown>(encodedRoutePath("preview_delete_title_version", {"id": encodeURIComponent(item), "fileId": encodeURIComponent(asset)}),'POST',fileInfo?proof(fileInfo):undefined));},
  async remove(preview:VersionDeletePreview,confirmation:string,operationId:string,fileInfo?:FileInfoActionProof){return parseDeleteResult(await request<unknown>(path(preview.targets[0].itemId,preview.assetId),'DELETE',{confirmation,operationId,previewKey:preview.previewKey,...proof(fileInfo)}));},
 });
}

export type LiveSourceChoicePage=Readonly<{sourceId:string;generation:string;kind:'groups'|'channels';total:number;items:readonly Readonly<{id:string;name:string;group:string;channels:number}>[];nextCursor:string}>;
export function parseLiveSourceChoicePage(v:unknown):LiveSourceChoicePage{
 if(!object(v)||!filled(v.sourceId,128)||!text(v.generation,128)||!count(v.total)||!text(v.nextCursor,4096))invalid('source choices');
 const kind=choice(v.kind,['groups','channels'] as const,'source choice kind');
 const items=readable(v.items,200,'source choices',e=>{if(!object(e)||!filled(e.id,256)||!text(e.name,512)||!text(e.group,256)||!count(e.channels))invalid('source choice');return Object.freeze({id:e.id,name:e.name,group:e.group,channels:e.channels});});
 return Object.freeze({sourceId:v.sourceId,generation:v.generation,kind,total:v.total,items:Object.freeze(items),nextCursor:v.nextCursor});
}
export function liveSourceChoicesService(request:<T>(path:string,method?:string,body?:unknown)=>Promise<T>,source:string){return Object.freeze({
 async list(kind:'groups'|'channels',cursor='',limit=50,q=''){return parseLiveSourceChoicePage(await request<unknown>(encodedRoutePath(kind==='groups'?'list_live_source_groups':'list_live_source_channels',{id:encodeURIComponent(source)})+`?cursor=${encodeURIComponent(cursor)}&limit=${limit}&q=${encodeURIComponent(q)}`));},
});}

/** One revision-fenced persisted source document, shared by owner clients. */
export function liveSourceConfigurationService(request:<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal)=>Promise<T>,serverId:string,sourceId:string){
 const path=encodedRoutePath('get_live_source_configuration',{id:encodeURIComponent(sourceId)});
 const checked=(raw:unknown)=>{const doc=parseEnvelope(raw,serverId,parseLiveSourceDocument).result;if(doc.sourceId!==sourceId)invalid('live source identity');return doc;};
 return {async read(signal?:AbortSignal){return checked(await request<unknown>(path,'GET',undefined,signal));},async write(expectedRevision:number,operationId:string,settings:LiveSourceSettings,signal?:AbortSignal){return checked(await request<unknown>(path,'PUT',{expectedRevision,operationId,settings},signal));}};
}
