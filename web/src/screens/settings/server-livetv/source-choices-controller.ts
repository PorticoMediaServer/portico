import type {LibraryContentApi} from '@core/library-content.ts';
import {callServer} from '@core/query/server-api.ts';
import {OwnerStore, errorFacts} from '../../live/owner/lifetime';
type ChoiceSnapshot=Readonly<{loading:boolean;query:string;page?:Readonly<{items:readonly {id:string;name:string}[];nextCursor?:string}>;error?:unknown}>;
export class SourceChoicesController extends OwnerStore<ChoiceSnapshot>{
 constructor(private api:LibraryContentApi,private sourceId:string,readonly kind:'groups'|'channels',current:()=>boolean,reportError:(error:unknown)=>void){super({loading:false,query:''},current,reportError);}
 async load(query='',more=false){if(!this.isActive()||more&&this.snapshot.loading)return;const head=this.snapshot.page,cursor=more?head?.nextCursor:undefined;if(more&&(!cursor||query!==this.snapshot.query))return;
  const attempt=this.begin();this.publish({loading:true,query,...(more?{page:head}:{})});if(!this.valid(attempt))return;
  try{
   let page:NonNullable<ChoiceSnapshot['page']>;
   if(this.kind==='groups'){const source=await callServer(this.api,'GET /v1/admin/live/sources/{id}',{id:this.sourceId,signal:attempt.abort.signal});if(source.id!==this.sourceId)throw {code:'invalid_response'};page={items:(source.report?.groups??[]).filter(name=>name.toLocaleLowerCase().includes(query.toLocaleLowerCase())).map(name=>({id:name,name}))};}
   else{const answer=await callServer(this.api,'GET /v1/admin/live/sources/{id}/channels',{id:this.sourceId,query:{q:query,cursor},signal:attempt.abort.signal});if(answer.items.some(row=>typeof row.sourceKey!=='string'||typeof row.name!=='string'))throw {code:'invalid_response'};page={items:answer.items.map(row=>({id:row.sourceKey,name:row.name})),nextCursor:answer.nextCursor};}
   if(!this.valid(attempt))return;const items=more&&head?[...head.items,...page.items.filter(row=>!head.items.some(prior=>prior.id===row.id))]:page.items;
   this.publish({loading:false,query,page:{...page,items}});
  }catch(error){if(this.valid(attempt))this.fail(error,{...this.snapshot,loading:false,error:errorFacts(error)});}
 }
}
