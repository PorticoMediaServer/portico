import type {LibraryContentApi} from '@core/library-content.ts';
import {readSourceChoices} from '@core/server-admin/live-source-settings.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
type ChoiceSnapshot=Readonly<{loading:boolean;query:string;page?:Readonly<{items:readonly {id:string;name:string}[];nextCursor?:string}>;error?:unknown}>;
export class SourceChoicesController extends OwnerStore<ChoiceSnapshot>{
 constructor(private api:LibraryContentApi,private sourceId:string,readonly kind:'groups'|'channels',current:()=>boolean,reportError:(error:unknown)=>void){super({loading:false,query:''},current,reportError);}
 async load(query='',more=false){if(!this.isActive()||more&&this.snapshot.loading)return;const head=this.snapshot.page,cursor=more?head?.nextCursor:undefined;if(more&&(!cursor||query!==this.snapshot.query))return;
  const attempt=this.begin();this.publish({loading:true,query,...(more?{page:head}:{})});if(!this.valid(attempt))return;
  try{
   const page=await readSourceChoices(this.api,this.sourceId,this.kind,query,cursor,attempt.abort.signal);
   if(!this.valid(attempt))return;const items=more&&head?[...head.items,...page.items.filter(row=>!head.items.some(prior=>prior.id===row.id))]:page.items;
   this.publish({loading:false,query,page:{...page,items}});
  }catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
}
