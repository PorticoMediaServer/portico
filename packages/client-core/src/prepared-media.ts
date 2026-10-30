import {routePrefix} from "../../contracts/src/routes.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import type {ContentScope, LibraryContentApi} from './library-content.ts';
import {firstOfEach, keepReadable} from './tolerant.ts';
import {wireDurations} from './wire-units.ts';

/** A derivative is neither a catalog edition nor an original source-version ID. */
export type PreparedChoice = Readonly<{versionId: string; expectedRevision: number; offersRevision: string}>;
export type PreparedFacts = Readonly<{container: string; videoCodec: string; audioCodec: string; width: number; height: number; duration: number; audioTracks: number}>;
export type PreparedVersion = Readonly<{
  id: string; itemId: string; sourceId: string; profileId: string; targetId: string;
  sourceRevision: string; partIndex: number; editionId: string|null; digest: string;
  size: number; facts: PreparedFacts; state: 'published'|'deleting'|'deleted';
  revision: number; createdMs: number; selectable: boolean; reason: string;
}>;
export type OptimizationProfile = Readonly<{id: string; name: string; kind: 'video'|'audio'; width: number; height: number; videoKbps: number; audioKbps: number; description: string}>;
export type OptimizationSource = Readonly<{id: string; revision: string; partIndex: number; container: string; height: number; available: boolean; reason: string}>;
export type OptimizationJob = Readonly<{id: string; itemId: string; sourceId: string; profileId: string; targetId: string; state: 'queued'|'running'|'cancelling'|'cancelled'|'failed'|'succeeded'; phase: string; generation: number; bytes: number; errorCode: string; revision: number; createdMs: number; updatedMs: number; versionId: string}>;
export type PreparedView = Readonly<{
  itemId: string; canManage: boolean; preparedOffersRevision: string; configured: boolean; configurationReason: string;
  profiles: readonly OptimizationProfile[]; targets: readonly Readonly<{id: string; name: string}>[];
  sources: readonly OptimizationSource[]; jobs: readonly OptimizationJob[]; versions: readonly PreparedVersion[]; pollAfterMs: number;
}>;
export type PreparedSnapshot = Readonly<{data: PreparedView|null; loading: boolean; busy: boolean; uncertain: boolean; error: string|null}>;
const object = (v: unknown): v is Record<string, unknown> => !!v && typeof v === 'object' && !Array.isArray(v);
const id = (v: unknown): v is string => typeof v === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(v);
const text = (v: unknown, max=1024): v is string => typeof v === 'string' && v.length <= max && !/[\x00-\x1f\x7f]/.test(v);
const integer = (v: unknown): v is number => Number.isSafeInteger(v) && Number(v) >= 0;
const digest = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{64}$/.test(v);
function invalid(): never { throw new Error(unreadableServerResponse); }
function array(v: unknown, max: number): unknown[] { if (!Array.isArray(v) || v.length > max) invalid(); return v; }
/** Reads a bounded list row by row: a row that cannot be read (a kind, state or codec this app does not know, a digest or revision that is not one) or that repeats an id is left out, and the rest are kept. */
function rows<T extends {id: string}>(part: string, values: unknown[], read: (v: unknown)=>T): readonly T[] { return Object.freeze(firstOfEach('prepared-versions', part, keepReadable('prepared-versions', part, values, v=>read(v)), v=>v.id)); }
export function parsePreparedChoice(v: unknown): PreparedChoice {
  if (!object(v) || Object.keys(v).some(k=>!['versionId','expectedRevision','offersRevision'].includes(k)) || !id(v.versionId) || !integer(v.expectedRevision) || v.expectedRevision<1 || !digest(v.offersRevision)) invalid();
  return Object.freeze({versionId:v.versionId, expectedRevision:v.expectedRevision, offersRevision:v.offersRevision});
}
export function parsePreparedVersion(v: unknown, itemId: string): PreparedVersion {
  if (!object(v) || !id(v.id) || v.itemId!==itemId || !id(v.sourceId) || !id(v.profileId) || !id(v.targetId) || !digest(v.sourceRevision) || !integer(v.partIndex) || v.editionId!==null&&!id(v.editionId) || !digest(v.digest) || !integer(v.size) || v.size<1 || !integer(v.revision) || v.revision<1 || !integer(v.createdMs) || !['published','deleting','deleted'].includes(String(v.state)) || typeof v.selectable!=='boolean' || !text(v.reason,128)) invalid();
  if (v.selectable ? v.state!=='published'||v.reason!=='' : !v.reason) invalid();
  const f=wireDurations(v.facts,{duration:'durationMs'});
  if (!object(f) || f.container!=='mp4' || !['','h264'].includes(String(f.videoCodec)) || !['','aac'].includes(String(f.audioCodec)) || !integer(f.width) || !integer(f.height) || !integer(f.audioTracks) || f.audioTracks>16 || typeof f.duration!=='number' || !Number.isFinite(f.duration) || f.duration<=0) invalid();
  if (f.videoCodec==='h264' ? f.width<2||f.height<2||f.width>1920||f.height>1080 : f.width!==0||f.height!==0) invalid();
  if ((f.audioCodec==='aac') !== (f.audioTracks>0) || !f.videoCodec&&!f.audioCodec) invalid();
  return Object.freeze({...v, facts:Object.freeze({...f})}) as PreparedVersion;
}
function job(v: unknown, itemId: string): OptimizationJob {
  if (!object(v) || !id(v.id) || v.itemId!==itemId || !id(v.sourceId) || !id(v.profileId) || !id(v.targetId) || !['queued','running','cancelling','cancelled','failed','succeeded'].includes(String(v.state)) || !text(v.phase,128) || !text(v.errorCode,128) || !integer(v.generation) || !integer(v.bytes) || !integer(v.revision) || v.revision<1 || !integer(v.createdMs) || !integer(v.updatedMs) || v.versionId!==''&&!id(v.versionId)) invalid();
  return Object.freeze({...v}) as OptimizationJob;
}
export function parsePreparedView(raw: unknown, itemId: string): PreparedView {
  if (!object(raw) || raw.itemId!==itemId || typeof raw.canManage!=='boolean' || !digest(raw.preparedOffersRevision) || typeof raw.configured!=='boolean' || !text(raw.configurationReason,256) || !integer(raw.pollAfterMs) || raw.pollAfterMs<1000 || raw.pollAfterMs>60000) invalid();
  const profiles=rows('profiles',array(raw.profiles,16),v=>{
    if (!object(v)||!id(v.id)||!text(v.name,128)||!['audio','video'].includes(String(v.kind))||!integer(v.width)||!integer(v.height)||!integer(v.videoKbps)||!integer(v.audioKbps)||!text(v.description,1024)) invalid();
    return Object.freeze({...v}) as OptimizationProfile;
  });
  const targets=rows('targets',array(raw.targets,16),v=>{ if(!object(v)||!id(v.id)||!text(v.name,128)) invalid();return Object.freeze({id:v.id,name:v.name}); });
  const sources=rows('sources',array(raw.sources,32),v=>{
    if(!object(v)||!id(v.id)||(v.available?!digest(v.revision):v.revision!==''&&!digest(v.revision))||!integer(v.partIndex)||!text(v.container,128)||!integer(v.height)||typeof v.available!=='boolean'||!text(v.reason,128)) invalid();
    return Object.freeze({...v}) as OptimizationSource;
  });
  const sentJobs=array(raw.jobs,64);
  const jobs=rows('jobs',sentJobs,v=>job(v,itemId));
  const versions=rows('versions',array(raw.versions,128),v=>parsePreparedVersion(v,itemId));
  // Judged on what the server sent, so an unreadable job never makes a viewer's answer look permitted.
  if(!raw.canManage&&sentJobs.length) invalid();
  return Object.freeze({...raw,profiles,targets,sources,jobs,versions}) as PreparedView;
}
export function preparedChoice(view: Pick<PreparedView,'preparedOffersRevision'>, version: PreparedVersion): PreparedChoice {
  if (!version.selectable) throw new Error('This prepared version is not currently available. Refresh the list.');
  return parsePreparedChoice({versionId:version.id,expectedRevision:version.revision,offersRevision:view.preparedOffersRevision});
}
export function preparedReason(reason: string): string {
  const reasons:Record<string,string>={optimization_failed:'The server could not prepare this copy. Retry, or check Logs & diagnostics for the encoder error.',output_limit:'This copy exceeded the configured output limit.',waiting_for_storage:'Waiting for the source storage to become available.',source_or_profile_unsupported:'This source is not supported by the selected profile.',source_changed:'The original source identity changed. Prepare a new version.', source_unavailable:'Reconnect the original source before preparing it.', part_mapping_unavailable:'This source belongs to an unresolved multipart entry; playback selection is unavailable.', pending_deletion:'Removal pending. Existing playback can finish.', encoder_unavailable:'The server encoder is not configured.', encoder_not_configured:'The server encoder is not configured.', decoder_confinement_unavailable:'A supported decoder sandbox is not configured on this server.', sandbox_unavailable:'A supported decoder sandbox is not configured on this server.', unsupported:'This source is not supported by this profile.', output_invalid:'The completed output did not pass validation.', permission_changed:'The owner’s access changed. Sign in and retry.', cancelled:'Cancelled.', interrupted:'Interrupted; the server will restart this attempt.'};
  return reasons[reason] ?? (reason ? reason.replace(/_/g,' ') : '');
}
export const preparedBytes = (n: number): string => n < 1048576 ? `${Math.round(n/1024)} KiB` : n < 1073741824 ? `${(n/1048576).toFixed(1)} MiB` : `${(n/1073741824).toFixed(2)} GiB`;

/** Item/viewer scoped management. Jobs live on the server; disposing the view
 * never cancels a job. A lost response retries the same immutable request. */
export class PreparedMediaService {
  private state:PreparedSnapshot={data:null,loading:false,busy:false,uncertain:false,error:null};
  private listeners=new Set<()=>void>();
  private disposed=false;
  private serial=0;
  private mutationSerial=0;
  private read?:AbortController;
  private mutation?:AbortController;
  private timer?:ReturnType<typeof setTimeout>;
  private pending?:{path:string;body:unknown};
  private api:LibraryContentApi; readonly scope:ContentScope; readonly itemId:string;
  private requestId:()=>string;
  constructor(options:{api:LibraryContentApi;scope:ContentScope;itemId:string;requestId:()=>string}) {
    if (!id(options.itemId)||!id(options.scope.serverId)||!text(options.scope.viewerId,4096)||!options.scope.viewerId) throw new Error('Invalid prepared-media scope.');
    this.api=options.api;this.scope=Object.freeze({...options.scope});this.itemId=options.itemId;this.requestId=options.requestId;
  }
  // Effect reconnects (including React StrictMode) may reuse the scoped instance.
  connect(){this.disposed=false;this.update({busy:false,loading:false,uncertain:!!this.pending});void this.refresh();}
  subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
  getSnapshot=()=>this.state;
  private update(change:Partial<PreparedSnapshot>) { if(this.disposed)return;this.state=Object.freeze({...this.state,...change});this.listeners.forEach(fn=>fn()); }
  private schedule() {
    clearTimeout(this.timer);if(this.disposed)return;
    const v=this.state.data,active=v?.jobs.some(j=>['queued','running','cancelling'].includes(j.state))||v?.versions.some(v=>v.state==='deleting');
    if(active){this.timer=setTimeout(()=>{void this.refresh();},v?.pollAfterMs??3000);(this.timer as any)?.unref?.();}
  }
  async refresh():Promise<void> {
    if(this.disposed)return;clearTimeout(this.timer);this.read?.abort();const serial=++this.serial,read=this.read=new AbortController();
    const timeout=setTimeout(()=>read.abort(),12000);this.update({loading:true});
    try{
      const raw=await this.api.request(encodedRoutePath("get_items_id_prepared_versions", {"id": this.itemId}),'GET',undefined,read.signal);
      const data=parsePreparedView(raw,this.itemId);if(serial!==this.serial||this.disposed)return;
      this.update({data,error:this.state.uncertain?this.state.error:null});
    }catch(e){if(serial!==this.serial||this.disposed)return;const status=(e as {status?:number})?.status;this.update({...(status===401||status===403?{data:null}:{}),error:'Prepared versions could not be refreshed. Check the connection and retry.'});}
    finally{clearTimeout(timeout);if(serial===this.serial&&!this.disposed){this.update({loading:false});this.schedule();}}
  }
  submit(source:OptimizationSource,profile:OptimizationProfile,targetId:string):Promise<void> {
    const v=this.state.data;if(!v?.canManage||!v.configured||!v.sources.some(s=>s.id===source.id&&s.revision===source.revision&&s.available)||!v.profiles.some(p=>p.id===profile.id)||!v.targets.some(t=>t.id===targetId))return this.reject('Refresh the source, profile and target before optimizing.');
    return this.begin(encodedRoutePath("post_items_id_optimizations", {"id": this.itemId}),{itemId:this.itemId,sourceId:source.id,expectedSourceRevision:source.revision,profileId:profile.id,targetId});
  }
  command(job:OptimizationJob,action:'cancel'|'retry'):Promise<void> {
    if(!this.state.data?.canManage||!this.state.data.jobs.some(j=>j.id===job.id&&j.revision===job.revision))return this.reject('Refresh the job before changing it.');
    return this.begin(encodedRoutePath(action==='cancel'?'post_optimization_jobs_id_cancel':'post_optimization_jobs_id_retry',{id:job.id}),{expectedRevision:job.revision});
  }
  remove(version:PreparedVersion):Promise<void> {
    if(!this.state.data?.canManage||!this.state.data.versions.some(v=>v.id===version.id&&v.revision===version.revision))return this.reject('Refresh the version before removing it.');
    return this.begin(encodedRoutePath("post_prepared_versions_id_delete", {"id": version.id}),{expectedRevision:version.revision});
  }
  private reject(message:string):Promise<void>{this.update({error:message});return Promise.resolve();}
  private begin(path:string,body:Record<string,unknown>):Promise<void> {
    if(this.disposed)return Promise.resolve();if(this.state.busy||this.pending)return this.reject('Confirm the earlier request before starting another one.');
    try{const key=this.requestId();if(!id(key))throw new Error();this.pending={path,body:Object.freeze({...body,idempotencyKey:key})};}catch{return this.reject('A request identity could not be allocated. Retry after reconnecting.');}
    return this.retryPending();
  }
  async retryPending():Promise<void> {
    if(this.disposed||this.state.busy||!this.pending)return;const request=this.pending,serial=++this.mutationSerial,abort=this.mutation=new AbortController();const timeout=setTimeout(()=>abort.abort(),20000);
    // An earlier read may ignore AbortSignal and finish after this mutation.
    // Fence it before posting; it must not resurrect removed or unauthorized data.
    this.read?.abort();++this.serial;clearTimeout(this.timer);
    this.update({busy:true,loading:false,uncertain:false,error:null});
    let rejected:string|null=null;let denied=false;
    try{
      await this.api.request(request.path,'POST',request.body,abort.signal);
      if(this.disposed||serial!==this.mutationSerial)return;this.pending=undefined;this.update({uncertain:false,error:null});
    }catch(e){
      if(this.disposed||serial!==this.mutationSerial)return;
      const status=(e as {status?:number})?.status;
      const definitive=typeof status==='number'&&status>=400&&status<500&&![408,425,429].includes(status);
      if(definitive)this.pending=undefined;
      denied=status===401||status===403;
      if(denied){this.read?.abort();++this.serial;clearTimeout(this.timer);}
      this.update({uncertain:!definitive,...(denied?{data:null,loading:false}:{}),error:definitive?(status===409?'The source or job changed. Refresh and choose again.':status===403?'Only the current server owner can manage prepared versions.':status===422?'The source, profile or server encoder configuration is unsupported.':'The optimization request was rejected. Refresh before trying again.'):'The request outcome is not confirmed. Retry the same request; this will not create a duplicate.'});
      if(definitive)rejected=this.state.error;
    }finally{
      clearTimeout(timeout);if(!this.disposed&&serial===this.mutationSerial){this.update({busy:false});if(!this.state.uncertain&&!denied){await this.refresh();if(rejected)this.update({error:rejected});}else if(!denied)this.schedule();}
    }
  }
  dispose(){this.disposed=true;++this.serial;++this.mutationSerial;clearTimeout(this.timer);this.read?.abort();this.mutation?.abort();this.listeners.clear();}
}
