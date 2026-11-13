import {callServer, serverPath} from '@core/query/server-api.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LiveSource, LiveSourceSave} from '../../../../../packages/contracts/src/next/api.ts';
import {rebaseDraft, type QuietRefreshOutcome} from '@core/freshness-actions.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
export type SourceSettingsDraft = Pick<LiveSource,'name'|'audience'|'tunerCount'|'paused'|'config'> & {username:string;password:string;playlist?:string;guide?:string};
type Document = Readonly<{source:LiveSource;etag:string;imagesEnabled:boolean}>;
export type SourceSettingsSnapshot=Readonly<{loading:boolean;saving:boolean;reading?:boolean;document?:Document;draft?:SourceSettingsDraft;error?:unknown;saved?:boolean;uncertain?:boolean;logoBusy?:boolean;logoResult?:Readonly<{requested:number;imported:number;skipped:number}>}>;
const from=(source:LiveSource):SourceSettingsDraft=>({name:source.name,audience:source.audience,tunerCount:source.tunerCount,paused:source.paused,config:{...source.config},username:'',password:''});
export class SourceSettingsController extends OwnerStore<SourceSettingsSnapshot> {
 private quietSerial=0;private quietRead?:AbortController;private uncertain=false;
 constructor(private api:LibraryContentApi,_serverId:string,readonly sourceId:string,current:()=>boolean,reportError:(error:unknown)=>void,_makeId?:()=>string){super({loading:false,saving:false},current,reportError);}
 quietBlocked=()=>this.snapshot.loading||this.snapshot.saving||!!this.snapshot.logoBusy||!!this.snapshot.reading||this.uncertain;
 invalidateQuiet=()=>{++this.quietSerial;this.quietRead?.abort();this.quietRead=undefined;};
 private async request(method:'GET'|'PATCH',signal?:AbortSignal,body?:LiveSourceSave,etag?:string):Promise<Document>{
  if(!this.api.requestRaw)throw {code:'invalid_response'};
  const answer=await this.api.requestRaw(serverPath('GET /v1/admin/live/sources/{id}',{id:this.sourceId}),method,{signal,body,...(etag?{headers:{'If-Match':etag},singleAttempt:true}:{})});
  if(answer.status>=400){const error=(answer.body as {error?:{code?:string;message?:string}})?.error;throw {status:answer.status,code:error?.code};}
  const source=answer.body as LiveSource,revision=answer.headers.etag??answer.headers.ETag;
  if(!source||source.id!==this.sourceId||!source.config||typeof source.name!=='string'||!/^"[0-9]+"$/.test(revision??''))throw {code:'invalid_response'};
  let imagesEnabled=this.snapshot.document?.imagesEnabled??true;
  if(method==='GET'){if(!this.isActive()||signal?.aborted)throw {code:'cancelled'};const settings=await callServer(this.api,'GET /v1/admin/settings/{name}',{id:'live',signal});imagesEnabled=(settings.values as {importImages?:boolean}).importImages!==false;}
  return {source,etag:revision!,imagesEnabled};
 }
 async load(){if(!this.isActive()||this.snapshot.saving||this.snapshot.logoBusy)return;this.invalidateQuiet();const attempt=this.begin();this.publish({...this.snapshot,loading:true,error:undefined});if(!this.valid(attempt))return;
  try{const document=await this.request('GET',attempt.abort.signal);if(this.valid(attempt)){this.uncertain=false;this.publish({loading:false,saving:false,document,draft:from(document.source)});}}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 refreshQuietly=async():Promise<QuietRefreshOutcome>=>{
  if(!this.isActive()||this.quietBlocked())return {kind:'deferred'};
  this.invalidateQuiet();const serial=this.quietSerial,read=this.quietRead=new AbortController(),base=this.snapshot.document;
  const owns=()=>this.isActive()&&!read.signal.aborted&&serial===this.quietSerial&&!this.quietBlocked()&&this.snapshot.document===base;
  try{const document=await this.request('GET',read.signal);if(!owns())return {kind:'deferred'};const local=this.snapshot.draft;
   if(base&&local&&document.etag!==base.etag&&(local.username||local.password||local.playlist||local.guide))return {kind:'conflict'};
   const merged=base&&local?rebaseDraft(from(base.source),local,from(document.source)):{merged:from(document.source),conflict:false};
   if(merged.conflict)return {kind:'conflict'};this.publish({...this.snapshot,document,draft:merged.merged});return {kind:'adopted'};
  }catch(error){if(!owns())return {kind:'deferred'};if([401,403].includes(errorFacts(error).status??0))this.fail(error,this.snapshot);return {kind:'unreadable',error};}
  finally{if(this.quietRead===read)this.quietRead=undefined;}
 };
 edit(patch:Partial<SourceSettingsDraft>){if(this.isActive()&&!this.snapshot.saving&&!this.snapshot.logoBusy&&!this.snapshot.reading&&this.snapshot.draft&&!this.uncertain)this.publish({...this.snapshot,draft:{...this.snapshot.draft,...patch},error:undefined,saved:false});}
 discard(){if(this.isActive()&&!this.quietBlocked()&&this.snapshot.document)this.publish({...this.snapshot,draft:from(this.snapshot.document.source),error:undefined});}
 async upload(field:'playlist'|'guide',read:()=>Promise<string>){
  if(!this.isActive()||this.quietBlocked()||!this.snapshot.draft)return;
  this.invalidateQuiet();const attempt=this.begin();this.publish({...this.snapshot,reading:true,error:undefined});if(!this.valid(attempt))return;
  try{const text=await read();if(!this.valid(attempt))return;
   if(new TextEncoder().encode(text).length>128*1024*1024)throw {code:'body_too_large',status:413};
   const draft=this.snapshot.draft!;this.publish({...this.snapshot,reading:false,draft:{...draft,[field]:text,config:{...draft.config,...(field==='playlist'?{uploaded:true,playlistUrl:''}:{guideUploaded:true,guideUrl:'',guideSource:''})}},saved:false});
  }catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,reading:false,error:errorFacts(error)});}
 }
 async save(){
  const {document,draft}=this.snapshot;if(!this.isActive()||this.quietBlocked()||!document||!draft||JSON.stringify(from(document.source))===JSON.stringify(draft))return false;
  this.invalidateQuiet();const attempt=this.begin(),base=from(document.source),body:LiveSourceSave={};
  for(const key of ['name','audience','paused','tunerCount','config'] as const)if(JSON.stringify(draft[key])!==JSON.stringify(base[key]))Object.assign(body,{[key]:draft[key]});
  if(draft.username||draft.password)body.signIn={username:draft.username,password:draft.password};if(draft.playlist)body.playlist=draft.playlist;if(draft.guide)body.guide=draft.guide;
  if(new TextEncoder().encode(JSON.stringify(body)).length>128*1024*1024){this.publish({...this.snapshot,error:{code:'body_too_large',status:413}});return false;}
  this.publish({...this.snapshot,saving:true,error:undefined});if(!this.valid(attempt))return false;
  try{const saved=await this.request('PATCH',attempt.abort.signal,body,document.etag);if(!this.valid(attempt))return false;this.publish({loading:false,saving:false,document:saved,draft:from(saved.source),saved:true});return true;}
  catch(error){if(!this.valid(attempt))return false;const facts=errorFacts(error);this.uncertain=![400,401,403,409,412,422].includes(facts.status??0);this.fail(error,{...this.snapshot,saving:false,error:facts,uncertain:this.uncertain});
   if(this.valid(attempt)&&(this.uncertain||facts.status===412)){try{const latest=await this.request('GET',attempt.abort.signal);if(this.valid(attempt)){this.uncertain=false;this.publish({...this.snapshot,document:latest,draft:from(latest.source),error:facts,uncertain:false});}}catch{/* Keep the original refusal. */}}
   return false;
  }
 }
 async importLogos(replaceExisting=false){if(!this.isActive()||this.quietBlocked()||!this.snapshot.document||JSON.stringify(this.snapshot.draft)!==JSON.stringify(from(this.snapshot.document.source)))return;
  this.invalidateQuiet();const attempt=this.begin();this.publish({...this.snapshot,logoBusy:true,error:undefined,logoResult:undefined});if(!this.valid(attempt))return;
  try{const logoResult=await callServer(this.api,'POST /v1/admin/live/sources/{id}/logos',{id:this.sourceId,body:{replaceExisting},signal:attempt.abort.signal});if(this.valid(attempt))this.publish({...this.snapshot,logoBusy:false,logoResult});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,logoBusy:false,error:errorFacts(error)});}
 }
 override dispose(){this.invalidateQuiet();super.dispose();}
}
