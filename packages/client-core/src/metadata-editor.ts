import {encodedRoutePath} from "../../contracts/src/route-paths.ts";
import {unreadableServerResponse} from './server-messages.ts';
import {oneOf} from './tolerant.ts';
/** Owner text edits are explicit CAS commands; interrupted saves are never replayed. */
export type MetadataEditorTarget=Readonly<{libraryId:string;itemId:string}>;
export type MetadataEditorScope=Readonly<{serverId:string;viewerId:string}>;
export type MetadataEditCommitted=Readonly<{scope:MetadataEditorScope;target:MetadataEditorTarget;revision:string}>;
type Api={request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>};
export type MetadataField=Readonly<{value:string;automaticValue:string;manual:boolean}>;
export type MetadataEditProjection=Readonly<{scope:Readonly<{serverId:string;libraryId:string;itemId:string;viewerFence:string}>;revision:string;kind?:'movie'|'episode'|'song'|'audiobook_file';title:MetadataField;description:MetadataField;updatedAt?:string}>;
export type MetadataEditPatch=Readonly<{title?:string|null;description?:string|null}>;
type ErrorInfo=Readonly<{code:string;message:string}>;
export type MetadataEditorSnapshot=Readonly<{phase:'idle'|'loading'|'ready'|'saving'|'conflict'|'uncertain'|'error'|'denied';data:MetadataEditProjection|null;error:ErrorInfo|null}>;
const object=(v:unknown):v is Record<string,unknown>=>v!==null&&typeof v==='object'&&!Array.isArray(v);
const id=(v:unknown):v is string=>typeof v==='string'&&v.length>0&&v.length<=256&&!/[\x00-\x1f\x7f-\x9f]/.test(v);
const hash=(v:unknown):v is string=>typeof v==='string'&&/^[a-f0-9]{64}$/.test(v);
const text=(v:unknown,max:number,multiline=false):v is string=>typeof v==='string'&&[...v].length<=max&&!(multiline?/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f-\x9f]/:/[\x00-\x1f\x7f-\x9f]/).test(v);
const sourceText=(v:unknown,title:boolean):v is string=>typeof v==='string'&&v.length<=(title?2048:65536)&&!(title?/[\x00-\x1f\x7f]/:/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/).test(v);
function invalid():never{throw Object.assign(new Error(unreadableServerResponse),{code:'invalid_metadata_edit'});}
export function validMetadataPatch(patch:unknown):patch is MetadataEditPatch{
 return object(patch)&&Object.keys(patch).length>0&&Object.keys(patch).every(k=>['title','description'].includes(k)&&patch[k]!==undefined)&&
 (patch.title===undefined||patch.title===null||text(patch.title,300)&&Boolean(patch.title.trim()))&&
 (patch.description===undefined||patch.description===null||text(patch.description,20000,true));
}
const editKinds=['movie','episode','song','audiobook_file'] as const;
/** The scope, the viewer fence and the revision stay exact; a kind this app does not know and an
 * optional date it cannot read are left off rather than refusing the whole projection. */
export function validateMetadataEdit(v:unknown,scope:MetadataEditorScope,target:MetadataEditorTarget):MetadataEditProjection{
 if(!object(v)||!object(v.scope)||v.scope.serverId!==scope.serverId||v.scope.libraryId!==target.libraryId||v.scope.itemId!==target.itemId||!hash(v.scope.viewerFence)||!hash(v.revision))invalid();
 const kind=oneOf(v.kind,editKinds);
 const updatedAt=typeof v.updatedAt==='string'&&v.updatedAt.length<=64&&Number.isFinite(Date.parse(v.updatedAt))?v.updatedAt:undefined;
 const field=(raw:unknown,title:boolean):MetadataField=>{if(!object(raw)||!sourceText(raw.value,title)||!sourceText(raw.automaticValue,title)||typeof raw.manual!=='boolean'||!raw.manual&&raw.value!==raw.automaticValue)invalid();return Object.freeze({value:raw.value,automaticValue:raw.automaticValue,manual:raw.manual});};
 return Object.freeze({scope:Object.freeze({serverId:scope.serverId,libraryId:target.libraryId,itemId:target.itemId,viewerFence:v.scope.viewerFence}),revision:v.revision,...(kind===undefined?{}:{kind}),title:field(v.title,true),description:field(v.description,false),...(updatedAt?{updatedAt}:{})});
}
type Options={api:Api;scope:MetadataEditorScope;timeoutMs?:number};
export class MetadataEditorService{
 private state:MetadataEditorSnapshot=Object.freeze({phase:'idle',data:null,error:null});private listeners=new Set<()=>void>();private epoch=0;private controller?:AbortController;private target?:MetadataEditorTarget;private fence?:string;private disposed=false;private options:Options;
 constructor(options:Options){if(!id(options.scope.serverId)||!options.scope.viewerId)throw Error('A bound owner scope is required.');if(options.timeoutMs!==undefined&&(!Number.isFinite(options.timeoutMs)||options.timeoutMs<=0||options.timeoutMs>30000))throw Error('Invalid metadata deadline.');this.options={...options,scope:Object.freeze({...options.scope})};}
 getSnapshot=()=>this.state;subscribe=(fn:()=>void)=>{this.listeners.add(fn);return()=>this.listeners.delete(fn)};
 private publish(next:MetadataEditorSnapshot){if(this.disposed)return;this.state=Object.freeze(next);for(const fn of this.listeners)fn();}
 cancel(){this.epoch++;this.controller?.abort();this.target=undefined;this.fence=undefined;this.publish({phase:'idle',data:null,error:null});}
 dispose(){this.cancel();this.disposed=true;this.listeners.clear();}
 private async request(target:MetadataEditorTarget,method:string,body:unknown,c:AbortController):Promise<unknown>{
  let timer:ReturnType<typeof setTimeout>|undefined;let rejectAbort!:(reason:Error)=>void;
  const cancelled=new Promise<never>((_,reject)=>{rejectAbort=reject;});
  const abort=()=>rejectAbort(Object.assign(new Error('Metadata request ended.'),{code:'metadata_interrupted'}));
  c.signal.addEventListener('abort',abort,{once:true});
  timer=setTimeout(()=>c.abort(),this.options.timeoutMs??15000);
  try{if(c.signal.aborted)abort();const result=await Promise.race([this.options.api.request(encodedRoutePath("get_items_id_metadata_manual", {"id": encodeURIComponent(target.itemId)}),method,body,c.signal),cancelled]);if(c.signal.aborted)throw Object.assign(new Error('Metadata request ended.'),{code:'metadata_interrupted'});return result;}
  finally{clearTimeout(timer);c.signal.removeEventListener('abort',abort);}
 }
 async load(target:MetadataEditorTarget){
  if(this.disposed)throw Error('Metadata editor is closed.');if(!id(target.libraryId)||!id(target.itemId))throw Error('Select a concrete library item.');
  this.controller?.abort();const epoch=++this.epoch,c=new AbortController();this.controller=c;
  if(this.target?.itemId!==target.itemId||this.target?.libraryId!==target.libraryId)this.fence=undefined;
  this.target=Object.freeze({...target});this.publish({phase:'loading',data:null,error:null});
  try{const raw=await this.request(target,'GET',undefined,c);if(this.disposed||epoch!==this.epoch||c.signal.aborted)return;const data=validateMetadataEdit(raw,this.options.scope,target);if(this.fence&&data.scope.viewerFence!==this.fence)throw Object.assign(new Error('Owner access changed.'),{code:'permission_changed'});this.fence=data.scope.viewerFence;this.publish({phase:'ready',data,error:null});}
  catch(e){if(!this.disposed&&epoch===this.epoch)this.failure(e,false);}
 }
 async save(patch:MetadataEditPatch):Promise<boolean>{
  const data=this.state.data,target=this.target;if(this.state.phase!=='ready'||!data||!target)throw Error('Load the current values before saving.');if(!validMetadataPatch(patch))throw Error('Check the title and description.');
  const epoch=++this.epoch,c=new AbortController();this.controller=c;this.publish({phase:'saving',data,error:null});
  try{const raw=await this.request(target,'PATCH',{expectedRevision:data.revision,...patch},c);if(this.disposed||epoch!==this.epoch||c.signal.aborted)return false;const saved=validateMetadataEdit(raw,this.options.scope,target);if(saved.scope.viewerFence!==this.fence)throw Object.assign(new Error('Owner access changed.'),{code:'permission_changed'});for(const key of ['title','description'] as const){if(patch[key]===null&&saved[key].manual||typeof patch[key]==='string'&&(!saved[key].manual||saved[key].value!==patch[key]))invalid();}this.publish({phase:'ready',data:saved,error:null});return true;}
  catch(e){if(!this.disposed&&epoch===this.epoch)this.failure(e,true);return false;}
 }
 private failure(e:unknown,write:boolean){
  const v=object(e)?e:{};const denied=v.status===401||v.status===403||['unauthorized','forbidden','permission_changed'].includes(String(v.code));const conflict=v.code==='manual_metadata_conflict'||v.status===409;
  const code=denied?'permission_changed':conflict?'manual_metadata_conflict':v.code==='invalid_metadata_edit'?'invalid_metadata_edit':'metadata_interrupted';
  const message=denied?'Owner access is no longer available. Close this editor and sign in again.':conflict?'These values changed. Your draft has not been saved. Load the latest values to review them.':write?'The save response was interrupted. Check the saved values before making another change.':code==='invalid_metadata_edit'?unreadableServerResponse:'Could not load editable metadata. Try again.';
  this.publish({phase:denied?'denied':conflict?'conflict':write?'uncertain':'error',data:denied||!write?null:this.state.data,error:Object.freeze({code,message})});
 }
}
