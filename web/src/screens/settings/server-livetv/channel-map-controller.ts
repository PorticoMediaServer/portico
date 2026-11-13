import {callServer} from '@core/query/server-api.ts';
import type {LibraryContentApi} from '@core/library-content.ts';
import type {LiveSourceChannel, LiveSourceChannelEdit} from '../../../../../packages/contracts/src/next/api.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
export const validChannelNumber=(number:string)=>/^[0-9]{1,5}(\.[0-9]{1,3})?$/.test(number);
export type ChannelOverride = Readonly<{number: string; guideSource: string; guideKey: string; hidden: boolean}>;
type MapSnapshot = Readonly<{loading: boolean; saving: boolean; rows: readonly LiveSourceChannel[]; guides: readonly {id: string; name: string}[]; edits: Readonly<Record<string, ChannelOverride>>; cursor: string; query: string; error?: unknown; partial?: {saved: number; total: number}}>;
const empty: MapSnapshot = Object.freeze({loading: false, saving: false, rows: [], guides: [], edits: {}, cursor: '', query: ''});
/** Targeted edits on the rebuilt source map. No old generations, receipts or synthetic revisions. */
export class ChannelMapController extends OwnerStore<MapSnapshot> {
 constructor(private api: LibraryContentApi, _serverId: string, readonly sourceId: string, current: () => boolean, reportError: (error: unknown) => void) {super(empty,current,reportError);}
 private async page(query: string, signal: AbortSignal, cursor = '') {
  const page=await callServer(this.api,'GET /v1/admin/live/sources/{id}/channels',{id:this.sourceId,query:{q:query,cursor},signal});
  if(!Array.isArray(page.items)||page.items.some(row=>typeof row.id!=='string'||typeof row.name!=='string'||typeof row.number!=='string'||typeof row.hidden!=='boolean'))throw {code:'invalid_response'};
  return page;
 }
 async load(more=false, query=this.snapshot.query) {
  if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||more&&!this.snapshot.cursor)return;
  // Keep a draft on its original search; saving or re-opening settles it first.
  if(query!==this.snapshot.query&&Object.keys(this.snapshot.edits).length)return;
  const head=this.snapshot,attempt=this.begin();this.publish({...head,loading:true,error:undefined});if(!this.valid(attempt))return;
  try {
   const page=await this.page(query,attempt.abort.signal,more?head.cursor:'');if(!this.valid(attempt))return;
   const guides=await callServer(this.api,'GET /v1/admin/live/sources',{signal:attempt.abort.signal});if(!this.valid(attempt))return;
   this.publish({...this.snapshot,loading:false,query,rows:more?[...head.rows,...page.items.filter(row=>!head.rows.some(prior=>prior.id===row.id))]:page.items,guides:guides.items.map(({id,name})=>({id,name})),cursor:page.nextCursor??''});
  }catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
 value(row:LiveSourceChannel):ChannelOverride{return this.snapshot.edits[row.id]??{number:row.number,guideSource:row.guideSource??'',guideKey:row.guideKey??'',hidden:row.hidden};}
 edit(row:LiveSourceChannel,patch:Partial<ChannelOverride>){
  if(!this.isActive()||this.snapshot.saving||!this.snapshot.rows.includes(row))return;
  const value={...this.value(row),...patch};if(!/^[0-9.]{0,12}$/.test(value.number)||value.guideKey.length>200||/[\x00-\x1f\x7f]/.test(value.guideKey))return;
  const original={number:row.number,guideSource:row.guideSource??'',guideKey:row.guideKey??'',hidden:row.hidden},edits={...this.snapshot.edits};
  if(JSON.stringify(value)===JSON.stringify(original))delete edits[row.id];else edits[row.id]=value;
  this.publish({...this.snapshot,edits,error:undefined,partial:undefined});
 }
 async save(){
  if(!this.isActive()||this.snapshot.loading||this.snapshot.saving||!Object.keys(this.snapshot.edits).length)return false;
  if(Object.values(this.snapshot.edits).some(value=>!validChannelNumber(value.number)))return false;
  const head=this.snapshot,attempt=this.begin(),changes:LiveSourceChannelEdit[]=Object.entries(head.edits).map(([id,value])=>{
   const row=head.rows.find(row=>row.id===id)!;
   return {id,...(value.number!==row.number?{number:value.number}:{}),...(value.hidden!==row.hidden?{hidden:value.hidden}:{}),...(value.guideSource!==(row.guideSource??'')||value.guideKey!==(row.guideKey??'')?{guideSource:value.guideSource,guideKey:value.guideKey}:{})};
  });
  this.publish({...head,saving:true,error:undefined});let saved=0;
  try {
   for(let offset=0;offset<changes.length;offset+=1000){if(!this.valid(attempt))return false;const channels=changes.slice(offset,offset+1000);
    await callServer(this.api,'PATCH /v1/admin/live/sources/{id}/channels',{id:this.sourceId,body:{channels},signal:attempt.abort.signal});if(!this.valid(attempt))return false;
    const edits={...this.snapshot.edits};for(const row of channels)delete edits[row.id];saved+=channels.length;this.publish({...this.snapshot,edits,partial:{saved,total:changes.length}});
   }
   const page=await this.page(head.query,attempt.abort.signal);if(!this.valid(attempt))return false;
   this.publish({...this.snapshot,saving:false,rows:page.items,cursor:page.nextCursor??''});return true;
  }catch(error){
   if(!this.valid(attempt))return false;
   const facts=errorFacts(error);this.fail(error,{...this.snapshot,saving:false,error:facts,partial:{saved,total:changes.length}});
   // A missing answer may have applied. Read back instead of replaying an old edit.
   if(this.valid(attempt)&&![400,401,403,409,412,422].includes(facts.status??0)){
    try {const page=await this.page(head.query,attempt.abort.signal);if(this.valid(attempt))this.publish({...this.snapshot,rows:page.items,edits:{},cursor:page.nextCursor??'',error:facts});}catch{/* Keep the failed draft if the server cannot be read. */}
   }
   return false;
  }
 }
}
