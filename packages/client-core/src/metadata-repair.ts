import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {routePattern} from "../../contracts/src/routes.ts";
import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {keepReadable} from './tolerant.ts';
/** Owner repairs are current-view CAS commands. A lost response is reconciled, never replayed. */
export type RepairTarget = Readonly<{kind:'item'|'show'|'season'|'album'|'artist'|'book';id:string;libraryId:string}>;
export type RepairCaller = Readonly<{signal:AbortSignal;current:()=>boolean}>;
export type RepairScope = Readonly<{serverId:string;viewerId:string}>;
export type RepairField = Readonly<{value:string;automaticValue:string;locked:boolean;source:string;observedAt?:string;values?:readonly string[]}>;
/** The server publishes the editable field registry; a client never hard-codes one. */
export type RepairFieldSpec = Readonly<{field:string;label:string;group:'general'|'numbers'|'text'|'identity';type:'text'|'multiline'|'integer'|'number'|'date'|'enum'|'list';maxLength?:number;min?:number;max?:number;allowed?:readonly string[];bulk:boolean}>;
export type RepairRelationship = Readonly<{sourceRowId?:string;ownerEntryId?:string;sourceStatus?:'current'|'missing'|'replaced';recordId?:string;provider?:string;department?:string;kind:string;targetKind:string;targetId:string;label:string;role?:string;ordinal:number;source:string;locked:boolean;observedAt?:string}>;
export type RepairChoice = Readonly<{role:string;subject:string;candidateId:string;digest:string;locked:boolean;revision:number;attribution:string;url:string}>;
export type RepairData = Readonly<{
 onlineLookups?:boolean;
 artworkFallbackUrl?:string;
 canMakeEpisodeThumbnail?:boolean;
 target:{kind:RepairTarget['kind'];id:string};libraryId:string;serverId:string;viewerFence:string;revision:string;
 snapshot:{canEditProviderCredits?:true;relationshipLocks:Readonly<Record<string,boolean>>;fields:Readonly<Record<string,RepairField>>;identity?:{engine?:'screen';type?:string;provider:string;id:string;order?:string;revision:number;status:string;locked:boolean};relationships:readonly RepairRelationship[];artwork:readonly RepairChoice[]};
 tabs?:readonly string[];schema:readonly RepairFieldSpec[];artworkRoles:readonly string[];
 candidates:readonly {id:string;identityId?:string;identityType?:string;provider:string;title:string;subtitle:string;observedAt:string;year?:number;previewUrl?:string;orders?:readonly {id:string;name:string}[]}[];
 history:readonly {id:number;trigger:string;observedAt:string}[];
 artwork:{candidates:readonly {id:string;role:string;subject:string;provider:string;imageId:string;locale:string;rank:number;votes?:number|null;attribution:string;observedAt:string;current:boolean;previewUrl?:string}[];jobs:readonly {id:string;role:string;subject:string;status:string;error:string;attempts:number;nextAttempt:string}[]};
 cascades:readonly {id:string;intent:string;status:string;processed:number;failed:number;completed?:number;skipped?:number;pending?:number}[];
}>;
export type RepairCommand = Readonly<{
 effectiveCredits?:true;
 action:'make_episode_thumbnail'|'retry'|'preview_artwork'|'edit'|'identify'|'search'|'undo'|'lock_all'|'unlock_all'|'select_artwork'|'artwork_lock'|'repair_assets'|'discover_artwork'|'cascade'|'cancel_cascade'|'relationship_lock'|'edit_relationships';
 fields?:Record<string,{value?:string;values?:readonly string[];locked?:boolean;useAutomatic?:boolean}>;
 provider?:string;candidateId?:string;order?:string;historyId?:number;confirm?:boolean;
 role?:string;subject?:string;locked?:boolean;intent?:string;operationId?:string;query?:string;year?:number;
 relationships?:readonly RepairRelationship[];
}>;
export type RepairPreview = Readonly<{revision:string;descendants:number;lockedFields:number;selectedImages:number;message:string;merge?:{target:{kind:string;id:string};allowed:boolean;blockers:readonly string[];references:Readonly<Record<string,number>>}}>; 
export type RepairView = Readonly<{phase:'idle'|'loading'|'ready'|'saving'|'conflict'|'uncertain'|'denied'|'error';data:RepairData|null;error:string|null;preview:RepairPreview|null}>;
type Api = {request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>; requestMetadataUpload?<T>(path:string,body:FormData,signal?:AbortSignal):Promise<T>};
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const str=(v:unknown,max=2048):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&v.length>0&&!/[\r\n\t]/.test(v);
const hash=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{64}$/.test(v);
const num=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
const arr=(v:unknown,max:number,check:(v:Record<string,unknown>)=>boolean):boolean=>Array.isArray(v)&&v.length<=max&&v.every(x=>obj(x)&&check(x));
/** The rows of a list that can be read. The list's bound is exact (over it, the answer is refused); a row that cannot be read is left out alone, and a list the server leaves out is empty. */
function readable(part:string,v:unknown,max:number,check:(v:Record<string,unknown>)=>boolean):Record<string,unknown>[]{
 if(v===undefined||v===null)return [];
 if(!Array.isArray(v)||v.length>max)bad();
 return keepReadable('metadata-repair',part,v as unknown[],x=>obj(x)&&check(x)?x:undefined);
}
const when=(v:unknown)=>str(v,64)&&(v===''||Number.isFinite(Date.parse(v)));
const roles=['poster','backdrop','cover','square','thumbnail','banner','logo','still','portrait'];
const fallbackPattern=new RegExp('^'+routePattern('get_metadata_kind_id_art_role').replace('{kind}','show').replace('{id}','[A-Za-z0-9_-]{1,128}').replace('{role}','backdrop')+'$');
function validFallbackUrl(value:unknown):value is string {if(typeof value!=='string'||value.length>512)return false;const parts=value.split('?');return parts.length===2&&fallbackPattern.test(parts[0]??'')&&/^v=[a-f0-9]{64}$/.test(parts[1]??'');}
function bad():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_metadata_repair'});}
/**
 * The owner's editing view of one title. Its target, library, server, viewer fence and revision are
 * exact, and so are the editable fields and their locks: an edit is sent against them. The lists
 * around them (genres and credits, pictures, match candidates, history, progress) are read row by
 * row, so one row this app cannot read never closes the editor.
 */
export function validateRepair(raw:unknown,scope:RepairScope,target:RepairTarget):RepairData {
 if(!obj(raw)||!obj(raw.target)||raw.target.kind!==target.kind||raw.target.id!==target.id||raw.libraryId!==target.libraryId||raw.serverId!==scope.serverId||!hash(raw.viewerFence)||!hash(raw.revision)||!obj(raw.snapshot)||!obj(raw.snapshot.fields)||Object.keys(raw.snapshot.fields).length>32)bad();
 // The registry decides which fields exist. A field the registry does not name
 // is not rendered, and a registry entry with no value is a broken projection.
 const groups=['general','numbers','text','identity'],types=['text','multiline','integer','number','date','enum','list'];
 if(!arr(raw.schema,32,f=>id(f.field)&&str(f.field,64)&&str(f.label,200)&&groups.includes(String(f.group))&&types.includes(String(f.type))&&typeof f.bulk==='boolean'&&(f.maxLength===undefined||num(f.maxLength))&&(f.min===undefined||num(f.min))&&(f.max===undefined||num(f.max))&&(f.allowed===undefined||Array.isArray(f.allowed)&&f.allowed.length<=200&&f.allowed.every(v=>str(v,200)))))bad();
 const specs=new Map((raw.schema as Record<string,unknown>[]).map(f=>[String(f.field),f]));
 if(specs.size!==(raw.schema as unknown[]).length)bad();
 // A place for a picture that this app has no shape for is not offered.
 if(!Array.isArray(raw.artworkRoles)||raw.artworkRoles.length>16)bad();const artworkRoles=[...new Set(raw.artworkRoles.filter(r=>roles.includes(String(r))))] as string[];
 for(const [key,value] of Object.entries(raw.snapshot.fields)){
  const spec=specs.get(key);
  if(!spec||!obj(value)||!str(value.value,65536)||!str(value.automaticValue,65536)||typeof value.locked!=='boolean'||!str(value.source,100)||value.observedAt!==undefined&&!when(value.observedAt))bad();
  if(spec.type==='list'&&value.values!==undefined&&(!Array.isArray(value.values)||value.values.length>64||!value.values.every(v=>typeof v==='string'&&[...v].length<=128&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v))))bad();
  if(spec.type!=='list'&&value.values!==undefined)bad();
 }
 for(const field of specs.keys())if(!(field in (raw.snapshot.fields as Record<string,unknown>)))bad();
 if(!obj(raw.snapshot.relationshipLocks)||Object.keys(raw.snapshot.relationshipLocks).length>3||Object.entries(raw.snapshot.relationshipLocks).some(([k,v])=>!['credit','genre','identity'].includes(k)||typeof v!=='boolean'))bad();
 const ident=raw.snapshot.identity;
 if(ident!==undefined&&(!obj(ident)||!str(ident.provider,40)||!str(ident.id,256)||!num(ident.revision)||!str(ident.status,64)||typeof ident.locked!=='boolean'||ident.order!==undefined&&!str(ident.order,160)||ident.engine!==undefined&&ident.engine!=='screen'||ident.type!==undefined&&!str(ident.type,40)))bad();
 // Genres, credits, pictures, candidates, history and progress are lists of rows: each row is read on its own.
 const relationships=readable('relationships',raw.snapshot.relationships,1024,r=>(r.sourceRowId===undefined||typeof r.sourceRowId==='string'&&/^[0-9a-f]{32}$/.test(r.sourceRowId))&&(r.ownerEntryId===undefined||typeof r.ownerEntryId==='string'&&/^[0-9a-f]{32}$/.test(r.ownerEntryId))&&(r.sourceStatus===undefined||typeof r.sourceStatus==='string'&&['current','missing','replaced'].includes(r.sourceStatus))&&(r.recordId===undefined||str(r.recordId,256))&&(r.provider===undefined||str(r.provider,40))&&(r.department===undefined||str(r.department,200))&&id(r.kind)&&id(r.targetKind)&&str(r.targetId,256)&&str(r.label,2048)&&str(r.source,100)&&typeof r.locked==='boolean'&&num(r.ordinal)&&(r.role===undefined||str(r.role,500))&&(r.observedAt===undefined||when(r.observedAt)));
 const prefix=encodedRoutePath("get_metadata_kind_id", {"kind": encodeURIComponent(target.kind), "id": encodeURIComponent(target.id)})+'/art/';
 const chosenArtwork=readable('snapshot.artwork',raw.snapshot.artwork,200,a=>roles.includes(String(a.role))&&str(a.subject,256)&&hash(a.candidateId)&&hash(a.digest)&&typeof a.locked==='boolean'&&num(a.revision)&&str(a.attribution,2048)&&str(a.url,2048)&&a.url.startsWith(prefix)&&!/[\r\n\\]/.test(a.url));
 const candidates=readable('candidates',raw.candidates,50,c=>id(c.id)&&str(c.provider,40)&&str(c.title,2048)&&str(c.subtitle,2048)&&(c.identityId===undefined||str(c.identityId,256)&&c.identityId.length>0)&&(c.identityType===undefined||typeof c.identityType==='string'&&['movie','show','anime','episode'].includes(c.identityType))&&when(c.observedAt)&&(c.year===undefined||num(c.year)&&c.year<=9999)&&(c.previewUrl===undefined||str(c.previewUrl,2048)&&c.previewUrl.startsWith(prefix)&&!/[\r\n\\]/.test(c.previewUrl))&&(c.orders===undefined||arr(c.orders,64,o=>id(o.id)&&str(o.id,160)&&str(o.name,2048)))),history=readable('history',raw.history,30,h=>num(h.id)&&h.id>0&&str(h.trigger,100)&&when(h.observedAt));
 const art=obj(raw.artwork)?raw.artwork:{};
 const artCandidates=readable('artwork.candidates',art.candidates,200,a=>hash(a.id)&&roles.includes(String(a.role))&&str(a.subject,256)&&str(a.provider,40)&&str(a.imageId,256)&&str(a.locale,30)&&typeof a.rank==='number'&&Number.isFinite(a.rank)&&(a.votes===undefined||a.votes===null||num(a.votes))&&str(a.attribution,2048)&&when(a.observedAt)&&typeof a.current==='boolean'&&(a.previewUrl===undefined||str(a.previewUrl,2048)&&a.previewUrl.startsWith(prefix)&&!/[\r\n\\]/.test(a.previewUrl))),artJobs=readable('artwork.jobs',art.jobs,201,j=>id(j.id)&&str(j.role,40)&&str(j.subject,256)&&str(j.status,64)&&str(j.error,256)&&num(j.attempts)&&when(j.nextAttempt)),cascades=readable('cascades',raw.cascades,10,c=>id(c.id)&&str(c.intent,40)&&str(c.status,40)&&num(c.processed)&&num(c.failed)&&['completed','skipped','pending'].every(k=>c[k]===undefined||num(c[k])));
 // Do not retain extra transport/audit fields in a viewer projection.
 const pick=(value:unknown,keys:readonly string[])=>Object.freeze(Object.fromEntries(keys.filter(k=>(value as Record<string,unknown>)[k]!==undefined).map(k=>[k,(value as Record<string,unknown>)[k]])));
 const rows=(value:unknown,keys:readonly string[])=>Object.freeze((value as unknown[]).map(v=>pick(v,keys)));
 return Object.freeze({target:Object.freeze({kind:target.kind,id:target.id}),libraryId:target.libraryId,serverId:scope.serverId,viewerFence:raw.viewerFence,revision:raw.revision,
 ...(Array.isArray(raw.tabs)?{tabs:Object.freeze(raw.tabs.filter((tab):tab is string=>typeof tab==='string'&&['general','numbers','text','identity'].includes(tab)))}:{}),schema:rows(raw.schema,['field','label','group','type','maxLength','min','max','allowed','bulk']),artworkRoles:Object.freeze(artworkRoles),
 ...(typeof raw.onlineLookups==='boolean'?{onlineLookups:raw.onlineLookups}:{}),
 ...(raw.canMakeEpisodeThumbnail===true?{canMakeEpisodeThumbnail:true}:{}),
 ...(target.kind==='item'&&validFallbackUrl(raw.artworkFallbackUrl)?{artworkFallbackUrl:raw.artworkFallbackUrl}:{}),
 snapshot:Object.freeze({...(raw.snapshot.canEditProviderCredits===true&&Array.isArray(raw.snapshot.relationships)&&relationships.length===raw.snapshot.relationships.length&&relationships.filter(r=>r.kind==='credit').length<=512?{canEditProviderCredits:true as const}:{}),fields:Object.freeze(Object.fromEntries(Object.entries(raw.snapshot.fields).map(([k,v])=>[k,pick(v,['value','automaticValue','locked','source','observedAt','values'])]))),relationshipLocks:Object.freeze({...raw.snapshot.relationshipLocks}),...(ident===undefined?{}:{identity:pick(ident,['engine','type','provider','id','order','revision','status','locked'])}),relationships:rows(relationships,['sourceRowId','ownerEntryId','sourceStatus','recordId','provider','department','kind','targetKind','targetId','label','role','ordinal','source','locked','observedAt']),artwork:rows(chosenArtwork,['role','subject','candidateId','digest','locked','revision','attribution','url'])}),
 candidates:Object.freeze(candidates.map(c=>Object.freeze({...pick(c,['id','identityId','identityType','provider','title','subtitle','observedAt','year','previewUrl']),...(c.orders===undefined?{}:{orders:rows(c.orders,['id','name'])})}))),history:rows(history,['id','trigger','observedAt']),
 artwork:Object.freeze({candidates:rows(artCandidates,['id','role','subject','provider','imageId','locale','rank','votes','attribution','observedAt','current','previewUrl']),jobs:rows(artJobs,['id','role','subject','status','error','attempts','nextAttempt'])}),cascades:rows(cascades,['id','intent','status','processed','failed','completed','skipped','pending'])}) as unknown as RepairData;
}
export class MetadataRepairService {
 private state:RepairView=Object.freeze({phase:'idle',data:null,error:null,preview:null});
 private listeners=new Set<()=>void>();private epoch=0;private controller?:AbortController;private fence?:string;private target?:RepairTarget;private disposed=false;
 private options:{api:Api;scope:RepairScope;timeoutMs?:number};
 constructor(options:{api:Api;scope:RepairScope;timeoutMs?:number}){if(!id(options.scope.serverId)||!options.scope.viewerId)throw Error('A concrete viewer scope is required.');if(options.timeoutMs!==undefined&&(!num(options.timeoutMs)||options.timeoutMs<1||options.timeoutMs>30000))throw Error('Invalid deadline.');this.options={...options,scope:Object.freeze({...options.scope})};}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(state:RepairView){if(this.disposed)return;this.state=Object.freeze(state);for(const fn of this.listeners)fn();}
 cancel(){this.epoch++;this.controller?.abort();this.target=undefined;this.fence=undefined;this.publish({phase:'idle',data:null,error:null,preview:null});}
 dispose(){this.cancel();this.disposed=true;this.listeners.clear();}
 private async request(target:RepairTarget,method:string,body?:unknown,suffix='',upload=false,explicitPath?:string,caller?:RepairCaller):Promise<unknown>{
  if(caller&&(caller.signal.aborted||!caller.current()))throw Error('Repair caller retired.');
  const c=new AbortController();this.controller?.abort();this.controller=c;
  let rejectAbort!:(reason:Error)=>void;const cancelled=new Promise<never>((_,reject)=>{rejectAbort=reject;});
  void cancelled.catch(()=>{});
  const abort=()=>rejectAbort(new Error('Repair request interrupted.'));c.signal.addEventListener('abort',abort,{once:true});
  const retire=()=>c.abort();caller?.signal.addEventListener('abort',retire,{once:true});
  if(caller&&(caller.signal.aborted||!caller.current()))c.abort();
  const timer=setTimeout(()=>c.abort(),this.options.timeoutMs??15000);
  try{if(c.signal.aborted)throw Error('Repair caller retired.');return await Promise.race([upload ? this.options.api.requestMetadataUpload!(suffix,body as FormData,c.signal) : this.options.api.request<RouteResponse<"get_metadata_kind_id"> | RouteResponse<"get_metadata_kind_id_preview">>(explicitPath??(suffix.startsWith('/preview')?encodedRoutePath('get_metadata_kind_id_preview',{kind:encodeURIComponent(target.kind),id:encodeURIComponent(target.id)})+suffix.slice(8):encodedRoutePath(method==='GET'?'get_metadata_kind_id':'post_metadata_kind_id',{kind:encodeURIComponent(target.kind),id:encodeURIComponent(target.id)})+suffix),method,body,c.signal),cancelled]);}
  finally{clearTimeout(timer);c.signal.removeEventListener('abort',abort);caller?.signal.removeEventListener('abort',retire);}
 }
 async load(target:RepairTarget){
  if(this.disposed)throw Error('Repair view is closed.');if(!id(target.id)||!id(target.libraryId)||!['item','show','season','album','artist','book'].includes(target.kind))throw Error('Select a concrete entity.');
  if(JSON.stringify(target)!==JSON.stringify(this.target))this.fence=undefined;
  this.target=Object.freeze({...target});const epoch=++this.epoch;this.publish({phase:'loading',data:null,error:null,preview:null});
  try{const raw=await this.request(target,'GET');if(epoch!==this.epoch||this.disposed)return;this.accept(raw,target);}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,false);}
 }
 async refresh(expectedData:RepairData):Promise<void>{
  const target=this.target;if(this.disposed||!target||this.state.phase!=='ready'||this.state.data!==expectedData)return;
  const epoch=++this.epoch;
  try{const raw=await this.request(target,'GET');if(epoch!==this.epoch||this.disposed||this.target!==target||this.state.data!==expectedData)return;this.accept(raw,target);}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,false);}
 }
 private accept(raw:unknown,target:RepairTarget){const data=validateRepair(raw,this.options.scope,target);if(this.fence&&this.fence!==data.viewerFence)throw Object.assign(new Error('Owner access changed.'),{status:403});this.fence=data.viewerFence;this.publish({phase:'ready',data,error:null,preview:null});}
 async command(command:RepairCommand,expectedData?:RepairData,caller?:RepairCaller):Promise<boolean>{
  const callerCurrent=()=>!caller||!caller.signal.aborted&&caller.current();
  if(!callerCurrent())return false;
  const data=this.state.data,target=this.target;if(this.state.phase!=='ready'||!data||!target||expectedData!==undefined&&expectedData!==data)throw Error('Load current metadata before making another change.');
  if(command.action==='make_episode_thumbnail'&&(target.kind!=='item'||data.canMakeEpisodeThumbnail!==true))throw Error('Video thumbnail generation is unavailable.');
  const epoch=++this.epoch;this.publish({...this.state,phase:'saving',error:null});
  const attempt=this.getSnapshot();
  if(this.disposed||epoch!==this.epoch||this.target!==target||attempt.phase!=='saving'||attempt.data!==data)return false;
  if(!callerCurrent()){this.failure(Error('Repair caller retired before submission.'),false);return false;}
  try{const raw=await this.request(target,'POST',{...command,expectedRevision:data.revision},'',false,undefined,caller);if(epoch!==this.epoch||this.disposed)return false;if(!callerCurrent()){this.failure(Error('Repair caller retired after submission.'),true);return false;}this.accept(raw,target);return true;}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,true);return false;}
 }
 async uploadArtwork(role:string,file:Blob,subject=''):Promise<boolean>{
  const data=this.state.data,target=this.target;
  if(this.state.phase!=='ready'||!data||!target||!this.options.api.requestMetadataUpload)throw Error('Load current metadata before uploading artwork.');
  if(!data.artworkRoles.includes(role)||!str(subject,256)||file.size<1||file.size>10*1024*1024||!['image/jpeg','image/png'].includes(file.type))throw Error('Choose a JPG or PNG up to 10 MiB.');
  const body=new FormData();body.append('file',file,'artwork');body.append('subject',subject);body.append('expectedRevision',data.revision);
  const path=encodedRoutePath('post_metadata_kind_id_art_role_upload',{kind:encodeURIComponent(target.kind),id:encodeURIComponent(target.id),role:encodeURIComponent(role)});
  const epoch=++this.epoch;this.publish({...this.state,phase:'saving',error:null});
  // Subscribers may synchronously retire this attempt while receiving the saving state.
  const attempt=this.getSnapshot();
  if(this.disposed||epoch!==this.epoch||this.target!==target||attempt.phase!=='saving'||attempt.data!==data)return false;
  try{const raw=await this.request(target,'POST',body,path,true);if(epoch!==this.epoch||this.disposed)return false;this.accept(raw,target);return true;}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,true);return false;}
 }
 async removeUploadedArtwork(role:string,candidateId:string,expectedData?:RepairData):Promise<boolean>{
  const data=this.state.data,target=this.target;
  if(this.state.phase!=='ready'||!data||!target)throw Error('Load current metadata before removing artwork.');
  if(expectedData&&data!==expectedData)throw Error('The artwork confirmation belongs to an old document.');
  if(!data.artwork.candidates.some(candidate=>candidate.id===candidateId&&candidate.role===role&&candidate.provider==='upload'))throw Error('Select an uploaded image.');
  const path=encodedRoutePath('delete_metadata_kind_id_art_role_upload_candidateId',{kind:encodeURIComponent(target.kind),id:encodeURIComponent(target.id),role:encodeURIComponent(role),candidateId:encodeURIComponent(candidateId)})+'?expectedRevision='+encodeURIComponent(data.revision);
  const epoch=++this.epoch;this.publish({...this.state,phase:'saving',error:null});
  const attempt=this.getSnapshot();
  if(this.disposed||epoch!==this.epoch||this.target!==target||attempt.phase!=='saving'||attempt.data!==data)return false;
  try{const raw=await this.request(target,'DELETE',undefined,'',false,path);if(epoch!==this.epoch||this.disposed)return false;this.accept(raw,target);return true;}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,true);return false;}
 }
 async preview(mergeId?:string):Promise<void>{
  const target=this.target,data=this.state.data;if(!target||!data||this.state.phase!=='ready')return;
  const epoch=++this.epoch;this.publish({...this.state,phase:'loading',error:null});
  try{const raw=await this.request(target,'GET',undefined,'/preview'+(mergeId?'?mergeId='+encodeURIComponent(mergeId):''));if(epoch!==this.epoch||this.disposed)return;
   if(!obj(raw)||raw.revision!==data.revision)throw Object.assign(new Error('Metadata changed.'),{status:409});
   if(!num(raw.descendants)||!num(raw.lockedFields)||!num(raw.selectedImages)||!str(raw.message,4096))bad();
   if(raw.merge!==undefined){const m=raw.merge;if(!obj(m)||!obj(m.target)||m.target.id!==mergeId||m.target.kind!==target.kind||typeof m.allowed!=='boolean'||!Array.isArray(m.blockers)||m.blockers.length>100||!m.blockers.every(s=>str(s,4096))||!obj(m.references)||Object.keys(m.references).length>1000||!Object.values(m.references).every(num))bad();}
   this.publish({phase:'ready',data,error:null,preview:raw as unknown as RepairPreview});
  }catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,false);}
 }
 private failure(error:unknown,write:boolean){const e=obj(error)?error:{};
  // A refused upload changed nothing: the editor stays ready with the reason.
  const refused=write&&e.status===422&&e.code==='artwork_cap_unreachable'?'This image is too detailed to store at full quality within the size limit. Try a simpler or smaller image.':write&&e.status===415&&e.code==='unsupported_artwork'?'Upload a JPEG, PNG or WebP image no larger than 10 MiB.':null;
  if(refused&&this.state.data){this.publish({...this.state,phase:'ready',preview:null,error:refused});return;}
  const denied=e.status===401||e.status===403;const conflict=e.status===409||e.code==='metadata_conflict';
  this.publish({phase:denied?'denied':conflict?'conflict':write?'uncertain':'error',data:denied?null:this.state.data,preview:null,error:denied?'Owner access is no longer available. Close this view and sign in again.':conflict?'Metadata changed. Nothing was replayed. Reload the current values and review your decision.':write?'The result is uncertain. Reload saved values before making another change.':'Could not read metadata repair state. Reload to try again.'});
 }
}
