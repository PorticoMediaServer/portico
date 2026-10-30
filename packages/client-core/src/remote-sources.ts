import {routeBody} from "../../contracts/src/routes.ts";
import {parseSourceChoiceApplicableFields,parseSourceChoiceBaseline,parseSourceChoiceDefinitions,sourceChoiceAccepted,sourceChoiceAdopt,sourceChoiceCommand,sourceChoicePutBody,type SourceChoiceAcceptance,type SourceChoiceBaseline,type SourceChoiceCommand,type SourceChoiceComparison,type SourceChoiceDefinition,type SourceChoiceField} from './remote-source-choice.ts';
import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
/** Owner-only source metadata and operation recovery. Credentials exist only in an
 * explicit in-flight request, never in snapshots, storage, URLs or retry queues. */
import type {ContentScope,LibraryContentApi} from './library-content.ts';
import {firstOfEach,keepReadable,noteSkipped,oneOfOr} from './tolerant.ts';
import {rebaseDraft} from './freshness-actions.ts';
import {UserMutationError, sameUserMutationScope, type UserMutationScope} from './user-mutations.ts';
export type RemoteSource=Readonly<{id:string;kind:'rclone'|'webdav'|'sftp'|'smb';name:string;rootPath:string;generation:number;state:string;errorCode?:string;rangeSupport:'unknown'|'conditional'|'unsupported'|'sequential';origin?:string;insecureLocal:boolean;credentialPresent:boolean;mountId?:string}>;
export type DAVConfiguration=Readonly<{name:string;root:string;username:string;password:string;keepPassword:boolean;keepConnection:boolean;insecureLocal:boolean;expectedGeneration?:number}>;
export type DAVConnectionInput=Pick<DAVConfiguration,'name'|'root'|'username'|'password'|'insecureLocal'>;
export type DAVConnectionResult=Readonly<{origin:string;rangeSupport:'unknown'|'conditional'|'unsupported'}>;
/** An imported source is connection material only: a remote name and the
 * configuration that defines it. Portico supplies the program, never the owner. */
export type RcloneSourceDraft=Readonly<{name:string;remote:string;config:string}>;
export type RcloneTestInput=Readonly<{remote:string;config:string}>;
export type RcloneTestResult=Readonly<{version:string}>;
/** The bundled program's own status: what the server ships, never a chosen path. */
export type RcloneProgram=Readonly<{included:boolean;available:boolean;version:string;minimumVersion:string;errorCode:string}>;
/** The durable identity an imported source save returns; the receipt carries no
 * source body, so the list is read again for the row's display facts. */
export type RcloneReceipt=Readonly<{operationId:string;sourceId:string;generation:number;removed:boolean}>;
/** One remote of the server account's own rclone configuration. `reason` says why
 * an unsupported one is unsupported; no value from its section is here. */
export type RcloneConfigRemote=Readonly<{name:string;kind:string;supported:boolean;reason:'kind'|'encrypted'|'wrapper'|'duplicate'|'ambiguous'|''}>;
export type RcloneConfigView=Readonly<{path:string;remotes:readonly RcloneConfigRemote[];truncated:boolean}>;
export type DriveOAuthStart=Readonly<{authorizationUrl:string;state:string}>;
export type DriveOAuthPhase='pending'|'ready'|'expired'|'denied';
export type DriveOAuthStatus=Readonly<{phase:DriveOAuthPhase;token:string}>;
export type RemoteReceipt=Readonly<{operationId:string;accepted:true;removed:boolean;source:RemoteSource}>;
/** What one command was allowed to do, kept beside its operation id so a later
 * receipt (live or recovered) must still match the original target and posture. */
export type SourceCommandIntent=Readonly<{operationId:string;kind:'source'|'rclone';action:'create'|'edit'|'remove';sourceId:string|null;generation?:number}>;
export type AnalysisPolicy=Readonly<{libraryId:string;enabled:boolean;revision:number;effective?:boolean;scanTier?:string}>;
/** The read-authority part of one source's Reading settings. A missing saved profile is
 * reported as `saved:false` with the server's list/once defaults; it is never invented. */
export type SourceReadValues=Readonly<{scanDepth:'file_list_only'|'basic'|'complete';wholeFileReads:boolean;connections:number;requestsPerSecond:number;bytesPerSecond:number;/** Recorded advanced media-analysis consent; absent means this server never published one, not false. */allowAdvancedMediaAnalysis?:boolean}>;
export type SourceReadSettings=Readonly<{sourceId:string;generation:string;revision:number;saved:boolean;values:SourceReadValues;
 /** Present only when the server observed an effective configuration generation. */
 configurationGeneration?:string;
 /** Root57 projection, present only as the server published it. */
 choiceDefinitions?:readonly SourceChoiceDefinition[];
 choiceApplicableFields?:readonly SourceChoiceField[];
 choiceBaseline?:SourceChoiceBaseline}>;
export type SourceReadPreset='list'|'once'|'everything'|'ask';
/** One folder inside a saved source during browsing: its display name, the
 * absolute path the catalog uses for it, and the path inside the source. */
export type RemoteFolder=Readonly<{name:string;path:string;relative:string;hidden:boolean}>;
export type RemoteFolderPage=Readonly<{sourceId:string;generation:string;path:string;relative:string;entries:readonly RemoteFolder[];nextCursor:string;truncated:boolean}>;
/** One library that uses folders from a source: the exact count of its enabled
 * folders from that source, and their stored last completed scan only when all
 * of them agree on one time. A mixed library omits it; nothing is picked,
 * averaged or inferred. */
export type SourceLibraryUse=Readonly<{libraryId:string;name:string;kind:string;folders:number;lastCompleteAt?:string}>;
/** A source's folder health, published only when every enabled folder agrees
 * on the same stored code. Absence is not a healthy or idle claim. */
export type RemoteSourceHealth='ready'|'healthy'|'degraded'|'inventory_running'|'cancelled'|'offline'|'root_changed'|'removing';
/** A real completed provider failure the contact history may carry. */
export type RemoteContactErrorCode='offline'|'credentials_required'|'rate_limited'|'quota_exceeded';
/** One source folder's real active scan: closed state/mode/phase codes and
 * stored counters/timestamps. sourceGeneration/runPolicyRevision are the values
 * the run captured, not proof of the current source or policy. queuedAt is the
 * job's queue time; no separate scan-start time is stored. completedAt is the
 * inventory pass completing (analysis may continue); finishedAt is the whole
 * run. No percentage, job error, path or guess. */
export type RemoteScanStateCode='queued'|'running'|'paused';
export type RemoteScanMode='first-import'|'incremental'|'integrity'|'full';
export type RemoteScanPhase='inventory'|'legacy_inventory'|'basic_pending'|'basic_running'|'analysis_enqueue'|'reconciling'|'sidecars'|'verifying'|'extras';
export type RemoteScanState=Readonly<{state:RemoteScanStateCode;mode?:RemoteScanMode;phase?:RemoteScanPhase;sourceGeneration:number;runPolicyRevision:number;processed:number;discovered:number;analyzed:number;warnings:number;queuedAt?:string;completedAt?:string;finishedAt?:string}>;
/** A source's present state: the reads it holds now, when it was last checked,
 * and the libraries its folders went into. Nothing here is estimated. */
/** A saved source's live test: the effective observation generation and, for a
 * completed provider failure, its closed code. No saved config changes. */
export type SavedSourceTest=Readonly<{sourceId:string;generation:string;reachable:boolean;rangeSupport?:RemoteSource['rangeSupport'];errorCode?:RemoteContactErrorCode}>;
export type RemoteSourceStatus=Readonly<{assignmentFenceSupported?:boolean;sourceId:string;generation:string;state:string;errorCode?:string;rangeSupport:string;lastCheckedAt?:string;connectionsInUse:number;playbackReads:number;analysisReads:number;
 /** The current live serve process's admitted shared operation-budget sample: direct DAV and listing work on that budget, not sockets, distinct viewers or a complete upstream connection count. Absent when no current process can sample it; a present zero is measured. */
 serveOperationsInUse?:number;scanIntervalSeconds:number;libraries:readonly SourceLibraryUse[];librariesTruncated:boolean;health?:RemoteSourceHealth;lastConnectedAt?:string;lastErrorAt?:string;lastErrorCode?:RemoteContactErrorCode;lastCompleteAt?:string;scan?:RemoteScanState;program?:string;programVersion?:string;programRevision?:number;programIncluded?:boolean;remote?:string;backgroundSpeed?:string}>;
/** The drawn Advanced page: how a source's folders are checked, the disk its
 * read cache may use, and which analysis stages run for files found through it. */
export type SourceAdvanced=Readonly<{scanIntervalSeconds:number;watchForChanges:boolean;scanOnStart:boolean;fullRecheckDays:number;cacheBytes:number;cacheFloorBytes:number;seekPreviews:boolean;chapterImages:boolean;waveforms:boolean;loudness:boolean;checksums:boolean;introsMode:'off'|'inherit'|'sound'|'picture';doubleCheckFolders:boolean;dirCacheSeconds:number;readBufferBytes:number;connectTimeoutSeconds:number;idleTimeoutSeconds:number;retries:number;lowLevelRetries:number}>;
export type SourceProgram=Readonly<{executable:string;version:string;revision:number;included:boolean}>;
export type SourceAdvancedSettings=Readonly<{sourceId:string;generation:string;revision:number;saved:boolean;cacheSupported:boolean;cacheApplied:boolean;cacheError?:string;cacheUsedBytes:number;scanIntervalObserved:number;values:SourceAdvanced}>;
// The retired whole-file flag stays in the wire body as a compatibility fact, but every drawn
// choice is false and no choice carries advanced-analysis consent: choosing or resetting a preset
// never grants or revokes it.
const sourceReadPresets:Readonly<Record<SourceReadPreset,SourceReadValues>>=Object.freeze({
 list:{scanDepth:'file_list_only',wholeFileReads:false,connections:2,requestsPerSecond:2,bytesPerSecond:0},
 once:{scanDepth:'basic',wholeFileReads:false,connections:4,requestsPerSecond:4,bytesPerSecond:0},
 everything:{scanDepth:'complete',wholeFileReads:false,connections:8,requestsPerSecond:10,bytesPerSecond:0},
 ask:{scanDepth:'basic',wholeFileReads:false,connections:2,requestsPerSecond:2,bytesPerSecond:0},
});
/** The drawn S3 form's fields. The owner fills these in; Portico writes the
 * rclone configuration section, so nobody types rclone's file format. */
export type S3SourceFields=Readonly<{endpoint:string;region:string;accessKey:string;secretKey:string;bucket:string;folder:string}>;
export type S3SourceDraft=Readonly<{remote:string;config:string}>;
const s3Value=(value:string,max:number):boolean=>value.length>=1&&value.length<=max&&!/[\r\n\u0000]/.test(value);
const s3FolderPart=/^[^\\/\r\n\u0000]{1,255}$/;
// Root-only grammar: scheme and authority, an optional bare slash, nothing that
// could normalise into a path, query, fragment or userinfo.
const s3EndpointShape=/^https:\/\/(?:[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?|\[[0-9a-fA-F:.]+\])(?::([0-9]{1,5}))?\/?$/i;
/**
 * One S3-compatible source as the exact section the server's own validator and
 * candidate test accept. Undefined means a field is not usable; nothing is
 * guessed or partly written. The display name is not part of the section.
 */
export function s3SourceDraft(fields:S3SourceFields):S3SourceDraft|undefined{
 const endpoint=fields.endpoint.trim();
 const shape=s3EndpointShape.exec(endpoint);
 if(!shape)return undefined;
 if(shape[1]!==undefined){const port=Number(shape[1]);if(port<1||port>65535)return undefined;}
 let url:URL;
 try{url=new URL(endpoint);}catch{return undefined;}
 if(url.protocol!=='https:'||!url.hostname||url.pathname!=='/'||url.search||url.hash||url.username||url.password)return undefined;
 const region=fields.region.trim();
 if(region&&!/^[a-z0-9-]{1,32}$/i.test(region))return undefined;
 const accessKey=fields.accessKey.trim();
 if(!s3Value(accessKey,256))return undefined;
 if(!s3Value(fields.secretKey,1024))return undefined;
 const bucket=fields.bucket.trim();
 if(!/^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$/.test(bucket))return undefined;
 const folder=fields.folder.trim().replace(/^\/+|\/+$/g,'');
 if(folder&&(folder.length>1024||!folder.split('/').every(part=>part!=='.'&&part!=='..'&&s3FolderPart.test(part))))return undefined;
 const config=['[portico]','type = s3','provider = Other',`access_key_id = ${accessKey}`,`secret_access_key = ${fields.secretKey}`,`endpoint = ${endpoint}`,...(region?[`region = ${region}`]:[])].join('\n')+'\n';
 return Object.freeze({remote:`portico:${bucket}${folder?`/${folder}`:''}`,config});
}

/** The drawn Google Drive form: the owner's own service-account key and where
 * the files are. Portico writes the rclone section; the key is never shown back. */
export type DriveKeyFields=Readonly<{keyJson:string;location:'folder'|'drive';locationId:string}>;
export type DriveKeyDraft=Readonly<{remote:string;config:string;email:string}>;
export type DriveKeyResult=Readonly<{ok:true;draft:DriveKeyDraft}|{ok:false;reason:'json'|'oauth'|'type'|'email'|'key'|'location'}>;
const driveLocationId=/^[A-Za-z0-9_-]{1,200}$/;
/**
 * One Google Drive source signed in with the owner's own service-account key.
 * The key is parsed, never authored: it must be a `service_account` JSON object
 * with a real address and private key, and the section carries it compactly so
 * it is one INI value. OAuth client files are told apart, not accepted here.
 */
export function driveServiceAccountDraft(fields:DriveKeyFields):DriveKeyResult{
 let parsed:unknown;
 try{parsed=JSON.parse(fields.keyJson);}catch{return Object.freeze({ok:false,reason:'json'});}
 if(!record(parsed))return Object.freeze({ok:false,reason:'json'});
 if(parsed.type!=='service_account')return Object.freeze({ok:false,reason:record(parsed.installed)||record(parsed.web)?'oauth':'type'});
 const email=parsed.client_email;
 if(!text(email,320)||!/^[^@\s]+@[^@\s]+$/.test(email))return Object.freeze({ok:false,reason:'email'});
 const key=parsed.private_key;
 if(typeof key!=='string'||key.length<64||key.length>16_384||!key.includes('BEGIN PRIVATE KEY'))return Object.freeze({ok:false,reason:'key'});
 const locationId=fields.locationId.trim();
 if(!driveLocationId.test(locationId))return Object.freeze({ok:false,reason:'location'});
 const credentials=JSON.stringify(parsed);
 if(credentials.length>60_000||/[\r\n]/.test(credentials))return Object.freeze({ok:false,reason:'key'});
 const target=fields.location==='drive'?`team_drive = ${locationId}`:`root_folder_id = ${locationId}`;
 const config=['[portico]','type = drive','scope = drive.readonly',`service_account_credentials = ${credentials}`,target].join('\n')+'\n';
 return Object.freeze({ok:true,draft:Object.freeze({remote:'portico:',config,email})});
}

/** The drawn cloud-drive token form: a drive signed in on another computer and
 * the token that sign-in produced. Portico never starts that sign-in itself. */
export type CloudTokenFields=Readonly<{provider:'onedrive'|'dropbox';token:string;folder:string}>;
export type CloudTokenDraft=Readonly<{remote:string;config:string}>;
export type CloudTokenResult=Readonly<{ok:true;draft:CloudTokenDraft}|{ok:false;reason:'json'|'token'|'client'|'google'|'folder'}>;
const cloudFolderPart=/^[^\\/\r\n\u0000]{1,255}$/;
/**
 * One OneDrive or Dropbox source from the token rclone produced on another
 * computer. The token is parsed and must hold a real access and refresh token;
 * an OAuth client file or a Google key is told apart, never accepted as one.
 */
export function cloudTokenDraft(fields:CloudTokenFields):CloudTokenResult{
 let parsed:unknown;
 try{parsed=JSON.parse(fields.token);}catch{return Object.freeze({ok:false,reason:'json'});}
 if(!record(parsed))return Object.freeze({ok:false,reason:'json'});
 if(record(parsed.installed)||record(parsed.web))return Object.freeze({ok:false,reason:'client'});
 if(typeof parsed.type==='string')return Object.freeze({ok:false,reason:'google'});
 if(!text(parsed.access_token,8192)||!parsed.access_token||!text(parsed.refresh_token,8192)||!parsed.refresh_token)return Object.freeze({ok:false,reason:'token'});
 const folder=fields.folder.trim().replace(/^\/+|\/+$/g,'');
 if(folder&&(folder.length>1024||!folder.split('/').every(part=>part!=='.'&&part!=='..'&&cloudFolderPart.test(part))))return Object.freeze({ok:false,reason:'folder'});
 const compact=JSON.stringify(parsed);
 if(compact.length>8192||/[\r\n]/.test(compact))return Object.freeze({ok:false,reason:'token'});
 const kind=fields.provider==='dropbox'?'dropbox':'onedrive';
 const config=['[portico]',`type = ${kind}`,`token = ${compact}`].join('\n')+'\n';
 return Object.freeze({ok:true,draft:Object.freeze({remote:`portico:${folder}`,config})});
}

/** The drawn OAuth-client Drive form: the owner's own client and the token the
 * server-run sign-in answered. */
export type DriveOAuthFields=Readonly<{clientId:string;clientSecret:string;token:string;location:'folder'|'drive';locationId:string}>;
export type DriveOAuthDraft=Readonly<{remote:string;config:string}>;
export type DriveOAuthDraftResult=Readonly<{ok:true;draft:DriveOAuthDraft}|{ok:false;reason:'client'|'token'|'location'}>;
export function driveOAuthDraft(fields:DriveOAuthFields):DriveOAuthDraftResult{
 const clientId=fields.clientId.trim();
 if(!text(clientId,512)||clientId.length<8)return Object.freeze({ok:false,reason:'client'});
 if(!text(fields.clientSecret,2048)||fields.clientSecret.length<8)return Object.freeze({ok:false,reason:'client'});
 let parsed:unknown;
 try{parsed=JSON.parse(fields.token);}catch{return Object.freeze({ok:false,reason:'token'});}
 if(!record(parsed)||!text(parsed.access_token,8192)||!parsed.access_token||!text(parsed.refresh_token,8192)||!parsed.refresh_token)return Object.freeze({ok:false,reason:'token'});
 const compact=JSON.stringify(parsed);
 if(compact.length>16_384||/[\r\n]/.test(compact))return Object.freeze({ok:false,reason:'token'});
 const locationId=fields.locationId.trim();
 if(!driveLocationId.test(locationId))return Object.freeze({ok:false,reason:'location'});
 const target=fields.location==='drive'?`team_drive = ${locationId}`:`root_folder_id = ${locationId}`;
 const config=['[portico]','type = drive','scope = drive.readonly',`client_id = ${clientId}`,`client_secret = ${fields.clientSecret}`,`token = ${compact}`,target].join('\n')+'\n';
 return Object.freeze({ok:true,draft:Object.freeze({remote:'portico:',config})});
}

/** The four drawn choices, exactly the server's own SourceReadPreset values. */
export function sourceReadPreset(name:SourceReadPreset):SourceReadValues{return Object.freeze({...sourceReadPresets[name]});}
export function sourceReadPresetName(values:SourceReadValues):SourceReadPreset|undefined{
 return (Object.keys(sourceReadPresets) as SourceReadPreset[]).find(name=>sameSourceRead(values,sourceReadPresets[name]));
}
function sameSourceRead(a:SourceReadValues,b:SourceReadValues):boolean{
 // The legacy whole-file flag and the independent analysis consent never decide which drawn
 // choice is in force, so a preset comparison cannot depend on either.
 return a.scanDepth===b.scanDepth&&a.connections===b.connections&&a.requestsPerSecond===b.requestsPerSecond&&a.bytesPerSecond===b.bytesPerSecond;
}
function sourceReadValues(raw:unknown):SourceReadValues{
 if(!record(raw))invalid();
 const scanDepth=raw.scanDepth, wholeFileReads=raw.wholeFileReads, connections=raw.connections, requestsPerSecond=raw.requestsPerSecond, bytesPerSecond=raw.bytesPerSecond, allowAdvancedMediaAnalysis=raw.allowAdvancedMediaAnalysis;
 if((scanDepth!=='file_list_only'&&scanDepth!=='basic'&&scanDepth!=='complete')||typeof wholeFileReads!=='boolean'||!integer(connections)||connections<1||connections>32||!integer(requestsPerSecond)||requestsPerSecond<1||requestsPerSecond>50||!integer(bytesPerSecond)||!(bytesPerSecond===0||bytesPerSecond>=1_000_000&&bytesPerSecond<=1_000_000_000&&bytesPerSecond%1_000_000===0))invalid();
 // A present consent is an exact boolean; an older server's absence stays unknown rather than false.
 if(allowAdvancedMediaAnalysis!==undefined&&typeof allowAdvancedMediaAnalysis!=='boolean')invalid();
 return Object.freeze({scanDepth,wholeFileReads,connections,requestsPerSecond,bytesPerSecond,...(allowAdvancedMediaAnalysis===undefined?{}:{allowAdvancedMediaAnalysis})});
}
function sourceReadSettings(raw:unknown):SourceReadSettings{
 if(!record(raw)||!text(raw.sourceId,256)||!raw.sourceId||!text(raw.generation,64)||!raw.generation||!integer(raw.revision)||typeof raw.saved!=='boolean')invalid();
 const configurationGeneration=raw.configurationGeneration;
 if(configurationGeneration!==undefined&&(typeof configurationGeneration!=='string'||!/^[1-9][0-9]{0,18}$/.test(configurationGeneration)||configurationGeneration.length>19))invalid();
 const choiceDefinitions=parseSourceChoiceDefinitions(raw.choiceDefinitions);
 const choiceApplicableFields=parseSourceChoiceApplicableFields(raw.choiceApplicableFields);
 const choiceBaseline=parseSourceChoiceBaseline(raw.choiceBaseline);
 return Object.freeze({sourceId:raw.sourceId,generation:raw.generation,revision:raw.revision,saved:raw.saved,values:sourceReadValues(raw.values),
  ...(configurationGeneration===undefined?{}:{configurationGeneration}),
  ...(choiceDefinitions===undefined?{}:{choiceDefinitions}),
  ...(choiceApplicableFields===undefined?{}:{choiceApplicableFields}),
  ...(choiceBaseline===undefined?{}:{choiceBaseline})});
}
function remoteFolder(raw:unknown):RemoteFolder{
 if(!record(raw)||!text(raw.name,255)||!raw.name||!text(raw.path)||!raw.path||!text(raw.relative)||typeof raw.hidden!=='boolean')invalid();
 return Object.freeze({name:raw.name,path:raw.path,relative:raw.relative,hidden:raw.hidden});
}
function remoteFolderPage(raw:unknown,id:string):RemoteFolderPage{
 if(!record(raw)||raw.sourceId!==id||!text(raw.generation,64)||!raw.generation||!text(raw.path)||!text(raw.relative)||!Array.isArray(raw.entries)||!text(raw.nextCursor,255)||typeof raw.truncated!=='boolean')invalid();
 return Object.freeze({sourceId:id,generation:raw.generation,path:raw.path,relative:raw.relative,entries:Object.freeze(raw.entries.map(remoteFolder)),nextCursor:raw.nextCursor,truncated:raw.truncated});
}
function sourceLibraryUse(raw:unknown):SourceLibraryUse{
 if(!record(raw)||!text(raw.libraryId,256)||!raw.libraryId||!text(raw.name,512)||!text(raw.kind,32)||!integer(raw.folders)||raw.folders<1)invalid();
 const lastComplete=scanTimestamp(raw.lastCompleteAt);
 return Object.freeze({libraryId:raw.libraryId,name:raw.name,kind:raw.kind,folders:raw.folders,...(lastComplete?{lastCompleteAt:lastComplete}:{})});
}
function sourceAdvanced(raw:unknown):SourceAdvanced{
 if(!record(raw))invalid();
 const {scanIntervalSeconds,watchForChanges,scanOnStart,fullRecheckDays,cacheBytes,cacheFloorBytes,seekPreviews,chapterImages,waveforms,loudness,checksums,doubleCheckFolders,dirCacheSeconds,readBufferBytes,connectTimeoutSeconds,idleTimeoutSeconds,retries,lowLevelRetries}=raw;
 if(!integer(scanIntervalSeconds)||![0,900,3600,21600,86400,604800].includes(scanIntervalSeconds))invalid();
 if(typeof watchForChanges!=='boolean'||typeof scanOnStart!=='boolean')invalid();
 if(!integer(fullRecheckDays)||![0,7,14,30].includes(fullRecheckDays))invalid();
 if(!integer(cacheBytes)||cacheBytes>64*1024*1024*1024||!integer(cacheFloorBytes)||cacheFloorBytes>1024*1024*1024*1024)invalid();
 if(![seekPreviews,chapterImages,waveforms,loudness,checksums,doubleCheckFolders].every(value=>typeof value==='boolean'))invalid();
 const introsMode=raw.introsMode;
 if(introsMode!=='off'&&introsMode!=='inherit'&&introsMode!=='sound'&&introsMode!=='picture')invalid();
 for(const value of [dirCacheSeconds,readBufferBytes,connectTimeoutSeconds,idleTimeoutSeconds,retries,lowLevelRetries])if(!integer(value))invalid();
 return Object.freeze({scanIntervalSeconds,watchForChanges,scanOnStart,fullRecheckDays,cacheBytes,cacheFloorBytes,seekPreviews,chapterImages,waveforms,loudness,checksums,introsMode,doubleCheckFolders,dirCacheSeconds,readBufferBytes,connectTimeoutSeconds,idleTimeoutSeconds,retries,lowLevelRetries}) as SourceAdvanced;
}
function sourceAdvancedSettings(raw:unknown,id:string):SourceAdvancedSettings{
 if(!record(raw)||raw.sourceId!==id||!text(raw.generation,64)||!raw.generation||!integer(raw.revision)||typeof raw.saved!=='boolean'||typeof raw.cacheSupported!=='boolean'||typeof raw.cacheApplied!=='boolean'||!integer(raw.cacheUsedBytes)||!integer(raw.scanIntervalObserved))invalid();
 if(raw.cacheError!==undefined&&!['source_changed','mount_unavailable','cache_refused','cache_record_failed'].includes(String(raw.cacheError)))invalid();
 return Object.freeze({sourceId:id,generation:raw.generation,revision:raw.revision,saved:raw.saved,cacheSupported:raw.cacheSupported,cacheApplied:raw.cacheApplied,...(raw.cacheError===undefined?{}:{cacheError:raw.cacheError as string}),cacheUsedBytes:raw.cacheUsedBytes,scanIntervalObserved:raw.scanIntervalObserved,values:sourceAdvanced(raw.values)});
}
const scanStates:readonly RemoteScanStateCode[]=['queued','running','paused'];
const scanModes:readonly RemoteScanMode[]=['first-import','incremental','integrity','full'];
const scanPhases:readonly RemoteScanPhase[]=['inventory','legacy_inventory','basic_pending','basic_running','analysis_enqueue','reconciling','sidecars','verifying','extras'];
function scanTimestamp(value:unknown):string|undefined{
 if(typeof value!=='string')return undefined;
 const match=/^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d{1,9})?(?:Z|([+-])(\d{2}):(\d{2}))$/.exec(value);
 if(!match)return undefined;
 const year=Number(match[1]),month=Number(match[2]),day=Number(match[3]),hour=Number(match[4]),minute=Number(match[5]),second=Number(match[6]),offsetHour=Number(match[8]??0),offsetMinute=Number(match[9]??0);
 const monthDays=[31,year%4===0&&(year%100!==0||year%400===0)?29:28,31,30,31,30,31,31,30,31,30,31];
 if(month<1||month>12||day<1||day>monthDays[month-1]||hour>23||minute>59||second>59||offsetHour>23||offsetMinute>59)return undefined;
 return Number.isFinite(Date.parse(value))?value:undefined;
}
const scanBounded=(value:unknown):value is number=>integer(value)&&value<=2**40;
const sourceHealths:readonly RemoteSourceHealth[]=['ready','healthy','degraded','inventory_running','cancelled','offline','root_changed','removing'];
const contactCodes:readonly RemoteContactErrorCode[]=['offline','credentials_required','rate_limited','quota_exceeded'];
function remoteScan(raw:unknown):RemoteScanState|undefined{
 if(!record(raw)||!scanStates.includes(raw.state as RemoteScanStateCode)||!scanBounded(raw.sourceGeneration)||!scanBounded(raw.runPolicyRevision)||!scanBounded(raw.processed)||!scanBounded(raw.discovered)||!scanBounded(raw.analyzed)||!scanBounded(raw.warnings))return undefined;
 const queued=scanTimestamp(raw.queuedAt),completed=scanTimestamp(raw.completedAt),finished=scanTimestamp(raw.finishedAt);
 return Object.freeze({state:raw.state as RemoteScanStateCode,...(scanModes.includes(raw.mode as RemoteScanMode)?{mode:raw.mode as RemoteScanMode}:{}),...(scanPhases.includes(raw.phase as RemoteScanPhase)?{phase:raw.phase as RemoteScanPhase}:{}),sourceGeneration:raw.sourceGeneration,runPolicyRevision:raw.runPolicyRevision,processed:raw.processed,discovered:raw.discovered,analyzed:raw.analyzed,warnings:raw.warnings,...(queued?{queuedAt:queued}:{}),...(completed?{completedAt:completed}:{}),...(finished?{finishedAt:finished}:{})});
}
function remoteSourceStatus(raw:unknown,id:string):RemoteSourceStatus{
 if(!record(raw)||raw.sourceId!==id||!text(raw.generation,64)||!raw.generation||!text(raw.state,100)||!text(raw.rangeSupport,32)||!integer(raw.connectionsInUse)||!integer(raw.playbackReads)||!integer(raw.analysisReads)||!integer(raw.scanIntervalSeconds)||!Array.isArray(raw.libraries)||raw.libraries.length>10||typeof raw.librariesTruncated!=='boolean')invalid();
 if(raw.errorCode!==undefined&&!text(raw.errorCode,100))invalid();
 const lastChecked=scanTimestamp(raw.lastCheckedAt),serveOperations=integer(raw.serveOperationsInUse)?raw.serveOperationsInUse:undefined,scan=remoteScan(raw.scan),health=sourceHealths.includes(raw.health as RemoteSourceHealth)?raw.health as RemoteSourceHealth:undefined,lastComplete=scanTimestamp(raw.lastCompleteAt),connected=scanTimestamp(raw.lastConnectedAt),errorAt=scanTimestamp(raw.lastErrorAt),errorCode=contactCodes.includes(raw.lastErrorCode as RemoteContactErrorCode)?raw.lastErrorCode as RemoteContactErrorCode:undefined;
 return Object.freeze({sourceId:id,generation:raw.generation,state:raw.state,...(raw.errorCode===undefined?{}:{errorCode:raw.errorCode as string}),rangeSupport:raw.rangeSupport,...(lastChecked?{lastCheckedAt:lastChecked}:{}),connectionsInUse:raw.connectionsInUse,playbackReads:raw.playbackReads,analysisReads:raw.analysisReads,...(serveOperations!==undefined?{serveOperationsInUse:serveOperations}:{}),scanIntervalSeconds:raw.scanIntervalSeconds,libraries:Object.freeze(raw.libraries.map(sourceLibraryUse)),librariesTruncated:raw.librariesTruncated,
  ...(text(raw.program,8192)&&raw.program?{program:raw.program}:{}),...(text(raw.programVersion,256)&&raw.programVersion?{programVersion:raw.programVersion}:{}),
  ...(integer(raw.programRevision)&&raw.programRevision>0?{programRevision:raw.programRevision}:{}),
  ...(typeof raw.programIncluded==='boolean'?{programIncluded:raw.programIncluded}:{}),
  ...(health?{health}:{}),...(lastComplete?{lastCompleteAt:lastComplete}:{}),...(connected?{lastConnectedAt:connected}:{}),...(errorAt&&errorCode?{lastErrorAt:errorAt,lastErrorCode:errorCode}:{}),
  ...(scan?{scan}:{}),
  ...(text(raw.remote,8192)&&raw.remote?{remote:raw.remote}:{}),...(text(raw.backgroundSpeed,64)&&raw.backgroundSpeed?{backgroundSpeed:raw.backgroundSpeed}:{})});
}
/** `alreadyDone`: the last command found, on a fresh read, that what it asked for is already so (a share removed elsewhere).
 * `operationKind` picks the receipt route that command's outcome is recovered from. */
export type RemoteState=Readonly<{sources:readonly RemoteSource[];loading:boolean;busy:boolean;error:string;errorCode:string;operationId:string|null;operationKind:'source'|'rclone'|'';ambiguous:boolean;receipt:RemoteReceipt|null;rclone:RcloneReceipt|null;alreadyDone:boolean;mountSupported:boolean}>;
const record=(x:unknown):x is Record<string,unknown>=>!!x&&typeof x==='object'&&!Array.isArray(x);
const text=(x:unknown,n=8192):x is string=>typeof x==='string'&&x.length<=n&&!/[\x00-\x1f\x7f]/.test(x);
/** Configuration is multi-line INI, so line feeds and tabs are the only control
 * characters it may carry; everything else is refused before it is sent. */
const configText=(x:unknown,n=65536):x is string=>typeof x==='string'&&x.length<=n&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(x);
const integer=(x:unknown):x is number=>typeof x==='number'&&Number.isSafeInteger(x)&&x>=0;
function invalid():never{throw new Error('Invalid remote source response.');}
/** Display-only saved endpoint fact. This never authorizes a connection. Native
 * schemes remain distinct from HTTP, including inside imported rclone sources. */
function sourceOrigin(value:string,kind:RemoteSource['kind']):boolean{
 const match=/^(https?|sftp|smb):\/\/([^/?#@%\\\s]+)\/?$/.exec(value);
 if(!match)return false;
 const scheme=match[1];
 if(kind==='webdav'&&scheme!=='http'&&scheme!=='https'||kind==='sftp'&&scheme!=='sftp'||kind==='smb'&&scheme!=='smb')return false;
 try{
  const url=new URL(value);
  if(!url.hostname||url.username||url.password||url.search||url.hash||url.pathname&&url.pathname!=='/')return false;
  if(scheme==='sftp'||scheme==='smb'){
   if(!/^(?:\[[0-9a-f:.]+\]|[a-z0-9.-]+):[0-9]{1,5}$/.test(match[2])||!url.port||Number(url.port)<1||Number(url.port)>65535)return false;
  }
  return true;
 }catch{return false;}
}
function source(x:unknown):RemoteSource{
 if(!record(x)||!text(x.id,256)||!x.id||!['rclone','webdav','sftp','smb'].includes(String(x.kind))||!text(x.name,512)||!text(x.rootPath)||!integer(x.generation)||x.generation<1||!text(x.state,100)||!x.state||typeof x.insecureLocal!=='boolean'||typeof x.credentialPresent!=='boolean'||x.password!==undefined||x.config!==undefined||x.username!==undefined||x.root!==undefined)invalid();
 if(x.origin!==undefined&&(!text(x.origin)||!sourceOrigin(x.origin,x.kind as RemoteSource['kind'])))invalid();
 // A state this app does not know is kept as the server named it, an error code it does not know is left off,
 // and range support it does not know reads as unknown. Credentials are never part of a read.
 const errorCode=text(x.errorCode,100)&&['source_changed','credentials_required','binary_changed','range_unsupported','invalid_configuration','cursor_expired','budget_exceeded','rate_limited','quota_exceeded','cancelled','offline'].includes(x.errorCode)?x.errorCode:undefined;if(x.errorCode!==undefined&&errorCode===undefined)noteSkipped('remote-sources','source.errorCode');
 if(x.mountId!==undefined&&!text(x.mountId,256))invalid();
 return Object.freeze({id:x.id,kind:x.kind as RemoteSource['kind'],name:x.name,rootPath:x.rootPath,generation:x.generation,state:x.state,rangeSupport:oneOfOr('remote-sources','source.rangeSupport',x.rangeSupport,['unknown','conditional','unsupported','sequential'] as const,'unknown'),insecureLocal:x.insecureLocal,credentialPresent:x.credentialPresent,...(x.origin===undefined?{}:{origin:x.origin as string}),...(errorCode===undefined?{}:{errorCode}),...(x.mountId===undefined?{}:{mountId:x.mountId as string})});
}
const messages:Record<string,string>={remote_source_changed:'This source changed. Refresh before submitting a new configuration.',storage_command_conflict:'The source changed or this operation conflicts with a previous receipt.',storage_operation_expired:'The operation expired. Check the device clock and refresh.',remote_credentials_required:'The provider rejected the credentials. The previous configuration remains active.',source_credentials_required:'The provider rejected the credentials. The previous configuration remains active.',remote_invalid_configuration:'Check the endpoint, approved network access and configuration.',remote_range_unsupported:'The server cannot safely range-read this provider representation.',remote_binary_changed:'The custom rclone program stopped working. Choose another program or use the included rclone.',remote_budget_exceeded:'This operation exceeded the configured safety budget.',remote_rate_limited:'The provider or server is busy. Check the original receipt before retrying.',source_folders_unsupported:'Folder browsing inside a mounted source is not available on this server.',storage_executable_invalid:"That isn't rclone. Choose the rclone program.",storage_executable_too_old:'This rclone is too old. Choose a newer rclone.',revision_mismatch:'This source changed. Refresh before saving.',idempotency_key_reused:'This operation was already used. Refresh and try again.',request_in_progress:'Another source change is in progress. Try again in a moment.',refresh_failed:'The profile was saved, but the refreshed values could not be read.',remote_administration_unavailable:'The rclone program on this server is unavailable. Check the server, then test again.',remote_source_not_listed:'The source was saved, but the list does not agree with the receipt yet. Refresh before continuing.',unauthorized:'Owner sign-in is required.',forbidden:'This viewer cannot manage sources.'};
const CONFLICT='storage_command_conflict';
const ERROR_CODES=new Set(['remote_source_changed','storage_command_conflict','storage_operation_expired','remote_credentials_required','remote_invalid_configuration','remote_range_unsupported','remote_binary_changed','remote_budget_exceeded','remote_rate_limited','revision_mismatch','idempotency_key_reused','request_in_progress','refresh_failed','source_credentials_required','administration_unavailable','remote_administration_unavailable','remote_source_not_listed','storage_command_capacity','storage_allocation_recovery_required','unauthorized','forbidden','not_permitted','invalid_request','source_folders_unsupported','storage_executable_invalid','storage_executable_too_old','internal']);
// A legacy storage route answers a busy source with remote_busy: the same retry a
// v1 route presents as request_in_progress. Recognise the existing code for this
// UI without changing what that route returns to its older consumers.
const BUSY_CODE_ALIASES:Record<string,string>={remote_busy:'request_in_progress'};
function normalizedCode(value:unknown):string{if(typeof value!=='string')return 'internal';const code=BUSY_CODE_ALIASES[value]??value;return ERROR_CODES.has(code)?code:'internal';}

/**
 * An edit carried onto the share as it is now, after someone else changed it. What the owner left
 * alone takes the share's new value; what they changed keeps theirs. Undefined when they changed
 * something that was also changed underneath (the one case worth telling them about). The
 * connection is one thing: its address and whether it has a password; credentials are never read.
 */
export function rebaseDavEdit(base:RemoteSource,fresh:RemoteSource,input:DAVConfiguration):DAVConfiguration|undefined{
 const connection=(x:RemoteSource)=>`${x.origin??''}|${x.credentialPresent}`;
 const mine=!input.keepConnection||!input.keepPassword;
 const shape=(x:RemoteSource)=>({name:x.name,insecureLocal:x.insecureLocal,connection:connection(x)});
 const {merged,conflict}=rebaseDraft(shape(base),{name:input.name.trim(),insecureLocal:input.insecureLocal,connection:mine?'edited':connection(base)},shape(fresh));
 if(conflict)return undefined;
 return {...input,name:merged.name,insecureLocal:merged.insecureLocal,expectedGeneration:fresh.generation};
}
function tooOldVersion(value:unknown):string|undefined{return typeof value==='string'&&/^\d{1,4}(?:\.\d{1,4}){2}(?:[-+][0-9A-Za-z.+-]{1,32})?$/.test(value)?value:undefined;}
function safeError(e:unknown):string{
 const raw=record(e)?e:undefined,code=raw&&typeof raw.code==='string'?normalizedCode(raw.code):'';
 if(code==='storage_executable_too_old'){
  const version=tooOldVersion(raw?.version),minimum=tooOldVersion(raw?.minimumVersion);
  if(version&&minimum)return `This rclone is too old (${version}). Choose ${minimum} or newer.`;
 }
 return messages[code]??'The operation outcome is not confirmed. Check the original receipt; credentials will not be resent automatically.';
}
export type SourceChoiceOpening=Readonly<{signal:AbortSignal;isCurrent:()=>boolean;consentDirty:false}>;
/** Issued once and retained by identity; a structural clone cannot recover it. */
export type SourceChoiceIntent=Readonly<{operationId:string;body:ReturnType<typeof sourceChoicePutBody>;command:SourceChoiceCommand}>;
export type SourceChoiceApplyResult=
 |Readonly<{kind:'applied';operationId:string;settings:SourceReadSettings;accepted:SourceChoiceAcceptance;comparison:SourceChoiceComparison}>
 |Readonly<{kind:'accepted-unavailable';operationId:string;settings:SourceReadSettings;accepted:SourceChoiceAcceptance;error?:unknown}>
 |Readonly<{kind:'unknown';intent:SourceChoiceIntent;error:unknown}>;
type ChoiceCustody={intent:SourceChoiceIntent;epoch:number;opening:SourceChoiceOpening;scope?:UserMutationScope;parent?:Readonly<{receipt:RemoteReceipt;accepted:SourceChoiceAcceptance}>};
type ChoiceRequest=Readonly<{token:object;signal:AbortSignal;onDispatch?:()=>void}>;
function definiteRefusal(error:unknown):boolean{
 if(error instanceof UserMutationError||record(error)&&typeof error.mutationKey==='string'&&/^p251_/.test(error.mutationKey))return false;
 const code=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'';
 return ['invalid_request','unauthorized','forbidden','not_permitted','revision_mismatch','idempotency_key_reused'].includes(code);
}
export class RemoteSourceService{
 private state:RemoteState={sources:[],loading:false,busy:false,error:'',errorCode:'',operationId:null,operationKind:'',ambiguous:false,receipt:null,rclone:null,alreadyDone:false,mountSupported:false};
 private listeners=new Set<()=>void>();private controllers=new Set<AbortController>();private epoch=0;private read=0;private disposed=false;private viewerFence?:string;
 private intent:SourceCommandIntent|null=null;
 // One nonsecret settings command belongs to this service opening. The durable
 // coordinator remains the only restart/retry owner; this merely retains the
 // caller's original ID and parsed answer until its acknowledgement persists.
 private settingsPending?:{path:string;payload:string;operationId:Promise<string>;dispatched:boolean;scope?:UserMutationScope;profile?:{receipt?:RemoteReceipt;settings?:SourceReadSettings};program?:SourceProgram};
 private async releaseReviewedSettings(){
  await this.api.awaitUserMutationsReady?.();
  const scope=this.api.getUserMutationScope?.(),coordinator=this.api.getUserMutationCoordinator?.(),prior=this.settingsPending;
  if(prior?.scope&&(!scope||!sameUserMutationScope(prior.scope,scope)))throw new UserMutationError('mutation_scope_changed');
  if(prior?.dispatched&&prior.scope&&coordinator){
   const op=await prior.operationId;
   if(!(await coordinator.inspectIntent(op))&&this.settingsPending===prior)this.settingsPending=undefined;
  }
 }
 private async settingsIntent(path:string,payload:object){
  await this.releaseReviewedSettings();
  const scope=this.api.getUserMutationScope?.();
  const serialized=JSON.stringify(payload);
  if(this.settingsPending&&(this.settingsPending.path!==path||this.settingsPending.payload!==serialized))throw new UserMutationError('mutation_needs_review');
  if(!this.settingsPending){
   const pending={path,payload:serialized,operationId:Promise.resolve().then(()=>this.operationId()),dispatched:false,...(scope?{scope}: {})};
   this.settingsPending=pending;
   void pending.operationId.catch(()=>{if(this.settingsPending===pending)this.settingsPending=undefined;});
  }
  const pending=this.settingsPending,op=await pending.operationId;
  if(!/^\d{13}-[0-9a-f-]{36}$/i.test(op)){if(this.settingsPending===pending)this.settingsPending=undefined;invalid();}
  return {pending,op,body:{operationId:op,...JSON.parse(pending.payload)}};
 }
 /** An owner refusal retires this viewer's source capability here: no later
  * request, read or command, is sent with it, and nothing already in flight can
  * publish into this viewer's snapshot. A new service reads afresh. */
 private refused=false;private refusedCode='';
 private api:LibraryContentApi;private scope:ContentScope;private operationId:()=>Promise<string>;private readonly isCurrent:()=>boolean;
 constructor(api:LibraryContentApi,scope:ContentScope,operationId:()=>Promise<string>,isCurrent:()=>boolean=()=>true){if(!scope.serverId||!scope.viewerId)throw new Error('Owner scope required.');this.api=api;this.scope=Object.freeze({...scope});this.operationId=operationId;this.isCurrent=isCurrent;}
 getSnapshot=()=>this.state;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn);};
 private live(){return !this.disposed&&this.isCurrent();}
 /** The refusal a failed transport carries, if it is one: an auth code, or a 401/403 status. */
 private refusalMessage(code:string):string{return code==='not_permitted'?messages.forbidden:(messages[code]??messages.unauthorized);}
 private refusalCode(error:unknown):string{
  const raw=record(error)?error:undefined;
  const code=raw&&typeof raw.code==='string'?normalizedCode(raw.code):'';
  if(['unauthorized','forbidden','not_permitted'].includes(code))return code;
  const status=raw?(typeof raw.status==='number'?raw.status:typeof raw.statusCode==='number'?raw.statusCode:NaN):NaN;
  if(status===401)return 'unauthorized';
  if(status===403)return 'forbidden';
  return '';
 }
 /** A refusal retires the capability at once: later reads and commands are
  * refused, in-flight transports are aborted, and the snapshot is emptied before
  * any captured UI handler reports it. Nothing publishes afterwards. */
 private retire(code:string){
  if(this.refused)return;
  this.refused=true;this.refusedCode=code;
  this.intent=null;this.choicePending=undefined;this.choiceFlight=undefined;this.settingsPending=undefined;
  this.epoch++;
  for(const c of this.controllers)c.abort();
  this.controllers.clear();
  this.state=Object.freeze({...this.state,sources:[],loading:false,busy:false,receipt:null,rclone:null,operationId:null,operationKind:'',ambiguous:false,alreadyDone:false,error:this.refusalMessage(code),errorCode:code});
  for(const fn of this.listeners)fn();
 }
 private refuse(error:unknown){const code=this.refusalCode(error);if(code)this.retire(code);}
 private publish(value:Partial<RemoteState>){if(this.refused||!this.live())return;this.state=Object.freeze({...this.state,...value});for(const fn of this.listeners)fn();}
 dispose(){this.epoch++;this.disposed=true;this.choicePending=undefined;this.choiceFlight=undefined;this.settingsPending=undefined;this.intent=null;this.refused=false;this.refusedCode='';for(const c of this.controllers)c.abort();this.controllers.clear();this.listeners.clear();this.state={...this.state,sources:[],receipt:null,rclone:null,operationId:null,operationKind:''};}
 private fence(raw:unknown){if(!record(raw)||!record(raw.scope)||raw.scope.serverId!==this.scope.serverId||!text(raw.scope.viewerFence,256)||!raw.scope.viewerFence||this.viewerFence&&this.viewerFence!==raw.scope.viewerFence)invalid();this.viewerFence=raw.scope.viewerFence;}
 /** Ordinary mutations retain their exclusion through operation-id waits and
  * callbacks. Outside a choice, their existing admission/behavior is unchanged. */
 private ordinaryMutations=0;private activeWrites=0;
 private holdOrdinaryMutation():()=>void{
  if(this.choiceFlight||this.choicePending)throw new Error('Finish or recover the current source command first.');
  this.ordinaryMutations++;
  return ()=>{this.ordinaryMutations--;};
 }
 private async request(path:string,method='GET',body?:unknown,guard?:()=>boolean,choice?:ChoiceRequest):Promise<unknown>{
  if(this.refused)throw Object.assign(new Error(messages[this.refusedCode]??messages.unauthorized),{code:this.refusedCode||'unauthorized'});
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  const write=method!=='GET';
  if(write&&(this.choiceFlight||this.choicePending)&&(!choice||!this.choiceFlight||choice.token!==this.choiceFlight))throw new Error('Finish or recover the current source command first.');
  if(choice&&this.choiceFlight!==choice.token)throw Object.assign(new Error('Scope changed.'),{code:'retired'});
  const c=new AbortController();this.controllers.add(c);let timer:ReturnType<typeof setTimeout>|undefined;
  let rejectAbort:((reason:unknown)=>void)|undefined;
  const abort=()=>{c.abort();rejectAbort?.(Object.assign(new Error('Scope changed.'),{code:'retired'}));};
  if(write)this.activeWrites++;
  try{
   const aborted=new Promise<never>((_,reject)=>{rejectAbort=reject;});
   void aborted.catch(()=>{}); // Registration/guard may retire before Promise.race is attached.
   choice?.signal.addEventListener('abort',abort,{once:true});
   if(choice?.signal.aborted)throw Object.assign(new Error('Scope changed.'),{code:'retired'});
   // Recheck after signal registration, before the exact original transport.
   if(choice&&guard&&!guard())throw new Error('Scope changed.');
   choice?.onDispatch?.();
   const pending=this.api.request(path,method,body,c.signal);
   return await Promise.race([pending,aborted,new Promise<never>((_,reject)=>{timer=setTimeout(()=>{c.abort();reject(new Error('timeout'));},45000);})]);
  }catch(error){
   const code=this.refusalCode(error);
   if(code){this.retire(code);throw Object.assign(new Error(this.refusalMessage(code)),{code});}
   throw error;
  }finally{
   if(timer)clearTimeout(timer);choice?.signal.removeEventListener('abort',abort);rejectAbort=undefined;this.controllers.delete(c);
   if(write)this.activeWrites--;
  }
 }
 /** The sources as the server has them, checked; `undefined` when a later read or another viewer has taken over. */
 private async readSources(epoch:number,sequence:number,guard?:()=>boolean){const raw=await this.request(routePath("get_storage_sources"),'GET',undefined,guard);if(!this.live()||(guard&&!guard())||epoch!==this.epoch||sequence!==this.read)return undefined;this.fence(raw);if(!record(raw)||!Array.isArray(raw.sources)||typeof raw.mountSupported!=='boolean')invalid();const rows=firstOfEach('remote-sources','sources',keepReadable('remote-sources','sources',raw.sources,source),s=>s.id);return {sources:Object.freeze(rows),mountSupported:raw.mountSupported};}
 /**
  * Reads the sources again while they are on screen (a share that became reachable, or stopped
  * being). What is shown stays; a failure leaves it as it was and rejects. Nothing is read while
  * a command is in flight or its outcome is unknown.
  */
 async revalidate(){if(!this.live()||this.state.busy||this.state.ambiguous||this.state.loading)return;const epoch=this.epoch,sequence=++this.read;const next=await this.readSources(epoch,sequence);if(next&&!this.state.busy)this.publish({...next,error:''});}
 async load(){if(!this.live())return;const epoch=this.epoch,sequence=++this.read;this.publish({loading:true,error:''});try{const next=await this.readSources(epoch,sequence);if(!next)return;this.publish(next);}catch(e){if(epoch===this.epoch&&sequence===this.read)this.publish({error:safeError(e),sources:[],receipt:null,rclone:null,mountSupported:false});}finally{if(epoch===this.epoch&&sequence===this.read)this.publish({loading:false});}}
 private parseReceipt(raw:unknown,op:string):RemoteReceipt{this.fence(raw);if(!record(raw)||!record(raw.receipt)||raw.receipt.operationId!==op||raw.receipt.accepted!==true||typeof raw.receipt.removed!=='boolean')invalid();return Object.freeze({operationId:op,accepted:true,removed:raw.receipt.removed,source:source(raw.receipt.source)});}
 /** The imported-source receipt holds identity only. `removed` is left out when false. */
 private parseRcloneReceipt(raw:unknown,intent:SourceCommandIntent):RcloneReceipt{this.fence(raw);if(!record(raw)||!record(raw.receipt))invalid();const r=raw.receipt;
  if(r.operationId!==intent.operationId||r.accepted!==true||!text(r.sourceId,256)||!r.sourceId||!integer(r.generation)||r.generation<1||r.removed!==undefined&&typeof r.removed!=='boolean')invalid();
  const removed=r.removed===true;
  // The receipt must answer this command: its own source, its own posture, and
  // a replace that did not move the source's generation backwards.
  if(removed!==(intent.action==='remove'))invalid();
  if(intent.sourceId!==null&&r.sourceId!==intent.sourceId)invalid();
  if(intent.action==='edit'&&intent.generation!==undefined&&r.generation<intent.generation)invalid();
  return Object.freeze({operationId:intent.operationId,sourceId:r.sourceId,generation:r.generation,removed});}
 /** A confirmed rclone receipt is only published after the fresh list agrees with
  * it: the created or replaced row at that generation, or no row after a removal.
  * A list that does not agree is reported as unconfirmed, never assumed. */
 private rcloneListed(intent:SourceCommandIntent,receipt:RcloneReceipt):boolean{
  const row=this.state.sources.find(value=>value.id===receipt.sourceId);
  return intent.action==='remove'?!row:!!row&&row.generation===receipt.generation;
 }
 /** What rclone the server ships, and whether it starts now. The route carries no
  * owner scope, so this read neither sets nor checks the viewer fence. */
 async rcloneProgram():Promise<RcloneProgram>{
  const epoch=this.epoch;
  const raw=await this.request(routePath('get_storage_rclone_bundled'));
  if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');
  if(!record(raw)||typeof raw.included!=='boolean'||typeof raw.available!=='boolean'||!text(raw.version,200)||!text(raw.minimumVersion,200)||!text(raw.errorCode,100))invalid();
  return Object.freeze({included:raw.included,available:raw.available,version:raw.version,minimumVersion:raw.minimumVersion,errorCode:raw.errorCode});
 }
 /** The configuration backend types this server accepts. Facts only: no form,
  * readiness or guided schema is implied by a name in this list. */
 async rcloneProviders():Promise<readonly string[]>{
  const epoch=this.epoch;
  const raw=await this.request(routePath('get_storage_source_rclone_providers'));
  if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!Array.isArray(raw.providers)||raw.providers.length>64||!raw.providers.every(name=>text(name,100)&&!!name))invalid();
  return Object.freeze([...raw.providers] as string[]);
 }
 /** The owner's own Google OAuth client and the server-run sign-in it starts.
  * The client secret is used by the server's exchange and is never returned. */
 async startDriveOAuth(clientId:string,clientSecret:string,guard?:()=>boolean):Promise<DriveOAuthStart>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous)throw new Error('Finish or recover the current source command first.');
  if(!text(clientId,512)||clientId.trim().length<8||!text(clientSecret,2048)||clientSecret.length<8)throw new Error('Invalid OAuth client.');
  const epoch=this.epoch;
  const raw=await this.request(routePath('post_storage_source_drive_oauth_start'),'POST',routeBody('post_storage_source_drive_oauth_start',{clientId:clientId.trim(),clientSecret}),guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!text(raw.authorizationUrl,8192)||!raw.authorizationUrl||!text(raw.state,128)||!raw.state)invalid();
  if(!/^https:\/\/accounts\.google\.com\/o\/oauth2\/v2\/auth\?/.test(raw.authorizationUrl))invalid();
  return Object.freeze({authorizationUrl:raw.authorizationUrl,state:raw.state});

 }finally{releaseChoiceExclusion();}
}
 private async driveOAuthAnswer(path:string,body:Record<string,unknown>,guard?:()=>boolean):Promise<DriveOAuthStatus>{
  const epoch=this.epoch;
  const raw=await this.request(path,'POST',body,guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||typeof raw.phase!=='string'||!['pending','ready','expired','denied'].includes(raw.phase))invalid();
  if(raw.phase==='ready'){if(!text(raw.token,16384)||!raw.token)invalid();return Object.freeze({phase:'ready',token:raw.token});}
  return Object.freeze({phase:raw.phase as DriveOAuthPhase,token:''});
 }
 async driveOAuthStatus(state:string,guard?:()=>boolean):Promise<DriveOAuthStatus>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||!text(state,128)||!state)throw new Error('Invalid sign-in.');
  return await this.driveOAuthAnswer(routePath('post_storage_source_drive_oauth_status'),{state},guard);

 }finally{releaseChoiceExclusion();}
}
 async driveOAuthPaste(state:string,redirectUrl:string,guard?:()=>boolean):Promise<DriveOAuthStatus>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||!text(state,128)||!state||!text(redirectUrl,8192)||!redirectUrl.trim())throw new Error('Invalid sign-in address.');
  // This only records the address's code; the single status owner exchanges it.
  const epoch=this.epoch;
  const raw=await this.request(routePath('post_storage_source_drive_oauth_paste'),'POST',routeBody('post_storage_source_drive_oauth_paste',{state,redirectUrl:redirectUrl.trim()}),guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||typeof raw.phase!=='string'||!['pending','ready','expired','denied'].includes(raw.phase))invalid();
  return Object.freeze({phase:raw.phase as DriveOAuthPhase,token:''});

 }finally{releaseChoiceExclusion();}
}
 async driveOAuthCancel(state:string):Promise<void>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||!text(state,128)||!state)return;
  try{await this.request(routePath('post_storage_source_drive_oauth_cancel'),'POST',routeBody('post_storage_source_drive_oauth_cancel',{state}));}catch{/* A closed session needs no second report. */}

 }finally{releaseChoiceExclusion();}
}
 /** The server account's own rclone configuration, by remote name and type only. */
 async rcloneConfig():Promise<RcloneConfigView>{
  const epoch=this.epoch;
  const raw=await this.request(routePath('get_storage_source_rclone_config'));
  if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!text(raw.path)||!Array.isArray(raw.remotes)||raw.remotes.length>200||typeof raw.truncated!=='boolean')invalid();
  const remotes=raw.remotes.map(value=>{
   if(!record(value)||!text(value.name,64)||!value.name||!text(value.kind,32)||typeof value.supported!=='boolean'||!['','kind','encrypted','wrapper','duplicate','ambiguous'].includes(String(value.reason)))invalid();
   if(value.supported&&(value.reason!==''||!value.kind))invalid();
   return Object.freeze({name:value.name,kind:value.kind,supported:value.supported,reason:value.reason as RcloneConfigRemote['reason']});
  });
  return Object.freeze({path:raw.path,remotes:Object.freeze(remotes),truncated:raw.truncated});
 }
 /** Saves a source from one remote of that configuration. The section itself is
  * read on the server and never sent to, or read back by, this client. */
 async configureRcloneFromConfig(input:Readonly<{name:string;remote:string}>,guard?:()=>boolean):Promise<RcloneReceipt|undefined>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous)throw new Error('Finish or recover the current source command first.');
  if(!text(input.name,200)||!input.name.trim()||!text(input.remote,64)||!input.remote.trim())throw new Error('Invalid connection fields.');
  const body=routeBody('create_storage_source_rclone_config',{name:input.name.trim(),remote:input.remote.trim()});
  const intent:SourceCommandIntent={operationId:'',kind:'rclone',action:'create',sourceId:null};
  return await this.command(routePath('create_storage_source_rclone_config'),'POST',body,intent,guard) as RcloneReceipt|undefined;

 }finally{releaseChoiceExclusion();}
}
 /** One explicit in-flight configuration test. It saves no source, receipt or approval. */
 async testRclone(input:RcloneTestInput,guard?:()=>boolean):Promise<RcloneTestResult>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous)throw new Error('Finish or recover the current source command first.');
  if(!text(input.remote,512)||!input.remote.trim()||!configText(input.config)||!input.config.trim())throw new Error('Invalid connection fields.');
  const epoch=this.epoch;this.publish({busy:true,error:'',errorCode:''});
  try{
   const raw=await this.request(routePath('test_storage_source_rclone'),'POST',routeBody('test_storage_source_rclone',{remote:input.remote,config:input.config}),guard);
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   this.fence(raw);
   if(!record(raw))invalid();
   const result=raw.result;
   if(!record(result)||!text(result.version,200)||['remote','config','password','token'].some(key=>result[key]!==undefined))invalid();
   return Object.freeze({version:result.version});
  }catch(error){
   // A refusal keeps its normalized code for the captured UI report path.
   const refused=this.refusalCode(error);
   if(refused)throw Object.assign(new Error(this.refusalMessage(refused)),{code:refused});
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   const rawCode=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'';
   const code=['unauthorized','forbidden','not_permitted','invalid_request','source_credentials_required','administration_unavailable','internal'].includes(rawCode)?rawCode:'internal';
   const aliases:Record<string,string>={not_permitted:'forbidden',forbidden:'forbidden',invalid_request:'remote_invalid_configuration',source_credentials_required:'remote_credentials_required',administration_unavailable:'remote_administration_unavailable'};
   const message=safeError({code:aliases[code]??(code==='internal'?'remote_invalid_configuration':code)});
   this.refuse({code});
   this.publish({error:message,errorCode:code});throw Object.assign(new Error(message),{code});
  }finally{if(this.live()&&epoch===this.epoch)this.publish({busy:false});}

 }finally{releaseChoiceExclusion();}
}
 /** Saves an imported source from its configuration, or replaces one at the
  * generation the owner was shown. A conflict is returned for a fresh look and a
  * new explicit save; nothing is replayed or rebased here, and the configuration
  * is not kept after the call. */
 async configureRclone(id:string|null,input:RcloneSourceDraft,base?:RemoteSource,guard?:()=>boolean):Promise<RcloneReceipt|undefined>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous)throw new Error('Finish or recover the current source command first.');
  if(id&&(!base||base.kind!=='rclone'||base.id!==id))throw new Error('This source cannot be changed now.');
  if(!text(input.name,100)||!input.name.trim()||!text(input.remote,512)||!input.remote.trim()||!configText(input.config)||!input.config.trim())throw new Error('Invalid connection fields.');
  const body=id
   ? routeBody('update_storage_source_rclone',{name:input.name,remote:input.remote,config:input.config,expectedGeneration:base!.generation})
   : routeBody('create_storage_source_rclone',{name:input.name,remote:input.remote,config:input.config});
  const intent:SourceCommandIntent={operationId:'',kind:'rclone',action:id?'edit':'create',sourceId:id,...(id?{generation:base!.generation}:{})};
  const path=id?encodedRoutePath('update_storage_source_rclone',{id:encodeURIComponent(id)}):routePath('create_storage_source_rclone');
  return await this.command(path,id?'PUT':'POST',body,intent,guard) as RcloneReceipt|undefined;

 }finally{releaseChoiceExclusion();}
}
 /** Removes an imported source at the generation the owner was shown. A conflict
  * is returned for a fresh look; nothing is replayed. */
 async removeRclone(row:RemoteSource,guard?:()=>boolean):Promise<RcloneReceipt|undefined>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||row.kind!=='rclone'||this.state.busy||this.state.ambiguous)throw new Error('This source cannot be removed now.');
  const path=encodedRoutePath('remove_storage_source_rclone',{id:encodeURIComponent(row.id)});
  const body=routeBody('remove_storage_source_rclone',{expectedGeneration:row.generation});
  const intent:SourceCommandIntent={operationId:'',kind:'rclone',action:'remove',sourceId:row.id};
  return await this.command(path,'DELETE',body,intent,guard) as RcloneReceipt|undefined;

 }finally{releaseChoiceExclusion();}
}
 /** A new-source test uses one explicit in-flight credential request. It saves no source or retry receipt. */
 async testConnection(input:DAVConnectionInput,guard?:()=>boolean):Promise<DAVConnectionResult>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous)throw new Error('Finish or recover the current source command first.');
  if(!text(input.name,100)||!input.name.trim()||!text(input.root)||!text(input.username,1024)||!text(input.password)||typeof input.insecureLocal!=='boolean')throw new Error('Invalid connection fields.');
  const epoch=this.epoch;this.publish({busy:true,error:'',errorCode:''});
  try{
   const raw=await this.request(routePath('test_storage_source_webdav'),'POST',routeBody('test_storage_source_webdav',{name:input.name,root:input.root,username:input.username,password:input.password,insecureLocal:input.insecureLocal}),guard);
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   this.fence(raw);
   if(!record(raw)||!record(raw.result))invalid();
   const result=raw.result;
   if(!text(result.origin)||!sourceOrigin(result.origin,'webdav')||!['unknown','conditional','unsupported'].includes(String(result.rangeSupport))||['password','username','config','root'].some(key=>result[key]!==undefined))invalid();
   return Object.freeze({origin:result.origin,rangeSupport:result.rangeSupport as DAVConnectionResult['rangeSupport']});
  }catch(error){
   const refused=this.refusalCode(error);
   if(refused)throw Object.assign(new Error(this.refusalMessage(refused)),{code:refused});
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   const rawCode=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'';
   const code=['unauthorized','forbidden','not_permitted','invalid_request','source_credentials_required','internal'].includes(rawCode)?rawCode:'internal';
   const aliases:Record<string,string>={not_permitted:'forbidden',invalid_request:'remote_invalid_configuration',source_credentials_required:'remote_credentials_required'};
   const message=safeError({code:aliases[code]??(code==='internal'?'remote_invalid_configuration':code)});
   this.refuse({code});
   this.publish({error:message,errorCode:code});throw Object.assign(new Error(message),{code});
  }finally{if(this.live()&&epoch===this.epoch)this.publish({busy:false});}

 }finally{releaseChoiceExclusion();}
}
 /**
  * Adds a share (`id` null) or changes one. `base` is the share as the edit started from it: when
  * someone else changed the share meanwhile, it is read again and the edit, carried onto it field by
  * field, is sent once more without a word; only a field both changed is a conflict.
  */
 async configure(id:string|null,input:DAVConfiguration,base?:RemoteSource,guard?:()=>boolean){
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous)throw new Error('Finish or recover the current source command first.');if(!text(input.name,100)||!input.name.trim()||!text(input.root)||!text(input.username,1024)||!text(input.password))throw new Error('Invalid connection fields.');
  const path=routePath("post_storage_sources_webdav")+(id?'/'+encodeURIComponent(id):'');
  await this.command(path,id?'PUT':'POST',input,{operationId:'',kind:'source',action:id?'edit':'create',sourceId:id},guard);
  if(!id||!base||this.state.errorCode!==CONFLICT)return;
  const fresh=await this.reread(id,guard);if(!fresh)return;
  const rebased=rebaseDavEdit(base,fresh,input);if(!rebased)return this.publish({error:safeError({code:CONFLICT}),errorCode:CONFLICT});
  return this.command(path,'PUT',rebased,{operationId:'',kind:'source',action:'edit',sourceId:id},guard);
 }finally{releaseChoiceExclusion();}
}
 /** Removes a share. A share changed elsewhere is still the share the owner meant: it is read again and removed; one already gone is done. */
 async remove(row:RemoteSource,guard?:()=>boolean){
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{if(!this.live()||(guard&&!guard())||row.kind!=='webdav'||this.state.busy||this.state.ambiguous)throw new Error('This source cannot be removed now.');
  const path=encodedRoutePath("delete_storage_sources_webdav_id", {"id": encodeURIComponent(row.id)});
  await this.command(path,'DELETE',{expectedGeneration:row.generation},{operationId:'',kind:'source',action:'remove',sourceId:row.id},guard);
  if(this.state.errorCode!==CONFLICT)return;
  const fresh=await this.reread(row.id,guard);if(fresh===null)return this.publish({error:'',errorCode:'',alreadyDone:true});if(!fresh)return;
  return this.command(path,'DELETE',{expectedGeneration:fresh.generation},{operationId:'',kind:'source',action:'remove',sourceId:row.id},guard);
 }finally{releaseChoiceExclusion();}
}
 /** After a conflict: the share as the server has it now, null when it is gone, undefined when it could not be read (the conflict stays as it was). */
 private async reread(id:string,guard?:()=>boolean):Promise<RemoteSource|null|undefined>{const epoch=this.epoch,sequence=++this.read;try{const next=await this.readSources(epoch,sequence,guard);if(!next||(guard&&!guard()))return undefined;this.publish(next);return next.sources.find(s=>s.id===id)??null;}catch{return undefined;}}
 private async command(path:string,method:string,body:unknown,intent:SourceCommandIntent,guard?:()=>boolean):Promise<RemoteReceipt|RcloneReceipt|undefined>{
  const epoch=this.epoch;
  this.publish({busy:true,error:'',errorCode:'',receipt:null,rclone:null,alreadyDone:false,operationId:null,operationKind:intent.kind});
  let submitted=false;
  try{
   const op=await this.operationId();
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)return;
   if(!/^\d{13}-[0-9a-f-]{36}$/i.test(op))invalid();
   const bound=Object.freeze({...intent,operationId:op});
   this.intent=bound;
   this.publish({operationId:op});submitted=true;
   const pending=this.request(path,method,{...(body as object),operationId:op},guard);body=undefined;
   const raw=await pending;
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)return;
   const receipt=intent.kind==='rclone'?this.parseRcloneReceipt(raw,bound):this.parseReceipt(raw,op);
   if(guard&&!guard())return;
   if(intent.kind==='source')this.publish({receipt:receipt as RemoteReceipt,rclone:null,ambiguous:false});
   await this.load();
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)return;
   if(intent.kind==='rclone'){
    // The fresh list is read first: a receipt the list cannot account for is not published.
    if(!this.rcloneListed(bound,receipt as RcloneReceipt)){this.publish({error:safeError({code:'remote_source_not_listed'}),errorCode:'remote_source_not_listed',ambiguous:false,rclone:null});return;}
    this.publish({rclone:receipt as RcloneReceipt,receipt:null,ambiguous:false});
   }
   return receipt;
  }catch(e){
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)return;
   const code=record(e)&&typeof e.code==='string'?normalizedCode(e.code):'';
   const rejected=['storage_command_conflict','storage_operation_expired','storage_command_capacity','storage_allocation_recovery_required','remote_invalid_configuration','remote_credentials_required','source_credentials_required','administration_unavailable','request_in_progress','unauthorized','forbidden','not_permitted','invalid_request'].includes(code);
   this.refuse({code});
   this.publish({ambiguous:submitted&&!rejected,error:safeError(e),errorCode:code});
  }finally{body=undefined;if(this.live()&&epoch===this.epoch)this.publish({busy:false});}
 }
 async recover(){
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  const intent=this.intent;
  if(!this.live()||this.state.busy||!this.state.operationId||!intent||intent.operationId!==this.state.operationId)throw new Error('No source operation to recover.');
  const op=intent.operationId,epoch=this.epoch;
  this.publish({busy:true,error:''});
  try{
   const path=intent.kind==='rclone'?encodedRoutePath('get_storage_source_rclone_operation',{id:encodeURIComponent(op)}):encodedRoutePath('get_storage_source_operations_id',{id:encodeURIComponent(op)});
   const raw=await this.request(path);
   if(!this.live()||epoch!==this.epoch)return;
   const receipt=intent.kind==='rclone'?this.parseRcloneReceipt(raw,intent):this.parseReceipt(raw,op);
   if(intent.kind==='source')this.publish({receipt:receipt as RemoteReceipt,rclone:null,ambiguous:false});
   await this.load();
   if(!this.live()||epoch!==this.epoch)return;
   if(intent.kind==='rclone'){
    if(!this.rcloneListed(intent,receipt as RcloneReceipt)){this.publish({error:safeError({code:'remote_source_not_listed'}),errorCode:'remote_source_not_listed',ambiguous:false,rclone:null});return;}
    this.publish({rclone:receipt as RcloneReceipt,receipt:null,ambiguous:false});
   }
  }catch(e){if(epoch===this.epoch)this.publish({ambiguous:true,error:safeError(e)});}
  finally{if(this.live()&&epoch===this.epoch)this.publish({busy:false});}

 }finally{releaseChoiceExclusion();}
}
 async policy(libraryId:string):Promise<AnalysisPolicy>{const epoch=this.epoch,raw=await this.request(encodedRoutePath("get_libraries_id_strm_analysis_policy", {"id": encodeURIComponent(libraryId)}));if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');return this.parsePolicy(raw,libraryId);}
 private parsePolicy(raw:unknown,id:string):AnalysisPolicy{this.fence(raw);if(!record(raw)||!record(raw.policy)||raw.policy.libraryId!==id||typeof raw.policy.enabled!=='boolean'||!integer(raw.policy.revision))invalid();if(raw.policy.effective!==undefined&&typeof raw.policy.effective!=='boolean')invalid();const scanTier=['file_list_only','basic','complete','custom'].includes(String(raw.policy.scanTier))?raw.policy.scanTier as string:undefined;if(raw.policy.scanTier!==undefined&&scanTier===undefined)noteSkipped('remote-sources','policy.scanTier');return Object.freeze({libraryId:id,enabled:raw.policy.enabled,revision:raw.policy.revision,...(raw.policy.effective===undefined?{}:{effective:raw.policy.effective as boolean}),...(scanTier===undefined?{}:{scanTier})});}
 async setPolicy(policy:AnalysisPolicy,enabled:boolean):Promise<AnalysisPolicy>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{const epoch=this.epoch,raw=await this.request(encodedRoutePath("put_libraries_id_strm_analysis_policy", {"id": encodeURIComponent(policy.libraryId)}),'PUT',routeBody("put_libraries_id_strm_analysis_policy", {enabled,expectedRevision:policy.revision}));if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');return this.parsePolicy(raw,policy.libraryId);
 }finally{releaseChoiceExclusion();}
}
 async readProfile(id:string,expectedGeneration?:string,guard?:()=>boolean):Promise<SourceReadSettings>{
 if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
 const epoch=this.epoch;
 try{
  const raw=await this.request(encodedRoutePath("get_storage_source_read_profile",{"id":encodeURIComponent(id)}),'GET',undefined,guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);if(!record(raw)||!record(raw.settings))invalid();
  const settings=sourceReadSettings(raw.settings);
  if(settings.sourceId!==id||expectedGeneration!==undefined&&settings.generation!==expectedGeneration)invalid();
  this.choiceReads.set(settings,epoch);return settings;
 }catch(error){
  const refused=this.refusalCode(error);
  if(refused)throw Object.assign(new Error(this.refusalMessage(refused)),{code:refused});
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  if(error instanceof Error&&error.message==='Invalid remote source response.')throw error;
  const code=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'internal';
  throw Object.assign(new Error(safeError({code})),{code});
 }
}
async saveProfile(profile:SourceReadSettings,values:SourceReadValues,guard?:()=>boolean):Promise<SourceReadSettings>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
 if(!this.live()||(guard&&!guard())||this.state.busy||this.state.ambiguous&&!this.settingsPending)throw new Error('Finish or recover the current source command first.');
 const parsed=sourceReadValues(values);
 if(!profile.sourceId||!profile.generation||!Number.isSafeInteger(profile.revision)||profile.revision<0)throw new Error('Invalid source profile.');
 const epoch=this.epoch,wasAmbiguous=this.state.ambiguous;
 this.publish({busy:true,error:'',errorCode:'',receipt:null,rclone:null,alreadyDone:false,operationId:this.settingsPending?this.state.operationId:null,operationKind:'source'});
 let submitted=false,accepted=false;
 try{
  const {pending,op,body}=await this.settingsIntent(encodedRoutePath('put_storage_source_read_profile',{id:encodeURIComponent(profile.sourceId)}),{expectedGeneration:profile.generation,expectedRevision:profile.revision,values:parsed});
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.publish({operationId:op});submitted=true;
  if(!pending.profile){
   pending.dispatched=true;
   const raw=await this.request(pending.path,'PUT',body,guard);
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   // Recovery may supply only the CURRENT typed GET, never an old source receipt.
   if(record(raw)&&record(raw.settings)){
    this.fence(raw);const settings=sourceReadSettings(raw.settings);
    if(settings.sourceId!==profile.sourceId)invalid();
    pending.profile={settings};
   }else{
    const receipt=this.parseReceipt(raw,op);
    if(receipt.removed||receipt.source.id!==profile.sourceId)invalid();
    const settings=record(raw)&&record(raw.receipt)&&raw.receipt.readSettings!==undefined?sourceReadSettings(raw.receipt.readSettings):undefined;
    if(settings&&(settings.sourceId!==profile.sourceId||settings.generation!==receipt.source.generation.toString()))invalid();
    pending.profile={receipt,settings};
   }
  }
  const {receipt,settings:next}=pending.profile;
  if(guard&&!guard())throw new Error('Scope changed.');
  await this.api.acknowledgeUserMutation?.(op);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  if(this.settingsPending===pending)this.settingsPending=undefined;
  accepted=true;this.publish({receipt:receipt??null,ambiguous:false});
  if(next)return next;
  try{return await this.readProfile(profile.sourceId, receipt!.source.generation.toString(),guard);}
  catch(error){
   const code=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'internal';
   if(['unauthorized','forbidden','not_permitted','remote_credentials_required','source_credentials_required'].includes(code))throw Object.assign(new Error(safeError({code})),{code});
   throw Object.assign(new Error(safeError({code:'refresh_failed'})),{code:'refresh_failed'});
  }
 }catch(error){
  const refused=this.refusalCode(error);
  if(refused)throw Object.assign(new Error(this.refusalMessage(refused)),{code:refused});
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  const code=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'';
  const rejected=['storage_command_conflict','storage_operation_expired','revision_mismatch','idempotency_key_reused','remote_invalid_configuration','remote_credentials_required','unauthorized','forbidden','not_permitted','invalid_request'].includes(code);
  const auth=['unauthorized','forbidden','not_permitted','remote_credentials_required','source_credentials_required'].includes(code);
  const mutationKey=record(error)&&typeof error.mutationKey==='string'&&/^p251_\d{13}_[a-f0-9]{32}$/.test(error.mutationKey)?error.mutationKey:undefined;
  this.publish(accepted?{ambiguous:false,error:safeError({code:auth?code:'refresh_failed'}),errorCode:auth?code:'refresh_failed'}:{ambiguous:submitted?!rejected:wasAmbiguous,error:safeError(error),errorCode:code});
  throw accepted?Object.assign(new Error(safeError({code:auth?code:'refresh_failed'})),{code:auth?code:'refresh_failed'}):Object.assign(new Error(safeError(error)),{code:code||'internal',...(mutationKey?{mutationKey}: {})});
 }finally{if(this.live()&&epoch===this.epoch)this.publish({busy:false});}

 }finally{releaseChoiceExclusion();}
}

 private choicePending?:ChoiceCustody;private choiceFlight?:object;
 private choiceReads=new WeakMap<SourceReadSettings,number>();private choiceAdvancedReads=new WeakMap<SourceAdvancedSettings,number>();
 private choiceCurrent(token:object,epoch:number,opening:SourceChoiceOpening):void{
  const owned=()=>this.choiceFlight===token&&epoch===this.epoch&&!this.disposed&&!this.refused;
  if(!owned()||opening.signal.aborted||!this.live()||opening.isCurrent()!==true||!owned()||opening.signal.aborted)
   throw Object.assign(new Error('Scope changed.'),{code:'retired'});
 }
 /** Listener faults cannot lose a submitted intent or turn acceptance unknown.
  * The exact claim remains held throughout synchronous notifications. */
 private publishChoice(token:object,epoch:number,opening:SourceChoiceOpening,value:Partial<RemoteState>):void{
  this.choiceCurrent(token,epoch,opening);this.state=Object.freeze({...this.state,...value});
  for(const listener of [...this.listeners]){this.choiceCurrent(token,epoch,opening);try{listener();}catch{/* Preserve the original operation outcome. */}}
  this.choiceCurrent(token,epoch,opening);
 }
 private finishChoice(token:object,epoch:number,opening?:SourceChoiceOpening):void{
  if(this.choiceFlight!==token)return;
  try{
   if(opening){try{this.choiceCurrent(token,epoch,opening);}catch{return;}
    this.publishChoice(token,epoch,opening,{busy:false});}
  }finally{if(this.choiceFlight===token)this.choiceFlight=undefined;}
 }
 private choiceParent(raw:unknown,intent:SourceChoiceIntent):Readonly<{receipt:RemoteReceipt;accepted:SourceChoiceAcceptance}>{
  const receipt=this.parseReceipt(raw,intent.operationId),command=intent.command;
  if(receipt.removed||receipt.source.id!==command.sourceId||String(receipt.source.generation)!==command.expectedGeneration)invalid();
  if(!record(raw)||!record(raw.receipt)||raw.receipt.readSettings===undefined)invalid();
  const accepted=sourceChoiceAccepted(sourceReadSettings(raw.receipt.readSettings),command);
  if(!accepted)invalid();
  return Object.freeze({receipt,accepted});
 }
 private async reconcileChoice(custody:ChoiceCustody,token:object,accepted:SourceChoiceAcceptance):Promise<SourceChoiceApplyResult>{
  const {intent,opening,epoch}=custody,current=()=>{this.choiceCurrent(token,epoch,opening);return true;};
  try{
   // No expectedGeneration from the historical receipt: a causal cache child
   // carries its own raw/effective generations but the SAME accepted revisions.
   const raw=await this.request(encodedRoutePath('get_storage_source_read_profile',{id:encodeURIComponent(intent.command.sourceId)}),'GET',undefined,current,{token,signal:opening.signal});
   current();this.fence(raw);if(!record(raw)||!record(raw.settings))invalid();
   const settings=sourceReadSettings(raw.settings),comparison=sourceChoiceAdopt(settings,intent.command,accepted);
   current();
   if(comparison){this.choiceReads.set(settings,epoch);return Object.freeze({kind:'applied',operationId:intent.operationId,settings,accepted,comparison});}
   return Object.freeze({kind:'accepted-unavailable',operationId:intent.operationId,settings:accepted.settings,accepted});
  }catch(error){
   current();
   // A refresh failure NEVER converts a confirmed write into an unknown write.
   return Object.freeze({kind:'accepted-unavailable',operationId:custody.intent.operationId,settings:accepted.settings,accepted,error});
  }
 }
 /** One atomic PUT from an original service-issued pair and offered definition.
  * Claim BEFORE even opening/profile getters, callbacks or busy publication. */
 async saveProfileStartChoice(profile:SourceReadSettings,advanced:SourceAdvancedSettings,offered:SourceChoiceDefinition,opening:SourceChoiceOpening):Promise<SourceChoiceApplyResult>{
  if(this.choiceFlight||this.ordinaryMutations||this.activeWrites||this.state.busy||this.state.ambiguous&&!this.choicePending&&!this.settingsPending||this.disposed||this.refused)
   throw new Error('Finish or recover the current source command first.');
  const token=Object.freeze({}),epoch=this.epoch;this.choiceFlight=token;
  let captured:SourceChoiceOpening|undefined,custody:ChoiceCustody|undefined,accepted:SourceChoiceAcceptance|undefined,dispatched=false;
  try{
   if(this.settingsPending){
    await this.releaseReviewedSettings();
    if(this.settingsPending)throw new UserMutationError('mutation_needs_review');
    if(this.choiceFlight!==token||!this.live()||epoch!==this.epoch)throw new UserMutationError('mutation_scope_changed');
   }
   if(this.choicePending){
    const prior=this.choicePending;await this.api.awaitUserMutationsReady?.();
    const scope=this.api.getUserMutationScope?.(),coordinator=this.api.getUserMutationCoordinator?.();
    if(!prior.scope||!scope||!sameUserMutationScope(prior.scope,scope)||!coordinator||await coordinator.inspectIntent(prior.intent.operationId))throw new UserMutationError('mutation_needs_review');
    if(this.choicePending!==prior||this.choiceFlight!==token||!this.live()||epoch!==this.epoch)throw new UserMutationError('mutation_scope_changed');
    this.choicePending=undefined;
   }
   const signal=opening?.signal,isCurrent=opening?.isCurrent,consentDirty=opening?.consentDirty;
   if(!signal||typeof signal.addEventListener!=='function'||typeof isCurrent!=='function'||consentDirty!==false)throw new Error('The choice opening is invalid.');
   captured=Object.freeze({signal,isCurrent,consentDirty:false as const});this.choiceCurrent(token,epoch,captured);
   if(this.choiceReads.get(profile)!==epoch||this.choiceAdvancedReads.get(advanced)!==epoch)throw new Error('Use the original source reads from this service.');
   const command=sourceChoiceCommand(profile,advanced,offered);
   if(!command)throw new Error('This choice cannot be applied from the current read.');
   this.choiceCurrent(token,epoch,captured);
   const op=await this.operationId();this.choiceCurrent(token,epoch,captured);
   const body=sourceChoicePutBody(command,op),intent=Object.freeze({operationId:op,body,command});
   custody={intent,epoch,opening:captured,scope:this.api.getUserMutationScope?.()};this.choicePending=custody;
   const originalOpening=captured;
   const current=()=>{this.choiceCurrent(token,epoch,originalOpening);return true;};
   this.publishChoice(token,epoch,captured,{busy:true,error:'',errorCode:'',receipt:null,rclone:null,alreadyDone:false,operationId:op,operationKind:'source',ambiguous:false});
   current();
   // The custom strict body names the actual published producer fields.
   // No handwritten generated declaration or contract cast is needed.
   const raw=await this.request(encodedRoutePath('put_storage_source_read_profile',{id:encodeURIComponent(command.sourceId)}),'PUT',body,current,
    {token,signal:captured.signal,onDispatch:()=>{dispatched=true;}});
   current();const parent=this.choiceParent(raw,intent);
   custody.parent=parent;
   await this.api.acknowledgeUserMutation?.(op);current();accepted=parent.accepted;
   this.choicePending=undefined;this.publishChoice(token,epoch,captured,{receipt:parent.receipt,ambiguous:false});current();
   return await this.reconcileChoice(custody,token,accepted);
  }catch(error){
   if(!dispatched&&this.choicePending===custody)this.choicePending=undefined;
   if(captured)this.choiceCurrent(token,epoch,captured);
   if(accepted&&custody)return Object.freeze({kind:'accepted-unavailable',operationId:custody.intent.operationId,settings:accepted.settings,accepted,error});
   if(dispatched&&custody&&!definiteRefusal(error)){
    this.publishChoice(token,epoch,custody.opening,{ambiguous:true,error:safeError(error),errorCode:record(error)&&typeof error.code==='string'?normalizedCode(error.code):'internal'});
    this.choiceCurrent(token,epoch,custody.opening);
    return Object.freeze({kind:'unknown',intent:custody.intent,error});
   }
   if(this.choicePending===custody)this.choicePending=undefined;
   throw error;
  }finally{this.finishChoice(token,epoch,captured);}
 }
 /** Recovery is GET only, using the EXACT privately issued intent and its
  * ORIGINAL opening. A 404 cannot establish that the PUT will never commit. */
 async recoverStartChoice(intent:SourceChoiceIntent):Promise<SourceChoiceApplyResult>{
  const custody=this.choicePending;
  if(!custody||custody.intent!==intent||this.choiceFlight||this.ordinaryMutations||this.activeWrites||this.disposed||this.refused)
   throw new Error('This operation belongs to another opening.');
  const token=Object.freeze({}),{epoch,opening}=custody;this.choiceFlight=token;
  let accepted:SourceChoiceAcceptance|undefined;
  try{
   const current=()=>{this.choiceCurrent(token,epoch,opening);return true;};current();
   this.publishChoice(token,epoch,opening,{busy:true,error:'',errorCode:''});current();
   const parent=custody.parent??this.choiceParent(await this.request(encodedRoutePath('get_storage_source_operations_id',{id:encodeURIComponent(intent.operationId)}),'GET',undefined,current,{token,signal:opening.signal}),intent);
   current();custody.parent=parent;
   await this.api.acknowledgeUserMutation?.(intent.operationId);current();accepted=parent.accepted;
   this.choicePending=undefined;this.publishChoice(token,epoch,opening,{receipt:parent.receipt,ambiguous:false});current();
   return await this.reconcileChoice(custody,token,accepted);
  }catch(error){
   this.choiceCurrent(token,epoch,opening);
   if(accepted&&custody)return Object.freeze({kind:'accepted-unavailable',operationId:custody.intent.operationId,settings:accepted.settings,accepted,error});
   this.publishChoice(token,epoch,opening,{ambiguous:true,error:safeError(error),errorCode:record(error)&&typeof error.code==='string'?normalizedCode(error.code):'internal'});
   this.choiceCurrent(token,epoch,opening);
   return Object.freeze({kind:'unknown',intent,error});
  }finally{this.finishChoice(token,epoch,opening);}
 }

async networkPolicy(libraryId:string):Promise<NetworkPolicy>{const epoch=this.epoch,raw=await this.request(encodedRoutePath("get_libraries_id_network_roots", {"id": encodeURIComponent(libraryId)}));if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');this.fence(raw);if(!record(raw)||!record(raw.policy)||raw.policy.libraryId!==libraryId||!integer(raw.policy.revision)||!Array.isArray(raw.policy.origins)||raw.policy.origins.length>8||!raw.policy.origins.every(origin=>text(origin,8192)))invalid();return Object.freeze({libraryId,revision:raw.policy.revision,origins:Object.freeze(raw.policy.origins as string[])});}
 async setNetworkPolicy(policy:NetworkPolicy,roots:readonly string[]):Promise<NetworkPolicy>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{if(roots.length>8||roots.some(root=>!text(root,8192)))throw new Error('Invalid network roots.');const epoch=this.epoch;await this.request(encodedRoutePath("post_libraries_id_network_roots", {"id": encodeURIComponent(policy.libraryId)}),'POST',routeBody("post_libraries_id_network_roots", {roots,expectedRevision:policy.revision}));if(!this.live()||epoch!==this.epoch)throw new Error('Scope changed.');return this.networkPolicy(policy.libraryId);
 }finally{releaseChoiceExclusion();}
}
 /** One folder of a saved source, folders only, read when the owner opens it.
  * Nothing is listed in advance, downloaded or cached by this call. */
 async listFolders(id:string,options:Readonly<{path?:string;cursor?:string;showHidden?:boolean;limit?:number}>={},guard?:()=>boolean):Promise<RemoteFolderPage>{
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  if(!text(id,256)||!id)throw new Error('Invalid source.');
  const limit=options.limit??200;
  if(!Number.isInteger(limit)||limit<1||limit>500)throw new Error('Invalid folder query.');
  const query=new URLSearchParams({limit:String(limit)});
  for(const key of ['path','cursor'] as const){const value=options[key];if(value!==undefined){if(!text(value)||!value)throw new Error('Invalid folder path.');query.set(key,value);}}
  if(options.showHidden!==undefined)query.set('showHidden',options.showHidden?'1':'0');
  const epoch=this.epoch;
  const raw=await this.request(encodedRoutePath('get_storage_source_folders',{id:encodeURIComponent(id)})+'?'+query,'GET',undefined,guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!record(raw.folders))invalid();
  return remoteFolderPage(raw.folders,id);
 }
 /** Change the rclone program that serves a mounted source; the mount approves
  * it by running it with version before it is accepted. */
 async setProgram(id:string,expectedRevision:number,choice:Readonly<{useIncluded?:boolean;executable?:string}>,guard?:()=>boolean):Promise<SourceProgram>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  if(!text(id,256)||!id||!Number.isSafeInteger(expectedRevision)||expectedRevision<1)throw new Error('Invalid program change.');
  const useIncluded=choice.useIncluded===true;
  const executable=choice.executable??'';
  if(!useIncluded&&(!text(executable)||!executable))throw new Error('Invalid program change.');
  const epoch=this.epoch;
  const {pending,op,body}=await this.settingsIntent(encodedRoutePath('put_storage_source_program',{id:encodeURIComponent(id)}),{expectedRevision,useIncluded,...(useIncluded?{}:{executable})});
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  if(!pending.program){
   pending.dispatched=true;
   const raw=await this.request(pending.path,'PUT',body,guard);
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   if(record(raw)&&record(raw.mutationReceipt))throw new UserMutationError('mutation_needs_review');
   this.fence(raw);
   if(!record(raw)||!record(raw.program)||!text(raw.program.executable)||!raw.program.executable||!text(raw.program.version,256))invalid();
   if(!integer(raw.program.revision)||raw.program.revision<expectedRevision)invalid();
   if(typeof raw.program.included!=='boolean')invalid();
   pending.program=Object.freeze({executable:raw.program.executable,version:raw.program.version,revision:raw.program.revision,included:raw.program.included});
  }
  await this.api.acknowledgeUserMutation?.(op);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  if(this.settingsPending===pending)this.settingsPending=undefined;
  return pending.program;

 }finally{releaseChoiceExclusion();}
}
 /** The Advanced page as saved; unsaved values default from the source's folders. */
 async readAdvanced(id:string,guard?:()=>boolean):Promise<SourceAdvancedSettings>{
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  if(!text(id,256)||!id)throw new Error('Invalid source.');
  const epoch=this.epoch;
  const raw=await this.request(encodedRoutePath('get_storage_source_advanced',{id:encodeURIComponent(id)}),'GET',undefined,guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!record(raw.settings))invalid();
  const settings=sourceAdvancedSettings(raw.settings,id);this.choiceAdvancedReads.set(settings,epoch);return settings;
 }
 /** One CAS write of the Advanced page; the server applies it to the folders and cache. */
 async saveAdvanced(settings:SourceAdvancedSettings,values:SourceAdvanced,guard?:()=>boolean):Promise<SourceAdvancedSettings>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  if(!settings.sourceId||!settings.generation||!Number.isSafeInteger(settings.revision)||settings.revision<0)throw new Error('Invalid source settings.');
  const epoch=this.epoch;
  this.publish({busy:true,error:'',errorCode:''});
  try{
   const raw=await this.request(encodedRoutePath('put_storage_source_advanced',{id:encodeURIComponent(settings.sourceId)}),'PUT',{expectedGeneration:settings.generation,expectedRevision:settings.revision,values:sourceAdvanced(values)},guard);
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   this.fence(raw);
   if(!record(raw)||!record(raw.settings))invalid();
   const next=sourceAdvancedSettings(raw.settings,settings.sourceId);
   if(next.revision<settings.revision||next.generation!==settings.generation)invalid();
   return next;
  }catch(error){
   if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
   const code=record(error)&&typeof error.code==='string'?normalizedCode(error.code):'internal';
   this.publish({error:safeError({code}),errorCode:code});
   throw Object.assign(new Error(safeError({code})),{code:code||'internal'});
  }finally{if(this.live()&&epoch===this.epoch)this.publish({busy:false});}

 }finally{releaseChoiceExclusion();}
}
 /** What the source is doing now, and where its folders went; one bounded read. */
 async testSaved(id:string,guard?:()=>boolean):Promise<SavedSourceTest>{
 const releaseChoiceExclusion=this.holdOrdinaryMutation();
 try{
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  if(!text(id,256)||id.length===0)throw new Error('Invalid source id.');
  const epoch=this.epoch;
  const raw=await this.request(encodedRoutePath('test_storage_source',{id:encodeURIComponent(id)}),'POST',undefined,guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!record(raw.result))invalid();
  const r=raw.result;
  if(!text(r.sourceId,256)||!r.sourceId||r.sourceId!==id||!text(r.generation,64)||!r.generation||typeof r.reachable!=='boolean')invalid();
  const rangeSupport=r.rangeSupport===undefined?undefined:(['unknown','conditional','unsupported','sequential'] as const).find(value=>value===r.rangeSupport);
  if(r.rangeSupport!==undefined&&!rangeSupport)invalid();
  const errorCode=r.errorCode===undefined?undefined:(['offline','credentials_required','rate_limited','quota_exceeded'] as const).find(value=>value===r.errorCode);
  if(r.errorCode!==undefined&&!errorCode)invalid();
  // A completed success carries no failure code; a completed failure must name one.
  if(r.reachable===true&&errorCode!==undefined)invalid();
  if(r.reachable===false&&errorCode===undefined)invalid();
  return Object.freeze({sourceId:r.sourceId,generation:r.generation,reachable:r.reachable,...(rangeSupport?{rangeSupport}:{}),...(errorCode?{errorCode}:{})});

 }finally{releaseChoiceExclusion();}
}
 async readStatus(id:string,guard?:()=>boolean):Promise<RemoteSourceStatus>{
  if(!this.live()||(guard&&!guard()))throw new Error('Scope changed.');
  if(!text(id,256)||!id)throw new Error('Invalid source.');
  const epoch=this.epoch;
  const raw=await this.request(encodedRoutePath('get_storage_source_status',{id:encodeURIComponent(id)}),'GET',undefined,guard);
  if(!this.live()||(guard&&!guard())||epoch!==this.epoch)throw new Error('Scope changed.');
  this.fence(raw);
  if(!record(raw)||!record(raw.status))invalid();
  return Object.freeze({...remoteSourceStatus(raw.status,id),assignmentFenceSupported:raw.assignmentFenceSupported===true});
 }
}
export type SourceAvailability='ready'|'unavailable'|'changed'|'unsupported'|'resolve_on_play';
export async function readSourceAvailability(api:LibraryContentApi,itemId:string,signal:AbortSignal):Promise<SourceAvailability>{
 const raw=await api.request<RouteResponse<"get_items_id_source_availability">>(encodedRoutePath("get_items_id_source_availability", {"id": encodeURIComponent(itemId)}),'GET',undefined,signal);
 if(signal.aborted)throw new Error('Source request cancelled.');
 if(!record(raw)||raw.itemId!==itemId||!['ready','unavailable','changed','unsupported','resolve_on_play'].includes(String(raw.state)))invalid();
 return raw.state as SourceAvailability;
}
export function sourceAvailabilityMessage(state:SourceAvailability):string{return {ready:'',unavailable:'This source is unavailable. Ask the server owner to check its connection.',changed:'The source changed. Ask the server owner to refresh this library before retrying.',unsupported:'This source cannot currently provide safe playback ranges.',resolve_on_play:'This external source will be checked securely by the server when playback starts.'}[state];}

export type NetworkPolicy=Readonly<{libraryId:string;revision:number;origins:readonly string[]}>;
