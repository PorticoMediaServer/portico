import {routePath} from "../../contracts/src/route-paths.ts";
import {parseMediaItem} from './media-item.ts';
import {unreadableServerResponse} from './server-messages.ts';
import type {Library,MediaItem} from './index.ts';
export interface CatalogApi {request<T>(path:string,method?:string,body?:unknown,signal?:AbortSignal):Promise<T>;}
/** History keeps at most this many page cursors, each a bounded opaque token. */
const historyLimit=64;
const continuation=(v:unknown):v is string|undefined=>v===undefined||typeof v==='string'&&v.length<=4096&&!/[\x00-\x1f\x7f]/.test(v);
export type CatalogSnapshot={generation:number;libraryId:string;libraries:Library[];items:MediaItem[];nextCursor?:string;canPrevious:boolean;pending:boolean;error?:string};
/** One bounded page, one request generation. Transport cancellation is an optimization, never the stale-result fence. */
export class CatalogService {
 private state:CatalogSnapshot={generation:0,libraryId:'',libraries:[],items:[],canPrevious:false,pending:false};
 private listeners=new Set<()=>void>();private controller?:AbortController;private cursors=[''];private cursorIndex=0;private disposed=false;
 private api:CatalogApi;private timeoutMs:number;
 constructor(api:CatalogApi,timeoutMs=15000){this.api=api;this.timeoutMs=timeoutMs;}
 getSnapshot=()=>this.state;
 subscribe=(listener:()=>void)=>{this.listeners.add(listener);return()=>{this.listeners.delete(listener);};};
 private publish(patch:Partial<CatalogSnapshot>){if(this.disposed)return;this.state=Object.freeze({...this.state,...patch});for(const listener of this.listeners)listener();}
 select(libraryId=''){this.cursors=[''];this.cursorIndex=0;return this.fetchPage(libraryId,'',true);}
 refresh(){return this.fetchPage(this.state.libraryId,this.cursors[this.cursorIndex]??'',false);}
 next(){if(this.state.pending||!this.state.nextCursor)return Promise.resolve();this.cursors=this.cursors.slice(0,this.cursorIndex+1);this.cursors.push(this.state.nextCursor);if(this.cursors.length>historyLimit)this.cursors.shift();this.cursorIndex=this.cursors.length-1;return this.fetchPage(this.state.libraryId,this.cursors[this.cursorIndex],false);}
 previous(){if(this.state.pending||this.cursorIndex===0)return Promise.resolve();this.cursorIndex--;return this.fetchPage(this.state.libraryId,this.cursors[this.cursorIndex],false);}
 dispose(){this.disposed=true;this.controller?.abort();this.listeners.clear();}
 private async fetchPage(libraryId:string,cursor:string,clear:boolean){
  this.controller?.abort();const controller=new AbortController();this.controller=controller;const generation=this.state.generation+1;
  this.publish({generation,libraryId,pending:true,error:undefined,...(clear?{items:[],nextCursor:undefined}:{}),canPrevious:this.cursorIndex>0});
  const query=new URLSearchParams({limit:'40'});if(libraryId)query.set('libraryId',libraryId);if(cursor)query.set('cursor',cursor);
  let timer:ReturnType<typeof setTimeout>|undefined;
  try{
   const result=await Promise.race([Promise.all([this.api.request<{items:Library[]}>(routePath("get_libraries"),'GET',undefined,controller.signal),this.api.request<{items:MediaItem[];nextCursor?:string}>(routePath("get_items") + "?" + (query),'GET',undefined,controller.signal)]),new Promise<never>((_,reject)=>{timer=setTimeout(()=>{controller.abort();reject(new Error('The server took too long to respond. Try again.'));},this.timeoutMs);})]);
   if(this.disposed||generation!==this.state.generation)return;
   const [libraries,page]=result;if(!Array.isArray(libraries.items)||!Array.isArray(page.items)||page.items.length>40||!continuation(page.nextCursor))throw new Error(unreadableServerResponse);
   this.publish({libraries:libraries.items,items:[...new Map(page.items.map(parseMediaItem).map(item=>[item.id,item])).values()],nextCursor:page.nextCursor,pending:false});
  }catch(error){if(!this.disposed&&generation===this.state.generation)this.publish({pending:false,error:error instanceof Error?error.message:'Could not load the library.'});}
  finally{if(timer)clearTimeout(timer);}
 }
}
