import {userMutationRoute} from './user-mutation-routes.ts';
import {sameUserMutationScope,type UserMutationCoordinator,type UserMutationScope} from './user-mutations.ts';
import {parseSettingsConfiguration} from './settings-configuration.ts';
import {canonicalQualityLadder,canonicalMusicQualityLadder} from '../../contracts/src/types.ts';
import {reportQuery,verifyReportInterval,type ReportContext} from './report-context.ts';
import {routePath, encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {parseFFmpegValidation,type FFmpegValidation} from './storage-status.ts';
import {wireInstants} from './wire-units.ts';
import {parseFeedbackList,parseFeedbackReport,type FeedbackItemStatus} from './notifications.ts';
/** Selected-server operations wire contract. No Hosted transport is accepted. */
import {parsePreferenceSnapshot,type DeviceClass,type PreferencePatch,type PreferenceScope} from './preferences.ts';
import {parseTelemetryReading,parseTelemetrySample,parseTranscodeCapacity,type TelemetryWindow} from './telemetry.ts';
import {parseAttention} from './attention.ts';
import {parsePlaybackHistory,type PlaybackHistoryPeriod} from './playback-history.ts';
import {firstOfEach,keepReadable,noteSkipped} from './tolerant.ts';
export type ConsoleAPI={awaitUserMutationsReady?():Promise<void>;getUserMutationScope?():UserMutationScope|undefined;getUserMutationCoordinator?():UserMutationCoordinator|undefined;acknowledgeUserMutation?(intentId:string):Promise<void>;request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;requestConsole?<T>(path:string,method:string,body:unknown,signal:AbortSignal):Promise<T>};
export type ConsoleScope={serverId:string;viewerId:string};
export type Page<T>={items:T[];nextCursor:string};
export type HardwareBackend='auto'|'software'|'videotoolbox'|'vaapi'|'qsv'|'nvenc'|'amf';
export type ToneMappingAlgorithm='clip'|'linear'|'gamma'|'reinhard'|'hable'|'mobius';
export type X264Preset='ultrafast'|'superfast'|'veryfast'|'faster'|'fast'|'medium'|'slow';
export type PlanningPolicy='maximum_fidelity'|'maximum_compatibility'|'minimize_server_work';
export const HARDWARE_BACKENDS:HardwareBackend[]=['auto','software','videotoolbox','vaapi','qsv','nvenc','amf'];
export const TONE_MAPPING_ALGORITHMS:ToneMappingAlgorithm[]=['clip','linear','gamma','reinhard','hable','mobius'];
export const X264_PRESETS:X264Preset[]=['ultrafast','superfast','veryfast','faster','fast','medium','slow'];
export const PLANNING_POLICIES:PlanningPolicy[]=['maximum_fidelity','maximum_compatibility','minimize_server_work'];
/** Session caps: 0 means unlimited, which the server states rather than infers. */
export type TranscodingSettings={
 hardwareBackend:HardwareBackend;hardwareDevice:string;hdrToneMapping:boolean;hdrToneMappingAlgorithm:ToneMappingAlgorithm;
 x264Preset:X264Preset;directStreamRemux:boolean;planningPolicy:PlanningPolicy;
 throttleBufferMs:number;playedRetentionMs:number;temporaryDirectory:string;ffmpegPath:string;sessionStorageMiB:number;
 maxConcurrentSessions:number;maxHardwareSessions:number;maxSoftwareSessions:number;maxBackgroundSessions:number};
export type RuntimeSettings=TranscodingSettings&{
 analysisStorageGiB:number;analysisStageStorageMiB:number;subtitleTrackLimit:number;subtitleFileLimitMiB:number;name:string;conversionMode:'all'|'sound'|'never';outputFormat:'auto'|'h264';downloadMaxQuality:string;musicAwayQuality:string;perAccountCap:number|null;serverCap:number|null;
 diagnosticDays:number;notificationDays:number;jobDays:number;playHistoryDays:number;'home.communityActivityEnabled':boolean;
 'updates.releaseChannel':string;'updates.feedUrl':string;'library.missingTitles':MissingTitlesRule;'library.missingTitlesDays':number;
 /** Intro and credits detection for the whole server; each library may follow it or choose its own. */
 'library.segmentDetection':SegmentDetectionMode;
 remoteSignInPolicy:string;remoteBitrateLimitKbps:number;lanNetworks:string[];accessUrls:string[];lanDiscoveryEnabled:boolean;dlnaEnabled:boolean;
 trustedProxies:string[];uploadCapacityKbps:number;pausedSessionTimeoutMinutes:number;advertisedInterface:string;
 customCertificatePath:string;customCertificateKeyPath:string;customCertificateDomain:string;ownAddressEnabled:boolean;
 /** "Secure connections": `required` refuses plain HTTP for everyone; `preferred` allows it. */
 secureConnections:'preferred'|'required';
 connectionLimit:number;advertiseUrl:string;apiRequestsPerMinute:number;
 'integrations.compatibilityEnabled'?:boolean;
 logLevel:string;logRetention:{category:string;days:number}[];
};
export type MissingTitlesRule='immediately'|'after_days'|'never';
export type SegmentDetectionMode='off'|'standard'|'thorough';
export const SEGMENT_DETECTION_MODES:readonly SegmentDetectionMode[]=Object.freeze(['off','standard','thorough']);
export const MISSING_TITLES_RULES:readonly MissingTitlesRule[]=Object.freeze(['immediately','after_days','never']);
export type SettingsDocument={ffmpegValidation?:FFmpegValidation;revision:number;digest:string;requested:RuntimeSettings;effective:RuntimeSettings;activeRevision:number;restartFields:string[];registryRevision:string};
export type FeedbackStatus=FeedbackItemStatus;
export type FeedbackTransition=Readonly<{operationId:string;expectedRevision:number;status:FeedbackStatus;reply:string}>;
export type Notice={id:string;createdAt:number;readAt:number|null;code:string;targetId?:string;message:string};
export type Inbox=Page<Notice>&{unread:number};
export type Job={id:string;kind:string;resource:string;trigger:string;state:'queued'|'running'|'paused'|'reconciling'|'cancellation-requested'|'cancelled'|'failed'|'succeeded';phase:string;revision:number;attempt:number;createdAt:number;updatedAt:number;nextAt:number;domainId?:string;errorCode?:string;predecessor?:string;settingsRevision:number;lane:string;actions:('cancel'|'retry'|'pause'|'resume')[];processed?:number;total?:number};
export type Schedule={id:string;revision:number;kind:string;resource:string;enabled:boolean;timezone:string;startMinute:number;windowMinutes:number;catchUp:boolean;lastSlot:string;createdAt:number};
export type StreamOptions={libraries:{id:string;label:string}[];viewers:{id:string;label:string}[]};
export type JobKind={kind:string;lane:string;resourceRequired:boolean};
export type Alert={params:Readonly<Partial<Record<'title'|'channel'|'reason'|'time',string>>>;id:string;code:string;severity:string;status:string;firstAt:number;lastAt:number;occurrences:number;revision:number};
export type Measurement={name:string;observedAt:number;freshUntil:number;facts:Record<string,{state:string;value:unknown;unit?:string;reason?:string}>};
export type EvidenceRecord={sequence:number;at:number;lane?:string;severity?:string;component?:string;code?:string;action?:string;target?:string;hash?:string;previousHash?:string;fields?:Record<string,number>};
export const SUPPORT_PATH_CATEGORIES=['library-configured-root','library-resolved-root','managed-mount-point'] as const;
export type SupportPathCategory=typeof SUPPORT_PATH_CATEGORIES[number];
export type SupportPathFacts=Readonly<{limit:128;hasMore:boolean;scanBudget:512;examined:number;scanTruncated:boolean;invalidAmongExamined:number;items:readonly Readonly<{category:SupportPathCategory;reference:string;value:string}>[]}>;
export type SupportExport={id:string;expiresAt:number;manifest:{version:string;from:number;to:number;included:string[];excluded:string[];redaction:string;maxBytes:number;includePaths?:boolean;pathCategories?:SupportPathCategory[];pathsTruncated?:boolean;pathsScanTruncated?:boolean;pathsInvalidAmongExamined?:number};snapshot?:unknown;snapshotPaths?:SupportPathFacts;runtime?:Page<EvidenceRecord>;client?:Page<EvidenceRecord>;audit?:Page<EvidenceRecord>};
const obj=(v:unknown):v is Record<string,any>=>typeof v==='object'&&v!==null&&!Array.isArray(v);
const integer=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const text=(v:unknown,max=4096):v is string=>typeof v==='string'&&v.length<=max;
const id=(v:unknown):v is string=>typeof v==='string'&&/^[A-Za-z0-9_.-]{1,160}$/.test(v);
function invalid():never{throw new Error('Invalid selected-server console response.');}
function base(v:unknown):Record<string,any>{if(!obj(v))invalid();return v;}
function revision(v:Record<string,any>){if(!integer(v.revision)||v.revision<1)invalid();}
export function parseSettings(v:unknown):SettingsDocument{
 const raw=base(v),d:Record<string,any>={...raw,requested:{...base(raw.requested)},effective:{...base(raw.effective)}};revision(d);if(!/^[a-f0-9]{64}$/.test(d.digest)||!integer(d.activeRevision)||!Array.isArray(d.restartFields))invalid();
 for(const key of ['requested','effective']){const s=base(d[key]);delete s.transcodingEnabled;delete s.deviceApprovalRequired;delete s.blockMemberAccessTokens;if(!text(s.name,100)||!['all','sound','never'].includes(s.conversionMode)||!['auto','h264'].includes(s.outputFormat)||!canonicalQualityLadder.some(q=>q.id!=='automatic'&&q.id===s.downloadMaxQuality)||!canonicalMusicQualityLadder.some(q=>q.id===s.musicAwayQuality))invalid();for(const key of ['serverCap','perAccountCap'])if(s[key]!==null&&(!integer(s[key])||s[key]<1||s[key]>1000000))invalid(); for(const key of ['diagnosticDays','notificationDays','jobDays'])if(!integer(s[key])||s[key]<1||s[key]>(key==='notificationDays'?180:30))invalid();if(!integer(s.playHistoryDays)||s.playHistoryDays>36500)invalid();checkTranscoding(s);checkOwnerDocument(s);if(typeof s['home.communityActivityEnabled']!=='boolean')invalid();}
 return {...d,...(d.ffmpegValidation?{ffmpegValidation:parseFFmpegValidation(d.ffmpegValidation)}:{})} as SettingsDocument;
}
function checkOwnerDocument(s:Record<string,any>){
 if(!integer(s.analysisStorageGiB)||s.analysisStorageGiB<1||s.analysisStorageGiB>16384||!integer(s.analysisStageStorageMiB)||s.analysisStageStorageMiB<1||s.analysisStageStorageMiB>4096||s.analysisStageStorageMiB>s.analysisStorageGiB*1024)invalid();
 if(!integer(s.subtitleTrackLimit)||s.subtitleTrackLimit<32||s.subtitleTrackLimit>4096||!integer(s.subtitleFileLimitMiB)||s.subtitleFileLimitMiB<32||s.subtitleFileLimitMiB>512)invalid();
 for(const key of ['updates.releaseChannel','updates.feedUrl','remoteSignInPolicy','advertiseUrl','advertisedInterface','customCertificatePath','customCertificateKeyPath','customCertificateDomain','logLevel'])if(!text(s[key],4096))invalid();
 for(const key of ['remoteBitrateLimitKbps','uploadCapacityKbps','pausedSessionTimeoutMinutes','connectionLimit','apiRequestsPerMinute'])if(!integer(s[key]))invalid();
 for(const key of ['lanDiscoveryEnabled','ownAddressEnabled'])if(typeof s[key]!=='boolean')invalid();
 if(s.secureConnections!=='preferred'&&s.secureConnections!=='required')invalid();
 // An older server has no DLNA switch, which reads as off.
 if(s.dlnaEnabled===undefined)s.dlnaEnabled=false;else if(typeof s.dlnaEnabled!=='boolean')invalid();
 for(const key of ['lanNetworks','accessUrls','trustedProxies'])if(!Array.isArray(s[key])||s[key].length>64||!s[key].every((v:unknown)=>text(v,4096)))invalid();
 if(!MISSING_TITLES_RULES.includes(s['library.missingTitles'])||!integer(s['library.missingTitlesDays'])||s['library.missingTitlesDays']<1||s['library.missingTitlesDays']>3650||s.connectionLimit>65536||s.apiRequestsPerMinute>100000)invalid();
 // An older server has no detection mode and leaves it out.
 if(s['library.segmentDetection']!==undefined&&!SEGMENT_DETECTION_MODES.includes(s['library.segmentDetection']))invalid();
 if(!Array.isArray(s.logRetention)||s.logRetention.length>64||!s.logRetention.every((v:unknown)=>obj(v)&&text(v.category,100)&&integer(v.days)))invalid();
}
/** Transcoding values are rejected outside the bounds the registry publishes,
 * so a client cannot render a choice this server would refuse to save. */
function checkTranscoding(s:Record<string,any>){
 if(!HARDWARE_BACKENDS.includes(s.hardwareBackend)||!TONE_MAPPING_ALGORITHMS.includes(s.hdrToneMappingAlgorithm)||
  !X264_PRESETS.includes(s.x264Preset)||!PLANNING_POLICIES.includes(s.planningPolicy))invalid();
 if(typeof s.hdrToneMapping!=='boolean'||typeof s.directStreamRemux!=='boolean')invalid();
 if(!text(s.hardwareDevice,200)||!text(s.temporaryDirectory,1024)||!text(s.ffmpegPath,1024)||!integer(s.sessionStorageMiB)||s.sessionStorageMiB<64||s.sessionStorageMiB>1048576)invalid();
 if(!integer(s.throttleBufferMs)||s.throttleBufferMs<10_000||s.throttleBufferMs>600_000)invalid();
 if(!integer(s.playedRetentionMs)||s.playedRetentionMs>3_600_000)invalid();
 for(const key of ['maxConcurrentSessions','maxHardwareSessions','maxSoftwareSessions','maxBackgroundSessions'])
  if(!integer(s[key])||s[key]>1000)invalid();
}
export function parseEvidenceRecord(value:unknown):EvidenceRecord {
 const record=base(wireInstants(value,["at"]));
 if(!integer(record.sequence)||!integer(record.at)||record.at>8640000000000000)invalid();
 for(const key of ['lane','severity','component','code','action','target','hash','previousHash'])if(record[key]!==undefined&&!text(record[key],4096))invalid();
 if(record.fields!==undefined){if(!obj(record.fields)||Object.keys(record.fields).length>64)invalid();for(const [key,value] of Object.entries(record.fields))if(!text(key,100)||typeof value!=='number'||!Number.isFinite(value))invalid();}
 return record as EvidenceRecord;
}
/** The page size and the cursor stay exact. A row this app cannot read is left out alone, and a repeated id keeps its first row. */
export function parsePage<T>(v:unknown,parse:(v:unknown)=>T):Page<T>{const p=base(v);if(!Array.isArray(p.items)||p.items.length>40||typeof p.nextCursor!=='string'||p.nextCursor!==''&&!/^\d{1,19}$/.test(p.nextCursor))invalid();return {items:distinct(keepReadable('console','page.items',p.items,parse)),nextCursor:p.nextCursor};}
function distinct<T>(rows:T[]):T[]{return rows.every(row=>obj(row)&&typeof row.id==='string')?firstOfEach('console','rows',rows,row=>(row as {id:string}).id):rows;}
function parseInbox(v:unknown):Inbox{const p=base(v);if(!integer(p.unread))invalid();return {...parsePage(v,n=>{const v=base(wireInstants(n,["createdAt","readAt"]));if(!id(v.id)||!integer(v.createdAt)||v.readAt!==null&&!integer(v.readAt)||!text(v.message,300)||v.targetId!==undefined&&!id(v.targetId))invalid();return v as Notice;}),unread:p.unread};}
/** A job in a state this app does not know cannot be shown truthfully, so it is left out of its list. An action this app does not know is not offered. */
function parseJob(v:unknown):Job{const j=base(wireInstants(v,["createdAt","updatedAt","nextAt"]));revision(j);if(!id(j.id)||!id(j.kind)||!Array.isArray(j.actions)||!['queued','running','paused','reconciling','cancellation-requested','cancelled','failed','succeeded'].includes(j.state))invalid();const actions=j.actions.filter((v:unknown)=>v==='retry'||v==='cancel'||v==='pause'||v==='resume');if(actions.length===j.actions.length)return j as Job;noteSkipped('console','job.actions',j.actions.length-actions.length);return {...j,actions} as Job;}
function list<T>(v:unknown,parse:(v:unknown)=>T,max=100):T[]{if(!Array.isArray(v)||v.length>max)invalid();return distinct(keepReadable('console','rows',v,parse));}
function parseSchedule(v:unknown):Schedule{const s=base(wireInstants(v,["createdAt"]));revision(s);if(!id(s.id)||!id(s.kind)||!text(s.timezone,80)||typeof s.enabled!=='boolean'||!integer(s.startMinute)||s.startMinute>=1440||!integer(s.windowMinutes)||s.windowMinutes<1||s.windowMinutes>1440||typeof s.catchUp!=='boolean')invalid();return s as Schedule;}
let sequence=0;
/** Operation keys are uniqueness tokens, not credentials or authorization. */
export function consoleOperationId():string{return globalThis.crypto?.randomUUID?.()??`op-${Date.now().toString(36)}-${(++sequence).toString(36)}-${Math.random().toString(36).slice(2,16)}`;}
export class ConsoleClient {
 private disposed=false;private fence?:string;private requests=new Set<AbortController>();private intents=new Map<string,{body:string;key:string;method:string;parse:(v:unknown)=>unknown;binding?:UserMutationScope;ack?:Promise<void>}>();private reconciled=new Set<()=>void>();private listeners=new Set<()=>void>();
 private drafts=new Map<string,unknown>();private denied=false;private recoveredExport?:SupportExport;
 private settingsRead = new Map<number,RuntimeSettings>();
 private pendingSettings?:{expectedRevision:number;baseline:RuntimeSettings;patch:string};
 private api:ConsoleAPI;readonly scope:ConsoleScope;
 constructor(api:ConsoleAPI,scope:ConsoleScope){if(!scope.serverId||!scope.viewerId)throw new Error('A selected viewer is required.');this.api=api;this.scope={...scope};}
 onDenied=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private revoke(){this.denied=true;this.recoveredExport=undefined;this.drafts.clear();this.intents.clear();this.settingsRead.clear();this.pendingSettings=undefined;for(const c of this.requests)c.abort();for(const fn of this.listeners)fn();}
 dispose(){this.disposed=true;this.recoveredExport=undefined;for(const c of this.requests)c.abort();this.requests.clear();this.drafts.clear();this.intents.clear();this.settingsRead.clear();this.pendingSettings=undefined;this.listeners.clear();this.reconciled.clear();}
 setDraft(key:string,value:unknown){if(this.disposed||this.denied)return;if(value===undefined)this.drafts.delete(key);else this.drafts.set(key,value);}
 getDraft<T>(key:string):T|undefined{return this.drafts.get(key) as T|undefined;}
 hasDrafts(){return this.drafts.size>0||this.hasUnconfirmed();}
 hasUnconfirmed(){if(this.disposed||this.denied)return false;const scope=this.api.getUserMutationScope?.();return this.intents.size>0||!!this.api.getUserMutationCoordinator?.()?.snapshot().some(entry=>scope&&sameUserMutationScope(entry.scope,scope)&&(entry.path===routePath('change_console_settings')||entry.path===routePath('patch_preferences')));}
 takeRecoveredExport(){const value=this.recoveredExport;this.recoveredExport=undefined;return value&&value.expiresAt>Date.now()?value:undefined;}
 onReconciled(fn:()=>void){this.reconciled.add(fn);return()=>{this.reconciled.delete(fn);};}
 private async restoreIntent(path:string,method:string,parse:(v:unknown)=>unknown,recoverConfirmed=false){
  const coordinator=this.api.getUserMutationCoordinator?.();if(!coordinator||!userMutationRoute(path,method))return;
  await this.api.awaitUserMutationsReady?.();await coordinator.reconcile();const scope=this.api.getUserMutationScope?.();
  const retained=coordinator.snapshot().find(entry=>scope&&sameUserMutationScope(entry.scope,scope)&&entry.path===path&&entry.method===method);
  if(!this.intents.has(path)&&retained){
   const body=obj(retained.body)?{...retained.body}:undefined;if(body)delete body.idempotencyKey;
   if(!body&&!recoverConfirmed)throw new Error('A previous action has an unconfirmed result. Reconcile it before submitting another draft.');
   this.intents.set(path,{body:body?JSON.stringify(body):'{}',key:retained.intentId,method,parse,binding:retained.scope});
  }
 }
 async reconcilePending(){
  await this.restoreIntent(routePath('change_console_settings'),'PATCH',parseSettings,true);
  await this.restoreIntent(routePath('patch_preferences'),'PATCH',parsePreferenceSnapshot,true);
  const entry=this.intents.entries().next().value;if(!entry)return;
  const [path,intent]=entry;const body=JSON.parse(intent.body) as Record<string,unknown>;
  const live=this.api.getUserMutationScope?.();if(intent.binding&&(!live||!sameUserMutationScope(intent.binding,live)))throw new Error('The selected viewer changed. Review the original pending action.');
  try{const value=await this.call(path,intent.method,{...body,idempotencyKey:intent.key},intent.parse,intent);if(this.intents.get(path)===intent)this.intents.delete(path);
   if(path===routePath("post_admin_console_exports"))this.recoveredExport=value as SupportExport;
   if(path===routePath("get_console_settings")){this.drafts.delete('runtime-settings');this.pendingSettings=undefined;this.rememberSettings(value as SettingsDocument);}
   else if(path===routePath("get_preferences"))this.drafts.delete('preferences:'+body.scope);
   else if(path===routePath("get_admin_console_schedules"))this.drafts.delete('schedule');
   for(const fn of this.reconciled)fn();
  }catch(e){if(!(userMutationRoute(path,intent.method)&&this.api.getUserMutationCoordinator?.())&&obj(e)&&[400,404,409,410,429].includes(e.status)){if(this.intents.get(path)===intent)this.intents.delete(path);for(const fn of this.reconciled)fn();}throw e;}
 }
 private async call<T>(path:string,method:string,body:unknown,parse:(v:unknown)=>T,custody?:{ack?:Promise<void>}):Promise<T>{
  if(this.disposed||this.denied)throw new Error('This server scope is closed.');const c=new AbortController();this.requests.add(c);const timer=setTimeout(()=>c.abort(),10000);
  try{const raw=await (this.api.requestConsole?this.api.requestConsole<unknown>(path,method,body,c.signal):this.api.request<unknown>(path,method,body,c.signal));if(this.disposed||this.denied||c.signal.aborted)throw new Error('Console request ended.');
   if(!obj(raw)||!obj(raw.scope)||raw.scope.serverId!==this.scope.serverId||typeof raw.scope.viewerFence!=='string'||!/^[a-f0-9]{64}$/.test(raw.scope.viewerFence)||this.fence!==undefined&&this.fence!==raw.scope.viewerFence){this.revoke();throw new Error('The selected viewer changed. Reopen this server.');}
   const out=parse(raw.data);this.fence=raw.scope.viewerFence;const intent=body&&typeof body==='object'?(body as Record<string,unknown>).idempotencyKey:undefined;if(typeof intent==='string'){if(custody)await(custody.ack??=(this.api.acknowledgeUserMutation?.(intent)??Promise.resolve()).catch(error=>{custody.ack=undefined;throw error;}));else await this.api.acknowledgeUserMutation?.(intent);}return out;
  }catch(e){if(obj(e)&&(e.status===401||e.status===403))this.revoke();throw e;}finally{clearTimeout(timer);this.requests.delete(c);}
 }
 /**
  * A change guarded by the revision that was read. When the document moved in between (another
  * device, a job's own progress), a caller may reread and prove that its edited fields or command
  * still apply. A revision alone is never evidence that replaying an edit is safe.
  */
 private async mutate<T>(path:string,method:string,body:Record<string,unknown>,parse:(v:unknown)=>T,rebase?:()=>Promise<number|Readonly<{revision:number;body:Record<string,unknown>}>|undefined>):Promise<T>{
  const pending=this.intents.get(path);
  if(!userMutationRoute(path,method)&&pending&&pending.method===method&&'expectedRevision' in body){
   const sent=JSON.parse(pending.body) as Record<string,unknown>;
   // A rebased write whose answer was lost is still the same intent: replay its exact receipt key.
   if(JSON.stringify({...body,expectedRevision:sent.expectedRevision})===pending.body)body=sent;
  }
  for(let retries=0;;retries++){
   try{return await this.mutateOnce(path,method,body,parse);}
   catch(e){
    const current=obj(e)?e.currentRevision:undefined;
    if(userMutationRoute(path,method)||retries>=2||!rebase||method==='PUT'||!obj(e)||!['console_conflict','revision_mismatch'].includes(String(e.code))||typeof current!=='number'||!('expectedRevision' in body)||current===body.expectedRevision)throw e;
    const rebased=await rebase();if(rebased===undefined)throw e;
    body=typeof rebased==='number'?{...body,expectedRevision:rebased}:{...rebased.body,expectedRevision:rebased.revision};
   }
  }
 }
 private async mutateOnce<T>(path:string,method:string,body:Record<string,unknown>,parse:(v:unknown)=>T):Promise<T>{
  const serialized=JSON.stringify(body);
  if(this.api.getUserMutationCoordinator?.())await this.restoreIntent(path,method,parse);
  let intent=this.intents.get(path);
  if(intent&&intent.body!==serialized)throw new Error('A previous action has an unconfirmed result. Confirm the pending action before submitting different values.');
  if(!intent){intent={body:serialized,key:consoleOperationId(),method,parse,binding:this.api.getUserMutationScope?.()};this.intents.set(path,intent);}
  const live=this.api.getUserMutationScope?.();if(intent.binding&&(!live||!sameUserMutationScope(intent.binding,live)))throw new Error('The selected viewer changed. Review the original pending action.');
  try{const result=await this.call(path,method,{...JSON.parse(intent.body),idempotencyKey:intent.key},parse,intent);if(this.intents.get(path)===intent)this.intents.delete(path);return result;}
  catch(e){// Mounted cohort work stays owned until parsed acknowledgement or explicit review. Legacy commands keep their refusal semantics.
   if(!(userMutationRoute(path,method)&&this.api.getUserMutationCoordinator?.())&&obj(e)&&[400,404,409,410,412,428,429].includes(e.status)&&this.intents.get(path)===intent)this.intents.delete(path);throw e;}

 }
 registry(){return this.call(routePath("get_console_registry"),'GET',undefined,base);}
 /** Uses the same selected-server and viewer fence as every console read. */
 configuration(){return this.call(routePath("get_settings_configuration"),'GET',undefined,parseSettingsConfiguration);}
 async settings(){const doc=await this.call(routePath("get_console_settings"),'GET',undefined,parseSettings);this.rememberSettings(doc);return doc;}
 private rememberSettings(doc:SettingsDocument){this.settingsRead.set(doc.revision,JSON.parse(JSON.stringify(doc.requested)) as RuntimeSettings);if(this.settingsRead.size>8)this.settingsRead.delete(this.settingsRead.keys().next().value!);}
 async applySettings(expectedRevision:number,values:Partial<RuntimeSettings>,saved?:RuntimeSettings){
  const path=routePath("change_console_settings");
  let baseline=saved??this.settingsRead.get(expectedRevision)??(this.pendingSettings?.expectedRevision===expectedRevision?this.pendingSettings.baseline:undefined);
  if(!baseline){
   const current=await this.settings();
   if(current.revision!==expectedRevision)throw Object.assign(new Error('The settings changed. Reload them before saving.'),{status:409,code:'revision_mismatch',currentRevision:current.revision});
   baseline=current.requested;
  }
  const original=JSON.parse(JSON.stringify(baseline)) as RuntimeSettings,patch:Record<string,unknown>={};
  for(const [key,value] of Object.entries(values))if(JSON.stringify(value)!==JSON.stringify((original as unknown as Record<string,unknown>)[key]))patch[key]=value;
  const patchText=JSON.stringify(Object.fromEntries(Object.entries(patch).sort(([a],[b])=>a<b?-1:a>b?1:0)));
  const pending=this.intents.get(path);
  if(pending&&this.pendingSettings?.patch!==patchText)throw new Error('A previous action has an unconfirmed result. Confirm the pending action before submitting different values.');
  // Complete wire documents come only from a captured server read. The sparse
  // patch is retained solely to prove safe rebases and identify a lost response.
  const body:Record<string,unknown>=pending?JSON.parse(pending.body):{expectedRevision,values:{...original,...patch}};
  if(!pending)this.pendingSettings={expectedRevision,baseline:original,patch:patchText};
  try{
   const doc=await this.mutate(path,'PATCH',body,parseSettings,async()=>{
    const current=await this.settings();
    for(const [key,value] of Object.entries(patch)){
     const now=(current.requested as unknown as Record<string,unknown>)[key],before=(original as unknown as Record<string,unknown>)[key];
     if(JSON.stringify(now)!==JSON.stringify(before)&&JSON.stringify(now)!==JSON.stringify(value))return undefined;
    }
    return {revision:current.revision,body:{expectedRevision:current.revision,values:{...current.requested,...patch}}};
   });
   this.rememberSettings(doc);return doc;
  }finally{if(!this.intents.has(path))this.pendingSettings=undefined;}
 }
 preferences(deviceClass:DeviceClass){return this.call(routePath("get_preferences") + "?deviceClass=" + (deviceClass),'GET',undefined,parsePreferenceSnapshot);}
 /** `values` carries only the keys that change; `null` clears that override. */
 applyPreferences(scope:PreferenceScope,deviceClass:DeviceClass,expectedRevision:number,values:PreferencePatch){return this.mutate(routePath("patch_preferences"),'PATCH',{scope,deviceClass,expectedRevision,values},parsePreferenceSnapshot);}
 /** Owner triage: every viewer report, newest first, a page the server bounds. */
 feedbackReports(cursor=''){return this.call(routePath("get_admin_feedback_reports")+(cursor?'?cursor='+encodeURIComponent(cursor):''),'GET',undefined,parseFeedbackList);}
 feedbackReport(id:string){return this.call(encodedRoutePath('get_admin_feedback_reports_id',{id:encodeURIComponent(id)}),'GET',undefined,parseFeedbackReport);}
 /** Status and reply behind the read revision. The caller keeps one operationId per
  * intended change, so a retry after a lost answer is the same transition. */
 transitionFeedback(id:string,change:FeedbackTransition){return this.call(encodedRoutePath('post_admin_feedback_reports_id_status',{id:encodeURIComponent(id)}),'POST',{...change},parseFeedbackReport);}
 inbox(cursor=''){return this.call(routePath("get_notifications") + "?cursor=" + (encodeURIComponent(cursor)),'GET',undefined,parseInbox);}
 readNotice(id:string){return this.call(encodedRoutePath("post_notifications_id_read", {"id": encodeURIComponent(id)}),'POST',{},v=>{if(!obj(v)||v.read!==true)invalid();});}
 accountOptions(){return this.call(routePath("get_admin_console_accounts"),'GET',undefined,v=>list(v,row=>{if(!obj(row)||!id(row.id)||!text(row.label,128))invalid();return {id:row.id as string,label:row.label as string};},1000));}
 streamOptions(){return this.call(routePath("get_admin_console_stream_options"),'GET',undefined,parseStreamOptions);}
 jobs(cursor=''){return this.call(routePath("get_admin_console_jobs") + "?cursor=" + (encodeURIComponent(cursor)),'GET',undefined,v=>parsePage(v,parseJob));}
 jobKinds(){return this.call(routePath("get_admin_console_job_kinds"),'GET',undefined,v=>list(v,k=>{const d=base(k);if(!id(d.kind)||typeof d.resourceRequired!=='boolean')invalid();return d as JobKind;}));}
 runJob(kind:string,resource:string){return this.mutate(routePath("post_admin_console_jobs"),'POST',{kind,resource},parseJob);}
 jobCommand(job:Job,action:'cancel'|'retry'|'pause'|'resume'){return this.mutate(encodedRoutePath("post_admin_console_jobs_id_action", {"id": encodeURIComponent(job.id), "action": action}),'POST',{expectedRevision:job.revision},parseJob,async()=>{const current=(await this.jobs()).items.find(j=>j.id===job.id);return current?.actions.includes(action)?current.revision:undefined;});}
 schedules(){return this.call(routePath("get_admin_console_schedules"),'GET',undefined,v=>list(v,parseSchedule));}
 saveSchedule(value:Schedule,expectedRevision:number){const {revision:_revision,lastSlot:_lastSlot,createdAt:_createdAt,...editable}=value;return this.mutate(routePath("put_admin_console_schedules"),'PUT',{expectedRevision,value:editable},parseSchedule);}
 measurement(name:string){return this.call(encodedRoutePath("get_admin_console_panels_panel", {"panel": encodeURIComponent(name)}),'GET',undefined,v=>{const p=base(wireInstants(v,["observedAt","freshUntil"]));if(p.name!==name||!integer(p.observedAt)||p.freshUntil!==p.observedAt+30000||!obj(p.facts)||Object.keys(p.facts).length>30)invalid();const facts:Measurement['facts']={};for(const [key,f] of Object.entries(p.facts)){if(obj(f)&&text(f.state,40))facts[key]=f as Measurement['facts'][string];else noteSkipped('console','measurement.fact');}return {...p,facts} as Measurement;});}
 alerts(){return this.call(routePath("get_admin_console_alerts"),'GET',undefined,v=>list(v,a=>{const p=base(a);revision(p);if(!id(p.id)||!id(p.code))invalid();return p as Alert;}));}
 acknowledge(alert:Alert){return this.mutate(encodedRoutePath("post_admin_console_alerts_id_acknowledge", {"id": encodeURIComponent(alert.id)}),'POST',{expectedRevision:alert.revision},base);}
 records(lane:'runtime'|'client'|'audit',cursor=''){return this.call(encodedRoutePath("get_admin_console_diagnostics_lane", {"lane": lane}) + "?cursor=" + (encodeURIComponent(cursor)),'GET',undefined,v=>parsePage(v,parseEvidenceRecord));}
 capture(component:string,expiresAt:number){return this.mutate(routePath("post_admin_console_diagnostics_capture"),'POST',{component,expiresAt:new Date(expiresAt).toISOString()},base);}
 createExport(from:number,to:number,components:string[],includePaths?:boolean){if(includePaths!==undefined&&typeof includePaths!=='boolean')return Promise.reject(new Error('Invalid export option.'));return this.mutate(routePath("post_admin_console_exports"),'POST',{from:new Date(from).toISOString(),to:new Date(to).toISOString(),components,...(includePaths===undefined?{}:{includePaths})},parseExport);}
 export(id:string){return this.call(encodedRoutePath("get_admin_console_exports_id", {"id": encodeURIComponent(id)}),'GET',undefined,parseExport);}
 /** Transcoding capacity, probes and dependencies as this server reports them. */
 transcodeCapacity(){return this.call(routePath("get_admin_transcode_capacity"),'GET',undefined,parseTranscodeCapacity);}
 /** Charted host and GPU history. The server chooses the resolution per window. */
 telemetry(window:TelemetryWindow,context:ReportContext){const q=reportQuery(context);q.set('window',window);return this.call(routePath('get_admin_telemetry')+'?'+q,'GET',undefined,v=>{const out=parseTelemetryReading(v);verifyReportInterval(out,context,window,window==='live'||window==='10m');return out;});}
 telemetryNow(){return this.call(routePath("get_admin_telemetry_now"),'GET',undefined,parseTelemetrySample);}
 attention(){return this.call(routePath("get_admin_attention"),'GET',undefined,parseAttention);}
 playbackHistory(period:PlaybackHistoryPeriod='24h',cursor='',limit=50){
  return this.call(routePath("get_admin_playback_history") + "?" + (new URLSearchParams({period,cursor,limit:String(limit)})),'GET',undefined,parsePlaybackHistory);
 }
 /** The export is a download, so its path is handed to the browser, not fetched. */
 playbackHistoryExportPath(period:PlaybackHistoryPeriod='24h'){return routePath("get_admin_playback_history_csv") + "?period=" + (period);}
}
function parseExport(v:unknown):SupportExport{
 const p=base(wireInstants(v,['expiresAt']));if(obj(p.manifest))p.manifest=wireInstants(p.manifest,['from','to']);
 if(!id(p.id)||!integer(p.expiresAt)||!obj(p.manifest)||!Array.isArray(p.manifest.included)||!Array.isArray(p.manifest.excluded)||!text(p.manifest.redaction,500))invalid();
 const m=p.manifest;
 for(const key of ['includePaths','pathsTruncated','pathsScanTruncated'])if(m[key]!==undefined&&typeof m[key]!=='boolean')invalid();
 if(m.pathCategories!==undefined&&(!Array.isArray(m.pathCategories)||m.pathCategories.length>3||m.pathCategories.some((value:unknown)=>!SUPPORT_PATH_CATEGORIES.includes(value as SupportPathCategory))))invalid();
 if(m.pathsInvalidAmongExamined!==undefined&&(!integer(m.pathsInvalidAmongExamined)||m.pathsInvalidAmongExamined>512))invalid();
 if(p.snapshotPaths!==undefined){
  const paths=p.snapshotPaths;
  if(!m.includePaths||!m.included.includes('snapshot')||!obj(paths)||Object.keys(paths).some(key=>!['limit','hasMore','scanBudget','examined','scanTruncated','invalidAmongExamined','items'].includes(key))||paths.limit!==128||paths.scanBudget!==512||typeof paths.hasMore!=='boolean'||typeof paths.scanTruncated!=='boolean'||!integer(paths.examined)||paths.examined>512||!integer(paths.invalidAmongExamined)||paths.invalidAmongExamined>paths.examined||!Array.isArray(paths.items)||paths.items.length>128||paths.items.length+paths.invalidAmongExamined>paths.examined||paths.hasMore&&(paths.items.length!==128||paths.examined<paths.items.length+paths.invalidAmongExamined+1)||paths.scanTruncated&&(paths.examined!==512||paths.hasMore))invalid();
  for(const [index,item] of paths.items.entries())if(!obj(item)||Object.keys(item).some(key=>!['category','reference','value'].includes(key))||!SUPPORT_PATH_CATEGORIES.includes(item.category)||!text(item.reference,16)||item.reference!==`path-${index+1}`||!text(item.value,4096)||!item.value||/\p{Cc}/u.test(item.value))invalid();
  const categories=[...new Set(paths.items.map((item:{category:SupportPathCategory})=>item.category))].sort();
  if(JSON.stringify(categories)!==JSON.stringify([...(m.pathCategories??[])].sort())||(m.pathsTruncated??false)!==paths.hasMore||(m.pathsScanTruncated??false)!==paths.scanTruncated||(m.pathsInvalidAmongExamined??0)!==paths.invalidAmongExamined)invalid();
 }else if(m.includePaths&&m.included.includes('snapshot'))invalid();
 if(p.snapshotPaths===undefined&&((m.pathCategories?.length??0)>0||m.pathsTruncated||m.pathsScanTruncated||m.pathsInvalidAmongExamined))invalid();
 return p as SupportExport;
}


export function parseStreamOptions(value:unknown):StreamOptions{const v=base(value);for(const field of ['libraries','viewers'])if(!Array.isArray(v[field])||v[field].length>500)invalid();const rows=(field:'libraries'|'viewers')=>distinct(keepReadable('console','stream-options.'+field,v[field] as unknown[],row=>{if(!obj(row)||!text(row.id,300)||!row.id||!text(row.label,4096))invalid();return row as {id:string;label:string};}));return {...v,libraries:rows('libraries'),viewers:rows('viewers')} as StreamOptions;}
