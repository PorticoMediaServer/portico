import type {RecordingSpace} from './next-dvr.ts';
import {unreadableServerResponse} from './server-messages.ts';
import {serviceProblem, serviceText} from './presentation/service-text.ts';
import type {MessageId} from '../../i18n/src/index.ts';
import {firstOfEach, keepReadable, oneOfOr} from './tolerant.ts';
import {wireDurations} from './wire-units.ts';
/** The recordings screens' types, parsers and model; the rebuilt server's client is `NextDVRClient` (next-dvr.ts). */
import {parseGuideProgramme} from './channel-guide.ts';
import type {GuideChannel, GuideProgramme} from './channel-guide';
export type DVRView = 'upcoming' | 'recorded' | 'rules' | 'history' | 'storage';
export type RecordingState = 'scheduled' | 'conflicted' | 'skipped' | 'waiting-source' | 'waiting-guide' | 'preparing' | 'recording' | 'finalizing' | 'completed' | 'incomplete-playable' | 'failed' | 'cancelled' | 'pending-delete' | 'deleted';
export type RecordingOptions = {beforeSeconds: number; afterSeconds: number; priority: number; retentionDays: number; episodeLimit: number};
export type Occurrence = {sourceId: string; channelId: string; generation: string; programmeId: string};
export type RecordingChannel = {id:string;name:string;number:string;mark?:{text:string;seed:string}};
export type Recording = {id: string; revision: number; occurrence: Occurrence; programme: GuideProgramme; ruleId: string; options: RecordingOptions; start: string; end: string; state: RecordingState; reason: string; keep: boolean; itemId: string; libraryId: string; coverageStart: string; coverageEnd: string; bytes: number; channel?: RecordingChannel; watched?: boolean|null;
 /** FEAT-08: the show this recording belongs to (rows group by it); absent for one-offs. */
 seriesId?: string; seriesTitle?: string; positionMs?:number;durationMs?:number;overlaps?:RecordingOverlap[];
 /** When it clashes; how many tuners there are and how many it needs only when the server says. */
 conflicts: {start: string; end: string; demand?: number; capacity?: number}[];
 /** Who booked it, by name (rule 48). */
 bookedBy?: string;
 /** What holds the tuners at a conflict's time: this account's recordings by title, anyone else's as "A recording" (rule 42). */
 tunersUsedBy?: string[];
 /** Whether this profile may change, stop or delete it (`mayChange`), and cancel a booking not yet started (`mayCancel`). Absent
  * from a server that does not say: the screen then decides as before. */
 mayChange?: boolean; mayCancel?: boolean};
export type RuleConfig = {matchKind?:"series"|"title";title?:string;channelId?:string;name: string; sourceId: string; seriesId: string; enabled: boolean; episodes: 'all' | 'new'; allowedChannels: string[]; blockedChannels: string[]; keywords: string[]; blockedKeywords: string[]; options: RecordingOptions};
export type RecordingRule = {id: string; revision: number; config: RuleConfig; reconcileState: string; diagnostic: string; updatedAt: string;summary?:RuleSummary;
 /** The channel a rule records from, when the server names it without a summary. */
 channel?:RecordingChannel};
export type DVRPage = {recordings: Recording[]; rules: RecordingRule[]; nextCursor: string; revision: number; captureAvailable: boolean; captureUnavailableReason: string; deletionAvailable: boolean; usage: {bytes: number; pendingDeleteBytes: number; recordings: number}};
export type Capacity = {known: boolean; effective: number; planningEstimate?: number; mode?: string};
export type RecordingDetail = {recording: Recording; capacity: Capacity; overlaps: RecordingOverlap[]; nextOverlap: string};
export type RulePreview = {matches: number; unknownNewEvidence: number; sample: GuideProgramme[]; generation: string; capacity: Capacity};
export type DeletePreview = {recordingId: string; revision: number; bytes: number; keep: boolean; activeReaders: boolean; result: string};
export type RecordingDraft = {channel: GuideChannel; programme: GuideProgramme; series: boolean};
export const defaultRecordingOptions: RecordingOptions = {beforeSeconds: 0, afterSeconds: 0, priority: 0, retentionDays: 0, episodeLimit: 0};
export const dvrViews: readonly DVRView[] = ['upcoming', 'recorded', 'rules', 'history', 'storage'];
export const viewTitle: Record<DVRView, string> = {upcoming: 'Upcoming', recorded: 'Recorded', rules: 'Series rules', history: 'History', storage: 'Storage'};
const states: readonly string[] = ['scheduled','conflicted','waiting-source','waiting-guide','preparing','recording','finalizing','completed','incomplete-playable','failed','cancelled','pending-delete','deleted'];
const object = (v: unknown): v is Record<string, any> => !!v && typeof v === 'object' && !Array.isArray(v);
const text = (v: unknown, max = 4096): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const number = (v: unknown, min = 0, max = Number.MAX_SAFE_INTEGER): v is number => Number.isSafeInteger(v) && Number(v) >= min && Number(v) <= max;
const id = (v: unknown, length = 64): v is string => typeof v === 'string' && new RegExp(`^[a-f0-9]{${length}}$`).test(v);
const instant = (v: unknown): v is string => typeof v === 'string' && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(v) && Number.isFinite(Date.parse(v)) && new Date(v).toISOString().slice(0,19) === v.slice(0,19);
function invalid(): never { throw new Error(unreadableServerResponse); }
export function validRecordingOptions(v: unknown): v is RecordingOptions {return object(v) && number(v.beforeSeconds,0,21600) && number(v.afterSeconds,0,21600) && number(v.priority,-1000,1000) && number(v.retentionDays,0,3650) && number(v.episodeLimit,0,100000);}
function programme(v: unknown): GuideProgramme {
 if (!object(v) || !id(v.id) || !id(v.channelId) || !text(v.title,512) || !instant(v.start) || !instant(v.end) || Date.parse(v.start)>=Date.parse(v.end)) invalid();
 // As in the guide: evidence of a kind this app does not know reads as the weakest.
 const lineage=oneOfOr('dvr','programme.lineage',v.lineage,['provider-id','interval-only','generated'] as const,'interval-only');
 const seriesId=v.seriesId??'',episodeId=v.episodeId??'',description=text(v.description??'',8192)?v.description??'':'',newEvidence=oneOfOr('dvr','programme.newEvidence',v.newEvidence||undefined,['new','repeat','unknown'] as const,'unknown');
 if(!text(seriesId,512)||!text(episodeId,512))invalid();
 const facts=parseGuideProgramme({...v,seriesId:'',episodeId:''},v.channelId);
 const image=typeof v.image==='string'&&/^\/v1\/dvr\/recordings\/[a-f0-9]{64}\/image$/.test(v.image)?v.image:facts.image;
 return {...facts,lineage,seriesId,episodeId,description,newEvidence,...(image?{image}:{})};
}
function parseRecordingChannel(v:unknown,sourceId?:string,requireId=false):RecordingChannel {
 if(!object(v)||!(requireId?id(v.id):text(v.id,64))||!text(v.name,256)||!text(v.number,32))invalid();
 let mark:RecordingChannel['mark'];
 if(v.mark!==undefined){
  if(!object(v.mark)||!text(v.mark.text,1024)||!v.mark.text.trim()||!text(v.mark.seed,513)||!v.mark.seed.trim()||sourceId!==undefined&&v.mark.seed!==`${sourceId}:${v.id}`)invalid();
  mark={text:v.mark.text,seed:v.mark.seed};
 }
 return {id:v.id,name:v.name,number:v.number,...(mark?{mark}:{})};
}
export function parseRecording(wire: unknown): Recording {
 const v: unknown = optionsFromWire(wire);
 if(!object(v)||!id(v.id)||!number(v.revision,1)||!object(v.occurrence)||!id(v.occurrence.sourceId,48)||!id(v.occurrence.channelId)||!id(v.occurrence.generation,48)||!id(v.occurrence.programmeId)||!validRecordingOptions(v.options)||!instant(v.start)||!instant(v.end)||!states.includes(v.state)||!text(v.reason,256)||typeof v.keep!=='boolean'||!text(v.ruleId,64)||v.ruleId!==''&&!id(v.ruleId)||!text(v.itemId,64)||!text(v.libraryId,64)||v.itemId!==''&&(!id(v.itemId)||!id(v.libraryId))||!number(v.bytes)||!Array.isArray(v.conflicts)||v.conflicts.length>100000)invalid();
 if(!(v.coverageStart===''||instant(v.coverageStart))||!(v.coverageEnd===''||instant(v.coverageEnd)))invalid();
 for(const c of v.conflicts)if(!object(c)||!instant(c.start)||!instant(c.end)||Date.parse(c.start)>=Date.parse(c.end)||!number(c.demand,1)||!number(c.capacity))invalid();
 const p=programme(v.programme);if(p.id!==v.occurrence.programmeId||p.channelId!==v.occurrence.channelId)invalid();
 if(Date.parse(v.start)>=Date.parse(v.end)||!!v.coverageStart!==!!v.coverageEnd||v.coverageStart&&Date.parse(v.coverageStart)>Date.parse(v.coverageEnd))invalid();
 // Part 2.2: channel and watched are optional (older servers omit them); render only when present.
 let channel: Recording['channel'];
 if(v.channel!==undefined){
  channel=parseRecordingChannel(v.channel,v.occurrence.sourceId,true);
 }
 let watched: Recording['watched'];
 if(v.watched!==undefined){
  if(v.watched!==null&&typeof v.watched!=='boolean')invalid();
  watched=v.watched;
 }
 // FEAT-08: optional grouping key and show name; an unreadable value is left out, never fatal.
 const series={...(text(v.seriesId,512)&&v.seriesId?{seriesId:v.seriesId}:{}),...(text(v.seriesTitle,512)&&v.seriesTitle?{seriesTitle:v.seriesTitle}:{})};
 const options:RecordingOptions={beforeSeconds:v.options.beforeSeconds,afterSeconds:v.options.afterSeconds,priority:v.options.priority,retentionDays:v.options.retentionDays,episodeLimit:v.options.episodeLimit};
 const progress={...(v.positionMs===undefined?{}:{positionMs:checkedNumber(v.positionMs)}),...(v.durationMs===undefined?{}:{durationMs:checkedNumber(v.durationMs)}),...(v.overlaps===undefined?{}:{overlaps:parseOverlaps(v.overlaps)})};
 return {...progress,id:v.id,revision:v.revision,occurrence:{sourceId:v.occurrence.sourceId,channelId:v.occurrence.channelId,generation:v.occurrence.generation,programmeId:v.occurrence.programmeId},programme:p,ruleId:v.ruleId,options,start:v.start,end:v.end,state:v.state,reason:v.reason,keep:v.keep,itemId:v.itemId,libraryId:v.libraryId,coverageStart:v.coverageStart,coverageEnd:v.coverageEnd,bytes:v.bytes,...(channel===undefined?{}:{channel}),...(watched===undefined?{}:{watched}),conflicts:v.conflicts.map((c:any)=>({start:c.start,end:c.end,demand:c.demand,capacity:c.capacity})),...series} as Recording;
}
/** Part 2.2: unwatched counts only watched===false; null (unpublished) counts as neither. */
export function countUnwatched(recordings: readonly Pick<Recording,'watched'>[]): number {
  return recordings.filter(r=>r.watched===false).length;
}
/** Padding travels in milliseconds; the options here keep the seconds the pickers offer. */
const PADDING = {beforeSeconds: 'beforeMs', afterSeconds: 'afterMs'} as const;
function optionsFromWire(v:unknown):unknown{return object(v)&&object(v.options)?{...v,options:wireDurations(v.options,PADDING)}:v;}
function config(v:unknown):RuleConfig{
 if(!object(v)||!text(v.name,160)||!v.name||!id(v.sourceId,48)||!text(v.seriesId,512)||(v.matchKind==='title'?(!text(v.title,512)||!v.title||!id(v.channelId)||v.seriesId!==''):!v.seriesId)||typeof v.enabled!=='boolean'||!['all','new'].includes(v.episodes)||!validRecordingOptions(v.options))invalid();
 for(const k of ['allowedChannels','blockedChannels'])if(!Array.isArray(v[k])||v[k].length>2000||v[k].some((x:unknown)=>!id(x)))invalid();
 for(const k of ['keywords','blockedKeywords'])if(!Array.isArray(v[k])||v[k].length>20||v[k].some((x:unknown)=>!text(x,128)||!x.trim()))invalid();
 return {...(v.matchKind===undefined?{}:{matchKind:oneOfOr("dvr","rule.matchKind",v.matchKind,["series","title"] as const,"series")}),...(v.title===undefined?{}:{title:v.title}),...(v.channelId===undefined?{}:{channelId:v.channelId}),name:v.name,sourceId:v.sourceId,seriesId:v.seriesId,enabled:v.enabled,episodes:v.episodes,allowedChannels:[...v.allowedChannels],blockedChannels:[...v.blockedChannels],keywords:[...v.keywords],blockedKeywords:[...v.blockedKeywords],options:{beforeSeconds:v.options.beforeSeconds,afterSeconds:v.options.afterSeconds,priority:v.options.priority,retentionDays:v.options.retentionDays,episodeLimit:v.options.episodeLimit}};
}
function rule(v:unknown):RecordingRule{if(!object(v)||!id(v.id)||!number(v.revision,1)||!text(v.reconcileState,64)||!text(v.diagnostic,256)||!instant(v.updatedAt))invalid();return {id:v.id,revision:v.revision,config:config(optionsFromWire(v.config)),reconcileState:v.reconcileState,diagnostic:v.diagnostic,updatedAt:v.updatedAt,...(v.summary===undefined?{}:{summary:parseRuleSummary(v.summary)})} as RecordingRule;}
/**
 * One page of recordings and rules. The cursor, the revision and the bounds are exact. A
 * recording or a rule that cannot be read (or is in a state this app does not know, so it could
 * not offer the right actions) is left out alone, and a repeated one keeps its first row.
 * Recording and deleting are offered only when the server says so outright; the usage figures are
 * only shown and read as 0 when they cannot be read.
 */
export function parseDVRPage(v: unknown): DVRPage {
 if(!object(v)||!Array.isArray(v.recordings)||v.recordings.length>50||!Array.isArray(v.rules)||v.rules.length>50||!text(v.nextCursor,4096)||!number(v.revision))invalid();
 const recordings=firstOfEach('dvr','recordings',keepReadable('dvr','recordings',v.recordings as unknown[],parseRecording),r=>r.id),rules=firstOfEach('dvr','rules',keepReadable('dvr','rules',v.rules as unknown[],rule),r=>r.id);
 const usage=object(v.usage)?v.usage:{},shown=(n:unknown)=>number(n)?n:0;
 return {recordings,rules,nextCursor:v.nextCursor,revision:v.revision,captureAvailable:v.captureAvailable===true,captureUnavailableReason:text(v.captureUnavailableReason,256)?v.captureUnavailableReason:'',deletionAvailable:v.deletionAvailable===true,usage:{bytes:shown(usage.bytes),pendingDeleteBytes:shown(usage.pendingDeleteBytes),recordings:shown(usage.recordings)}} as DVRPage;
}
export function dvrMessage(error: unknown): string {
 const status=object(error)?error.status:0;
 if(status===401||status===403)return serviceText('dvr.error.access');
 if(status===409)return serviceText('dvr.error.changed');
 if(status===422)return serviceText('dvr.error.unsupported');
 if(status===400)return serviceText('dvr.error.invalid');
 return serviceProblem(error,'live','dvr.error.unconfirmed');
}
export function isAccessError(error: unknown): boolean {return object(error)&&(error.status===401||error.status===403);}
const stateIds:Record<string,MessageId>={scheduled:'dvr.state.scheduled',conflicted:'dvr.state.conflicted','waiting-source':'dvr.state.waitingSource','waiting-guide':'dvr.state.waitingGuide',preparing:'dvr.state.preparing',recording:'dvr.state.recording',finalizing:'dvr.state.finalizing',completed:'dvr.state.completed','incomplete-playable':'dvr.state.incompletePlayable',incomplete:'dvr.state.incomplete',failed:'dvr.state.failed',cancelled:'dvr.state.cancelled','pending-delete':'dvr.state.pendingDelete',deleted:'dvr.state.deleted'};
const reasonIds:Record<string,MessageId>={'capture-window-missed':'dvr.reason.missed','tuner-conflict':'dvr.reason.conflict','tuner-in-use-live':'dvr.reason.liveInUse','storage-floor':'dvr.reason.storageFloor','storage-cap':'dvr.reason.storageCap','owner-cancelled':'dvr.reason.cancelled','source-disabled':'dvr.reason.sourceDisabled','source-changed':'dvr.reason.sourceChanged','guide-occurrence-missing':'dvr.reason.guideMissing','partial-capture':'dvr.reason.partial','physical-reader-active':'dvr.reason.inUse','active-reader-grace':'dvr.reason.graceWait','artifact-remove-unavailable':'dvr.reason.removeRetry','recovered-partial':'dvr.reason.recoveredPartial'};
/** A recording's state in the viewer's language (catalogue `dvr.state.*`). */
export function recordingStateLabel(state: string): string {const id=stateIds[state];return id?serviceText(id):state.replace(/-/g,' ').replace(/^./,c=>c.toUpperCase());}
/** Why a recording is in its state (catalogue `dvr.reason.*`); empty when there's no reason. */
export function recordingReason(reason:string):string {
 if(!reason)return '';const id=reasonIds[reason];return id?serviceText(id):reason.replaceAll('-',' ');
}
export const mutableRecording=(r:Recording)=>['scheduled','conflicted','waiting-source','waiting-guide'].includes(r.state);
export const activeRecording=(r:Recording)=>mutableRecording(r)||['preparing','recording','finalizing'].includes(r.state);
export const playableRecording=(r:Recording)=>!!r.itemId&&!!r.libraryId&&['completed','incomplete-playable'].includes(r.state);
export function occurrence(d:RecordingDraft):Occurrence{return {sourceId:d.channel.sourceId,channelId:d.channel.id,generation:d.channel.generation,programmeId:d.programme.id};}
export function draftRule(d:RecordingDraft):RuleConfig{return {...(!d.programme.seriesId?{matchKind:"title",title:d.programme.title,channelId:d.channel.id}:{}),name:d.programme.title,sourceId:d.channel.sourceId,seriesId:d.programme.seriesId,enabled:true,episodes:'all',allowedChannels:[],blockedChannels:[],keywords:[],blockedKeywords:[],options:{...defaultRecordingOptions}};}
export type SeriesRuleUi = {episodes: 'all'|'new'; keep: number; beforeSeconds: number; afterSeconds: number; anyChannel: boolean};
/**
 * FEAT-02: rule-editor state → RuleConfig (plus the anchor). Both clients' record-series dialogs
 * build through this so the guide sheet and the rule editor offer the same bounded choices.
 * Out-of-range values fail via config(), never reach the server.
 */
export function seriesRuleConfig(d:RecordingDraft,ui:SeriesRuleUi):{config:RuleConfig;anchor:Occurrence}{
 if(ui.episodes!=='all'&&ui.episodes!=='new')invalid();
 const draft=draftRule({...d,series:true});
 draft.episodes=ui.episodes;
 draft.allowedChannels=ui.anyChannel&&draft.matchKind!=="title"?[]:[d.channel.id];
 draft.options={...defaultRecordingOptions,beforeSeconds:ui.beforeSeconds,afterSeconds:ui.afterSeconds,episodeLimit:ui.keep};
 return {config:config(draft),anchor:occurrence(d)};
}
export function bytesLabel(bytes:number):string{if(!Number.isFinite(bytes)||bytes<0)return 'Unknown';const units=['B','KiB','MiB','GiB','TiB'];let n=0;while(bytes>=1024&&n<4){bytes/=1024;n++}return `${bytes.toLocaleString(undefined,{maximumFractionDigits:n?1:0})} ${units[n]}`;}
export function recordingTime(value:string):string{return value?new Date(value).toLocaleString(undefined,{month:'short',day:'numeric',hour:'numeric',minute:'2-digit',timeZoneName:'short'}):serviceText('dvr.notCaptured');}

/** Optional original caller scope; omitted callers keep the viewer client's existing lifetime. */
export type DVRCommandLifetime = Readonly<{signal: AbortSignal; isCurrent: () => boolean}>;


export type DVRQuery = {view:DVRView;cursor?:string;recordingId?:string};
/** What the model reads through: the recordings, one recording, and the recordings' space. */
export type DVRModelClient = {onAccessChanged(listener:()=>void):void;activate():void;dispose():void;detail(recordingId:string,after?:string):Promise<RecordingDetail>;storage():Promise<RecordingSpace>;list(view:Exclude<DVRView,'storage'>,cursor?:string):Promise<DVRPage>};
export type DVRSnapshot = Readonly<{query:DVRQuery;page:DVRPage|null;storage:RecordingSpace|null;detail:RecordingDetail|null;loading:boolean;mutating:boolean;accessDenied:boolean;error:string;revision:number}>;
/** Observable query state, not a navigator. All destinations are supplied by the
 * application's existing LibraryNavigationService. Late reads cannot revive a
 * previous route or viewer; authorization errors clear private presentation. */
export class DVRModel {
 private listeners=new Set<()=>void>();private epoch=0;private overlapRequest=0;private mutation=0;private closed=false;private activation=0;
 private snapshot:DVRSnapshot={query:{view:'upcoming'},page:null,storage:null,detail:null,loading:false,mutating:false,accessDenied:false,error:'',revision:0};
 readonly client:DVRModelClient;constructor(client:DVRModelClient){this.client=client;this.observeAccess();}
 private observeAccess(){this.client.onAccessChanged(()=>{this.epoch++;this.publish({page:null,detail:null,storage:null,loading:false,mutating:false,accessDenied:true,error:dvrMessage({status:403})});});}
 activate(){const lease=++this.activation;this.epoch++;this.closed=false;this.client.activate();this.observeAccess();this.publish({page:null,detail:null,storage:null,loading:false,mutating:false,accessDenied:false,error:''});return lease;}
 deactivate(lease:number){if(lease===this.activation)this.dispose();}
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn)};
 getSnapshot=()=>this.snapshot;
 private publish(p:Partial<DVRSnapshot>){if(this.closed)return;this.snapshot=Object.freeze({...this.snapshot,...p,revision:this.snapshot.revision+1});for(const fn of this.listeners)fn();}
 dispose(){this.closed=true;this.epoch++;this.client.dispose();this.snapshot=Object.freeze({...this.snapshot,page:null,storage:null,detail:null,loading:false,mutating:false,error:'',revision:this.snapshot.revision+1});this.listeners.clear();}
 async load(query:DVRQuery=this.snapshot.query){
  const epoch=++this.epoch,same=JSON.stringify(query)===JSON.stringify(this.snapshot.query);
  this.publish({query,loading:true,error:'',...(!same?{page:null,storage:null,detail:null}:{})});
  try{if(query.recordingId){const detail=await this.client.detail(query.recordingId);if(epoch===this.epoch)this.publish({detail,loading:false,accessDenied:false});}
   else if(query.view==='storage'){const storage=await this.client.storage();if(epoch===this.epoch)this.publish({storage,loading:false,accessDenied:false});}
   else{const page=await this.client.list(query.view,query.cursor);if(epoch===this.epoch)this.publish({page,loading:false,accessDenied:false});}}
  catch(e){if(epoch===this.epoch)this.publish({loading:false,error:dvrMessage(e),...(isAccessError(e)?{page:null,storage:null,detail:null,accessDenied:true}:{})});}
 }
 async act(work:()=>Promise<unknown>):Promise<boolean>{if(this.snapshot.mutating||this.closed)return false;const epoch=this.epoch,mutation=++this.mutation;this.publish({mutating:true,error:''});try{await work();if(this.closed||epoch!==this.epoch)return false;await this.load();return true;}catch(e){/* A refused command (403) is a refusal of that command; only a lost sign-in (401) retires what is shown. */if(!this.closed&&epoch===this.epoch)this.publish({error:dvrMessage(e),...((e as {status?:unknown}|null)?.status===401?{page:null,storage:null,detail:null,accessDenied:true}:{})});return false;}finally{if(mutation===this.mutation)this.publish({mutating:false});}}
 async moreOverlaps(){
  const d=this.snapshot.detail,epoch=this.epoch,request=++this.overlapRequest;if(!d?.nextOverlap)return;
  try{const next=await this.client.detail(d.recording.id,d.nextOverlap);
   if(epoch!==this.epoch||request!==this.overlapRequest)return;
   if(next.recording.id!==d.recording.id){await this.load();return;}
   const overlaps=[...new Map([...d.overlaps,...next.overlaps].map(o=>[o.id,o])).values()];
   this.publish({detail:{...next,overlaps}});
  }catch(e){if(epoch===this.epoch&&request===this.overlapRequest)this.publish({error:dvrMessage(e),...(isAccessError(e)?{page:null,storage:null,detail:null,accessDenied:true}:{})});}
 }
}

export type RecordingOverlap={id:string;title:string;start:string;end:string;priority:number;state:string;channel?:RecordingChannel;ruleId?:string;episode?:GuideProgramme['episode']};
export type RuleSummary={upcoming:number;recorded:number;conflicts:number;matchingTonight:number;next?:GuideProgramme;channel:RecordingChannel};
export type DVRSummary={upcoming:number;recorded:number;rules:number;conflicts:number;groups:number;unwatched:number;
 /** Whether this profile may see everyone's recordings (the Recorded TV library); absent when the server does not say. */
 everyonesRecordings?:boolean};
export type RecordingGroups={groups:{key:string;title:string;recordings:number;unwatched:number;kept:number;exceptions:number;latest:string;recording:Recording}[];nextCursor:string;revision:number};
function checkedNumber(v:unknown):number{if(!number(v))invalid();return v;}
function parseOverlaps(v:unknown):RecordingOverlap[]{if(!Array.isArray(v)||v.length>20)invalid();return v.map(o=>{if(!object(o)||!id(o.id)||!text(o.title,512)||!instant(o.start)||!instant(o.end)||!number(o.priority,-1000,1000)||!states.includes(o.state))invalid();const ch=o.channel;if(ch!==undefined&&(!object(ch)||!text(ch.id,64)||!text(ch.name,256)||!text(ch.number,32)))invalid();return {id:o.id,title:o.title,start:o.start,end:o.end,priority:o.priority,state:o.state,...(ch?{channel:parseRecordingChannel(ch)}:{}),...(typeof o.ruleId==='string'?{ruleId:o.ruleId}:{}),...(object(o.episode)?{episode:{season:checkedNumber(o.episode.season??0),number:checkedNumber(o.episode.number??0)}}:{})};});}
function parseRuleSummary(v:unknown):RuleSummary{if(!object(v)||!object(v.channel)||!text(v.channel.id,64)||!text(v.channel.name,256)||!text(v.channel.number,32))invalid();return {upcoming:checkedNumber(v.upcoming),recorded:checkedNumber(v.recorded),conflicts:checkedNumber(v.conflicts),matchingTonight:checkedNumber(v.matchingTonight),...(v.next===undefined?{}:{next:programme(v.next)}),channel:parseRecordingChannel(v.channel)};}
