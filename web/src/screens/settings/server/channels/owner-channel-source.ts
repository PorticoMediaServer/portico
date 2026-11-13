import type {LibraryContentApi} from '@core/library-content.ts';
import {rebaseDraft,type QuietRefreshOutcome} from '@core/freshness-actions.ts';
import * as wire from '@core/server-admin/owner-library-channels.ts';
import type {LibraryChannelPreview,LibraryChannelTemplateChoices} from '../../../../../../packages/contracts/src/next/api.ts';
import {OwnerStore,errorFacts} from '../../../live/owner/lifetime';
export type Channel=wire.LibraryChannel;
export type ChannelDraft=Required<Pick<wire.LibraryChannelSave,'name'|'description'|'timeZone'|'on'|'builder'>>;
export type Editor=Readonly<{key:string;document?:Channel;draft:ChannelDraft;templateName?:string;conflict?:boolean}>;
export type PresetEditor=Readonly<{key:string;document?:wire.LibraryChannelBlockPreset;conflict?:boolean;draft:Required<wire.LibraryChannelBlockPresetSave>}>;
type State=Readonly<{loading:boolean;busy:boolean;channels:readonly Channel[];cursor?:string;choices?:LibraryChannelTemplateChoices;editor?:Editor;health?:Channel;error?:unknown;preview?:LibraryChannelPreview;previewFor?:ChannelDraft;previewLoading:boolean;pendingCreate?:boolean;uncertain?:boolean;presets:readonly wire.LibraryChannelBlockPreset[];presetCursor?:string;presetEditor?:PresetEditor;pendingPreset?:boolean}>;
const empty:State={loading:false,busy:false,channels:[],previewLoading:false,presets:[]};
const clone=<T,>(v:T):T=>structuredClone(v);
const from=(row:Channel):ChannelDraft=>({name:row.name,description:row.description??'',timeZone:row.timeZone,on:row.on,builder:clone(row.builder!)});
export class OwnerChannelSource extends OwnerStore<State>{
 private previewRead?:AbortController;private quietRead?:AbortController;private quietSerial=0;
 private createIntent?:{key:string;body:ChannelDraft};private presetIntent?:{key:string;body:Required<wire.LibraryChannelBlockPresetSave>};
 constructor(readonly api:LibraryContentApi,current:()=>boolean,reportError:(error:unknown)=>void){super(empty,current,reportError);}
 quietBlocked=()=>this.snapshot.loading||this.snapshot.busy||!!this.snapshot.editor||!!this.snapshot.presetEditor||!!this.createIntent||!!this.presetIntent||!!this.snapshot.uncertain;
 invalidateQuiet=()=>{++this.quietSerial;this.quietRead?.abort();this.quietRead=undefined;};
 private stopPreview(){this.previewRead?.abort();this.previewRead=undefined;}
 async load(more=false,preserveError?:unknown){if(!this.isActive()||this.snapshot.busy||this.snapshot.loading||this.snapshot.editor||this.snapshot.presetEditor||more&&!this.snapshot.cursor)return;
  this.invalidateQuiet();const attempt=this.begin(),head=this.snapshot;this.publish({...head,loading:true,error:preserveError});if(!this.valid(attempt))return;
  try{const page=await wire.readOwnerChannels(this.api,more?head.cursor:undefined,attempt.abort.signal);if(!this.valid(attempt))return;const choices=await wire.readChannelChoices(this.api,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,loading:false,channels:more?[...head.channels,...page.items.filter(row=>!head.channels.some(old=>old.id===row.id))]:page.items,cursor:page.nextCursor,choices,error:preserveError,uncertain:false});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 refreshQuietly=async():Promise<QuietRefreshOutcome>=>{
  if(!this.isActive()||this.quietBlocked())return {kind:'deferred'};this.invalidateQuiet();const read=this.quietRead=new AbortController(),serial=this.quietSerial,head=this.snapshot;
  const owns=()=>this.isActive()&&!read.signal.aborted&&serial===this.quietSerial&&!this.quietBlocked()&&head.channels===this.snapshot.channels;
  try{const rows:Channel[]=[],seen=new Set<string>();let cursor:string|undefined;do{if(!owns())return {kind:'deferred'};const page=await wire.readOwnerChannels(this.api,cursor,read.signal);rows.push(...page.items);cursor=page.nextCursor;if(cursor&&seen.has(cursor))throw {code:'invalid_response'};if(cursor)seen.add(cursor);}while(cursor&&rows.length<head.channels.length);
   if(!owns())return {kind:'deferred'};const choices=await wire.readChannelChoices(this.api,read.signal);if(!owns())return {kind:'deferred'};let health=head.health;if(health){try{health=await wire.readOwnerChannel(this.api,health.id,read.signal);}catch(error){if((error as {status?:number}).status===404)health=undefined;else throw error;}}if(!owns())return {kind:'deferred'};this.publish({...this.snapshot,channels:rows,cursor,health,choices});return {kind:'adopted'};
  }catch(error){if(!owns())return {kind:'deferred'};if([401,403].includes(errorFacts(error).status??0))this.fail(error,this.snapshot);return {kind:'unreadable',error};}
  finally{if(this.quietRead===read)this.quietRead=undefined;}
 };
 blank(template?:LibraryChannelTemplateChoices['items'][number]){if(!this.isActive()||this.snapshot.busy||this.snapshot.uncertain||this.snapshot.presetEditor||this.presetIntent||this.snapshot.editor||this.createIntent||!this.snapshot.choices||template&&!template.available)return;
  this.invalidateQuiet();const draft:ChannelDraft={name:template?.name??'',description:template?.description??'',timeZone:this.snapshot.choices.timeZone,on:true,builder:clone(template?.builder??{libraries:[],kinds:['movie','show'],quality:{},logoOnScreen:{}})};
  this.publish({...this.snapshot,editor:{key:wire.newChannelOperation(),draft,templateName:template?.name},error:undefined,preview:undefined});
 }
 async open(row:Channel){if(!this.snapshot.channels.includes(row))return;await this.openId(row.id);}
 async openId(id:string){if(!this.isActive()||this.snapshot.busy||this.snapshot.uncertain||this.snapshot.editor||this.snapshot.presetEditor||this.createIntent||this.presetIntent)return;this.invalidateQuiet();const attempt=this.begin();this.publish({...this.snapshot,busy:true,error:undefined});if(!this.valid(attempt))return;
  try{const document=await wire.readOwnerChannel(this.api,id,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,busy:false,editor:{key:wire.newChannelOperation(),document,draft:from(document)},preview:undefined});}
  catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,busy:false,error:errorFacts(error)});}
 }
 edit(expected:Editor,change:(draft:ChannelDraft)=>ChannelDraft){if(!this.isActive()||this.snapshot.editor!==expected||this.snapshot.busy||this.snapshot.uncertain||this.createIntent||this.presetIntent)return;this.stopPreview();this.publish({...this.snapshot,editor:{...expected,draft:change(expected.draft)},previewLoading:false,error:undefined});}
 close(expected:Editor){if(!this.isActive()||this.snapshot.busy||this.snapshot.editor!==expected)return;this.stopPreview();this.publish({...this.snapshot,editor:undefined,preview:undefined,previewLoading:false});}
 resume(){if(this.isActive()&&this.createIntent&&!this.snapshot.busy)this.publish({...this.snapshot,editor:{key:this.createIntent.key,draft:this.createIntent.body},pendingCreate:true});}
 async preview(expected:Editor){if(!this.isActive()||this.snapshot.editor!==expected||this.snapshot.busy||this.createIntent)return;this.stopPreview();const read=this.previewRead=new AbortController();this.publish({...this.snapshot,previewLoading:true});
  const owns=()=>this.isActive()&&!read.signal.aborted&&this.previewRead===read&&this.snapshot.editor===expected;
  try{if(!owns())return;const result=await wire.previewOwnerChannel(this.api,expected.draft,expected.document?.id,read.signal);if(owns())this.publish({...this.snapshot,preview:result,previewFor:expected.draft,previewLoading:false});}
  catch(error){if(owns())this.fail(error,{...this.snapshot,previewLoading:false,error:errorFacts(error)});}
 }
 async save(expected:Editor){
  if(!this.isActive()||this.snapshot.editor!==expected||this.snapshot.busy||expected.conflict||this.snapshot.uncertain||this.presetIntent)return;
  this.invalidateQuiet();this.stopPreview();const attempt=this.begin();this.publish({...this.snapshot,busy:true,previewLoading:false,error:undefined});if(!this.valid(attempt))return;
  const original=expected.document?from(expected.document):undefined;
  const body=original?Object.fromEntries(Object.entries(expected.draft).filter(([key,value])=>JSON.stringify(value)!==JSON.stringify(original[key as keyof ChannelDraft]))):expected.draft;
  if(!expected.document&&!this.createIntent)this.createIntent={key:expected.key,body:clone(expected.draft)};
  try{const saved=await wire.saveOwnerChannel(this.api,this.createIntent?.body??body,expected.document,this.createIntent?.key,attempt.abort.signal);if(!this.valid(attempt))return;this.createIntent=undefined;this.publish({...this.snapshot,busy:false,editor:undefined,pendingCreate:false,channels:[...this.snapshot.channels.filter(row=>row.id!==saved.id),saved].sort((a,b)=>a.number-b.number),preview:undefined});}
  catch(error){if(!this.valid(attempt))return;const facts=errorFacts(error);
   if(!expected.document&&![400,401,403,409,422].includes(facts.status??0)){this.fail(error,{...this.snapshot,busy:false,pendingCreate:true,error:facts});return;}
   this.createIntent=undefined;this.fail(error,{...this.snapshot,busy:false,error:facts});if(!this.valid(attempt)||!expected.document)return;
   if(facts.status===412||![400,401,403,404,409,422].includes(facts.status??0)){
    this.publish({...this.snapshot,uncertain:true});if(!this.valid(attempt))return;
    try{const fresh=await wire.readOwnerChannel(this.api,expected.document.id,attempt.abort.signal);if(this.valid(attempt)){const merged=rebaseDraft(original!,expected.draft,from(fresh));this.publish({...this.snapshot,editor:{...expected,document:fresh,draft:merged.merged,conflict:merged.conflict},uncertain:false,error:facts});}}catch(readError){if(this.valid(attempt))this.fail(readError,this.snapshot);}
   }
  }
 }
 acceptLocal(expected:Editor){if(this.isActive()&&!this.snapshot.busy&&this.snapshot.editor===expected)this.publish({...this.snapshot,editor:{...expected,conflict:false}});}
 async reloadEditor(expected:Editor){if(!this.isActive()||this.snapshot.editor!==expected||this.snapshot.busy||!expected.document)return;const attempt=this.begin();this.publish({...this.snapshot,busy:true});if(!this.valid(attempt))return;
  try{const document=await wire.readOwnerChannel(this.api,expected.document.id,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,busy:false,uncertain:false,editor:{...expected,document,draft:from(document),conflict:false},error:undefined});}catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,busy:false,error:errorFacts(error)});}
 }
 async moveTo(row:Channel,target:number){if(!Number.isInteger(target)||target<0)return;await this.action(row,'move',target);}
 async action(row:Channel,kind:'toggle'|'remove'|'regenerate'|'up'|'down'|'move',target?:number){
  if(!this.isActive()||this.snapshot.busy||this.snapshot.editor||this.snapshot.presetEditor||this.createIntent||this.presetIntent||this.snapshot.uncertain||!this.snapshot.channels.includes(row))return;this.invalidateQuiet();const attempt=this.begin();this.publish({...this.snapshot,busy:true,error:undefined});if(!this.valid(attempt))return;
  try{if(kind==='toggle')await wire.saveOwnerChannel(this.api,{on:!row.on},row,undefined,attempt.abort.signal);
   else if(kind==='remove')await wire.removeOwnerChannel(this.api,row,attempt.abort.signal);
   else if(kind==='regenerate')await wire.regenerateOwnerChannel(this.api,row,attempt.abort.signal);
   else{const all:Channel[]=[],seen=new Set<string>();let cursor:string|undefined;do{if(!this.valid(attempt))return;const page=await wire.readOwnerChannels(this.api,cursor,attempt.abort.signal);all.push(...page.items);cursor=page.nextCursor;if(cursor&&seen.has(cursor))throw {code:'invalid_response'};if(cursor)seen.add(cursor);}while(cursor);
    const index=all.findIndex(value=>value.id===row.id&&value.revision===row.revision),to=kind==='move'?target!:index+(kind==='up'?-1:1);if(index<0)throw {status:412,code:'changed_elsewhere'};if(to>=0&&to<all.length){const [moved]=all.splice(index,1);all.splice(to,0,moved!);if(!this.valid(attempt))return;await wire.orderOwnerChannels(this.api,all.map(value=>value.id),attempt.abort.signal);}}
   if(this.valid(attempt)){this.publish({...this.snapshot,busy:false});await this.load();}
  }catch(error){if(this.valid(attempt)){const facts=errorFacts(error);this.fail(error,{...this.snapshot,busy:false,error:facts});if(this.valid(attempt))await this.load(false,facts);}}
 }
 async health(row:Channel){if(!this.isActive()||this.snapshot.busy||!this.snapshot.channels.includes(row))return;const attempt=this.begin();try{const health=await wire.readOwnerChannel(this.api,row.id,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,health});}catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,error:errorFacts(error)});}}
 closeHealth(){if(this.isActive())this.publish({...this.snapshot,health:undefined});}
 async logo(row:Channel,upload:(signal:AbortSignal)=>Promise<Channel>){if(!this.isActive()||this.snapshot.busy||!this.snapshot.channels.includes(row))return;this.invalidateQuiet();const attempt=this.begin();this.publish({...this.snapshot,busy:true,error:undefined});if(!this.valid(attempt))return;
  try{await upload(attempt.abort.signal);if(this.valid(attempt)){this.publish({...this.snapshot,busy:false});await this.load();}}catch(error){if(this.valid(attempt)){const facts=errorFacts(error);this.fail(error,{...this.snapshot,busy:false,error:facts});if(this.valid(attempt))await this.load(false,facts);}}
 }
 async presets(more=false){if(!this.isActive()||this.snapshot.busy||more&&!this.snapshot.presetCursor)return;const attempt=this.begin();this.publish({...this.snapshot,busy:true,error:undefined});if(!this.valid(attempt))return;
  try{const page=await wire.readOwnerBlockPresets(this.api,more?this.snapshot.presetCursor:undefined,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,busy:false,presets:more?[...this.snapshot.presets,...page.items]:page.items,presetCursor:page.nextCursor,uncertain:false});}catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,busy:false,error:errorFacts(error)});}
 }
 presetResume(){if(this.isActive()&&!this.snapshot.busy&&this.presetIntent)this.publish({...this.snapshot,presetEditor:{key:this.presetIntent.key,draft:this.presetIntent.body},pendingPreset:true});}
 presetOpen(row?:wire.LibraryChannelBlockPreset){if(!this.isActive()||this.snapshot.busy||this.snapshot.uncertain||this.snapshot.presetEditor||this.presetIntent||this.createIntent||row&&!this.snapshot.presets.includes(row))return;const draft:PresetEditor['draft']=row?{name:row.name,description:row.description??'',library:row.library,recipe:clone(row.recipe)}:{name:'',description:'',library:'',recipe:{clauses:[],order:'shuffle',minutes:120,days:['mon','tue','wed','thu','fri','sat','sun']}};this.publish({...this.snapshot,presetEditor:{key:wire.newChannelOperation(),document:row,draft},error:undefined});}
 presetEdit(expected:PresetEditor,draft:PresetEditor['draft']){if(this.isActive()&&!this.snapshot.busy&&!this.snapshot.uncertain&&!this.presetIntent&&this.snapshot.presetEditor===expected)this.publish({...this.snapshot,presetEditor:{...expected,draft},error:undefined});}
 async presetReload(expected:PresetEditor){if(!this.isActive()||this.snapshot.busy||this.snapshot.presetEditor!==expected||!expected.document)return;
 const attempt=this.begin();this.publish({...this.snapshot,busy:true});if(!this.valid(attempt))return;
 try{const document=await wire.readOwnerBlockPreset(this.api,expected.document.id,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,busy:false,uncertain:false,presetEditor:{...expected,document,conflict:false,draft:{name:document.name,description:document.description??'',library:document.library,recipe:document.recipe}},error:undefined});}catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,busy:false,error:errorFacts(error)});}
 }
 presetClose(expected:PresetEditor){if(this.isActive()&&!this.snapshot.busy&&this.snapshot.presetEditor===expected)this.publish({...this.snapshot,presetEditor:undefined});}
 presetAccept(expected:PresetEditor){if(this.isActive()&&!this.snapshot.busy&&this.snapshot.presetEditor===expected)this.publish({...this.snapshot,presetEditor:{...expected,conflict:false}});}
 async presetSave(expected:PresetEditor){if(!this.isActive()||this.snapshot.busy||this.snapshot.uncertain||expected.conflict||this.createIntent||this.snapshot.presetEditor!==expected)return;const attempt=this.begin();this.publish({...this.snapshot,busy:true,error:undefined});if(!this.valid(attempt))return;
  if(!expected.document&&!this.presetIntent)this.presetIntent={key:expected.key,body:clone(expected.draft)};
  try{const saved=await wire.saveOwnerBlockPreset(this.api,this.presetIntent?.body??expected.draft,expected.document,this.presetIntent?.key,attempt.abort.signal);if(this.valid(attempt)){this.presetIntent=undefined;this.publish({...this.snapshot,busy:false,presetEditor:undefined,pendingPreset:false,presets:[saved,...this.snapshot.presets.filter(row=>row.id!==saved.id)]});}}
  catch(error){if(!this.valid(attempt))return;const facts=errorFacts(error);if(!expected.document&&![400,401,403,409,422].includes(facts.status??0)){this.fail(error,{...this.snapshot,busy:false,pendingPreset:true,error:facts});return;}this.presetIntent=undefined;this.fail(error,{...this.snapshot,busy:false,error:facts});
 if(this.valid(attempt)&&expected.document&&(facts.status===412||![400,401,403,404,409,422].includes(facts.status??0))){
  this.publish({...this.snapshot,uncertain:true});if(!this.valid(attempt))return;
  try{const document=await wire.readOwnerBlockPreset(this.api,expected.document.id,attempt.abort.signal);if(this.valid(attempt)){const original={name:expected.document.name,description:expected.document.description??'',library:expected.document.library,recipe:expected.document.recipe},fresh={name:document.name,description:document.description??'',library:document.library,recipe:document.recipe},merged=rebaseDraft(original,expected.draft,fresh);this.publish({...this.snapshot,uncertain:false,presetEditor:{...expected,document,draft:merged.merged,conflict:merged.conflict},error:facts});}}catch(readError){if(this.valid(attempt))this.fail(readError,this.snapshot);}
 }}
 }
 async presetRemove(row:wire.LibraryChannelBlockPreset){if(!this.isActive()||this.snapshot.busy||this.snapshot.uncertain||this.snapshot.presetEditor||!this.snapshot.presets.includes(row))return;const attempt=this.begin();this.publish({...this.snapshot,busy:true,error:undefined});if(!this.valid(attempt))return;
  try{await wire.removeOwnerBlockPreset(this.api,row,attempt.abort.signal);if(this.valid(attempt)){this.publish({...this.snapshot,busy:false});await this.presets();}}catch(error){if(!this.valid(attempt))return;const facts=errorFacts(error);this.fail(error,{...this.snapshot,busy:false,error:facts,uncertain:true});if(!this.valid(attempt))return;
   try{const document=await wire.readOwnerBlockPreset(this.api,row.id,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,uncertain:false,presets:this.snapshot.presets.map(item=>item.id===row.id?document:item),error:facts});}
   catch(readError){if(this.valid(attempt)&&(readError as {status?:number}).status===404)this.publish({...this.snapshot,uncertain:false,presets:this.snapshot.presets.filter(item=>item.id!==row.id),error:facts});else if(this.valid(attempt))this.fail(readError,this.snapshot);}
  }
 }
 override dispose(){this.invalidateQuiet();this.stopPreview();this.createIntent=undefined;this.presetIntent=undefined;super.dispose();}
}
