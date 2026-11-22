import type {RouteResponse} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {callServer, serverPath} from './query/server-api.ts';
import type {ArtworkPicture, MetadataCredit, MetadataDocument, MetadataHistoryEntry, MetadataPatch} from '../../contracts/src/next/api.ts';
/** Owner repairs are current-view CAS commands. A lost response is reconciled, never replayed. */
export type RepairTarget = Readonly<{kind:'item'|'show'|'season'|'album'|'artist'|'book';id:string;libraryId:string}>;
export type RepairCaller = Readonly<{signal:AbortSignal;current:()=>boolean}>;
export type RepairScope = Readonly<{serverId:string;viewerId:string}>;
export type RepairField = Readonly<{value:string;automaticValue:string;locked:boolean;source:string;observedAt?:string;values?:readonly string[]}>;
/** The server publishes the editable field registry; a client never hard-codes one. */
export type RepairFieldSpec = Readonly<{field:string;label:string;group:'general'|'numbers'|'text'|'identity';type:'text'|'multiline'|'integer'|'number'|'date'|'enum'|'list';maxLength?:number;min?:number;max?:number;allowed?:readonly string[];bulk:boolean;/** OW-3: can't be cleared (an episode's season and number). */required?:boolean}>;
export type RepairRelationship = Readonly<{sourceRowId?:string;ownerEntryId?:string;sourceStatus?:'current'|'missing'|'replaced';recordId?:string;provider?:string;department?:string;kind:string;targetKind:string;targetId:string;label:string;role?:string;ordinal:number;source:string;locked:boolean;observedAt?:string}>;
export type RepairChoice = Readonly<{role:string;subject:string;candidateId:string;digest:string;locked:boolean;revision:number;attribution:string;url:string}>;
export type RepairData = Readonly<{
 onlineLookups?:boolean;
 /** The rebuilt server's kind of title (movie, show, episode, album…). */
 itemKind?:string;
 artworkFallbackUrl?:string;
 canMakeEpisodeThumbnail?:boolean;
 target:{kind:RepairTarget['kind'];id:string};libraryId:string;serverId:string;viewerFence:string;revision:string;
 snapshot:{canEditProviderCredits?:true;relationshipLocks:Readonly<Record<string,boolean>>;fields:Readonly<Record<string,RepairField>>;identity?:{engine?:'screen';type?:string;provider:string;id:string;order?:string;offset?:number;revision:number;status:string;locked:boolean};relationships:readonly RepairRelationship[];artwork:readonly RepairChoice[]};
 tabs?:readonly string[];schema:readonly RepairFieldSpec[];artworkRoles:readonly string[];
 candidates:readonly {id:string;identityId?:string;identityType?:string;provider:string;title:string;subtitle:string;observedAt:string;year?:number;previewUrl?:string;orders?:readonly {id:string;name:string}[]}[];
 history:readonly {id:number|string;trigger:string;observedAt:string}[];
 artwork:{candidates:readonly {id:string;role:string;subject:string;provider:string;imageId:string;locale:string;rank:number;votes?:number|null;attribution:string;observedAt:string;current:boolean;previewUrl?:string}[];jobs:readonly {id:string;role:string;subject:string;status:string;error:string;attempts:number;nextAttempt:string}[]};
 cascades:readonly {id:string;intent:string;status:string;processed:number;failed:number;completed?:number;skipped?:number;pending?:number}[];
}>;
export type RepairCommand = Readonly<{
 effectiveCredits?:true;
 action:'keep_as_is'|'make_episode_thumbnail'|'refetch_artwork'|'retry'|'preview_artwork'|'edit'|'identify'|'search'|'undo'|'lock_all'|'unlock_all'|'select_artwork'|'artwork_lock'|'repair_assets'|'discover_artwork'|'cascade'|'cancel_cascade'|'relationship_lock'|'edit_relationships';
 fields?:Record<string,{value?:string;values?:readonly string[];locked?:boolean;useAutomatic?:boolean}>;
 provider?:string;candidateId?:string;order?:string;episodeOffset?:number;historyId?:number|string;confirm?:boolean;
 role?:string;subject?:string;locked?:boolean;intent?:string;operationId?:string;query?:string;year?:number;
 relationships?:readonly RepairRelationship[];
}>;
export type RepairPreview = Readonly<{revision:string;descendants:number;lockedFields:number;selectedImages:number;message:string;merge?:{target:{kind:string;id:string};allowed:boolean;blockers:readonly string[];references:Readonly<Record<string,number>>}}>; 
/** OW-4: what went wrong, as a code and status the shared presenter maps to its own sentence. Never a sentence: the
 * editor used to hand English text to the presenter as a code and showed "Something went wrong" instead. */
export type RepairError = Readonly<{code:string;status?:number}>;
export type RepairView = Readonly<{phase:'idle'|'loading'|'ready'|'saving'|'conflict'|'uncertain'|'denied'|'error';data:RepairData|null;error:RepairError|null;preview:RepairPreview|null}>;
type Api = {request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>; requestMetadataUpload?<T>(path:string,body:Blob,signal?:AbortSignal):Promise<T>;
 requestRaw?(path:string,method:string,options:Readonly<{headers?:Readonly<Record<string,string>>;body?:unknown;signal?:AbortSignal}>):Promise<{status:number;body?:unknown}>};
const obj=(v:unknown):v is Record<string,unknown>=>!!v&&typeof v==='object'&&!Array.isArray(v);
const str=(v:unknown,max=2048):v is string=>typeof v==='string'&&v.length<=max&&!/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(v);
const id=(v:unknown):v is string=>str(v,256)&&v.length>0&&!/[\r\n\t]/.test(v);
const num=(v:unknown):v is number=>typeof v==='number'&&Number.isSafeInteger(v)&&v>=0;
/** The rows of a list that can be read. The list's bound is exact (over it, the answer is refused); a row that cannot be read is left out alone, and a list the server leaves out is empty. */
const when=(v:unknown)=>str(v,64)&&(v===''||Number.isFinite(Date.parse(v)));
const roles=['poster','backdrop','cover','square','thumbnail','banner','logo','still','portrait'];
function bad():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_metadata_repair'});}
/*
 * The editor on the rebuilt server (definition area 4, rules 16 to 32): one title's document
 * (GET/PATCH /v1/admin/items/{id}/metadata, the edit naming the revision it was made from), its
 * pictures per role (…/artwork), its History and Undo (…/metadata/history), and its match
 * (…/matches, PUT …/match, POST …/metadata/refresh). The editor screens keep reading RepairData;
 * this builds it from those answers.
 */
/** The pictures each kind of title has, in the editor's words (the server says `thumb`). */
const ROLES_BY_KIND:Readonly<Record<string,readonly string[]>>={movie:['poster','backdrop','logo'],show:['poster','backdrop','logo','banner'],season:['poster'],episode:['thumbnail'],
 collection:['poster','backdrop'],album:['cover'],artist:['portrait'],audiobook:['cover'],book:['cover'],comic:['cover'],issue:['cover'],series:['cover']};
const wireRole=(role:string)=>role==='thumbnail'?'thumb':role;
/** The limits of rule 20; the labels the editor already has. */
const short={group:'general' as const,type:'text' as const,maxLength:300},list={group:'general' as const,type:'list' as const,max:64,maxLength:128};
const FIELD_SPECS:Readonly<Record<string,Omit<RepairFieldSpec,'field'|'bulk'>>>={
 title:{label:'web.metadata.field.title',...short},sortTitle:{label:'web.metadata.field.sortTitle',...short},originalTitle:{label:'web.metadata.field.originalTitle',...short},
 metadataLanguage:{label:'web.libraries.metadataLanguage',group:'general',type:'enum'},
 edition:{label:'web.metadata.field.edition',...short},tagline:{label:'web.metadata.field.tagline',...short},contentRating:{label:'web.metadata.field.contentRating',...short},
 summary:{label:'web.metadata.field.description',group:'text',type:'multiline',maxLength:20000},year:{label:'web.metadata.field.year',group:'numbers',type:'integer',min:0,max:9999},
 // A volume's or issue's number in its series (books, audiobooks, comics and issues); 0 removes it.
 number:{label:'web.metadata.field.seriesNumber',group:'numbers',type:'integer',min:0,max:99999},
 releaseDate:{label:'web.metadata.field.releaseDate',group:'numbers',type:'date'},publicationType:{label:'web.metadata.field.publicationType',group:'general',type:'enum',allowed:['','book','magazine','newspaper']},
 genres:{label:'web.metadata.genres',...list},studios:{label:'web.metadata.field.studio',...list},networks:{label:'web.metadata.field.network',...list},
 countries:{label:'web.metadata.field.country',...list},labels:{label:'web.metadata.field.tags',...list},
};
/** History's kinds in the words the editor has for them. */
const TRIGGERS:Readonly<Record<string,string>>={fields_edited:'edit',artwork_chosen:'select_artwork',match_changed:'identify',undone:'undo'};
const PROVIDERS=['tmdb','tvdb','anilist','musicbrainz'];
type Pictures=Readonly<Record<string,readonly ArtworkPicture[]>>;
export function rebuiltRepair(doc:MetadataDocument,pictures:Pictures,history:readonly MetadataHistoryEntry[],candidates:RepairData['candidates'],scope:RepairScope,target:RepairTarget):RepairData{
 const ids=doc.match?.ids??{},provider=PROVIDERS.find(name=>ids[name])??'';
 const source=(value:string)=>value==='edit'?'manual':value==='file'||value==='library'?'scanner':provider||'automatic';
 const schema:RepairFieldSpec[]=[],fields:Record<string,RepairField>={};
 for(const [name,field] of Object.entries(doc.fields??{})){const spec=FIELD_SPECS[name];if(!spec)continue;schema.push({field:name,bulk:false,...spec});
  // The provider's value beside an edited, locked or file-given field (rule 23); "Use it" takes it now.
  const provided=field.providerValue;
  fields[name]={value:field.value===null||field.value===undefined||field.value===0&&(name==='year'||name==='number')?'':String(field.value),automaticValue:provided===null||provided===undefined?'':String(provided),locked:field.locked,source:source(field.source)};}
 for(const [name,values] of Object.entries(doc.lists??{})){const spec=FIELD_SPECS[name];if(!spec)continue;schema.push({field:name,bulk:false,...spec});
  fields[name]={value:(values.values??[]).join(', '),automaticValue:'',locked:values.locked,source:source(values.source),values:values.values??[]};}
 const credits=doc.credits,roles=ROLES_BY_KIND[doc.kind]??[];
 const relationships:RepairRelationship[]=(credits?.values??[]).map((c,i)=>({kind:'credit',provider:'manual',recordId:String(i),targetKind:'person',targetId:c.personId??'',label:c.name??'',role:c.character??'',department:c.role,ordinal:i,source:source(credits!.source),locked:credits!.locked}));
 const chosen:RepairChoice[]=[],artwork:RepairData['artwork']['candidates'][number][]=[];
 for(const role of roles)for(const [rank,p] of (pictures[role]??[]).entries()){
  if(p.selected)chosen.push({role,subject:'',candidateId:p.id,digest:'',locked:p.locked,revision:0,attribution:'',url:p.url});
  artwork.push({id:p.id,role,subject:'',provider:p.source,imageId:p.id,locale:p.language??'',rank,attribution:'',observedAt:'',current:p.selected,previewUrl:p.url});}
 return Object.freeze({target:Object.freeze({kind:target.kind,id:target.id}),itemKind:doc.kind,canMakeEpisodeThumbnail:doc.kind==='episode',libraryId:target.libraryId,serverId:scope.serverId,viewerFence:'',revision:doc.revision,
  schema:Object.freeze(schema),artworkRoles:Object.freeze([...roles]),
  snapshot:Object.freeze({canEditProviderCredits:true as const,fields:Object.freeze(fields),
   relationshipLocks:Object.freeze({identity:doc.match?.locked===true,...(credits?{credit:credits.locked}:{})}),
   identity:{provider,id:ids[provider]??'',status:doc.match?.state??'unmatched',locked:doc.match?.locked===true,revision:0,...(doc.match?.episodeOrder?{order:doc.match.episodeOrder}:{}),...(doc.match?.episodeOffset?{offset:doc.match.episodeOffset}:{})},
   relationships:Object.freeze(relationships),artwork:Object.freeze(chosen)}),
  candidates,history:Object.freeze(history.map(entry=>({id:entry.id,trigger:TRIGGERS[entry.kind]??entry.kind,observedAt:entry.at}))),
  artwork:Object.freeze({candidates:Object.freeze(artwork),jobs:[]}),cascades:[]}) as unknown as RepairData;
}
/** An edit as the rebuilt server takes it: a field sent is set and locked; a lock alone is a lock or an unlock. */
export function repairPatch(fields:NonNullable<RepairCommand['fields']>,data:RepairData):MetadataPatch{
 const patch:Record<string,unknown>={},lock:string[]=[],unlock:string[]=[],useProvider:string[]=[];
 for(const [name,change] of Object.entries(fields)){
  const before=data.snapshot.fields[name],spec=data.schema.find(row=>row.field===name);if(!before||!spec)continue;
  const changed=spec.type==='list'?JSON.stringify(change.values??before.values??[])!==JSON.stringify(before.values??[]):(change.value??before.value)!==before.value;
  // "Use it": the provider's own value, unlocked, is taken by name; the server sets it and its source.
  if(changed&&spec.type!=='list'&&before.automaticValue&&change.value===before.automaticValue&&change.locked===false){useProvider.push(name);continue;}
  if(changed)patch[name]=spec.type==='list'?[...(change.values??[])]:spec.type==='integer'?Number(change.value||0):change.value??'';
  if(name==='metadataLanguage')continue; // A language override follows the library again when cleared; it has no lock.
  if(change.locked===false&&(changed||before.locked))unlock.push(name);else if(change.locked===true&&!changed&&!before.locked)lock.push(name);
 }
 return {...patch,...(lock.length?{lock}:{}),...(unlock.length?{unlock}:{}),...(useProvider.length?{useProvider}:{})};
}
/** A typed search that is an id or an address goes to the provider as one (rule 14). */
const isRef=(text:string)=>/^(https?:\/\/|tt\d+$|(tmdb|tvdb|anilist)[:=-]\d+$|\d+$)/i.test(text.trim());
export class MetadataRepairService {
 private state:RepairView=Object.freeze({phase:'idle',data:null,error:null,preview:null});
 private listeners=new Set<()=>void>();private epoch=0;private controller?:AbortController;private target?:RepairTarget;private disposed=false;
 private candidates:RepairData['candidates']=Object.freeze([]);
 private noDecoder=false;
 private options:{api:Api;scope:RepairScope;timeoutMs?:number};
 constructor(options:{api:Api;scope:RepairScope;timeoutMs?:number}){if(!id(options.scope.serverId)||!options.scope.viewerId)throw Error('A concrete viewer scope is required.');if(options.timeoutMs!==undefined&&(!num(options.timeoutMs)||options.timeoutMs<1||options.timeoutMs>60000))throw Error('Invalid deadline.');this.options={...options,scope:Object.freeze({...options.scope})};}
 getSnapshot=()=>this.state;
 subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>{this.listeners.delete(fn);};};
 private publish(state:RepairView){if(this.disposed)return;this.state=Object.freeze(state);for(const fn of this.listeners)fn();}
 cancel(){this.epoch++;this.controller?.abort();this.target=undefined;this.candidates=Object.freeze([]);this.publish({phase:'idle',data:null,error:null,preview:null});}
 dispose(){this.cancel();this.disposed=true;this.listeners.clear();}
 /** One step at a time: a newer one aborts the one before; a caller that has gone away aborts its own; each has a deadline (a lookup asks a provider). */
 private async run<T>(work:(signal:AbortSignal)=>Promise<T>,caller?:RepairCaller):Promise<T>{
  if(caller&&(caller.signal.aborted||!caller.current()))throw Error('Repair caller retired.');
  const c=new AbortController();this.controller?.abort();this.controller=c;
  const retire=()=>c.abort();caller?.signal.addEventListener('abort',retire,{once:true});
  const timer=setTimeout(()=>c.abort(),this.options.timeoutMs??30000);
  let rejectAbort!:(reason:Error)=>void;const cancelled=new Promise<never>((_,reject)=>{rejectAbort=reject;});void cancelled.catch(()=>{});
  const abort=()=>rejectAbort(new Error('Repair request interrupted.'));c.signal.addEventListener('abort',abort,{once:true});
  try{return await Promise.race([work(c.signal),cancelled]);}
  finally{clearTimeout(timer);c.signal.removeEventListener('abort',abort);caller?.signal.removeEventListener('abort',retire);}
 }
 private async read(target:RepairTarget,signal:AbortSignal):Promise<RepairData>{
  const api=this.options.api,item=target.id;
  const doc=await callServer(api,'GET /v1/admin/items/{id}/metadata',{id:item,signal});
  if(!obj(doc)||!str(doc.revision,64)||!obj(doc.fields))bad();
  const roles=ROLES_BY_KIND[doc.kind]??[];
  const [pages,history]=await Promise.all([Promise.all(roles.map(async role=>[role,(await callServer(api,'GET /v1/admin/items/{id}/artwork',{id:item,query:{role:wireRole(role)},signal})).items??[]] as const)),
   callServer(api,'GET /v1/admin/items/{id}/metadata/history',{id:item,signal})]);
  const data=rebuiltRepair(doc,Object.fromEntries(pages),history.items??[],this.candidates,this.options.scope,target);
  return this.noDecoder?Object.freeze({...data,canMakeEpisodeThumbnail:false}):data;
 }
 async load(target:RepairTarget){
  if(this.disposed)throw Error('Repair view is closed.');if(!id(target.id)||!['item','show','season','album','artist','book'].includes(target.kind))throw Error('Select a concrete entity.');
  if(target.id!==this.target?.id)this.candidates=Object.freeze([]);
  this.target=Object.freeze({...target});const epoch=++this.epoch;this.publish({phase:'loading',data:null,error:null,preview:null});
  try{const data=await this.run(signal=>this.read(target,signal));if(epoch!==this.epoch||this.disposed)return;this.publish({phase:'ready',data,error:null,preview:null});}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,false);}
 }
 async refresh(expectedData:RepairData):Promise<void>{
  const target=this.target;if(this.disposed||!target||this.state.phase!=='ready'||this.state.data!==expectedData)return;
  const epoch=++this.epoch;
  try{const data=await this.run(signal=>this.read(target,signal));if(epoch!==this.epoch||this.disposed||this.target!==target||this.state.data!==expectedData)return;this.publish({phase:'ready',data,error:null,preview:null});}
  catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,false);}
 }
 /** An edit names the revision the editor shows; one made over a newer change is refused and changes nothing (rule 25). */
 private async edit(item:string,revision:string,patch:MetadataPatch,signal:AbortSignal){
  const raw=this.options.api.requestRaw;if(!raw)throw Object.assign(new Error(unreadableServerResponse),{code:'unsupported'});
  const answer=await raw(serverPath('PATCH /v1/admin/items/{id}/metadata',{id:item}),'PATCH',{headers:{'If-Match':`"${revision}"`},body:patch,signal});
  if(answer.status>=400){const e=obj(answer.body)&&obj(answer.body.error)?answer.body.error:{};throw Object.assign(new Error(typeof e.message==='string'?e.message:unreadableServerResponse),{status:answer.status,code:typeof e.code==='string'?e.code:'request_failed',...(Array.isArray(e.fields)?{fields:e.fields}:{})});}
 }
 /** The server's own step for each editor command. A command the rebuilt server has no route for is refused before anything is sent. */
 private async send(command:RepairCommand,data:RepairData,signal:AbortSignal):Promise<void>{
  const api=this.options.api,item=data.target.id;
  switch(command.action){
   case 'edit':return this.edit(item,data.revision,repairPatch(command.fields??{},data),signal);
   case 'lock_all':case 'unlock_all':return this.edit(item,data.revision,command.action==='lock_all'?{lockAll:true}:{unlockAll:true},signal);
   case 'relationship_lock':{const field=command.role==='identity'?'match':'credits';return this.edit(item,data.revision,command.locked?{lock:[field]}:{unlock:[field]},signal);}
   case 'edit_relationships':return this.edit(item,data.revision,{credits:(command.relationships??[]).map((row):MetadataCredit=>({...(row.targetId?{personId:row.targetId}:{}),name:row.label.trim(),role:(row.department??'').trim().toLowerCase(),...(row.role?.trim()?{character:row.role.trim()}:{})}))},signal);
   case 'identify':await callServer(api,'PUT /v1/admin/items/{id}/match',{id:item,body:{provider:command.provider,id:command.candidateId,...(command.order?{episodeOrder:command.order}:{}),...(command.episodeOffset?{episodeOffset:command.episodeOffset}:{})},signal});return;
   case 'keep_as_is':await callServer(api,'PUT /v1/admin/items/{id}/match',{id:item,body:{keepAsIs:true},signal});return;
   case 'retry':await callServer(api,'POST /v1/admin/items/{id}/metadata/refresh',{id:item,signal});return;
   case 'search':{const text=(command.query??'').trim();
    const found=await callServer(api,'GET /v1/admin/items/{id}/matches',{id:item,query:isRef(text)?{ref:text}:{q:text||undefined,year:command.year},signal});
    this.candidates=Object.freeze((found.items??[]).map(c=>Object.freeze({id:c.id,provider:c.provider,title:c.title,subtitle:c.originalTitle&&c.originalTitle!==c.title?c.originalTitle:'',observedAt:'',...(c.year?{year:c.year}:{})})));return;}
   case 'undo':await callServer(api,'POST /v1/admin/items/{id}/metadata/history/{entryId}/undo',{id:item,ids:{entryId:String(command.historyId)},signal});return;
   case 'select_artwork':case 'artwork_lock':{const role=command.role??'';const current=data.snapshot.artwork.find(choice=>choice.role===role);
    const body=command.action==='select_artwork'?{imageId:command.candidateId}:command.locked&&current?{imageId:current.candidateId}:{unlock:true};
    await callServer(api,'PUT /v1/admin/items/{id}/artwork/{role}',{id:item,ids:{role:wireRole(role)},body,signal});return;}
   case 'discover_artwork':await callServer(api,'POST /v1/admin/items/{id}/artwork/find',{id:item,query:{role:wireRole(command.role??'')},signal});return;
   case 'refetch_artwork':await callServer(api,'POST /v1/admin/items/{id}/artwork/refetch',{id:item,query:{role:wireRole(command.role??'')},signal});return;
   case 'make_episode_thumbnail':await callServer(api,'POST /v1/admin/items/{id}/artwork/make',{id:item,signal});return;
   default:throw Object.assign(new Error('This server has no such metadata action.'),{code:'unsupported'});
  }
 }
 async command(command:RepairCommand,expectedData?:RepairData,caller?:RepairCaller):Promise<boolean>{
  const callerCurrent=()=>!caller||!caller.signal.aborted&&caller.current();
  if(!callerCurrent())return false;
  const data=this.state.data,target=this.target;if(this.state.phase!=='ready'||!data||!target||expectedData!==undefined&&expectedData!==data)throw Error('Load current metadata before making another change.');
  return this.write(target,data,signal=>this.send(command,data,signal),command.action!=='search',caller);
 }
 /** A write, then the title read again so every tab shows what the server now holds. */
 private async write(target:RepairTarget,data:RepairData,work:(signal:AbortSignal)=>Promise<void>,write:boolean,caller?:RepairCaller):Promise<boolean>{
  const epoch=++this.epoch;this.publish({...this.state,phase:'saving',error:null});
  const attempt=this.getSnapshot();
  if(this.disposed||epoch!==this.epoch||this.target!==target||attempt.phase!=='saving'||attempt.data!==data)return false;
  try{
   const next=await this.run(async signal=>{await work(signal);return this.read(target,signal);},caller);
   if(epoch!==this.epoch||this.disposed)return false;
   if(caller&&(caller.signal.aborted||!caller.current())){this.failure(Error('Repair caller retired after submission.'),write);return false;}
   this.publish({phase:'ready',data:next,error:null,preview:null});return true;
  }catch(error){if(epoch===this.epoch&&!this.disposed)this.failure(error,write);return false;}
 }
 async uploadArtwork(role:string,file:Blob,_subject=''):Promise<boolean>{
  const data=this.state.data,target=this.target,upload=this.options.api.requestMetadataUpload;
  if(this.state.phase!=='ready'||!data||!target||!upload)throw Error('Load current metadata before uploading artwork.');
  if(!data.artworkRoles.includes(role)||file.size<1||file.size>10*1024*1024||!['image/jpeg','image/png'].includes(file.type))throw Error('Choose a JPG or PNG up to 10 MiB.');
  const path=serverPath('POST /v1/admin/items/{id}/artwork',{id:target.id,query:{role:wireRole(role)}});
  return this.write(target,data,async signal=>{await upload(path,file,signal);},true);
 }
 async removeUploadedArtwork(role:string,candidateId:string,expectedData?:RepairData):Promise<boolean>{
  const data=this.state.data,target=this.target;
  if(this.state.phase!=='ready'||!data||!target)throw Error('Load current metadata before removing artwork.');
  if(expectedData&&data!==expectedData)throw Error('The artwork confirmation belongs to an old document.');
  if(!data.artwork.candidates.some(candidate=>candidate.id===candidateId&&candidate.role===role&&candidate.provider==='upload'))throw Error('Select an uploaded image.');
  return this.write(target,data,async signal=>{await callServer(this.options.api,'DELETE /v1/admin/items/{id}/artwork/{imageId}',{id:target.id,ids:{imageId:candidateId},signal});},true);
 }
 private failure(error:unknown,write:boolean){const e=obj(error)?error:{};
  const status=typeof e.status==='number'?e.status:undefined,code=typeof e.code==='string'?e.code:'';
  if(code==='no_decoder'&&this.state.data){this.noDecoder=true;this.publish({...this.state,phase:'ready',data:Object.freeze({...this.state.data,canMakeEpisodeThumbnail:false}),preview:null,error:Object.freeze({code,status})});return;}
  // Refused before anything was stored: the editor stays ready with the reason and its values (a field over its limit, a picture the server can't keep, a match that needs another kind).
  const refused=write&&(status===400||status===415||status===422)||['unsupported','match_locked','no_provider','unknown_ref','wrong_kind','not_a_show','not_matched','provider_unavailable','file_unavailable','not_from_provider','not_an_episode'].includes(code);
  if(refused&&this.state.data){this.publish({...this.state,phase:'ready',preview:null,error:Object.freeze({code,...(status!==undefined?{status}:{})})});return;}
  const denied=status===401||status===403;const conflict=status===409||status===412||code==='stale';
  const reason:RepairError=denied?{code:'permission_changed',status:403}:conflict?{code:'metadata_conflict',status:409}:write?{code:'unconfirmed_change'}:{code:code||'unreadable',...(status!==undefined?{status}:{})};
  this.publish({phase:denied?'denied':conflict?'conflict':write?'uncertain':'error',data:denied?null:this.state.data,preview:null,error:Object.freeze(reason)});
 }
}
