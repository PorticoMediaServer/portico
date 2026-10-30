import {DetailService,type DetailApi,type DetailScope,type PersonalIntent} from './detail.ts';
import {validateContentEntry,type ContentEntry} from './library-content.ts';
export type SelectionAction='watchlist'|'favorite'|'watched';
export type SelectionResult=Readonly<{key:string;title:string;status:'waiting'|'working'|'saved'|'failed'|'conflict'|'unavailable';message?:string}>;
export type MediaSelectionSnapshot=Readonly<{active:boolean;busy:boolean;entries:readonly ContentEntry[];selected:readonly string[];results:readonly SelectionResult[];canApply:boolean}>;
export function mediaSelectionKey(entry:ContentEntry):string{return JSON.stringify([entry.libraryId??'',entry.kind,entry.id]);}
/** Aggregate catalog entries must not silently stand in for their first playable item. */
export function selectionItem(entry:ContentEntry):{libraryId:string;itemId:string}|null{
 if(!entry.libraryId||!['movie','episode','song','audiobook_file'].includes(entry.kind))return null;
 return {libraryId:entry.libraryId,itemId:entry.navigation?.view==='item'?entry.navigation.entityId??entry.id:entry.playback?.itemId??entry.id};
}
/** Ephemeral, principal-bound selection of visible catalog entries. Each write retains the existing detail service's revision and receipt guarantees. */
export class MediaSelectionService {
 private entries:ContentEntry[]=[];private selected=new Set<string>();private results:SelectionResult[]=[];
 private active=false;private busy=false;private disposed=false;private generation=0;private anchor:string|null=null;
 private listeners=new Set<()=>void>();private workers=new Map<string,DetailService>();private intent?:PersonalIntent;
 private snapshot!:MediaSelectionSnapshot;
 private options:{api:DetailApi;scope:DetailScope;requestId:()=>Promise<string>;timeoutMs?:number};
 constructor(options:{api:DetailApi;scope:DetailScope;requestId:()=>Promise<string>;timeoutMs?:number}){this.options=options;this.publish();}
 getSnapshot=()=>this.snapshot;
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private publish(){if(this.disposed)return;this.snapshot=Object.freeze({active:this.active,busy:this.busy,entries:Object.freeze([...this.entries]),selected:Object.freeze([...this.selected]),results:Object.freeze([...this.results]),canApply:this.selected.size>0&&this.entries.filter(e=>this.selected.has(mediaSelectionKey(e))).every(e=>selectionItem(e)!==null)});for(const listener of this.listeners)listener();}
 setEntries(entries:readonly ContentEntry[]){if(this.disposed)return;if(this.busy||this.results.some(r=>['failed','conflict'].includes(r.status)))return;const unique=new Map<string,ContentEntry>();for(const input of entries){const e=validateContentEntry(input);unique.set(mediaSelectionKey(e),e);}this.entries=[...unique.values()];this.selected=new Set([...this.selected].filter(k=>unique.has(k)));if(this.anchor&&!unique.has(this.anchor))this.anchor=null;this.publish();}
 begin(){if(this.disposed)return;this.active=true;this.publish();}
 toggle(key:string,range=false){if(this.busy||this.results.length||this.disposed)return;const index=this.entries.findIndex(e=>mediaSelectionKey(e)===key);if(index<0)return;this.active=true;const from=range&&this.anchor?this.entries.findIndex(e=>mediaSelectionKey(e)===this.anchor):-1;
 if(from>=0){for(const e of this.entries.slice(Math.min(from,index),Math.max(from,index)+1))this.selected.add(mediaSelectionKey(e));}
 else if(this.selected.has(key))this.selected.delete(key);else this.selected.add(key);
 this.anchor=key;this.publish();}
 selectLoaded(){if(this.busy||this.results.length||this.disposed)return;this.active=true;this.selected=new Set(this.entries.map(mediaSelectionKey));this.publish();}
 /** Explicitly finish or abandon unresolved results; no request is retried by clearing selection. */
 clear(){if(this.busy||this.disposed)return;this.generation++;for(const w of this.workers.values())w.dispose();this.workers.clear();this.selected.clear();this.results=[];this.active=false;this.intent=undefined;this.anchor=null;this.publish();}
 async apply(action:SelectionAction,value:boolean){if(this.busy||this.results.length||!this.snapshot.canApply||this.disposed)return;this.intent={action,value};this.results=this.entries.filter(e=>this.selected.has(mediaSelectionKey(e))).map(e=>({key:mediaSelectionKey(e),title:e.title,status:'waiting'}));await this.run(false);}
 /** Retries lost responses with the same operation ID. Conflicts require separate explicit approval. */
 async retry(includeConflicts=false){if(this.busy||this.disposed||!this.intent)return;await this.run(true,includeConflicts);}
 private async run(retry:boolean,includeConflicts=false){const generation=this.generation;this.busy=true;this.publish();
 try{for(const result of [...this.results]){
  if(this.disposed||generation!==this.generation)return;
  if(retry?!['failed',...(includeConflicts?['conflict']:[])].includes(result.status):result.status!=='waiting')continue;
  const e=this.entries.find(e=>mediaSelectionKey(e)===result.key),target=e&&selectionItem(e);if(!target)continue;
  const update=(status:SelectionResult['status'],message?:string)=>{this.results=this.results.map(r=>r.key===result.key?{...r,status,...(message?{message}:{message:undefined})}:r);this.publish();};
  update('working');let worker=this.workers.get(result.key);
  if(!worker){worker=new DetailService(this.options);this.workers.set(result.key,worker);}
  try{
   if(!worker.getSnapshot().target)await worker.select(target);
   else if(!worker.getSnapshot().pending.length)await worker.retryRead();
   if(this.disposed||generation!==this.generation)return;
   const current=worker.getSnapshot();
   if(current.pending.length)await worker.retryMutation();
   else if(current.phase==='error')throw new Error(current.error?.message??'Couldn’t load this item.');
   else if(!current.data?.actions.some(a=>a.id===this.intent!.action&&a.enabled)){update('unavailable','This action is no longer available for this item.');continue;}
   else await worker.mutate(this.intent!);
   if(this.disposed||generation!==this.generation)return;
   const after=worker.getSnapshot();
   if(after.pending.length)update(after.pending[0].phase==='conflict'?'conflict':'failed',after.mutationError?.message??'The result could not be confirmed. Retry to check it.');
   else update('saved');
  }catch(error){if(this.disposed||generation!==this.generation)return;update('failed',error instanceof Error?error.message:'Couldn’t update this item.');}
 }}finally{if(!this.disposed&&generation===this.generation){this.busy=false;this.publish();}}}
 dispose(){this.generation++;for(const w of this.workers.values())w.dispose();this.workers.clear();this.disposed=true;this.entries=[];this.selected.clear();this.results=[];this.listeners.clear();}
}
